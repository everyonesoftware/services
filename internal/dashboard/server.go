package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"services/internal/groups"
)

type ContainerSource interface {
	Containers() ([]groups.Container, error)
}

type UpdateApplier interface {
	Apply([]groups.Container)
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
	source       ContainerSource
	updates      UpdateApplier
	updater      ServiceUpdater
	allowedHosts map[string]struct{}
	publicOrigin *originAuthority
	version      int
	mux          *http.ServeMux
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
	s.mux.HandleFunc("GET /api/projects", s.projects)
	s.mux.HandleFunc("GET /api/version", s.versionInfo)
	s.mux.HandleFunc("POST /api/containers/{id}/pull", s.pullContainer)
	s.mux.HandleFunc("POST /api/containers/{id}/update", s.updateContainer)
	s.mux.Handle("GET /", http.FileServer(http.Dir("web/dist")))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) versionInfo(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(struct {
		Version int `json:"version"`
	}{Version: s.version})
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
	if s.updater == nil {
		http.Error(w, "Compose actions are not configured", http.StatusServiceUnavailable)
		return
	}
	containers, err := s.source.Containers()
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
	if selected.State != "running" || !selected.UpdateAvailable {
		http.Error(w, "Container has no available update", http.StatusConflict)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if action == "pull" {
		err = s.updater.Pull(ctx, *selected)
	} else {
		err = s.updater.Update(ctx, *selected)
	}
	if err != nil {
		log.Printf("Compose %s failed for project %q service %q: %v", action, selected.Project, selected.Service, err)
		http.Error(w, "Compose operation failed; the service may be unchanged", http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
