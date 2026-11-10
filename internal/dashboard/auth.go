package dashboard

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"services/internal/auth"
)

const sessionCookieName = "services_session"

type authUserContext struct{}
type authKeyContext struct{}

// ConfigureAuthentication enables fail-closed authorization. Production startup
// must call this before serving, and must not start if configuration fails.
func (s *Server) ConfigureAuthentication(store *auth.Store, setupToken string) error {
	s.authRequired = true
	s.authStore = nil
	if store == nil {
		return errors.New("authentication storage is required")
	}
	hasUsers, err := store.HasUsers()
	if err != nil {
		return err
	}
	if !hasUsers && len(setupToken) < 32 {
		return errors.New("first administrator requires a local setup token of at least 32 characters")
	}
	s.authStore = store
	s.setupTokenHash = sha256.Sum256([]byte(setupToken))
	s.setupTokenPresent = setupToken != ""
	s.authAttempts = &loginAttempts{entries: make(map[string]loginAttempt), concurrent: make(chan struct{}, 4)}
	s.mux.HandleFunc("GET /api/auth/status", s.authStatus)
	s.mux.HandleFunc("GET /api/auth/me", s.authMe)
	s.mux.HandleFunc("POST /api/auth/setup", s.authSetup)
	s.mux.HandleFunc("POST /api/auth/login", s.authLogin)
	s.mux.HandleFunc("POST /api/auth/logout", s.authLogout)
	s.mux.HandleFunc("GET /api/auth/users", s.authUsers)
	s.mux.HandleFunc("POST /api/auth/users", s.authCreateUser)
	s.mux.HandleFunc("DELETE /api/auth/users/{id}", s.authDeleteUser)
	s.mux.HandleFunc("PUT /api/auth/password", s.authPassword)
	s.mux.HandleFunc("GET /api/auth/keys", s.authKeys)
	s.mux.HandleFunc("POST /api/auth/keys", s.authCreateKey)
	s.mux.HandleFunc("DELETE /api/auth/keys/{id}", s.authRevokeKey)
	return nil
}

