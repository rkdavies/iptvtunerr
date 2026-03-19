package epgdoctor

import (
	"testing"
	"time"

	"github.com/snapetech/iptvtunerr/internal/epglink"
	"github.com/snapetech/iptvtunerr/internal/guidehealth"
)

func TestSuggestedAliasOverrides_OnlyHealthyNameExactMatches(t *testing.T) {
	gh := guidehealth.Report{
		Channels: []guidehealth.ChannelHealth{
			{ChannelID: "1", GuideName: "FOX News Channel US", HasRealProgrammes: true},
			{ChannelID: "2", GuideName: "Movie Max HD", HasRealProgrammes: false},
		},
	}
	links := &epglink.Report{
		Rows: []epglink.ChannelMatch{
			{ChannelID: "1", GuideName: "FOX News Channel US", Matched: true, MatchedXMLTV: "foxnews.us", Method: epglink.MatchNormalizedNameExact},
			{ChannelID: "2", GuideName: "Movie Max HD", Matched: true, MatchedXMLTV: "moviemax.us", Method: epglink.MatchNormalizedNameExact},
			{ChannelID: "3", GuideName: "CNN", Matched: true, MatchedXMLTV: "cnn.us", Method: epglink.MatchTVGIDExact},
		},
	}

	got := SuggestedAliasOverrides(gh, links)
	if len(got.NameToXMLTVID) != 1 {
		t.Fatalf("aliases len=%d want 1", len(got.NameToXMLTVID))
	}
	if got.NameToXMLTVID["FOX News Channel US"] != "foxnews.us" {
		t.Fatalf("unexpected aliases=%v", got.NameToXMLTVID)
	}
}

func TestBuild_SetsSuggestedAliasOverrideCount(t *testing.T) {
	gh := guidehealth.Report{
		Summary: guidehealth.Summary{
			TotalChannels:              1,
			ChannelsWithRealProgrammes: 1,
		},
		Channels: []guidehealth.ChannelHealth{
			{ChannelID: "1", GuideName: "FOX News Channel US", HasRealProgrammes: true},
		},
	}
	links := &epglink.Report{
		Matched: 1,
		Rows: []epglink.ChannelMatch{
			{ChannelID: "1", GuideName: "FOX News Channel US", Matched: true, MatchedXMLTV: "foxnews.us", Method: epglink.MatchNormalizedNameExact},
		},
	}

	rep := Build(gh, links, time.Now())
	if rep.Summary.SuggestedAliasOverrides != 1 {
		t.Fatalf("suggested_alias_overrides=%d want 1", rep.Summary.SuggestedAliasOverrides)
	}
}
