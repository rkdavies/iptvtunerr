package channeldna

import (
	"sort"
	"strings"
	"time"

	"github.com/snapetech/iptvtunerr/internal/catalog"
)

type GroupMember struct {
	ChannelID      string `json:"channel_id"`
	GuideNumber    string `json:"guide_number"`
	GuideName      string `json:"guide_name"`
	TVGID          string `json:"tvg_id,omitempty"`
	StreamURLCount int    `json:"stream_url_count"`
}

type Group struct {
	DNAID        string        `json:"dna_id"`
	DisplayName  string        `json:"display_name"`
	TVGIDs       []string      `json:"tvg_ids,omitempty"`
	ChannelCount int           `json:"channel_count"`
	Members      []GroupMember `json:"members"`
}

type Report struct {
	GeneratedAt string  `json:"generated_at"`
	Groups      []Group `json:"groups"`
}

func BuildReport(live []catalog.LiveChannel) Report {
	byDNA := map[string]*Group{}
	for _, ch := range live {
		dna := Compute(ch)
		g := byDNA[dna]
		if g == nil {
			g = &Group{
				DNAID:   dna,
				Members: []GroupMember{},
			}
			byDNA[dna] = g
		}
		g.Members = append(g.Members, GroupMember{
			ChannelID:      ch.ChannelID,
			GuideNumber:    ch.GuideNumber,
			GuideName:      ch.GuideName,
			TVGID:          ch.TVGID,
			StreamURLCount: len(ch.StreamURLs),
		})
		if tvg := strings.TrimSpace(ch.TVGID); tvg != "" && !containsString(g.TVGIDs, tvg) {
			g.TVGIDs = append(g.TVGIDs, tvg)
		}
		if g.DisplayName == "" && strings.TrimSpace(ch.GuideName) != "" {
			g.DisplayName = strings.TrimSpace(ch.GuideName)
		}
	}
	out := Report{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Groups:      make([]Group, 0, len(byDNA)),
	}
	for _, g := range byDNA {
		sort.SliceStable(g.Members, func(i, j int) bool {
			if g.Members[i].GuideName == g.Members[j].GuideName {
				return g.Members[i].ChannelID < g.Members[j].ChannelID
			}
			return g.Members[i].GuideName < g.Members[j].GuideName
		})
		sort.Strings(g.TVGIDs)
		g.ChannelCount = len(g.Members)
		out.Groups = append(out.Groups, *g)
	}
	sort.SliceStable(out.Groups, func(i, j int) bool {
		if out.Groups[i].DisplayName == out.Groups[j].DisplayName {
			return out.Groups[i].DNAID < out.Groups[j].DNAID
		}
		return out.Groups[i].DisplayName < out.Groups[j].DisplayName
	})
	return out
}

func containsString(in []string, want string) bool {
	for _, s := range in {
		if s == want {
			return true
		}
	}
	return false
}
