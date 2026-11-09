package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

var ErrNotFound = errors.New("Docker object not found")

type Engine struct {
	base   string
	client *http.Client
}

func NewEngine(base string, client *http.Client) *Engine {
	return &Engine{strings.TrimRight(base, "/"), client}
}
func UnixEngine(socket string) *Engine {
	return NewEngine("http://docker", &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}})
}
func (e *Engine) request(ctx context.Context, method, p string, body, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, e.base+p, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	response, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == 404 {
		return ErrNotFound
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Docker %s %s: %d %s", method, p, response.StatusCode, b)
	}
	if out != nil {
		return json.NewDecoder(response.Body).Decode(out)
	}
	_, err = io.Copy(io.Discard, response.Body)
	return err
}

type Inspect struct {
	ID     string `json:"Id"`
	Image  string
	Name   string
	Config struct {
		User   string
		Labels map[string]string
	}
	State struct {
		Status   string
		Running  bool
		ExitCode int
		Health   *struct{ Status string }
	}
	HostConfig struct {
		NetworkMode string
		GroupAdd    []string
	}
	Mounts []struct {
		Type        string
		Name        string
		Source      string
		Destination string
		RW          bool
	}
}

func (e *Engine) Inspect(ctx context.Context, id string) (Inspect, error) {
	var out Inspect
	err := e.request(ctx, "GET", "/containers/"+url.PathEscape(id)+"/json", nil, &out)
	return out, err
}

type WorkerConfig struct {
	Image, User, DataDir, HostRoot, MountRoot, Socket, SocketSource, ProjectSource, Project, Service, SelfID string
	Storage                                                                                                  StorageMount
	GroupAdd                                                                                                 []string
	SocketReadOnly                                                                                           bool
}

var identityPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

func (e *Engine) Discover(ctx context.Context, self, dataDir, hostRoot, mountRoot, socket, project, service string) (WorkerConfig, error) {
	c := WorkerConfig{DataDir: dataDir, HostRoot: hostRoot, MountRoot: mountRoot, Socket: socket, Project: project, Service: service}
	if dataDir != "/data" || !path.IsAbs(mountRoot) || mountRoot == "/" || hostRoot == "" || !path.IsAbs(socket) || !identityPattern.MatchString(project) || !identityPattern.MatchString(service) {
		return c, errors.New("durable worker configuration is incomplete or unsafe")
	}
	inspected, err := e.Inspect(ctx, self)
	if err != nil {
		return c, err
	}
	if inspected.Config.Labels["com.docker.compose.project"] != project || inspected.Config.Labels["com.docker.compose.service"] != service || !inspected.State.Running || !strings.HasPrefix(inspected.Image, "sha256:") {
		return c, errors.New("Services container identity could not be verified")
	}
	c.Image = inspected.Image
	c.User = inspected.Config.User
	c.GroupAdd = append([]string(nil), inspected.HostConfig.GroupAdd...)
	c.SelfID = inspected.ID
	c.Storage, err = ResolveDataMount(inspected)
	if err != nil {
		return c, err
	}
	for _, m := range inspected.Mounts {
		if m.Destination == mountRoot && m.Type == "bind" {
			c.ProjectSource = m.Source
		}
		if m.Destination == socket && m.Type == "bind" {
			c.SocketSource = m.Source
			// A read-only bind still allows Docker API writes through the socket.
			c.SocketReadOnly = !m.RW
		}
	}
	if c.ProjectSource == "" || c.SocketSource == "" {
		return c, errors.New("worker requires Compose root and Docker socket bind mounts")
	}
	return c, nil
}
func (e *Engine) Launch(ctx context.Context, c WorkerConfig, o Operation) error {
	if c.DataDir != "/data" {
		return errors.New("worker durable mount destination must be /data")
	}
	if err := c.Storage.validate(); err != nil {
		return err
	}
	type mount struct {
		Type, Source, Target string
		ReadOnly             bool
	}
	body := struct {
		Image       string
		User        string
		Entrypoint  []string
		Cmd         []string
		Env         []string
		Labels      map[string]string
		Healthcheck struct{ Test []string }
		HostConfig  struct {
			GroupAdd      []string
			Mounts        []mount
			RestartPolicy struct{ Name string }
			AutoRemove    bool
			NetworkMode   string
		}
	}{Image: c.Image, User: c.User, Entrypoint: []string{"/app/services"}, Cmd: []string{"worker", "--operation", o.ID}, Env: []string{"SERVICES_DATA_DIR=" + c.DataDir, "SERVICES_COMPOSE_HOST_ROOT=" + c.HostRoot, "SERVICES_COMPOSE_MOUNT_ROOT=" + c.MountRoot, "DOCKER_SOCKET=" + c.Socket, "SERVICES_PROJECT_NAME=" + c.Project, "SERVICES_SERVICE_NAME=" + c.Service}, Labels: map[string]string{"io.services.worker": "true", "io.services.operation": o.ID, "io.services.schema": "1"}}
	body.HostConfig.GroupAdd = append([]string(nil), c.GroupAdd...)
	body.Healthcheck.Test = []string{"NONE"}
	body.HostConfig.Mounts = []mount{{c.Storage.Type, c.Storage.Source, c.DataDir, false}, {"bind", c.ProjectSource, c.MountRoot, true}, {"bind", c.SocketSource, c.Socket, c.SocketReadOnly}}
	body.HostConfig.RestartPolicy.Name = "no"
	body.HostConfig.NetworkMode = "bridge"
	var created struct {
		ID string `json:"Id"`
	}
	if err := e.request(ctx, "POST", "/containers/create?name="+url.QueryEscape(o.Worker), body, &created); err != nil {
		return err
	}
	return e.Start(ctx, o.Worker)
}
func (e *Engine) Start(ctx context.Context, name string) error {
	return e.request(ctx, "POST", "/containers/"+url.PathEscape(name)+"/start", nil, nil)
}
func (e *Engine) Remove(ctx context.Context, name string) error {
	return e.request(ctx, "DELETE", "/containers/"+url.PathEscape(name), nil, nil)
}
func (e *Engine) ImageID(ctx context.Context, reference string) (string, error) {
	var image struct {
		ID string `json:"Id"`
	}
	err := e.request(ctx, "GET", "/images/"+url.PathEscape(reference)+"/json", nil, &image)
	if err == nil && !strings.HasPrefix(image.ID, "sha256:") {
		err = errors.New("pulled image has no immutable image ID")
	}
	return image.ID, err
}

