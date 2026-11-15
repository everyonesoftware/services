package dockerapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LogOptions provides Engine timestamp bounds; Tail zero means all.
type LogOptions struct {
	Since, Until time.Time
	Tail         int
}
type LogRecord struct {
	Timestamp       time.Time
	Stream, Message string
}

var ErrLogLimit = errors.New("container log read exceeded its safety limit")
var ErrLogsUnavailable = errors.New("container logs unavailable")
var ErrAmbiguousTTYLogs = errors.New("raw TTY partial log boundaries are ambiguous")

var logTimestampPattern = regexp.MustCompile(`[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(?:\.[0-9]{1,9})?(?:Z|[+-][0-9]{2}:[0-9]{2}) `)

const maxLogBytes = 8 << 20
const maxLogLine = 256 << 10
const maxLogRecords = 20000

// Logs reads a finite, bounded Engine response. It never returns a silently
// truncated success. Errors deliberately exclude engine bodies and log text.
func (c *Client) Logs(ctx context.Context, id string, opts LogOptions) ([]LogRecord, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var version engineVersion
	if c.logMetadata(ctx, "/version", &version) != nil || version.APIVersion == "" {
		return nil, ErrLogsUnavailable
	}
	prefix := "/v" + version.APIVersion + "/containers/" + url.PathEscape(id)
	var inspect struct {
		Config struct {
			TTY bool `json:"Tty"`
		} `json:"Config"`
	}
	if c.logMetadata(ctx, prefix+"/json", &inspect) != nil {
		return nil, ErrLogsUnavailable
	}
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "timestamps": {"1"}, "follow": {"0"}, "tail": {"all"}}
	if opts.Tail > 0 {
		q.Set("tail", strconv.Itoa(opts.Tail))
	}
	if !opts.Since.IsZero() {
		q.Set("since", fmt.Sprintf("%d.%09d", opts.Since.Unix(), opts.Since.Nanosecond()))
	}
	if !opts.Until.IsZero() {
		q.Set("until", fmt.Sprintf("%d.%09d", opts.Until.Unix(), opts.Until.Nanosecond()))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+prefix+"/logs?"+q.Encode(), nil)
	if err != nil {
		return nil, ErrLogsUnavailable
	}
	res, err := c.http.Do(req)
	if err != nil {
		return nil, ErrLogsUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, ErrLogsUnavailable
	}
	return decodeLogs(res.Body, inspect.Config.TTY)
}

func (c *Client) logMetadata(ctx context.Context, path string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return ErrLogsUnavailable
	}
	res, err := c.http.Do(req)
	if err != nil {
		return ErrLogsUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ErrLogsUnavailable
	}
	if json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(target) != nil {
		return ErrLogsUnavailable
	}
	return nil
}

// logTimestampPrefix distinguishes a complete record header from a bounded
// prefix split across multiplex frames. Digit positions are wildcards; the
// complete timestamp is always validated by time.Parse before acceptance.
func logTimestampPrefix(data []byte) (complete, possible bool) {
	if space := bytes.IndexByte(data[:min(len(data), 40)], ' '); space > 0 {
		_, err := time.Parse(time.RFC3339Nano, string(data[:space]))
		return err == nil, false
	}
	if len(data) > 35 {
		return false, false
	}
	for fraction := 0; fraction <= 9; fraction++ {
		base := "2006-01-02T15:04:05"
		if fraction > 0 {
			base += "." + strings.Repeat("0", fraction)
		}
		for _, zone := range []string{"Z", "+00:00", "-00:00"} {
			pattern := base + zone
			if len(data) > len(pattern) {
				continue
			}
			matches := true
			for i, character := range data {
				if pattern[i] >= '0' && pattern[i] <= '9' {
					matches = matches && character >= '0' && character <= '9'
				} else {
					matches = matches && character == pattern[i]
				}
			}
			if matches {
				return false, true
			}
		}
	}
	return false, false
}

