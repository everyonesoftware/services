package monitor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type webhook struct {
	url    string
	client *http.Client
}

// NewWebhook returns nil when disabled. Invalid URLs fail safely on delivery.
// Requests contain only Event fields, never Docker inspect output or credentials.
func NewWebhook(address string) Notifier {
	if strings.TrimSpace(address) == "" {
		return nil
	}
	return &webhook{url: address, client: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (w *webhook) Notify(ctx context.Context, event Event) error {
	u, err := url.Parse(w.url)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("invalid notification webhook URL")
	}
	body, err := json.Marshal(event)
	if err != nil {
		return errors.New("encode notification")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, w.url, bytes.NewReader(body))
	if err != nil {
		return errors.New("invalid notification request")
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := w.client.Do(req)
	if err != nil {
		return errors.New("notification delivery failed")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("notification webhook returned non-success status")
	}
	return nil
}

// deliver runs outside the snapshot lock. A failed event stays queued and is
// retried on subsequent polls, before later events, preserving recovery order.
func (m *Monitor) deliver(ctx context.Context) {
	if m.notifier == nil {
		return
	}
	batch, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for len(m.pending) > 0 {
		if batch.Err() != nil {
			return
		}
		err := m.notifier.Notify(batch, m.pending[0])
		if err != nil {
			return
		}
		m.pending = m.pending[1:]
	}
}
