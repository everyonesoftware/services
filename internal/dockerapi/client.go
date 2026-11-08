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
	"time"

	"services/internal/groups"
)

const composeProjectLabel = "com.docker.compose.project"
const composeServiceLabel = "com.docker.compose.service"
const composeWorkingDirLabel = "com.docker.compose.project.working_dir"
const composeConfigFilesLabel = "com.docker.compose.project.config_files"

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
	ID      string            `json:"Id"`
	Names   []string          `json:"Names"`
	State   string            `json:"State"`
	Image   string            `json:"Image"`
	ImageID string            `json:"ImageID"`
	Labels  map[string]string `json:"Labels"`
}

type imageSummary struct {
	ID          string   `json:"Id"`
	Created     int64    `json:"Created"`
	RepoDigests []string `json:"RepoDigests"`
}

type containerInspect struct {
	RestartCount int `json:"RestartCount"`
	State        struct {
		StartedAt string `json:"StartedAt"`
		ExitCode  int    `json:"ExitCode"`
		OOMKilled bool   `json:"OOMKilled"`
		Status    string `json:"Status"`
		Health    *struct {
			Status string `json:"Status"`
			Log    []struct {
				Output string `json:"Output"`
			} `json:"Log"`
		} `json:"Health"`
	} `json:"State"`
}

func (c *Client) Containers() ([]groups.Container, error) {
	return c.ContainersContext(context.Background())
}

// ContainersContext bounds a complete refresh, including all inspect requests.
func (c *Client) ContainersContext(ctx context.Context) ([]groups.Container, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var version engineVersion
	if err := c.get(ctx, "/version", &version); err != nil {
		return nil, fmt.Errorf("read Docker Engine version: %w", err)
	}
	if version.APIVersion == "" {
		return nil, fmt.Errorf("Docker Engine did not report an API version")
	}
	prefix := "/v" + version.APIVersion
	var summaries []containerSummary
	if err := c.get(ctx, prefix+"/containers/json?all=1", &summaries); err != nil {
		return nil, fmt.Errorf("list Docker containers: %w", err)
	}
	var images []imageSummary
	imageDigests := make(map[string][]string)
	imageCreatedAt := make(map[string]int64)
	if err := c.get(ctx, prefix+"/images/json?all=1&digests=1", &images); err == nil {
		for _, image := range images {
			imageDigests[image.ID] = image.RepoDigests
			imageCreatedAt[image.ID] = image.Created
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	containers := make([]groups.Container, 0, len(summaries))
	for _, summary := range summaries {
		state := summary.State
		if len(summary.Names) > 0 {
			// Keep a stable display name without Docker's leading slash.
			summary.Names[0] = strings.TrimPrefix(summary.Names[0], "/")
		}
		container := groups.Container{
			ID:                      summary.ID,
			Name:                    first(summary.Names),
			Project:                 summary.Labels[composeProjectLabel],
			Service:                 summary.Labels[composeServiceLabel],
			Image:                   summary.Image,
			State:                   state,
			RepoDigests:             imageDigests[summary.ImageID],
			ImageCreatedAt:          imageCreatedAt[summary.ImageID],
			ComposeWorkingDir:       summary.Labels[composeWorkingDirLabel],
			ComposeConfigFiles:      splitConfigFiles(summary.Labels[composeConfigFilesLabel]),
			ComposeConfigFilesLabel: summary.Labels[composeConfigFilesLabel],
		}
		var inspect containerInspect
		path := prefix + "/containers/" + url.PathEscape(summary.ID) + "/json"
		if err := c.get(ctx, path, &inspect); err != nil {
			return nil, fmt.Errorf("inspect container %s: %w", summary.ID, err)
		}
		container.RestartCount = inspect.RestartCount
		container.StartedAt = inspect.State.StartedAt
		container.ExitCode = inspect.State.ExitCode
		container.OOMKilled = inspect.State.OOMKilled
		container.OneShot = strings.EqualFold(summary.Labels["com.docker.compose.oneoff"], "true")
		if inspect.State.Status != "" {
			container.State = inspect.State.Status
		}
		if inspect.State.Health != nil {
			container.Health = inspect.State.Health.Status
			if logs := inspect.State.Health.Log; len(logs) > 0 {
				container.HealthOutput = logs[len(logs)-1].Output
			}
		}
		containers = append(containers, container)
	}
	return containers, nil
}

func (c *Client) get(ctx context.Context, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
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

func splitConfigFiles(value string) []string {
	var files []string
	for _, file := range strings.Split(value, ",") {
		if file = strings.TrimSpace(file); file != "" {
			files = append(files, file)
		}
	}
	return files
}
