package githubapp

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/crypto/nacl/box"
)

const secretName = "SERVICES_PUBLISH_TOKEN"
const enabledName = "SERVICES_PUBLISH_NOTIFICATIONS"

var variableAllowlist = map[string]bool{
	"SERVICES_PUBLISH_URL": true,
	enabledName:            true,
	"TAILSCALE_CLIENT_ID":  true,
	"TAILSCALE_AUDIENCE":   true,
	"TAILSCALE_TAGS":       true,
}

// repositoryToken requests a token restricted to exactly one repository and
// verifies all caller-supplied identity fields against GitHub before use.
func (o *operation) repositoryToken(app App, repository Repository, permissions map[string]string) (string, error) {
	if !validRepository(repository) {
		return "", errors.New("invalid GitHub repository")
	}
	jwt, err := appJWT(app)
	if err != nil {
		return "", err
	}
	token, err := o.installationToken(jwt, repository.InstallationID, []int64{repository.ID}, permissions)
	if err != nil {
		return "", err
	}
	var v struct {
		ID            int64  `json:"id"`
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	}
	if _, err = o.request("GET", "/repos/"+repository.FullName, token, nil, &v, 200); err != nil {
		return "", err
	}
	if v.ID != repository.ID || v.FullName != repository.FullName || v.DefaultBranch != repository.DefaultBranch {
		return "", errors.New("GitHub repository identity changed; select it again")
	}
	return token, nil
}

type variable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func (o *operation) getVariable(base, token, name string) (variable, bool, error) {
	var v variable
	status, err := o.request("GET", base+"/variables/"+name, token, nil, &v, 200, 404)
	if err != nil {
		return v, false, err
	}
	if status == 404 {
		return v, false, nil
	}
	if v.Name != name {
		return v, false, errors.New("GitHub returned invalid variable metadata")
	}
	return v, true, nil
}
func (o *operation) verifyVariable(base, token, name, value string) error {
	v, exists, err := o.getVariable(base, token, name)
	if err != nil {
		return err
	}
	if !exists || v.Value != value {
		return errors.New("GitHub variable readback did not match")
	}
	return nil
}
func (o *operation) secretExists(base, token string) (bool, error) {
	var v struct {
		Name string `json:"name"`
	}
	status, err := o.request("GET", base+"/secrets/"+secretName, token, nil, &v, 200, 404)
	if err != nil {
		return false, err
	}
	if status == 404 {
		return false, nil
	}
	if v.Name != secretName {
		return false, errors.New("GitHub returned invalid secret metadata")
	}
	return true, nil
}

// Configure refuses to overwrite a preexisting secret or a differing variable.
// It checks every conflict before writing. GitHub's secret endpoint is an upsert,
// not a conditional create: concurrent outside writers cannot be made atomic.
// A failed or uncertain write must be recovered explicitly, not blindly retried.
func (c *Client) Configure(ctx context.Context, app App, repository Repository, secret string, variables map[string]string) error {
	return c.configure(ctx, app, repository, secret, variables, false)
}

// RecoverConfiguration leaves an existing secret untouched and reconciles only
// identical or missing variables. Metadata cannot prove the stored key; callers
// must require the authenticated connection callback before enabling delivery.
func (c *Client) RecoverConfiguration(ctx context.Context, app App, repository Repository, secret string, variables map[string]string) error {
	return c.configure(ctx, app, repository, secret, variables, true)
}

func validPublisherSecret(secret string) bool {
	return len(secret) >= 32 && len(secret) <= 48<<10 && strings.IndexFunc(secret, func(r rune) bool { return r < 33 || r > 126 }) < 0
}

