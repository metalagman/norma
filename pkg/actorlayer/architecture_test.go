package actorlayer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCorePackageHasNoADKDependencies(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.) error = %v", err)
	}

	forbidden := []string{
		"google.golang.org/adk",
		"google.golang.org/genai",
		"github.com/normahq/norma/pkg/actoradapter/",
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		content, readErr := os.ReadFile(filepath.Clean(name))
		if readErr != nil {
			t.Fatalf("ReadFile(%q) error = %v", name, readErr)
		}
		text := string(content)
		for _, pattern := range forbidden {
			if strings.Contains(text, pattern) {
				t.Fatalf("%s must not depend on %q", name, pattern)
			}
		}
	}
}
