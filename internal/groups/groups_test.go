package groups

import "testing"

func TestAggregateComposeGroupsByProjectAndHealth(t *testing.T) {
	containers := []Container{
		{ID: "1", Name: "web-1", Project: "shop", Service: "web", State: "running", Health: "healthy"},
		{ID: "2", Name: "db-1", Project: "shop", Service: "db", State: "running", Health: "healthy"},
		{ID: "3", Name: "cache-1", Project: "blog", Service: "cache", State: "running"},
	}

	got := Aggregate(containers)
	if len(got) != 2 {
		t.Fatalf("Aggregate() returned %d groups, want 2", len(got))
	}
	if got[0].Name != "blog" || got[0].Status != Status("running") {
		t.Errorf("first group = (%q, %q), want (blog, running)", got[0].Name, got[0].Status)
	}
	if got[1].Name != "shop" || got[1].Status != StatusHealthy || len(got[1].Containers) != 2 {
		t.Errorf("second group = %+v, want healthy shop group with 2 containers", got[1])
	}
}

func TestAggregateMarksProjectHealthyWhenCheckedContainersPass(t *testing.T) {
	got := Aggregate([]Container{
		{Project: "media", Service: "app", State: "running", Health: "healthy"},
		{Project: "media", Service: "worker", State: "running"},
	})
	if len(got) != 1 || got[0].Status != StatusHealthy {
		t.Fatalf("Aggregate() = %+v, want one healthy media group", got)
	}
}

func TestAggregateMarksProjectUnhealthyWhenAContainerIsNotRunning(t *testing.T) {
	got := Aggregate([]Container{
		{Project: "media", Service: "app", State: "running", Health: "healthy"},
		{Project: "media", Service: "worker", State: "restarting"},
	})
	if len(got) != 1 || got[0].Status != StatusUnhealthy {
		t.Fatalf("Aggregate() = %+v, want one unhealthy media group", got)
	}
}

func TestAggregateMarksProjectStartingWhenHealthCheckIsStarting(t *testing.T) {
	got := Aggregate([]Container{
		{Project: "media", Service: "app", State: "running", Health: "starting"},
	})
	if len(got) != 1 || got[0].Status != StatusStarting {
		t.Fatalf("Aggregate() = %+v, want one starting media group", got)
	}
}

func TestAggregateKeepsUnlabelledContainersOutOfComposeGroups(t *testing.T) {
	got := Aggregate([]Container{{ID: "raw", Name: "manual", State: "running"}})
	if len(got) != 0 {
		t.Fatalf("Aggregate() returned %d groups for an unlabelled container, want 0", len(got))
	}
}

func TestAggregateMarksProjectWhenAnyContainerHasAnUpdate(t *testing.T) {
	got := Aggregate([]Container{
		{Project: "media", Service: "app", State: "running"},
		{Project: "media", Service: "worker", State: "running", UpdateAvailable: true},
	})
	if len(got) != 1 || !got[0].UpdateAvailable {
		t.Fatalf("Aggregate() = %+v, want media project with update available", got)
	}
}
