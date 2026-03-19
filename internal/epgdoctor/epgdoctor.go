package epgdoctor

import (
	"sort"
	"strings"
	"time"

	"github.com/snapetech/iptvtunerr/internal/epglink"
	"github.com/snapetech/iptvtunerr/internal/guidehealth"
)

type Report struct {
	GeneratedAt string             `json:"generated_at"`
	SourceReady bool               `json:"source_ready"`
	Summary     Summary            `json:"summary"`
	GuideHealth guidehealth.Report `json:"guide_health"`
	LinkReport  *epglink.Report    `json:"link_report,omitempty"`
}

type Summary struct {
	TotalChannels              int      `json:"total_channels"`
	MatchedChannels            int      `json:"matched_channels"`
	UnmatchedChannels          int      `json:"unmatched_channels"`
	ChannelsWithRealProgrammes int      `json:"channels_with_real_programmes"`
	PlaceholderOnlyChannels    int      `json:"placeholder_only_channels"`
	NoProgrammeChannels        int      `json:"no_programme_channels"`
	SuggestedAliasOverrides    int      `json:"suggested_alias_overrides"`
	TopFindings                []string `json:"top_findings"`
}

func Build(gh guidehealth.Report, links *epglink.Report, now time.Time) Report {
	out := Report{
		GeneratedAt: now.UTC().Format(time.RFC3339),
		SourceReady: gh.SourceReady,
		GuideHealth: gh,
		LinkReport:  links,
		Summary: Summary{
			TotalChannels:              gh.Summary.TotalChannels,
			ChannelsWithRealProgrammes: gh.Summary.ChannelsWithRealProgrammes,
			PlaceholderOnlyChannels:    gh.Summary.PlaceholderOnlyChannels,
			NoProgrammeChannels:        gh.Summary.NoProgrammeChannels,
			TopFindings:                []string{},
		},
	}
	if links != nil {
		out.Summary.MatchedChannels = links.Matched
		out.Summary.UnmatchedChannels = links.Unmatched
	}
	findings := []string{}
	if gh.Summary.PlaceholderOnlyChannels > 0 {
		findings = append(findings, "Some channels are guide-linked but still only serve placeholder rows")
	}
	if gh.Summary.NoProgrammeChannels > 0 {
		findings = append(findings, "Some channels have no programme rows in the merged guide")
	}
	if links != nil && links.Unmatched > 0 {
		findings = append(findings, "Some channels still have no deterministic XMLTV match")
	}
	if gh.Summary.ChannelsWithRealProgrammes == gh.Summary.TotalChannels && gh.Summary.TotalChannels > 0 {
		findings = append(findings, "All channels in this report have real guide programme coverage")
	}
	out.Summary.SuggestedAliasOverrides = len(SuggestedAliasOverrides(gh, links).NameToXMLTVID)
	if out.Summary.SuggestedAliasOverrides > 0 {
		findings = append(findings, "High-confidence alias overrides can be exported from normalized-name matches")
	}
	out.Summary.TopFindings = findings
	return out
}

func SortedMethodCounts(rep *epglink.Report) []string {
	if rep == nil {
		return nil
	}
	type kv struct {
		Key   string
		Count int
	}
	rows := make([]kv, 0, len(rep.Methods))
	for k, v := range rep.Methods {
		rows = append(rows, kv{k, v})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Count == rows[j].Count {
			return rows[i].Key < rows[j].Key
		}
		return rows[i].Count > rows[j].Count
	})
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Key)
	}
	return out
}

func SuggestedAliasOverrides(gh guidehealth.Report, links *epglink.Report) epglink.AliasOverrides {
	out := epglink.AliasOverrides{NameToXMLTVID: map[string]string{}}
	if links == nil || len(links.Rows) == 0 {
		return out
	}
	healthByChannelID := make(map[string]guidehealth.ChannelHealth, len(gh.Channels))
	for _, row := range gh.Channels {
		healthByChannelID[row.ChannelID] = row
	}
	conflicts := map[string]bool{}
	for _, row := range links.Rows {
		if !row.Matched || row.Method != epglink.MatchNormalizedNameExact {
			continue
		}
		if strings.TrimSpace(row.GuideName) == "" || strings.TrimSpace(row.MatchedXMLTV) == "" {
			continue
		}
		health, ok := healthByChannelID[row.ChannelID]
		if ok && !health.HasRealProgrammes {
			continue
		}
		key := strings.TrimSpace(row.GuideName)
		if existing, exists := out.NameToXMLTVID[key]; exists && existing != row.MatchedXMLTV {
			conflicts[key] = true
			delete(out.NameToXMLTVID, key)
			continue
		}
		if !conflicts[key] {
			out.NameToXMLTVID[key] = row.MatchedXMLTV
		}
	}
	return out
}
