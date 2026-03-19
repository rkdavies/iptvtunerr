package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/snapetech/iptvtunerr/internal/channeldna"
	"github.com/snapetech/iptvtunerr/internal/channelreport"
	"github.com/snapetech/iptvtunerr/internal/config"
	"github.com/snapetech/iptvtunerr/internal/tuner"
)

func reportCommands() []commandSpec {
	channelReportCmd := flag.NewFlagSet("channel-report", flag.ExitOnError)
	channelReportCatalog := channelReportCmd.String("catalog", "", "Input catalog.json (default: IPTV_TUNERR_CATALOG)")
	channelReportXMLTV := channelReportCmd.String("xmltv", "", "Optional XMLTV file path or http(s) URL to enrich report with exact/alias/name match details")
	channelReportAliases := channelReportCmd.String("aliases", "", "Optional alias override JSON (name_to_xmltv_id map)")
	channelReportOut := channelReportCmd.String("out", "", "Optional JSON report output path (default: stdout)")

	channelLeaderboardCmd := flag.NewFlagSet("channel-leaderboard", flag.ExitOnError)
	channelLeaderboardCatalog := channelLeaderboardCmd.String("catalog", "", "Input catalog.json (default: IPTV_TUNERR_CATALOG)")
	channelLeaderboardXMLTV := channelLeaderboardCmd.String("xmltv", "", "Optional XMLTV file path or http(s) URL to enrich leaderboard with exact/alias/name match details")
	channelLeaderboardAliases := channelLeaderboardCmd.String("aliases", "", "Optional alias override JSON (name_to_xmltv_id map)")
	channelLeaderboardLimit := channelLeaderboardCmd.Int("limit", 10, "Max rows per leaderboard bucket")
	channelLeaderboardOut := channelLeaderboardCmd.String("out", "", "Optional JSON report output path (default: stdout)")

	channelDNAReportCmd := flag.NewFlagSet("channel-dna-report", flag.ExitOnError)
	channelDNAReportCatalog := channelDNAReportCmd.String("catalog", "", "Input catalog.json (default: IPTV_TUNERR_CATALOG)")
	channelDNAReportOut := channelDNAReportCmd.String("out", "", "Optional JSON report output path (default: stdout)")

	ghostHunterCmd := flag.NewFlagSet("ghost-hunter", flag.ExitOnError)
	ghostHunterPMSURL := ghostHunterCmd.String("pms-url", strings.TrimSpace(os.Getenv("IPTV_TUNERR_PMS_URL")), "Plex base URL")
	ghostHunterToken := ghostHunterCmd.String("token", strings.TrimSpace(os.Getenv("IPTV_TUNERR_PMS_TOKEN")), "Plex token")
	ghostHunterObserve := ghostHunterCmd.Duration("observe", 4*time.Second, "Observation window before classifying stale sessions")
	ghostHunterPoll := ghostHunterCmd.Duration("poll", time.Second, "Poll interval while observing")
	ghostHunterStop := ghostHunterCmd.Bool("stop", false, "Stop stale visible transcode sessions after classification")
	ghostHunterMachineID := ghostHunterCmd.String("machine-id", strings.TrimSpace(os.Getenv("IPTV_TUNERR_PLEX_SESSION_REAPER_MACHINE_ID")), "Optional client machineIdentifier scope")
	ghostHunterPlayerIP := ghostHunterCmd.String("player-ip", strings.TrimSpace(os.Getenv("IPTV_TUNERR_PLEX_SESSION_REAPER_PLAYER_IP")), "Optional player IP scope")

	catchupCapsulesCmd := flag.NewFlagSet("catchup-capsules", flag.ExitOnError)
	catchupCapsulesCatalog := catchupCapsulesCmd.String("catalog", "", "Input catalog.json (default: IPTV_TUNERR_CATALOG)")
	catchupCapsulesXMLTV := catchupCapsulesCmd.String("xmltv", "", "Guide/XMLTV file path or http(s) URL (required; /guide.xml works well)")
	catchupCapsulesHorizon := catchupCapsulesCmd.Duration("horizon", 3*time.Hour, "How far ahead to include candidate programme windows")
	catchupCapsulesLimit := catchupCapsulesCmd.Int("limit", 20, "Max capsules to export")
	catchupCapsulesOut := catchupCapsulesCmd.String("out", "", "Optional JSON output path (default: stdout)")
	catchupCapsulesLayoutDir := catchupCapsulesCmd.String("layout-dir", "", "Optional output directory for lane-split capsule JSON files plus manifest.json")
	catchupCapsulesGuidePolicy := catchupCapsulesCmd.String("guide-policy", strings.TrimSpace(os.Getenv("IPTV_TUNERR_CATCHUP_GUIDE_POLICY")), "Optional guide-quality policy: off|healthy|strict")
	catchupCapsulesReplayTemplate := catchupCapsulesCmd.String("replay-url-template", strings.TrimSpace(os.Getenv("IPTV_TUNERR_CATCHUP_REPLAY_URL_TEMPLATE")), "Optional source-backed replay URL template; when set, capsules include replay URLs instead of launcher-only metadata")

	return []commandSpec{
		{Name: "channel-report", Section: "Guide/EPG", Summary: "Channel intelligence report: score stream resilience + guide confidence", FlagSet: channelReportCmd, Run: func(cfg *config.Config, args []string) {
			_ = channelReportCmd.Parse(args)
			handleChannelReport(cfg, *channelReportCatalog, *channelReportXMLTV, *channelReportAliases, *channelReportOut)
		}},
		{Name: "channel-leaderboard", Section: "Guide/EPG", Summary: "Hall of fame/shame plus guide-risk and stream-risk channel leaderboards", FlagSet: channelLeaderboardCmd, Run: func(cfg *config.Config, args []string) {
			_ = channelLeaderboardCmd.Parse(args)
			handleChannelLeaderboard(cfg, *channelLeaderboardCatalog, *channelLeaderboardXMLTV, *channelLeaderboardAliases, *channelLeaderboardLimit, *channelLeaderboardOut)
		}},
		{Name: "channel-dna-report", Section: "Guide/EPG", Summary: "Group live channels by stable dna_id identity", FlagSet: channelDNAReportCmd, Run: func(cfg *config.Config, args []string) {
			_ = channelDNAReportCmd.Parse(args)
			handleChannelDNAReport(cfg, *channelDNAReportCatalog, *channelDNAReportOut)
		}},
		{Name: "ghost-hunter", Section: "Guide/EPG", Summary: "Observe Plex Live TV sessions, classify stalls, optionally stop stale ones", FlagSet: ghostHunterCmd, Run: func(_ *config.Config, args []string) {
			_ = ghostHunterCmd.Parse(args)
			handleGhostHunter(*ghostHunterPMSURL, *ghostHunterToken, *ghostHunterObserve, *ghostHunterPoll, *ghostHunterStop, *ghostHunterMachineID, *ghostHunterPlayerIP)
		}},
		{Name: "catchup-capsules", Section: "Guide/EPG", Summary: "Export near-live capsule candidates from guide XML/guide.xml", FlagSet: catchupCapsulesCmd, Run: func(cfg *config.Config, args []string) {
			_ = catchupCapsulesCmd.Parse(args)
			handleCatchupCapsules(cfg, *catchupCapsulesCatalog, *catchupCapsulesXMLTV, *catchupCapsulesHorizon, *catchupCapsulesLimit, *catchupCapsulesOut, *catchupCapsulesLayoutDir, *catchupCapsulesGuidePolicy, *catchupCapsulesReplayTemplate)
		}},
	}
}

