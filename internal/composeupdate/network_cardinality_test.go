package composeupdate

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"services/internal/groups"
)

func TestAffectedDesiredCardinalityRejectedBeforeMutation(t *testing.T) {
	for _, service := range []string{"vpn", "services", "child"} {
		for _, declaration := range []string{`"scale":0`, `"scale":2`, `"deploy":{"replicas":0}`, `"deploy":{"replicas":2}`, `"scale":1,"deploy":{"replicas":2}`, `"scale":2,"deploy":{"replicas":1}`} {
			t.Run(service+"/"+declaration, func(t *testing.T) {
				u, members, r, ctx, events, _ := networkFixture(t)
				r.config = strings.Replace(r.config, `"image":"`+service+`:latest"`, `"image":"`+service+`:latest",`+declaration, 1)
				err := u.UpdateProjectVerified(ctx, members, localImage, func(groups.Container, string) error { return nil })
				if err == nil || !strings.Contains(err.Error(), "replica") {
					t.Fatalf("expected replica rejection, got %v", err)
				}
				if len(r.calls) != 0 || len(*events) != 0 {
					t.Fatalf("mutation before cardinality rejection: calls=%+v events=%v", r.calls, *events)
				}
			})
		}
	}
}

func TestDesiredSingletonDeclarationsAccepted(t *testing.T) {
	for _, declaration := range []string{`"scale":1`, `"deploy":{"replicas":1}`, `"scale":1,"deploy":{"replicas":1}`} {
		t.Run(declaration, func(t *testing.T) {
			u, members, r, ctx, _, _ := networkFixture(t)
			r.config = strings.Replace(r.config, `"image":"vpn:latest"`, `"image":"vpn:latest",`+declaration, 1)
			if err := u.UpdateProjectVerified(ctx, members, localImage, func(groups.Container, string) error { return nil }); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestReplacementCardinalityIsVerified(t *testing.T) {
	for _, scenario := range []string{"extra running", "extra stopped", "missing", "wrong identity", "wrong service", "wrong project", "invalid", "trailing invalid", "read failure", "stopped dependent extra"} {
		t.Run(scenario, func(t *testing.T) {
			u, members, r, ctx, _, _ := networkFixture(t)
			target := "vpn"
			if scenario == "stopped dependent extra" {
				target = "child"
				members[2].State = "exited"
			}
			r.psOutput = func(name string) ([]byte, error) {
				id := "old-" + name
				if r.replaced[name] {
					id = "new-" + name
				}
				row := fmt.Sprintf(`{"ID":%q,"Project":"shop","Service":%q,"State":"running"}`, id, name)
				if name != target {
					return []byte(row), nil
				}
				switch scenario {
				case "extra running", "extra stopped", "stopped dependent extra":
					state := "running"
					if scenario != "extra running" {
						state = "exited"
					}
					return []byte(row + "\n" + fmt.Sprintf(`{"ID":"unexpected","Project":"shop","Service":%q,"State":%q}`, name, state)), nil
				case "missing":
					return []byte(`[]`), nil
				case "wrong identity":
					return []byte(strings.Replace(row, id, "unexpected", 1)), nil
				case "wrong service":
					return []byte(strings.Replace(row, `"Service":"vpn"`, `"Service":"foreign"`, 1)), nil
				case "wrong project":
					return []byte(strings.Replace(row, "shop", "foreign", 1)), nil
				case "invalid":
					return []byte(`{"services":{}}`), nil
				case "trailing invalid":
					return []byte(row + "\nnot-json"), nil
				case "read failure":
					return nil, fmt.Errorf("private output must not leak")
				}
				return []byte(row), nil
			}
			err := u.UpdateProjectVerified(ctx, members, localImage, func(groups.Container, string) error { return nil })
			if err == nil {
				t.Fatal("unverified service cardinality succeeded")
			}
			if strings.Contains(err.Error(), "private output must not leak") {
				t.Fatal("private output leaked")
			}
			if scenario != "stopped dependent extra" && r.replaced["services"] {
				t.Fatal("consumer started after unverified provider")
			}
		})
	}
}

func TestCardinalitySupportsComposeJSONFormats(t *testing.T) {
	for _, array := range []bool{false, true} {
		t.Run(fmt.Sprint(array), func(t *testing.T) {
			u, members, r, ctx, _, _ := networkFixture(t)
			r.psOutput = func(name string) ([]byte, error) {
				row := fmt.Sprintf(`{"ID":"new-%s","Project":"shop","Service":%q}`, name, name)
				if array {
					row = "[" + row + "]"
				}
				return []byte(row), nil
			}
			if err := u.UpdateProjectVerified(ctx, members, localImage, func(groups.Container, string) error { return nil }); err != nil {
				t.Fatal(err)
			}
			if len(r.psCalls) == 0 {
				t.Fatal("service inventory never read")
			}
			for _, args := range r.psCalls {
				if !strings.Contains(strings.Join(args, " "), "ps --all --format json --no-trunc ") {
					t.Fatalf("inventory must include stopped containers and full IDs: %v", args)
				}
			}
		})
	}
}

func TestUnaffectedDesiredCardinalityDoesNotBlockUpdate(t *testing.T) {
	u, members, r, ctx, _, _ := networkFixture(t)
	var config map[string]map[string]map[string]any
	if err := json.Unmarshal([]byte(r.config), &config); err != nil {
		t.Fatal(err)
	}
	config["services"]["unrelated"]["scale"] = 2
	data, _ := json.Marshal(config)
	r.config = string(data)
	if err := u.UpdateProjectVerified(ctx, members, localImage, func(groups.Container, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
}
