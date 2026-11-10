package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"services/internal/auth"
)

func runAuthCommand(args []string, dataDir string, input io.Reader, output io.Writer) (handled bool, err error) {
	if len(args) == 0 || args[0] != "auth" {
		return false, nil
	}
	if len(args) != 5 || args[1] != "reset-password" || args[2] != "--username" || strings.TrimSpace(args[3]) == "" || args[4] != "--password-stdin" {
		return true, errors.New("usage: auth reset-password --username <username> --password-stdin")
	}
	if !filepath.IsAbs(dataDir) {
		return true, errors.New("password recovery requires an absolute SERVICES_DATA_DIR")
	}
	info, statErr := os.Lstat(filepath.Join(dataDir, "auth.db"))
	if statErr != nil || !info.Mode().IsRegular() {
		return true, errors.New("password recovery requires an existing regular auth.db")
	}
	// One lookahead byte distinguishes an oversized input from 1024 bytes plus CRLF.
	data, readErr := io.ReadAll(io.LimitReader(input, 1027))
	if readErr != nil {
		return true, errors.New("could not read password from stdin")
	}
	password := strings.TrimSuffix(string(data), "\n")
	if len(password) != len(data) {
		password = strings.TrimSuffix(password, "\r")
	}
	if !utf8.ValidString(password) || utf8.RuneCountInString(password) < 15 || len(password) > 1024 {
		return true, errors.New("password must contain at least 15 characters and at most 1024 UTF-8 bytes")
	}
	store, err := auth.Open(filepath.Join(dataDir, "auth.db"))
	if err != nil {
		return true, errors.New("could not open authentication database")
	}
	defer store.Close()
	users, err := store.ListUsers()
	if err != nil {
		return true, errors.New("could not list authentication users")
	}
	username := strings.ToLower(strings.TrimSpace(args[3]))
	for _, user := range users {
		if user.Username == username {
			if err := store.ChangePassword(user.ID, password); err != nil {
				return true, errors.New("could not reset password")
			}
			if _, err := io.WriteString(output, "Password reset successfully.\n"); err != nil {
				return true, errors.New("password reset completed but could not write confirmation")
			}
			return true, nil
		}
	}
	return true, errors.New("authentication user not found")
}
