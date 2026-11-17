package githubapp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

// ConfigurationVariable describes one reviewed publisher variable.
type ConfigurationVariable struct {
	Name         string `json:"name"`
	Exists       bool   `json:"exists"`
	CurrentValue string `json:"currentValue"`
	DesiredValue string `json:"desiredValue"`
}

// ConfigurationPlan reviews configuration only; secret plaintext is never read.
type ConfigurationPlan struct {
	Variables    []ConfigurationVariable `json:"variables"`
	SecretExists bool                    `json:"secretExists"`
	Fingerprint  string                  `json:"fingerprint"`
}

func configurationNames(variables map[string]string) ([]string, error) {
	if len(variables) != len(variableAllowlist) || variables[enabledName] != "false" {
		return nil, errors.New("invalid publisher repair variables")
	}
	names := make([]string, 0, len(variableAllowlist))
	for name, value := range variables {
		if !variableAllowlist[name] || len(value) > 48<<10 || strings.ContainsRune(value, '\x00') {
			return nil, errors.New("invalid publisher repair variable")
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// PlanConfiguration reads exactly the five publisher variables and token metadata.
// Minting a repository-scoped installation token does not mutate configuration.
func (c *Client) PlanConfiguration(ctx context.Context, app App, repo Repository, variables map[string]string) (ConfigurationPlan, error) {
	names, err := configurationNames(variables)
	if err != nil {
		return ConfigurationPlan{}, err
	}
	o, cancel := c.begin(ctx)
	defer cancel()
	token, err := o.repositoryToken(app, repo, map[string]string{"metadata": "read", "secrets": "read", "actions_variables": "read"})
	if err != nil {
		return ConfigurationPlan{}, err
	}
	return o.planConfiguration(app, repo, token, variables, names)
}

// ApplyConfigurationRepair explicitly replaces the reviewed publisher settings.
// GitHub has no conditional write API; outside writers can still race the repair.
// Secret metadata readback cannot authenticate plaintext: require a callback before
// enabling notifications or revoking a retained local credential.
func (c *Client) ApplyConfigurationRepair(ctx context.Context, app App, repo Repository, secret string, variables map[string]string, fingerprint string) error {
	if !validPublisherSecret(secret) {
		return errors.New("invalid publisher secret")
	}
	if len(fingerprint) != sha256.Size*2 {
		return errors.New("invalid publisher configuration fingerprint")
	}
	decoded, err := hex.DecodeString(fingerprint)
	if err != nil || len(decoded) != sha256.Size || fingerprint != strings.ToLower(fingerprint) {
		return errors.New("invalid publisher configuration fingerprint")
	}
	names, err := configurationNames(variables)
	if err != nil {
		return err
	}
	o, cancel := c.begin(ctx)
	defer cancel()
	token, err := o.repositoryToken(app, repo, map[string]string{"metadata": "read", "secrets": "write", "actions_variables": "write"})
	if err != nil {
		return err
	}
	plan, err := o.planConfiguration(app, repo, token, variables, names)
	if err != nil {
		return err
	}
	if plan.Fingerprint != fingerprint {
		return ErrConflict
	}
	base := "/repos/" + repo.FullName + "/actions"
	for _, v := range plan.Variables {
		if !v.Exists || v.CurrentValue != v.DesiredValue {
			method, path, status := "POST", base+"/variables", 201
			if v.Exists {
				method, path, status = "PATCH", base+"/variables/"+v.Name, 204
			}
			if _, err = o.request(method, path, token, variable{Name: v.Name, Value: v.DesiredValue}, nil, status); err != nil {
				return err
			}
		}
		if err = o.verifyVariable(base, token, v.Name, v.DesiredValue); err != nil {
			return err
		}
	}
	encrypted, err := o.encryptPublisherSecret(base, token, secret)
	if err != nil {
		return err
	}
	return o.writePublisherSecret(base, token, encrypted)
}

func (o *operation) planConfiguration(app App, repo Repository, token string, variables map[string]string, names []string) (ConfigurationPlan, error) {
	base := "/repos/" + repo.FullName + "/actions"
	plan := ConfigurationPlan{Variables: make([]ConfigurationVariable, 0, len(names))}
	// Include all metadata, not just existence: a secret replacement invalidates review.
	var metadata map[string]json.RawMessage
	status, err := o.request("GET", base+"/secrets/"+secretName, token, nil, &metadata, 200, 404)
	if err != nil {
		return ConfigurationPlan{}, err
	}
	plan.SecretExists = status == 200
	if plan.SecretExists {
		var name string
		if json.Unmarshal(metadata["name"], &name) != nil || name != secretName {
			return ConfigurationPlan{}, errors.New("GitHub returned invalid secret metadata")
		}
	} else {
		metadata = nil
	}
	for _, name := range names {
		v, exists, err := o.getVariable(base, token, name)
		if err != nil {
			return ConfigurationPlan{}, err
		}
		plan.Variables = append(plan.Variables, ConfigurationVariable{Name: name, Exists: exists, CurrentValue: v.Value, DesiredValue: variables[name]})
	}
	encoded, _ := json.Marshal(struct {
		Operation      string
		AppID          int64
		AppSlug        string
		Repository     Repository
		Variables      []ConfigurationVariable
		SecretExists   bool
		SecretMetadata map[string]json.RawMessage
	}{"configuration-repair", app.ID, app.Slug, repo, plan.Variables, plan.SecretExists, metadata})
	digest := sha256.Sum256(encoded)
	plan.Fingerprint = hex.EncodeToString(digest[:])
	return plan, nil
}
