package publishers

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPrivateLifecycleFieldsRoundTrip(t *testing.T) {
	state := Snapshot{Connections: []Connection{{ID: "fixture"}}}
	connection := reflect.ValueOf(&state.Connections[0]).Elem()
	for _, name := range []string{"PreviousKeyID", "PreviousSecret"} {
		field := connection.FieldByName(name)
		if !field.IsValid() || field.Kind() != reflect.String {
			t.Fatalf("missing private lifecycle field %s", name)
		}
		field.SetString("fixture-private-" + name)
	}
	receipt := connection.FieldByName("LastPublication")
	if !receipt.IsValid() || receipt.Kind() != reflect.Pointer {
		t.Fatal("missing private publication receipt")
	}
	receipt.Set(reflect.New(receipt.Type().Elem()))
	for _, name := range []string{"Version", "Digest", "Commit", "RunURL", "PublishedAt", "ReceivedAt", "Result"} {
		field := receipt.Elem().FieldByName(name)
		if !field.IsValid() || field.Kind() != reflect.String {
			t.Fatalf("missing receipt field %s", name)
		}
		field.SetString("fixture-" + name)
	}
	tags := receipt.Elem().FieldByName("Tags")
	if !tags.IsValid() || tags.Type() != reflect.TypeOf([]string{}) {
		t.Fatal("missing receipt tags")
	}
	tags.Set(reflect.ValueOf([]string{"fixture-tag"}))
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Save(state); err != nil {
		t.Fatal(err)
	}
	store.Close()
	raw, err := os.ReadFile(filepath.Join(dir, "publishers.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("fixture-private-")) {
		t.Fatal("previous credentials stored in plaintext")
	}
	store, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	restored, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(restored.Connections, state.Connections) {
		t.Fatal("private lifecycle fields lost")
	}
}

func TestProtectedStateSurvivesRestartWithoutPlaintextSecrets(t *testing.T) {
	dir := t.TempDir()
	store, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := Snapshot{App: App{ID: 42, Slug: "services-private", Name: "Services", PEM: "sensitive-private-key-marker"}, Tailnet: Tailnet{ClientID: "client", Audience: "audience", Tags: "tag:publisher"}, Connections: []Connection{{ID: "connection", Secret: "sensitive-publisher-token-marker", State: "configured"}}}
	if err = store.Save(state); err != nil {
		t.Fatal(err)
	}
	if err = store.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "publishers.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{state.App.PEM, state.Connections[0].Secret} {
		if bytes.Contains(raw, []byte(secret)) {
			t.Fatal("secret persisted without encryption")
		}
	}
	store, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	restored, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if restored.App.PEM != state.App.PEM || len(restored.Connections) != 1 || restored.Connections[0].Secret != state.Connections[0].Secret {
		t.Fatal("state lost across restart")
	}
}
