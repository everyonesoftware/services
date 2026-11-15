package dockerapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLogsUsesUnixNanosecondBoundsAndTTYRaw(t *testing.T) {
	since := time.Date(2026, 1, 1, 0, 0, 0, 123456789, time.UTC)
	until := since.Add(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"ApiVersion":"1.51"}`)
		case "/v1.51/containers/abc/json":
			fmt.Fprint(w, `{"Config":{"Tty":true}}`)
		case "/v1.51/containers/abc/logs":
			if r.URL.Query().Get("since") != strconv.FormatInt(since.Unix(), 10)+".123456789" || r.URL.Query().Get("until") != strconv.FormatInt(until.Unix(), 10)+".123456789" {
				t.Errorf("bounds=%s", r.URL.RawQuery)
			}
			fmt.Fprint(w, "2026-01-01T00:00:00.123456789Z tty line\n2026-01-01T00:00:00.123456789Z partial")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	rows, err := New(server.URL, server.Client()).Logs(context.Background(), "abc", LogOptions{Since: since, Until: until})
	if err != nil || len(rows) != 2 || rows[0].Stream != "tty" || rows[1].Message != "partial" || !rows[0].Timestamp.Equal(since) {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestDecodeLogsSafetyLimitsAndMalformedFrames(t *testing.T) {
	huge := make([]byte, 8)
	huge[0] = 1
	binary.BigEndian.PutUint32(huge[4:], ^uint32(0))
	for _, test := range []struct {
		name string
		body []byte
		want error
	}{
		{"byte cap mid frame", bytes.Repeat(logFrame(1, "2026-01-01T00:00:00Z "+strings.Repeat("x", 10000)+"\n"), 900), ErrLogLimit},
		{"huge frame", huge, ErrLogLimit},
		{"overlong line", logFrame(1, "2026-01-01T00:00:00Z "+strings.Repeat("x", maxLogLine+1)), ErrLogLimit},
		{"broken frame", []byte{1, 0}, ErrLogsUnavailable},
		{"invalid timestamp", logFrame(1, "sensitive log content\n"), ErrLogsUnavailable},
		{"too many lines", logFrame(1, strings.Repeat("2026-01-01T00:00:00Z x\n", maxLogRecords+1)), ErrLogLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows, err := decodeLogs(bytes.NewReader(test.body), false)
			if !errors.Is(err, test.want) || rows != nil {
				t.Fatalf("rows=%d err=%v", len(rows), err)
			}
		})
	}
}

func TestLogsBoundsInspectMetadataRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"ApiVersion":"1.51"}`)
		case "/v1.51/containers/abc/json":
			fmt.Fprintf(w, `{"Unneeded":"%s","Config":{"Tty":true}}`, strings.Repeat("x", 2<<20))
		default:
			fmt.Fprint(w, "2026-01-01T00:00:00Z secret\n")
		}
	}))
	defer server.Close()
	rows, err := New(server.URL, server.Client()).Logs(context.Background(), "abc", LogOptions{})
	if !errors.Is(err, ErrLogsUnavailable) || len(rows) > 0 {
		t.Fatalf("unbounded inspect accepted: rows=%d err=%v", len(rows), err)
	}
}

func TestLogsEngineFailureIsSafeAndCancellationStopsRead(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			fmt.Fprint(w, `{"ApiVersion":"1.51"}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/json") {
			fmt.Fprint(w, `{"Config":{"Tty":false}}`)
			return
		}
		if strings.Contains(r.URL.Path, "missing") {
			http.Error(w, "secret response snippet", 404)
			return
		}
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	c := New(server.URL, server.Client())
	if _, err := c.Logs(context.Background(), "missing", LogOptions{}); !errors.Is(err, ErrLogsUnavailable) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("error=%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.Logs(ctx, "abc", LogOptions{}); err == nil || ctx.Err() == nil {
		t.Fatalf("cancellation=%v ctx=%v", err, ctx.Err())
	}
}
