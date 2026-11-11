package dashboard

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"services/internal/groups"
)

func TestResolutionRoutesAdminSessionCanSnoozeAndReadMissingStatus(t *testing.T) {
	s, store := securedServer(t)
	_, m, _ := resolutionServer(t)
	s.source = m
	if _, err := store.Bootstrap("admin", "a sufficiently long password"); err != nil {
		t.Fatal(err)
	}
	_, session, err := store.Login("admin", "a sufficiently long password")
	if err != nil {
		t.Fatal(err)
	}
	cookie := &http.Cookie{Name: sessionCookieName, Value: session}
	var input any
	if err := json.Unmarshal([]byte(resolutionBody(t, m, "snooze")), &input); err != nil {
		t.Fatal(err)
	}
	w := authRequest(s, "POST", "/api/monitor/resolve", input, cookie, "")
	if w.Code != 200 {
		t.Fatalf("session resolution=%d %s", w.Code, w.Body.String())
	}
	w = authRequest(s, "GET", "/api/monitor", nil, cookie, "")
	var snapshot map[string]any
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &snapshot) != nil {
		t.Fatalf("monitor read=%d %s", w.Code, w.Body.String())
	}
	incidents, ok := snapshot["incidents"].([]any)
	if !ok || len(incidents) != 1 {
		t.Fatalf("snoozed incident lost: %s", w.Body.String())
	}
	i := incidents[0].(map[string]any)
	if i["status"] != "missing" || i["snoozedUntil"] == nil {
		t.Fatalf("invalid snoozed incident: %v", i)
	}
	w = authRequest(s, "GET", "/api/projects", nil, cookie, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"monitorSnoozedUntil"`) || !strings.Contains(w.Body.String(), `"monitorStatus":"missing"`) {
		t.Fatalf("snoozed inventory read=%d %s", w.Code, w.Body.String())
	}
}

func TestResolutionRoutesRejectReturnedIdentityAndAtomicInvalidBatch(t *testing.T) {
	s, m, source := resolutionServer(t)
	valid := resolutionBody(t, m, "forget")
	var body struct {
		Decisions []map[string]any `json:"decisions"`
	}
	if err := json.Unmarshal([]byte(valid), &body); err != nil {
		t.Fatal(err)
	}
	body.Decisions = append(body.Decisions, map[string]any{"incidentId": "unknown", "since": body.Decisions[0]["since"], "action": "forget"})
	batch, _ := json.Marshal(body)
	w := resolutionRequest(s, "/api/monitor/resolve", string(batch), "http://localhost")
	if w.Code != 409 || len(m.Snapshot().Incidents) != 1 {
		t.Fatalf("partial invalid batch applied: %d %s", w.Code, w.Body.String())
	}
	source.containers = append(source.containers, groups.Container{ID: "returned-id", Project: "agent", Service: "everyoneagent", Name: "everyoneagent", State: "running"})
	w = resolutionRequest(s, "/api/monitor/resolve", valid, "http://localhost")
	if w.Code != 409 {
		t.Fatalf("cached incident overrode live returned service: %d %s", w.Code, w.Body.String())
	}
}
