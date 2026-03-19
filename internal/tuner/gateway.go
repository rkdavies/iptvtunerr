package tuner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/snapetech/iptvtunerr/internal/catalog"
	"github.com/snapetech/iptvtunerr/internal/httpclient"
	"github.com/snapetech/iptvtunerr/internal/safeurl"
)

// errCFBlock is returned by fetchAndWriteSegment when FetchCFReject is true and a segment
// is redirected to the Cloudflare abuse page (cloudflare-terms-of-service-abuse.com).
// The HLS relay loop treats this as a fatal error that aborts the entire stream immediately.
var errCFBlock = errors.New("cloudflare-abuse-block")

// Gateway proxies live stream requests to provider URLs with optional auth.
// Limit concurrent streams to TunerCount (tuner semantics).
type Gateway struct {
	Channels             []catalog.LiveChannel
	ProviderUser         string
	ProviderPass         string
	TunerCount           int
	StreamBufferBytes    int    // 0 = no buffer, -1 = auto
	StreamTranscodeMode  string // "off" | "on" | "auto"
	TranscodeOverrides   map[string]bool
	DefaultProfile       string
	ProfileOverrides     map[string]string
	Client               *http.Client
	FetchCFReject        bool // abort HLS stream on segment redirected to CF abuse page
	PlexPMSURL           string
	PlexPMSToken         string
	PlexClientAdapt      bool
	Autopilot            *autopilotStore
	mu                   sync.Mutex
	inUse                int
	learnedUpstreamLimit int
	reqSeq               uint64
	providerStateMu      sync.Mutex
	concurrencyHits      int
	lastConcurrencyAt    time.Time
	lastConcurrencyBody  string
	lastConcurrencyCode  int
	cfBlockHits          int
	lastCFBlockAt        time.Time
	lastCFBlockURL       string
	hlsPlaylistFailures  int
	lastHLSPlaylistAt    time.Time
	lastHLSPlaylistURL   string
	hlsSegmentFailures   int
	lastHLSSegmentAt     time.Time
	lastHLSSegmentURL    string
}

type gatewayReqIDKey struct{}

func gatewayReqIDFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(gatewayReqIDKey{}).(string); ok {
		return v
	}
	return ""
}

func gatewayReqIDField(ctx context.Context) string {
	if id := gatewayReqIDFromContext(ctx); id != "" {
		return " req=" + id
	}
	return ""
}

func getenvInt(key string, def int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func getenvFloat(key string, def float64) float64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return def
	}
	return f
}

func getenvBool(key string, def bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return def
	}
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func mpegTSFlagsWithOptionalInitialDiscontinuity() string {
	flags := []string{"resend_headers", "pat_pmt_at_frames"}
	if getenvBool("IPTV_TUNERR_MPEGTS_INITIAL_DISCONTINUITY", true) {
		flags = append(flags, "initial_discontinuity")
	}
	return "+" + strings.Join(flags, "+")
}

func isClientDisconnectWriteError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) ||
		errors.Is(err, io.ErrClosedPipe) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, syscall.EPIPE) ||
		errors.Is(err, syscall.ECONNRESET) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "use of closed network connection")
}

func resolveFFmpegPath() (string, error) {
	if v := strings.TrimSpace(os.Getenv("IPTV_TUNERR_FFMPEG_PATH")); v != "" {
		return exec.LookPath(v)
	}
	return exec.LookPath("ffmpeg")
}

