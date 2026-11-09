package composeupdate

import (
	"context"
	"services/internal/groups"
	"testing"
)

func TestUnselectedProviderIdentityCannotChangeSilently(t *testing.T) {
	u, m, _, ctx, _, _ := networkFixture(t)
	m[0].UpdateAvailable = false
	m[1].UpdateAvailable = true
	runtime := ctx.Value(networkRuntimeKey{}).(NetworkRuntime)
	inspect := runtime.Inspect
	runtime.Inspect = func(ctx context.Context, ref string) (NetworkContainer, error) {
		c, e := inspect(ctx, ref)
		if ref == "vpn" || ref == "new-vpn" {
			c.ID = "unexpected-provider"
		}
		if ref == "services" {
			c.NetworkMode = "container:new-vpn"
		}
		return c, e
	}
	ctx = WithNetworkRuntime(ctx, runtime)
	if err := u.UpdateProjectVerified(ctx, m, localImage, func(groups.Container, string) error { return nil }); err == nil {
		t.Fatal("unplanned provider replacement accepted")
	}
}
