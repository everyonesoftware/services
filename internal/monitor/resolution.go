package monitor

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"services/internal/groups"
)

var (
	ErrResolutionConflict    = errors.New("monitor resolution conflicts with current state")
	ErrInvalidResolution     = errors.New("invalid monitor resolution")
	ErrResolutionUnavailable = errors.New("Docker Engine unavailable for monitor resolution")
)

// ResolutionDecision identifies the exact reviewed missing incident. SnoozedUntil
// is its expected prior snooze value (empty for an unsnoozed incident), so another
// tab cannot silently overwrite a newer decision. ReplacementID is a Docker ID.
type ResolutionDecision struct {
	IncidentID    string `json:"incidentId"`
	Since         string `json:"since"`
	Action        string `json:"action"`
	ReplacementID string `json:"replacementId,omitempty"`
	SnoozeSeconds int    `json:"snoozeSeconds,omitempty"`
	SnoozedUntil  string `json:"snoozedUntil,omitempty"`
}
type resolutionEntry struct {
	incident     Incident
	container    groups.Container
	revision     uint64
	action       string
	snoozedUntil string
	replacement  groups.Container
}
type resolutionUndo struct {
	expires time.Time
	entries []resolutionEntry
}

// freshContainers is called only while pollMu is held, never from a snapshot lock.
func (m *Monitor) freshContainers(ctx context.Context) ([]groups.Container, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var rows []groups.Container
	var err error
	if s, ok := m.source.(interface {
		ContainersContext(context.Context) ([]groups.Container, error)
	}); ok {
		rows, err = s.ContainersContext(ctx)
	} else if m.source != nil {
		rows, err = m.source.Containers()
	} else {
		err = ErrResolutionUnavailable
	}
	if err != nil {
		return nil, ErrResolutionUnavailable
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	return cloneContainers(rows), nil
}

// Resolve atomically applies a reviewed batch after a fresh, serialized engine
// read. It changes only volatile monitoring expectations, never Docker state.
// The returned opaque receipt permits one undo within five minutes.
func (m *Monitor) Resolve(ctx context.Context, decisions []ResolutionDecision) (string, error) {
	m.pollMu.Lock()
	defer m.pollMu.Unlock()
	if len(decisions) == 0 {
		return "", ErrInvalidResolution
	}
	seen, targets := map[string]bool{}, map[string]bool{}
	for _, d := range decisions {
		if d.IncidentID == "" || d.Since == "" || seen[d.IncidentID] {
			return "", ErrInvalidResolution
		}
		if _, err := time.Parse(time.RFC3339Nano, d.Since); err != nil {
			return "", ErrInvalidResolution
		}
		if d.SnoozedUntil != "" {
			if _, err := time.Parse(time.RFC3339Nano, d.SnoozedUntil); err != nil {
				return "", ErrInvalidResolution
			}
		}
		seen[d.IncidentID] = true
		switch d.Action {
		case "forget":
			if d.ReplacementID != "" || d.SnoozeSeconds != 0 {
				return "", ErrInvalidResolution
			}
		case "replace":
			if d.ReplacementID == "" || d.SnoozeSeconds != 0 || targets[d.ReplacementID] {
				return "", ErrInvalidResolution
			}
			targets[d.ReplacementID] = true
		case "snooze":
			if d.ReplacementID != "" || (d.SnoozeSeconds != 900 && d.SnoozeSeconds != 3600 && d.SnoozeSeconds != 86400) {
				return "", ErrInvalidResolution
			}
		default:
			return "", ErrInvalidResolution
		}
	}
	rows, err := m.freshContainers(ctx)
	if err != nil {
		return "", err
	}
	m.mu.Lock()
	defer func() { m.mu.Unlock(); m.deliver(ctx) }()
	incidents := map[string]Incident{}
	for _, i := range m.snapshot.Incidents {
		incidents[i.ID] = i
	}
	live := map[string]groups.Container{}
	present := map[string]bool{}
	for _, c := range rows {
		live[c.ID] = c
		present[identity(c)] = true
	}
	replacements := map[string]groups.Container{}
	for _, d := range decisions {
		i, ok := incidents[d.IncidentID]
		if !ok || i.Since != d.Since || i.Status != "missing" || present[i.ID] || i.SnoozedUntil != d.SnoozedUntil {
			return "", ErrResolutionConflict
		}
		c, ok := m.known[i.ID]
		if !ok {
			return "", ErrResolutionConflict
		}
		if d.Action == "replace" {
			target, ok := live[d.ReplacementID]
			if !ok {
				return "", ErrResolutionConflict
			}
			if target.ID == c.ID || identity(target) == i.ID || project(target) != i.Project {
				return "", ErrInvalidResolution
			}
			replacements[i.ID] = target
		}
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b[:])
	if m.revisions == nil {
		m.revisions = map[string]uint64{}
	}
	if m.resolutions == nil {
		m.resolutions = map[string]resolutionUndo{}
	}
	now := m.now().UTC()
	u := resolutionUndo{expires: now.Add(5 * time.Minute)}
	for _, d := range decisions {
		i := incidents[d.IncidentID]
		m.revisions[i.ID]++
		e := resolutionEntry{incident: i, container: cloneContainers([]groups.Container{m.known[i.ID]})[0], revision: m.revisions[i.ID], action: d.Action, replacement: replacements[i.ID]}
		message := fmt.Sprintf("Stopped monitoring missing service %s", i.Name)
		if d.Action == "snooze" {
			e.snoozedUntil = now.Add(time.Duration(d.SnoozeSeconds) * time.Second).Format(time.RFC3339Nano)
			m.setSnooze(i.ID, e.snoozedUntil)
			message = fmt.Sprintf("Snoozed missing service %s until %s", i.Name, e.snoozedUntil)
		} else {
			delete(m.known, i.ID)
			delete(m.restarts, i.ID)
			delete(m.lastObserved, i.ID)
			m.removeResolutionIncident(i.ID)
			if d.Action == "replace" {
				message = fmt.Sprintf("Replaced missing service %s with %s (%s); monitoring expectation removed", i.Name, e.replacement.Name, e.replacement.ID)
			}
		}
		m.discardProblemNotifications(i)
		u.entries = append(u.entries, e)
		m.event(i, "resolution", message, now.Format(time.RFC3339Nano))
	}
	for k, u := range m.resolutions {
		if !now.Before(u.expires) {
			delete(m.resolutions, k)
		}
	}
	if len(m.resolutions) >= 100 {
		var oldest string
		var at time.Time
		for k, u := range m.resolutions {
			if oldest == "" || u.expires.Before(at) {
				oldest = k
				at = u.expires
			}
		}
		delete(m.resolutions, oldest)
	}
	m.resolutions[token] = u
	return token, nil
}
func (m *Monitor) setSnooze(id, until string) {
	for j := range m.snapshot.Incidents {
		if m.snapshot.Incidents[j].ID == id {
			m.snapshot.Incidents[j].SnoozedUntil = until
		}
	}
	for j := range m.containers {
		if identity(m.containers[j]) == id {
			m.containers[j].MonitorSnoozedUntil = until
		}
	}
}
func (m *Monitor) discardProblemNotifications(i Incident) {
	kept := m.pending[:0]
	for _, e := range m.pending {
		if e.Kind != "problem" || e.incidentID != i.ID {
			kept = append(kept, e)
		}
	}
	m.pending = kept
}
func (m *Monitor) removeResolutionIncident(id string) {
	kept := m.snapshot.Incidents[:0]
	for _, i := range m.snapshot.Incidents {
		if i.ID != id {
			kept = append(kept, i)
		}
	}
	m.snapshot.Incidents = kept
	rows := m.containers[:0]
	for _, c := range m.containers {
		if identity(c) != id {
			rows = append(rows, c)
		}
	}
	m.containers = rows
}
func missingContainer(c groups.Container, i Incident) groups.Container {
	c.MonitorStatus = "missing"
	c.MonitorReason = i.Reason
	c.MonitorSnoozedUntil = i.SnoozedUntil
	c.UpdateAvailable = false
	c.ProposedDigest = ""
	c.ComposeWorkingDir = ""
	c.ComposeConfigFiles = nil
	c.ComposeConfigFilesLabel = ""
	return c
}

// UndoResolution restores only the identities changed by a valid receipt.
// Fresh inventory, exact incident versions, and per-identity revisions reject
// returned identities and newer manual choices without clobbering other services.
func (m *Monitor) UndoResolution(ctx context.Context, token string) error {
	m.pollMu.Lock()
	defer m.pollMu.Unlock()
	// Invalid receipts are conflicts regardless of engine availability. A valid
	// receipt is checked again after the engine read, which may cross its TTL.
	m.mu.RLock()
	receipt, exists := m.resolutions[token]
	valid := exists && m.now().Before(receipt.expires)
	m.mu.RUnlock()
	if !valid {
		return ErrResolutionConflict
	}
	rows, err := m.freshContainers(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer func() { m.mu.Unlock(); m.deliver(ctx) }()
	u, ok := m.resolutions[token]
	if !ok || !m.now().Before(u.expires) {
		return ErrResolutionConflict
	}
	incidents := map[string]Incident{}
	for _, i := range m.snapshot.Incidents {
		incidents[i.ID] = i
	}
	for _, e := range u.entries {
		if m.revisions[e.incident.ID] != e.revision {
			return ErrResolutionConflict
		}
		for _, c := range rows {
			if identity(c) == e.incident.ID {
				return ErrResolutionConflict
			}
		}
		if e.action == "snooze" {
			i, ok := incidents[e.incident.ID]
			if !ok || i.Status != "missing" || i.Since != e.incident.Since || i.SnoozedUntil != e.snoozedUntil {
				return ErrResolutionConflict
			}
		} else if _, ok := m.known[e.incident.ID]; ok {
			return ErrResolutionConflict
		}
		if e.action == "replace" {
			found := false
			for _, c := range rows {
				if c.ID == e.replacement.ID && identity(c) == identity(e.replacement) {
					found = true
				}
			}
			if !found {
				return ErrResolutionConflict
			}
		}
	}
	for _, e := range u.entries {
		m.revisions[e.incident.ID]++
		if e.action == "snooze" {
			m.setSnooze(e.incident.ID, e.incident.SnoozedUntil)
		} else {
			m.known[e.incident.ID] = cloneContainers([]groups.Container{e.container})[0]
			m.snapshot.Incidents = append(m.snapshot.Incidents, e.incident)
			m.containers = append(m.containers, missingContainer(cloneContainers([]groups.Container{e.container})[0], e.incident))
		}
		m.event(e.incident, "resolution", "Restored monitoring expectation", m.now().UTC().Format(time.RFC3339Nano))
	}
	sort.Slice(m.snapshot.Incidents, func(a, b int) bool { return m.snapshot.Incidents[a].ID < m.snapshot.Incidents[b].ID })
	delete(m.resolutions, token)
	return nil
}