func (g *Gateway) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqID := fmt.Sprintf("r%06d", atomic.AddUint64(&g.reqSeq, 1))
	r = r.WithContext(context.WithValue(r.Context(), gatewayReqIDKey{}, reqID))
	channelID, ok := channelIDFromRequestPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if channelID == "" {
		http.NotFound(w, r)
		return
	}
	var channel *catalog.LiveChannel
	for i := range g.Channels {
		if g.Channels[i].ChannelID == channelID {
			channel = &g.Channels[i]
			break
		}
	}
	if channel == nil {
		// Fallback: numeric index for backwards compatibility when ChannelID is not set
		if idx, err := strconv.Atoi(channelID); err == nil && idx >= 0 && idx < len(g.Channels) {
			channel = &g.Channels[idx]
		}
	}
	if channel == nil {
		// PMS may request /auto/v<GuideNumber> while our stream path uses a
		// non-numeric ChannelID (for example a tvg-id slug). Accept GuideNumber as
		// a fallback lookup for both /auto/ and /stream/ requests.
		for i := range g.Channels {
			if g.Channels[i].GuideNumber == channelID {
				channel = &g.Channels[i]
				break
			}
		}
	}
	if channel == nil {
		http.NotFound(w, r)
		return
	}
	log.Printf("gateway: req=%s recv path=%q channel=%q remote=%q ua=%q", reqID, r.URL.Path, channelID, r.RemoteAddr, r.UserAgent())
	debugOpts := streamDebugOptionsFromEnv()
	if debugOpts.HTTPHeaders {
		for _, line := range debugHeaderLines(r.Header) {
			log.Printf("gateway: req=%s channel=%q id=%s debug-http < %s", reqID, channel.GuideName, channelID, line)
		}
	}
	hasTranscodeOverride, forceTranscode, forcedProfile, adaptReason, clientClass := g.requestAdaptation(r.Context(), r, channel, channelID)
	if adaptReason != "" && adaptReason != "adapt-disabled" {
		if hasTranscodeOverride {
			log.Printf("gateway: channel=%q id=%s adapt transcode=%t profile=%q reason=%s", channel.GuideName, channelID, forceTranscode, forcedProfile, adaptReason)
		} else {
			log.Printf("gateway: channel=%q id=%s adapt inherit profile=%q reason=%s", channel.GuideName, channelID, forcedProfile, adaptReason)
		}
	}
	start := time.Now()
	if debugOpts.enabled() {
		dw := newStreamDebugResponseWriter(w, reqID, channel.GuideName, channelID, start, debugOpts)
		defer dw.Close()
		w = dw
	}
	urls := channel.StreamURLs
	if len(urls) == 0 && channel.StreamURL != "" {
		urls = []string{channel.StreamURL}
	}
	if len(urls) == 0 {
		log.Printf("gateway: req=%s channel=%q id=%s no-stream-url", reqID, channel.GuideName, channelID)
		http.Error(w, "no stream URL", http.StatusBadGateway)
		return
	}

	g.mu.Lock()
	limit := g.effectiveTunerLimitLocked()
	if g.inUse >= limit {
		g.mu.Unlock()
		log.Printf("gateway: req=%s channel=%q id=%s reject all-tuners-in-use limit=%d ua=%q", reqID, channel.GuideName, channelID, limit, r.UserAgent())
		w.Header().Set("X-HDHomeRun-Error", "805") // All Tuners In Use
		http.Error(w, "All tuners in use", http.StatusServiceUnavailable)
		return
	}
	g.inUse++
	inUseNow := g.inUse
	g.mu.Unlock()
	log.Printf("gateway: req=%s channel=%q id=%s acquire inuse=%d/%d", reqID, channel.GuideName, channelID, inUseNow, limit)
	defer func() {
		g.mu.Lock()
		g.inUse--
		inUseLeft := g.inUse
		g.mu.Unlock()
		log.Printf("gateway: req=%s channel=%q id=%s release inuse=%d/%d dur=%s", reqID, channel.GuideName, channelID, inUseLeft, limit, time.Since(start).Round(time.Millisecond))
	}()

	upstreamConcurrencyLimited := false
	// Try primary then backups until one works. Do not retry or backoff on 429/423 here:
	// that would block stream throughput. We only fail over to next URL and return 502 if all fail.
	// Reject non-http(s) URLs to prevent SSRF (e.g. file:// or provider-supplied internal URLs).
	for i, streamURL := range urls {
		if !safeurl.IsHTTPOrHTTPS(streamURL) {
			if i == 0 {
				log.Printf("gateway: channel %s: invalid stream URL scheme (rejected)", channel.GuideName)
			}
			continue
		}
		req, err := g.newUpstreamRequest(r.Context(), r, streamURL)
		if err != nil {
			continue
		}

		client := g.Client
		if client == nil {
			client = httpclient.ForStreaming()
		}
		client = cloneClientWithCookieJar(client)
		resp, err := client.Do(req)
		if err != nil {
			log.Printf("gateway: channel=%q id=%s upstream[%d/%d] error url=%s err=%v",
				channel.GuideName, channelID, i+1, len(urls), safeurl.RedactURL(streamURL), err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			preview := readUpstreamErrorPreview(resp)
			limited := isUpstreamConcurrencyLimit(resp.StatusCode, preview)
			if limited {
				upstreamConcurrencyLimited = true
				g.noteUpstreamConcurrencySignal(resp.StatusCode, preview)
				if learned := g.learnUpstreamConcurrencyLimit(preview); learned > 0 {
					log.Printf("gateway: channel=%q id=%s learned upstream concurrency limit=%d from status=%d body=%q",
						channel.GuideName, channelID, learned, resp.StatusCode, preview)
				}
			}
			switch {
			case resp.StatusCode == http.StatusTooManyRequests:
				log.Printf("gateway: channel=%q id=%s upstream[%d/%d] 429 rate limited url=%s body=%q",
					channel.GuideName, channelID, i+1, len(urls), safeurl.RedactURL(streamURL), preview)
			case limited:
				log.Printf("gateway: channel=%q id=%s upstream[%d/%d] concurrency-limited status=%d url=%s body=%q",
					channel.GuideName, channelID, i+1, len(urls), resp.StatusCode, safeurl.RedactURL(streamURL), preview)
			default:
				log.Printf("gateway: channel=%q id=%s upstream[%d/%d] status=%d url=%s body=%q",
					channel.GuideName, channelID, i+1, len(urls), resp.StatusCode, safeurl.RedactURL(streamURL), preview)
			}
			resp.Body.Close()
			continue
		}
		// Reject 200 with empty body (e.g. Cloudflare/redirect returning 0 bytes) — try next URL (learned from k3s IPTV hardening).
		if resp.ContentLength == 0 {
			log.Printf("gateway: channel=%q id=%s upstream[%d/%d] empty-body url=%s ct=%q",
				channel.GuideName, channelID, i+1, len(urls), safeurl.RedactURL(streamURL), resp.Header.Get("Content-Type"))
			resp.Body.Close()
			continue
		}
		log.Printf("gateway: req=%s channel=%q id=%s start upstream[%d/%d] url=%s ct=%q cl=%d inuse=%d/%d ua=%q",
			reqID, channel.GuideName, channelID, i+1, len(urls), safeurl.RedactURL(streamURL), resp.Header.Get("Content-Type"), resp.ContentLength, inUseNow, limit, r.UserAgent())
		for k, v := range resp.Header {
			if k == "Content-Length" || k == "Transfer-Encoding" {
				continue
			}
			for _, vv := range v {
				w.Header().Add(k, vv)
			}
		}
		if isHLSResponse(resp, streamURL) {
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil {
				log.Printf("gateway: channel=%q id=%s read-playlist-failed err=%v", channel.GuideName, channelID, err)
				continue
			}
			body = rewriteHLSPlaylist(body, streamURL)
			firstSeg := firstHLSMediaLine(body)
			transcode := g.effectiveTranscodeForChannelMeta(r.Context(), channelID, channel.GuideNumber, channel.TVGID, streamURL)
			if hasTranscodeOverride {
				transcode = forceTranscode
			}
			bufferSize := g.effectiveBufferSize(transcode)
			mode := "remux"
			if transcode {
				mode = "transcode"
			}
			bufDesc := strconv.Itoa(bufferSize)
			if bufferSize == -1 {
				bufDesc = "adaptive"
			}
			log.Printf("gateway: channel=%q id=%s hls-playlist bytes=%d first-seg=%q dur=%s (relaying as ts, %s buffer=%s)",
				channel.GuideName, channelID, len(body), firstSeg, time.Since(start).Round(time.Millisecond), mode, bufDesc)
			log.Printf("gateway: channel=%q id=%s hls-mode transcode=%t mode=%q guide=%q tvg=%q", channel.GuideName, channelID, transcode, g.StreamTranscodeMode, channel.GuideNumber, channel.TVGID)
			if ffmpegPath, ffmpegErr := resolveFFmpegPath(); ffmpegErr == nil {
				if err := g.relayHLSWithFFmpeg(w, r, ffmpegPath, streamURL, channel.GuideName, channelID, channel.GuideNumber, channel.TVGID, start, transcode, bufferSize, forcedProfile); err == nil {
					g.rememberAutopilotDecision(channel, clientClass, transcode, effectiveProfileName(g, channel, channelID, forcedProfile), adaptReason)
					return
				} else {
					log.Printf("gateway: channel=%q id=%s ffmpeg-%s failed (falling back to go relay): %v",
						channel.GuideName, channelID, mode, err)
				}
			} else if strings.TrimSpace(os.Getenv("IPTV_TUNERR_FFMPEG_PATH")) != "" {
				log.Printf("gateway: channel=%q id=%s ffmpeg unavailable path=%q err=%v",
					channel.GuideName, channelID, os.Getenv("IPTV_TUNERR_FFMPEG_PATH"), ffmpegErr)
			} else if transcode {
				log.Printf("gateway: channel=%q id=%s ffmpeg unavailable transcode-requested=true err=%v (falling back to go relay; web clients may get incompatible audio/video codecs)", channel.GuideName, channelID, ffmpegErr)
			}
			if err := g.relayHLSAsTS(
				w,
				r,
				client,
				streamURL,
				body,
				channel.GuideName,
				channelID,
				channel.GuideNumber,
				channel.TVGID,
				start,
				transcode,
				forcedProfile,
				bufferSize,
				responseAlreadyStarted(w),
			); err != nil {
				log.Printf("gateway: channel=%q id=%s hls-relay failed: %v", channel.GuideName, channelID, err)
				continue
			}
			g.rememberAutopilotDecision(channel, clientClass, transcode, effectiveProfileName(g, channel, channelID, forcedProfile), adaptReason)
			return
		}
		bufferSize := g.effectiveBufferSize(false)
		ct := resp.Header.Get("Content-Type")
		isMPEGTS := strings.Contains(ct, "video/mp2t") ||
			strings.HasSuffix(strings.ToLower(streamURL), ".ts")
		if isMPEGTS {
			if ffmpegPath, ffmpegErr := resolveFFmpegPath(); ffmpegErr == nil {
				if g.relayRawTSWithFFmpeg(w, r, ffmpegPath, resp.Body, channel.GuideName, channelID, resp.StatusCode, start, bufferSize) {
					return
				}
				log.Printf("gateway: channel=%q id=%s ffmpeg-ts-norm failed to launch; falling back to raw proxy", channel.GuideName, channelID)
			}
		}
		w.WriteHeader(resp.StatusCode)
		sw, flush := streamWriter(w, bufferSize)
		n, _ := io.Copy(sw, resp.Body)
		resp.Body.Close()
		flush()
		g.rememberAutopilotDecision(channel, clientClass, false, "", adaptReason)
		log.Printf("gateway: channel=%q id=%s proxied bytes=%d dur=%s", channel.GuideName, channelID, n, time.Since(start).Round(time.Millisecond))
		return
	}
	if upstreamConcurrencyLimited {
		log.Printf("gateway: req=%s channel=%q id=%s upstream concurrency limit hit; surfacing all-tuners-in-use to client",
			reqID, channel.GuideName, channelID)
		w.Header().Set("X-HDHomeRun-Error", "805")
		http.Error(w, "All tuners in use", http.StatusServiceUnavailable)
		return
	}
	log.Printf("gateway: channel=%q id=%s all %d upstream(s) failed dur=%s", channel.GuideName, channelID, len(urls), time.Since(start).Round(time.Millisecond))
	http.Error(w, "All upstreams failed", http.StatusBadGateway)
}

