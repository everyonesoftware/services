package dashboard

import (
	"encoding/json"
	"net/http"

	"services/internal/groups"
)

type ContainerSource interface {
	Containers() ([]groups.Container, error)
}

type Server struct {
	source  ContainerSource
	version int
	mux     *http.ServeMux
}

func New(source ContainerSource, version int) *Server {
	s := &Server{source: source, version: version, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/projects", s.projects)
	s.mux.HandleFunc("GET /api/version", s.versionInfo)
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
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(struct {
		Projects []groups.Project `json:"projects"`
	}{Projects: groups.Aggregate(containers)})
}
