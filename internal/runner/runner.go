package runner

import (
	"path/filepath"
	"strings"
)

// FormatCommand builds a test runner command for the given framework and test paths.
func FormatCommand(framework string, testPaths []string) string {
	if len(testPaths) == 0 {
		return ""
	}

	switch strings.ToLower(framework) {
	case "elixir", "phoenix":
		return "mix test " + strings.Join(testPaths, " ")
	case "go":
		return "go test " + formatGoPackages(testPaths)
	case "python", "django", "flask", "fastapi":
		return "pytest " + strings.Join(testPaths, " ")
	case "ruby", "rails":
		return "bundle exec rspec " + strings.Join(testPaths, " ")
	case "node", "express", "next.js", "react", "vue":
		return "npx jest " + strings.Join(testPaths, " ")
	case "rust":
		return "cargo test " + strings.Join(testPaths, " ")
	default:
		// Generic fallback: just list the paths.
		return strings.Join(testPaths, " ")
	}
}

// DetectFramework returns a framework name from recon's overview data.
func DetectFramework(frameworks []string) string {
	for _, f := range frameworks {
		lower := strings.ToLower(f)
		switch {
		case strings.Contains(lower, "phoenix"):
			return "elixir"
		case strings.Contains(lower, "gin") || strings.Contains(lower, "echo") || strings.Contains(lower, "fiber"):
			return "go"
		case strings.Contains(lower, "django") || strings.Contains(lower, "flask") || strings.Contains(lower, "fastapi"):
			return "python"
		case strings.Contains(lower, "rails"):
			return "ruby"
		case strings.Contains(lower, "react") || strings.Contains(lower, "next") || strings.Contains(lower, "express") || strings.Contains(lower, "vue"):
			return "node"
		}
	}
	return ""
}

// formatGoPackages converts test file paths to Go package paths.
func formatGoPackages(paths []string) string {
	pkgs := make(map[string]bool)
	for _, p := range paths {
		dir := filepath.Dir(p)
		pkgs["./" + dir + "/..."] = true
	}
	var result []string
	for pkg := range pkgs {
		result = append(result, pkg)
	}
	return strings.Join(result, " ")
}
