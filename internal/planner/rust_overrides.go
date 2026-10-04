package planner

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// cargoOverrides deliberately does not resolve Cargo versions, registries, lock
// files or feature activation. A patch can affect an unseen transitive registry
// dependency, so even an exact package-name match is insufficient to prune the
// graph. Local patches trigger all discovered Cargo suites. Unknown graph forms
// are errors, retaining commands as evidence but forbidding execution.
func (r *resolver) cargoOverrides() (bool, []error) {
	full := false
	var issues []error
	configs := map[string]bool{}
	dirs := make([]string, 0, len(r.crates))
	for dir := range r.crates {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		doc := r.crates[dir]
		manifest := path.Join(dir, "Cargo.toml")
		issues = append(issues, cargoUnknownKeys(doc.raw, manifest,
			"package workspace dependencies dev-dependencies build-dependencies target patch replace features lib bin example test bench profile badges lints")...)
		for _, key := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
			issues = append(issues, cargoCheckGroup(doc.raw[key], manifest+" ["+key+"]")...)
		}
		if ws, ok := doc.raw["workspace"].(map[string]any); ok {
			issues = append(issues, cargoUnknownKeys(ws, manifest+" [workspace]", "members default-members exclude resolver package dependencies metadata lints")...)
			issues = append(issues, cargoCheckGroup(ws["dependencies"], manifest+" [workspace.dependencies]")...)
		}
		if targets, ok := doc.raw["target"].(map[string]any); ok {
			for name, raw := range targets {
				group, ok := raw.(map[string]any)
				if !ok {
					issues = append(issues, fmt.Errorf("unsupported Cargo target in %s", manifest))
					continue
				}
				where := manifest + " [target." + name + "]"
				issues = append(issues, cargoUnknownKeys(group, where, "dependencies dev-dependencies build-dependencies")...)
				for key, deps := range group {
					issues = append(issues, cargoCheckGroup(deps, where+"."+key)...)
				}
			}
		}
		if _, ok := doc.raw["replace"]; ok {
			issues = append(issues, fmt.Errorf("unsupported Cargo [replace] override in %s; use local [patch] or explicit suite mappings", manifest))
		}
		if raw, exists := doc.raw["patch"]; exists {
			full = true
			sources, ok := raw.(map[string]any)
			if !ok {
				issues = append(issues, fmt.Errorf("unsupported Cargo patch table in %s", manifest))
			}
			for source, raw := range sources {
				where := manifest + " [patch." + source + "]"
				issues = append(issues, cargoCheckGroup(raw, where)...)
				entries, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				for name, raw := range entries {
					dep, ok := raw.(map[string]any)
					local, hasPath := dep["path"].(string)
					if !ok || !hasPath {
						issues = append(issues, fmt.Errorf("unsupported Cargo patch %s in %s: only local path overrides are resolved", name, where))
						continue
					}
					target, err := r.cargoDependency(dir, local)
					if err == nil {
						crate, found := r.crates[target]
						expected := name
						if alias, ok := dep["package"].(string); ok {
							expected = alias
						}
						if !found {
							err = fmt.Errorf("target is outside manifest scan scope or has no Cargo.toml")
						} else if crate.Package.Name != expected {
							err = fmt.Errorf("target package identity does not match %q", expected)
						}
					}
					if err != nil {
						issues = append(issues, fmt.Errorf("Cargo patch %s in %s: %w", name, where, err))
					}
				}
			}
		}
		// Discovery skips hidden directories. Inspect these specific configuration
		// files explicitly; never read Cargo credentials or ambient home config.
		for anc := dir; ; anc = path.Dir(anc) {
			for _, name := range []string{"config", "config.toml"} {
				configs[path.Join(anc, ".cargo", name)] = true
			}
			if anc == "." {
				break
			}
		}
	}
	for _, file := range sortedCargoKeys(configs) {
		_, err := os.Lstat(filepath.Join(r.root, filepath.FromSlash(file)))
		if os.IsNotExist(err) {
			continue
		}
		if err == nil {
			var b []byte
			b, err = r.read(file)
			if err == nil {
				var config map[string]any
				err = toml.Unmarshal(b, &config)
				if err == nil {
					// Unknown keys may introduce graph-changing behavior in future Cargo.
					issues = append(issues, cargoUnknownKeys(config, file, "alias build doc env future-incompat-report cache cargo-new http net registries registry resolver target term credential-alias paths patch source include unstable")...)
					for _, key := range []string{"paths", "patch", "source", "include", "unstable"} {
						if _, exists := config[key]; exists {
							issues = append(issues, fmt.Errorf("unsupported Cargo config override %q in %s; use explicit suite mappings", key, file))
						}
					}
				}
			}
		}
		if err != nil {
			issues = append(issues, fmt.Errorf("Cargo config %s: %w", file, err))
		}
	}
	// Map iteration must not change API diagnostics from run to run.
	sort.Slice(issues, func(i, j int) bool { return issues[i].Error() < issues[j].Error() })
	return full, issues
}

func sortedCargoKeys(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cargoUnknownKeys(m map[string]any, where, allowed string) []error {
	var issues []error
	for key := range m {
		if !slices.Contains(strings.Fields(allowed), key) {
			issues = append(issues, fmt.Errorf("unsupported Cargo declaration %q in %s; coverage cannot be established", key, where))
		}
	}
	return issues
}

func cargoCheckGroup(raw any, where string) []error {
	if raw == nil {
		return nil
	}
	group, ok := raw.(map[string]any)
	if !ok {
		return []error{fmt.Errorf("unsupported Cargo dependency group in %s", where)}
	}
	var issues []error
	for name, raw := range group {
		if _, ok := raw.(string); ok {
			continue
		}
		dep, ok := raw.(map[string]any)
		if !ok {
			issues = append(issues, fmt.Errorf("unsupported Cargo dependency %s in %s", name, where))
			continue
		}
		issues = append(issues, cargoUnknownKeys(dep, where+"."+name, "version path package registry registry-index git branch tag rev features optional default-features default_features workspace public")...)
		for key, value := range dep {
			valid := false
			switch key {
			case "workspace", "optional", "default-features", "default_features", "public":
				_, valid = value.(bool)
				if key == "workspace" && value == false {
					valid = false
				}
			case "features":
				list, ok := value.([]any)
				valid = ok
				for _, item := range list {
					if _, ok := item.(string); !ok {
						valid = false
					}
				}
			default:
				_, valid = value.(string)
			}
			if !valid {
				issues = append(issues, fmt.Errorf("unsupported Cargo dependency field %s in %s.%s", key, where, name))
			}
		}
	}
	return issues
}
