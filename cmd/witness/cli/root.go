package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/djtouchette/recon/pkg/recon"
	"github.com/djtouchette/witness/internal/gitdiff"
	"github.com/djtouchette/witness/internal/runner"
	"github.com/djtouchette/witness/internal/selector"
	"github.com/spf13/cobra"
)

// ExitCodeError carries a test-runner exit code out of `run` so the standalone
// main() can exit with it. The command itself must NOT call os.Exit: witness is
// embedded in-process by other tools (e.g. Rivet), where os.Exit would kill the
// host. main() translates this into os.Exit; embedded callers see a normal
// non-nil error.
type ExitCodeError struct{ Code int }

func (e *ExitCodeError) Error() string { return fmt.Sprintf("tests failed (exit code %d)", e.Code) }

// NewRootCmd creates the witness CLI command tree.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "witness",
		Short: "Test selector — find which tests to run for changed files",
		Long:  "Witness maps code changes to relevant tests using dependency analysis, co-change history, and hotspot scoring.",
		// run returns ExitCodeError to pass the runner's code up; don't let
		// cobra print it or dump usage for a plain test failure.
		SilenceErrors: true,
		SilenceUsage:  true,
	}

	root.Version = version
	root.AddCommand(newSelectCmd(), newRunCmd())

	return root
}

// selectFlags are the knobs shared by `select` and `run`.
type selectFlags struct {
	depth     int
	minScore  float64
	maxTests  int
	staged    bool
	since     string
	cacheDir  string
	coChange  int
	fanOutCap int
	exclude   []string
	kinds     []string
}

func (sf *selectFlags) bind(cmd *cobra.Command) {
	f := cmd.Flags()
	f.IntVar(&sf.depth, "depth", 2, "import graph traversal depth")
	f.Float64Var(&sf.minScore, "min-score", 0.1, "minimum relevance score")
	f.IntVar(&sf.maxTests, "max", 50, "max tests to return")
	f.BoolVar(&sf.staged, "staged", false, "use git diff --staged")
	f.StringVar(&sf.since, "since", "", "use git diff <ref>...HEAD")
	f.StringVar(&sf.cacheDir, "cache-dir", "", "recon cache directory")
	f.IntVar(&sf.coChange, "co-change-min", 2, "minimum co-change count to consider")
	f.IntVar(&sf.fanOutCap, "fan-out-cap", 100, "skip files with more importers than this")
	f.StringSliceVar(&sf.exclude, "exclude", nil, "glob patterns of test paths to drop (repeatable)")
	f.StringSliceVar(&sf.kinds, "kind", nil, "only return these test kinds: unit, integration, e2e, ... (repeatable)")
}

func (sf *selectFlags) options() selector.SelectOptions {
	return selector.SelectOptions{
		MaxDepth:         sf.depth,
		MinScore:         sf.minScore,
		MaxTests:         sf.maxTests,
		CoChangeMinCount: sf.coChange,
		FanOutCap:        sf.fanOutCap,
		Exclude:          sf.exclude,
		Kinds:            sf.kinds,
	}
}

// resolve opens recon and figures out the changed files (explicit args, or a
// git diff in the chosen mode). Caller must Close the returned recon.
func (sf *selectFlags) resolve(args []string) (*recon.Recon, string, []string, error) {
	root, err := os.Getwd()
	if err != nil {
		return nil, "", nil, fmt.Errorf("getting working directory: %w", err)
	}

	var reconOpts []recon.Option
	if sf.cacheDir != "" {
		reconOpts = append(reconOpts, recon.WithCacheDir(sf.cacheDir))
	}
	r, err := recon.New(root, reconOpts...)
	if err != nil {
		return nil, "", nil, fmt.Errorf("initializing recon: %w", err)
	}

	changed := args
	if len(changed) == 0 {
		mode := gitdiff.WorkingTree
		ref := ""
		switch {
		case sf.staged:
			mode = gitdiff.Staged
		case sf.since != "":
			mode = gitdiff.SinceRef
			ref = sf.since
		}
		changed, err = gitdiff.ChangedFiles(root, mode, ref)
		if err != nil {
			r.Close()
			return nil, "", nil, fmt.Errorf("detecting changes: %w", err)
		}
	}
	return r, root, changed, nil
}

