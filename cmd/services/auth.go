package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"services/internal/auth"
	"services/internal/dashboard"
)

func configureAuthenticationAt(handler *dashboard.Server, directory string) (*auth.Store, error) {
	if directory == "" || !filepath.IsAbs(directory) {
		return nil, errors.New("authentication requires an absolute SERVICES_DATA_DIR on persistent writable storage")
	}
	store, err := auth.Open(filepath.Join(directory, "auth.db"))
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		if !ok {
			store.Close()
		}
	}()
	hasUsers, err := store.HasUsers()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(directory, "admin-setup-token")
	token := ""
	if !hasUsers {
		token, err = localSetupToken(path)
		if err != nil {
			return nil, err
		}
	} else {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove obsolete setup token: %w", err)
		}
	}
	if err = handler.ConfigureAuthentication(store, token); err != nil {
		return nil, err
	}
	if !hasUsers {
		log.Printf("First administrator setup requires the local token in %s (never share it in chat or logs)", path)
	}
	ok = true
	return store, nil
}

func localSetupToken(path string) (string, error) {
	// Exclusive creation preserves the same setup token across restarts. The token
	// is never printed, and only someone with local volume access can claim setup.
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	generated := base64.RawURLEncoding.EncodeToString(b)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err == nil {
		if _, err = f.WriteString(generated + "\n"); err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return "", err
		}
		if closeErr != nil {
			return "", closeErr
		}
		return generated, nil
	}
	if !os.IsExist(err) {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 512 {
		return "", errors.New("local setup token must be a small regular file")
	}
	f, err = os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 513))
	if err != nil {
		return "", err
	}
	token := strings.TrimSpace(string(data))
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		return "", errors.New("invalid local setup token file; restore a valid token or remove it while Services is stopped")
	}
	if err = os.Chmod(path, 0600); err != nil {
		return "", err
	}
	return token, nil
}
