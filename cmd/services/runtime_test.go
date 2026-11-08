package main

import "testing"

func TestWorkerModeRequiresExactOperationFlag(t *testing.T) {
	valid := "compose-0123456789abcdef0123456789abcdef"
	id, worker, err := parseMode([]string{"worker", "--operation", valid})
	if err != nil || !worker || id != valid {
		t.Fatal(id, worker, err)
	}
	for _, args := range [][]string{{"worker"}, {"worker", "--operation", "../bad"}, {"worker", "--operation", valid, "extra"}, {"other"}} {
		if _, _, err := parseMode(args); err == nil {
			t.Fatalf("accepted unsafe worker arguments %v", args)
		}
	}
	if _, worker, err := parseMode(nil); worker || err != nil {
		t.Fatal(worker, err)
	}
}
