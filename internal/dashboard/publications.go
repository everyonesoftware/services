package dashboard

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"time"

	"services/internal/auth"
	"services/internal/groups"
	"services/internal/updates"
)

// PublicationReceiver only records metadata; it cannot authorize Compose work.
type PublicationReceiver interface {
	AcceptPublication(context.Context, groups.ImagePublication) (string, error)
}

var publicationRepository = regexp.MustCompile(`^ghcr\.io/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*(?:/[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*)+$`)

// ConfigurePublicationNotifications must be called before serving requests.
// Invalid configuration leaves the endpoint disabled, including on reconfiguration.
func (s *Server) ConfigurePublicationNotifications(token string, repositories []string, receiver PublicationReceiver) error {
	s.publicationHandler = nil
	s.publicationLegacy = false
	if len(token) < 32 || len(token) > 512 || strings.IndexFunc(token, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return errors.New("publication token must contain 32 to 512 printable non-space ASCII characters")
	}
	if receiver == nil || len(repositories) == 0 || len(repositories) > 256 {
		return errors.New("publication receiver and explicit repository allowlist are required")
	}
	allowed := make(map[string]struct{}, len(repositories))
	for _, repository := range repositories {
		if len(repository) > 255 || !publicationRepository.MatchString(repository) {
			return errors.New("publication allowlist must contain exact untagged lowercase GHCR repository names")
		}
		allowed[repository] = struct{}{}
	}
	s.publicationHandler = s.newPublicationHandler(token, allowed, receiver)
	s.publicationLegacy = true
	return nil
}

// ConfigurePublicationReceiver enables ingestion using managed API keys. Each key
// supplies its own repository allowlist; no environment credential is required.
func (s *Server) ConfigurePublicationReceiver(receiver PublicationReceiver) error {
	s.publicationHandler = nil
	s.publicationLegacy = false
	if !s.AuthenticationConfigured() || receiver == nil {
		return errors.New("managed publication notifications require authentication and a receiver")
	}
	s.publicationHandler = s.newPublicationHandler("", nil, receiver)
	return nil
}

func (s *Server) newPublicationHandler(token string, legacyAllowed map[string]struct{}, receiver PublicationReceiver) http.Handler {
	expected := sha256.Sum256([]byte(token))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		allowed := legacyAllowed
		key, managed := r.Context().Value(authKeyContext{}).(auth.APIKey)
		if managed {
			allowed = make(map[string]struct{}, len(key.Repositories))
			for _, repository := range key.Repositories {
				allowed[repository] = struct{}{}
			}
		} else {
			credentials := r.Header.Values("Authorization")
			if token == "" || len(credentials) != 1 || !strings.HasPrefix(credentials[0], "Bearer ") {
				w.Header().Set("WWW-Authenticate", `Bearer realm="image-publication"`)
				http.Error(w, "Notification credential required", http.StatusUnauthorized)
				return
			}
			actual := sha256.Sum256([]byte(strings.TrimPrefix(credentials[0], "Bearer ")))
			if subtle.ConstantTimeCompare(actual[:], expected[:]) != 1 {
				http.Error(w, "Invalid notification credential", http.StatusUnauthorized)
				return
			}
		}
		host, _, validHost := normalizedAuthority(r.Host, "http")
		if !validHost || !s.allowedOriginHost(host) || len(r.Header.Values("Origin")) != 0 || len(r.Header.Values("Sec-Fetch-Site")) != 0 {
			http.Error(w, "Server-to-server notification required on an allowed host", http.StatusForbidden)
			return
		}
		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if err != nil || mediaType != "application/json" || r.Header.Get("Content-Encoding") != "" {
			http.Error(w, "Unencoded application/json required", http.StatusUnsupportedMediaType)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		defer r.Body.Close()
		body, err := io.ReadAll(r.Body)
		if err != nil {
			var limit *http.MaxBytesError
			if errors.As(err, &limit) {
				http.Error(w, "Publication exceeds 64 KiB", http.StatusRequestEntityTooLarge)
			} else {
				http.Error(w, "Cannot read publication", http.StatusBadRequest)
			}
			return
		}
		var publication groups.ImagePublication
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&publication); err != nil {
			http.Error(w, "Invalid publication JSON", http.StatusBadRequest)
			return
		}
		var trailing any
		if err := decoder.Decode(&trailing); err != io.EOF {
			http.Error(w, "Exactly one publication object required", http.StatusBadRequest)
			return
		}
		if err := updates.ValidatePublication(publication); err != nil {
			http.Error(w, "Invalid publication metadata", http.StatusBadRequest)
			return
		}
		if _, ok := allowed[publication.Repository]; !ok {
			http.Error(w, "Image repository is outside credential scope", http.StatusForbidden)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		result, err := receiver.AcceptPublication(ctx, publication)
		if err != nil {
			if errors.Is(err, updates.ErrPublicationConflict) {
				http.Error(w, "Publication conflicts with an existing event", http.StatusConflict)
			} else {
				http.Error(w, "Cannot persist publication notification", http.StatusServiceUnavailable)
			}
			return
		}
		if result != "accepted" && result != "duplicate" && result != "stale" {
			http.Error(w, "Unexpected publication result", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = fmt.Fprintf(w, "{\"result\":%q}\n", result)
	})
}

func (s *Server) imagePublished(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if s.publicationHandler == nil {
		http.Error(w, "Publication notifications are not configured", http.StatusServiceUnavailable)
		return
	}
	s.publicationHandler.ServeHTTP(w, r)
}
