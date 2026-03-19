package main

import (
	"log"
	"os"
	"strings"
	"time"

	"github.com/snapetech/iptvtunerr/internal/catalog"
	"github.com/snapetech/iptvtunerr/internal/config"
	"github.com/snapetech/iptvtunerr/internal/epglink"
	"github.com/snapetech/iptvtunerr/internal/refio"
)

func loadLiveReportCatalog(cfg *config.Config, catalogPath string) []catalog.LiveChannel {
	path := strings.TrimSpace(catalogPath)
	if path == "" {
		path = cfg.CatalogPath
	}
	c := catalog.New()
	if err := c.Load(path); err != nil {
		log.Printf("Load catalog %s: %v", path, err)
		os.Exit(1)
	}
	live := c.SnapshotLive()
	if len(live) == 0 {
		log.Printf("Catalog %s contains no live_channels", path)
		os.Exit(1)
	}
	return live
}

func loadOptionalMatchReport(live []catalog.LiveChannel, xmltvRef, aliasesRef string) *epglink.Report {
	xmltvRef = strings.TrimSpace(xmltvRef)
	if xmltvRef == "" {
		return nil
	}
	xmltvR, err := refio.Open(xmltvRef, 45*time.Second)
	if err != nil {
		log.Printf("Open XMLTV %s: %v", xmltvRef, err)
		os.Exit(1)
	}
	xmltvChans, err := epglink.ParseXMLTVChannels(xmltvR)
	_ = xmltvR.Close()
	if err != nil {
		log.Printf("Parse XMLTV channels: %v", err)
		os.Exit(1)
	}
	aliases := epglink.AliasOverrides{NameToXMLTVID: map[string]string{}}
	if p := strings.TrimSpace(aliasesRef); p != "" {
		aliasR, err := refio.Open(p, 45*time.Second)
		if err != nil {
			log.Printf("Open aliases %s: %v", p, err)
			os.Exit(1)
		}
		aliases, err = epglink.LoadAliasOverrides(aliasR)
		_ = aliasR.Close()
		if err != nil {
			log.Printf("Parse aliases: %v", err)
			os.Exit(1)
		}
	}
	rep := epglink.MatchLiveChannels(live, xmltvChans, aliases)
	return &rep
}
