package channeldna

import (
	"testing"

	"github.com/snapetech/iptvtunerr/internal/catalog"
)

func TestBuildReportGroupsSharedDNA(t *testing.T) {
	live := []catalog.LiveChannel{
		{ChannelID: "1", GuideName: "FOX News", TVGID: "foxnews.us", StreamURLs: []string{"a"}},
		{ChannelID: "2", GuideName: "FOX News HD", TVGID: "foxnews.us", StreamURLs: []string{"b", "c"}},
		{ChannelID: "3", GuideName: "CNN", TVGID: "cnn.us", StreamURLs: []string{"d"}},
	}
	rep := BuildReport(live)
	if len(rep.Groups) != 2 {
		t.Fatalf("groups len=%d want 2", len(rep.Groups))
	}
	foundShared := false
	for _, g := range rep.Groups {
		if g.ChannelCount == 2 {
			foundShared = true
			if len(g.TVGIDs) != 1 || g.TVGIDs[0] != "foxnews.us" {
				t.Fatalf("unexpected tvgids: %+v", g.TVGIDs)
			}
		}
	}
	if !foundShared {
		t.Fatalf("expected shared DNA group")
	}
}
