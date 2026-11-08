package composeupdate

import (
	"context"
	"errors"
	"services/internal/groups"
	"strings"
	"testing"
)

func TestVerifiedUpdateDoesNotReplaceBeforeImageValidation(t *testing.T) {
	r := &recordingRunner{}
	u, c, _ := testUpdater(t, r)
	c.UpdateAvailable = true
	err := u.UpdateProjectVerified(context.Background(), []groups.Container{c}, func(string) (string, error) { return "", errors.New("image unavailable") }, func(groups.Container, string) error { t.Fatal("verification before apply"); return nil })
	if err == nil || len(r.calls) != 1 {
		t.Fatalf("applied unvalidated image: %v %+v", err, r.calls)
	}
}
func TestVerifiedUpdateRequiresReplacementVerification(t *testing.T) {
	r := &recordingRunner{}
	u, c, _ := testUpdater(t, r)
	c.UpdateAvailable = true
	count := 0
	err := u.UpdateProjectVerified(context.Background(), []groups.Container{c}, func(string) (string, error) { return "sha256:pulled", nil }, func(g groups.Container, image string) error {
		count++
		if image != "sha256:pulled" {
			t.Fatal(image)
		}
		return errors.New("readiness failed")
	})
	if err == nil || count != 1 || len(r.calls) != 2 {
		t.Fatalf("verification: %v %d %+v", err, count, r.calls)
	}
	if !strings.Contains(strings.Join(r.calls[1].args, " "), "--wait") {
		t.Fatal("missing compose wait")
	}
}
