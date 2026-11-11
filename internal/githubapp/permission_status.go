package githubapp

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var permissionOwner = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)

// PermissionStatus describes the GitHub App's current Variables grant and its
// authenticated owner's settings page. Installation grants are checked separately.
type PermissionStatus struct {
	VariablesWrite bool   `json:"variablesWrite"`
	SettingsURL    string `json:"settingsUrl"`
}

// VerifyRepositoryPermissions proves the installation accepts Variables write
// for exactly the selected repository, without modifying repository state.
func (c *Client) VerifyRepositoryPermissions(ctx context.Context, app App, repository Repository) error {
	o, cancel := c.begin(ctx)
	defer cancel()
	_, err := o.repositoryToken(app, repository, map[string]string{"metadata": "read", "actions_variables": "write"})
	if err != nil {
		// repositoryToken returns only sanitized local errors. Keep cancellation
		// distinguishable without exposing the underlying transport's error text.
		if o.ctx.Err() != nil {
			err = o.ctx.Err()
		}
		return fmt.Errorf("could not verify GitHub repository permissions: %w; confirm the App's Variables permission is Read & write and approve the installation's updated permissions, then retry", err)
	}
	return nil
}

// CheckPermissions reads the current App grant using App authentication.
func (c *Client) CheckPermissions(ctx context.Context, app App) (PermissionStatus, error) {
	o, cancel := c.begin(ctx)
	defer cancel()
	jwt, err := appJWT(app)
	if err != nil {
		return PermissionStatus{}, err
	}
	var v struct {
		ID    int64  `json:"id"`
		Slug  string `json:"slug"`
		Owner struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"owner"`
		Permissions map[string]string `json:"permissions"`
	}
	if _, err := o.request("GET", "/app", jwt, nil, &v, 200); err != nil {
		return PermissionStatus{}, err
	}
	if v.ID != app.ID || v.Slug != app.Slug {
		return PermissionStatus{}, errors.New("GitHub App identity changed; reconnect the App")
	}
	if !permissionOwner.MatchString(v.Owner.Login) || strings.Contains(v.Owner.Login, "--") || (v.Owner.Type != "User" && v.Owner.Type != "Organization") {
		return PermissionStatus{}, errors.New("GitHub returned an invalid App owner")
	}
	settingsURL := "https://github.com/settings/apps/" + v.Slug + "/permissions"
	if v.Owner.Type == "Organization" {
		settingsURL = "https://github.com/organizations/" + v.Owner.Login + "/settings/apps/" + v.Slug + "/permissions"
	}
	return PermissionStatus{VariablesWrite: v.Permissions["actions_variables"] == "write", SettingsURL: settingsURL}, nil
}