func handleChannelReport(cfg *config.Config, catalogPath, xmltvRef, aliasesRef, outPath string) {
	live := loadLiveReportCatalog(cfg, catalogPath)
	rep := channelreport.Build(live)
	if matchRep := loadOptionalMatchReport(live, xmltvRef, aliasesRef); matchRep != nil {
		channelreport.AttachEPGMatchReport(&rep, *matchRep)
		log.Print(matchRep.SummaryString())
	}
	data, _ := json.MarshalIndent(rep, "", "  ")
	if p := strings.TrimSpace(outPath); p != "" {
		if err := os.WriteFile(p, data, 0o600); err != nil {
			log.Printf("Write channel report %s: %v", p, err)
			os.Exit(1)
		}
		log.Printf("Wrote channel report: %s", p)
	} else {
		fmt.Println(string(data))
	}
}

func handleChannelLeaderboard(cfg *config.Config, catalogPath, xmltvRef, aliasesRef string, limit int, outPath string) {
	live := loadLiveReportCatalog(cfg, catalogPath)
	rep := channelreport.Build(live)
	if matchRep := loadOptionalMatchReport(live, xmltvRef, aliasesRef); matchRep != nil {
		channelreport.AttachEPGMatchReport(&rep, *matchRep)
		log.Print(matchRep.SummaryString())
	}
	leaderboard := channelreport.BuildLeaderboardFromReport(rep, limit)
	data, _ := json.MarshalIndent(leaderboard, "", "  ")
	if p := strings.TrimSpace(outPath); p != "" {
		if err := os.WriteFile(p, data, 0o600); err != nil {
			log.Printf("Write channel leaderboard %s: %v", p, err)
			os.Exit(1)
		}
		log.Printf("Wrote channel leaderboard: %s", p)
	} else {
		fmt.Println(string(data))
	}
}

