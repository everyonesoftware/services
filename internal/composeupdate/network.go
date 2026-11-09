package composeupdate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"services/internal/groups"
)

// NetworkContainer is a fresh Engine observation, never cached dashboard data.
type NetworkContainer struct {
	ID, Image, Name, Project, Service, NetworkMode string
	Running                                        bool
}

// PlannedService records only identities and reasons, never resolved Compose secrets.
type PlannedService struct {
	Service       string `json:"service"`
	OldID         string `json:"oldId"`
	OldImage      string `json:"oldImage"`
	ExpectedImage string `json:"expectedImage"`
	Provider      string `json:"provider,omitempty"`
	ProviderOldID string `json:"providerOldId,omitempty"`
	Reason        string `json:"reason"`
	Running       bool   `json:"running"`
}
type UpdatePlan struct {
	Services []PlannedService `json:"services"`
}
type NetworkRuntime struct {
	Inspect func(context.Context, string) (NetworkContainer, error)
	Persist func(UpdatePlan) error
}
type networkRuntimeKey struct{}

func WithNetworkRuntime(ctx context.Context, r NetworkRuntime) context.Context {
	return context.WithValue(ctx, networkRuntimeKey{}, r)
}

type privateOutputRunner interface {
	Output(context.Context, string, []string) ([]byte, error)
}
type resolvedService struct {
	Image         string `json:"image"`
	NetworkMode   string `json:"network_mode"`
	ContainerName string `json:"container_name"`
	Scale         *int   `json:"scale"`
	Deploy        struct {
		Replicas *int `json:"replicas"`
	} `json:"deploy"`
}

func (u *Updater) networkPlan(ctx context.Context, directory string, args []string, containers []groups.Container) (UpdatePlan, NetworkRuntime, error) {
	fail := func(err error) (UpdatePlan, NetworkRuntime, error) { return UpdatePlan{}, NetworkRuntime{}, err }
	runtime, ok := ctx.Value(networkRuntimeKey{}).(NetworkRuntime)
	if !ok || runtime.Inspect == nil || runtime.Persist == nil {
		return fail(fmt.Errorf("network-aware runtime is not configured"))
	}
	output, ok := u.runner.(privateOutputRunner)
	if !ok {
		return fail(fmt.Errorf("private Compose config reader is not configured"))
	}
	data, err := output.Output(ctx, directory, append(append([]string{}, args...), "config", "--format", "json"))
	if err != nil {
		return fail(fmt.Errorf("resolve Compose config failed"))
	}
	var config struct {
		Services map[string]resolvedService `json:"services"`
	}
	if json.Unmarshal(data, &config) != nil || len(config.Services) == 0 {
		return fail(fmt.Errorf("invalid resolved Compose config"))
	}
	byName := map[string]groups.Container{}
	observed := map[string]NetworkContainer{}
	providers := map[string]string{}
	selected := map[string]bool{}
	for _, c := range containers {
		if _, exists := byName[c.Service]; exists {
			return fail(fmt.Errorf("ambiguous Compose service %s", c.Service))
		}
		cfg, exists := config.Services[c.Service]
		if !exists || cfg.Image == "" || cfg.Image != c.Image {
			return fail(fmt.Errorf("Compose image configuration drift for %s", c.Service))
		}
		actual, e := runtime.Inspect(ctx, c.ID)
		if e != nil {
			return fail(fmt.Errorf("inspect service %s: %w", c.Service, e))
		}
		if actual.ID != c.ID || actual.Project != c.Project || actual.Service != c.Service || !strings.HasPrefix(actual.Image, "sha256:") {
			return fail(fmt.Errorf("unverified service identity %s", c.Service))
		}
		byName[c.Service] = c
		observed[c.Service] = actual
		if c.UpdateAvailable && actual.Running {
			selected[c.Service] = true
		}
	}
	for name, c := range byName {
		mode := config.Services[name].NetworkMode
		actual := observed[name]
		provider := ""
		switch {
		case strings.HasPrefix(mode, "service:"):
			provider = strings.TrimPrefix(mode, "service:")
			if _, ok := byName[provider]; !ok {
				return fail(fmt.Errorf("missing namespace provider for %s", name))
			}
		case strings.HasPrefix(mode, "container:"):
			ref := strings.TrimPrefix(mode, "container:")
			for n, candidate := range byName {
				if ref == strings.TrimPrefix(candidate.Name, "/") || ref == config.Services[n].ContainerName {
					if provider != "" {
						return fail(fmt.Errorf("ambiguous namespace provider for %s", name))
					}
					provider = n
				}
			}
			if provider == "" {
				return fail(fmt.Errorf("external or literal container namespace for %s", name))
			}
		default:
			if strings.HasPrefix(actual.NetworkMode, "container:") {
				return fail(fmt.Errorf("namespace configuration drift for %s", name))
			}
		}
		if provider != "" {
			if !strings.HasPrefix(actual.NetworkMode, "container:") {
				return fail(fmt.Errorf("namespace configuration drift for %s", name))
			}
			// A deliberately stopped consumer can still refer to a retired
			// provider. We neither start nor recreate it; only running consumers
			// must prove their current namespace attachment.
			if actual.Running {
				target, e := runtime.Inspect(ctx, strings.TrimPrefix(actual.NetworkMode, "container:"))
				if e != nil || target.ID != observed[provider].ID {
					return fail(fmt.Errorf("stale namespace for %s", name))
				}
			}
			providers[c.Service] = provider
		}
	}
	// Validate the entire namespace graph, including currently unselected branches.
	names := make([]string, 0, len(byName))
	for n := range byName {
		names = append(names, n)
	}
	sort.Strings(names)
	state := map[string]int{}
	order := []string{}
	var visit func(string) error
	visit = func(n string) error {
		if state[n] == 1 {
			return fmt.Errorf("network namespace cycle at %s", n)
		}
		if state[n] == 2 {
			return nil
		}
		state[n] = 1
		if p := providers[n]; p != "" {
			if err := visit(p); err != nil {
				return err
			}
		}
		state[n] = 2
		order = append(order, n)
		return nil
	}
	for _, n := range names {
		if err := visit(n); err != nil {
			return fail(err)
		}
	}
	if len(selected) == 0 {
		return fail(fmt.Errorf("project has no running services with available image updates"))
	}
	affected := map[string]bool{}
	for n := range selected {
		affected[n] = true
	}
	for _, n := range order {
		if affected[providers[n]] {
			affected[n] = true
		}
	}
	for _, n := range order {
		if affected[n] && observed[n].Running && providers[n] != "" && !observed[providers[n]].Running {
			return fail(fmt.Errorf("namespace provider is stopped for %s", n))
		}
	}
	plan := UpdatePlan{}
	for _, n := range order {
		if !affected[n] {
			continue
		}
		cfg := config.Services[n]
		if cfg.Scale != nil && cfg.Deploy.Replicas != nil && *cfg.Scale != *cfg.Deploy.Replicas {
			return fail(fmt.Errorf("conflicting replica declarations for %s", n))
		}
		if (cfg.Scale != nil && *cfg.Scale != 1) || (cfg.Deploy.Replicas != nil && *cfg.Deploy.Replicas != 1) {
			return fail(fmt.Errorf("unsupported desired replica count for %s: require exactly one", n))
		}
		reason := "network reattachment"
		if selected[n] {
			reason = "image update"
		}
		a := observed[n]
		if !a.Running {
			reason = "preserve stopped"
		}
		plan.Services = append(plan.Services, PlannedService{Service: n, OldID: a.ID, OldImage: a.Image, ExpectedImage: a.Image, Provider: providers[n], ProviderOldID: observed[providers[n]].ID, Reason: reason, Running: a.Running})
	}
	return plan, runtime, nil
}

