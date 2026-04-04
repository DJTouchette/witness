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

// NewRootCmd creates the witness CLI command tree.
func NewRootCmd(version string) *cobra.Command {
	root := &cobra.Command{
		Use:   "witness",
		Short: "Test selector — find which tests to run for changed files",
		Long:  "Witness maps code changes to relevant tests using dependency analysis, co-change history, and hotspot scoring.",
	}

	root.Version = version
	root.AddCommand(newSelectCmd())

	return root
}

func newSelectCmd() *cobra.Command {
	var (
		depth    int
		minScore float64
		maxTests int
		format   string
		staged   bool
		since    string
		cacheDir string
	)

	cmd := &cobra.Command{
		Use:   "select [files...]",
		Short: "Select tests to run based on changed files",
		Long: `Given changed files, return a prioritized list of tests to run.

If no files are provided, uses git diff to detect changes.

Output formats:
  json   — structured JSON with scores and signals (default)
  paths  — one test path per line
  exec   — test runner command (auto-detected: mix test, go test, etc.)`,
		RunE: func(cmd *cobra.Command, args []string) error {
			root, _ := os.Getwd()

			var reconOpts []recon.Option
			if cacheDir != "" {
				reconOpts = append(reconOpts, recon.WithCacheDir(cacheDir))
			}

			r, err := recon.New(root, reconOpts...)
			if err != nil {
				return fmt.Errorf("initializing recon: %w", err)
			}
			defer r.Close()

			// Determine changed files.
			changedFiles := args
			if len(changedFiles) == 0 {
				mode := gitdiff.WorkingTree
				ref := ""
				if staged {
					mode = gitdiff.Staged
				} else if since != "" {
					mode = gitdiff.SinceRef
					ref = since
				}
				changedFiles, err = gitdiff.ChangedFiles(root, mode, ref)
				if err != nil {
					return fmt.Errorf("detecting changes: %w", err)
				}
			}

			if len(changedFiles) == 0 {
				if format == "json" {
					fmt.Println(`{"changed_files":[],"tests":[],"summary":{"changed":0,"tests_selected":0,"by_signal":{}}}`)
				} else {
					fmt.Fprintln(os.Stderr, "No changed files detected.")
				}
				return nil
			}

			opts := selector.SelectOptions{
				MaxDepth: depth,
				MinScore: minScore,
				MaxTests: maxTests,
			}

			result, err := selector.Select(r, changedFiles, opts)
			if err != nil {
				return err
			}

			switch format {
			case "paths":
				for _, t := range result.Tests {
					fmt.Println(t.Path)
				}
			case "exec":
				var paths []string
				for _, t := range result.Tests {
					paths = append(paths, t.Path)
				}
				overview, _ := r.Overview()
				framework := ""
				if overview != nil {
					var fwNames []string
					for _, fw := range overview.Frameworks {
						fwNames = append(fwNames, fw.Name)
					}
					framework = runner.DetectFramework(fwNames)
					if framework == "" && len(overview.Languages) > 0 {
						framework = strings.ToLower(overview.Languages[0].Name)
					}
				}
				command := runner.FormatCommand(framework, paths)
				fmt.Println(command)
			default:
				enc := json.NewEncoder(os.Stdout)
				enc.SetIndent("", "  ")
				return enc.Encode(result)
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&depth, "depth", 2, "import graph traversal depth")
	cmd.Flags().Float64Var(&minScore, "min-score", 0.1, "minimum relevance score")
	cmd.Flags().IntVar(&maxTests, "max", 50, "max tests to return")
	cmd.Flags().StringVar(&format, "format", "json", "output format: json, paths, exec")
	cmd.Flags().BoolVar(&staged, "staged", false, "use git diff --staged")
	cmd.Flags().StringVar(&since, "since", "", "use git diff <ref>...HEAD")
	cmd.Flags().StringVar(&cacheDir, "cache-dir", "", "recon cache directory")

	return cmd
}