// ValidateWorkerImage rejects Services targets that cannot participate in the
// durable worker protocol. Call before applying a self-service replacement.
func (e *Engine) ValidateWorkerImage(ctx context.Context, reference string) error {
	return e.validateWorkerImage(ctx, reference, false)
}

func (e *Engine) validateWorkerImage(ctx context.Context, reference string, bindStorage bool) error {
	var image struct {
		Config struct {
			Labels map[string]string
		}
	}
	if err := e.request(ctx, "GET", "/images/"+url.PathEscape(reference)+"/json", nil, &image); err != nil {
		return fmt.Errorf("inspect target worker image: %w", err)
	}
	if image.Config.Labels["io.services.worker.protocol"] != "1" {
		return errors.New("target Services image does not support worker protocol 1")
	}
	if bindStorage && image.Config.Labels["io.services.storage.bind"] != "1" {
		return errors.New("target Services image does not support host-folder storage; refusing self-update before replacement")
	}
	return nil
}

func (e *Engine) Ready(ctx context.Context, id string) error {
	var created struct {
		ID string `json:"Id"`
	}
	if err := e.request(ctx, "POST", "/containers/"+url.PathEscape(id)+"/exec", map[string]any{"AttachStdout": true, "AttachStderr": true, "Cmd": []string{"wget", "-q", "-O", "-", "http://127.0.0.1:8080/api/ready"}}, &created); err != nil {
		return err
	}
	if err := e.request(ctx, "POST", "/exec/"+url.PathEscape(created.ID)+"/start", map[string]any{"Detach": false, "Tty": false}, nil); err != nil {
		return err
	}
	var result struct {
		Running  bool
		ExitCode int
	}
	if err := e.request(ctx, "GET", "/exec/"+url.PathEscape(created.ID)+"/json", nil, &result); err != nil {
		return err
	}
	if result.Running || result.ExitCode != 0 {
		return errors.New("replacement readiness check failed")
	}
	return nil
}
func (e *Engine) Verify(ctx context.Context, oldID, name, expected string, self bool) error {
	var last error
	for {
		inspected, err := e.Inspect(ctx, name)
		if err == nil {
			if inspected.Image != expected {
				last = errors.New("replacement image does not match pulled image")
			} else if !inspected.State.Running || (inspected.State.Health != nil && inspected.State.Health.Status != "healthy") {
				last = errors.New("replacement is not ready")
			} else if self && inspected.ID == oldID {
				last = errors.New("Services container was not replaced")
			} else if self {
				last = e.Ready(ctx, inspected.ID)
				if last == nil {
					return nil
				}
			} else {
				return nil
			}
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("replacement verification: %v: %w", last, ctx.Err())
		case <-time.After(time.Second):
		}
	}
}
