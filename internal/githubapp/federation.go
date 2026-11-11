package githubapp

import (
	"context"
	"errors"
	"strings"
)

// FederationSubject reads the current repository OIDC configuration; immutable
// owner/repository identifiers must come from GitHub, never local guesses.
func (c *Client) FederationSubject(ctx context.Context, app App, repo Repository) (string, error) {
	if !validRepository(repo) {
		return "", errors.New("Invalid GitHub repository for OIDC guidance")
	}
	jwt, err := appJWT(app)
	if err != nil {
		return "", err
	}
	o, cancel := c.begin(ctx)
	defer cancel()
	token, err := o.installationToken(jwt, repo.InstallationID, []int64{repo.ID}, map[string]string{"actions": "read"})
	if err != nil {
		return "", errors.New("Cannot read GitHub OIDC settings. Grant Actions read access to this repository's App installation and retry.")
	}
	var config struct {
		UseDefault          *bool  `json:"use_default"`
		UseImmutableSubject bool   `json:"use_immutable_subject"`
		Prefix              string `json:"sub_claim_prefix"`
	}
	if _, err = o.request("GET", "/repos/"+repo.FullName+"/actions/oidc/customization/sub", token, nil, &config, 200); err != nil {
		return "", errors.New("Cannot read GitHub OIDC customization. Check Actions read access for this repository and retry.")
	}
	if config.UseDefault == nil || !*config.UseDefault {
		return "", errors.New("Custom GitHub OIDC subject claims are not supported by this wizard. Review repository/organization OIDC customization and restore the default subject template before continuing.")
	}
	prefix := config.Prefix
	if prefix == "" {
		if config.UseImmutableSubject {
			return "", errors.New("GitHub did not return the immutable OIDC subject prefix. Refresh repository OIDC settings; do not substitute repository IDs manually.")
		}
		prefix = "repo:" + repo.FullName
	}
	if !strings.HasPrefix(prefix, "repo:") || len(prefix) > 1024 || strings.IndexFunc(prefix, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return "", errors.New("GitHub returned an unsupported OIDC subject prefix. Review repository OIDC settings.")
	}
	return prefix + ":ref:" + strings.ReplaceAll("refs/heads/"+repo.DefaultBranch, ":", "%3A"), nil
}
