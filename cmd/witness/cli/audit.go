package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	witness "github.com/djtouchette/witness/pkg/witness"
	"github.com/spf13/cobra"
)

type auditCase struct {
	Name             string   `json:"name"`
	Root             string   `json:"root"`
	Files            []string `json:"files"`
	ExpectedTests    []string `json:"expected_tests"`
	ExpectedCommands []struct {
		Cwd  string   `json:"cwd"`
		Argv []string `json:"argv"`
	} `json:"expected_commands"`
	ExpectedStatus       string   `json:"expected_status"`
	ExpectedCoverage     string   `json:"expected_coverage"`
	ExpectedDiagnostics  []string `json:"expected_diagnostics"`
	ExpectedCommandCount *int     `json:"expected_command_count"`
}
type auditResult struct {
	Name     string        `json:"name"`
	ColdMS   int64         `json:"cold_ms"`
	WarmMS   int64         `json:"warm_ms"`
	Pass     bool          `json:"pass"`
	Failures []string      `json:"failures"`
	Plan     *witness.Plan `json:"plan,omitempty"`
}

func newAuditCmd() *cobra.Command {
	var root string
	cmd := &cobra.Command{Use: "audit cases.json", Short: "Check repeatable selection/plan expectations with temporary indexes; never execute tests", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		b, err := os.ReadFile(args[0])
		if err != nil {
			return err
		}
		var cases []auditCase
		if err = json.Unmarshal(b, &cases); err != nil {
			return err
		}
		cache, err := os.MkdirTemp("", "witness-audit-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(cache)
		results := []auditResult{}
		failed := false
		for i, c := range cases {
			row := auditResult{Name: c.Name, Pass: true, Failures: []string{}}
			repo := c.Root
			if !filepath.IsAbs(repo) {
				repo = filepath.Join(root, repo)
			}
			for pass := 0; pass < 2; pass++ {
				start := time.Now()
				w, e := witness.New(repo, witness.WithCacheDir(filepath.Join(cache, fmt.Sprint(i))))
				if e != nil {
					row.Failures = append(row.Failures, e.Error())
					break
				}
				sel, e := w.Select(c.Files, witness.DefaultOptions())
				if e == nil {
					row.Plan, e = w.Plan(sel)
				}
				w.Close()
				if pass == 0 {
					row.ColdMS = time.Since(start).Milliseconds()
				} else {
					row.WarmMS = time.Since(start).Milliseconds()
				}
				if e != nil && (row.Plan == nil || c.ExpectedStatus != "incomplete") {
					row.Failures = append(row.Failures, e.Error())
				}
			}
			if p := row.Plan; p != nil {
				want := c.ExpectedStatus
				if want == "" {
					want = "ready"
				}
				if p.Status != want {
					row.Failures = append(row.Failures, "status: "+p.Status+", want "+want)
				}
				if c.ExpectedCoverage != "" && p.Coverage != c.ExpectedCoverage {
					row.Failures = append(row.Failures, "coverage: "+p.Coverage+", want "+c.ExpectedCoverage)
				}
				if c.ExpectedCommandCount != nil && len(p.Commands) != *c.ExpectedCommandCount {
					row.Failures = append(row.Failures, fmt.Sprintf("command count: %d, want %d", len(p.Commands), *c.ExpectedCommandCount))
				}
				for _, want := range c.ExpectedDiagnostics {
					if !strings.Contains(strings.Join(p.Diagnostics, "\n"), want) {
						row.Failures = append(row.Failures, "missing diagnostic: "+want)
					}
				}
				for _, test := range c.ExpectedTests {
					found := false
					for _, got := range p.Selection.Tests {
						if got.Path == test {
							found = true
						}
					}
					if !found {
						row.Failures = append(row.Failures, "missing selected test: "+test)
					}
				}
				for _, want := range c.ExpectedCommands {
					found := false
					for _, got := range p.Commands {
						if got.Cwd == want.Cwd && reflect.DeepEqual(got.Argv, want.Argv) {
							found = true
						}
					}
					if !found {
						row.Failures = append(row.Failures, fmt.Sprintf("missing command: cwd=%s argv=%v", want.Cwd, want.Argv))
					}
				}
				for _, got := range p.Commands {
					if len(got.Argv) == 0 || got.Cwd == "" {
						row.Failures = append(row.Failures, "invalid command shape")
					}
				}
			} else {
				row.Failures = append(row.Failures, "no plan")
			}
			row.Pass = len(row.Failures) == 0
			failed = failed || !row.Pass
			results = append(results, row)
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		if err = enc.Encode(results); err != nil {
			return err
		}
		if failed {
			return fmt.Errorf("witness audit expectations failed")
		}
		return nil
	}}
	cmd.Flags().StringVar(&root, "root", ".", "base directory for case repository roots")
	return cmd
}
