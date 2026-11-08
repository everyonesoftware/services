package updates

import (
	"context"
	"encoding/json"
	"errors"
	"services/internal/groups"
	"testing"
	"time"
)

type unavailableRegistry struct{}

func (unavailableRegistry) ManifestDigest(context.Context, string) (string, error) {
	return "", errors.New("authentication failed: do not expose registry response")
}
func statusFields(t *testing.T, c groups.Container) map[string]any {
	t.Helper()
	b, e := json.Marshal(c)
	if e != nil {
		t.Fatal(e)
	}
	var v map[string]any
	json.Unmarshal(b, &v)
	return v
}
func TestMissingPlaceholderCannotRegainUpdateEligibility(t *testing.T) {
	c := NewChecker(&fakeRegistry{digest: "sha256:new"}, time.Hour)
	if err := c.checkNow(context.Background(), "ghcr.io/acme/app:stable"); err != nil {
		t.Fatal(err)
	}
	items := []groups.Container{{Project: "media", State: "running", MonitorStatus: "missing", Image: "ghcr.io/acme/app:stable", RepoDigests: []string{"ghcr.io/acme/app@sha256:old"}}}
	c.Apply(items)
	if items[0].UpdateAvailable || items[0].UpdateStatus != "unavailable" {
		t.Fatalf("missing container offered an update: %+v", items[0])
	}
	registry := &fakeRegistry{digest: "sha256:new"}
	checker := NewChecker(registry, time.Hour)
	if err := checker.CheckNow(context.Background(), items); err != nil {
		t.Fatal(err)
	}
	if registry.calls != 0 {
		t.Fatal("missing container triggered registry request")
	}
}

func TestRegistryFailureIsNotReportedAsCurrent(t *testing.T) {
	c := NewChecker(unavailableRegistry{}, time.Hour)
	_ = c.checkNow(context.Background(), "ghcr.io/acme/app:latest")
	items := []groups.Container{{Project: "media", State: "running", Image: "ghcr.io/acme/app:latest", RepoDigests: []string{"ghcr.io/acme/app@sha256:old"}}}
	c.Apply(items)
	v := statusFields(t, items[0])
	if v["updateStatus"] != "error" {
		t.Fatalf("want explicit registry error, got %v", v)
	}
	if v["updateError"] != "Registry check failed; authentication or registry availability may be required" {
		t.Fatalf("unsafe or missing diagnostic: %v", v)
	}
}
func TestUnsupportedImageExplainsUnknownUpdateState(t *testing.T) {
	c := NewChecker(nil, time.Hour)
	items := []groups.Container{{Project: "media", State: "running", Image: "private.example/app:latest"}}
	c.Apply(items)
	v := statusFields(t, items[0])
	if v["updateStatus"] != "unsupported" {
		t.Fatalf("want unsupported status, got %v", v)
	}
}
func TestSuccessfulCheckPublishesTimestampAndCurrentStatus(t *testing.T) {
	c := NewChecker(&fakeRegistry{digest: "sha256:new"}, time.Hour)
	if err := c.checkNow(context.Background(), "ghcr.io/acme/app:stable"); err != nil {
		t.Fatal(err)
	}
	items := []groups.Container{{Project: "media", State: "running", Image: "ghcr.io/acme/app:stable", RepoDigests: []string{"ghcr.io/acme/app@sha256:new"}}}
	c.Apply(items)
	v := statusFields(t, items[0])
	if v["updateStatus"] != "current" || v["updateCheckedAt"] == nil {
		t.Fatalf("want current plus timestamp, got %v", v)
	}
}
