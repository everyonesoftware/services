package dashboard

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"services/internal/dockerapi"
)

// TestLogsProductionBrowserIntegration uses a test-only HTTP Docker Engine, the
// real inventory/log clients, authenticated JSON API and built React assets.
// It does not claim to verify a live Docker daemon. No browser API routes are mocked.
func TestLogsProductionBrowserIntegration(t *testing.T) {
	if os.Getenv("SERVICES_LOG_BROWSER") != "1" {
		t.Skip("opt in with SERVICES_LOG_BROWSER=1; build web/dist first")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "web/dist/index.html")); err != nil {
		t.Fatal("run npm run build in web first: ", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	type row struct {
		stamp  time.Time
		stream byte
		text   string
	}
	var mu sync.Mutex
	rows := map[string][]row{}
	for n := 0; n < 260; n++ {
		stamp, text := base.Add(time.Duration(n)*time.Second), fmt.Sprintf("fixture web %03d", n)
		if n >= 60 && n <= 64 {
			stamp = base.Add(60 * time.Second)
			text = "fixture identical timestamp duplicate"
		}
		stream := byte(1)
		if n%2 == 1 {
			stream = 2
		}
		rows["web-exact-id"] = append(rows["web-exact-id"], row{stamp, stream, text})
	}
	rows["worker-exact-id"] = []row{{base.Add(258500 * time.Millisecond), 2, "fixture worker stderr"}, {base.Add(259500 * time.Millisecond), 1, "fixture worker stdout"}}
	// Deliberately similar IDs/names in another project expose accidental prefix scope.
	rows["web-exact-id-other"] = []row{{base.Add(300 * time.Second), 1, "OUTSIDE PROJECT MUST NOT APPEAR"}}
	bound := func(raw string) time.Time {
		if raw == "" {
			return time.Time{}
		}
		parts := strings.SplitN(raw, ".", 2)
		sec, e := strconv.ParseInt(parts[0], 10, 64)
		if e != nil {
			t.Errorf("invalid Engine bound %q", raw)
		}
		var nano int64
		if len(parts) > 1 {
			nano, _ = strconv.ParseInt((parts[1] + "000000000")[:9], 10, 64)
		}
		return time.Unix(sec, nano)
	}
	var inventoryReads, logReads int
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"ApiVersion":"1.51"}`)
		case "/v1.51/images/json":
			fmt.Fprint(w, `[]`)
		case "/v1.51/containers/json":
			inventoryReads++
			var containers []map[string]any
			for _, c := range []struct{ id, name, project, service string }{{"web-exact-id", "fixture-web-1", "browser-fixture", "web"}, {"worker-exact-id", "fixture-worker-1", "browser-fixture", "worker"}, {"web-exact-id-other", "fixture-web-other", "other-fixture", "web"}} {
				containers = append(containers, map[string]any{"Id": c.id, "Names": []string{"/" + c.name}, "State": "running", "Image": "fixture:only", "Labels": map[string]string{"com.docker.compose.project": c.project, "com.docker.compose.service": c.service}})
			}
			_ = json.NewEncoder(w).Encode(containers)
		default:
			path := strings.TrimPrefix(r.URL.Path, "/v1.51/containers/")
			id, op, ok := strings.Cut(path, "/")
			records, exists := rows[id]
			if !ok || !exists {
				http.NotFound(w, r)
				return
			}
			if op == "json" {
				fmt.Fprint(w, `{"Config":{"Tty":false,"Image":"fixture:only"},"State":{"Status":"running"}}`)
				return
			}
			if op != "logs" {
				http.NotFound(w, r)
				return
			}
			logReads++
			q := r.URL.Query()
			if q.Get("follow") != "0" || q.Get("timestamps") != "1" || q.Get("stdout") != "1" || q.Get("stderr") != "1" {
				t.Errorf("unexpected Engine query: %s", r.URL.RawQuery)
			}
			// Moby selects the physical tail BEFORE timestamp filtering.
			if tail, e := strconv.Atoi(q.Get("tail")); e == nil && tail < len(records) {
				records = records[len(records)-tail:]
			}
			since, until := bound(q.Get("since")), bound(q.Get("until"))
			w.Header().Set("Content-Type", "application/vnd.docker.raw-stream")
			for _, record := range records {
				if (!since.IsZero() && record.stamp.Before(since)) || (!until.IsZero() && !record.stamp.Before(until)) {
					continue
				}
				text := record.stamp.Format(time.RFC3339Nano) + " " + record.text + "\n"
				// Split inside the timestamp to exercise the production multiplex decoder.
				for _, part := range []string{text[:12], text[12:]} {
					header := make([]byte, 8)
					header[0] = record.stream
					binary.BigEndian.PutUint32(header[4:], uint32(len(part)))
					_, _ = w.Write(header)
					_, _ = w.Write([]byte(part))
				}
			}
		}
	}))
	defer engine.Close()
	client := dockerapi.New(engine.URL, engine.Client())
	s, store := securedServer(t)
	s.source = client
	s.SetActionSource(client)
	s.SetLogSource(client)
	if _, err := store.Bootstrap("browser-fixture-admin", "test-only sufficiently long password"); err != nil {
		t.Fatal(err)
	}
	_, session, err := store.Login("browser-fixture-admin", "test-only sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := store.CreateKey("browser-fixture-reader", []string{"services:read"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	static := http.FileServer(http.Dir(filepath.Join(root, "web/dist")))
	appended := false
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Harness control exists ONLY in this httptest wrapper, never production.
		if r.URL.Path == "/__test/append" {
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			cookie, err := r.Cookie(sessionCookieName)
			if err != nil || cookie.Value != session {
				w.WriteHeader(401)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if !appended {
				for n := 260; n < 710; n++ {
					rows["web-exact-id"] = append(rows["web-exact-id"], row{base.Add(time.Duration(n) * time.Second), 1, fmt.Sprintf("fixture appended %03d", n)})
				}
				rows["worker-exact-id"] = append(rows["worker-exact-id"], row{base.Add(259500 * time.Millisecond), 2, "fixture worker equal-time appended"})
				appended = true
			}
			w.WriteHeader(204)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			s.ServeHTTP(w, r)
			return
		}
		static.ServeHTTP(w, r)
	}))
	defer app.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", filepath.Join(root, "web/tests/logs.integration.browser.mjs"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "SERVICES_LOG_URL="+app.URL, "SERVICES_LOG_SESSION="+session, "SERVICES_LOG_COOKIE="+sessionCookieName, "SERVICES_LOG_READ_KEY="+key)

	output, err := cmd.CombinedOutput()
	t.Logf("real backend/browser integration:\n%s", output)
	if err != nil {
		t.Fatalf("browser integration: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if inventoryReads == 0 || logReads == 0 || !appended {
		t.Fatalf("fixture was not exercised: inventory=%d logs=%d appended=%v", inventoryReads, logReads, appended)
	}
	t.Logf("test-only Engine exercised: inventory=%d log snapshots=%d", inventoryReads, logReads)
}
