package icons

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type resolverFunc func(context.Context, string) ([]netip.Addr, error)

func (f resolverFunc) LookupNetIP(c context.Context, _, h string) ([]netip.Addr, error) {
	return f(c, h)
}

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestDownloaderRejectsUnsafeURLsBeforeTransport(t *testing.T) {
	d := newDownloader()
	calls := 0
	d.client.Transport = transportFunc(func(*http.Request) (*http.Response, error) { calls++; return nil, errors.New("unexpected transport") })
	for _, u := range []string{"file:///etc/passwd", "ftp://example.com/x", "data:image/png;base64,x", "http://user:pass@example.com/x", "http://127.0.0.1/x", "http://[::1]/x", "http://100.100.100.100/x", "http://[fd7a:115c:a1e0::1]/x", "http://example.com:0/x", "http://example.com:65536/x", "http://example.com/x#fragment", "http://example.com/" + strings.Repeat("x", 4096)} {
		if _, err := d.Download(context.Background(), u); err == nil {
			t.Errorf("accepted %q", u)
		}
	}
	if calls != 0 {
		t.Fatalf("unsafe URL reached transport %d times", calls)
	}
}
func TestPublicAddressPolicy(t *testing.T) {
	for _, ip := range []string{"0.0.0.0", "0.1.2.3", "10.0.0.1", "127.0.0.1", "169.254.169.254", "172.31.0.1", "192.168.0.1", "192.0.0.8", "192.0.2.1", "192.88.99.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.1.2.3", "255.255.255.255", "100.64.0.1", "100.127.255.254", "::", "::1", "::ffff:127.0.0.1", "fc00::1", "fe80::1", "ff02::1", "2001:db8::1", "2002:7f00:1::1", "64:ff9b::7f00:1", "2001::1", "3fff::1"} {
		if publicAddress(netip.MustParseAddr(ip)) {
			t.Errorf("accepted reserved IP %s", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if !publicAddress(netip.MustParseAddr(ip)) {
			t.Errorf("rejected public IP %s", ip)
		}
	}
}
func TestDialValidatesAllDNSAnswersAndPinsPublicAddress(t *testing.T) {
	called := ""
	lookups := 0
	resolver := resolverFunc(func(context.Context, string) ([]netip.Addr, error) {
		lookups++
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	})
	dial := func(_ context.Context, network, address string) (net.Conn, error) {
		called = address
		return nil, errors.New("dial stopped")
	}
	_, _ = dialPublic(context.Background(), "tcp", "example.com:443", resolver, dial)
	if called != "8.8.8.8:443" || lookups != 1 {
		t.Fatalf("dial %q lookups %d", called, lookups)
	}
	for _, answers := range [][]netip.Addr{nil, {netip.MustParseAddr("127.0.0.1")}, {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("10.0.0.1")}} {
		called = ""
		resolver = resolverFunc(func(context.Context, string) ([]netip.Addr, error) { return answers, nil })
		if _, err := dialPublic(context.Background(), "tcp", "example.com:80", resolver, dial); err == nil || called != "" {
			t.Fatalf("DNS answers %v reached %q: %v", answers, called, err)
		}
	}
}
func TestDialBoundsDNSAndConnectionWorkWithDeadline(t *testing.T) {
	resolver := resolverFunc(func(ctx context.Context, _ string) ([]netip.Addr, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Fatal("DNS does not have a bounded deadline")
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	})
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("numeric dial does not have deadline")
		}
		return nil, errors.New("offline")
	}
	_, _ = dialPublic(context.Background(), "tcp", "example.com:443", resolver, dial)
}
func TestDownloaderRedirectSafetyAndBounds(t *testing.T) {
	for _, target := range []string{"http://127.0.0.1/secret", "http://user:pass@example.com/logo", "file:///etc/passwd", "http://[::ffff:10.0.0.1]/x"} {
		d := newDownloader()
		calls := 0
		d.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{target}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		})
		if _, err := d.Download(context.Background(), "https://example.com/start"); err == nil || calls != 1 {
			t.Fatalf("redirect %q calls=%d err=%v", target, calls, err)
		}
	}
	d := newDownloader()
	calls := 0
	d.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 302, Header: http.Header{"Location": []string{"/next"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
	})
	if _, err := d.Download(context.Background(), "https://example.com/start"); err == nil || calls > 6 {
		t.Fatalf("unbounded redirects calls=%d err=%v", calls, err)
	}
}
func TestDownloaderRevalidatesRedirectDNSBeforeDial(t *testing.T) {
	d := newDownloader()
	var mu sync.Mutex
	var dialed []string
	resolver := resolverFunc(func(_ context.Context, host string) ([]netip.Addr, error) {
		if host == "start.example" {
			return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
		}
		return []netip.Addr{netip.MustParseAddr("127.0.0.1")}, nil
	})
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		mu.Lock()
		dialed = append(dialed, address)
		mu.Unlock()
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			_, err := http.ReadRequest(bufio.NewReader(server))
			if err != nil {
				return
			}
			_, _ = io.WriteString(server, "HTTP/1.1 302 Found\r\nLocation: http://redirect.example/secret\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		}()
		return client, nil
	}
	tr := d.client.Transport.(*http.Transport)
	tr.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialPublic(ctx, network, address, resolver, dial)
	}
	defer tr.CloseIdleConnections()
	if _, err := d.Download(context.Background(), "http://start.example/logo"); err == nil {
		t.Fatal("private redirect DNS accepted")
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(dialed, []string{"8.8.8.8:80"}) {
		t.Fatalf("unsafe or unpinned dial: %v", dialed)
	}
}
func TestDownloaderNoAmbientStateAndSizeCap(t *testing.T) {
	d := newDownloader()
	tr := d.client.Transport.(*http.Transport)
	if tr.Proxy != nil || d.client.Jar != nil || d.client.Timeout <= 0 || d.client.Timeout > 30*time.Second || tr.ResponseHeaderTimeout <= 0 || tr.MaxResponseHeaderBytes <= 0 {
		t.Fatal("missing secure HTTP defaults")
	}
	for _, size := range []int{MaxDownloadBytes, MaxDownloadBytes + 1} {
		d.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || r.Header.Get("Proxy-Authorization") != "" {
				t.Fatal("ambient credentials")
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: -1, Body: io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{'x'}, size))), Request: r}, nil
		})
		got, err := d.Download(context.Background(), "https://example.com/logo")
		if size == MaxDownloadBytes {
			if err != nil || len(got) != size {
				t.Fatalf("at bound %d %v", len(got), err)
			}
		} else if err == nil {
			t.Fatal("accepted oversized download")
		}
	}
	for _, status := range []int{201, 404, 500} {
		d.client.Transport = transportFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader("x")), Header: make(http.Header), Request: r}, nil
		})
		if _, err := d.Download(context.Background(), "https://example.com/logo"); err == nil {
			t.Fatal(fmt.Sprintf("status %d accepted", status))
		}
	}
}