func (s *Server) AuthenticationConfigured() bool { return s.authRequired && s.authStore != nil }

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	if !s.authRequired || !(r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/")) {
		return r, true
	}
	w.Header().Set("Cache-Control", "no-store")
	if s.authStore == nil {
		http.Error(w, "Authentication unavailable", http.StatusServiceUnavailable)
		return r, false
	}
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && (r.URL.Path == "/api/auth/status" || r.URL.Path == "/api/ready") {
		return r, true
	}
	if r.Method == http.MethodPost && (r.URL.Path == "/api/auth/setup" || r.URL.Path == "/api/auth/login") {
		if !s.sameOriginAllowed(r) {
			http.Error(w, "Cross-origin action rejected", http.StatusForbidden)
			return r, false
		}
		return r, true
	}
	// An explicit Authorization header never falls back to browser cookies.
	if credentials := r.Header.Values("Authorization"); len(credentials) != 0 {
		if len(credentials) != 1 || !strings.HasPrefix(credentials[0], "Bearer ") || len(credentials[0]) > 1024 {
			unauthenticated(w)
			return r, false
		}
		key, err := s.authStore.AuthenticateKey(strings.TrimPrefix(credentials[0], "Bearer "))
		if err != nil {
			// Existing notification credentials remain usable only at their original
			// endpoint. The publication handler still validates them in constant time.
			if s.publicationLegacy && r.Method == http.MethodPost && r.URL.Path == "/api/images/published" {
				return r, true
			}
			if !errors.Is(err, auth.ErrUnauthorized) {
				http.Error(w, "Authentication unavailable", http.StatusServiceUnavailable)
			} else {
				unauthenticated(w)
			}
			return r, false
		}
		if !keyRouteAllowed(key, r) {
			http.Error(w, "API key does not permit this operation", http.StatusForbidden)
			return r, false
		}
		if !s.serverClientAllowed(r) {
			http.Error(w, "API keys require a server-to-server request on an allowed host", http.StatusForbidden)
			return r, false
		}
		return r.WithContext(context.WithValue(r.Context(), authKeyContext{}, key)), true
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || len(cookie.Value) > 512 {
		unauthenticated(w)
		return r, false
	}
	user, err := s.authStore.Session(cookie.Value)
	if err != nil {
		if errors.Is(err, auth.ErrUnauthorized) {
			unauthenticated(w)
		} else {
			http.Error(w, "Authentication unavailable", http.StatusServiceUnavailable)
		}
		return r, false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && !s.sameOriginAllowed(r) {
		http.Error(w, "Cross-origin action rejected", http.StatusForbidden)
		return r, false
	}
	if r.URL.Path == "/api/images/published" {
		http.Error(w, "A scoped publisher API key is required", http.StatusForbidden)
		return r, false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		log.Printf("audit user=%s method=%s path=%s", user.ID, r.Method, r.URL.EscapedPath())
	}
	return r.WithContext(context.WithValue(r.Context(), authUserContext{}, user)), true
}

func unauthenticated(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="services"`)
	http.Error(w, "Authentication required", http.StatusUnauthorized)
}

func (s *Server) serverClientAllowed(r *http.Request) bool {
	host, _, ok := normalizedAuthority(r.Host, "http")
	return ok && s.allowedOriginHost(host) && len(r.Header.Values("Origin")) == 0 && len(r.Header.Values("Sec-Fetch-Site")) == 0
}

func keyRouteAllowed(key auth.APIKey, r *http.Request) bool {
	for _, scope := range key.Scopes {
		if scope == "publications:write" && r.Method == http.MethodPost && r.URL.Path == "/api/images/published" {
			return true
		}
		if scope == "services:read" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
			switch r.URL.Path {
			case "/api/projects", "/api/monitor", "/api/operations", "/api/icons", "/api/version", "/api/ready":
				return true
			}
			// Match the mux's registered read routes, not arbitrary API prefixes.
			parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
			if len(parts) == 3 && parts[0] == "api" && parts[1] == "operations" && parts[2] != "" {
				return true
			}
			if len(parts) == 4 && parts[0] == "api" && parts[1] == "icons" && parts[2] == "images" && parts[3] != "" {
				return true
			}
		}
	}
	return false
}

func (s *Server) authStatus(w http.ResponseWriter, _ *http.Request) {
	has, err := s.authStore.HasUsers()
	if err != nil {
		authFailure(w, err)
		return
	}
	authJSON(w, http.StatusOK, map[string]any{"setupRequired": !has})
}

func (s *Server) authMe(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value(authUserContext{}).(auth.User)
	if !ok {
		unauthenticated(w)
		return
	}
	authJSON(w, http.StatusOK, map[string]any{"user": user})
}

type loginInput struct {
	Username   string `json:"username"`
	Password   string `json:"password"`
	SetupToken string `json:"setupToken,omitempty"`
}

func (s *Server) authSetup(w http.ResponseWriter, r *http.Request) {
	if !s.authAttempts.allowRequest(w, r) {
		return
	}
	has, err := s.authStore.HasUsers()
	if err != nil {
		authFailure(w, err)
		return
	}
	if has {
		authFailure(w, auth.ErrConflict)
		return
	}
	var input loginInput
	if !authDecode(w, r, &input) {
		return
	}
	actual := sha256.Sum256([]byte(input.SetupToken))
	if !s.setupTokenPresent || subtle.ConstantTimeCompare(actual[:], s.setupTokenHash[:]) != 1 {
		http.Error(w, "Invalid local setup token", http.StatusForbidden)
		return
	}
	if !s.authAttempts.acquire(w) {
		return
	}
	defer s.authAttempts.release()
	if _, err = s.authStore.Bootstrap(input.Username, input.Password); err != nil {
		authFailure(w, err)
		return
	}
	user, token, err := s.authStore.Login(input.Username, input.Password)
	if err != nil {
		authFailure(w, err)
		return
	}
	s.sessionCookie(w, r, token, 12*60*60)
	log.Printf("audit action=bootstrap user=%s", user.ID)
	authJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (s *Server) authLogin(w http.ResponseWriter, r *http.Request) {
	if !s.authAttempts.allowRequest(w, r) {
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !authDecode(w, r, &input) {
		return
	}
	if !s.authAttempts.acquire(w) {
		return
	}
	defer s.authAttempts.release()
	user, token, err := s.authStore.Login(input.Username, input.Password)
	if err != nil {
		authFailure(w, err)
		return
	}
	s.sessionCookie(w, r, token, 12*60*60)
	log.Printf("audit action=login user=%s", user.ID)
	authJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (s *Server) sessionCookie(w http.ResponseWriter, r *http.Request, token string, maxAge int) {
	secure := r.TLS != nil || (s.publicOrigin != nil && s.publicOrigin.scheme == "https")
	cookie := &http.Cookie{Name: sessionCookieName, Value: token, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge}
	if maxAge > 0 {
		cookie.Expires = time.Now().Add(time.Duration(maxAge) * time.Second)
	} else {
		cookie.Expires = time.Unix(1, 0)
	}
	http.SetCookie(w, cookie)
}

func (s *Server) authLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err == nil {
		err = s.authStore.Logout(cookie.Value)
	}
	if err != nil {
		authFailure(w, err)
		return
	}
	s.sessionCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) authUsers(w http.ResponseWriter, _ *http.Request) {
	users, err := s.authStore.ListUsers()
	if err != nil {
		authFailure(w, err)
		return
	}
	authJSON(w, http.StatusOK, map[string]any{"users": users})
}
func (s *Server) authCreateUser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !authDecode(w, r, &input) {
		return
	}
	user, err := s.authStore.CreateUser(input.Username, input.Password)
	if err != nil {
		authFailure(w, err)
		return
	}
	authJSON(w, http.StatusCreated, map[string]any{"user": user})
}
func (s *Server) authDeleteUser(w http.ResponseWriter, r *http.Request) {
	user, _ := r.Context().Value(authUserContext{}).(auth.User)
	if user.ID == r.PathValue("id") {
		http.Error(w, "You cannot delete your own account", http.StatusConflict)
		return
	}
	if err := s.authStore.DeleteUser(r.PathValue("id")); err != nil {
		authFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) authPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Password string `json:"password"`
	}
	if !authDecode(w, r, &input) {
		return
	}
	user, _ := r.Context().Value(authUserContext{}).(auth.User)
	if err := s.authStore.ChangePassword(user.ID, input.Password); err != nil {
		authFailure(w, err)
		return
	}
	s.sessionCookie(w, r, "", -1)
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) authKeys(w http.ResponseWriter, _ *http.Request) {
	keys, err := s.authStore.ListKeys()
	if err != nil {
		authFailure(w, err)
		return
	}
	authJSON(w, http.StatusOK, map[string]any{"keys": keys})
}
func (s *Server) authCreateKey(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name         string   `json:"name"`
		Scopes       []string `json:"scopes"`
		Repositories []string `json:"repositories"`
		ExpiresAt    string   `json:"expiresAt"`
	}
	if !authDecode(w, r, &input) {
		return
	}
	key, token, err := s.authStore.CreateKey(input.Name, input.Scopes, input.Repositories, input.ExpiresAt)
	if err != nil {
		authFailure(w, err)
		return
	}
	authJSON(w, http.StatusCreated, map[string]any{"key": key, "token": token})
}
func (s *Server) authRevokeKey(w http.ResponseWriter, r *http.Request) {
	if err := s.authStore.RevokeKey(r.PathValue("id")); err != nil {
		authFailure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func authDecode(w http.ResponseWriter, r *http.Request, target any) bool {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || r.Header.Get("Content-Encoding") != "" {
		http.Error(w, "Unencoded application/json required", http.StatusUnsupportedMediaType)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var limit *http.MaxBytesError
		if errors.As(err, &limit) {
			http.Error(w, "Request too large", http.StatusRequestEntityTooLarge)
		} else {
			http.Error(w, "Invalid JSON request", http.StatusBadRequest)
		}
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		http.Error(w, "Exactly one JSON object required", http.StatusBadRequest)
		return false
	}
	return true
}
func authJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
func authFailure(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, auth.ErrUnauthorized):
		unauthenticated(w)
	case errors.Is(err, auth.ErrInvalid):
		http.Error(w, "Invalid input: usernames must be nonempty; passwords require at least 15 characters; keys need supported scopes and exact repositories", http.StatusBadRequest)
	case errors.Is(err, auth.ErrConflict):
		http.Error(w, "Account conflict: setup is complete, the username exists, or the last administrator cannot be deleted", http.StatusConflict)
	case errors.Is(err, auth.ErrNotFound):
		http.Error(w, "Account or API key not found", http.StatusNotFound)
	default:
		http.Error(w, "Authentication storage unavailable", http.StatusServiceUnavailable)
	}
}

type loginAttempt struct {
	start time.Time
	count int
}
type loginAttempts struct {
	mu         sync.Mutex
	entries    map[string]loginAttempt
	concurrent chan struct{}
}

// Rate limits use the direct peer only. Forwarding headers are not trusted;
// callers behind a reverse proxy deliberately share its request bucket.
func (l *loginAttempts) allowRequest(w http.ResponseWriter, r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	now := time.Now()
	l.mu.Lock()
	for key, value := range l.entries {
		if now.Sub(value.start) >= time.Minute {
			delete(l.entries, key)
		}
	}
	entry := l.entries[host]
	if entry.start.IsZero() {
		entry.start = now
	}
	if entry.count >= 10 || (len(l.entries) >= 1024 && entry.count == 0) {
		l.mu.Unlock()
		w.Header().Set("Retry-After", "60")
		http.Error(w, "Too many login attempts; try again later", http.StatusTooManyRequests)
		return false
	}
	entry.count++
	l.entries[host] = entry
	l.mu.Unlock()
	return true
}

// Acquire only after the size-bounded request body has been fully decoded, so
// slow clients cannot occupy the global password-derivation slots.
func (l *loginAttempts) acquire(w http.ResponseWriter) bool {
	select {
	case l.concurrent <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "5")
		http.Error(w, "Authentication busy; try again shortly", http.StatusTooManyRequests)
		return false
	}
}
func (l *loginAttempts) release() { <-l.concurrent }