// verifyServiceCardinality reads private, full-ID inventory including stopped
// containers. Compose versions emit either a JSON array or a stream of objects.
// The sole member must be the exact container whose identity we verified.
func (u *Updater) verifyServiceCardinality(ctx context.Context, directory string, args []string, project, service, id string) error {
	output, ok := u.runner.(privateOutputRunner)
	if !ok {
		return fmt.Errorf("private Compose inventory reader is not configured")
	}
	data, err := output.Output(ctx, directory, append(append([]string{}, args...), "ps", "--all", "--format", "json", "--no-trunc", service))
	if err != nil {
		return fmt.Errorf("read Compose replica inventory failed for %s", service)
	}
	type member struct {
		ID      string `json:"ID"`
		Project string `json:"Project"`
		Service string `json:"Service"`
	}
	var members []member
	data = bytes.TrimSpace(data)
	if bytes.HasPrefix(data, []byte("[")) {
		if json.Unmarshal(data, &members) != nil {
			return fmt.Errorf("invalid Compose replica inventory for %s", service)
		}
	} else {
		decoder := json.NewDecoder(bytes.NewReader(data))
		for {
			var m member
			err := decoder.Decode(&m)
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("invalid Compose replica inventory for %s", service)
			}
			members = append(members, m)
		}
	}
	if len(members) != 1 || members[0].ID != id || members[0].Project != project || members[0].Service != service {
		return fmt.Errorf("replica cardinality or identity mismatch for %s: require exactly the verified container", service)
	}
	return nil
}

func verifyNamespace(ctx context.Context, r NetworkRuntime, c NetworkContainer, provider NetworkContainer) error {
	if !strings.HasPrefix(c.NetworkMode, "container:") {
		return fmt.Errorf("replacement has no container namespace")
	}
	target, err := r.Inspect(ctx, strings.TrimPrefix(c.NetworkMode, "container:"))
	if err != nil || target.ID != provider.ID {
		return fmt.Errorf("replacement namespace does not resolve to current provider")
	}
	return nil
}