func decodeLogs(input io.Reader, tty bool) ([]LogRecord, error) {
	limited := &io.LimitedReader{R: input, N: maxLogBytes + 1}
	rows := make([]LogRecord, 0)
	pending := map[byte][]byte{}
	candidates := map[byte][]byte{}
	emit := func(stream byte, line []byte) error {
		stamp, message, ok := strings.Cut(string(line), " ")
		ts, err := time.Parse(time.RFC3339Nano, stamp)
		if !ok || err != nil {
			return ErrLogsUnavailable
		}
		name := "stdout"
		if stream == 2 {
			name = "stderr"
		}
		if tty {
			// Raw TTY has no frame metadata. An embedded valid record header
			// could be either injected by Engine or application text: refuse
			// this ambiguous window rather than corrupt it or invent a split.
			for _, embedded := range logTimestampPattern.FindAllString(message, -1) {
				if _, err := time.Parse(time.RFC3339Nano, strings.TrimSuffix(embedded, " ")); err == nil {
					return ErrAmbiguousTTYLogs
				}
			}
			name = "tty"
		}
		rows = append(rows, LogRecord{Timestamp: ts, Stream: name, Message: message})
		if len(rows) > maxLogRecords {
			return ErrLogLimit
		}
		return nil
	}
	consume := func(stream byte, data []byte) error {
		p := append(pending[stream], data...)
		for {
			i := strings.IndexByte(string(p), '\n')
			if i < 0 {
				break
			}
			if i > maxLogLine {
				return ErrLogLimit
			}
			if err := emit(stream, p[:i]); err != nil {
				return err
			}
			p = p[i+1:]
		}
		if len(p) > maxLogLine {
			return ErrLogLimit
		}
		pending[stream] = append([]byte(nil), p...)
		return nil
	}
	if tty {
		reader := bufio.NewReader(limited)
		data := make([]byte, 32<<10)
		for {
			n, err := reader.Read(data)
			if n > 0 {
				if e := consume(1, data[:n]); e != nil {
					return nil, e
				}
			}
			if err == io.EOF {
				break
			}
			if err != nil {
				if limited.N <= 0 {
					return nil, ErrLogLimit
				}
				return nil, ErrLogsUnavailable
			}
		}
	} else {
		var header [8]byte
		for {
			_, err := io.ReadFull(limited, header[:])
			if err == io.EOF {
				break
			}
			if err != nil {
				if limited.N <= 0 {
					return nil, ErrLogLimit
				}
				return nil, ErrLogsUnavailable
			}
			size := binary.BigEndian.Uint32(header[4:])
			if size > maxLogBytes {
				return nil, ErrLogLimit
			}
			if (header[0] != 1 && header[0] != 2) || header[1] != 0 || header[2] != 0 || header[3] != 0 {
				return nil, ErrLogsUnavailable
			}
			// Stream frames can split lines anywhere, even inside their timestamps.
			remaining := int64(size)
			data := make([]byte, 32<<10)
			firstChunk := true
			for remaining > 0 {
				n := int64(len(data))
				if remaining < n {
					n = remaining
				}
				if _, err := io.ReadFull(limited, data[:n]); err != nil {
					if limited.N <= 0 {
						return nil, ErrLogLimit
					}
					return nil, ErrLogsUnavailable
				}
				chunk := data[:n]
				// Hold a possible next header separately until enough frames
				// arrive to decide. This also works when its timestamp is split.
				if len(candidates[header[0]]) > 0 || (firstChunk && len(pending[header[0]]) > 0) {
					chunk = append(candidates[header[0]], chunk...)
					candidates[header[0]] = nil
					complete, possible := logTimestampPrefix(chunk)
					pendingHeader, _ := logTimestampPrefix(pending[header[0]])
					if pendingHeader && complete {
						if err := emit(header[0], pending[header[0]]); err != nil {
							return nil, err
						}
						pending[header[0]] = nil
					} else if pendingHeader && possible {
						candidates[header[0]] = append([]byte(nil), chunk...)
						firstChunk = false
						remaining -= n
						continue
					}
				}
				firstChunk = false
				if err := consume(header[0], chunk); err != nil {
					return nil, err
				}
				remaining -= n
			}
		}
	}
	if limited.N <= 0 {
		return nil, ErrLogLimit
	}
	for _, stream := range []byte{1, 2} {
		if len(candidates[stream]) > 0 {
			return nil, ErrLogsUnavailable
		}
		if len(pending[stream]) > 0 {
			if err := emit(stream, pending[stream]); err != nil {
				return nil, err
			}
		}
	}
	return rows, nil
}
