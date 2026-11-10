package dashboard

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"services/internal/auth"
	"services/internal/composeupdate"
	"services/internal/groups"
	"services/internal/icons"
	"services/internal/monitor"
	"services/internal/operations"
)

type ContainerSource interface {
	Containers() ([]groups.Container, error)
}

type UpdateApplier interface {
	Apply([]groups.Container)
}

type ImmediateUpdateChecker interface {
	CheckNow(context.Context, []groups.Container) error
}

type ServiceUpdater interface {
	Pull(context.Context, groups.Container) error
	Update(context.Context, groups.Container) error
}

type originAuthority struct {
	scheme string
	host   string
	port   string
}

type Server struct {
	authRequired         bool
	authStore            *auth.Store
	setupTokenHash       [sha256.Size]byte
	setupTokenPresent    bool
	authAttempts         *loginAttempts
	publicationLegacy    bool
	iconStore            *icons.Store
	publicationHandler   http.Handler
	acknowledgeOperation func(context.Context, string) error
	durable              *operations.Store
	durableProduction    bool
	durableProject       string
	mutationError        error
	source               ContainerSource
	actionSource         ContainerSource
	updates              UpdateApplier
	updater              ServiceUpdater
	allowedHosts         map[string]struct{}
	publicOrigin         *originAuthority
	version              int
	mux                  *http.ServeMux
}

func New(source ContainerSource, version int) *Server {
	return NewWithUpdates(source, nil, version)
}

func NewWithUpdates(source ContainerSource, updates UpdateApplier, version int) *Server {
	return NewWithActions(source, updates, nil, version)
}

func NewWithActions(source ContainerSource, updates UpdateApplier, updater ServiceUpdater, version int) *Server {
	return NewWithActionsAndAllowedHosts(source, updates, updater, version, nil)
}

func NewWithActionsAndAllowedHosts(source ContainerSource, updates UpdateApplier, updater ServiceUpdater, version int, allowedHosts []string) *Server {
	return newWithActionsAndOrigin(source, updates, updater, version, allowedHosts, nil)
}

func NewWithActionsAndPublicOrigin(source ContainerSource, updates UpdateApplier, updater ServiceUpdater, version int, allowedHosts []string, publicOrigin string) (*Server, error) {
	var configuredOrigin *originAuthority
	if publicOrigin != "" {
		parsed, ok := parseOriginAuthority(publicOrigin)
		if !ok {
			return nil, fmt.Errorf("SERVICES_PUBLIC_ORIGIN must be an absolute http or https origin without a path, query, or fragment")
		}
		configuredOrigin = &parsed
	}
	return newWithActionsAndOrigin(source, updates, updater, version, allowedHosts, configuredOrigin), nil
}