func (c *Client) configure(ctx context.Context, app App, repository Repository, secret string, variables map[string]string, recover bool) error {
	if !validPublisherSecret(secret) {
		return errors.New("invalid publisher secret")
	}
	if variables[enabledName] != "false" {
		return errors.New("publisher notifications must initially be disabled")
	}
	names := make([]string, 0, len(variables))
	for name, value := range variables {
		if !variableAllowlist[name] || len(value) > 48<<10 || strings.ContainsRune(value, '\x00') {
			return errors.New("invalid publisher variable")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	o, cancel := c.begin(ctx)
	defer cancel()
	token, err := o.repositoryToken(app, repository, map[string]string{"metadata": "read", "secrets": "write", "variables": "write"})
	if err != nil {
		return err
	}
	base := "/repos/" + repository.FullName + "/actions"
	exists, err := o.secretExists(base, token)
	if err != nil {
		return err
	}
	if exists && !recover {
		return ErrConflict
	}
	missing := []string{}
	for _, name := range names {
		v, exists, err := o.getVariable(base, token, name)
		if err != nil {
			return err
		}
		if exists && v.Value != variables[name] {
			return ErrConflict
		}
		if !exists {
			missing = append(missing, name)
		}
	}
	if !exists {
		encrypted, err := o.encryptPublisherSecret(base, token, secret)
		if err != nil {
			return err
		}
		// GitHub has no conditional-create secret API. Recheck to narrow the
		// unavoidable race with outside writers; recovery never knowingly overwrites.
		exists, err = o.secretExists(base, token)
		if err != nil {
			return err
		}
		if exists && !recover {
			return ErrConflict
		}
		if !exists {
			if err = o.writePublisherSecret(base, token, encrypted); err != nil {
				return err
			}
		}
	}
	for _, name := range missing {
		// Recheck absence: never knowingly overwrite a concurrently created variable.
		v, exists, err := o.getVariable(base, token, name)
		if err != nil {
			return err
		}
		if exists && v.Value != variables[name] {
			return ErrConflict
		}
		if !exists {
			if _, err = o.request("POST", base+"/variables", token, variable{Name: name, Value: variables[name]}, nil, 201); err != nil {
				return err
			}
		}
		if err = o.verifyVariable(base, token, name, variables[name]); err != nil {
			return err
		}
	}
	// Verify preexisting identical variables too, not just writes.
	for _, name := range names {
		if err = o.verifyVariable(base, token, name, variables[name]); err != nil {
			return err
		}
	}
	if recover {
		exists, err := o.secretExists(base, token)
		if err != nil {
			return err
		}
		if !exists {
			return errors.New("GitHub secret readback was missing")
		}
	}
	return nil
}

// ReplaceSecret explicitly rotates an integration-owned configured connection.
// Ownership and retention of the old local key are the caller's responsibility.
// Metadata readback is not proof of plaintext; the new key must authenticate a
// callback before the caller revokes the old key or enables notifications.
func (c *Client) ReplaceSecret(ctx context.Context, app App, repository Repository, secret string) error {
	if !validPublisherSecret(secret) {
		return errors.New("invalid publisher secret")
	}
	o, cancel := c.begin(ctx)
	defer cancel()
	token, err := o.repositoryToken(app, repository, map[string]string{"metadata": "read", "secrets": "write"})
	if err != nil {
		return err
	}
	base := "/repos/" + repository.FullName + "/actions"
	encrypted, err := o.encryptPublisherSecret(base, token, secret)
	if err != nil {
		return err
	}
	return o.writePublisherSecret(base, token, encrypted)
}

func (o *operation) encryptPublisherSecret(base, token, secret string) (map[string]string, error) {
	var key struct {
		ID  string `json:"key_id"`
		Key string `json:"key"`
	}
	if _, err := o.request("GET", base+"/secrets/public-key", token, nil, &key, 200); err != nil {
		return nil, err
	}
	decoded, err := base64.StdEncoding.DecodeString(key.Key)
	if err != nil || len(decoded) != 32 || key.ID == "" || len(key.ID) > 256 {
		return nil, errors.New("GitHub returned invalid encryption metadata")
	}
	var public [32]byte
	copy(public[:], decoded)
	encrypted, err := box.SealAnonymous(nil, []byte(secret), &public, rand.Reader)
	if err != nil {
		return nil, errors.New("publisher secret encryption failed")
	}
	return map[string]string{"key_id": key.ID, "encrypted_value": base64.StdEncoding.EncodeToString(encrypted)}, nil
}
func (o *operation) writePublisherSecret(base, token string, encrypted map[string]string) error {
	if _, err := o.request("PUT", base+"/secrets/"+secretName, token, encrypted, nil, 201, 204); err != nil {
		return err
	}
	exists, err := o.secretExists(base, token)
	if err != nil {
		return err
	}
	if !exists {
		return errors.New("GitHub secret readback was missing")
	}
	return nil
}

func (c *Client) SetEnabled(ctx context.Context, app App, repository Repository, enabled bool) error {
	o, cancel := c.begin(ctx)
	defer cancel()
	token, err := o.repositoryToken(app, repository, map[string]string{"metadata": "read", "variables": "write"})
	if err != nil {
		return err
	}
	base := "/repos/" + repository.FullName + "/actions"
	_, exists, err := o.getVariable(base, token, enabledName)
	if err != nil {
		return err
	}
	method, path, status := "POST", base+"/variables", 201
	if exists {
		method, path, status = "PATCH", base+"/variables/"+enabledName, 204
	}
	value := strconv.FormatBool(enabled)
	if _, err = o.request(method, path, token, variable{Name: enabledName, Value: value}, nil, status); err != nil {
		return err
	}
	return o.verifyVariable(base, token, enabledName, value)
}
