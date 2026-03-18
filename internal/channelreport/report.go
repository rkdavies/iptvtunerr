package channelreport

import (
	"sort"
	"strings"
	"time"

	"github.com/snapetech/iptvtunerr/internal/catalog"
	"github.com/snapetech/iptvtunerr/internal/epglink"
)

type Tier string

const (
	TierExcellent Tier = "excellent"
	TierGood      Tier = "good"
	TierFair      Tier = "fair"
	TierPoor      Tier = "poor"
)

type Report struct {
	GeneratedAt string          `json:"generated_at"`
	Summary     Summary         `json:"summary"`
	Channels    []ChannelHealth `json:"channels"`
}

type Summary struct {
	TotalChannels     int            `json:"total_channels"`
	EPGLinkedChannels int            `json:"epg_linked_channels"`
	MissingTVGID      int            `json:"missing_tvg_id"`
	NoBackupStreams   int            `json:"no_backup_streams"`
	AverageScore      int            `json:"average_score"`
	TierCounts        map[Tier]int   `json:"tier_counts"`
	TopOpportunities  []string       `json:"top_opportunities"`
	EPGMatchSummary   *EPGMatchStats `json:"epg_match_summary,omitempty"`
}

type EPGMatchStats struct {
	TotalMatched   int            `json:"total_matched"`
	TotalUnmatched int            `json:"total_unmatched"`
	Methods        map[string]int `json:"methods"`
}

type ChannelHealth struct {
	ChannelID         string   `json:"channel_id"`
	GuideNumber       string   `json:"guide_number"`
	GuideName         string   `json:"guide_name"`
	TVGID             string   `json:"tvg_id,omitempty"`
	EPGLinked         bool     `json:"epg_linked"`
	PrimaryStreamURL  string   `json:"primary_stream_url"`
	StreamURLCount    int      `json:"stream_url_count"`
	BackupStreamCount int      `json:"backup_stream_count"`
	GuideConfidence   int      `json:"guide_confidence"`
	StreamResilience  int      `json:"stream_resilience"`
	Score             int      `json:"score"`
	Tier              Tier     `json:"tier"`
	EPGMatchMethod    string   `json:"epg_match_method,omitempty"`
	EPGMatchReason    string   `json:"epg_match_reason,omitempty"`
	Strengths         []string `json:"strengths"`
	Risks             []string `json:"risks"`
	NextActions       []string `json:"next_actions"`
}

func Build(live []catalog.LiveChannel) Report {
	out := Report{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		Summary: Summary{
			TotalChannels:    len(live),
			TierCounts:       map[Tier]int{},
			TopOpportunities: []string{},
		},
		Channels: make([]ChannelHealth, 0, len(live)),
	}

	opportunityCounts := map[string]int{}
	totalScore := 0
	for _, ch := range live {
		row := scoreChannel(ch)
		out.Channels = append(out.Channels, row)
		out.Summary.TierCounts[row.Tier]++
		if row.EPGLinked {
			out.Summary.EPGLinkedChannels++
		}
		if strings.TrimSpace(row.TVGID) == "" {
			out.Summary.MissingTVGID++
		}
		if row.BackupStreamCount == 0 {
			out.Summary.NoBackupStreams++
		}
		totalScore += row.Score
		for _, next := range row.NextActions {
			opportunityCounts[next]++
		}
	}
	if len(out.Channels) > 0 {
		out.Summary.AverageScore = totalScore / len(out.Channels)
	}

	type kv struct {
		Key   string
		Count int
	}
	top := make([]kv, 0, len(opportunityCounts))
	for k, v := range opportunityCounts {
		top = append(top, kv{Key: k, Count: v})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].Count == top[j].Count {
			return top[i].Key < top[j].Key
		}
		return top[i].Count > top[j].Count
	})
	for i := 0; i < len(top) && i < 5; i++ {
		out.Summary.TopOpportunities = append(out.Summary.TopOpportunities, top[i].Key)
	}
	return out
}

func AttachEPGMatchReport(report *Report, rep epglink.Report) {
	if report == nil {
		return
	}
	byChannelID := make(map[string]epglink.ChannelMatch, len(rep.Rows))
	for _, row := range rep.Rows {
		byChannelID[row.ChannelID] = row
	}
	for i := range report.Channels {
		row, ok := byChannelID[report.Channels[i].ChannelID]
		if !ok {
			continue
		}
		if row.Matched {
			report.Channels[i].EPGMatchMethod = string(row.Method)
			report.Channels[i].EPGMatchReason = "Matched XMLTV"
			switch row.Method {
			case epglink.MatchTVGIDExact:
				report.Channels[i].Strengths = appendUnique(report.Channels[i].Strengths, "Exact TVGID/XMLTV match")
			case epglink.MatchAliasExact:
				report.Channels[i].Strengths = appendUnique(report.Channels[i].Strengths, "Alias-assisted XMLTV match")
			case epglink.MatchNormalizedNameExact:
				report.Channels[i].Strengths = appendUnique(report.Channels[i].Strengths, "Normalized-name XMLTV match")
				report.Channels[i].NextActions = appendUnique(report.Channels[i].NextActions, "Consider persisting this repaired TVGID for long-term stability")
			}
		} else {
			report.Channels[i].EPGMatchReason = row.Reason
			report.Channels[i].Risks = appendUnique(report.Channels[i].Risks, "No deterministic XMLTV match")
			report.Channels[i].NextActions = appendUnique(report.Channels[i].NextActions, "Review XMLTV match report for alias or naming fixes")
		}
	}
	report.Summary.EPGMatchSummary = &EPGMatchStats{
		TotalMatched:   rep.Matched,
		TotalUnmatched: rep.Unmatched,
		Methods:        rep.Methods,
	}
}

