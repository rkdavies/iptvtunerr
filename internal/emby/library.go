package emby

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
)

// LibraryCreateSpec describes a library/virtual folder to create or reuse.
type LibraryCreateSpec struct {
	Name           string
	CollectionType string // "movies" or "tvshows"
	Path           string
	Refresh        bool
}

func ListLibraries(cfg Config) ([]LibraryInfo, error) {
	client := newHTTPClient()
	u := cfg.Host + "/Library/VirtualFolders/Query"
	status, data, err := apiRequest(client, http.MethodGet, u, cfg.Token, nil)
	if err != nil {
		return nil, fmt.Errorf("list libraries: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("list libraries returned %d: %s", status, trunc(string(data), 300))
	}
	var resp VirtualFolderQueryResult
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("parse libraries: %w", err)
	}
	out := make([]LibraryInfo, 0, len(resp.Items))
	for _, item := range resp.Items {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = strings.TrimSpace(item.ItemID)
		}
		locations := make([]string, 0, len(item.Locations))
		for _, loc := range item.Locations {
			loc = filepath.Clean(strings.TrimSpace(loc))
			if loc != "" {
				locations = append(locations, loc)
			}
		}
		out = append(out, LibraryInfo{
			ID:             id,
			Name:           strings.TrimSpace(item.Name),
			CollectionType: strings.TrimSpace(item.CollectionType),
			Locations:      locations,
		})
	}
	return out, nil
}

func CreateLibrary(cfg Config, spec LibraryCreateSpec) error {
	spec.Name = strings.TrimSpace(spec.Name)
	spec.Path = filepath.Clean(strings.TrimSpace(spec.Path))
	spec.CollectionType = strings.ToLower(strings.TrimSpace(spec.CollectionType))
	if spec.Name == "" {
		return fmt.Errorf("library name required")
	}
	if spec.Path == "" || spec.Path == "." || spec.Path == "/" {
		return fmt.Errorf("library path must be a specific directory")
	}
	if spec.CollectionType != "movies" && spec.CollectionType != "tvshows" {
		return fmt.Errorf("unsupported collection type %q", spec.CollectionType)
	}
	body := AddVirtualFolder{
		Name:           spec.Name,
		CollectionType: spec.CollectionType,
		RefreshLibrary: spec.Refresh,
		Paths:          []string{spec.Path},
	}
	client := newHTTPClient()
	u := cfg.Host + "/Library/VirtualFolders"
	status, data, err := apiRequest(client, http.MethodPost, u, cfg.Token, body)
	if err != nil {
		return fmt.Errorf("create library %q: %w", spec.Name, err)
	}
	if status != http.StatusOK && status != http.StatusNoContent {
		return fmt.Errorf("create library %q returned %d: %s", spec.Name, status, trunc(string(data), 300))
	}
	return nil
}

func EnsureLibrary(cfg Config, spec LibraryCreateSpec) (*LibraryInfo, bool, error) {
	libraries, err := ListLibraries(cfg)
	if err != nil {
		return nil, false, err
	}
	wantPath := filepath.Clean(strings.TrimSpace(spec.Path))
	wantType := strings.ToLower(strings.TrimSpace(spec.CollectionType))
	for _, lib := range libraries {
		if lib.Name != spec.Name {
			continue
		}
		if strings.ToLower(strings.TrimSpace(lib.CollectionType)) != wantType {
			return nil, false, fmt.Errorf("library %q exists with collectionType=%s (wanted %s)", spec.Name, lib.CollectionType, wantType)
		}
		for _, loc := range lib.Locations {
			if filepath.Clean(loc) == wantPath {
				return &lib, false, nil
			}
		}
		return nil, false, fmt.Errorf("library %q exists but path differs (have %v, want %s)", spec.Name, lib.Locations, wantPath)
	}
	if err := CreateLibrary(cfg, spec); err != nil {
		return nil, false, err
	}
	libraries, err = ListLibraries(cfg)
	if err != nil {
		return nil, true, nil
	}
	for _, lib := range libraries {
		if lib.Name == spec.Name {
			return &lib, true, nil
		}
	}
	return &LibraryInfo{Name: spec.Name, CollectionType: wantType, Locations: []string{wantPath}}, true, nil
}

func RefreshLibraryScan(cfg Config) error {
	base := strings.TrimSpace(cfg.Host)
	if base == "" {
		return fmt.Errorf("host required")
	}
	u, err := url.Parse(base)
	if err != nil {
		return fmt.Errorf("parse host: %w", err)
	}
	u.Path = "/Library/Refresh"
	client := newHTTPClient()
	status, data, err := apiRequest(client, http.MethodPost, u.String(), cfg.Token, nil)
	if err != nil {
		return fmt.Errorf("refresh library scan: %w", err)
	}
	if status != http.StatusOK && status != http.StatusNoContent {
		return fmt.Errorf("refresh library scan returned %d: %s", status, trunc(string(data), 300))
	}
	return nil
}
