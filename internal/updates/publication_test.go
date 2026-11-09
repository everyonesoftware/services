package updates

import (
	"services/internal/groups"
	"strings"
	"testing"
)

func publicationFixture() groups.ImagePublication {
	return groups.ImagePublication{SchemaVersion: 1, Repository: "ghcr.io/acme/app", Tags: []string{"stable", "latest"}, Digest: "sha256:" + strings.Repeat("a", 64), Version: "v1.2.3", SourceRepository: "acme/app", Commit: strings.Repeat("b", 40), Ref: "refs/heads/main", RunID: "123", RunAttempt: 1, RunURL: "https://github.com/acme/app/actions/runs/123", PublishedAt: "2026-11-09T12:00:00Z"}
}
func TestValidatePublicationValidBoundaryForms(t *testing.T) {
	p := publicationFixture()
	p.SourceRepository = "acme/.github"
	p.RunURL = "https://github.com/" + p.SourceRepository + "/actions/runs/" + p.RunID
	p.Commit = strings.Repeat("b", 64)
	p.Version = ""
	p.PublishedAt = "2026-11-09T12:00:00.123456789Z"
	if err := ValidatePublication(p); err != nil {
		t.Fatal(err)
	}
}
func TestValidatePublication(t *testing.T) {
	if err := ValidatePublication(publicationFixture()); err != nil {
		t.Fatal(err)
	}
	tests := map[string]func(*groups.ImagePublication){
		"time comma":           func(p *groups.ImagePublication) { p.PublishedAt = "2026-11-09T12:00:00,1Z" },
		"ref hidden component": func(p *groups.ImagePublication) { p.Ref = "refs/heads/.private" },
		"ref lock component":   func(p *groups.ImagePublication) { p.Ref = "refs/heads/main.lock" },
		"source owner dash": func(p *groups.ImagePublication) {
			p.SourceRepository = "acme-/app"
			p.RunURL = "https://github.com/acme-/app/actions/runs/123"
		},
		"schema":           func(p *groups.ImagePublication) { p.SchemaVersion = 2 },
		"tagged repo":      func(p *groups.ImagePublication) { p.Repository += ":latest" },
		"registry":         func(p *groups.ImagePublication) { p.Repository = "docker.io/acme/app" },
		"uppercase repo":   func(p *groups.ImagePublication) { p.Repository = "ghcr.io/Acme/app" },
		"repo size":        func(p *groups.ImagePublication) { p.Repository = "ghcr.io/acme/" + strings.Repeat("a", 256) },
		"no tags":          func(p *groups.ImagePublication) { p.Tags = nil },
		"too many tags":    func(p *groups.ImagePublication) { p.Tags = make([]string, 65) },
		"duplicate tags":   func(p *groups.ImagePublication) { p.Tags = []string{"stable", "stable"} },
		"invalid tag":      func(p *groups.ImagePublication) { p.Tags = []string{"bad tag"} },
		"digest":           func(p *groups.ImagePublication) { p.Digest = "sha256:abc" },
		"uppercase digest": func(p *groups.ImagePublication) { p.Digest = "sha256:" + strings.Repeat("A", 64) },
		"commit":           func(p *groups.ImagePublication) { p.Commit = strings.Repeat("B", 40) },
		"source":           func(p *groups.ImagePublication) { p.SourceRepository = "acme/app/other" },
		"ref":              func(p *groups.ImagePublication) { p.Ref = "main" },
		"ref control":      func(p *groups.ImagePublication) { p.Ref = "refs/heads/main\n" },
		"run zero":         func(p *groups.ImagePublication) { p.RunID = "0" },
		"run leading zero": func(p *groups.ImagePublication) { p.RunID = "0123" },
		"run oversized":    func(p *groups.ImagePublication) { p.RunID = strings.Repeat("1", 21) },
		"attempt":          func(p *groups.ImagePublication) { p.RunAttempt = 0 },
		"url":              func(p *groups.ImagePublication) { p.RunURL += "?foo" },
		"time zone":        func(p *groups.ImagePublication) { p.PublishedAt = "2026-11-09T12:00:00+00:00" },
		"time":             func(p *groups.ImagePublication) { p.PublishedAt = "yesterday" },
		"version size":     func(p *groups.ImagePublication) { p.Version = strings.Repeat("a", 257) },
		"version control":  func(p *groups.ImagePublication) { p.Version = "a\nb" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			p := publicationFixture()
			mutate(&p)
			if ValidatePublication(p) == nil {
				t.Fatal("invalid publication accepted")
			}
		})
	}
}
