package dockerapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func logFrame(stream byte, text string) []byte {
	b := make([]byte, 8)
	b[0] = stream
	binary.BigEndian.PutUint32(b[4:], uint32(len(text)))
	return append(b, []byte(text)...)
}

func TestLogsReassemblesFramesAndPreservesDuplicates(t *testing.T) {
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version":
			fmt.Fprint(w, `{"ApiVersion":"1.51"}`)
		case "/v1.51/containers/abc/json":
			fmt.Fprint(w, `{"Config":{"Tty":false}}`)
		case "/v1.51/containers/abc/logs":
			if r.URL.Query().Get("timestamps") != "1" || r.URL.Query().Get("stderr") != "1" || r.URL.Query().Get("follow") != "0" {
				t.Errorf("query: %s", r.URL.RawQuery)
			}
			w.Write(bytes.Join([][]byte{logFrame(1, "2026-01-01T00:00:00Z he"), logFrame(2, "2026-01-01T00:00:00Z error\n"), logFrame(1, "llo\n2026-01-01T00:00:00Z hello\n")}, nil))
		default:
			http.NotFound(w, r)
		}
	}))
	defer fixture.Close()
	rows, err := New(fixture.URL, fixture.Client()).Logs(context.Background(), "abc", LogOptions{Tail: 201})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 || rows[0].Message != "error" || rows[0].Stream != "stderr" || rows[1].Message != "hello" || rows[2].Message != "hello" {
		t.Fatalf("rows=%+v", rows)
	}
}
