package dockerapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"services/internal/groups"
)

const composeProjectLabel = "com.docker.compose.project"
const composeServiceLabel = "com.docker.compose.service"

type Client struct {
	baseURL string
	http    *http.Client
}

func New(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: httpClient}
}

func NewUnixSocketClient(socketPath string) *Client {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "unix", socketPath)
	}}
	return New("http://docker", &http.Client{Transport: transport})
}

type engineVersion struct {
	APIVersion string `json:"ApiVersion"`
}

type containerSummary struct {
	ID     string            `json:"Id"`
	Names  []string          `json:"Names"`
	State  string            `json:"State"`
	Image  string            `json:"Image"`
	Labels map[string]string `json:"Labels"`
}

type containerInspect struct {
	State struct {
		Status string `json:"Status"`
		Health *struct {
			Status string `json:"Status"`
		} `json:"Health"`
	} `json:"State"`
}

func (c *Client) Containers() ([]groups.Container, error) {
	var version engineVersion
	if err := c.get("/version", &version); err != nil {
		return nil, fmt.Errorf("read Docker Engine version: %w", err)
	}
	if version.APIVersion == "" {
		return nil, fmt.Errorf("Docker Engine did not report an API version")
	}
	prefix := "/v" + version.APIVersion
	var summaries []containerSummary
	if err := c.get(prefix+"/containers/json?all=1", &summaries); err != nil {
		return nil, fmt.Errorf("list Docker containers: %w", err)
	}

	containers := make([]groups.Container, 0, len(summaries))
	for _, summary := range summaries {
		state := summary.State
		if len(summary.Names) > 0 {
			// Keep a stable display name without Docker's leading slash.
			summary.Names[0] = strings.TrimPrefix(summary.Names[0], "/")
		}
		container := groups.Container{
			ID:      summary.ID,
			Name:    first(summary.Names),
			Project: summary.Labels[composeProjectLabel],
			Service: summary.Labels[composeServiceLabel],
			Image:   summary.Image,
			State:   state,
		}
		var inspect containerInspect
		path := prefix + "/containers/" + url.PathEscape(summary.ID) + "/json"
		if err := c.get(path, &inspect); err != nil {
			return nil, fmt.Errorf("inspect container %s: %w", summary.ID, err)
		}
		if inspect.State.Status != "" {
			container.State = inspect.State.Status
		}
		if inspect.State.Health != nil {
			container.Health = inspect.State.Health.Status
		}
		containers = append(containers, container)
	}
	return containers, nil
}

func (c *Client) get(path string, target any) error {
	request, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("engine returned %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(response.Body).Decode(target)
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
