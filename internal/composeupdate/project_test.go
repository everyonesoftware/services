package composeupdate

import (
	"context"
	"reflect"
	"services/internal/groups"
	"testing"
)

func TestUpdateProjectValidatesEveryMemberBeforeCommands(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*groups.Container)
	}{
		{"different project", func(c *groups.Container) { c.Project = "other" }},
		{"different files", func(c *groups.Container) { c.ComposeConfigFiles = c.ComposeConfigFiles[:1] }},
		{"outside root", func(c *groups.Container) { c.ComposeConfigFiles = []string{`C:\\outside\\compose.yaml`} }},
		{"invalid service", func(c *groups.Container) { c.Service = "--all" }},
		{"missing service", func(c *groups.Container) { c.Service = "" }},
		{"missing project", func(c *groups.Container) { c.Project = "" }},
		{"different working directory", func(c *groups.Container) { c.ComposeWorkingDir = `E:\\Containers` }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner := &recordingRunner{}
			updater, web, _ := testUpdater(t, runner)
			web.UpdateAvailable = true
			other := web
			tc.mutate(&other)
			// Unchanged services still must agree with all project metadata.
			other.UpdateAvailable = false
			if err := updater.UpdateProject(context.Background(), []groups.Container{web, other}); err == nil {
				t.Fatal("want validation error")
			}
			if len(runner.calls) != 0 {
				t.Fatalf("validation ran commands: %v", runner.calls)
			}
		})
	}
}

func TestUpdateProjectRejectsNoChangesWithoutCommands(t *testing.T) {
	for _, empty := range []bool{false, true} {
		runner := &recordingRunner{}
		updater, web, _ := testUpdater(t, runner)
		members := []groups.Container{web}
		if empty {
			members = nil
		}
		if err := updater.UpdateProject(context.Background(), members); err == nil {
			t.Fatal("want no-updates error")
		}
		if len(runner.calls) != 0 {
			t.Fatalf("calls = %v", runner.calls)
		}
	}
}

func TestUpdateProjectPropagatesPullAndUpFailures(t *testing.T) {
	for _, failAt := range []int{1, 2} {
		runner := &recordingRunner{failAt: failAt}
		updater, web, _ := testUpdater(t, runner)
		web.UpdateAvailable = true
		if err := updater.UpdateProject(context.Background(), []groups.Container{web}); err == nil {
			t.Fatal("want Compose failure")
		}
		if len(runner.calls) != failAt {
			t.Fatalf("got %d calls, want %d", len(runner.calls), failAt)
		}
	}
}

func TestUpdateProjectDeduplicatesChangedServices(t *testing.T) {
	runner := &recordingRunner{}
	updater, web, _ := testUpdater(t, runner)
	web.UpdateAvailable = true
	api := web
	api.Service = "api"
	unchanged := web
	unchanged.Service = "db"
	unchanged.UpdateAvailable = false
	if err := updater.UpdateProject(context.Background(), []groups.Container{web, api, web, unchanged}); err != nil {
		t.Fatal(err)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %v, want one pull and one up", runner.calls)
	}
	for i, tail := range [][]string{{"pull", "api", "web"}, {"up", "-d", "--no-deps", "--pull", "never", "--no-build", "--wait", "--wait-timeout", "300", "api", "web"}} {
		args := runner.calls[i].args
		if len(args) < len(tail) || !reflect.DeepEqual(args[len(args)-len(tail):], tail) {
			t.Fatalf("args = %v, want suffix %v", args, tail)
		}
	}
}
