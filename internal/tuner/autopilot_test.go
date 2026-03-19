package tuner

import (
	"path/filepath"
	"testing"
)

func TestAutopilotStorePersistsAndReloads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "autopilot.json")

	store, err := loadAutopilotStore(path)
	if err != nil {
		t.Fatalf("loadAutopilotStore: %v", err)
	}
	store.put(autopilotDecision{
		DNAID:       "dna:test",
		ClientClass: "web",
		Profile:     profilePlexSafe,
		Transcode:   true,
		Reason:      "resolved-web-client",
	})
	store.put(autopilotDecision{
		DNAID:       "dna:test",
		ClientClass: "web",
		Profile:     profilePlexSafe,
		Transcode:   true,
		Reason:      "resolved-web-client",
	})

	reloaded, err := loadAutopilotStore(path)
	if err != nil {
		t.Fatalf("reload autopilot store: %v", err)
	}
	row, ok := reloaded.get("dna:test", "web")
	if !ok {
		t.Fatalf("expected persisted decision")
	}
	if row.Profile != profilePlexSafe {
		t.Fatalf("profile=%q want %q", row.Profile, profilePlexSafe)
	}
	if !row.Transcode {
		t.Fatalf("expected transcode=true")
	}
	if row.Hits != 2 {
		t.Fatalf("hits=%d want 2", row.Hits)
	}
	if row.UpdatedAt == "" {
		t.Fatalf("expected updated timestamp")
	}
}

func TestAutopilotStoreHotDecisionAndReport(t *testing.T) {
	store := &autopilotStore{
		byKey: map[string]autopilotDecision{
			autopilotKey("dna:fox", "web"): {
				DNAID:       "dna:fox",
				ClientClass: "web",
				Profile:     profileDashFast,
				Transcode:   true,
				Hits:        4,
			},
			autopilotKey("dna:cnn", "native"): {
				DNAID:       "dna:cnn",
				ClientClass: "native",
				Profile:     profilePlexSafe,
				Transcode:   false,
				Hits:        2,
			},
		},
	}
	if _, ok := store.hotDecision("dna:fox", "web", 3); !ok {
		t.Fatal("expected hot decision for dna:fox/web")
	}
	if _, ok := store.hotDecision("dna:cnn", "native", 3); ok {
		t.Fatal("did not expect hot decision below threshold")
	}
	rep := store.report(1)
	if rep.DecisionCount != 2 {
		t.Fatalf("decision_count=%d want 2", rep.DecisionCount)
	}
	if len(rep.HotChannels) != 1 || rep.HotChannels[0].DNAID != "dna:fox" {
		t.Fatalf("unexpected hot channels=%+v", rep.HotChannels)
	}
}
