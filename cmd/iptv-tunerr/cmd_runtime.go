package main

import (
	"context"
	"io"
	"log"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/snapetech/iptvtunerr/internal/catalog"
	"github.com/snapetech/iptvtunerr/internal/channeldna"
	"github.com/snapetech/iptvtunerr/internal/config"
	"github.com/snapetech/iptvtunerr/internal/emby"
	"github.com/snapetech/iptvtunerr/internal/hdhomerun"
	"github.com/snapetech/iptvtunerr/internal/health"
	"github.com/snapetech/iptvtunerr/internal/plex"
	"github.com/snapetech/iptvtunerr/internal/tuner"
)

func handleServe(cfg *config.Config, catalogPath, addr, baseURL, deviceID, friendlyName, mode string) {
	path := catalogPath
	if path == "" {
		path = cfg.CatalogPath
	}
	c := catalog.New()
	if err := c.Load(path); err != nil {
		log.Printf("Load catalog (live channels): %v; serving with no channels", err)
	}
	live := c.SnapshotLive()
	applyRuntimeEPGRepairs(cfg, live, cfg.ProviderBaseURL, cfg.ProviderUser, cfg.ProviderPass)
	channeldna.Assign(live)
	log.Printf("Loaded %d live channels from %s", len(live), path)
	serveLineupCap := cfg.LineupMaxChannels
	if mode == "easy" {
		serveLineupCap = tuner.PlexDVRWizardSafeMax
	}
	if deviceID == "" {
		deviceID = cfg.DeviceID
	}
	if friendlyName == "" {
		friendlyName = cfg.FriendlyName
	}
	srv := &tuner.Server{
		Addr:                addr,
		BaseURL:             baseURL,
		TunerCount:          cfg.TunerCount,
		LineupMaxChannels:   serveLineupCap,
		GuideNumberOffset:   cfg.GuideNumberOffset,
		DeviceID:            deviceID,
		FriendlyName:        friendlyName,
		StreamBufferBytes:   cfg.StreamBufferBytes,
		StreamTranscodeMode: cfg.StreamTranscodeMode,
		AutopilotStateFile:  cfg.AutopilotStateFile,
		ProviderUser:        cfg.ProviderUser,
		ProviderPass:        cfg.ProviderPass,
		ProviderBaseURL:     cfg.ProviderBaseURL,
		XMLTVSourceURL:      cfg.XMLTVURL,
		XMLTVTimeout:        cfg.XMLTVTimeout,
		XMLTVCacheTTL:       cfg.XMLTVCacheTTL,
		EpgPruneUnlinked:    cfg.EpgPruneUnlinked,
		FetchCFReject:       cfg.FetchCFReject,
		ProviderEPGEnabled:  cfg.ProviderEPGEnabled,
		ProviderEPGTimeout:  cfg.ProviderEPGTimeout,
		ProviderEPGCacheTTL: cfg.ProviderEPGCacheTTL,
	}
	srv.UpdateChannels(live)
	if cfg.XMLTVURL != "" {
		log.Printf("External XMLTV enabled: %s (timeout %v)", cfg.XMLTVURL, cfg.XMLTVTimeout)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hdhrConfig := &hdhomerun.Config{
		Enabled:      cfg.HDHREnabled,
		DeviceID:     cfg.HDHRDeviceID,
		TunerCount:   cfg.HDHRTunerCount,
		DiscoverPort: cfg.HDHRDiscoverPort,
		ControlPort:  cfg.HDHRControlPort,
		BaseURL:      cfg.BaseURL,
		FriendlyName: cfg.HDHRFriendlyName,
	}
	log.Printf("HDHomeRun config: enabled=%v, deviceID=0x%x, tuners=%d",
		hdhrConfig.Enabled, hdhrConfig.DeviceID, hdhrConfig.TunerCount)
	if hdhrConfig.Enabled {
		if hdhrConfig.BaseURL == "" {
			hdhrConfig.BaseURL = baseURL
		}
		streamFunc := func(ctx context.Context, channelID string) (io.ReadCloser, error) {
			return srv.GetStream(ctx, channelID)
		}
		server, err := hdhomerun.NewServer(hdhrConfig, streamFunc)
		if err != nil {
			log.Printf("HDHomeRun network mode failed to start: %v", err)
		} else {
			go func() {
				if err := server.Run(ctx); err != nil {
					log.Printf("HDHomeRun network server error: %v", err)
				}
			}()
			log.Printf("HDHomeRun network mode enabled (UDP 65001 + TCP 65001)")
		}
	}

	if err := srv.Run(ctx); err != nil {
		log.Printf("Serve failed: %v", err)
		os.Exit(1)
	}
}

func handleRun(cfg *config.Config, catalogPath, addr, baseURL, deviceID, friendlyName string, refresh time.Duration, skipIndex, skipHealth bool, registerPlex string, registerOnly bool, registerInterval time.Duration, mode string, registerEmby, registerJellyfin bool, embyInterval, jellyfinInterval time.Duration, embyStateFile, jellyfinStateFile string) {
	runCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	path := catalogPath
	if path == "" {
		path = cfg.CatalogPath
	}

	var runApiBase string
	var runProviderBase, runProviderUser, runProviderPass string
	if !skipIndex {
		log.Print("Refreshing catalog ...")
		res, err := fetchCatalog(cfg, "")
		if err != nil {
			log.Printf("Catalog refresh failed: %v", err)
			os.Exit(1)
		}
		runApiBase = res.APIBase
		runProviderBase = res.ProviderBase
		runProviderUser = res.ProviderUser
		runProviderPass = res.ProviderPass
		epgLinked, withBackups := catalogStats(res.Live)
		c := catalog.New()
		c.ReplaceWithLive(res.Movies, res.Series, res.Live)
		if err := c.Save(path); err != nil {
			log.Printf("Save catalog failed: %v", err)
			os.Exit(1)
		}
		log.Printf("Catalog saved: %d movies, %d series, %d live (%d EPG-linked, %d with backups)",
			len(res.Movies), len(res.Series), len(res.Live), epgLinked, withBackups)
	}

	var checkURL string
	if cfg.ProviderUser != "" && cfg.ProviderPass != "" {
		base := runApiBase
		if base == "" && !cfg.BlockCFProviders {
			if baseURLs := cfg.ProviderURLs(); len(baseURLs) > 0 {
				base = strings.TrimSuffix(baseURLs[0], "/")
			}
		}
		if base != "" {
			checkURL = base + "/player_api.php?username=" + url.QueryEscape(cfg.ProviderUser) + "&password=" + url.QueryEscape(cfg.ProviderPass)
		}
	}
	if !skipHealth && checkURL != "" {
		log.Print("Checking provider ...")
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := health.CheckProvider(ctx, checkURL); err != nil {
			log.Printf("Provider check failed: %v", err)
			os.Exit(1)
		}
		log.Print("Provider OK")
	}

	c := catalog.New()
	if err := c.Load(path); err != nil {
		log.Printf("Load catalog failed: %v", err)
		os.Exit(1)
	}
	live := c.SnapshotLive()
	applyRuntimeEPGRepairs(
		cfg,
		live,
		firstNonEmpty(runProviderBase, cfg.ProviderBaseURL),
		firstNonEmpty(runProviderUser, cfg.ProviderUser),
		firstNonEmpty(runProviderPass, cfg.ProviderPass),
	)
	channeldna.Assign(live)
	log.Printf("Loaded %d live channels from %s", len(live), path)

	if baseURL == "http://localhost:5004" && cfg.BaseURL != "" {
		baseURL = cfg.BaseURL
	}
	lineupCap := cfg.LineupMaxChannels
	switch mode {
	case "easy":
		lineupCap = tuner.PlexDVRWizardSafeMax
	case "full", "":
		if registerPlex != "" {
			lineupCap = tuner.NoLineupCap
		}
	default:
		log.Printf("Unknown -mode=%q; use easy or full", mode)
	}
	if deviceID == "" {
		deviceID = cfg.DeviceID
	}
	if friendlyName == "" {
		friendlyName = cfg.FriendlyName
	}
	srv := &tuner.Server{
		Addr:                addr,
		BaseURL:             baseURL,
		TunerCount:          cfg.TunerCount,
		LineupMaxChannels:   lineupCap,
		GuideNumberOffset:   cfg.GuideNumberOffset,
		DeviceID:            deviceID,
		FriendlyName:        friendlyName,
		StreamBufferBytes:   cfg.StreamBufferBytes,
		StreamTranscodeMode: cfg.StreamTranscodeMode,
		AutopilotStateFile:  cfg.AutopilotStateFile,
		ProviderUser:        firstNonEmpty(runProviderUser, cfg.ProviderUser),
		ProviderPass:        firstNonEmpty(runProviderPass, cfg.ProviderPass),
		ProviderBaseURL:     firstNonEmpty(runProviderBase, cfg.ProviderBaseURL),
		XMLTVSourceURL:      cfg.XMLTVURL,
		XMLTVTimeout:        cfg.XMLTVTimeout,
		XMLTVCacheTTL:       cfg.XMLTVCacheTTL,
		EpgPruneUnlinked:    cfg.EpgPruneUnlinked,
		FetchCFReject:       cfg.FetchCFReject,
		ProviderEPGEnabled:  cfg.ProviderEPGEnabled,
		ProviderEPGTimeout:  cfg.ProviderEPGTimeout,
		ProviderEPGCacheTTL: cfg.ProviderEPGCacheTTL,
	}
	srv.UpdateChannels(live)
	if cfg.XMLTVURL != "" {
		log.Printf("External XMLTV enabled: %s (timeout %v)", cfg.XMLTVURL, cfg.XMLTVTimeout)
	}

	credentials := cfg.ProviderUser != "" && cfg.ProviderPass != ""
	if credentials {
		sigHUP := make(chan os.Signal, 1)
		signal.Notify(sigHUP, syscall.SIGHUP)
		defer signal.Stop(sigHUP)

		var tickerC <-chan time.Time
		if refresh > 0 {
			ticker := time.NewTicker(refresh)
			defer ticker.Stop()
			tickerC = ticker.C
		}

		go func() {
			for {
				select {
				case <-runCtx.Done():
					return
				case <-tickerC:
					log.Print("Refreshing catalog (scheduled) ...")
				case <-sigHUP:
					log.Print("SIGHUP received — reloading catalog")
				}
				res, err := fetchCatalog(cfg, "")
				if err != nil {
					log.Printf("Scheduled refresh failed: %v", err)
					continue
				}
				cat := catalog.New()
				cat.ReplaceWithLive(res.Movies, res.Series, res.Live)
				if err := cat.Save(path); err != nil {
					log.Printf("Save catalog failed (scheduled refresh): %v", err)
					continue
				}
				channeldna.Assign(res.Live)
				srv.UpdateChannels(res.Live)
				log.Printf("Catalog refreshed: %d movies, %d series, %d live channels (lineup updated)",
					len(res.Movies), len(res.Series), len(res.Live))
			}
		}()
	}

	log.Printf("[PLEX-REG] START: runRegisterPlex=%q runMode=%q", registerPlex, mode)
	if registerPlex != "" && mode != "easy" {
		plexHost := os.Getenv("PLEX_HOST")
		plexToken := os.Getenv("PLEX_TOKEN")

		log.Printf("[PLEX-REG] Checking API registration: runRegisterPlex=%q mode=%q PLEX_HOST=%q PLEX_TOKEN present=%v",
			registerPlex, mode, plexHost, plexToken != "")

		apiRegistrationDone := false
		var registeredDeviceUUID string
		channelInfo := make([]plex.ChannelInfo, len(live))
		for i := range live {
			ch := &live[i]
			channelInfo[i] = plex.ChannelInfo{GuideNumber: ch.GuideNumber, GuideName: ch.GuideName}
		}
		if len(live) == 0 {
			log.Printf("[PLEX-REG] Skipping registration: 0 channels after filtering (no empty EPG tabs)")
		}
		if len(live) > 0 && plexHost != "" && plexToken != "" {
			log.Printf("[PLEX-REG] Attempting Plex API registration...")
			devUUID, _, regErr := plex.FullRegisterPlex(baseURL, plexHost, plexToken, cfg.FriendlyName, cfg.DeviceID, channelInfo)
			if regErr != nil {
				log.Printf("Plex API registration failed: %v (falling back to DB registration)", regErr)
			} else {
				log.Printf("Plex registered via API")
				apiRegistrationDone = true
				registeredDeviceUUID = devUUID
			}
		}

		if !apiRegistrationDone && len(live) > 0 {
			if registerPlex == "api" {
				log.Printf("[PLEX-REG] API registration failed; skipping file-based fallback (-register-plex=api is not a filesystem path)")
			} else {
				if err := plex.RegisterTuner(registerPlex, baseURL); err != nil {
					log.Printf("Register Plex failed: %v", err)
				} else {
					log.Printf("Plex DB updated at %s (DVR + XMLTV -> %s)", registerPlex, baseURL)
				}
				lineupChannels := make([]plex.LineupChannel, len(live))
				for i := range live {
					ch := &live[i]
					channelID := ch.ChannelID
					if channelID == "" {
						channelID = strconv.Itoa(i)
					}
					lineupChannels[i] = plex.LineupChannel{
						GuideNumber: ch.GuideNumber,
						GuideName:   ch.GuideName,
						URL:         baseURL + "/stream/" + channelID,
					}
				}
				if err := plex.SyncLineupToPlex(registerPlex, lineupChannels); err != nil {
					if err == plex.ErrLineupSchemaUnknown {
						log.Printf("Lineup sync skipped: %v (full lineup still served over HTTP; see docs/adr/0001-zero-touch-plex-lineup.md)", err)
					} else {
						log.Printf("Lineup sync failed: %v", err)
					}
				} else {
					log.Printf("Lineup synced to Plex: %d channels (no wizard needed)", len(lineupChannels))
				}

				dvrUUID := os.Getenv("IPTV_TUNERR_DVR_UUID")
				if dvrUUID == "" {
					dvrUUID = "iptvtunerr-" + cfg.DeviceID
				}
				epgChannels := make([]plex.EPGChannel, len(live))
				for i := range live {
					ch := &live[i]
					epgChannels[i] = plex.EPGChannel{GuideNumber: ch.GuideNumber, GuideName: ch.GuideName}
				}
				if err := plex.SyncEPGToPlex(registerPlex, dvrUUID, epgChannels); err != nil {
					log.Printf("EPG sync warning: %v (channels may not appear in guide without wizard)", err)
				} else {
					log.Printf("EPG synced to Plex: %d channels", len(epgChannels))
				}
			}
		}
		if registerOnly {
			log.Printf("Register-only mode: Plex DB updated, exiting without serving.")
			return
		}

		if apiRegistrationDone && registeredDeviceUUID != "" && registerInterval > 0 {
			watchdogCfg := plex.PlexAPIConfig{
				BaseURL:      baseURL,
				PlexHost:     plexHost,
				PlexToken:    plexToken,
				FriendlyName: cfg.FriendlyName,
				DeviceID:     cfg.DeviceID,
			}
			guideURL := baseURL + "/guide.xml"
			channelInfoCopy := channelInfo
			log.Printf("[dvr-watchdog] starting: device=%s interval=%v", registeredDeviceUUID, registerInterval)
			go plex.DVRWatchdog(runCtx, watchdogCfg, registeredDeviceUUID, guideURL, registerInterval, channelInfoCopy)
		}
	} else {
		_, _ = os.Stderr.WriteString("\n--- Plex one-time setup ---\n")
		_, _ = os.Stderr.WriteString("Easy (wizard): -mode=easy -> lineup capped at 479; add tuner in Plex, pick suggested guide (e.g. Rogers West).\n")
		_, _ = os.Stderr.WriteString("Full (zero-touch): -mode=full -register-plex=/path/to/Plex -> max feeds, no wizard.\n")
		_, _ = os.Stderr.WriteString("  Device / Base URL: " + baseURL + "   Guide: " + baseURL + "/guide.xml\n")
		_, _ = os.Stderr.WriteString("---\n\n")
	}

	registerMediaServer := func(serverType, host, token, stateFile string, interval time.Duration) {
		if host == "" || token == "" {
			envPrefix := strings.ToUpper(serverType)
			missing := "IPTV_TUNERR_" + envPrefix + "_HOST"
			if host != "" {
				missing = "IPTV_TUNERR_" + envPrefix + "_TOKEN"
			}
			log.Printf("[%s-reg] Skipping: %s is not set", serverType, missing)
			return
		}
		embyCfg := emby.Config{
			Host:         host,
			Token:        token,
			TunerURL:     baseURL,
			FriendlyName: cfg.FriendlyName,
			TunerCount:   cfg.TunerCount,
			ServerType:   serverType,
		}
		if err := emby.FullRegister(embyCfg, stateFile); err != nil {
			log.Printf("[%s-reg] Registration failed: %v", serverType, err)
		}
		if interval > 0 {
			log.Printf("[%s-watchdog] starting: interval=%v", serverType, interval)
			go emby.DVRWatchdog(runCtx, embyCfg, stateFile, interval)
		}
	}
	if registerEmby {
		registerMediaServer("emby", cfg.EmbyHost, cfg.EmbyToken, embyStateFile, embyInterval)
	}
	if registerJellyfin {
		registerMediaServer("jellyfin", cfg.JellyfinHost, cfg.JellyfinToken, jellyfinStateFile, jellyfinInterval)
	}

	if err := srv.Run(runCtx); err != nil {
		log.Printf("Tuner failed: %v", err)
		os.Exit(1)
	}
}
