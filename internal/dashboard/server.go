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
	source ContainerSource
	mux    *http.ServeMux
}

func New(source ContainerSource) *Server {
	s := &Server{source: source, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/projects", s.projects)
	s.mux.Handle("GET /", http.FileServer(http.Dir("web/dist")))
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
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
