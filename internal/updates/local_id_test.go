package updates

import "testing"

func TestLocalImageIDIsNotARegistryTag(t *testing.T) {
	if _, err := parseReference("sha256:0123456789abcdef"); err == nil {
		t.Fatal("local image ID was treated as a mutable Docker Hub tag")
	}
}
