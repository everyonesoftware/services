package updates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const manifestAccept = "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.docker.distribution.manifest.v1+json"

type RegistryClient struct {
	httpClient *http.Client
	endpoints  map[string]string
}

func NewRegistryClient(httpClient *http.Client) *RegistryClient {
	return newRegistryClient(httpClient, map[string]string{
		"registry-1.docker.io": "https://registry-1.docker.io",
		"ghcr.io":              "https://ghcr.io",
	})
}

func newRegistryClient(httpClient *http.Client, endpoints map[string]string) *RegistryClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	copied := make(map[string]string, len(endpoints))
	for host, endpoint := range endpoints {
		copied[host] = strings.TrimRight(endpoint, "/")
	}
	return &RegistryClient{httpClient: httpClient, endpoints: copied}
}

func (c *RegistryClient) ManifestDigest(ctx context.Context, image string) (string, error) {
	ref, err := parseReference(image)
	if err != nil {
		return "", err
	}
	base, ok := c.endpoints[ref.apiHost]
	if !ok {
		return "", errUnsupportedImage
	}
	manifestURL := base + "/v2/" + ref.repository + "/manifests/" + url.PathEscape(ref.tag)
	response, err := c.headManifest(ctx, manifestURL, "")
	if err != nil {
		return "", err
	}
	if response.StatusCode == http.StatusUnauthorized {
		challenge := response.Header.Get("WWW-Authenticate")
		_ = response.Body.Close()
		token, tokenErr := c.pullToken(ctx, ref, base, challenge)
		if tokenErr != nil {
			return "", tokenErr
		}
		response, err = c.headManifest(ctx, manifestURL, token)
		if err != nil {
			return "", err
		}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("registry returned %s for %s", response.Status, ref.key())
	}
	digest := strings.TrimSpace(response.Header.Get("Docker-Content-Digest"))
	if digest == "" {
		return "", fmt.Errorf("registry did not return Docker-Content-Digest for %s", ref.key())
	}
	return digest, nil
}

func (c *RegistryClient) headManifest(ctx context.Context, manifestURL, token string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, manifestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", manifestAccept)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	return c.httpClient.Do(request)
}

func (c *RegistryClient) pullToken(ctx context.Context, ref imageReference, base, challenge string) (string, error) {
	attributes, err := parseBearerChallenge(challenge)
	if err != nil {
		return "", err
	}
	realm, err := url.Parse(attributes["realm"])
	if err != nil || realm == nil || realm.Host == "" {
		return "", fmt.Errorf("registry returned an invalid authentication realm")
	}
	registryURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	allowedHosts := map[string]bool{registryURL.Host: true}
	if ref.apiHost == "registry-1.docker.io" {
		allowedHosts["auth.docker.io"] = true
	}
	if realm.Scheme != registryURL.Scheme || !allowedHosts[realm.Host] {
		return "", fmt.Errorf("registry returned an untrusted authentication realm")
	}
	query := realm.Query()
	query.Set("scope", "repository:"+ref.repository+":pull")
	if service := attributes["service"]; service != "" {
		query.Set("service", service)
	}
	realm.RawQuery = query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, realm.String(), nil)
	if err != nil {
		return "", err
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", fmt.Errorf("registry token endpoint returned %s", response.Status)
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return "", fmt.Errorf("decode registry token: %w", err)
	}
	if payload.Token == "" {
		payload.Token = payload.AccessToken
	}
	if payload.Token == "" {
		return "", fmt.Errorf("registry token response contained no token")
	}
	return payload.Token, nil
}

func parseBearerChallenge(value string) (map[string]string, error) {
	scheme, rest, ok := strings.Cut(strings.TrimSpace(value), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return nil, fmt.Errorf("registry did not provide a Bearer authentication challenge")
	}
	attributes := make(map[string]string)
	var parts []string
	start, quoted, escaped := 0, false, false
	for index, character := range rest {
		if escaped {
			escaped = false
			continue
		}
		if character == '\\' && quoted {
			escaped = true
			continue
		}
		if character == '"' {
			quoted = !quoted
			continue
		}
		if character == ',' && !quoted {
			parts = append(parts, rest[start:index])
			start = index + 1
		}
	}
	parts = append(parts, rest[start:])
	for _, part := range parts {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			unquoted, err := strconv.Unquote(value)
			if err != nil {
				return nil, fmt.Errorf("parse registry challenge: %w", err)
			}
			value = unquoted
		}
		attributes[strings.TrimSpace(key)] = value
	}
	if attributes["realm"] == "" {
		return nil, fmt.Errorf("registry Bearer challenge omitted its realm")
	}
	return attributes, nil
}
