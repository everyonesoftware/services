package composeupdate

import (
	"context"
	"fmt"
	"reflect"
	"sort"

	"services/internal/groups"
)

// UpdateProject pulls and recreates only services with an available image change.
func (u *Updater) UpdateProject(ctx context.Context, containers []groups.Container) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	var selected *groups.Container
	var projectArgs []string
	services := make(map[string]struct{})
	for i := range containers {
		// Validate every member before deduplication or any command, including
		// replicas and members without image changes.
		_, args, err := u.composeArgs(containers[i])
		if err != nil {
			return err
		}
		if i == 0 {
			projectArgs = args
		} else if !reflect.DeepEqual(projectArgs, args) {
			return fmt.Errorf("inconsistent Compose project metadata")
		}
		if containers[i].UpdateAvailable {
			if selected == nil {
				selected = &containers[i]
			}
			services[containers[i].Service] = struct{}{}
		}
	}
	if selected == nil {
		return fmt.Errorf("project has no available image updates")
	}
	names := make([]string, 0, len(services))
	for name := range services {
		names = append(names, name)
	}
	sort.Strings(names)
	if err := u.run(ctx, *selected, append([]string{"pull"}, names...)...); err != nil {
		return err
	}
	return u.run(ctx, *selected, append([]string{"up", "-d", "--no-deps", "--pull", "never", "--no-build", "--wait", "--wait-timeout", "300"}, names...)...)
}
