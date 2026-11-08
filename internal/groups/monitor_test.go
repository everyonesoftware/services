package groups

import (
	"encoding/json"
	"testing"
)

func TestMonitoringHealthIntegration(t *testing.T) {
	for _, tc := range []struct {
		c    Container
		want Status
	}{
		{Container{State: "running", MonitorStatus: "restart-loop"}, StatusUnhealthy},
		{Container{State: "exited", OneShot: true, ExitCode: 0}, StatusRunning},
		{Container{State: "exited", OneShot: true, ExitCode: 1}, StatusUnhealthy},
	} {
		if got := Aggregate([]Container{withProject(tc.c)})[0].Status; got != tc.want {
			t.Errorf("%+v: got %s want %s", tc.c, got, tc.want)
		}
	}
}
func withProject(c Container) Container { c.Project = "app"; return c }
func TestUpdateCheckFieldsContract(t *testing.T) {
	var c Container
	if err := json.Unmarshal([]byte(`{"updateStatus":"error","updateError":"registry unavailable","updateCheckedAt":"2026-11-07T12:00:00Z"}`), &c); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(c)
	var got map[string]any
	json.Unmarshal(data, &got)
	for _, key := range []string{"updateStatus", "updateError", "updateCheckedAt"} {
		if got[key] == nil {
			t.Errorf("missing %s", key)
		}
	}
}
