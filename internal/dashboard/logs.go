package dashboard

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"services/internal/dockerapi"
	"services/internal/groups"
)

type LogSource interface {
	Logs(context.Context, string, dockerapi.LogOptions) ([]dockerapi.LogRecord, error)
}

const maxLogCursorBytes = 32768

type LogEntry struct {
	ID          string    `json:"id"`
	Timestamp   time.Time `json:"timestamp"`
	ContainerID string    `json:"containerId"`
	Container   string    `json:"container"`
	Stream      string    `json:"stream"`
	Message     string    `json:"message"`
	ordinal     int
	anchor      string
}
type LogWarning struct {
	ContainerID string `json:"containerId"`
	Container   string `json:"container"`
	Message     string `json:"message"`
}
type LogsResponse struct {
	Entries    []LogEntry   `json:"entries"`
	HasMore    bool         `json:"hasMore"`
	NextCursor string       `json:"nextCursor,omitempty"`
	NextAfter  string       `json:"nextAfter,omitempty"`
	HasNewer   bool         `json:"hasNewer,omitempty"`
	Warnings   []LogWarning `json:"warnings,omitempty"`
}
type logPosition struct {
	Timestamp   time.Time
	ContainerID string
	Ordinal     int
	Anchor      string `json:",omitempty"`
}
type logCursor struct {
	Project, Container string
	Position           logPosition
	Positions          map[string]logPosition `json:",omitempty"`
}

type logBackend struct {
	LogSource
	secret   [32]byte
	requests chan struct{}
}

