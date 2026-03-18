package tuner

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type CatchupCapsuleLaneFile struct {
	GeneratedAt string           `json:"generated_at"`
	SourceReady bool             `json:"source_ready"`
	Lane        string           `json:"lane"`
	Capsules    []CatchupCapsule `json:"capsules"`
}

func DefaultCatchupCapsuleLanes() []string {
	return []string{"sports", "movies", "general"}
}

func SaveCatchupCapsuleLanes(outDir string, preview CatchupCapsulePreview) (map[string]string, error) {
	if strings.TrimSpace(outDir) == "" {
		return nil, fmt.Errorf("output directory required")
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	byLane := map[string][]CatchupCapsule{}
	for _, capsule := range preview.Capsules {
		lane := strings.TrimSpace(capsule.Lane)
		if lane == "" {
			lane = "general"
		}
		byLane[lane] = append(byLane[lane], capsule)
	}
	written := map[string]string{}
	for _, lane := range DefaultCatchupCapsuleLanes() {
		capsules := byLane[lane]
		if len(capsules) == 0 {
			continue
		}
		body := CatchupCapsuleLaneFile{
			GeneratedAt: preview.GeneratedAt,
			SourceReady: preview.SourceReady,
			Lane:        lane,
			Capsules:    capsules,
		}
		data, err := json.MarshalIndent(body, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshal lane %s: %w", lane, err)
		}
		path := filepath.Join(outDir, lane+".json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return nil, fmt.Errorf("write lane %s: %w", lane, err)
		}
		written[lane] = path
	}
	manifestData, err := json.MarshalIndent(map[string]any{
		"generated_at":  preview.GeneratedAt,
		"source_ready":  preview.SourceReady,
		"capsule_count": len(preview.Capsules),
		"lane_order":    DefaultCatchupCapsuleLanes(),
		"written_lanes": written,
	}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal manifest: %w", err)
	}
	manifestPath := filepath.Join(outDir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestData, 0o600); err != nil {
		return nil, fmt.Errorf("write manifest: %w", err)
	}
	return written, nil
}
