package dockerapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestContainersContextDoesNotIgnoreCancellationDuringOptionalImages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			w.Write([]byte(`{"ApiVersion":"1.47"}`))
		case "/v1.47/containers/json":
			w.Write([]byte(`[]`))
		case "/v1.47/images/json":
			cancel()
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	if _, err := New(server.URL, server.Client()).ContainersContext(ctx); err == nil {
		t.Fatal("canceled refresh reported success")
	}
}
func TestContainersContextCancelsEngineRequest(t *testing.T) {
	reached := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(reached); <-r.Context().Done() }))
	defer server.Close()
	client := New(server.URL, server.Client())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := client.ContainersContext(ctx); done <- err }()
	select {
	case <-reached:
	case <-time.After(time.Second):
		t.Fatal("request not started")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected cancellation")
		}
	case <-time.After(time.Second):
		t.Fatal("request was not canceled")
	}
}
