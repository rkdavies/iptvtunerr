package emby

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnsureLibraryReusesExisting(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/Library/VirtualFolders/Query" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(VirtualFolderQueryResult{
			Items: []VirtualFolderInfo{{
				Name:           "Catchup Sports",
				CollectionType: "movies",
				ItemID:         "lib-1",
				Locations:      []string{"/data/catchup/sports"},
			}},
		})
	}))
	defer srv.Close()

	lib, created, err := EnsureLibrary(newTestConfig(srv.URL, "emby"), LibraryCreateSpec{
		Name:           "Catchup Sports",
		CollectionType: "movies",
		Path:           "/data/catchup/sports",
	})
	if err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	if created {
		t.Fatal("expected existing library to be reused")
	}
	if lib == nil || lib.ID != "lib-1" {
		t.Fatalf("unexpected library: %+v", lib)
	}
}

func TestEnsureLibraryCreatesMissing(t *testing.T) {
	var (
		listCalls   int
		createBody  AddVirtualFolder
		createCalls int
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/Library/VirtualFolders/Query":
			listCalls++
			if listCalls == 1 {
				_ = json.NewEncoder(w).Encode(VirtualFolderQueryResult{})
				return
			}
			_ = json.NewEncoder(w).Encode(VirtualFolderQueryResult{
				Items: []VirtualFolderInfo{{
					Name:           "Catchup Movies",
					CollectionType: "movies",
					ID:             "lib-2",
					Locations:      []string{"/data/catchup/movies"},
				}},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/Library/VirtualFolders":
			createCalls++
			if err := json.NewDecoder(r.Body).Decode(&createBody); err != nil {
				t.Fatalf("decode create body: %v", err)
			}
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
	defer srv.Close()

	lib, created, err := EnsureLibrary(newTestConfig(srv.URL, "jellyfin"), LibraryCreateSpec{
		Name:           "Catchup Movies",
		CollectionType: "movies",
		Path:           "/data/catchup/movies",
		Refresh:        true,
	})
	if err != nil {
		t.Fatalf("EnsureLibrary: %v", err)
	}
	if !created {
		t.Fatal("expected library creation")
	}
	if createCalls != 1 {
		t.Fatalf("createCalls=%d want 1", createCalls)
	}
	if createBody.Name != "Catchup Movies" || createBody.CollectionType != "movies" || !createBody.RefreshLibrary {
		t.Fatalf("unexpected create body: %+v", createBody)
	}
	if len(createBody.Paths) != 1 || createBody.Paths[0] != "/data/catchup/movies" {
		t.Fatalf("unexpected create paths: %+v", createBody.Paths)
	}
	if lib == nil || lib.ID != "lib-2" {
		t.Fatalf("unexpected created library: %+v", lib)
	}
}

func TestRefreshLibraryScan(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/Library/Refresh" {
			t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("Authorization"), "testtoken") {
			t.Fatalf("missing auth header")
		}
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if err := RefreshLibraryScan(newTestConfig(srv.URL, "emby")); err != nil {
		t.Fatalf("RefreshLibraryScan: %v", err)
	}
	if !called {
		t.Fatal("expected refresh call")
	}
}