func scoreChannel(ch catalog.LiveChannel) ChannelHealth {
	row := ChannelHealth{
		ChannelID:         ch.ChannelID,
		GuideNumber:       ch.GuideNumber,
		GuideName:         ch.GuideName,
		TVGID:             ch.TVGID,
		EPGLinked:         ch.EPGLinked,
		PrimaryStreamURL:  ch.StreamURL,
		StreamURLCount:    len(ch.StreamURLs),
		BackupStreamCount: max(0, len(ch.StreamURLs)-1),
		Strengths:         []string{},
		Risks:             []string{},
		NextActions:       []string{},
	}

	row.GuideConfidence = GuideConfidence(ch)
	row.StreamResilience = StreamResilience(ch)
	annotateGuideSignals(&row, ch)
	annotateStreamSignals(&row, ch)

	row.Score = row.GuideConfidence + row.StreamResilience
	row.Tier = tierForScore(row.Score)
	return row
}

func GuideConfidence(ch catalog.LiveChannel) int {
	guide := 0
	if ch.EPGLinked {
		guide += 25
	}
	if strings.TrimSpace(ch.TVGID) != "" {
		guide += 15
	}
	if strings.TrimSpace(ch.GuideNumber) != "" {
		guide += 10
	}
	return clamp(guide, 0, 50)
}

func StreamResilience(ch catalog.LiveChannel) int {
	stream := 0
	if strings.TrimSpace(ch.StreamURL) != "" {
		stream += 20
	}
	if len(ch.StreamURLs) > 1 {
		stream += 20
	}
	if strings.TrimSpace(ch.ChannelID) != "" {
		stream += 10
	}
	return clamp(stream, 0, 50)
}

func Score(ch catalog.LiveChannel) int {
	return GuideConfidence(ch) + StreamResilience(ch)
}

func annotateGuideSignals(row *ChannelHealth, ch catalog.LiveChannel) {
	if ch.EPGLinked {
		row.Strengths = append(row.Strengths, "EPG-linked")
	} else {
		row.Risks = append(row.Risks, "Guide mapping unresolved")
		row.NextActions = appendUnique(row.NextActions, "Add XMLTV alias overrides or improve TVGID matching")
	}
	if strings.TrimSpace(ch.TVGID) != "" {
		row.Strengths = appendUnique(row.Strengths, "Has TVGID")
	} else {
		row.Risks = appendUnique(row.Risks, "Missing TVGID")
		row.NextActions = appendUnique(row.NextActions, "Repair missing TVGID before relying on guide-only filtering")
	}
	if strings.TrimSpace(ch.GuideNumber) != "" {
		row.Strengths = appendUnique(row.Strengths, "Has guide number")
	} else {
		row.Risks = appendUnique(row.Risks, "Missing guide number")
		row.NextActions = appendUnique(row.NextActions, "Assign a stable guide number for cleaner DVR mapping")
	}
}

func annotateStreamSignals(row *ChannelHealth, ch catalog.LiveChannel) {
	if strings.TrimSpace(ch.StreamURL) != "" {
		row.Strengths = appendUnique(row.Strengths, "Primary stream present")
	} else {
		row.Risks = appendUnique(row.Risks, "Missing primary stream URL")
		row.NextActions = appendUnique(row.NextActions, "Re-index or inspect provider feed for missing stream URL")
	}
	if len(ch.StreamURLs) > 1 {
		row.Strengths = appendUnique(row.Strengths, "Backup streams available")
	} else {
		row.Risks = appendUnique(row.Risks, "No backup streams")
		row.NextActions = appendUnique(row.NextActions, "Add multi-host or multi-subscription fallbacks for this channel")
	}
	if strings.TrimSpace(ch.ChannelID) != "" {
		row.Strengths = appendUnique(row.Strengths, "Stable channel ID present")
	} else {
		row.Risks = appendUnique(row.Risks, "Missing stable channel ID")
		row.NextActions = appendUnique(row.NextActions, "Preserve a stable channel ID to reduce client remap churn")
	}
}

func tierForScore(score int) Tier {
	switch {
	case score >= 85:
		return TierExcellent
	case score >= 70:
		return TierGood
	case score >= 50:
		return TierFair
	default:
		return TierPoor
	}
}

func appendUnique(in []string, v string) []string {
	for _, existing := range in {
		if existing == v {
			return in
		}
	}
	return append(in, v)
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