func handleChannelDNAReport(cfg *config.Config, catalogPath, outPath string) {
	rep := channeldna.BuildReport(loadLiveReportCatalog(cfg, catalogPath))
	data, _ := json.MarshalIndent(rep, "", "  ")
	if p := strings.TrimSpace(outPath); p != "" {
		if err := os.WriteFile(p, data, 0o600); err != nil {
			log.Printf("Write channel DNA report %s: %v", p, err)
			os.Exit(1)
		}
		log.Printf("Wrote channel DNA report: %s", p)
	} else {
		fmt.Println(string(data))
	}
}

func handleGhostHunter(pmsURL, token string, observe, poll time.Duration, stop bool, machineID, playerIP string) {
	ghCfg := tuner.NewGhostHunterConfigFromEnv()
	ghCfg.PMSURL = strings.TrimSpace(pmsURL)
	ghCfg.Token = strings.TrimSpace(token)
	ghCfg.ObserveWindow = observe
	ghCfg.PollInterval = poll
	ghCfg.ScopeMachineID = strings.TrimSpace(machineID)
	ghCfg.ScopePlayerIP = strings.TrimSpace(playerIP)
	rep, err := tuner.RunGhostHunter(context.Background(), ghCfg, stop, nil)
	if err != nil {
		log.Printf("Ghost Hunter failed: %v", err)
		os.Exit(1)
	}
	data, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Println(string(data))
}

func handleCatchupCapsules(cfg *config.Config, catalogPath, xmltvRef string, horizon time.Duration, limit int, outPath, layoutDir, guidePolicy, replayTemplate string) {
	path := strings.TrimSpace(catalogPath)
	if path == "" {
		path = cfg.CatalogPath
	}
	if strings.TrimSpace(xmltvRef) == "" {
		log.Print("Set -xmltv to a local file or http(s) guide/XMLTV URL")
		os.Exit(1)
	}
	rep, err := buildCatchupCapsulePreviewFromRef(path, strings.TrimSpace(xmltvRef), horizon, limit, guidePolicy)
	if err != nil {
		log.Printf("Build catchup capsule preview failed: %v", err)
		os.Exit(1)
	}
	rep = tuner.ApplyCatchupReplayTemplate(rep, replayTemplate)
	out, _ := json.MarshalIndent(rep, "", "  ")
	if dir := strings.TrimSpace(layoutDir); dir != "" {
		written, err := tuner.SaveCatchupCapsuleLanes(dir, rep)
		if err != nil {
			log.Printf("Write catchup capsule layout %s: %v", dir, err)
			os.Exit(1)
		}
		log.Printf("Wrote catchup capsule layout: %s (%d lane files)", dir, len(written))
	}
	if p := strings.TrimSpace(outPath); p != "" {
		if err := os.WriteFile(p, out, 0o600); err != nil {
			log.Printf("Write catchup capsules %s: %v", p, err)
			os.Exit(1)
		}
		log.Printf("Wrote catchup capsules: %s", p)
	} else {
		fmt.Println(string(out))
	}
}
