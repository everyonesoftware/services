package dashboard

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"services/internal/groups"
	"services/internal/icons"
)

// Limit the complete JSON body, including trailing whitespace, to 8 KiB.
const maxIconPayloadBytes = 8 << 10

// SetIconStore configures local icon persistence. Call before serving requests;
// nil disables mutation and reports available:false without affecting inventory.
func (s *Server) SetIconStore(store *icons.Store) { s.iconStore = store }
func (s *Server) listIcons(w http.ResponseWriter, r *http.Request) {
	records := []icons.Icon{}
	if s.iconStore != nil {
		records = s.iconStore.List()
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(struct {
		Available bool         `json:"available"`
		Icons     []icons.Icon `json:"icons"`
	}{Available: s.iconStore != nil && s.iconStore.Available(), Icons: records})
}
func (s *Server) putIcon(w http.ResponseWriter, r *http.Request) {
	if !s.sameOriginAllowed(r) {
		http.Error(w, "Cross-origin action rejected", http.StatusForbidden)
		return
	}
	if s.iconStore == nil || !s.iconStore.Available() {
		http.Error(w, "Icon storage unavailable", http.StatusServiceUnavailable)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxIconPayloadBytes)
	var payload *struct {
		Kind string `json:"kind"`
		Key  string `json:"key"`
		URL  string `json:"url"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		iconPayloadError(w, err)
		return
	}
	if payload == nil {
		http.Error(w, "Invalid icon payload", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		iconPayloadError(w, err)
		return
	}
	if !s.iconTargetExists(w, payload.Kind, payload.Key) {
		return
	}
	icon, err := s.iconStore.Put(r.Context(), payload.Kind, payload.Key, payload.URL)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, icons.ErrInvalidInput) {
			status = http.StatusBadRequest
		}
		if errors.Is(err, icons.ErrDownload) {
			status = http.StatusBadGateway
		}
		if errors.Is(err, icons.ErrUnavailable) {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, "Unable to save icon", status)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(icon)
}
func (s *Server) deleteIcon(w http.ResponseWriter, r *http.Request) {
	if !s.sameOriginAllowed(r) {
		http.Error(w, "Cross-origin action rejected", http.StatusForbidden)
		return
	}
	if s.iconStore == nil || !s.iconStore.Available() {
		http.Error(w, "Icon storage unavailable", http.StatusServiceUnavailable)
		return
	}
	// ParseQuery reports malformed escapes rather than silently dropping them.
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["kind"]) != 1 || len(query["key"]) != 1 {
		http.Error(w, "Invalid icon target", http.StatusBadRequest)
		return
	}
	kind, key := query.Get("kind"), query.Get("key")
	if !s.iconTargetExists(w, kind, key) {
		return
	}
	if err := s.iconStore.Delete(kind, key); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, icons.ErrUnavailable) {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, "Unable to reset icon", status)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func iconPayloadError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	var max *http.MaxBytesError
	if errors.As(err, &max) {
		status = http.StatusRequestEntityTooLarge
	}
	http.Error(w, "Invalid icon payload (maximum 8 KiB)", status)
}
func (s *Server) iconTargetExists(w http.ResponseWriter, kind, key string) bool {
	if (kind != "project" && kind != "container") || strings.TrimSpace(key) == "" || len(key) > 512 || strings.ContainsAny(key, "\x00\r\n") {
		http.Error(w, "Invalid icon target", http.StatusBadRequest)
		return false
	}
	if s.source == nil {
		http.Error(w, "Inventory unavailable", http.StatusServiceUnavailable)
		return false
	}
	containers, err := s.source.Containers()
	if err != nil {
		http.Error(w, "Inventory unavailable", http.StatusServiceUnavailable)
		return false
	}
	for _, project := range groups.Aggregate(containers) {
		if kind == "project" && project.Name == key {
			return true
		}
		if kind == "container" {
			for _, container := range project.Containers {
				if container.Name == key {
					return true
				}
			}
		}
	}
	http.Error(w, "Icon target not found", http.StatusNotFound)
	return false
}
func (s *Server) iconImage(w http.ResponseWriter, r *http.Request) {
	if s.iconStore == nil {
		http.NotFound(w, r)
		return
	}
	f, err := s.iconStore.Image(r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(w, r, info.Name(), info.ModTime(), f)
}
