package operations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"services/internal/composeupdate"
	"services/internal/groups"
)

type namespaceCompose struct {
	t         *testing.T
	store     *Store
	operation string
	config    string
	replaced  map[string]bool
	stopped   []string
	fail      string
	commands  []string
}

func (r *namespaceCompose) Output(_ context.Context, _ string, args []string) ([]byte, error) {
	for _, arg := range args {
		if arg == "ps" {
			name := args[len(args)-1]
			id := "old-" + name
			if r.replaced[name] {
				id = "new-" + name
			}
			return json.Marshal(map[string]string{"ID": id, "Project": "self", "Service": name})
		}
	}
	return []byte(r.config), nil
}
func (r *namespaceCompose) Run(_ context.Context, _ string, args []string) error {
	command := ""
	for _, a := range args {
		if a == "pull" || a == "stop" || a == "up" {
			command = a
			break
		}
	}
	name := args[len(args)-1]
	r.commands = append(r.commands, command+":"+name)
	if command == "stop" || command == "up" {
		o, err := r.store.Get(r.operation)
		if err != nil {
			r.t.Fatal(err)
		}
		if o.Plan == nil || len(o.Plan.Services) != 2 || o.Plan.Services[0].OldID != "old-vpn" || o.Plan.Services[1].Reason != "network reattachment" {
			r.t.Fatalf("missing durable plan before destructive action: %+v", o)
		}
	}
	if command == "stop" {
		r.stopped = append(r.stopped, name)
	}
	if command == "up" {
		if r.fail == name {
			return fmt.Errorf("provider not ready")
		}
		r.replaced[name] = true
	}
	return nil
}

type namespaceChecker struct{}

func (namespaceChecker) CheckNow(_ context.Context, c []groups.Container) error {
	for i := range c {
		c[i].UpdateAvailable = c[i].Service == "vpn"
	}
	return nil
}
func (namespaceChecker) Apply([]groups.Container) {}

func TestDurableNetworkUpdateSurvivesSelfReattachment(t *testing.T) {
	for _, scenario := range []string{"success", "provider failure", "stale replacement namespace"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, "compose.yaml")
			if err := os.WriteFile(configPath, []byte("services: {}"), 0600); err != nil {
				t.Fatal(err)
			}
			store, err := Open(filepath.Join(t.TempDir(), "ops.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			op, err := store.Submit("self", "update", "old-vpn", "")
			if err != nil {
				t.Fatal(err)
			}
			store.Claim()
			runner := &namespaceCompose{t: t, store: store, operation: op.ID, replaced: map[string]bool{}, config: `{"services":{"vpn":{"image":"vpn:latest"},"services":{"image":"services:latest","network_mode":"service:vpn"}}}`}
			if scenario == "provider failure" {
				runner.fail = "vpn"
			}
			readyCalls, protocolCalls := 0, 0
			docker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if strings.HasPrefix(r.URL.Path, "/images/") {
					n := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/images/"), "/json")
					n = strings.TrimSuffix(n, ":latest")
					if n == "services" {
						protocolCalls++
					}
					json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:" + n})
					return
				}
				if strings.HasPrefix(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json") {
					ref := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/containers/"), "/json")
					n := strings.TrimPrefix(strings.TrimPrefix(ref, "old-"), "new-")
					id := ref
					if ref == n {
						id = "old-" + n
						if runner.replaced[n] {
							id = "new-" + n
						}
					}
					mode := "bridge"
					if n == "services" {
						mode = "container:old-vpn"
						if strings.HasPrefix(id, "new-") {
							mode = "container:new-vpn"
							if scenario == "stale replacement namespace" {
								mode = "container:old-vpn"
							}
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"Id": id, "Image": "sha256:" + n, "Name": "/" + n, "Config": map[string]any{"Labels": map[string]string{"com.docker.compose.project": "self", "com.docker.compose.service": n}}, "HostConfig": map[string]string{"NetworkMode": mode}, "State": map[string]any{"Running": true, "Status": "running"}})
					return
				}
				if r.URL.Path == "/containers/new-services/exec" {
					readyCalls++
					fmt.Fprint(w, `{"Id":"readiness"}`)
					return
				}
				if r.URL.Path == "/exec/readiness/start" {
					fmt.Fprint(w, "ready")
					return
				}
				if r.URL.Path == "/exec/readiness/json" {
					fmt.Fprint(w, `{"ExitCode":0}`)
					return
				}
				t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
				w.WriteHeader(500)
			}))
			defer docker.Close()
			members := []groups.Container{}
			for _, name := range []string{"vpn", "services"} {
				members = append(members, groups.Container{ID: "old-" + name, Name: name, Image: name + ":latest", Project: "self", Service: name, State: "running", ComposeWorkingDir: root, ComposeConfigFiles: []string{configPath}})
			}
			worker := Executor{Store: store, Source: &fixtureSource{members: members}, Checker: namespaceChecker{}, Updater: composeupdate.NewWithRunner(root, root, "/var/run/docker.sock", runner), Engine: NewEngine(docker.URL, docker.Client()), Project: "self", Service: "services"}
			err = worker.Execute(context.Background(), op.ID)
			got, e := store.Get(op.ID)
			if e != nil {
				t.Fatal(e)
			}
			if scenario == "provider failure" {
				if err == nil || !got.NeedsAttention || runner.replaced["services"] || readyCalls != 0 {
					t.Fatalf("unsafe provider failure %v %+v %v", err, got, runner.commands)
				}
			} else if scenario == "stale replacement namespace" {
				if err == nil || !got.NeedsAttention || got.State != "error" || readyCalls != 1 {
					t.Fatalf("stale namespace did not persist interruption: %v %+v", err, got)
				}
			} else {
				if err != nil || got.State != "complete" || readyCalls != 1 {
					t.Fatalf("execution %v %+v readiness=%d", err, got, readyCalls)
				}
			}
			// An unchanged Services tag is inspected for drift, but no protocol labels
			// are required. This fixture deliberately provides none.
			if protocolCalls == 0 {
				t.Fatal("unchanged image drift was not checked")
			}
			before := len(runner.commands)
			if worker.Execute(context.Background(), op.ID) == nil || len(runner.commands) != before {
				t.Fatal("execution replayed")
			}
		})
	}
}
