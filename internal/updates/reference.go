package updates

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var (
	errDigestPinned     = errors.New("digest-pinned image references do not move")
	errUnsupportedImage = errors.New("image registry or reference is unsupported")
	tagPattern          = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
)

type imageReference struct {
	registry   string
	apiHost    string
	repository string
	tag        string
}

func parseReference(image string) (imageReference, error) {
	image = strings.TrimSpace(image)
	if image == "" {
		return imageReference{}, errUnsupportedImage
	}
	if strings.Contains(image, "@") {
		return imageReference{}, errDigestPinned
	}
	tag := "latest"
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon > lastSlash {
		tag = image[lastColon+1:]
		image = image[:lastColon]
	}
	if !tagPattern.MatchString(tag) {
		return imageReference{}, errUnsupportedImage
	}
	registry, repository, err := splitName(image)
	if err != nil {
		return imageReference{}, err
	}
	apiHost := registry
	switch registry {
	case "docker.io", "index.docker.io", "registry-1.docker.io":
		registry = "docker.io"
		apiHost = "registry-1.docker.io"
		if !strings.Contains(repository, "/") {
			repository = "library/" + repository
		}
	case "ghcr.io":
	default:
		return imageReference{}, errUnsupportedImage
	}
	return imageReference{registry: registry, apiHost: apiHost, repository: repository, tag: tag}, nil
}

func splitName(name string) (string, string, error) {
	if name == "" || strings.ContainsAny(name, "\\?#") {
		return "", "", errUnsupportedImage
	}
	parts := strings.Split(name, "/")
	registry := "docker.io"
	if first := parts[0]; strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		registry = strings.ToLower(first)
		parts = parts[1:]
	}
	if len(parts) == 0 {
		return "", "", errUnsupportedImage
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || part != strings.ToLower(part) {
			return "", "", errUnsupportedImage
		}
		for _, character := range part {
			if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '.' || character == '_' || character == '-') {
				return "", "", errUnsupportedImage
			}
		}
		if part[0] == '.' || part[0] == '_' || part[0] == '-' || part[len(part)-1] == '.' || part[len(part)-1] == '_' || part[len(part)-1] == '-' {
			return "", "", errUnsupportedImage
		}
	}
	return registry, strings.Join(parts, "/"), nil
}

func (r imageReference) key() string {
	return fmt.Sprintf("%s/%s:%s", r.registry, r.repository, r.tag)
}

func (r imageReference) canonicalRepository() string {
	return r.registry + "/" + r.repository
}

func hasLocalRepository(ref imageReference, repoDigests []string) bool {
	for _, value := range repoDigests {
		repository, _, ok := splitRepoDigest(value)
		if ok && repository == ref.canonicalRepository() {
			return true
		}
	}
	return false
}

func hasLocalDigest(ref imageReference, repoDigests []string, digest string) bool {
	for _, value := range repoDigests {
		repository, localDigest, ok := splitRepoDigest(value)
		if ok && repository == ref.canonicalRepository() && localDigest == digest {
			return true
		}
	}
	return false
}

func splitRepoDigest(value string) (string, string, bool) {
	name, digest, ok := strings.Cut(value, "@")
	if !ok || digest == "" {
		return "", "", false
	}
	registry, repository, err := splitName(name)
	if err != nil {
		return "", "", false
	}
	switch registry {
	case "docker.io", "index.docker.io", "registry-1.docker.io":
		registry = "docker.io"
		if !strings.Contains(repository, "/") {
			repository = "library/" + repository
		}
	}
	return registry + "/" + repository, digest, true
}
