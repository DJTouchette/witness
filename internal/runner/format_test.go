package runner

import (
	"strings"
	"testing"
)

func TestFormatCommand_Go(t *testing.T) {
	// Go paths collapse to deduped package globs (./dir/...).
	got := FormatCommand("go", []string{"internal/a/x_test.go", "internal/a/y_test.go", "internal/b/z_test.go"})
	if !strings.HasPrefix(got, "go test ") {
		t.Fatalf("got %q, want a `go test` command", got)
	}
	if !strings.Contains(got, "./internal/a/...") || !strings.Contains(got, "./internal/b/...") {
		t.Errorf("missing package globs: %q", got)
	}
	// Two files in internal/a must collapse to a single package glob.
	if strings.Count(got, "./internal/a/...") != 1 {
		t.Errorf("duplicate package not deduped: %q", got)
	}
}

func TestFormatCommand_DefaultFallback(t *testing.T) {
	// Unknown framework just lists the paths.
	got := FormatCommand("cobol", []string{"a", "b"})
	if got != "a b" {
		t.Errorf("fallback = %q, want \"a b\"", got)
	}
}

func TestFormatCommand_EmptyPaths(t *testing.T) {
	if got := FormatCommand("go", nil); got != "" {
		t.Errorf("empty paths = %q, want empty", got)
	}
}

func TestDetectFramework_GoWebFrameworks(t *testing.T) {
	for _, fw := range []string{"Gin", "Echo", "Fiber"} {
		if got := DetectFramework([]string{fw}); got != "go" {
			t.Errorf("DetectFramework(%q) = %q, want go", fw, got)
		}
	}
	// FastAPI -> python (covers that branch).
	if got := DetectFramework([]string{"FastAPI"}); got != "python" {
		t.Errorf("FastAPI = %q, want python", got)
	}
	// First match wins across a mixed list.
	if got := DetectFramework([]string{"unknown", "Vue"}); got != "node" {
		t.Errorf("mixed list = %q, want node", got)
	}
}
