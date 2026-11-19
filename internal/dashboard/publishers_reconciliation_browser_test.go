package dashboard

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"services/internal/groups"
	"services/internal/monitor"
	"services/internal/publishers"
	"services/internal/updates"
)

type reconciliationBrowserSource struct{ fakeSource }

type reconciliationBrowserFailure struct {
	*updates.Checker
	mu   sync.Mutex
	db   *sql.DB
	fail bool
}

func (f *reconciliationBrowserFailure) ReconcilePublicationRepository(ctx context.Context, expected updates.PublicationBinding, id, installation int64, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.Checker.ReconcilePublicationRepository(ctx, expected, id, installation, name); err != nil {
		return err
	}
	if f.fail {
		_, err := f.db.Exec(`CREATE TRIGGER IF NOT EXISTS fail_browser_reconciliation BEFORE UPDATE ON publisher_state BEGIN SELECT RAISE(FAIL,'injected browser write failure'); END`)
		return err
	}
	return nil
}

func (f *reconciliationBrowserFailure) resume() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = false
	_, err := f.db.Exec("DROP TRIGGER IF EXISTS fail_browser_reconciliation")
	return err
}

func (reconciliationBrowserSource) Snapshot() monitor.Snapshot {
	return monitor.Snapshot{Connected: true, LastChecked: time.Now().UTC().Format(time.RFC3339Nano)}
}

// TestPublisherReconciliationProductionBrowserIntegration exercises production
// React assets, session authentication, review/apply handlers and both SQLite
// stores together. Only the GitHub provider and Docker inventory are test fakes;
// no browser HTTP API responses are intercepted or replaced.
func TestPublisherReconciliationProductionBrowserIntegration(t *testing.T) {
	if os.Getenv("SERVICES_RECONCILIATION_BROWSER") != "1" {
		t.Skip("opt in with SERVICES_RECONCILIATION_BROWSER=1; build web/dist first")
	}
	for _, currentName := range []string{"acme/renamed", "acme/app"} {
		t.Run(currentName, func(t *testing.T) { runPublisherReconciliationProductionBrowser(t, currentName, false) })
	}
	t.Run("partial-write", func(t *testing.T) { runPublisherReconciliationProductionBrowser(t, "acme/renamed", true) })
}

func runPublisherReconciliationProductionBrowser(t *testing.T, currentName string, partialWrite bool) {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "web/dist/index.html")); err != nil {
		t.Fatal("build web/dist first: ", err)
	}
	s, keys, store, provider, cookie := publisherServer(t)
	c := connectionForTest(t, s, store, cookie)
	var failure *reconciliationBrowserFailure
	var failureDB *sql.DB
	if partialWrite {
		snapshot, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		directory := filepath.Join(t.TempDir(), "retry-publisher")
		retryStore, err := publishers.Open(directory)
		if err != nil {
			t.Fatal(err)
		}
		defer retryStore.Close()
		if err := retryStore.Save(snapshot); err != nil {
			t.Fatal(err)
		}
		store = retryStore
		s.publishers.store = retryStore
		failureDB, err = sql.Open("sqlite", filepath.Join(directory, "publishers.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer failureDB.Close()
	}
	history := realPublisherReceiver(t, s)
	checker := updates.NewChecker(nil, 0)
	checker.SetPublicationStore(history)
	if partialWrite {
		failure = &reconciliationBrowserFailure{Checker: checker, db: failureDB, fail: true}
		if err := s.ConfigurePublicationReceiver(failure); err != nil {
			t.Fatal(err)
		}
	}
	original := reviewPublication(t)
	if _, err := checker.AcceptPublication(context.Background(), original); err != nil {
		t.Fatal(err)
	}
	provider.repositories[0].FullName = currentName
	s.source = reconciliationBrowserSource{fakeSource{containers: []groups.Container{{ID: "browser-finance", Name: "finance-web", Project: "Finance", Service: "web", Image: c.ImageRepository + ":latest", State: "running", Health: "healthy"}}}}
	// The isolated browser uses an ephemeral local HTTP origin, not the configured
	// HTTPS integration endpoint. The normal host and same-origin guards still run.
	s.publicOrigin = nil
	static := http.FileServer(http.Dir(filepath.Join(root, "web/dist")))
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__test/resume" {
			session, e := r.Cookie(cookie.Name)
			if r.Method != "POST" || e != nil || session.Value != cookie.Value || failure == nil {
				http.Error(w, "fixture authentication required", 403)
				return
			}
			if err := failure.resume(); err != nil {
				http.Error(w, "fixture resume failed", 500)
				return
			}
			// Unrelated administrator activity must not strand the pending
			// publisher's revalidated, explicitly approved recovery.
			if _, _, err := keys.CreateKey("unrelated-browser-reader", []string{"services:read"}, nil, ""); err != nil {
				http.Error(w, "fixture unrelated key creation failed", 500)
				return
			}
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if r.URL.Path == "/__test/publication" {
			session, e := r.Cookie(cookie.Name)
			if r.Method != "POST" || e != nil || session.Value != cookie.Value {
				http.Error(w, "fixture authentication required", 403)
				return
			}
			p := original
			p.SourceRepository = currentName
			p.RunID = "999999"
			p.RunURL = "https://github.com/" + currentName + "/actions/runs/999999"
			p.Version = "117"
			p.PublishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			result := publisherRequest(s, "POST", "/api/images/published", p, nil, c.Secret)
			w.WriteHeader(result.Code)
			_, _ = w.Write(result.Body.Bytes())
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
	cmd := exec.CommandContext(ctx, "node", filepath.Join(root, "web/tests/publisher-reconciliation.integration.browser.mjs"))
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "RECONCILIATION_URL="+app.URL, "RECONCILIATION_SESSION="+cookie.Value, "RECONCILIATION_COOKIE="+cookie.Name, "RECONCILIATION_CURRENT_NAME="+currentName)
	if partialWrite {
		cmd.Env = append(cmd.Env, "RECONCILIATION_PARTIAL_WRITE=1")
	}
	output, err := cmd.CombinedOutput()
	t.Logf("production backend/browser integration:\n%s", output)
	if err != nil {
		t.Fatalf("browser integration: %v", err)
	}
	binding, err := history.PublicationRepositoryBinding(context.Background(), c.ImageRepository)
	if err != nil || binding.RepositoryID != 12 || binding.SourceRepository != currentName {
		t.Fatal("binding not reconciled", err, binding)
	}
	saved, err := store.Load()
	if err != nil || saved.Connections[0].Reconciliation.State != "reconciled" || saved.Connections[0].LastPublication.Version != "117" {
		t.Fatal("receipt was not durably reflected", err)
	}
}
