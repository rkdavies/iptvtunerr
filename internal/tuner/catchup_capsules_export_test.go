package tuner

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveCatchupCapsuleLanes(t *testing.T) {
	dir := t.TempDir()
	preview := CatchupCapsulePreview{
		GeneratedAt: "2026-03-18T12:00:00Z",
		SourceReady: true,
		Capsules: []CatchupCapsule{
			{CapsuleID: "a", Lane: "sports", Title: "Game"},
			{CapsuleID: "b", Lane: "general", Title: "News"},
		},
	}
	written, err := SaveCatchupCapsuleLanes(dir, preview)
	if err != nil {
		t.Fatalf("SaveCatchupCapsuleLanes: %v", err)
	}
	if written["sports"] == "" || written["general"] == "" {
		t.Fatalf("missing written lanes: %+v", written)
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if manifest["capsule_count"].(float64) != 2 {
		t.Fatalf("capsule_count=%v want 2", manifest["capsule_count"])
	}
}
