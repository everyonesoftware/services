package composeupdate

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"services/internal/groups"
)

func localImage(ref string) (string, error) {
	return "sha256:" + strings.TrimSuffix(ref, ":latest"), nil
}
func TestNetworkSelectionAndStoppedServices(t *testing.T) {
	for _, scenario := range []string{"services only", "project", "multiple providers", "stopped dependent", "stopped selected"} {
		t.Run(scenario, func(t *testing.T) {
			u, m, r, ctx, events, plan := networkFixture(t)
			switch scenario {
			case "services only":
				m[0].UpdateAvailable = false
				m[1].UpdateAvailable = true
			case "project":
				for i := range m {
					m[i].UpdateAvailable = true
				}
			case "multiple providers":
				m[1].UpdateAvailable = true
			case "stopped dependent":
				m[2].State = "exited"
			case "stopped selected":
				m[3].State = "exited"
				m[3].UpdateAvailable = true
			}
			if err := u.UpdateProjectVerified(ctx, m, localImage, func(c groups.Container, _ string) error { *events = append(*events, "verify:"+c.Service); return nil }); err != nil {
				t.Fatal(err)
			}
			got := strings.Join(*events, ",")
			if scenario == "services only" && (r.replaced["vpn"] || strings.Contains(got, "pull:vpn")) {
				t.Fatal(got)
			}
			for _, name := range []string{"vpn", "services", "child", "unrelated"} {
				count := 0
				for _, event := range *events {
					if event == "up:"+name {
						count++
					}
				}
				if count > 1 {
					t.Fatalf("duplicate recreation %s", got)
				}
			}
			if scenario == "project" && len(plan.Services) != 4 {
				t.Fatalf("project plan %+v", plan)
			}
			if scenario == "multiple providers" && strings.Contains(got, "pull:child") {
				t.Fatal(got)
			}
			if scenario == "stopped dependent" && r.replaced["child"] {
				t.Fatal(got)
			}
			if scenario == "stopped selected" && r.replaced["unrelated"] {
				t.Fatal(got)
			}
		})
	}
}

func TestNetworkPreflightRejectsUnsafeGraphsWithoutMutation(t *testing.T) {
	for _, scenario := range []string{"cycle", "external", "literal id", "missing provider", "ambiguous", "stale namespace", "config drift", "tag drift", "inspect failure", "no runtime", "persist failure"} {
		t.Run(scenario, func(t *testing.T) {
			u, m, r, ctx, _, _ := networkFixture(t)
			runtime := ctx.Value(networkRuntimeKey{}).(NetworkRuntime)
			inspect := runtime.Inspect
			image := localImage
			switch scenario {
			case "cycle":
				r.config = strings.Replace(r.config, `"image":"vpn:latest"`, `"image":"vpn:latest","network_mode":"service:child"`, 1)
				runtime.Inspect = func(ctx context.Context, ref string) (NetworkContainer, error) {
					c, e := inspect(ctx, ref)
					if ref == "old-vpn" {
						c.NetworkMode = "container:old-child"
					}
					return c, e
				}
			case "external":
				r.config = strings.Replace(r.config, "service:vpn", "container:foreign", 1)
			case "literal id":
				r.config = strings.Replace(r.config, "service:vpn", "container:old-vpn", 1)
			case "missing provider":
				r.config = strings.Replace(r.config, "service:vpn", "service:missing", 1)
			case "ambiguous":
				m = append(m, m[0])
			case "stale namespace":
				runtime.Inspect = func(ctx context.Context, ref string) (NetworkContainer, error) {
					c, e := inspect(ctx, ref)
					if ref == "old-services" {
						c.NetworkMode = "container:gone"
					}
					return c, e
				}
			case "config drift":
				r.config = strings.Replace(r.config, "services:latest", "other:latest", 1)
			case "tag drift":
				image = func(ref string) (string, error) {
					if ref == "services:latest" {
						return "sha256:unexpected", nil
					}
					return localImage(ref)
				}
			case "inspect failure":
				runtime.Inspect = func(context.Context, string) (NetworkContainer, error) {
					return NetworkContainer{}, fmt.Errorf("unavailable")
				}
			case "no runtime":
				runtime = NetworkRuntime{}
			case "persist failure":
				runtime.Persist = func(UpdatePlan) error { return fmt.Errorf("disk full") }
			}
			ctx = WithNetworkRuntime(ctx, runtime)
			if err := u.UpdateProjectVerified(ctx, m, image, func(groups.Container, string) error { return nil }); err == nil {
				t.Fatal("unsafe plan succeeded")
			}
			for _, call := range r.calls {
				a := strings.Join(call.args, " ")
				if strings.Contains(a, " up ") || strings.Contains(a, " stop ") {
					t.Fatalf("mutation before validation: %s", a)
				}
			}
			if scenario != "persist failure" && len(r.calls) != 0 {
				t.Fatalf("pull before safe preflight: %+v", r.calls)
			}
		})
	}
}

