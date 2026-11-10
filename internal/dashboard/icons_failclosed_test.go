package dashboard

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"services/internal/groups"
	"services/internal/icons"
)

func TestIconDeletePersistenceFailureDisablesEditing(t *testing.T) {
	dir := t.TempDir()
	data := iconFixture(t)
	downloads := 0
	store, err := icons.OpenWithDownloader(dir, iconDownloadFunc(func(context.Context, string) ([]byte, error) {
		downloads++
		return data, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Put(context.Background(), "project", "app", "https://example.com/old"); err != nil {
		t.Fatal(err)
	}
	before := store.List()
	metadata := filepath.Join(dir, "metadata.json")
	if err = os.Remove(metadata); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(metadata, 0700); err != nil {
		t.Fatal(err)
	}
	s := New(fakeSource{containers: []groups.Container{{Name: "api-1", Project: "app"}}}, 1)
	s.SetIconStore(store)
	if w := iconRequest(s, "DELETE", "/api/icons?kind=project&key=app", ""); w.Code != 503 {
		t.Fatalf("failing delete: %d %s", w.Code, w.Body.String())
	}
	w := iconRequest(s, "GET", "/api/icons", "")
	var listing struct {
		Available bool         `json:"available"`
		Icons     []icons.Icon `json:"icons"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
	if w.Code != 200 || listing.Available || !reflect.DeepEqual(listing.Icons, before) {
		t.Fatalf("failed store list: %d %s", w.Code, w.Body.String())
	}
	for _, method := range []string{"PUT", "DELETE"} {
		w := iconRequest(s, method, "/api/icons?kind=project&key=app", `{"kind":"project","key":"app","url":"https://example.com/new"}`)
		if w.Code != 503 {
			t.Fatalf("disabled %s: %d", method, w.Code)
		}
	}
	if downloads != 1 {
		t.Fatalf("disabled mutation downloaded: %d", downloads)
	}
}
