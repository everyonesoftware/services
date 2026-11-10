package dashboard

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A request whose body has started arriving but never finishes. Releasing it
// makes parsing fail, without tying test cleanup to an HTTP server timeout.
type stalledAuthBody struct {
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (b *stalledAuthBody) Read([]byte) (int, error) {
	b.once.Do(func() { close(b.entered) })
	<-b.release
	return 0, io.EOF
}
func (b *stalledAuthBody) Close() error { return nil }

func TestIncompleteAuthBodiesDoNotReservePasswordSlots(t *testing.T) {
	for _, endpoint := range []string{"login", "setup"} {
		t.Run(endpoint, func(t *testing.T) {
			s, store := securedServer(t)
			want := http.StatusCreated
			if endpoint == "login" {
				if _, err := store.Bootstrap("admin", "a sufficiently long password"); err != nil {
					t.Fatal(err)
				}
				want = http.StatusOK
			}
			release := make(chan struct{})
			var requests sync.WaitGroup
			defer func() {
				close(release)
				requests.Wait()
			}()
			for i := 0; i < cap(s.authAttempts.concurrent); i++ {
				body := &stalledAuthBody{entered: make(chan struct{}), release: release}
				r := httptest.NewRequest("POST", "http://localhost/api/auth/"+endpoint, body)
				r.RemoteAddr = "192.0.2.1:1234"
				r.Header.Set("Origin", "http://localhost")
				r.Header.Set("Content-Type", "application/json")
				requests.Add(1)
				go func() {
					defer requests.Done()
					s.ServeHTTP(httptest.NewRecorder(), r)
				}()
				select {
				case <-body.entered:
				case <-time.After(5 * time.Second):
					t.Fatal("request never began reading its body")
				}
			}
			input := map[string]string{
				"username": "admin", "password": "a sufficiently long password",
			}
			if endpoint == "setup" {
				input["setupToken"] = "test-bootstrap-token-that-is-at-least-32-bytes"
			}
			w := authRequest(s, "POST", "/api/auth/"+endpoint, input, nil, "")
			if w.Code != want {
				t.Fatalf("incomplete bodies blocked legitimate %s: status=%d want=%d body=%s", endpoint, w.Code, want, w.Body.String())
			}
			if active := len(s.authAttempts.concurrent); active != 0 {
				t.Fatalf("password slots remain reserved after %s: %d", endpoint, active)
			}
		})
	}
}

func TestPasswordConcurrencyLimitStillRejectsCompleteRequests(t *testing.T) {
	s, _ := securedServer(t)
	for i := 0; i < cap(s.authAttempts.concurrent); i++ {
		s.authAttempts.concurrent <- struct{}{}
	}
	for _, endpoint := range []string{"login", "setup"} {
		input := map[string]string{
			"username": "admin", "password": "a sufficiently long password",
		}
		if endpoint == "setup" {
			input["setupToken"] = "test-bootstrap-token-that-is-at-least-32-bytes"
		}
		w := authRequest(s, "POST", "/api/auth/"+endpoint, input, nil, "")
		if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
			t.Fatalf("%s did not retain password concurrency bound: %d", endpoint, w.Code)
		}
	}
}

func TestLoginThrottleUsesPeerNotUntrustedForwardingHeaders(t *testing.T) {
	s, _ := securedServer(t)
	for i := 0; i < 11; i++ {
		r := httptest.NewRequest("POST", "http://localhost/api/auth/login", strings.NewReader(`{"username":"nobody","password":"a sufficiently long password"}`))
		r.RemoteAddr = "192.0.2.1:1234"
		r.Header.Set("Origin", "http://localhost")
		r.Header.Set("Content-Type", "application/json")
		forwarded := fmt.Sprintf("198.51.100.%d", i+1)
		r.Header.Set("X-Forwarded-For", forwarded)
		r.Header.Set("Forwarded", "for="+forwarded)
		r.Header.Set("X-Real-IP", forwarded)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, r)
		want := http.StatusUnauthorized
		if i == 10 {
			want = http.StatusTooManyRequests
		}
		if w.Code != want {
			t.Fatalf("forwarded attempt %d = %d want %d", i, w.Code, want)
		}
	}
	r := httptest.NewRequest("POST", "http://localhost/api/auth/login", strings.NewReader(`{"username":"nobody","password":"a sufficiently long password"}`))
	r.RemoteAddr = "192.0.2.2:1234"
	r.Header.Set("Origin", "http://localhost")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("another peer inherited the exhausted bucket: %d", w.Code)
	}
}
