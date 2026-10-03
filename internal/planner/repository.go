package planner

import (
	"errors"
	"path"

	"github.com/djtouchette/witness/internal/selector"
)

// WholeRepository enumerates owning manifests rather than choosing a dominant
// language. A manifest with no derivable suite is reported as incomplete.
func WholeRepository(root string) (*Plan, error) {
	r := resolver{root: root}
	files := []string{}
	err := r.walkManifests(func(f string) error {
		switch path.Base(f) {
		case "go.mod", "Cargo.toml", "package.json":
			files = append(files, f)
		}
		switch path.Ext(f) {
		case ".csproj", ".fsproj", ".vbproj":
			files = append(files, f)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, errors.New("no supported manifests; use Plan with changed files and suite mappings")
	}
	return Build(root, &selector.SelectResult{ChangedFiles: files})
}
