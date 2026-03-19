package tuner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRecordCatchupCapsules(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ts-data"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	preview := CatchupCapsulePreview{
		Capsules: []CatchupCapsule{
			{CapsuleID: "dna:test:1", Lane: "sports", Title: "Live Game", ChannelID: "101", State: "in_progress"},
			{CapsuleID: "dna:test:2", Lane: "general", Title: "Later", ChannelID: "102", State: "starting_soon"},
		},
	}
	manifest, err := RecordCatchupCapsules(context.Background(), preview, srv.URL, dir, 100*time.Millisecond, srv.Client())
	if err != nil {
		t.Fatalf("RecordCatchupCapsules: %v", err)
	}
	if len(manifest.Recorded) != 1 {
		t.Fatalf("recorded=%d want 1", len(manifest.Recorded))
	}
	item := manifest.Recorded[0]
	if item.Bytes == 0 {
		t.Fatalf("bytes=%d want >0", item.Bytes)
	}
	data, err := os.ReadFile(item.OutputPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if string(data) != "ts-data" {
		t.Fatalf("data=%q", string(data))
	}
	manifestData, err := os.ReadFile(filepath.Join(dir, "record-manifest.json"))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	var parsed CatchupRecordManifest
	if err := json.Unmarshal(manifestData, &parsed); err != nil {
		t.Fatalf("unmarshal manifest: %v", err)
	}
	if len(parsed.Recorded) != 1 {
		t.Fatalf("parsed recorded=%d want 1", len(parsed.Recorded))
	}
}
