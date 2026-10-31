package updates

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"services/internal/groups"
)

type fakeRegistry struct {
	digest string
	calls  int
}

func (f *fakeRegistry) ManifestDigest(context.Context, string) (string, error) {
	f.calls++
	return f.digest, nil
}

type signalingRegistry struct {
	digest string
	calls  chan string
}

func (r *signalingRegistry) ManifestDigest(_ context.Context, image string) (string, error) {
	r.calls <- image
	return r.digest, nil
}

type retryingRegistry struct {
	attempts atomic.Int32
	calls    chan struct{}
}

func (r *retryingRegistry) ManifestDigest(context.Context, string) (string, error) {
	r.calls <- struct{}{}
	if r.attempts.Add(1) == 1 {
		return "", errors.New("temporary registry failure")
	}
	return "sha256:new", nil
}

func TestApplySchedulesInitialCheckAndPublishesResult(t *testing.T) {
	registry := &signalingRegistry{digest: "sha256:new", calls: make(chan string, 1)}
	checker := NewChecker(registry, time.Hour)
	containers := []groups.Container{{
		Project:     "media",
		Image:       "ghcr.io/acme/app:stable",
		State:       "running",
		RepoDigests: []string{"ghcr.io/acme/app@sha256:old"},
	}}
	checker.Apply(containers)
	select {
	case image := <-registry.calls:
		if image != "ghcr.io/acme/app:stable" {
			t.Fatalf("registry image = %q, want configured tag", image)
		}
	case <-time.After(time.Second):
		t.Fatal("Apply() did not schedule a registry check")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		checker.Apply(containers)
		if containers[0].UpdateAvailable {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("update status was not visible after the registry check completed")
}

func TestApplyIgnoresUnlabelledContainers(t *testing.T) {
	registry := &signalingRegistry{digest: "sha256:new", calls: make(chan string, 1)}
	checker := NewChecker(registry, time.Hour)
	checker.Apply([]groups.Container{{Image: "ghcr.io/acme/app:stable", State: "running"}})
	select {
	case image := <-registry.calls:
		t.Fatalf("unlabelled container triggered registry check for %q", image)
	case <-time.After(20 * time.Millisecond):
	}
}

func TestApplyRetriesFailedRegistryCheckBeforeNormalPollInterval(t *testing.T) {
	registry := &retryingRegistry{calls: make(chan struct{}, 2)}
	checker := newChecker(registry, 6*time.Hour, 10*time.Millisecond)
	if err := checker.checkNow(context.Background(), "ghcr.io/acme/app:stable"); err == nil {
		t.Fatal("first registry check should fail")
	}
	<-registry.calls
	containers := []groups.Container{{
		Project:     "media",
		Image:       "ghcr.io/acme/app:stable",
		State:       "running",
		RepoDigests: []string{"ghcr.io/acme/app@sha256:old"},
	}}
	time.Sleep(15 * time.Millisecond)
	checker.Apply(containers)
	select {
	case <-registry.calls:
	case <-time.After(time.Second):
		t.Fatal("failed registry check was not retried before the six-hour poll")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		checker.Apply(containers)
		if containers[0].UpdateAvailable {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("successful retry did not publish update status")
}

func TestApplyMarksContainerWhenConfiguredTagDigestMoved(t *testing.T) {
	registry := &fakeRegistry{digest: "sha256:new"}
	checker := NewChecker(registry, time.Hour)
	if err := checker.checkNow(context.Background(), "ghcr.io/acme/app:stable"); err != nil {
		t.Fatal(err)
	}
	containers := []groups.Container{{
		Project:     "media",
		Image:       "ghcr.io/acme/app:stable",
		State:       "running",
		RepoDigests: []string{"ghcr.io/acme/app@sha256:old"},
	}}
	checker.Apply(containers)
	if !containers[0].UpdateAvailable {
		t.Fatal("expected updateAvailable when configured tag digest differs")
	}
	if registry.calls != 1 {
		t.Fatalf("registry calls = %d, want 1", registry.calls)
	}
}

func TestApplyDoesNotMarkCurrentStoppedUnsupportedOrDigestPinnedImages(t *testing.T) {
	registry := &fakeRegistry{digest: "sha256:new"}
	checker := NewChecker(registry, time.Hour)
	if err := checker.checkNow(context.Background(), "ghcr.io/acme/app:stable"); err != nil {
		t.Fatal(err)
	}
	containers := []groups.Container{
		{Project: "media", Image: "ghcr.io/acme/app:stable", State: "running", RepoDigests: []string{"ghcr.io/acme/app@sha256:new"}},
		{Project: "media", Image: "ghcr.io/acme/app:stable", State: "exited", RepoDigests: []string{"ghcr.io/acme/app@sha256:old"}},
		{Project: "media", Image: "registry.example.com/acme/app:stable", State: "running", RepoDigests: []string{"registry.example.com/acme/app@sha256:old"}},
		{Project: "media", Image: "ghcr.io/acme/app@sha256:old", State: "running", RepoDigests: []string{"ghcr.io/acme/app@sha256:old"}},
	}
	checker.Apply(containers)
	for i, container := range containers {
		if container.UpdateAvailable {
			t.Errorf("container %d unexpectedly marked updateAvailable", i)
		}
	}
}

func TestCheckDoesNotExposeUpdateWhenLocalRepoDigestIsMissing(t *testing.T) {
	checker := NewChecker(&fakeRegistry{digest: "sha256:new"}, time.Hour)
	if err := checker.checkNow(context.Background(), "ghcr.io/acme/app:stable"); err != nil {
		t.Fatal(err)
	}
	containers := []groups.Container{{Project: "media", Image: "ghcr.io/acme/app:stable", State: "running"}}
	checker.Apply(containers)
	if containers[0].UpdateAvailable {
		t.Fatal("missing local RepoDigests must not be treated as an available update")
	}
}

func TestNewCheckerUsesSixHourPollAndFifteenMinuteFailureRetry(t *testing.T) {
	checker := NewChecker(&fakeRegistry{}, 0)
	if checker.interval != 6*time.Hour || checker.retryInterval != 15*time.Minute {
		t.Fatalf("checker intervals = (%s, %s), want (6h, 15m)", checker.interval, checker.retryInterval)
	}
}
