package githubapp

import (
	"context"
	"errors"
)

// RemovePublisherSecret deletes only the repository's Services publisher secret.
// Callers must establish ownership and prevent concurrent reconfiguration. A
// successful delete response is not sufficient: exact-name readback must prove
// absence, including when GitHub reports the secret was already missing.
func (c *Client) RemovePublisherSecret(ctx context.Context, app App, repository Repository) error {
	o, cancel := c.begin(ctx)
	defer cancel()
	token, err := o.repositoryToken(app, repository, map[string]string{"metadata": "read", "secrets": "write"})
	if err != nil {
		return err
	}
	base := "/repos/" + repository.FullName + "/actions"
	if _, err = o.request("DELETE", base+"/secrets/"+secretName, token, nil, nil, 204, 404); err != nil {
		return err
	}
	exists, err := o.secretExists(base, token)
	if err != nil {
		return err
	}
	if exists {
		return errors.New("GitHub publisher secret removal readback was not absent")
	}
	return nil
}
