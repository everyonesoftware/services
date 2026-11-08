// Package monitor maintains an in-memory, polling-based Docker health snapshot.
// History is volatile and resets on process restart. Containers returns cached
// data (including the last good snapshot during outages); use the Docker source
// directly for actions which require a fresh view.
package monitor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"services/internal/groups"
)

type ContainerSource interface {
	Containers() ([]groups.Container, error)
}
type Notifier interface {
	Notify(context.Context, Event) error
}
type Incident struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Service string `json:"service"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Reason  string `json:"reason"`
	Since   string `json:"since"`
}
type Event struct {
	ID      string `json:"id"`
	Project string `json:"project"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Time    string `json:"time"`
}
type Snapshot struct {
	Connected              bool       `json:"connected"`
	LastChecked            string     `json:"lastChecked"`
	Error                  string     `json:"error"`
	Incidents              []Incident `json:"incidents"`
	Events                 []Event    `json:"events"`
	NotificationConfigured bool       `json:"notificationConfigured"`
}
type restartSample struct {
	at    time.Time
	count int
}
type Monitor struct {
	source       ContainerSource
	notifier     Notifier
	mu           sync.RWMutex
	pollMu       sync.Mutex
	containers   []groups.Container
	snapshot     Snapshot
	now          func() time.Time
	known        map[string]groups.Container
	sequence     uint64
	restarts     map[string][]restartSample
	lastObserved map[string]time.Time
	interval     time.Duration
	pending      []Event
	engineFailed bool
}

func New(source ContainerSource, notifier Notifier) *Monitor {
	return &Monitor{source: source, notifier: notifier, now: time.Now, known: map[string]groups.Container{}, restarts: map[string][]restartSample{}, lastObserved: map[string]time.Time{}, snapshot: Snapshot{Incidents: []Incident{}, Events: []Event{}, NotificationConfigured: notifier != nil}}
}

