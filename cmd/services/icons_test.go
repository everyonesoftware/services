package main

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"services/internal/dashboard"
)

func TestConfigureIconsRequiresExplicitWritableDataDirectory(t *testing.T) {
	for _, directory := range []string{"", "relative-data"} {
		t.Run(directory, func(t *testing.T) {
			t.Setenv("SERVICES_DATA_DIR", directory)
			server := dashboard.New(nil, 0)
			if err := configureIcons(server); err == nil {
				t.Fatal("accepted missing or relative persistent data directory")
			}
		})
	}
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "icons"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SERVICES_DATA_DIR", directory)
	if err := configureIcons(dashboard.New(nil, 0)); err == nil {
		t.Fatal("accepted unusable icon storage")
	}
}

func TestConfigureIconsEnablesStoreInsideDataDirectory(t *testing.T) {
	directory := t.TempDir()
	t.Setenv("SERVICES_DATA_DIR", directory)
	server := dashboard.New(nil, 0)
	if err := configureIcons(server); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("GET", "/api/icons", nil))
	var result struct {
		Available bool              `json:"available"`
		Icons     []json.RawMessage `json:"icons"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || !result.Available || result.Icons == nil || len(result.Icons) != 0 {
		t.Fatalf("icon store was not enabled: %d %s", response.Code, response.Body.String())
	}
	info, err := os.Stat(filepath.Join(directory, "icons"))
	if err != nil || !info.IsDir() {
		t.Fatalf("icon directory missing: %v", err)
	}
	// Reopening the data folder must work rather than resetting storage.
	if err := configureIcons(dashboard.New(nil, 0)); err != nil {
		t.Fatal(err)
	}
}
