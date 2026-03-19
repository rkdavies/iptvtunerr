// Command iptv-tunerr: IPTV bridge providing live TV streaming and XMLTV guide serving
// for Plex, Emby, and Jellyfin. Two core capabilities:
//
//   - Streaming: HDHomeRun-compatible tuner endpoints (/discover.json, /lineup.json,
//     /stream/{id}) backed by M3U/Xtream provider with optional ffmpeg transcode.
//   - Guide/EPG: XMLTV guide at /guide.xml — provider xmltv.php, external XMLTV,
//     and placeholder fallback merged and cached, with deterministic TVGID repair during catalog build.
//
// Subcommands:
//
//	run    One-run: refresh catalog + health check + serve tuner and guide (for systemd)
//	serve  Run tuner (streams) and guide (XMLTV) server from existing catalog
//	index  Fetch M3U/Xtream, parse, save catalog (live channels + VOD + series)
//	mount  Load catalog and mount VODFS (optional -cache for on-demand download)
//	probe  Cycle through provider URLs, probe each, report OK / Cloudflare / fail
package main

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"

	"github.com/snapetech/iptvtunerr/internal/config"
	"github.com/snapetech/iptvtunerr/internal/emby"
	"github.com/snapetech/iptvtunerr/internal/plex"
	"github.com/snapetech/iptvtunerr/internal/tuner"
)

func applyPlexVODLibraryPreset(plexBaseURL, plexToken string, sec *plex.LibrarySection) error {
	if sec == nil {
		return fmt.Errorf("nil library section")
	}
	prefs, err := plex.GetLibrarySectionPrefs(plexBaseURL, plexToken, sec.Key)
	if err != nil {
		return err
	}
	// Disable expensive media-analysis/background jobs for virtual catch-up libraries only.
	desired := map[string]string{
		"enableBIFGeneration":           "0",
		"enableChapterThumbGeneration":  "0",
		"enableIntroMarkerGeneration":   "0",
		"enableCreditsMarkerGeneration": "0",
		"enableAdMarkerGeneration":      "0",
		"enableVoiceActivityGeneration": "0",
	}
	updates := map[string]string{}
	for k, v := range desired {
		if got, ok := prefs[k]; ok && got != v {
			updates[k] = v
		}
	}
	if len(updates) == 0 {
		return nil
	}
	return plex.UpdateLibrarySectionPrefs(plexBaseURL, plexToken, sec.Key, updates)
}

func resolvePlexAccess(flagURL, flagToken string) (string, string) {
	baseURL := strings.TrimSpace(flagURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("IPTV_TUNERR_PMS_URL"))
	}
	if baseURL == "" {
		if host := strings.TrimSpace(os.Getenv("PLEX_HOST")); host != "" {
			baseURL = "http://" + host + ":32400"
		}
	}
	token := strings.TrimSpace(flagToken)
	if token == "" {
		token = strings.TrimSpace(os.Getenv("IPTV_TUNERR_PMS_TOKEN"))
	}
	if token == "" {
		token = strings.TrimSpace(os.Getenv("PLEX_TOKEN"))
	}
	return baseURL, token
}

func registerCatchupPlexLibraries(baseURL, token string, manifest tuner.CatchupPublishManifest, refresh bool) error {
	for _, lib := range manifest.Libraries {
		sec, created, err := plex.EnsureLibrarySection(baseURL, token, plex.LibraryCreateSpec{
			Name:     lib.Name,
			Type:     "movie",
			Path:     lib.Path,
			Language: "en-US",
		})
		if err != nil {
			return err
		}
		if created {
			log.Printf("Created Plex catch-up library %q (key=%s path=%s)", sec.Title, sec.Key, lib.Path)
		} else {
			log.Printf("Reusing Plex catch-up library %q (key=%s path=%s)", sec.Title, sec.Key, lib.Path)
		}
		if err := applyPlexVODLibraryPreset(baseURL, token, sec); err != nil {
			return err
		}
		if refresh {
			if err := plex.RefreshLibrarySection(baseURL, token, sec.Key); err != nil {
				return err
			}
			log.Printf("Refresh started for Plex catch-up library %q", sec.Title)
		}
	}
	return nil
}

func registerCatchupMediaServerLibraries(serverType, host, token string, manifest tuner.CatchupPublishManifest, refresh bool) error {
	cfg := emby.Config{
		Host:       strings.TrimSpace(host),
		Token:      strings.TrimSpace(token),
		ServerType: serverType,
	}
	for _, lib := range manifest.Libraries {
		got, created, err := emby.EnsureLibrary(cfg, emby.LibraryCreateSpec{
			Name:           lib.Name,
			CollectionType: "movies",
			Path:           lib.Path,
			Refresh:        false,
		})
		if err != nil {
			return err
		}
		if created {
			log.Printf("Created %s catch-up library %q (id=%s path=%s)", serverType, lib.Name, got.ID, lib.Path)
		} else {
			log.Printf("Reusing %s catch-up library %q (id=%s path=%s)", serverType, got.Name, got.ID, lib.Path)
		}
	}
	if refresh {
		if err := emby.RefreshLibraryScan(cfg); err != nil {
			return err
		}
		log.Printf("Triggered %s library refresh", serverType)
	}
	return nil
}

func main() {
	_ = config.LoadEnvFile(".env")
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("[iptv-tunerr] ")

	if len(os.Args) == 2 && (os.Args[1] == "-version" || os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Println(Version)
		os.Exit(0)
	}

	commands := append(coreCommands(), reportCommands()...)
	commands = append(commands, opsCommands()...)
	commandByName := make(map[string]commandSpec, len(commands))
	sections := []string{"Core", "Guide/EPG", "VOD (Linux)", "Lab/ops"}
	for _, cmd := range commands {
		commandByName[cmd.Name] = cmd
	}

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "iptv-tunerr %s — live TV streaming + XMLTV guide for Plex, Emby, Jellyfin\n\n", Version)
		fmt.Fprintf(os.Stderr, "Streaming: HDHomeRun-compatible tuner endpoints backed by M3U/Xtream with optional transcode.\n")
		fmt.Fprintf(os.Stderr, "Guide/EPG: /guide.xml — provider XMLTV + external XMLTV + placeholder fallback, with deterministic TVGID repair during catalog build.\n\n")
		fmt.Fprintf(os.Stderr, "Usage: %s <command> [flags]\n\n", os.Args[0])
		for _, section := range sections {
			first := true
			for _, cmd := range commands {
				if cmd.Section != section {
					continue
				}
				if first {
					fmt.Fprintf(os.Stderr, "%s:\n", section)
					first = false
				}
				fmt.Fprintf(os.Stderr, "  %-18s %s\n", cmd.Name, cmd.Summary)
			}
			if !first {
				fmt.Fprintln(os.Stderr)
			}
		}
		os.Exit(1)
	}

	cfg := config.Load()
	cmd, ok := commandByName[os.Args[1]]
	if !ok {
		fmt.Fprintf(os.Stderr, "Unknown command %q\n", os.Args[1])
		os.Exit(1)
	}
	cmd.Run(cfg, os.Args[2:])
}

func parseCSV(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func hostPortFromBaseURL(base string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return "", fmt.Errorf("missing host")
	}
	return u.Host, nil
}