// SetLogSource configures the live Engine client before serving requests. A new
// key deliberately invalidates cursors across restarts or source changes.
func (s *Server) SetLogSource(source LogSource) {
	if source == nil {
		s.logSource = nil
		return
	}
	backend := &logBackend{LogSource: source, requests: make(chan struct{}, 4)}
	if _, err := rand.Read(backend.secret[:]); err != nil {
		panic("generate log cursor key")
	}
	s.logSource = backend
}
func logLess(a, b LogEntry) bool {
	if !a.Timestamp.Equal(b.Timestamp) {
		return a.Timestamp.Before(b.Timestamp)
	}
	if a.ContainerID != b.ContainerID {
		return a.ContainerID < b.ContainerID
	}
	return a.ordinal < b.ordinal
}
func entryPosition(e LogEntry) logPosition {
	return logPosition{Timestamp: e.Timestamp, ContainerID: e.ContainerID, Ordinal: e.ordinal, Anchor: e.anchor}
}
func (s *Server) encodeLogCursor(project, container string, pos logPosition) string {
	return s.signLogCursor(logCursor{Project: project, Container: container, Position: pos})
}
func (s *Server) signLogCursor(cursor logCursor) string {
	b, _ := json.Marshal(cursor)
	mac := hmac.New(sha256.New, s.logSource.secret[:])
	mac.Write(b)
	return base64.RawURLEncoding.EncodeToString(b) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func (s *Server) decodeLogCursor(token, project, container string) (logCursor, error) {
	var cursor logCursor
	invalid := errors.New("invalid log cursor")
	if s.logSource == nil || len(token) > maxLogCursorBytes {
		return cursor, invalid
	}
	payload, signature, ok := strings.Cut(token, ".")
	if !ok {
		return cursor, invalid
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(payload)
	if err != nil {
		return cursor, invalid
	}
	sig, err := base64.RawURLEncoding.Strict().DecodeString(signature)
	if err != nil {
		return cursor, invalid
	}
	mac := hmac.New(sha256.New, s.logSource.secret[:])
	mac.Write(data)
	if !hmac.Equal(sig, mac.Sum(nil)) || json.Unmarshal(data, &cursor) != nil || cursor.Project != project || cursor.Container != container || cursor.Position.Timestamp.IsZero() || cursor.Position.Ordinal < -1 {
		return cursor, invalid
	}
	return cursor, nil
}

func entriesForLogs(c groups.Container, rows []dockerapi.LogRecord) []LogEntry {
	entries := make([]LogEntry, 0, len(rows))
	counts := map[string]int{}
	prefixes := map[string][sha256.Size]byte{}
	for _, row := range rows {
		key := row.Timestamp.UTC().Format(time.RFC3339Nano)
		ordinal := counts[key]
		counts[key]++
		// Bind each ordinal to its complete retained timestamp prefix. Rotation
		// must not silently reuse an already-consumed position for other text.
		previous := prefixes[key]
		h := sha256.New()
		h.Write(previous[:])
		h.Write([]byte(row.Stream))
		h.Write([]byte{0})
		h.Write([]byte(row.Message))
		var sum [sha256.Size]byte
		copy(sum[:], h.Sum(nil))
		prefixes[key] = sum
		anchor := base64.RawURLEncoding.EncodeToString(sum[:])
		entries = append(entries, LogEntry{ID: c.ID + ":" + key + ":" + strconv.Itoa(ordinal) + ":" + anchor, Timestamp: row.Timestamp, ContainerID: c.ID, Container: c.Name, Stream: row.Stream, Message: row.Message, ordinal: ordinal, anchor: anchor})
	}
	return entries
}

var errLogHistoryChanged = errors.New("log history changed; reload recent logs")

func validLogAnchor(entries []LogEntry, position logPosition) bool {
	if position.Ordinal < 0 || position.Anchor == "" {
		return true
	}
	for _, entry := range entries {
		if entry.ContainerID == position.ContainerID && entry.Timestamp.Equal(position.Timestamp) && entry.ordinal == position.Ordinal {
			return entry.anchor == position.Anchor
		}
	}
	return false
}
func positionEntry(p logPosition) LogEntry {
	return LogEntry{Timestamp: p.Timestamp, ContainerID: p.ContainerID, ordinal: p.Ordinal}
}

// Replay the oldest sampled timestamp without tail to recover its absolute
// ordinal. This preserves identical lines even when a tail cuts a timestamp tie.
func (s *Server) recentLogEntries(ctx context.Context, c groups.Container, limit int) ([]LogEntry, error) {
	sample, err := s.logSource.Logs(ctx, c.ID, dockerapi.LogOptions{Tail: limit + 1})
	if err != nil {
		return nil, err
	}
	if len(sample) == 0 {
		return nil, nil
	}
	earliest, latest := sample[0].Timestamp, sample[0].Timestamp
	for _, row := range sample {
		if row.Timestamp.Before(earliest) {
			earliest = row.Timestamp
		}
		if row.Timestamp.After(latest) {
			latest = row.Timestamp
		}
	}
	rows, err := s.logSource.Logs(ctx, c.ID, dockerapi.LogOptions{Since: earliest, Until: latest.Add(time.Nanosecond)})
	if err != nil {
		return nil, err
	}
	return entriesForLogs(c, rows), nil
}

// Docker's json-file driver applies tail before until. Historical reads must
// therefore use tail=all with a bounded timestamp window, not tail=N&until=T.
// Narrow capped windows; widen empty ones. A narrowed window is never reported
// as exhausted, and an unpageable timestamp tie fails instead of skipping it.
func (s *Server) olderLogEntries(ctx context.Context, c groups.Container, before logPosition) ([]LogEntry, time.Time, error) {
	epoch := time.Unix(0, 0).UTC()
	upper := before.Timestamp.Add(time.Nanosecond)
	lower := upper.Add(-time.Hour)
	if lower.Before(epoch) {
		lower = epoch
	}
	var capped time.Time
	empty := upper
	for attempt := 0; attempt < 64; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, time.Time{}, err
		}
		records, err := s.logSource.Logs(ctx, c.ID, dockerapi.LogOptions{Since: lower, Until: upper})
		if errors.Is(err, dockerapi.ErrLogLimit) {
			capped = lower
			next := lower.Add(empty.Sub(lower) / 2)
			if !next.After(lower) {
				return nil, time.Time{}, err
			}
			lower = next
			continue
		}
		if err != nil {
			return nil, time.Time{}, err
		}
		entries := entriesForLogs(c, records)
		if c.ID == before.ContainerID && !validLogAnchor(entries, before) {
			return nil, time.Time{}, errLogHistoryChanged
		}
		filtered := entries[:0]
		for _, e := range entries {
			if logLess(e, positionEntry(before)) {
				filtered = append(filtered, e)
			}
		}
		if len(filtered) > 0 || lower.Equal(epoch) {
			if lower.Equal(epoch) {
				return filtered, time.Time{}, nil
			}
			return filtered, lower, nil
		}
		if !capped.IsZero() {
			empty = lower
			next := capped.Add(empty.Sub(capped) / 2)
			if !next.After(capped) {
				return nil, time.Time{}, dockerapi.ErrLogLimit
			}
			lower = next
		} else {
			lower = upper.Add(-2 * upper.Sub(lower))
			if lower.Before(epoch) {
				lower = epoch
			}
		}
	}
	return nil, time.Time{}, dockerapi.ErrLogLimit
}

