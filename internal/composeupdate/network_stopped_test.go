package composeupdate

import (
	"context"
	"encoding/json"
	"services/internal/groups"
	"strings"
	"testing"
)

func TestStoppedConsumerMayRetainRetiredNamespaceWithoutBlockingLaterUpdates(t *testing.T) {
	u, m, r, ctx, _, plan := networkFixture(t)
	m[2].State = "exited"
	runtime := ctx.Value(networkRuntimeKey{}).(NetworkRuntime)
	inspect := runtime.Inspect
	runtime.Inspect = func(ctx context.Context, ref string) (NetworkContainer, error) {
		c, e := inspect(ctx, ref)
		if ref == "old-child" {
			c.NetworkMode = "container:retired-services"
		}
		return c, e
	}
	ctx = WithNetworkRuntime(ctx, runtime)
	if err := u.UpdateProjectVerified(ctx, m, localImage, func(groups.Container, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if r.replaced["child"] {
		t.Fatal("stopped consumer recreated")
	}
	for _, p := range plan.Services {
		if p.Service == "child" && p.Reason != "preserve stopped" {
			t.Fatalf("misleading plan: %+v", p)
		}
	}
}

func TestIndependentNamespaceProvidersAreOrderedAndDeduplicated(t *testing.T) {
	u, m, r, ctx, events, plan := networkFixture(t)
	second := m[0]
	second.Service = "second"
	second.Name = "second"
	second.ID = "old-second"
	second.Image = "second:latest"
	m = append(m, second)
	var cfg struct {
		Services map[string]map[string]string `json:"services"`
	}
	if err := json.Unmarshal([]byte(r.config), &cfg); err != nil {
		t.Fatal(err)
	}
	cfg.Services["second"] = map[string]string{"image": "second:latest"}
	cfg.Services["unrelated"]["network_mode"] = "service:second"
	b, _ := json.Marshal(cfg)
	r.config = string(b)
	runtime := ctx.Value(networkRuntimeKey{}).(NetworkRuntime)
	inspect := runtime.Inspect
	runtime.Inspect = func(ctx context.Context, ref string) (NetworkContainer, error) {
		c, e := inspect(ctx, ref)
		if c.Service == "unrelated" {
			c.NetworkMode = "container:old-second"
			if strings.HasPrefix(c.ID, "new-") {
				c.NetworkMode = "container:new-second"
			}
		}
		return c, e
	}
	ctx = WithNetworkRuntime(ctx, runtime)
	if err := u.UpdateProjectVerified(ctx, m, localImage, func(c groups.Container, _ string) error { *events = append(*events, "verify:"+c.Service); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(plan.Services) != 5 {
		t.Fatalf("plan %+v", plan)
	}
	got := strings.Join(*events, ",")
	if strings.Index(got, "verify:second") > strings.Index(got, "up:unrelated") || strings.Count(got, "up:second") != 1 {
		t.Fatal(got)
	}
}
