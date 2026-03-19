package channelreport

import (
	"testing"

	"github.com/snapetech/iptvtunerr/internal/catalog"
)

func TestBuildLeaderboard(t *testing.T) {
	live := []catalog.LiveChannel{
		{ChannelID: "1", GuideName: "Best News", GuideNumber: "101", TVGID: "best.news", EPGLinked: true, StreamURL: "http://a/1", StreamURLs: []string{"http://a/1", "http://b/1"}},
		{ChannelID: "2", GuideName: "Weak Guide", GuideNumber: "102", StreamURL: "http://a/2"},
		{ChannelID: "3", GuideName: "No Backup", GuideNumber: "103", TVGID: "nobackup.tv", EPGLinked: true, StreamURL: "http://a/3"},
	}

	rep := BuildLeaderboard(live, 2)
	if len(rep.HallOfFame) != 2 || rep.HallOfFame[0].GuideName != "Best News" {
		t.Fatalf("unexpected hall_of_fame=%+v", rep.HallOfFame)
	}
	if len(rep.HallOfShame) != 2 || rep.HallOfShame[0].GuideName != "Weak Guide" {
		t.Fatalf("unexpected hall_of_shame=%+v", rep.HallOfShame)
	}
	if len(rep.GuideRisks) != 2 || rep.GuideRisks[0].GuideName != "Weak Guide" {
		t.Fatalf("unexpected guide_risks=%+v", rep.GuideRisks)
	}
	if len(rep.StreamRisks) != 2 || rep.StreamRisks[0].GuideName != "No Backup" {
		t.Fatalf("unexpected stream_risks=%+v", rep.StreamRisks)
	}
}
