package composeupdate

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"services/internal/groups"
)

// UpdateProjectVerified validates the complete project, pulls first, records the
// pulled immutable image IDs, and verifies every selected replacement against
// them. No temporary Compose file is used: it would contaminate the persistent
// config_files labels and make the next update unsafe/unresolvable.
// It is used only inside the independent operation helper.
func (u *Updater) UpdateProjectVerified(ctx context.Context, containers []groups.Container, imageID func(string) (string, error), verify func(groups.Container, string) error) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if imageID == nil || verify == nil || u.runner == nil {
		return fmt.Errorf("verified updater is not configured")
	}
	var directory string
	var args []string

	for i, c := range containers {
		d, a, err := u.composeArgs(c)
		if err != nil {
			return err
		}
		if i == 0 {
			directory = d
			args = a
		} else if !reflect.DeepEqual(args, a) {
			return fmt.Errorf("inconsistent Compose project metadata")
		}

	}
	plan, runtime, err := u.networkPlan(ctx, directory, args, containers)
	if err != nil {
		return err
	}
	members := map[string]groups.Container{}
	for _, c := range containers {
		members[c.Service] = c
	}
	// Check unchanged dependents before pulling too: tag aliases must not silently
	// upgrade a service selected only for namespace repair.
	checkUnchanged := func() error {
		for _, p := range plan.Services {
			if p.Reason != "image update" && p.Running {
				image, err := imageID(members[p.Service].Image)
				if err != nil {
					return err
				}
				if image != p.OldImage {
					return fmt.Errorf("image tag drift for unchanged dependent %s", p.Service)
				}
			}
		}
		return nil
	}
	if err := checkUnchanged(); err != nil {
		return err
	}
	names := []string{}
	for _, p := range plan.Services {
		if p.Reason == "image update" {
			names = append(names, p.Service)
		}
	}
	reportProgress(ctx, "phase: pulling")
	if err := u.runner.Run(ctx, directory, append(append([]string{}, args...), append([]string{"pull"}, names...)...)); err != nil {
		return err
	}
	for i := range plan.Services {
		p := &plan.Services[i]
		if p.Reason != "image update" {
			continue
		}
		image, err := imageID(members[p.Service].Image)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(image, "sha256:") {
			return fmt.Errorf("pulled image has no immutable ID")
		}
		p.ExpectedImage = image
	}
	if err := checkUnchanged(); err != nil {
		return err
	}
	if err := runtime.Persist(plan); err != nil {
		return fmt.Errorf("persist execution plan: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	reportProgress(ctx, "phase: applying")
	// Stop consumers before their namespace is removed. Leave intentionally
	// stopped services untouched; a later explicit Compose up can reconcile them.
	for i := len(plan.Services) - 1; i >= 0; i-- {
		p := plan.Services[i]
		if !p.Running || p.Provider == "" {
			continue
		}
		affectedProvider := false
		for _, other := range plan.Services {
			if other.Service == p.Provider {
				affectedProvider = true
			}
		}
		if affectedProvider {
			if err := u.runner.Run(ctx, directory, append(append([]string{}, args...), "stop", p.Service)); err != nil {
				return err
			}
		}
	}
	current := map[string]NetworkContainer{}
	for _, p := range plan.Services {
		if !p.Running {
			continue
		}
		apply := append(append([]string{}, args...), "up", "-d", "--no-deps", "--force-recreate", "--pull", "never", "--no-build", "--wait", "--wait-timeout", "300", p.Service)
		if err := u.runner.Run(ctx, directory, apply); err != nil {
			return err
		}
		reportProgress(ctx, "phase: verifying")
		c := members[p.Service]
		if err := verify(c, p.ExpectedImage); err != nil {
			return err
		}
		actual, err := runtime.Inspect(ctx, c.Name)
		if err != nil {
			return err
		}
		if actual.ID == p.OldID || actual.Image != p.ExpectedImage || !actual.Running || actual.Project != c.Project || actual.Service != c.Service {
			return fmt.Errorf("replacement identity mismatch for %s", p.Service)
		}
		if err := u.verifyServiceCardinality(ctx, directory, args, c.Project, p.Service, actual.ID); err != nil {
			return err
		}
		if p.Provider != "" {
			provider, ok := current[p.Provider]
			if !ok {
				provider, err = runtime.Inspect(ctx, members[p.Provider].Name)
				if err != nil {
					return err
				}
				if provider.ID != p.ProviderOldID || !provider.Running || provider.Project != c.Project || provider.Service != p.Provider {
					return fmt.Errorf("unselected namespace provider changed for %s", p.Service)
				}
			}
			if err := verifyNamespace(ctx, runtime, actual, provider); err != nil {
				return fmt.Errorf("%s: %w", p.Service, err)
			}
		}
		current[p.Service] = actual
	}
	// Recheck the completed plan, including deliberately stopped services and
	// earlier replacements, rather than inferring cardinality from one name.
	for _, p := range plan.Services {
		id := p.OldID
		if p.Running {
			id = current[p.Service].ID
		}
		if err := u.verifyServiceCardinality(ctx, directory, args, members[p.Service].Project, p.Service, id); err != nil {
			return err
		}
	}
	return nil
}
