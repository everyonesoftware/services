package dashboard

import (
	"context"
	"encoding/json"
	"image/png"
	"os"
	"reflect"
	"strings"
	"testing"

	"services/internal/groups"
	"services/internal/icons"
	"services/internal/svgicon"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == svgicon.WorkerFlag {
		if err := svgicon.RunWorker(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestSVGUploadAndURLServeOnlyStaticPNG(t *testing.T) {
	data := []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 10"><rect width="20" height="10" fill="red"/></svg>`)
	store, err := icons.OpenWithDownloader(t.TempDir(), iconDownloadFunc(func(context.Context, string) ([]byte, error) { return data, nil }))
	if err != nil {
		t.Fatal(err)
	}
	server := New(fakeSource{containers: []groups.Container{{Project: "app", Name: "app-1"}}}, 0)
	server.SetIconStore(store)
	responses := []string{iconRequest(server, "PUT", "/api/icons/upload?kind=project&key=app", string(data)).Body.String(), iconRequest(server, "PUT", "/api/icons", `{"kind":"container","key":"app-1","url":"https://example.com/logo.any"}`).Body.String()}
	for _, response := range responses {
		var icon icons.Icon
		if err := json.Unmarshal([]byte(response), &icon); err != nil {
			t.Fatal(response)
		}
		served := iconRequest(server, "GET", icon.URL, "")
		if served.Code != 200 || !strings.HasPrefix(served.Header().Get("Content-Type"), "image/png") {
			t.Fatalf("not PNG: %d %v", served.Code, served.Header())
		}
		im, err := png.Decode(served.Body)
		if err != nil {
			t.Fatal(err)
		}
		if im.Bounds().Dx() != 20 || im.Bounds().Dy() != 10 {
			t.Fatal(im.Bounds())
		}
	}
	before := store.List()
	data = []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 20 10"><script/></svg>`)
	for _, route := range []string{"/api/icons/upload?kind=project&key=app", "/api/icons"} {
		body := string(data)
		if route == "/api/icons" {
			body = `{"kind":"container","key":"app-1","url":"https://example.com/logo.any"}`
		}
		response := iconRequest(server, "PUT", route, body)
		if response.Code != 400 || !reflect.DeepEqual(before, store.List()) {
			t.Fatalf("unsafe SVG changed records: %d", response.Code)
		}
	}
	response := iconRequest(server, "PUT", "/api/icons/upload?kind=project&key=app", string(data))
	if !strings.Contains(response.Body.String(), "restricted SVG") {
		t.Fatal(response.Body.String())
	}
}
