package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"services/internal/groups"
)

type fakeSource struct{ containers []groups.Container }

func (f fakeSource) Containers() ([]groups.Container, error) { return f.containers, nil }

type fakeUpdateApplier struct{}

func (fakeUpdateApplier) Apply(containers []groups.Container) {
	containers[0].UpdateAvailable = true
}

type fakeServiceUpdater struct {
	pulls   []string
	updates []string
	err     error
}

func (f *fakeServiceUpdater) Pull(_ context.Context, container groups.Container) error {
	f.pulls = append(f.pulls, container.ID)
	return f.err
}

func (f *fakeServiceUpdater) Update(_ context.Context, container groups.Container) error {
	f.updates = append(f.updates, container.ID)
	return f.err
}

func TestProjectsEndpointReturnsComposeGroupsAndStatus(t *testing.T) {
	server := NewWithUpdates(fakeSource{containers: []groups.Container{
		{ID: "abc", Name: "api-1", Project: "store", Service: "api", State: "running", Health: "healthy", ComposeWorkingDir: `E:\Containers\shop`, ComposeConfigFiles: []string{`E:\Containers\shop\compose.yaml`}, ComposeConfigFilesLabel: `E:\Containers\shop\compose.yaml`},
		{ID: "def", Name: "db-1", Project: "store", Service: "db", State: "exited"},
	}}, fakeUpdateApplier{}, 42)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, httptest.NewRequest("GET", "/api/projects", nil))

	if response.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Projects []groups.Project `json:"projects"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Projects) != 1 || body.Projects[0].Name != "store" || body.Projects[0].Status != groups.StatusUnhealthy {
		t.Fatalf("projects = %+v, want one unhealthy store project", body.Projects)
	}
	if !body.Projects[0].UpdateAvailable || !body.Projects[0].Containers[0].UpdateAvailable {
		t.Fatalf("projects = %+v, want update status on container and group", body.Projects)
	}
	if body.Projects[0].Containers[0].ComposeWorkingDir != "" || len(body.Projects[0].Containers[0].ComposeConfigFiles) != 0 || body.Projects[0].Containers[0].ComposeConfigFilesLabel != "" {
		t.Fatal("internal Compose paths must not be exposed by the dashboard API")
	}
}

func TestPullEndpointPullsTheSelectedUpdatedContainerAndReturnsSuccess(t *testing.T) {
	updater := &fakeServiceUpdater{}
	server := NewWithActionsAndAllowedHosts(fakeSource{containers: []groups.Container{{
		ID: "abc", Name: "api-1", Project: "store", Service: "api", State: "running",
	}}}, fakeUpdateApplier{}, updater, 42, []string{"dashboard.test"})
	request := httptest.NewRequest("POST", "http://dashboard.test/api/containers/abc/pull", nil)
	request.Header.Set("Origin", "http://dashboard.test")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != 200 || !reflect.DeepEqual(updater.pulls, []string{"abc"}) || !strings.Contains(response.Body.String(), `"Compose pull completed successfully"`) {
		t.Fatalf("pull response = %d, calls = %v, body = %s, want progress stream and [abc]", response.Code, updater.pulls, response.Body.String())
	}
}

func TestUpdateEndpointRequiresSameOriginAndLeavesFailuresVisible(t *testing.T) {
	updater := &fakeServiceUpdater{err: errors.New("compose failed")}
	server := NewWithActionsAndAllowedHosts(fakeSource{containers: []groups.Container{{
		ID: "abc", Name: "api-1", Project: "store", Service: "api", State: "running",
	}}}, fakeUpdateApplier{}, updater, 42, []string{"dashboard.test"})
	request := httptest.NewRequest("POST", "http://dashboard.test/api/containers/abc/update", nil)
	request.Header.Set("Origin", "http://dashboard.test")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 200 || !reflect.DeepEqual(updater.updates, []string{"abc"}) || !strings.Contains(response.Body.String(), `"ERROR: compose failed"`) {
		t.Fatalf("update response = %d, calls = %v, body = %s, want streamed error and [abc]", response.Code, updater.updates, response.Body.String())
	}

	request = httptest.NewRequest("POST", "http://dashboard.test/api/containers/abc/update", nil)
	request.Header.Set("Origin", "http://attacker.test")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 403 || len(updater.updates) != 1 {
		t.Fatalf("cross-origin update response = %d, calls = %v, want 403 and no new call", response.Code, updater.updates)
	}
	request = httptest.NewRequest("POST", "http://dashboard.test/api/containers/abc/update", nil)
	request.Header.Set("Origin", "https://dashboard.test")
	response = httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 403 || len(updater.updates) != 1 {
		t.Fatalf("cross-scheme update response = %d, calls = %v, want 403 and no new call", response.Code, updater.updates)
	}
}

func TestUpdateEndpointRejectsUntrustedSameOriginHost(t *testing.T) {
	updater := &fakeServiceUpdater{}
	server := NewWithActions(fakeSource{containers: []groups.Container{{
		ID: "abc", Name: "api-1", Project: "store", Service: "api", State: "running",
	}}}, fakeUpdateApplier{}, updater, 42)
	request := httptest.NewRequest("POST", "http://attacker.example/api/containers/abc/update", nil)
	request.Header.Set("Origin", "http://attacker.example")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)

	if response.Code != 403 || len(updater.updates) != 0 {
		t.Fatalf("untrusted-host update response = %d, calls = %v, want 403 and no calls", response.Code, updater.updates)
	}
}

func TestPullEndpointRejectsContainersWithoutAnAvailableUpdate(t *testing.T) {
	updater := &fakeServiceUpdater{}
	server := NewWithActionsAndAllowedHosts(fakeSource{containers: []groups.Container{{
		ID: "abc", Name: "api-1", Project: "store", Service: "api", State: "running",
	}}}, nil, updater, 42, []string{"dashboard.test"})
	request := httptest.NewRequest("POST", "http://dashboard.test/api/containers/abc/pull", nil)
	request.Header.Set("Origin", "http://dashboard.test")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 409 || len(updater.pulls) != 0 {
		t.Fatalf("pull response = %d, calls = %v, want 409 and no calls", response.Code, updater.pulls)
	}
}

func TestUpdateEndpointAllowsApplyingAnAlreadyPulledImage(t *testing.T) {
	updater := &fakeServiceUpdater{}
	server := NewWithActionsAndAllowedHosts(fakeSource{containers: []groups.Container{{
		ID: "abc", Name: "api-1", Project: "store", Service: "api", State: "running",
	}}}, nil, updater, 42, []string{"dashboard.test"})
	request := httptest.NewRequest("POST", "http://dashboard.test/api/containers/abc/update", nil)
	request.Header.Set("Origin", "http://dashboard.test")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != 200 || !reflect.DeepEqual(updater.updates, []string{"abc"}) || !strings.Contains(response.Body.String(), `"Compose update completed successfully"`) {
		t.Fatalf("update response = %d, calls = %v, body = %s, want progress stream and [abc]", response.Code, updater.updates, response.Body.String())
	}
}

func TestSameOriginValidatesCanonicalAuthority(t *testing.T) {
	cases := []struct {
		name        string
		requestURL  string
		requestHost string
		origin      string
		fetchSite   string
		want        bool
	}{
		{name: "implicit default port", requestURL: "http://dashboard.test/action", requestHost: "dashboard.test", origin: "http://dashboard.test:80", want: true},
		{name: "explicit default port", requestURL: "http://dashboard.test/action", requestHost: "dashboard.test:80", origin: "http://dashboard.test", want: true},
		{name: "empty query marker", requestURL: "http://dashboard.test/action", requestHost: "dashboard.test", origin: "http://dashboard.test?", want: false},
		{name: "path is not an origin", requestURL: "http://dashboard.test/action", requestHost: "dashboard.test", origin: "http://dashboard.test/path", want: false},
		{name: "userinfo is not an origin", requestURL: "http://dashboard.test/action", requestHost: "dashboard.test", origin: "http://user@dashboard.test", want: false},
		{name: "invalid port", requestURL: "http://dashboard.test/action", requestHost: "dashboard.test", origin: "http://dashboard.test:bad", want: false},
		{name: "cross-site fetch metadata", requestURL: "http://dashboard.test/action", requestHost: "dashboard.test", origin: "http://dashboard.test", fetchSite: "cross-site", want: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", testCase.requestURL, nil)
			request.Host = testCase.requestHost
			request.Header.Set("Origin", testCase.origin)
			if testCase.fetchSite != "" {
				request.Header.Set("Sec-Fetch-Site", testCase.fetchSite)
			}
			if got := sameOrigin(request); got != testCase.want {
				t.Fatalf("sameOrigin() = %v, want %v", got, testCase.want)
			}
		})
	}
}

func TestAllowedOriginHostRestrictsActionsToLoopbackAndTailnet(t *testing.T) {
	server := NewWithActions(fakeSource{}, nil, nil, 42)
	cases := []struct {
		host string
		want bool
	}{
		{host: "localhost", want: true},
		{host: "127.0.0.1", want: true},
		{host: "::1", want: true},
		{host: "100.64.0.1", want: true},
		{host: "100.127.255.254", want: true},
		{host: "100.128.0.1", want: false},
		{host: "fd7a:115c:a1e0::1", want: true},
		{host: "fd7a:115c:a1e1::1", want: false},
		{host: "services.example.net", want: false},
	}
	for _, testCase := range cases {
		t.Run(testCase.host, func(t *testing.T) {
			if got := server.allowedOriginHost(testCase.host); got != testCase.want {
				t.Fatalf("allowedOriginHost(%q) = %v, want %v", testCase.host, got, testCase.want)
			}
		})
	}
}

func TestAllowedOriginHostAcceptsExplicitHostname(t *testing.T) {
	server := NewWithActionsAndAllowedHosts(fakeSource{}, nil, nil, 42, []string{" Services.Example.Net. "})
	if !server.allowedOriginHost("services.example.net") {
		t.Fatal("configured hostname should be allowed case-insensitively")
	}
	if server.allowedOriginHost("attacker.example.net") {
		t.Fatal("unconfigured hostname should not be allowed")
	}
}

func TestPublicOriginAllowsTlsTerminatedProxyRequests(t *testing.T) {
	for _, requestHost := range []string{"127.0.0.1:8080", "services.tail1a7c8f.ts.net"} {
		t.Run(requestHost, func(t *testing.T) {
			updater := &fakeServiceUpdater{}
			server, err := NewWithActionsAndPublicOrigin(fakeSource{containers: []groups.Container{{
				ID: "abc", Name: "api-1", Project: "store", Service: "api", State: "running",
			}}}, fakeUpdateApplier{}, updater, 42, nil, "https://services.tail1a7c8f.ts.net")
			if err != nil {
				t.Fatalf("construct server: %v", err)
			}
			request := httptest.NewRequest("POST", "http://"+requestHost+"/api/containers/abc/update", nil)
			request.Header.Set("Origin", "https://services.tail1a7c8f.ts.net")
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)

			if response.Code != 200 || !reflect.DeepEqual(updater.updates, []string{"abc"}) || !strings.Contains(response.Body.String(), `"Compose update completed successfully"`) {
				t.Fatalf("proxy update response = %d, calls = %v, body = %s, want progress stream and [abc]", response.Code, updater.updates, response.Body.String())
			}
		})
	}
}

func TestPublicOriginRejectsMismatchedOriginsAndHosts(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		requestHost string
		origin      string
	}{
		{name: "cross-origin", requestHost: "127.0.0.1:8080", origin: "https://attacker.example"},
		{name: "untrusted host", requestHost: "attacker.example", origin: "https://services.tail1a7c8f.ts.net"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			updater := &fakeServiceUpdater{}
			server, err := NewWithActionsAndPublicOrigin(fakeSource{containers: []groups.Container{{
				ID: "abc", Name: "api-1", Project: "store", Service: "api", State: "running",
			}}}, fakeUpdateApplier{}, updater, 42, nil, "https://services.tail1a7c8f.ts.net")
			if err != nil {
				t.Fatalf("construct server: %v", err)
			}
			request := httptest.NewRequest("POST", "http://"+testCase.requestHost+"/api/containers/abc/update", nil)
			request.Header.Set("Origin", testCase.origin)
			request.Header.Set("Sec-Fetch-Site", "same-origin")
			response := httptest.NewRecorder()
			server.ServeHTTP(response, request)

			if response.Code != 403 || len(updater.updates) != 0 {
				t.Fatalf("request response = %d, calls = %v, want 403 and no calls", response.Code, updater.updates)
			}
		})
	}
}

func TestPublicOriginMustBeAnOriginOnlyUrl(t *testing.T) {
	for _, origin := range []string{"https://services.tail1a7c8f.ts.net/", "https://user@services.tail1a7c8f.ts.net", "https://services.tail1a7c8f.ts.net/path"} {
		if _, err := NewWithActionsAndPublicOrigin(fakeSource{}, nil, nil, 42, nil, origin); err == nil {
			t.Errorf("constructor accepted non-origin URL %q", origin)
		}
	}
}