func TestNetworkFailureNeverRestartsConsumers(t *testing.T) {
	for _, scenario := range []string{"provider apply", "provider verify", "stale replacement namespace", "same identity", "wrong image", "post-pull tag drift"} {
		t.Run(scenario, func(t *testing.T) {
			u, m, r, ctx, events, _ := networkFixture(t)
			runtime := ctx.Value(networkRuntimeKey{}).(NetworkRuntime)
			inspect := runtime.Inspect
			image := localImage
			verify := func(groups.Container, string) error { return nil }
			switch scenario {
			case "provider apply":
				r.failService = "vpn"
			case "provider verify":
				verify = func(c groups.Container, _ string) error {
					if c.Service == "vpn" {
						return fmt.Errorf("unhealthy")
					}
					return nil
				}
			case "stale replacement namespace":
				runtime.Inspect = func(ctx context.Context, ref string) (NetworkContainer, error) {
					c, e := inspect(ctx, ref)
					if ref == "services" {
						c.NetworkMode = "container:old-vpn"
					}
					return c, e
				}
			case "same identity":
				runtime.Inspect = func(ctx context.Context, ref string) (NetworkContainer, error) {
					c, e := inspect(ctx, ref)
					if ref == "vpn" {
						c.ID = "old-vpn"
					}
					return c, e
				}
			case "wrong image":
				runtime.Inspect = func(ctx context.Context, ref string) (NetworkContainer, error) {
					c, e := inspect(ctx, ref)
					if ref == "vpn" {
						c.Image = "sha256:wrong"
					}
					return c, e
				}
			case "post-pull tag drift":
				image = func(ref string) (string, error) {
					if len(r.calls) > 0 && ref == "services:latest" {
						return "sha256:drift", nil
					}
					return localImage(ref)
				}
			}
			ctx = WithNetworkRuntime(ctx, runtime)
			if err := u.UpdateProjectVerified(ctx, m, image, verify); err == nil {
				t.Fatal("unsafe replacement succeeded")
			}
			if r.replaced["child"] {
				t.Fatalf("started transitive consumer after failure: %v", *events)
			}
			if scenario != "stale replacement namespace" && r.replaced["services"] {
				t.Fatalf("started consumer after provider failure: %v", *events)
			}
			if scenario == "post-pull tag drift" {
				for _, call := range r.calls {
					if strings.Contains(strings.Join(call.args, " "), " stop ") {
						t.Fatal("quiesced before drift check")
					}
				}
			}
		})
	}
}

func TestContainerNameNamespaceReferences(t *testing.T) {
	u, m, r, ctx, _, _ := networkFixture(t)
	r.config = strings.Replace(r.config, "service:vpn", "container:vpn", 1)
	if err := u.UpdateProjectVerified(ctx, m, localImage, func(groups.Container, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
}

func TestRunningConsumerWithStoppedProviderFailsBeforePull(t *testing.T) {
	u, m, r, ctx, _, _ := networkFixture(t)
	m[0].State = "exited"
	m[0].UpdateAvailable = false
	m[1].UpdateAvailable = true
	if err := u.UpdateProjectVerified(ctx, m, localImage, func(groups.Container, string) error { return nil }); err == nil {
		t.Fatal("started consumer with stopped provider")
	}
	if len(r.calls) != 0 {
		t.Fatalf("mutation with stopped provider %+v", r.calls)
	}
}