// Run performs an immediate poll, then refreshes every 15 seconds until canceled.
func (m *Monitor) Run(ctx context.Context) {
	interval := m.interval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		_ = m.Poll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (m *Monitor) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.snapshot
	s.Incidents = append([]Incident{}, s.Incidents...)
	s.Events = append([]Event{}, s.Events...)
	return s
}
func (m *Monitor) Containers() ([]groups.Container, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.containers == nil {
		return nil, errors.New("Docker snapshot not yet available")
	}
	return cloneContainers(m.containers), nil
}
func cloneContainers(in []groups.Container) []groups.Container {
	out := append([]groups.Container{}, in...)
	for i := range out {
		out[i].RepoDigests = append([]string(nil), in[i].RepoDigests...)
		out[i].ComposeConfigFiles = append([]string(nil), in[i].ComposeConfigFiles...)
	}
	return out
}
func (m *Monitor) Poll(ctx context.Context) error {
	m.pollMu.Lock()
	defer m.pollMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	pollCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var containers []groups.Container
	var err error
	if source, ok := m.source.(interface {
		ContainersContext(context.Context) ([]groups.Container, error)
	}); ok {
		containers, err = source.ContainersContext(pollCtx)
	} else {
		containers, err = m.source.Containers()
	}
	observedAt := m.now().UTC()
	now := observedAt.Format(time.RFC3339Nano)
	m.mu.Lock()
	defer func() { m.mu.Unlock(); m.deliver(ctx) }()
	m.snapshot.LastChecked = now
	m.snapshot.Connected = err == nil
	if err != nil {
		m.snapshot.Error = "Docker Engine unavailable; showing last successful snapshot"
		if !m.engineFailed {
			m.event(Incident{Project: "Docker Engine", Name: "Docker Engine"}, "problem", "Docker Engine unavailable", now)
		}
		m.engineFailed = true
		return errors.New("Docker Engine unavailable")
	}
	if m.engineFailed {
		m.event(Incident{Project: "Docker Engine", Name: "Docker Engine"}, "recovery", "Docker Engine connection restored", now)
	}
	m.engineFailed = false
	m.snapshot.Error = ""
	containers = cloneContainers(containers)
	old := make(map[string]Incident)
	for _, incident := range m.snapshot.Incidents {
		old[incident.ID] = incident
	}
	current := make(map[string]Incident)
	seen := make(map[string]bool)
	for i := range containers {
		c := &containers[i]
		key := identity(*c)
		seen[key] = true
		c.MonitorStatus, c.MonitorReason = classify(*c)
		previous, exists := m.known[key]
		samples := m.restarts[key]
		last := m.lastObserved[key]
		cutoff := observedAt.Add(-5 * time.Minute)
		if exists && last.After(cutoff) && !last.After(observedAt) {
			delta := 0
			if previous.ID == c.ID && c.RestartCount > previous.RestartCount {
				delta = c.RestartCount - previous.RestartCount
			}
			// A replacement's counter is a new baseline, not a delta. Other
			// lifecycle evidence contributes one transition only when the
			// counter has not already accounted for this observation.
			startedChanged := previous.StartedAt != "" && c.StartedAt != "" && previous.StartedAt != c.StartedAt
			if delta == 0 && (previous.ID != c.ID || startedChanged || c.RestartCount < previous.RestartCount) {
				delta = 1
			}
			if delta > 0 {
				// The change occurred somewhere in this observation interval.
				// Expire from its start, not the poll which discovered it.
				samples = append(samples, restartSample{last, delta})
			}
		} else {
			// No evidence can place changes across a long gap in this window.
			samples = nil
		}
		m.lastObserved[key] = observedAt
		kept := samples[:0]
		count := 0
		for _, sample := range samples {
			if sample.at.After(cutoff) {
				kept = append(kept, sample)
				count += sample.count
			}
		}
		m.restarts[key] = kept
		if count >= 3 && c.State == "running" && c.Health == "" {
			c.MonitorStatus = "restart-loop"
			c.MonitorReason = "At least 3 restarts observed within 5 minutes"
		}
		m.known[key] = *c
		if problem(c.MonitorStatus) {
			current[key] = Incident{ID: key, Project: project(*c), Service: c.Service, Name: c.Name, Status: c.MonitorStatus, Reason: c.MonitorReason, Since: now}
		}
	}
	for key, c := range m.known {
		if !seen[key] && !(c.OneShot && c.State == "exited" && c.ExitCode == 0 && !c.OOMKilled) {
			current[key] = Incident{ID: key, Project: project(c), Service: c.Service, Name: c.Name, Status: "missing", Reason: "Previously observed container disappeared", Since: now}
		}
	}
	keys := make([]string, 0, len(current))
	for key := range current {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	m.snapshot.Incidents = []Incident{}
	for _, key := range keys {
		incident := current[key]
		if previous, ok := old[key]; ok && previous.Status == incident.Status && previous.Reason == incident.Reason {
			incident.Since = previous.Since
		} else {
			m.event(incident, "problem", incident.Reason, now)
		}
		m.snapshot.Incidents = append(m.snapshot.Incidents, incident)
		if incident.Status == "missing" {
			missing := m.known[key]
			missing.MonitorStatus = "missing"
			missing.MonitorReason = incident.Reason
			missing.UpdateAvailable = false
			missing.ProposedDigest = ""
			missing.ComposeWorkingDir = ""
			missing.ComposeConfigFiles = nil
			missing.ComposeConfigFilesLabel = ""
			containers = append(containers, missing)
		}
	}
	keys = keys[:0]
	for key := range old {
		if _, ok := current[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		m.event(old[key], "recovery", "Container recovered", now)
	}
	m.containers = containers
	return nil
}

// identity deliberately ignores the replaceable Docker container ID.
func identity(c groups.Container) string { return project(c) + "/" + c.Service + "/" + c.Name }
func (m *Monitor) event(i Incident, kind, message, now string) {
	m.sequence++
	m.snapshot.Events = append(m.snapshot.Events, Event{ID: fmt.Sprintf("%d", m.sequence), Project: i.Project, Name: i.Name, Kind: kind, Message: message, Time: now})
	if m.notifier != nil {
		m.pending = append(m.pending, m.snapshot.Events[len(m.snapshot.Events)-1])
		if len(m.pending) > 100 {
			m.pending = append([]Event{}, m.pending[len(m.pending)-100:]...)
		}
	}
	if len(m.snapshot.Events) > 100 {
		m.snapshot.Events = append([]Event{}, m.snapshot.Events[len(m.snapshot.Events)-100:]...)
	}
}
func project(c groups.Container) string {
	if c.Project == "" {
		return "Standalone"
	}
	return c.Project
}
func problem(status string) bool {
	return status != "running" && status != "healthy" && status != "starting" && status != "completed"
}
func classify(c groups.Container) (string, string) {
	if c.State != "running" {
		if c.OOMKilled {
			return "oom-killed", "Container was killed due to memory exhaustion"
		}
		if c.OneShot && c.State == "exited" && c.ExitCode == 0 {
			return "completed", "One-off task completed successfully"
		}
		if c.State == "created" {
			return "starting", "Container has not started yet"
		}
		return "stopped", fmt.Sprintf("Container state: %s (exit code %d)", c.State, c.ExitCode)
	}
	switch c.Health {
	case "healthy":
		return "healthy", "Docker health check is passing"
	case "unhealthy":
		return "unhealthy", "Docker health check is failing"
	case "starting":
		return "starting", "Docker health check is starting"
	case "":
		return "running", "Running without a Docker health check"
	default:
		return "starting", "Docker health check status is unknown"
	}
}