// Read the oldest unread window, shrinking time bounds on a capped Engine
// response. Never replace an unread backlog with tail=N (which loses records).
func (s *Server) afterLogEntries(ctx context.Context, c groups.Container, after logPosition) ([]LogEntry, time.Time, error) {
	var upper time.Time
	for attempt := 0; attempt < 24; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, upper, err
		}
		records, err := s.logSource.Logs(ctx, c.ID, dockerapi.LogOptions{Since: after.Timestamp, Until: upper})
		if errors.Is(err, dockerapi.ErrLogLimit) {
			if upper.IsZero() {
				// Discover the Engine's clock boundary only after a cap. A host
				// wall-clock until bound can hide output from a clock-ahead daemon.
				latest, e := s.logSource.Logs(ctx, c.ID, dockerapi.LogOptions{Tail: 1})
				if e != nil {
					return nil, upper, e
				}
				if len(latest) == 0 {
					return nil, upper, dockerapi.ErrLogsUnavailable
				}
				upper = latest[len(latest)-1].Timestamp.Add(time.Nanosecond)
				continue
			}
			span := upper.Sub(after.Timestamp)
			if span <= time.Nanosecond {
				return nil, upper, err
			}
			upper = after.Timestamp.Add(span / 2)
			continue
		}
		if err != nil {
			return nil, upper, err
		}
		entries := entriesForLogs(c, records)
		if !validLogAnchor(entries, after) {
			return nil, upper, errLogHistoryChanged
		}
		filtered := entries[:0]
		for _, e := range entries {
			if logLess(positionEntry(after), e) {
				filtered = append(filtered, e)
			}
		}
		return filtered, upper, nil
	}
	return nil, upper, dockerapi.ErrLogLimit
}