func newWithActionsAndOrigin(source ContainerSource, updates UpdateApplier, updater ServiceUpdater, version int, allowedHosts []string, publicOrigin *originAuthority) *Server {
	allowed := make(map[string]struct{}, len(allowedHosts))
	for _, host := range allowedHosts {
		if normalized := normalizeAllowedHost(host); normalized != "" {
			allowed[normalized] = struct{}{}
		}
	}
	if publicOrigin != nil {
		allowed[publicOrigin.host] = struct{}{}
	}
	s := &Server{source: source, updates: updates, updater: updater, allowedHosts: allowed, publicOrigin: publicOrigin, version: version, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/icons", s.listIcons)
	s.mux.HandleFunc("PUT /api/icons", s.putIcon)
	s.mux.HandleFunc("PUT /api/icons/upload", s.uploadIcon)
	s.mux.HandleFunc("DELETE /api/icons", s.deleteIcon)
	s.mux.HandleFunc("GET /api/icons/images/{name}", s.iconImage)
	s.mux.HandleFunc("GET /api/ready", s.ready)
	s.mux.HandleFunc("GET /api/operations", s.operations)
	s.mux.HandleFunc("GET /api/operations/{id}", s.operation)
	s.mux.HandleFunc("POST /api/operations/{id}/acknowledge", s.acknowledge)
	s.mux.HandleFunc("GET /api/projects", s.projects)
	s.mux.HandleFunc("GET /api/monitor", s.monitorInfo)
	s.mux.HandleFunc("POST /api/projects/{name}/update", s.projectUpdate)
	s.mux.HandleFunc("POST /api/updates/check", s.checkUpdates)
	s.mux.HandleFunc("POST /api/images/published", s.imagePublished)
	s.mux.HandleFunc("GET /api/version", s.versionInfo)
	s.mux.HandleFunc("POST /api/containers/{id}/pull", s.pullContainer)
	s.mux.HandleFunc("POST /api/containers/{id}/update", s.updateContainer)
	s.mux.Handle("GET /", http.FileServer(http.Dir("web/dist")))
	return s
}

// SetActionSource makes mutation preflight read live Docker state, not a monitoring cache.
// Configure before serving requests.
func (s *Server) SetActionSource(source ContainerSource) { s.actionSource = source }
func (s *Server) actionContainers() ([]groups.Container, error) {
	if s.actionSource != nil {
		return s.actionSource.Containers()
	}
	return s.source.Containers()
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("X-Frame-Options", "DENY")
	var ok bool
	r, ok = s.authorize(w, r)
	if !ok {
		return
	}
	s.mux.ServeHTTP(w, r)
}

func (s *Server) checkUpdates(w http.ResponseWriter, r *http.Request) {
	if !s.sameOriginAllowed(r) {
		http.Error(w, "Cross-origin action rejected", http.StatusForbidden)
		return
	}
	checker, ok := s.updates.(ImmediateUpdateChecker)
	if !ok {
		http.Error(w, "Update checks are not configured", http.StatusServiceUnavailable)
		return
	}
	containers, err := s.source.Containers()
	if err != nil {
		http.Error(w, "Docker engine unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if err := checker.CheckNow(ctx, containers); err != nil {
		http.Error(w, "One or more registry checks failed", http.StatusBadGateway)
		return
	}
	s.updates.Apply(containers)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) versionInfo(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(struct {
		Version int `json:"version"`
	}{Version: s.version})
}

func (s *Server) monitorInfo(w http.ResponseWriter, _ *http.Request) {
	source, ok := s.source.(interface{ Snapshot() monitor.Snapshot })
	if !ok {
		http.Error(w, "Monitoring is not configured", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(source.Snapshot())
}

func (s *Server) projects(w http.ResponseWriter, r *http.Request) {
	containers, err := s.source.Containers()
	if err != nil {
		http.Error(w, "Docker engine unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.updates != nil {
		s.updates.Apply(containers)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(struct {
		Projects []groups.Project `json:"projects"`
	}{Projects: groups.Aggregate(containers)})
}

func (s *Server) pullContainer(w http.ResponseWriter, r *http.Request) {
	s.containerAction(w, r, "pull")
}

func (s *Server) updateContainer(w http.ResponseWriter, r *http.Request) {
	s.containerAction(w, r, "update")
}

func (s *Server) containerAction(w http.ResponseWriter, r *http.Request, action string) {
	if !s.sameOriginAllowed(r) {
		http.Error(w, "Cross-origin action rejected", http.StatusForbidden)
		return
	}
	if s.durableProduction {
		s.durableContainerAction(w, r, action)
		return
	}
	if s.updater == nil {
		http.Error(w, "Compose actions are not configured", http.StatusServiceUnavailable)
		return
	}
	release, acquired := s.tryComposeAction("")
	if !acquired {
		http.Error(w, "Another Compose action is already running", http.StatusConflict)
		return
	}
	defer release()
	containers, err := s.actionContainers()
	if err != nil {
		http.Error(w, "Docker engine unavailable", http.StatusServiceUnavailable)
		return
	}
	if s.updates != nil {
		s.updates.Apply(containers)
	}
	var selected *groups.Container
	for index := range containers {
		if containers[index].ID == r.PathValue("id") {
			selected = &containers[index]
			break
		}
	}
	if selected == nil {
		http.Error(w, "Container not found", http.StatusNotFound)
		return
	}
	selfProject := os.Getenv("SERVICES_PROJECT_NAME")
	if selfProject == "" {
		selfProject = "services"
	}
	if action == "update" && (!composeProjectName.MatchString(selfProject) || selected.Project == selfProject) {
		http.Error(w, "The Services project cannot update itself; update it externally", http.StatusConflict)
		return
	}
	if selected.State != "running" || (action == "pull" && !selected.UpdateAvailable) {
		http.Error(w, "Container has no available update", http.StatusConflict)
		return
	}

	job := composeOperations.beginLegacy(selected.Project, action)
	defer func() { composeOperations.finishLegacy(job, err) }()
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("X-Accel-Buffering", "no")
	encoder := json.NewEncoder(w)
	var streamMu sync.Mutex
	progress := func(message string) {
		streamMu.Lock()
		defer streamMu.Unlock()
		composeOperations.message(job, message)
		_ = encoder.Encode(map[string]string{"type": "progress", "message": message})
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
	}
	progress("Starting Compose " + action + " for " + selected.Project + "/" + selected.Service)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	ctx = composeupdate.WithProgress(ctx, composeupdate.ProgressFunc(progress))
	if action == "pull" {
		err = s.updater.Pull(ctx, *selected)
	} else {
		err = s.updater.Update(ctx, *selected)
	}
	if err != nil {
		log.Printf("Compose %s failed for project %q service %q: %v", action, selected.Project, selected.Service, err)
		progress("ERROR: " + err.Error())
		_ = encoder.Encode(map[string]string{"type": "error", "message": err.Error()})
		return
	}
	progress("Compose " + action + " completed successfully")
	_ = encoder.Encode(map[string]string{"type": "complete", "message": "Compose " + action + " completed successfully"})
}

func (s *Server) sameOriginAllowed(r *http.Request) bool {
	if s.publicOrigin != nil {
		origin, ok := requestOrigin(r)
		if !ok || origin != *s.publicOrigin {
			return false
		}
	} else if !sameOrigin(r) {
		return false
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host, _, ok := normalizedAuthority(r.Host, scheme)
	return ok && s.allowedOriginHost(host)
}

func (s *Server) allowedOriginHost(host string) bool {
	host = normalizeAllowedHost(host)
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	if address, err := netip.ParseAddr(host); err == nil {
		address = address.Unmap()
		if address.IsLoopback() || tailscaleIPv4Prefix.Contains(address) || tailscaleIPv6Prefix.Contains(address) {
			return true
		}
	}
	_, allowed := s.allowedHosts[host]
	return allowed
}

func normalizeAllowedHost(host string) string {
	host = strings.TrimSpace(host)
	if host == "" || strings.ContainsAny(host, "/\\?#@, \t\r\n") {
		return ""
	}
	host = strings.Trim(host, "[]")
	if address, err := netip.ParseAddr(host); err == nil {
		if address.Zone() != "" {
			return ""
		}
		return strings.ToLower(address.String())
	}
	if strings.ContainsAny(host, ":[]") {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

var (
	tailscaleIPv4Prefix = netip.MustParsePrefix("100.64.0.0/10")
	tailscaleIPv6Prefix = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

func sameOrigin(r *http.Request) bool {
	origin, ok := requestOrigin(r)
	if !ok {
		return false
	}
	expectedScheme := "http"
	if r.TLS != nil {
		expectedScheme = "https"
	}
	if origin.scheme != expectedScheme {
		return false
	}
	requestHost, requestPort, ok := normalizedAuthority(r.Host, expectedScheme)
	return ok && origin.host == requestHost && origin.port == requestPort
}

func requestOrigin(r *http.Request) (originAuthority, bool) {
	values := r.Header.Values("Origin")
	if len(values) != 1 {
		return originAuthority{}, false
	}
	if fetchSite := r.Header.Get("Sec-Fetch-Site"); fetchSite != "" && !strings.EqualFold(fetchSite, "same-origin") {
		return originAuthority{}, false
	}
	return parseOriginAuthority(values[0])
}

func parseOriginAuthority(value string) (originAuthority, bool) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme == "" || parsed.Opaque != "" || parsed.Host == "" || parsed.Path != "" || parsed.RawPath != "" || parsed.User != nil || parsed.ForceQuery || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.RawFragment != "" {
		return originAuthority{}, false
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return originAuthority{}, false
	}
	host, port, ok := normalizedAuthority(parsed.Host, scheme)
	if !ok {
		return originAuthority{}, false
	}
	return originAuthority{scheme: scheme, host: host, port: port}, true
}

func normalizedAuthority(authority, scheme string) (string, string, bool) {
	if authority == "" || strings.ContainsAny(authority, " 	\r\n,") || strings.HasSuffix(authority, ":") {
		return "", "", false
	}
	parsed, err := url.Parse("//" + authority)
	if err != nil || parsed.Host != authority || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", false
	}
	host := parsed.Hostname()
	if host == "" {
		return "", "", false
	}
	port := parsed.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	} else {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", "", false
		}
		port = strconv.Itoa(number)
	}
	return strings.ToLower(host), port, true
}
