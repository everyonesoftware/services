package icons

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const MaxURLBytes = 4096
const maxRedirects = 5

type downloader struct{ client *http.Client }
type ipResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}
type dialFunc func(context.Context, string, string) (net.Conn, error)

func newDownloader() *downloader {
	dialer := &net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		// Deliberately no Proxy: environment-controlled proxies could bypass IP checks.
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialPublic(ctx, network, address, net.DefaultResolver, dialer.DialContext)
		},
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second,
		MaxResponseHeaderBytes: 32 << 10, IdleConnTimeout: 30 * time.Second, MaxIdleConns: 8, MaxIdleConnsPerHost: 2,
		DisableCompression: true,
	}
	return &downloader{client: &http.Client{Transport: transport, Timeout: 15 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > maxRedirects {
			return errors.New("too many icon redirects")
		}
		return validateURL(req.URL.String())
	}}}
}
func validateURL(raw string) error {
	if len(raw) > MaxURLBytes {
		return errors.New("icon URL exceeds 4096 bytes")
	}
	u, err := url.Parse(raw)
	if err != nil || u == nil || u.Opaque != "" || u.User != nil || u.Host == "" || u.Fragment != "" || u.RawFragment != "" || (u.Scheme != "http" && u.Scheme != "https") {
		return errors.New("icon URL must be a public HTTP(S) URL without credentials or fragment")
	}
	host := u.Hostname()
	if host == "" || strings.ContainsAny(host, "%\\\x00\r\n\t ") {
		return errors.New("invalid icon hostname")
	}
	if strings.HasSuffix(u.Host, ":") {
		return errors.New("invalid icon port")
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return errors.New("invalid icon port")
		}
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !publicAddress(ip) {
			return errors.New("icon address is not public")
		}
	} else {
		if strings.ContainsAny(host, ":[]") || len(host) > 253 {
			return errors.New("invalid icon hostname")
		}
		for _, label := range strings.Split(strings.TrimSuffix(host, "."), ".") {
			if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
				return errors.New("invalid icon hostname")
			}
			for _, c := range label {
				if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
					return errors.New("invalid icon hostname")
				}
			}
		}
	}
	return nil
}
func (d *downloader) Download(ctx context.Context, raw string) ([]byte, error) {
	if err := validateURL(raw); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "image/png, image/jpeg, image/gif")
	response, err := d.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("icon download returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > MaxDownloadBytes {
		return nil, errors.New("icon download exceeds 2 MiB")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, MaxDownloadBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > MaxDownloadBytes {
		return nil, errors.New("icon download exceeds 2 MiB")
	}
	return data, nil
}

// Resolve once, reject the entire set if any answer is nonpublic, and dial only
// validated numeric addresses. Transport still performs TLS verification against
// the original request hostname. DNS rebinding cannot change the socket target.
func dialPublic(ctx context.Context, network, address string, resolver ipResolver, dial dialFunc) (net.Conn, error) {
	// Bound DNS plus all connection attempts, even when Transport's dial context
	// outlives the requesting RoundTrip while it looks for a reusable connection.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	var addresses []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		addresses = []netip.Addr{ip}
	} else {
		addresses, err = resolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, err
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("icon hostname has no addresses")
	}
	for _, ip := range addresses {
		if !publicAddress(ip) {
			return nil, errors.New("icon DNS answer is not public")
		}
	}
	var last error
	for _, ip := range addresses {
		conn, err := dial(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		last = err
	}
	return nil, last
}

var blockedPrefixes = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"), netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"), netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.31.196.0/24"),
	netip.MustParsePrefix("192.52.193.0/24"), netip.MustParsePrefix("192.88.99.0/24"), netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("192.175.48.0/24"), netip.MustParsePrefix("198.18.0.0/15"), netip.MustParsePrefix("198.51.100.0/24"),
	netip.MustParsePrefix("203.0.113.0/24"), netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"), netip.MustParsePrefix("2002::/16"),
	netip.MustParsePrefix("2620:4f:8000::/48"), netip.MustParsePrefix("3fff::/20"),
}
var ipv6Global = netip.MustParsePrefix("2000::/3")

func publicAddress(ip netip.Addr) bool {
	if !ip.IsValid() || ip.Zone() != "" {
		return false
	}
	ip = ip.Unmap()
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	if ip.Is6() && !ipv6Global.Contains(ip) {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}
