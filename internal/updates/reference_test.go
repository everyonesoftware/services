package updates

import "testing"

func TestParseReferenceNormalizesDockerHubAndKeepsConfiguredTag(t *testing.T) {
	cases := []struct {
		input, key, repository, tag string
	}{
		{"nginx", "docker.io/library/nginx:latest", "library/nginx", "latest"},
		{"docker.io/nginx:1.27", "docker.io/library/nginx:1.27", "library/nginx", "1.27"},
		{"ghcr.io/acme/widget:stable", "ghcr.io/acme/widget:stable", "acme/widget", "stable"},
	}
	for _, test := range cases {
		t.Run(test.input, func(t *testing.T) {
			got, err := parseReference(test.input)
			if err != nil {
				t.Fatalf("parseReference() error = %v", err)
			}
			if got.key() != test.key || got.repository != test.repository || got.tag != test.tag {
				t.Fatalf("parseReference() = %+v, want key=%q repository=%q tag=%q", got, test.key, test.repository, test.tag)
			}
		})
	}
}

func TestParseReferenceRejectsDigestPinnedAndUnsupportedRegistries(t *testing.T) {
	for _, image := range []string{
		"ghcr.io/acme/widget@sha256:abcdef",
		"registry.example.com/acme/widget:latest",
		"ghcr.io/Acme/widget:latest",
	} {
		if _, err := parseReference(image); err == nil {
			t.Errorf("parseReference(%q) succeeded, want unsupported reference", image)
		}
	}
}

func TestHasLocalDigestMatchesNormalizedRepositoryAndExactDigest(t *testing.T) {
	ref, err := parseReference("nginx:latest")
	if err != nil {
		t.Fatal(err)
	}
	const digest = "sha256:0123456789abcdef"
	if !hasLocalDigest(ref, []string{"docker.io/library/nginx@" + digest}, digest) {
		t.Fatal("expected normalized Docker Hub repo digest to match")
	}
	if hasLocalDigest(ref, []string{"docker.io/library/busybox@" + digest}, digest) {
		t.Fatal("different repository must not match")
	}
	if hasLocalDigest(ref, []string{"docker.io/library/nginx@sha256:old"}, digest) {
		t.Fatal("different digest must not match")
	}
}