// relayRawTSWithFFmpeg normalizes a raw MPEG-TS stream through FFmpeg to fix
// disposition:default=0 and MPTS issues that cause Plex clients to play with no audio.
// The upstream response headers must already be set on w before calling.
// Returns true if FFmpeg launched and handled the response; false signals the caller
// to fall back to a raw io.Copy proxy (resp.Body is untouched on false return).
func (g *Gateway) relayRawTSWithFFmpeg(
	w http.ResponseWriter,
	r *http.Request,
	ffmpegPath string,
	src io.ReadCloser,
	channelName, channelID string,
	respStatus int,
	start time.Time,
	bufferBytes int,
) bool {
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-fflags", "+discardcorrupt+genpts",
		"-analyzeduration", "500000",
		"-probesize", "500000",
		"-f", "mpegts",
		"-i", "pipe:0",
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-c", "copy",
		"-f", "mpegts",
		"pipe:1",
	}
	cmd := exec.CommandContext(r.Context(), ffmpegPath, args...)
	cmd.Stdin = src
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return false
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		return false
	}
	defer src.Close()
	defer cmd.Wait() //nolint:errcheck
	w.WriteHeader(respStatus)
	sw, flush := streamWriter(w, bufferBytes)
	n, _ := io.Copy(sw, stdout)
	flush()
	log.Printf("gateway: channel=%q id=%s ffmpeg-ts-norm bytes=%d dur=%s",
		channelName, channelID, n, time.Since(start).Round(time.Millisecond))
	return true
}

