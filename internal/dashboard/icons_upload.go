package dashboard

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"

	"services/internal/icons"
)

// The upload body is the image itself, not multipart data. Filenames are neither
// needed nor accepted as storage paths; the store chooses a content-derived name.
func (s *Server) uploadIcon(w http.ResponseWriter, r *http.Request) {
	if !s.sameOriginAllowed(r) {
		http.Error(w, "Cross-origin action rejected", http.StatusForbidden)
		return
	}
	if s.iconStore == nil || !s.iconStore.Available() {
		http.Error(w, "Icon storage unavailable", http.StatusServiceUnavailable)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(query["kind"]) != 1 || len(query["key"]) != 1 {
		http.Error(w, "Invalid icon target", http.StatusBadRequest)
		return
	}
	kind, key := query.Get("kind"), query.Get("key")
	if !s.iconTargetExists(w, kind, key) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, icons.MaxDownloadBytes)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		status := http.StatusBadRequest
		var oversized *http.MaxBytesError
		if errors.As(err, &oversized) {
			status = http.StatusRequestEntityTooLarge
		}
		http.Error(w, "Unable to read image upload (maximum 2 MiB)", status)
		return
	}
	icon, err := s.iconStore.Upload(r.Context(), kind, key, data)
	if err != nil {
		status := http.StatusInternalServerError
		message := "Unable to save uploaded icon"
		if errors.Is(err, icons.ErrInvalidInput) {
			status = http.StatusBadRequest
			message = "Choose a valid PNG, JPEG, GIF or restricted SVG image: maximum 2 MiB, 4 million pixels and 4096 pixels per side; SVG complexity is limited and output is static PNG up to 512 pixels per side"
		}
		if errors.Is(err, icons.ErrUnavailable) {
			status = http.StatusServiceUnavailable
		}
		http.Error(w, message, status)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(icon)
}
