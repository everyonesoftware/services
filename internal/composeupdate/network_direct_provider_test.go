package composeupdate

import (
	"context"
	"services/internal/groups"
	"testing"
)

func TestTransitiveConsumerMustReferenceDirectProviderNotUltimateNamespaceOwner(t *testing.T) {
	u, m, _, ctx, _, _ := networkFixture(t)
	runtime := ctx.Value(networkRuntimeKey{}).(NetworkRuntime)
	inspect := runtime.Inspect
	runtime.Inspect = func(ctx context.Context, ref string) (NetworkContainer, error) {
		c, e := inspect(ctx, ref)
		if ref == "child" {
			c.NetworkMode = "container:new-vpn"
		}
		return c, e
	}
	ctx = WithNetworkRuntime(ctx, runtime)
	if err := u.UpdateProjectVerified(ctx, m, localImage, func(groups.Container, string) error { return nil }); err == nil {
		t.Fatal("transitive consumer attached to ultimate owner instead of direct provider")
	}
}