func ffmpegRelayErr(phase string, err error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg != "" {
		if len(msg) > 600 {
			msg = msg[:600] + "..."
		}
		return fmt.Errorf("%s: %w (stderr=%q)", phase, err, msg)
	}
	return fmt.Errorf("%s: %w", phase, err)
}

func channelIDFromRequestPath(path string) (string, bool) {
	if strings.HasPrefix(path, "/stream/") {
		return strings.TrimPrefix(path, "/stream/"), true
	}
	if strings.HasPrefix(path, "/auto/") {
		rest := strings.TrimPrefix(path, "/auto/")
		// PMS fallback commonly uses /auto/v<channelID>.
		if strings.HasPrefix(rest, "v") {
			rest = strings.TrimPrefix(rest, "v")
		}
		return rest, true
	}
	return "", false
}

func (g *Gateway) relayHLSWithFFmpeg(
	w http.ResponseWriter,
	r *http.Request,
	ffmpegPath string,
	playlistURL string,
	channelName string,
	channelID string,
	guideNumber string,
	tvgID string,
	start time.Time,
	transcode bool,
	bufferBytes int,
	forcedProfile string,
) error {
	reqField := gatewayReqIDField(r.Context())
	profile := g.profileForChannelMeta(channelID, guideNumber, tvgID)
	if strings.TrimSpace(forcedProfile) != "" {
		profile = normalizeProfileName(forcedProfile)
	}
	ffmpegPlaylistURL, ffmpegInputHost, ffmpegInputIP := canonicalizeFFmpegInputURL(r.Context(), playlistURL)

	// HLS inputs are more sensitive to over-aggressive probing/low-latency flags than raw TS.
	// Default to safer probing and allow env overrides when chasing startup races.
	hlsAnalyzeDurationUs := getenvInt("IPTV_TUNERR_FFMPEG_HLS_ANALYZEDURATION_US", 5000000)
	hlsProbeSize := getenvInt("IPTV_TUNERR_FFMPEG_HLS_PROBESIZE", 5000000)
	hlsRWTimeoutUs := getenvInt("IPTV_TUNERR_FFMPEG_HLS_RW_TIMEOUT_US", 60000000)
	hlsLiveStartIndex := getenvInt("IPTV_TUNERR_FFMPEG_HLS_LIVE_START_INDEX", -3)
	hlsUseNoBuffer := getenvBool("IPTV_TUNERR_FFMPEG_HLS_NOBUFFER", false)
	// Let ffmpeg's HLS demuxer manage live playlist refreshes by default.
	// Generic HTTP reconnect flags (especially reconnect-at-EOF) can cause
	// live .m3u8 inputs to loop on playlist EOF and never start segment reads.
	hlsReconnect := getenvBool("IPTV_TUNERR_FFMPEG_HLS_RECONNECT", false)
	if g.shouldAutoEnableHLSReconnect() {
		hlsReconnect = true
	}
	hlsRealtime := getenvBool("IPTV_TUNERR_FFMPEG_HLS_REALTIME", false)
	hlsLogLevel := strings.TrimSpace(os.Getenv("IPTV_TUNERR_FFMPEG_HLS_LOGLEVEL"))
	if hlsLogLevel == "" {
		hlsLogLevel = "error"
	}
	fflags := "+discardcorrupt+genpts"
	if hlsUseNoBuffer {
		fflags += "+nobuffer"
	}
	args := []string{
		"-nostdin",
		"-hide_banner",
		"-loglevel", hlsLogLevel,
		"-fflags", fflags,
		"-analyzeduration", strconv.Itoa(hlsAnalyzeDurationUs),
		"-probesize", strconv.Itoa(hlsProbeSize),
		"-rw_timeout", strconv.Itoa(hlsRWTimeoutUs),
		"-user_agent", "IptvTunerr/1.0",
	}
	if hlsReconnect {
		args = append(args,
			"-reconnect", "1",
			"-reconnect_streamed", "1",
			"-reconnect_at_eof", "1",
			"-reconnect_on_network_error", "1",
			"-reconnect_delay_max", "2",
		)
	}
	if hlsRealtime {
		// Pace ffmpeg reads to input timestamps (wall-clock-ish) to avoid racing
		// far ahead of Plex's live consumer attach during startup on HLS inputs.
		args = append(args, "-re")
	}
	if hlsLiveStartIndex != 0 {
		args = append(args, "-live_start_index", strconv.Itoa(hlsLiveStartIndex))
	}
	if headers := g.ffmpegInputHeaderBlock(r, ffmpegInputHost); headers != "" {
		args = append(args, "-headers", headers)
	}
	args = append(args, "-i", ffmpegPlaylistURL)
	args = append(args, buildFFmpegMPEGTSCodecArgs(transcode, profile)...)

	cmd := exec.CommandContext(r.Context(), ffmpegPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	modeLabel := "ffmpeg-remux"
	if transcode {
		modeLabel = "ffmpeg-transcode"
	}
	if ffmpegInputHost != "" && ffmpegInputIP != "" {
		log.Printf("gateway:%s channel=%q id=%s %s input-host-resolved %q=>%q",
			reqField, channelName, channelID, modeLabel, ffmpegInputHost, ffmpegInputIP)
	}
	log.Printf("gateway:%s channel=%q id=%s %s profile=%s", reqField, channelName, channelID, modeLabel, profile)
	log.Printf("gateway:%s channel=%q id=%s %s hls-input analyzeduration_us=%d probesize=%d rw_timeout_us=%d live_start_index=%d nobuffer=%t reconnect=%t realtime=%t loglevel=%s",
		reqField, channelName, channelID, modeLabel, hlsAnalyzeDurationUs, hlsProbeSize, hlsRWTimeoutUs, hlsLiveStartIndex, hlsUseNoBuffer, hlsReconnect, hlsRealtime, hlsLogLevel)
	// In web-safe transcode modes, hold back the first bytes (and optionally prepend a short
	// deterministic H264/AAC TS bootstrap) so Plex's live DASH packager gets a clean start.
	startupMin := getenvInt("IPTV_TUNERR_WEBSAFE_STARTUP_MIN_BYTES", 65536)
	startupMax := getenvInt("IPTV_TUNERR_WEBSAFE_STARTUP_MAX_BYTES", 786432)
	startupTimeoutMs := getenvInt("IPTV_TUNERR_WEBSAFE_STARTUP_TIMEOUT_MS", 60000)
	enableBootstrap := transcode && getenvBool("IPTV_TUNERR_WEBSAFE_BOOTSTRAP", true)
	enableTimeoutBootstrap := getenvBool("IPTV_TUNERR_WEBSAFE_TIMEOUT_BOOTSTRAP", true)
	continueOnStartupTimeout := transcode && getenvBool("IPTV_TUNERR_WEBSAFE_TIMEOUT_CONTINUE_FFMPEG", false)
	bootstrapSec := getenvFloat("IPTV_TUNERR_WEBSAFE_BOOTSTRAP_SECONDS", 1.5)
	requireGoodStart := transcode && getenvBool("IPTV_TUNERR_WEBSAFE_REQUIRE_GOOD_START", true)
	enableNullTSKeepalive := transcode && getenvBool("IPTV_TUNERR_WEBSAFE_NULL_TS_KEEPALIVE", false)
	nullTSKeepaliveMs := getenvInt("IPTV_TUNERR_WEBSAFE_NULL_TS_KEEPALIVE_MS", 100)
	nullTSKeepalivePackets := getenvInt("IPTV_TUNERR_WEBSAFE_NULL_TS_KEEPALIVE_PACKETS", 1)
	// PAT+PMT keepalive: sends real program-structure packets (not just null PIDs) so
	// Plex's DASH packager can instantiate its consumer before the first IDR arrives.
	enableProgramKeepalive := transcode && getenvBool("IPTV_TUNERR_WEBSAFE_PROGRAM_KEEPALIVE", false)
	programKeepaliveMs := getenvInt("IPTV_TUNERR_WEBSAFE_PROGRAM_KEEPALIVE_MS", 500)
	// Do not run both keepalives concurrently against the same ResponseWriter: parallel
	// writes can interleave/chunk-corrupt HTTP output and manifest as short writes.
	if enableProgramKeepalive && enableNullTSKeepalive {
		enableNullTSKeepalive = false
		log.Printf("gateway:%s channel=%q id=%s %s keepalive-select program=true null=false reason=program-priority",
			reqField, channelName, channelID, modeLabel)
	}
	var bodyOut io.Writer
	flushBody := func() {}
	responseStarted := false
	startResponse := func() {
		if responseStarted {
			return
		}
		w.Header().Set("Content-Type", "video/mp2t")
		w.Header().Del("Content-Length")
		w.WriteHeader(http.StatusOK)
		bodyOut, flushBody = streamWriter(w, bufferBytes)
		responseStarted = true
	}
	defer func() { flushBody() }()
	stopNullTSKeepalive := func(string) {}
	stopPATMPTKeepalive := func(string) {}
	bootstrapAlreadySent := false
	var prefetch []byte
	if transcode && startupMin > 0 {
		// Send HTTP 200 + Content-Type headers immediately, before any body bytes.
		// This separates "connection accepted" from "bytes available" and prevents
		// Plex from timing out on the HTTP response header wait during startup gate.
		startResponse()
		if fw, ok := w.(http.Flusher); ok {
			fw.Flush()
		}
		if enableNullTSKeepalive {
			flusher, _ := w.(http.Flusher)
			stopNullTSKeepalive = startNullTSKeepalive(
				r.Context(),
				bodyOut,
				flushBody,
				flusher,
				channelName,
				channelID,
				modeLabel,
				start,
				time.Duration(nullTSKeepaliveMs)*time.Millisecond,
				nullTSKeepalivePackets,
			)
			log.Printf("gateway:%s channel=%q id=%s %s null-ts-keepalive start interval_ms=%d packets=%d",
				reqField, channelName, channelID, modeLabel, nullTSKeepaliveMs, nullTSKeepalivePackets)
		}
		if enableProgramKeepalive {
			flusher, _ := w.(http.Flusher)
			stopPATMPTKeepalive = startPATMPTKeepalive(
				r.Context(),
				bodyOut,
				flushBody,
				flusher,
				channelName,
				channelID,
				modeLabel,
				start,
				time.Duration(programKeepaliveMs)*time.Millisecond,
			)
			log.Printf("gateway:%s channel=%q id=%s %s pat-pmt-keepalive start interval_ms=%d",
				reqField, channelName, channelID, modeLabel, programKeepaliveMs)
		}
		type prefetchRes struct {
			b     []byte
			err   error
			state startSignalState
		}
		ch := make(chan prefetchRes, 1)
		go func() {
			buf := make([]byte, 0, startupMin)
			tmp := make([]byte, 32768)
			if startupMax < startupMin {
				startupMax = startupMin
			}
			for {
				n, rerr := stdout.Read(tmp)
				if n > 0 {
					room := startupMax - len(buf)
					if room > 0 {
						if n > room {
							n = room
						}
						buf = append(buf, tmp[:n]...)
					}
					st := looksLikeGoodTSStart(buf)
					good := !requireGoodStart || (st.HasIDR && st.HasAAC && st.TSLikePackets >= 8)
					if len(buf) >= startupMin && good {
						ch <- prefetchRes{b: buf, state: st}
						return
					}
					if len(buf) >= startupMax {
						ch <- prefetchRes{b: buf, state: st}
						return
					}
				}
				if rerr != nil {
					st := looksLikeGoodTSStart(buf)
					if len(buf) > 0 {
						ch <- prefetchRes{b: buf, err: rerr, state: st}
					} else {
						ch <- prefetchRes{err: rerr, state: st}
					}
					return
				}
			}
		}()
		timeout := time.Duration(startupTimeoutMs) * time.Millisecond
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		select {
		case pr := <-ch:
			stopReason := "startup-gate-ready"
			if pr.err != nil && len(pr.b) == 0 {
				stopReason = "startup-gate-prefetch-error"
			}
			stopNullTSKeepalive(stopReason)
			stopPATMPTKeepalive(stopReason)
			prefetch = pr.b
			if pr.state.AlignedOffset > 0 && pr.state.AlignedOffset < len(prefetch) {
				prefetch = prefetch[pr.state.AlignedOffset:]
			}
			if len(prefetch) > 0 {
				log.Printf(
					"gateway:%s channel=%q id=%s %s startup-gate buffered=%d min=%d max=%d timeout_ms=%d ts_pkts=%d idr=%t aac=%t align=%d",
					reqField, channelName, channelID, modeLabel, len(prefetch), startupMin, startupMax, startupTimeoutMs,
					pr.state.TSLikePackets, pr.state.HasIDR, pr.state.HasAAC, pr.state.AlignedOffset,
				)
			}
			if pr.err != nil && len(prefetch) == 0 {
				_ = cmd.Process.Kill()
				waitErr := cmd.Wait()
				msg := strings.TrimSpace(stderr.String())
				if msg == "" {
					msg = pr.err.Error()
				}
				if pr.err != nil {
					errOut := error(pr.err)
					if waitErr != nil && waitErr.Error() != pr.err.Error() {
						errOut = fmt.Errorf("%w (wait=%v)", pr.err, waitErr)
					}
					return ffmpegRelayErr("startup-gate-prefetch", errOut, stderr.String())
				}
				return errors.New(msg)
			}
		case <-time.After(timeout):
			stopNullTSKeepalive("startup-gate-timeout")
			stopPATMPTKeepalive("startup-gate-timeout")
			if responseStarted && enableBootstrap && enableTimeoutBootstrap && bootstrapSec > 0 {
				if err := writeBootstrapTS(r.Context(), ffmpegPath, bodyOut, channelName, channelID, bootstrapSec, profile); err != nil {
					log.Printf("gateway:%s channel=%q id=%s %s timeout-bootstrap failed: %v", reqField, channelName, channelID, modeLabel, err)
				} else {
					bootstrapAlreadySent = true
					flushBody()
					log.Printf("gateway:%s channel=%q id=%s %s timeout-bootstrap emitted before relay fallback", reqField, channelName, channelID, modeLabel)
				}
			} else if responseStarted && enableBootstrap && !enableTimeoutBootstrap {
				log.Printf("gateway:%s channel=%q id=%s %s timeout-bootstrap disabled before relay fallback", reqField, channelName, channelID, modeLabel)
			}
			log.Printf("gateway:%s channel=%q id=%s %s startup-gate timeout after=%dms", reqField, channelName, channelID, modeLabel, startupTimeoutMs)
			if continueOnStartupTimeout {
				log.Printf("gateway:%s channel=%q id=%s %s startup-gate timeout continue-ffmpeg=true", reqField, channelName, channelID, modeLabel)
				break
			}
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = "startup gate timeout"
			}
			if msg == "startup gate timeout" {
				return ffmpegRelayErr("startup-gate-timeout", errors.New(msg), stderr.String())
			}
			return ffmpegRelayErr("startup-gate-timeout", errors.New(msg), stderr.String())
		case <-r.Context().Done():
			stopNullTSKeepalive("startup-gate-cancel")
			stopPATMPTKeepalive("startup-gate-cancel")
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil
		}
	}

	startResponse()

	if enableBootstrap && bootstrapSec > 0 && !bootstrapAlreadySent {
		if err := writeBootstrapTS(r.Context(), ffmpegPath, bodyOut, channelName, channelID, bootstrapSec, profile); err != nil {
			log.Printf("gateway:%s channel=%q id=%s bootstrap failed: %v", reqField, channelName, channelID, err)
		}
		if joinDelayMs := getenvInt("IPTV_TUNERR_WEBSAFE_JOIN_DELAY_MS", 0); joinDelayMs > 0 {
			if joinDelayMs > 5000 {
				joinDelayMs = 5000
			}
			log.Printf("gateway: channel=%q id=%s websafe-join-delay ms=%d", channelName, channelID, joinDelayMs)
			select {
			case <-time.After(time.Duration(joinDelayMs) * time.Millisecond):
			case <-r.Context().Done():
				return nil
			}
		}
	}

	mainReader := io.Reader(stdout)
	if len(prefetch) > 0 {
		mainReader = io.MultiReader(bytes.NewReader(prefetch), stdout)
	}
	dst := io.Writer(bodyOut)
	if fw, ok := w.(http.Flusher); ok {
		dst = &firstWriteLogger{
			w:           flushWriter{w: bodyOut, f: fw},
			channelName: channelName,
			channelID:   channelID,
			reqID:       gatewayReqIDFromContext(r.Context()),
			modeLabel:   modeLabel,
			start:       start,
		}
	} else {
		dst = &firstWriteLogger{
			w:           bodyOut,
			channelName: channelName,
			channelID:   channelID,
			reqID:       gatewayReqIDFromContext(r.Context()),
			modeLabel:   modeLabel,
			start:       start,
		}
	}
	dst = maybeWrapTSInspectorWriter(dst, gatewayReqIDFromContext(r.Context()), channelName, channelID, guideNumber, tvgID, modeLabel, start)
	if c, ok := dst.(interface{ Close() }); ok {
		defer c.Close()
	}
	n, copyErr := io.Copy(dst, mainReader)
	waitErr := cmd.Wait()

	if r.Context().Err() != nil {
		log.Printf("gateway:%s channel=%q id=%s %s client-done bytes=%d dur=%s",
			reqField, channelName, channelID, modeLabel, n, time.Since(start).Round(time.Millisecond))
		return nil
	}
	if copyErr != nil && r.Context().Err() == nil {
		return ffmpegRelayErr("copy", copyErr, stderr.String())
	}
	if waitErr != nil && r.Context().Err() == nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return ffmpegRelayErr("wait", waitErr, stderr.String())
		}
		return ffmpegRelayErr("wait", errors.New(msg), stderr.String())
	}
	log.Printf("gateway:%s channel=%q id=%s %s bytes=%d dur=%s",
		reqField, channelName, channelID, modeLabel, n, time.Since(start).Round(time.Millisecond))
	return nil
}

