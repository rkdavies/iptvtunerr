package tuner

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestApplyCatchupReplayTemplate(t *testing.T) {
	rep := CatchupCapsulePreview{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		SourceReady: true,
		Capsules: []CatchupCapsule{
			{
				CapsuleID:    "dna:202603181200:fox-news",
				DNAID:        "dna-fox",
				ChannelID:    "1001",
				GuideNumber:  "101",
				ChannelName:  "FOX News",
				Title:        "Morning News",
				Start:        "2026-03-18T12:00:00Z",
				Stop:         "2026-03-18T13:00:00Z",
				DurationMins: 60,
			},
		},
	}

	got := ApplyCatchupReplayTemplate(rep, "http://provider.example/timeshift/{channel_id}/{duration_mins}/{start_xtream}")
	if got.ReplayMode != "replay" {
		t.Fatalf("replay_mode=%q want replay", got.ReplayMode)
	}
	if len(got.Capsules) != 1 {
		t.Fatalf("capsules len=%d want 1", len(got.Capsules))
	}
	if want := "http://provider.example/timeshift/1001/60/2026-03-18:12-00"; got.Capsules[0].ReplayURL != want {
		t.Fatalf("replay_url=%q want %q", got.Capsules[0].ReplayURL, want)
	}
}

func TestSaveCatchupCapsuleLibraryLayout_UsesReplayURLWhenPresent(t *testing.T) {
	dir := t.TempDir()
	preview := CatchupCapsulePreview{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		SourceReady: true,
		ReplayMode:  "replay",
		Capsules: []CatchupCapsule{
			{
				CapsuleID:    "capsule-1",
				ChannelID:    "1001",
				ChannelName:  "FOX News",
				Title:        "Morning News",
				Lane:         "general",
				State:        "ready",
				Start:        "2026-03-18T12:00:00Z",
				Stop:         "2026-03-18T13:00:00Z",
				ReplayMode:   "replay",
				ReplayURL:    "http://provider.example/replay/1001",
				DurationMins: 60,
			},
		},
	}
	manifest, err := SaveCatchupCapsuleLibraryLayout(dir, "http://127.0.0.1:5004", "Catchup", preview)
	if err != nil {
		t.Fatalf("SaveCatchupCapsuleLibraryLayout: %v", err)
	}
	if manifest.ReplayMode != "replay" {
		t.Fatalf("manifest replay_mode=%q want replay", manifest.ReplayMode)
	}
	if len(manifest.Items) != 1 {
		t.Fatalf("items len=%d want 1", len(manifest.Items))
	}
	if manifest.Items[0].StreamURL != "http://provider.example/replay/1001" {
		t.Fatalf("stream_url=%q want replay url", manifest.Items[0].StreamURL)
	}
	data, err := os.ReadFile(manifest.Items[0].StreamPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != "http://provider.example/replay/1001" {
		t.Fatalf("strm contents=%q want replay url", got)
	}
}
