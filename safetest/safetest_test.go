package safetest

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestFindBareGo(t *testing.T) {
	root := filepath.Join("testdata", "mod")

	found, err := FindBareGo(root, "allowed")
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || !strings.HasSuffix(found[0], filepath.Join("bad", "bad.go")+":4") {
		t.Fatalf("FindBareGo = %v, want only bad/bad.go:4", found)
	}

	found, err = FindBareGo(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 2 {
		t.Fatalf("FindBareGo without skip = %v, want 2 hits", found)
	}
}