func (s *Server) logs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	q := r.URL.Query()
	for _, key := range []string{"project", "container", "limit", "cursor", "after"} {
		if len(q[key]) > 1 {
			http.Error(w, "Duplicate log parameter", 400)
			return
		}
	}
	if q.Has("cursor") && q.Has("after") {
		http.Error(w, "cursor and after are mutually exclusive", 400)
		return
	}
	if (q.Has("cursor") && q.Get("cursor") == "") || (q.Has("after") && q.Get("after") == "") {
		http.Error(w, "Invalid empty log cursor", 400)
		return
	}
	project, container := q.Get("project"), q.Get("container")
	if project == "" {
		http.Error(w, "Project is required", 400)
		return
	}
	limit := 200
	if q.Get("limit") != "" {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > 500 {
			http.Error(w, "Invalid log limit", 400)
			return
		}
		limit = n
	}
	var before, after *logPosition
	checkpoints := make(map[string]logPosition)
	token := q.Get("cursor")
	if q.Get("after") != "" {
		token = q.Get("after")
	}
	if token != "" {
		cursor, err := s.decodeLogCursor(token, project, container)
		if err != nil || (q.Has("after") && cursor.Positions == nil) || (q.Has("cursor") && cursor.Positions != nil) {
			http.Error(w, "Invalid log cursor", 400)
			return
		}
		if q.Get("after") != "" {
			after = &cursor.Position
			for id, pos := range cursor.Positions {
				checkpoints[id] = pos
			}
		} else {
			before = &cursor.Position
		}
	}
	if s.logSource == nil {
		http.Error(w, "Container logs are not configured", 503)
		return
	}
	select {
	case s.logSource.requests <- struct{}{}:
		defer func() { <-s.logSource.requests }()
	default:
		w.Header().Set("Retry-After", "1")
		http.Error(w, "Too many concurrent log reads", 429)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	source := s.actionSource
	if source == nil {
		source = s.source
	}
	var containers []groups.Container
	var err error
	if contextual, ok := source.(interface {
		ContainersContext(context.Context) ([]groups.Container, error)
	}); ok {
		containers, err = contextual.ContainersContext(ctx)
	} else if source != nil {
		containers, err = source.Containers()
	} else {
		http.Error(w, "Docker engine unavailable", 503)
		return
	}
	if err != nil {
		http.Error(w, "Docker engine unavailable", 503)
		return
	}
	var targets []groups.Container
	for _, c := range containers {
		matchesProject := c.Project == project
		if project == "Standalone" {
			matchesProject = c.Project == ""
		}
		if matchesProject && (container == "" || container == c.ID) {
			targets = append(targets, c)
		}
	}
	if len(targets) == 0 {
		http.Error(w, "Project or container not found", 404)
		return
	}
	if len(targets) > 64 {
		http.Error(w, "Project has too many containers for a bounded log read", 503)
		return
	}
	currentIDs := make(map[string]bool, len(targets))
	for _, target := range targets {
		currentIDs[target.ID] = true
	}
	for id := range checkpoints {
		if !currentIDs[id] {
			delete(checkpoints, id)
		}
	}
	out := LogsResponse{Entries: make([]LogEntry, 0)}
	// No consumed source record is represented by the Engine epoch, never
	// by this host's wall clock (which may be ahead of the Docker daemon).
	start := time.Unix(0, 0).UTC()
	var frontier, historyFrontier time.Time
	for _, c := range targets {
		var rows []LogEntry
		var e error
		if after != nil {
			var end time.Time
			checkpoint, ok := checkpoints[c.ID]
			if !ok {
				checkpoint = logPosition{Timestamp: start, ContainerID: c.ID, Ordinal: -1}
				checkpoints[c.ID] = checkpoint
			}
			rows, end, e = s.afterLogEntries(ctx, c, checkpoint)
			if !end.IsZero() && (frontier.IsZero() || end.Before(frontier)) {
				frontier = end
			}
		} else if before != nil {
			var start time.Time
			rows, start, e = s.olderLogEntries(ctx, c, *before)
			if !start.IsZero() && (historyFrontier.IsZero() || start.After(historyFrontier)) {
				historyFrontier = start
			}
		} else {
			rows, e = s.recentLogEntries(ctx, c, limit)
		}
		if e != nil {
			if errors.Is(e, errLogHistoryChanged) {
				http.Error(w, "Log history changed; Jump to latest to reload recent logs", http.StatusConflict)
				return
			}
			message := "Container logs unavailable (container removed, logging driver unsupported, or Engine read failed)"
			if errors.Is(e, dockerapi.ErrLogLimit) {
				message = "Container log window exceeded safety limits; no cursor was advanced"
			}
			if errors.Is(e, dockerapi.ErrAmbiguousTTYLogs) {
				message = "Raw TTY partial record boundaries are ambiguous; logs cannot be separated safely. Non-TTY containers preserve log record boundaries"
			}
			out.Warnings = append(out.Warnings, LogWarning{ContainerID: c.ID, Container: c.Name, Message: message})
			continue
		}
		out.Entries = append(out.Entries, rows...)
		// Retain only the candidates needed by this page while merging. Keeping
		// whole per-container windows would multiply the Engine byte budget by
		// the project size. Clone to release discarded message references.
		sort.SliceStable(out.Entries, func(i, j int) bool { return logLess(out.Entries[i], out.Entries[j]) })
		if len(out.Entries) > limit+1 {
			if after != nil {
				out.Entries = append([]LogEntry(nil), out.Entries[:limit+1]...)
			} else {
				out.Entries = append([]LogEntry(nil), out.Entries[len(out.Entries)-limit-1:]...)
			}
		}
		if after == nil {
			for _, e := range rows {
				p, ok := checkpoints[c.ID]
				if !ok || logLess(positionEntry(p), e) {
					checkpoints[c.ID] = entryPosition(e)
				}
			}
		}
	}
	if len(out.Warnings) == len(targets) {
		if len(out.Warnings) == 1 {
			http.Error(w, out.Warnings[0].Message, http.StatusServiceUnavailable)
			return
		}
		http.Error(w, "Container logs unavailable or safety limit exceeded; retry or select a container", 503)
		return
	}
	if err := ctx.Err(); err != nil {
		http.Error(w, "Container log read canceled or timed out", 503)
		return
	}
	if !historyFrontier.IsZero() {
		out.HasMore = true
		filtered := out.Entries[:0]
		for _, e := range out.Entries {
			if !e.Timestamp.Before(historyFrontier) {
				filtered = append(filtered, e)
			}
		}
		out.Entries = filtered
	}
	if after != nil && !frontier.IsZero() {
		out.HasNewer = true
		filtered := out.Entries[:0]
		for _, e := range out.Entries {
			if e.Timestamp.Before(frontier) {
				filtered = append(filtered, e)
			}
		}
		out.Entries = filtered
	}
	sort.SliceStable(out.Entries, func(i, j int) bool { return logLess(out.Entries[i], out.Entries[j]) })
	if len(out.Entries) > limit {
		if after != nil {
			out.HasNewer = true
			out.Entries = out.Entries[:limit]
		} else {
			out.HasMore = true
			out.Entries = out.Entries[len(out.Entries)-limit:]
		}
	}
	if after != nil {
		out.NextAfter = s.signLogCursor(logCursor{Project: project, Container: container, Position: *after, Positions: checkpoints})
		if len(out.Entries) > 0 {
			for _, e := range out.Entries {
				checkpoints[e.ContainerID] = entryPosition(e)
			}
			out.NextAfter = s.signLogCursor(logCursor{Project: project, Container: container, Position: entryPosition(out.Entries[len(out.Entries)-1]), Positions: checkpoints})
		} else if !frontier.IsZero() {
			for id, p := range checkpoints {
				if p.Timestamp.Before(frontier) {
					checkpoints[id] = logPosition{Timestamp: frontier, ContainerID: id, Ordinal: -1}
				}
			}
			out.NextAfter = s.signLogCursor(logCursor{Project: project, Container: container, Position: logPosition{Timestamp: frontier, Ordinal: -1}, Positions: checkpoints})
		}
	} else if len(out.Entries) > 0 {
		last := entryPosition(out.Entries[len(out.Entries)-1])
		for _, c := range targets {
			if _, ok := checkpoints[c.ID]; !ok {
				checkpoints[c.ID] = logPosition{Timestamp: start, ContainerID: c.ID, Ordinal: -1}
			}
		}
		out.NextAfter = s.signLogCursor(logCursor{Project: project, Container: container, Position: last, Positions: checkpoints})
	}
	if after == nil && len(out.Entries) == 0 {
		for _, c := range targets {
			checkpoints[c.ID] = logPosition{Timestamp: start, ContainerID: c.ID, Ordinal: -1}
		}
		out.NextAfter = s.signLogCursor(logCursor{Project: project, Container: container, Position: logPosition{Timestamp: start, Ordinal: -1}, Positions: checkpoints})
	}
	if len(out.Entries) > 0 {
		out.NextCursor = s.encodeLogCursor(project, container, entryPosition(out.Entries[0]))
	}
	// A partial project read is useful, but cannot establish a safe cross-source
	// boundary. Retry the same cursor; stable entry IDs allow clients to dedupe.
	if len(out.Warnings) > 0 {
		out.NextCursor = ""
		out.NextAfter = ""
		out.HasMore = true
		if after != nil {
			out.NextAfter = token
			out.HasNewer = true
		}
	}
	if len(out.NextCursor) > maxLogCursorBytes || len(out.NextAfter) > maxLogCursorBytes {
		http.Error(w, "Log scope exceeds cursor safety limits; select a container", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(out)
}