func newSelectCmd() *cobra.Command {
	var sf selectFlags
	var format string

	cmd := &cobra.Command{
		Use:   "select [files...]",
		Short: "Select tests to run based on changed files",
		Long: `Given changed files, return a prioritized list of tests to run.

If no files are provided, uses git diff to detect changes.

Output formats:
  json   — structured JSON with scores and signals (default)
  paths  — one test path per line
  exec   — test runner command (auto-detected: mix test, go test, dotnet test, etc.)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, _, changedFiles, err := sf.resolve(args)
			if err != nil {
				return err
			}
			defer r.Close()

			if len(changedFiles) == 0 {
				if format == "json" {
					fmt.Println(`{"changed_files":[],"tests":[],"summary":{"changed":0,"tests_selected":0,"by_signal":{}}}`)
				} else {
					fmt.Fprintln(os.Stderr, "No changed files detected.")
				}
				return nil
			}

			result, err := selector.Select(r, changedFiles, sf.options())
			if err != nil {
				return err
			}

			switch format {
			case "paths":
				for _, t := range result.Tests {
					fmt.Println(t.Path)
				}
			case "exec":
				fmt.Println(commandFor(r, result))
			default:
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}
			return nil
		},
	}

	sf.bind(cmd)
	cmd.Flags().StringVar(&format, "format", "json", "output format: json, paths, exec")
	return cmd
}

func newRunCmd() *cobra.Command {
	var sf selectFlags

	cmd := &cobra.Command{
		Use:   "run [files...]",
		Short: "Select tests and run them",
		Long: `Select the relevant tests for the changed files and execute them.

Detects the test runner from the project (go test, mix test, pytest, dotnet test, ...),
streams its output, and exits with the runner's exit code — so it drops into
CI or a pre-commit hook directly. With no files, uses git diff like 'select'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			r, root, changedFiles, err := sf.resolve(args)
			if err != nil {
				return err
			}
			defer r.Close()

			out, errOut := cmd.OutOrStdout(), cmd.ErrOrStderr()

			if len(changedFiles) == 0 {
				fmt.Fprintln(errOut, "No changed files detected; nothing to run.")
				return nil
			}

			result, err := selector.Select(r, changedFiles, sf.options())
			if err != nil {
				return err
			}
			if len(result.Tests) == 0 {
				fmt.Fprintln(errOut, "No relevant tests selected; nothing to run.")
				return nil
			}

			command := commandFor(r, result)
			fmt.Fprintf(errOut, "witness: running %d test target(s)\n  $ %s\n\n", len(result.Tests), command)

			// Write through the command's writers so embedded callers capture
			// output instead of it leaking to the host's stdout.
			code, err := runner.Execute(command, root, out, errOut)
			if err != nil {
				return err
			}
			if code != 0 {
				return &ExitCodeError{Code: code}
			}
			return nil
		},
	}

	sf.bind(cmd)
	return cmd
}

// commandFor builds the runnable test command for a selection, detecting the
// framework from recon's overview (falling back to the primary language).
func commandFor(r *recon.Recon, result *selector.SelectResult) string {
	var paths []string
	for _, t := range result.Tests {
		paths = append(paths, t.Path)
	}

	framework := ""
	if overview, _ := r.Overview(); overview != nil {
		var fwNames []string
		for _, fw := range overview.Frameworks {
			fwNames = append(fwNames, fw.Name)
		}
		framework = runner.DetectFramework(fwNames)
		if framework == "" && len(overview.Languages) > 0 {
			framework = strings.ToLower(overview.Languages[0].Name)
		}
	}
	return runner.FormatCommand(framework, paths)
}
