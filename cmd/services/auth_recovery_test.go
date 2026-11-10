package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"services/internal/auth"
)

type recoveryErrorReader struct{}

func (recoveryErrorReader) Read([]byte) (int, error) { return 0, errors.New("secret-reader-password") }

func TestAuthRecoveryRejectsInvalidPasswordInput(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "auth.db"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	oversized := strings.NewReader(strings.Repeat("x", 1<<20))
	for _, input := range []io.Reader{strings.NewReader(""), strings.NewReader("short"), strings.NewReader(strings.Repeat("x", 1025)), oversized, recoveryErrorReader{}} {
		var output bytes.Buffer
		handled, err := runAuthCommand([]string{"auth", "reset-password", "--username", "admin", "--password-stdin"}, dir, input, &output)
		if !handled || err == nil || output.Len() != 0 {
			t.Fatalf("invalid password accepted: handled=%v err=%v", handled, err)
		}
		if strings.Contains(err.Error(), "secret-reader-password") {
			t.Fatal("reader error leaked secret")
		}
	}
	if consumed := (1 << 20) - oversized.Len(); consumed > 1027 {
		t.Fatalf("read unbounded password: %d bytes", consumed)
	}
}

func TestAuthRecoveryResetsPasswordAndInvalidatesSessions(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.Open(filepath.Join(dir, "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const oldPassword = "old operator password"
	user, err := store.Bootstrap("admin", oldPassword)
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := store.Login("admin", oldPassword)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := store.Login("admin", oldPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session(first); err != nil {
		t.Fatal(err)
	}
	const newPassword = "  new operator\tpassword  "
	var output bytes.Buffer
	handled, err := runAuthCommand([]string{"auth", "reset-password", "--username", "  AdMiN  ", "--password-stdin"}, dir, strings.NewReader(newPassword+"\r\n"), &output)
	if !handled || err != nil {
		t.Fatalf("reset handled=%v err=%v", handled, err)
	}
	if output.String() != "Password reset successfully.\n" {
		t.Fatal("expected benign success message only")
	}
	for _, token := range []string{first, second} {
		if _, err := store.Session(token); !errors.Is(err, auth.ErrUnauthorized) {
			t.Fatal("old session remained valid")
		}
	}
	if _, _, err := store.Login("admin", oldPassword); !errors.Is(err, auth.ErrUnauthorized) {
		t.Fatal("old password remained valid")
	}
	got, _, err := store.Login("admin", newPassword)
	if err != nil || got.ID != user.ID {
		t.Fatal("new password did not recover original account")
	}
}

func TestAuthRecoveryPreservesPasswordBytes(t *testing.T) {
	for _, tc := range []struct{ name, password, ending string }{
		{"minimum", "123456789012345", ""},
		{"maximum", strings.Repeat("x", 1024), "\r\n"},
		{"lf", "new recovery password", "\n"},
		{"preserve whitespace", " \tnew recovery password \t", ""},
		{"preserve embedded newline", "new\nrecovery password\n", "\n"},
		{"preserve bare carriage return", "new recovery password\r", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			store, err := auth.Open(filepath.Join(dir, "auth.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if _, err := store.Bootstrap("admin", "old operator password"); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			handled, err := runAuthCommand([]string{"auth", "reset-password", "--username", "admin", "--password-stdin"}, dir, strings.NewReader(tc.password+tc.ending), &output)
			if !handled || err != nil {
				t.Fatalf("reset failed: handled=%v err=%v", handled, err)
			}
			if _, _, err := store.Login("admin", tc.password); err != nil {
				t.Fatal("password bytes changed during recovery")
			}
		})
	}
}

func TestAuthRecoveryUnknownUserDoesNotChangeCredentials(t *testing.T) {
	dir := t.TempDir()
	store, err := auth.Open(filepath.Join(dir, "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	const oldPassword = "old operator password"
	if _, err := store.Bootstrap("admin", oldPassword); err != nil {
		t.Fatal(err)
	}
	_, token, err := store.Login("admin", oldPassword)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "secret-new-password"
	var output bytes.Buffer
	handled, err := runAuthCommand([]string{"auth", "reset-password", "--username", secret, "--password-stdin"}, dir, strings.NewReader(secret), &output)
	if !handled || err == nil {
		t.Fatal("unknown user accepted")
	}
	if output.Len() != 0 || strings.Contains(err.Error(), secret) {
		t.Fatal("failed reset disclosed input")
	}
	if _, err := store.Session(token); err != nil {
		t.Fatal("failed reset invalidated session")
	}
	if _, _, err := store.Login("admin", oldPassword); err != nil {
		t.Fatal("failed reset changed password")
	}
}

func TestAuthRecoveryRequiresExistingDatabase(t *testing.T) {
	for _, dir := range []string{"", "relative", t.TempDir(), filepath.Join(t.TempDir(), "missing")} {
		var output bytes.Buffer
		handled, err := runAuthCommand([]string{"auth", "reset-password", "--username", "admin", "--password-stdin"}, dir, strings.NewReader("a sufficiently long password"), &output)
		if !handled || err == nil || output.Len() != 0 {
			t.Fatalf("unsafe database path accepted: handled=%v err=%v", handled, err)
		}
		if filepath.IsAbs(dir) {
			if _, err := os.Stat(filepath.Join(dir, "auth.db")); !os.IsNotExist(err) {
				t.Fatal("recovery created missing database")
			}
		}
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "auth.db"), 0700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if handled, err := runAuthCommand([]string{"auth", "reset-password", "--username", "admin", "--password-stdin"}, dir, strings.NewReader("a sufficiently long password"), &output); !handled || err == nil {
		t.Fatal("directory accepted as database")
	}
}

func TestAuthRecoveryRejectsMalformedCommands(t *testing.T) {
	const secret = "do-not-print-this-password"
	commands := [][]string{
		{"auth"}, {"auth", "unknown"},
		{"auth", "reset-password"},
		{"auth", "reset-password", "--username", "admin"},
		{"auth", "reset-password", "--username", "", "--password-stdin"},
		{"auth", "reset-password", "--username", "   ", "--password-stdin"},
		{"auth", "reset-password", "--username", "admin", "--password", secret},
		{"auth", "reset-password", "--username", "admin", "--password-stdin", secret},
		{"auth", "reset-password", "--username=admin", "--password-stdin"},
		{"auth", "reset-password", "--password-stdin", "--username", "admin"},
	}
	for _, args := range commands {
		var output bytes.Buffer
		handled, err := runAuthCommand(args, t.TempDir(), strings.NewReader(secret), &output)
		if !handled || err == nil {
			t.Fatalf("malformed command handled=%v err=%v", handled, err)
		}
		if strings.Contains(err.Error(), secret) || output.Len() != 0 {
			t.Fatal("malformed command exposed input")
		}
	}
}

func TestAuthRecoveryIgnoresOtherCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"server"}, {"update-helper"}} {
		var output bytes.Buffer
		handled, err := runAuthCommand(args, "", strings.NewReader("unused secret password"), &output)
		if handled || err != nil || output.Len() != 0 {
			t.Fatalf("unrelated command handled=%v error=%v output=%q", handled, err, output.String())
		}
	}
}
