package composeupdate

import (
	"context"
	"fmt"
	"reflect"
	"sort"
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
	selected := map[string]groups.Container{}
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
		if c.UpdateAvailable {
			selected[c.Service] = c
		}
	}
	if len(selected) == 0 {
		return fmt.Errorf("project has no available image updates")
	}
	names := make([]string, 0, len(selected))
	for name := range selected {
		names = append(names, name)
	}
	sort.Strings(names)
	reportProgress(ctx, "phase: pulling")
	if err := u.runner.Run(ctx, directory, append(append([]string{}, args...), append([]string{"pull"}, names...)...)); err != nil {
		return err
	}
	expected := map[string]string{}
	for _, name := range names {
		image, err := imageID(selected[name].Image)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(image, "sha256:") {
			return fmt.Errorf("pulled image has no immutable ID")
		}
		expected[name] = image
	}
	apply := append(append([]string{}, args...), "up", "-d", "--no-deps", "--pull", "never", "--no-build", "--wait", "--wait-timeout", "300")
	apply = append(apply, names...)
	reportProgress(ctx, "phase: applying")
	if err := u.runner.Run(ctx, directory, apply); err != nil {
		return err
	}
	reportProgress(ctx, "phase: verifying")
	for _, c := range containers {
		if image, ok := expected[c.Service]; ok {
			if err := verify(c, image); err != nil {
				return err
			}
		}
	}
	return nil
}
