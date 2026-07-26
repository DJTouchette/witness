// Package e2e holds witness's end-to-end tests: real fixture repositories with
// real git history and a real recon index, driven through the real CLI command
// tree.
//
// Every other test in the repository is a unit test against a fake RepoIntel,
// which is exactly why the failures that mattered survived — a language
// misclassified, a runner that cannot run, a command built from paths that do
// not exist. Those only show up when the whole pipeline runs: git diff ->
// recon -> selector -> runner -> stdout.
//
// The per-language answers are pinned with golden files under testdata/golden.
// Regenerate them with:
//
//	go test ./internal/e2e -update
//	WITNESS_UPDATE_GOLDEN=1 go test ./...
//
// and read the diff before committing it: a golden that changes silently is a
// behaviour change nobody reviewed.
//
// The package deliberately contains no non-test code beyond this file.
package e2e