func (g *Gateway) relayHLSAsTS(
	w http.ResponseWriter,
	r *http.Request,
	client *http.Client,
	playlistURL string,
	initialPlaylist []byte,
	channelName string,
	channelID string,
	guideNumber string,
	tvgID string,
	start time.Time,
	transcode bool,
	forcedProfile string,
	bufferBytes int,
	responseStarted bool,
) (retErr error) {
	reqField := gatewayReqIDField(r.Context())
	if client == nil {
		client = httpclient.ForStreaming()
	}
	profile := g.profileForChannelMeta(channelID, guideNumber, tvgID)
	if strings.TrimSpace(forcedProfile) != "" {
		profile = normalizeProfileName(forcedProfile)
	}
	sw, flush := streamWriter(w, bufferBytes)
	defer flush()
	flusher, _ := w.(http.Flusher)
	seen := map[string]struct{}{}
	lastProgress := time.Now()
	sentBytes := int64(0)
	sentSegments := 0
	headerSent := responseStarted
	firstRelayBytesLogged := false
	currentPlaylistURL := playlistURL
	currentPlaylist := initialPlaylist
	relayLogLabel := "hls-relay"

	enableFFmpegStdinNormalize := getenvBool("IPTV_TUNERR_HLS_RELAY_FFMPEG_STDIN_NORMALIZE", false)
	var normalizer *hlsRelayFFmpegStdinNormalizer
	if enableFFmpegStdinNormalize {
		if ffmpegPath, ffmpegErr := resolveFFmpegPath(); ffmpegErr == nil {
			norm, err := g.startHLSRelayFFmpegStdinNormalizer(
				w,
				r,
				ffmpegPath,
				channelName,
				channelID,
				start,
				transcode,
				profile,
				sw,
				flush,
				bufferBytes,
				responseStarted,
			)
			if err != nil {
				log.Printf("gateway:%s channel=%q id=%s hls-relay-ffmpeg-stdin start failed (falling back to raw relay): %v",
					reqField, channelName, channelID, err)
			} else {
				normalizer = norm
				relayLogLabel = "hls-relay-ffmpeg-stdin-feed"
				log.Printf("gateway:%s channel=%q id=%s hls-relay-ffmpeg-stdin enabled transcode=%t profile=%s",
					reqField, channelName, channelID, transcode, profile)
			}
		} else if strings.TrimSpace(os.Getenv("IPTV_TUNERR_FFMPEG_PATH")) != "" {
			log.Printf("gateway:%s channel=%q id=%s hls-relay-ffmpeg-stdin ffmpeg unavailable path=%q err=%v",
				reqField, channelName, channelID, os.Getenv("IPTV_TUNERR_FFMPEG_PATH"), ffmpegErr)
		} else if transcode {
			log.Printf("gateway:%s channel=%q id=%s hls-relay-ffmpeg-stdin ffmpeg unavailable transcode-requested=true err=%v", reqField, channelName, channelID, ffmpegErr)
		}
	}
	if normalizer != nil {
		defer func() {
			if err := normalizer.CloseAndWait(); err != nil && retErr == nil && r.Context().Err() == nil {
				retErr = err
			}
		}()
	}
	if responseStarted {
		if normalizer != nil {
			log.Printf("gateway:%s channel=%q id=%s %s splice-start prior-bytes=true", reqField, channelName, channelID, relayLogLabel)
		} else {
			log.Printf("gateway:%s channel=%q id=%s hls-relay splice-start prior-bytes=true", reqField, channelName, channelID)
		}
	}
	clientStarted := func() bool {
		return headerSent || (normalizer != nil && normalizer.ResponseStarted())
	}

	for {
		select {
		case <-r.Context().Done():
			log.Printf("gateway:%s channel=%q id=%s %s client-done segs=%d bytes=%d dur=%s",
				reqField, channelName, channelID, relayLogLabel, sentSegments, sentBytes, time.Since(start).Round(time.Millisecond))
			return nil
		default:
		}

		mediaLines := hlsMediaLines(currentPlaylist)
		// Prune seen to only segment URLs still in playlist so map doesn't grow unbounded.
		segmentURLSet := make(map[string]struct{}, len(mediaLines))
		for _, u := range mediaLines {
			if !strings.HasSuffix(strings.ToLower(u), ".m3u8") {
				segmentURLSet[u] = struct{}{}
			}
		}
		for u := range seen {
			if _, inPlaylist := segmentURLSet[u]; !inPlaylist {
				delete(seen, u)
			}
		}
		if len(mediaLines) == 0 {
			if !clientStarted() {
				return errors.New("hls playlist has no media lines")
			}
			if time.Since(lastProgress) > 12*time.Second {
				return errors.New("hls relay stalled (no media lines)")
			}
			time.Sleep(1 * time.Second)
		} else {
			progressThisPass := false
			for _, segURL := range mediaLines {
				if strings.HasSuffix(strings.ToLower(segURL), ".m3u8") {
					// Some providers return a master/variant indirection; follow one level.
					next, err := g.fetchAndRewritePlaylist(r, client, segURL)
					if err != nil {
						if !clientStarted() {
							return err
						}
						log.Printf("gateway:%s channel=%q id=%s nested-playlist fetch failed url=%s err=%v",
							reqField, channelName, channelID, safeurl.RedactURL(segURL), err)
						g.noteHLSPlaylistFailure(segURL)
						continue
					}
					currentPlaylistURL = segURL
					currentPlaylist = next
					progressThisPass = true
					break
				}
				if _, ok := seen[segURL]; ok {
					continue
				}
				seen[segURL] = struct{}{}
				var segOut io.Writer = sw
				var spliceWriter *tsDiscontinuitySpliceWriter
				if normalizer != nil {
					segOut = normalizer
				} else if responseStarted && sentSegments == 0 {
					spliceWriter = newTSDiscontinuitySpliceWriter(r.Context(), sw, channelName, channelID)
					segOut = spliceWriter
				}
				n, err := g.fetchAndWriteSegment(w, segOut, r, client, segURL, headerSent || normalizer != nil)
				if err == nil && spliceWriter != nil {
					if ferr := spliceWriter.FlushRemainder(); ferr != nil {
						err = ferr
					}
				}
				if err != nil {
					if errors.Is(err, errCFBlock) {
						g.noteCFBlock(segURL)
						log.Printf("gateway:%s channel=%q id=%s CF-blocked segment rejected; aborting stream url=%s",
							reqField, channelName, channelID, safeurl.RedactURL(segURL))
						return err
					}
					if isClientDisconnectWriteError(err) {
						if n > 0 {
							sentBytes += n
						}
						log.Printf("gateway:%s channel=%q id=%s %s client-done write-closed segs=%d bytes=%d dur=%s",
							reqField, channelName, channelID, relayLogLabel, sentSegments, sentBytes, time.Since(start).Round(time.Millisecond))
						return nil
					}
					if !clientStarted() {
						return err
					}
					if r.Context().Err() != nil {
						return nil
					}
					g.noteHLSSegmentFailure(segURL)
					log.Printf("gateway:%s channel=%q id=%s segment fetch failed url=%s err=%v",
						reqField, channelName, channelID, safeurl.RedactURL(segURL), err)
					continue
				}
				if normalizer != nil {
					headerSent = headerSent || normalizer.ResponseStarted()
				}
				if normalizer == nil && !headerSent {
					headerSent = true
					if flusher != nil {
						flusher.Flush()
					}
				}
				if n > 0 {
					if !firstRelayBytesLogged {
						firstRelayBytesLogged = true
						if normalizer != nil {
							log.Printf("gateway:%s channel=%q id=%s hls-relay-ffmpeg-stdin first-feed-bytes=%d seg=%q startup=%s",
								reqField, channelName, channelID, n, safeurl.RedactURL(segURL), time.Since(start).Round(time.Millisecond))
						} else {
							log.Printf("gateway:%s channel=%q id=%s hls-relay first-bytes=%d seg=%q startup=%s",
								reqField, channelName, channelID, n, safeurl.RedactURL(segURL), time.Since(start).Round(time.Millisecond))
						}
					}
					sentBytes += n
					sentSegments++
					lastProgress = time.Now()
					progressThisPass = true
				}
				if normalizer == nil && flusher != nil {
					flusher.Flush()
				}
			}

			if !progressThisPass && time.Since(lastProgress) > 12*time.Second {
				if !clientStarted() {
					return errors.New("hls relay stalled before first segment")
				}
				log.Printf("gateway:%s channel=%q id=%s %s ended no-new-segments segs=%d bytes=%d dur=%s",
					reqField, channelName, channelID, relayLogLabel, sentSegments, sentBytes, time.Since(start).Round(time.Millisecond))
				return nil
			}
			sleepHLSRefresh(currentPlaylist)
		}

		next, err := g.fetchAndRewritePlaylist(r, client, currentPlaylistURL)
		if err != nil {
			if !clientStarted() {
				return err
			}
			if r.Context().Err() != nil {
				return nil
			}
			if time.Since(lastProgress) > 12*time.Second {
				g.noteHLSPlaylistFailure(currentPlaylistURL)
				return err
			}
			g.noteHLSPlaylistFailure(currentPlaylistURL)
			log.Printf("gateway:%s channel=%q id=%s playlist refresh failed url=%s err=%v",
				reqField, channelName, channelID, safeurl.RedactURL(currentPlaylistURL), err)
			sleepHLSRefresh(currentPlaylist)
			continue
		}
		currentPlaylist = next
	}
}
