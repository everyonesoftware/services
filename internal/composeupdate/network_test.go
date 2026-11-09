package composeupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"services/internal/groups"
	"strings"
	"testing"
)

type networkRunner struct {
	recordingRunner
	config      string
	events      *[]string
	failService string
	replaced    map[string]bool
	psOutput    func(string) ([]byte, error)
	psCalls     [][]string
}

func (r *networkRunner) Output(_ context.Context, _ string, args []string) ([]byte, error) {
	for _, arg := range args {
		if arg == "ps" {
			r.psCalls = append(r.psCalls, append([]string{}, args...))
			name := args[len(args)-1]
			if r.psOutput != nil {
				return r.psOutput(name)
			}
			id := "old-" + name
			if r.replaced[name] {
				id = "new-" + name
			}
			return json.Marshal(map[string]string{"ID": id, "Project": "shop", "Service": name})
		}
	}
	return []byte(r.config), nil
}
func (r *networkRunner) Run(ctx context.Context, dir string, args []string) error {
	command := ""
	for _, a := range args {
		if a == "pull" || a == "stop" || a == "up" || a == "create" {
			command = a
			break
		}
	}
	name := args[len(args)-1]
	*r.events = append(*r.events, command+":"+name)
	if command == "up" && name == r.failService {
		return fmt.Errorf("provider failure")
	}
	if command == "up" {
		r.replaced[name] = true
	}
	return r.recordingRunner.Run(ctx, dir, args)
}
func networkFixture(t *testing.T) (*Updater, []groups.Container, *networkRunner, context.Context, *[]string, *UpdatePlan) {
	t.Helper()
	events := []string{}
	r := &networkRunner{events: &events, replaced: map[string]bool{}}
	u, c, _ := testUpdater(t, &r.recordingRunner)
	u.runner = r
	members := []groups.Container{}
	for _, n := range []string{"vpn", "services", "child", "unrelated"} {
		x := c
		x.Service = n
		x.ID = "old-" + n
		x.Name = n
		x.Image = n + ":latest"
		x.State = "running"
		x.UpdateAvailable = n == "vpn"
		members = append(members, x)
	}
	config := map[string]any{"services": map[string]any{"vpn": map[string]string{"image": "vpn:latest"}, "services": map[string]string{"image": "services:latest", "network_mode": "service:vpn"}, "child": map[string]string{"image": "child:latest", "network_mode": "service:services"}, "unrelated": map[string]string{"image": "unrelated:latest"}}}
	b, _ := json.Marshal(config)
	r.config = string(b)
	plan := &UpdatePlan{}
	ctx := WithNetworkRuntime(context.Background(), NetworkRuntime{Inspect: func(_ context.Context, ref string) (NetworkContainer, error) {
		n := strings.TrimPrefix(strings.TrimPrefix(ref, "old-"), "new-")
		id := ref
		mode := "bridge"
		if ref == n {
			id = "old-" + n
			if r.replaced[n] {
				id = "new-" + n
			}
		}
		provider := ""
		if n == "services" {
			provider = "vpn"
		}
		if n == "child" {
			provider = "services"
		}
		if provider != "" {
			target := "old-" + provider
			if strings.HasPrefix(id, "new-") && r.replaced[provider] {
				target = "new-" + provider
			}
			mode = "container:" + target
		}
		running := true
		for _, m := range members {
			if m.Service == n {
				running = m.State == "running"
			}
		}
		return NetworkContainer{ID: id, Image: "sha256:" + n, Name: n, Project: "shop", Service: n, Running: running, NetworkMode: mode}, nil
	}, Persist: func(p UpdatePlan) error { *plan = p; events = append(events, "persist"); return nil }})
	return u, members, r, ctx, &events, plan
}
func TestNetworkProviderReattachesTransitiveDependents(t *testing.T) {
	u, m, r, ctx, events, plan := networkFixture(t)
	err := u.UpdateProjectVerified(ctx, m, func(ref string) (string, error) { return "sha256:" + strings.TrimSuffix(ref, ":latest"), nil }, func(c groups.Container, _ string) error { *events = append(*events, "verify:"+c.Service); return nil })
	if err != nil {
		t.Fatal(err)
	}
	want := "pull:vpn,persist,stop:child,stop:services,up:vpn,verify:vpn,up:services,verify:services,up:child,verify:child"
	if got := strings.Join(*events, ","); got != want {
		t.Fatalf("events %s want %s", got, want)
	}
	if len(plan.Services) != 3 {
		t.Fatalf("plan %+v", plan)
	}
	if plan.Services[1].ProviderOldID != "old-vpn" || plan.Services[2].ProviderOldID != "old-services" {
		t.Fatalf("missing dependency identities: %+v", plan)
	}
	for _, call := range r.calls {
		if strings.Contains(strings.Join(call.args, " "), " up ") && !strings.Contains(strings.Join(call.args, " "), "--force-recreate") {
			t.Fatal("missing forced recreation")
		}
	}
}
