package githubapp

import (
	"os"
	"testing"
)

func TestEmbeddedTemplatesMatchHardenedRepositoryFiles(t *testing.T) {
	for _, entry := range []struct{ path, template string }{{senderPath, senderTemplate}, {callbackPath, callbackTemplate}, {connectionPath, connectionTemplate}} {
		data, err := os.ReadFile("../../" + entry.path)
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != entry.template {
			t.Errorf("embedded template diverges from %s", entry.path)
		}
	}
}
