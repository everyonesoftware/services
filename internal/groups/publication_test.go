package groups

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicationJSONContract(t *testing.T) {
	p := ImagePublication{SchemaVersion: 1, Repository: "ghcr.io/acme/app", Tags: []string{"latest"}, Digest: "sha256:abc", SourceRepository: "acme/app", Commit: "abc", Ref: "refs/heads/main", RunID: "123", RunAttempt: 1, RunURL: "https://github.com/acme/app/actions/runs/123", PublishedAt: "2026-11-09T12:00:00Z"}
	encoded, err := json.Marshal(Container{UpdatePublication: &p})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]json.RawMessage
	if err = json.Unmarshal(encoded, &value); err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(value["updatePublication"], &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"schemaVersion", "repository", "tags", "digest", "sourceRepository", "commit", "ref", "runId", "runAttempt", "runUrl", "publishedAt"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("missing JSON key %s", key)
		}
	}
	if _, ok := fields["version"]; ok {
		t.Fatal("empty optional version emitted")
	}
	encoded, err = json.Marshal(Container{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "updatePublication") {
		t.Fatal("nil publication emitted")
	}
}
func TestAggregatePublicationIsDetachedAndStandaloneCleared(t *testing.T) {
	p := &ImagePublication{Tags: []string{"stable"}}
	items := []Container{{Project: "media", Name: "app", State: "running", UpdatePublication: p}, {Name: "standalone", State: "running", UpdatePublication: p}}
	projects := Aggregate(items)
	for _, project := range projects {
		for _, container := range project.Containers {
			if project.Name == "Standalone" {
				if container.UpdatePublication != nil {
					t.Fatal("standalone retained publication metadata")
				}
				continue
			}
			container.UpdatePublication.Tags[0] = "mutated"
			container.UpdatePublication.Version = "mutated"
		}
	}
	if p.Tags[0] != "stable" || p.Version != "" {
		t.Fatal("aggregate publication aliases source")
	}
}
