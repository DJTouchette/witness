package runner

import (
	"path/filepath"
	"sort"
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
	case "node", "express", "next.js", "react", "vue", "typescript", "javascript":
		return "npx jest " + strings.Join(testPaths, " ")
	case "rust":
		return "cargo test " + strings.Join(testPaths, " ")
	case "csharp", "c#", ".net", "dotnet", "asp.net core", "xunit", "nunit", "mstest":
		return formatDotNetProjects(testPaths)
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
		case strings.Contains(lower, "asp.net") || strings.Contains(lower, ".net") ||
			strings.Contains(lower, "xunit") || strings.Contains(lower, "nunit") ||
			strings.Contains(lower, "mstest"):
			return "dotnet"
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
		pkgs["./"+dir+"/..."] = true
	}
	var result []string
	for pkg := range pkgs {
		result = append(result, pkg)
	}
	return strings.Join(result, " ")
}

func formatDotNetProjects(paths []string) string {
	projectDirs := make(map[string]bool)
	for _, p := range paths {
		dir := dotNetProjectDir(p)
		if dir == "." || dir == "" {
			continue
		}
		projectDirs["./"+filepath.ToSlash(dir)] = true
	}
	if len(projectDirs) == 0 {
		return "dotnet test"
	}

	var targets []string
	for dir := range projectDirs {
		targets = append(targets, dir)
	}
	sort.Strings(targets)

	commands := make([]string, 0, len(targets))
	for _, target := range targets {
		commands = append(commands, "dotnet test "+target)
	}
	return strings.Join(commands, " && ")
}

func dotNetProjectDir(path string) string {
	p := filepath.ToSlash(filepath.Clean(path))
	parts := strings.Split(p, "/")
	for i, part := range parts[:len(parts)-1] {
		lower := strings.ToLower(part)
		switch {
		case strings.HasSuffix(lower, ".tests"),
			strings.HasSuffix(lower, ".test"),
			strings.HasSuffix(lower, ".integrationtests"),
			strings.HasSuffix(lower, ".unittests"),
			strings.HasSuffix(lower, ".e2etests"),
			strings.HasSuffix(lower, ".e2e"):
			return strings.Join(parts[:i+1], "/")
		}
	}
	for i, part := range parts[:len(parts)-1] {
		lower := strings.ToLower(part)
		if lower == "test" || lower == "tests" {
			return strings.Join(parts[:i+1], "/")
		}
	}
	return filepath.Dir(p)
}
