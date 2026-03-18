package channelreport

import (
	"testing"

	"github.com/snapetech/iptvtunerr/internal/catalog"
	"github.com/snapetech/iptvtunerr/internal/epglink"
)

func TestBuildScoresChannelsAndSummarizesOpportunities(t *testing.T) {
	rep := Build([]catalog.LiveChannel{
		{ChannelID: "1", GuideNumber: "101", GuideName: "FOX News", TVGID: "foxnews.us", EPGLinked: true, StreamURL: "http://a/1", StreamURLs: []string{"http://a/1", "http://b/1"}},
		{ChannelID: "2", GuideName: "Mystery Feed", StreamURL: "http://a/2", StreamURLs: []string{"http://a/2"}},
	})
	if rep.Summary.TotalChannels != 2 {
		t.Fatalf("total=%d want 2", rep.Summary.TotalChannels)
	}
	if rep.Summary.EPGLinkedChannels != 1 {
		t.Fatalf("epg_linked=%d want 1", rep.Summary.EPGLinkedChannels)
	}
	if rep.Summary.NoBackupStreams != 1 {
		t.Fatalf("no_backup_streams=%d want 1", rep.Summary.NoBackupStreams)
	}
	if rep.Channels[0].Tier != TierExcellent {
		t.Fatalf("first tier=%s want %s", rep.Channels[0].Tier, TierExcellent)
	}
	if rep.Channels[1].Tier == TierExcellent {
		t.Fatalf("second tier unexpectedly excellent: %+v", rep.Channels[1])
	}
}

func TestAttachEPGMatchReportAddsMatchSignals(t *testing.T) {
	rep := Build([]catalog.LiveChannel{
		{ChannelID: "1", GuideName: "FOX News", TVGID: "foxnews.us", EPGLinked: true, StreamURL: "http://a/1", StreamURLs: []string{"http://a/1"}},
		{ChannelID: "2", GuideName: "Unknown", StreamURL: "http://a/2", StreamURLs: []string{"http://a/2"}},
	})
	match := epglink.Report{
		Matched:   1,
		Unmatched: 1,
		Methods:   map[string]int{"tvg_id_exact": 1},
		Rows: []epglink.ChannelMatch{
			{ChannelID: "1", Matched: true, Method: epglink.MatchTVGIDExact, MatchedXMLTV: "foxnews.us"},
			{ChannelID: "2", Matched: false, Reason: "no deterministic match"},
		},
	}
	AttachEPGMatchReport(&rep, match)
	if rep.Summary.EPGMatchSummary == nil || rep.Summary.EPGMatchSummary.TotalMatched != 1 {
		t.Fatalf("epg summary missing or wrong: %+v", rep.Summary.EPGMatchSummary)
	}
	if rep.Channels[0].EPGMatchMethod != "tvg_id_exact" {
		t.Fatalf("match method=%q want tvg_id_exact", rep.Channels[0].EPGMatchMethod)
	}
	if rep.Channels[1].EPGMatchReason != "no deterministic match" {
		t.Fatalf("match reason=%q", rep.Channels[1].EPGMatchReason)
	}
}
