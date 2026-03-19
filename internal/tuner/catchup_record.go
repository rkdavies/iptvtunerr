package tuner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/snapetech/iptvtunerr/internal/httpclient"
)

type CatchupRecordedItem struct {
	CapsuleID  string `json:"capsule_id"`
	Lane       string `json:"lane"`
	Title      string `json:"title"`
	ChannelID  string `json:"channel_id"`
	OutputPath string `json:"output_path"`
	SourceURL  string `json:"source_url"`
	Bytes      int64  `json:"bytes"`
}

type CatchupRecordManifest struct {
	GeneratedAt string                `json:"generated_at"`
	RootDir     string                `json:"root_dir"`
	Recorded    []CatchupRecordedItem `json:"recorded"`
}

func RecordCatchupCapsules(ctx context.Context, preview CatchupCapsulePreview, streamBaseURL, outDir string, maxDuration time.Duration, client *http.Client) (CatchupRecordManifest, error) {
	streamBaseURL = strings.TrimRight(strings.TrimSpace(streamBaseURL), "/")
	outDir = strings.TrimSpace(outDir)
	if streamBaseURL == "" {
		return CatchupRecordManifest{}, fmt.Errorf("stream base url required")
	}
	if outDir == "" {
		return CatchupRecordManifest{}, fmt.Errorf("output directory required")
	}
	if client == nil {
		client = httpclient.ForStreaming()
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return CatchupRecordManifest{}, err
	}
	manifest := CatchupRecordManifest{
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
		RootDir:     outDir,
	}
	for _, capsule := range preview.Capsules {
		if strings.ToLower(strings.TrimSpace(capsule.State)) != "in_progress" {
			continue
		}
		sourceURL := strings.TrimSpace(capsule.ReplayURL)
		if sourceURL == "" {
			sourceURL = streamBaseURL + "/stream/" + capsule.ChannelID
		}
		reqCtx := ctx
		if maxDuration > 0 {
			var cancel context.CancelFunc
			reqCtx, cancel = context.WithTimeout(ctx, maxDuration)
			defer cancel()
		}
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, sourceURL, nil)
		if err != nil {
			return manifest, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return manifest, err
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return manifest, fmt.Errorf("record %s status=%d", capsule.CapsuleID, resp.StatusCode)
		}
		laneDir := filepath.Join(outDir, firstNonEmptyString(capsule.Lane, "general"))
		if err := os.MkdirAll(laneDir, 0o755); err != nil {
			resp.Body.Close()
			return manifest, err
		}
		path := filepath.Join(laneDir, sanitizeCatchupName(capsule.CapsuleID)+".ts")
		f, err := os.Create(path)
		if err != nil {
			resp.Body.Close()
			return manifest, err
		}
		n, copyErr := io.Copy(f, resp.Body)
		_ = f.Close()
		resp.Body.Close()
		if copyErr != nil && reqCtx.Err() == nil {
			return manifest, copyErr
		}
		manifest.Recorded = append(manifest.Recorded, CatchupRecordedItem{
			CapsuleID:  capsule.CapsuleID,
			Lane:       capsule.Lane,
			Title:      capsule.Title,
			ChannelID:  capsule.ChannelID,
			OutputPath: path,
			SourceURL:  sourceURL,
			Bytes:      n,
		})
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return manifest, err
	}
	if err := os.WriteFile(filepath.Join(outDir, "record-manifest.json"), data, 0o600); err != nil {
		return manifest, err
	}
	return manifest, nil
}
