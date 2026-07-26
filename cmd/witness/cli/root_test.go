package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/djtouchette/witness/internal/runner"
	"github.com/djtouchette/witness/internal/selector"
	"github.com/spf13/cobra"
)

func TestRootCommandWiring(t *testing.T) {
	root := NewRootCmd("v-test")
	if root.Version != "v-test" {
		t.Errorf("version = %q, want v-test", root.Version)
	}

	want := map[string]bool{"select": false, "run": false}
	for _, c := range root.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("subcommand %q not registered", name)
		}
	}
}

// --version must exist and, like every other output, must go to the command's
// writer rather than the host's stdout.
func TestVersionFlagWritesToCommandWriter(t *testing.T) {
	root := NewRootCmd("v1.2.3")

	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatalf("--version: %v", err)
	}
	// Cobra registers --version lazily, during Execute.
	if root.Flags().Lookup("version") == nil {
		t.Error("root has no --version flag")
	}
	if !strings.Contains(out.String(), "v1.2.3") {
		t.Errorf("--version output = %q, want it to contain v1.2.3", out.String())
	}
}

func TestSelectAndRunShareFlags(t *testing.T) {
	root := NewRootCmd("v")
	shared := []string{
		"depth", "min-score", "max", "staged", "since", "co-change-min",
		"fan-out-cap", "exclude", "kind", "fallback", "test-cmd", "runner",
	}
	for _, name := range []string{"select", "run"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatalf("find %q: %v", name, err)
		}
		for _, flag := range shared {
			if cmd.Flags().Lookup(flag) == nil {
				t.Errorf("%s is missing --%s", name, flag)
			}
		}
	}

	// `select` has --format; `run` has --timeout. Neither has the other's.
	sel, _, _ := root.Find([]string{"select"})
	if sel.Flags().Lookup("format") == nil {
		t.Error("select should have --format")
	}
	if sel.Flags().Lookup("timeout") != nil {
		t.Error("select should not have --timeout")
	}
	run, _, _ := root.Find([]string{"run"})
	if run.Flags().Lookup("format") != nil {
		t.Error("run should not have --format")
	}
	if run.Flags().Lookup("timeout") == nil {
		t.Error("run should have --timeout")
	}
}

// options() is a pure mapping that nothing else pins: transposing two fields
// compiles, vets and passes every other test in the package.
func TestSelectFlagsOptionsMapping(t *testing.T) {
	sf := selectFlags{
		depth:     7,
		minScore:  0.42,
		maxTests:  13,
		coChange:  5,
		fanOutCap: 91,
		exclude:   []string{"vendor/**"},
		kinds:     []string{"e2e"},
	}
	got := sf.options()
	want := selector.SelectOptions{
		MaxDepth:         7,
		MinScore:         0.42,
		MaxTests:         13,
		CoChangeMinCount: 5,
		FanOutCap:        91,
		Exclude:          []string{"vendor/**"},
		Kinds:            []string{"e2e"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("options() = %+v, want %+v", got, want)
	}
}

// An explicitly-typed 0 must not be swallowed by selector.Select's zero-value
// backfill, and must widen the selection rather than empty it.
func TestSelectFlagsExplicitZeroWidens(t *testing.T) {
	tests := []struct {
		name  string
		flag  string
		apply func(*selectFlags)
		check func(*testing.T, selector.SelectOptions)
	}{
		{"depth", "depth", func(sf *selectFlags) { sf.depth = 0 }, func(t *testing.T, o selector.SelectOptions) {
			if o.MaxDepth >= 0 {
				t.Errorf("MaxDepth = %d, want negative so no traversal happens", o.MaxDepth)
			}
		}},
		{"min-score", "min-score", func(sf *selectFlags) { sf.minScore = 0 }, func(t *testing.T, o selector.SelectOptions) {
			if o.MinScore <= 0 || o.MinScore >= 0.1 {
				t.Errorf("MinScore = %v, want a positive value below the 0.1 default", o.MinScore)
			}
		}},
		{"max", "max", func(sf *selectFlags) { sf.maxTests = 0 }, func(t *testing.T, o selector.SelectOptions) {
			if o.MaxTests != math.MaxInt {
				t.Errorf("MaxTests = %d, want no cap", o.MaxTests)
			}
		}},
		{"co-change-min", "co-change-min", func(sf *selectFlags) { sf.coChange = 0 }, func(t *testing.T, o selector.SelectOptions) {
			if o.CoChangeMinCount != 1 {
				t.Errorf("CoChangeMinCount = %d, want 1", o.CoChangeMinCount)
			}
		}},
		{"fan-out-cap", "fan-out-cap", func(sf *selectFlags) { sf.fanOutCap = 0 }, func(t *testing.T, o selector.SelectOptions) {
			if o.FanOutCap != math.MaxInt {
				t.Errorf("FanOutCap = %d, want no cap", o.FanOutCap)
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var sf selectFlags
			tc.apply(&sf)
			sf.explicit = map[string]bool{tc.flag: true}
			tc.check(t, sf.options())
		})
	}
}

// Parsing real argv is the only way to catch a flag bound to the wrong field.
func TestParseArgvWiresOptions(t *testing.T) {
	sf, _ := parseSelectFlags(t, "--depth", "3", "--max", "7", "--min-score", "0.6",
		"--co-change-min", "9", "--fan-out-cap", "11",
		"--exclude", "x/**", "--kind", "e2e", "--runner", "go test -race")

	got := sf.options()
	want := selector.SelectOptions{
		MaxDepth:         3,
		MinScore:         0.6,
		MaxTests:         7,
		CoChangeMinCount: 9,
		FanOutCap:        11,
		Exclude:          []string{"x/**"},
		Kinds:            []string{"e2e"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("options() = %+v, want %+v", got, want)
	}
	if sf.testCmd != "go test -race" {
		t.Errorf("--runner did not fold into testCmd: %q", sf.testCmd)
	}
}

func TestParseArgvExplicitZero(t *testing.T) {
	sf, _ := parseSelectFlags(t, "--max", "0")
	if !sf.explicit["max"] {
		t.Fatal("--max not recorded as explicitly set")
	}
	if got := sf.options().MaxTests; got != math.MaxInt {
		t.Errorf("MaxTests = %d, want no cap for an explicit --max 0", got)
	}
}

// parseSelectFlags binds selectFlags to a throwaway command, parses argv and
// runs the same capture step PreRunE does.
func parseSelectFlags(t *testing.T, argv ...string) (*selectFlags, *cobra.Command) {
	t.Helper()
	var sf selectFlags
	cmd := &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return nil }}
	sf.bind(cmd)
	if err := cmd.Flags().Parse(argv); err != nil {
		t.Fatalf("parse %v: %v", argv, err)
	}
	sf.capture(cmd)
	return &sf, cmd
}

func TestStagedAndSinceAreMutuallyExclusive(t *testing.T) {
	for _, name := range []string{"select", "run"} {
		t.Run(name, func(t *testing.T) {
			root := NewRootCmd("v")
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs([]string{name, "--staged", "--since", "HEAD~1"})
			err := root.Execute()
			if err == nil {
				t.Fatal("--staged --since together should be an error, not a silent drop of --since")
			}
			if !strings.Contains(err.Error(), "staged") || !strings.Contains(err.Error(), "since") {
				t.Errorf("error = %v, want it to name both flags", err)
			}
		})
	}
}

func TestUnknownFormatIsRejected(t *testing.T) {
	root := NewRootCmd("v")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"select", "--format", "path"})
	err := root.Execute()
	if err == nil {
		t.Fatal("--format path should be rejected, not silently treated as json")
	}
	if !strings.Contains(err.Error(), "unknown --format") {
		t.Errorf("error = %v, want an unknown --format message", err)
	}
	if out.Len() != 0 {
		t.Errorf("nothing should be printed for an invalid format, got %q", out.String())
	}
}

func TestUnknownFallbackIsRejected(t *testing.T) {
	root := NewRootCmd("v")
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs([]string{"run", "--fallback", "maybe"})
	err := root.Execute()
	if err == nil {
		t.Fatal("--fallback maybe should be rejected")
	}
	if !strings.Contains(err.Error(), "unknown --fallback") {
		t.Errorf("error = %v, want an unknown --fallback message", err)
	}
}

func TestSplitPassthrough(t *testing.T) {
	tests := []struct {
		name     string
		argv     []string
		wantFile []string
		wantPass []string
	}{
		{"no dash", []string{"a.go", "b.go"}, []string{"a.go", "b.go"}, nil},
		{"only passthrough", []string{"--", "-race"}, []string{}, []string{"-race"}},
		{"both", []string{"a.go", "--", "-race", "-v"}, []string{"a.go"}, []string{"-race", "-v"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cmd := &cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error { return nil }}
			if err := cmd.Flags().Parse(tc.argv); err != nil {
				t.Fatalf("parse: %v", err)
			}
			files, pass := splitPassthrough(cmd, cmd.Flags().Args())
			if !reflect.DeepEqual(files, tc.wantFile) && !(len(files) == 0 && len(tc.wantFile) == 0) {
				t.Errorf("files = %q, want %q", files, tc.wantFile)
			}
			if !reflect.DeepEqual(pass, tc.wantPass) && !(len(pass) == 0 && len(tc.wantPass) == 0) {
				t.Errorf("passthrough = %q, want %q", pass, tc.wantPass)
			}
		})
	}
}

func TestSplitCommand(t *testing.T) {
	tests := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{"go test", []string{"go", "test"}, false},
		{"go test -race", []string{"go", "test", "-race"}, false},
		{`mix test --only "tag with space"`, []string{"mix", "test", "--only", "tag with space"}, false},
		{`pytest -k 'a or b'`, []string{"pytest", "-k", "a or b"}, false},
		{`echo ""`, []string{"echo", ""}, false},
		{`go   test`, []string{"go", "test"}, false},
		{"", nil, false},
		{`go "test`, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := splitCommand(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("splitCommand(%q) = %q, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("splitCommand(%q): %v", tc.in, err)
			}
			if !reflect.DeepEqual(got, tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
				t.Errorf("splitCommand(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestUnprovenReasons(t *testing.T) {
	tests := []struct {
		name            string
		result          *selector.SelectResult
		deleted         []string
		requireCoverage bool
		want            bool
	}{
		{
			name: "uncovered file alongside a real selection is proven",
			result: &selector.SelectResult{
				Tests:   []selector.ScoredTest{{Path: "a_test.go"}},
				Summary: selector.Summary{Unmapped: []string{"README.md"}},
			},
			want: false,
		},
		{
			name:   "fully covered selection is proven",
			result: &selector.SelectResult{Tests: []selector.ScoredTest{{Path: "a_test.go"}}},
			want:   false,
		},
		{
			name:   "empty selection with uncovered files is not proven",
			result: &selector.SelectResult{Summary: selector.Summary{Unmapped: []string{"go.mod"}}},
			want:   true,
		},
		{
			name:   "no changed files at all is proven",
			result: &selector.SelectResult{},
			want:   false,
		},
		{
			name:   "stale index is not proven",
			result: &selector.SelectResult{Summary: selector.Summary{NotIndexed: []string{"a.go"}}},
			want:   true,
		},
		{
			name:   "recon error is not proven",
			result: &selector.SelectResult{Summary: selector.Summary{AnalysisError: "recon Tests(a.go): boom"}},
			want:   true,
		},
		{
			name:    "deletion is not proven",
			result:  &selector.SelectResult{},
			deleted: []string{"gone.go"},
			want:    true,
		},
		{
			// The common PR shape: a dependency bump alongside a source edit.
			// The default reading has teeth (tests ran), --require-coverage is
			// for a gate that wants every changed file accounted for.
			name: "uncovered file alongside a selection is a gap under --require-coverage",
			result: &selector.SelectResult{
				Tests:   []selector.ScoredTest{{Path: "a_test.go"}},
				Summary: selector.Summary{Unmapped: []string{"go.mod"}},
			},
			requireCoverage: true,
			want:            true,
		},
		{
			name:            "--require-coverage is satisfied by a fully covered selection",
			result:          &selector.SelectResult{Tests: []selector.ScoredTest{{Path: "a_test.go"}}},
			requireCoverage: true,
			want:            false,
		},
		{
			// The caller's own --kind/--exclude/--signals emptied the list.
			// Widening to the whole suite there defeats the flag they typed.
			name: "a selection the caller filtered to empty is not a gap",
			result: &selector.SelectResult{
				Summary: selector.Summary{Unmapped: []string{"calc.go"}, Filtered: 3},
			},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := unprovenReasons(tc.result, tc.deleted, tc.requireCoverage)
			if (len(got) > 0) != tc.want {
				t.Errorf("unprovenReasons = %q, want unproven=%v", got, tc.want)
			}
		})
	}
}

func TestApplyFallback(t *testing.T) {
	gaps := []string{"stale index"}

	if useFull, err := applyFallback(io.Discard, fallbackFull, nil); useFull || err != nil {
		t.Errorf("no gaps: got (%v, %v), want (false, nil)", useFull, err)
	}

	var warn bytes.Buffer
	useFull, err := applyFallback(&warn, fallbackFull, gaps)
	if !useFull || err != nil {
		t.Errorf("full: got (%v, %v), want (true, nil)", useFull, err)
	}
	if !strings.Contains(warn.String(), "full suite") {
		t.Errorf("full: warning = %q, want it to mention the full suite", warn.String())
	}

	if _, err := applyFallback(io.Discard, fallbackFail, gaps); err == nil {
		t.Error("fail: want a non-nil error so the gate cannot pass")
	}

	warn.Reset()
	if useFull, err := applyFallback(&warn, fallbackNone, gaps); useFull || err != nil {
		t.Errorf("none: got (%v, %v), want (false, nil)", useFull, err)
	}
	if warn.Len() == 0 {
		t.Error("none: an unprovable selection must still be reported on stderr")
	}
}

func TestNoRunnerHint(t *testing.T) {
	err := noRunnerHint(&runner.NoRunnerError{Lang: "java", Paths: []string{"A.java"}})
	if err == nil {
		t.Fatal("want an error")
	}
	if !errors.Is(err, runner.ErrNoRunner) {
		t.Error("the ErrNoRunner sentinel must survive so callers can match on it")
	}
	for _, want := range []string{"java", "witness select --format paths"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message %q missing %q", err.Error(), want)
		}
	}

	plain := errors.New("boom")
	if got := noRunnerHint(plain); got != plain {
		t.Errorf("non-runner errors must pass through unchanged, got %v", got)
	}
}

// The full-suite fallback's refusal is a different failure, and it is the one a
// CI operator meets when a Java, Swift, PHP or Dart build turns red on an
// ordinary uncovered change. It used to read "witness cannot run tests for
// unknown; use 'witness select --format paths' ...: no test runner known for
// language "unknown"; 0 selected test(s) cannot be run: " — advice for the
// other problem, a language nobody writes, a count of zero, and nothing at all
// after the colon.
func TestNoRunnerHintForTheFullSuiteFallback(t *testing.T) {
	err := noRunnerHint(&runner.NoRunnerError{WholeSuite: true})
	if err == nil {
		t.Fatal("want an error")
	}
	if !errors.Is(err, runner.ErrNoRunner) {
		t.Error("the ErrNoRunner sentinel must survive so callers can match on it")
	}
	msg := err.Error()
	for _, want := range []string{"whole-suite", "a language recon could not identify"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q missing %q", msg, want)
		}
	}
	for _, unwanted := range []string{"unknown", "0 selected test(s)", "witness select --format paths"} {
		if strings.Contains(msg, unwanted) {
			t.Errorf("message %q must not contain %q", msg, unwanted)
		}
	}
}

func TestReportGapsMentionsTruncationAndUnmapped(t *testing.T) {
	var buf bytes.Buffer
	reportGaps(&buf, &selector.SelectResult{
		Tests: []selector.ScoredTest{{Path: "a_test.go"}},
		Summary: selector.Summary{
			TestsSelected: 50,
			Truncated:     50,
			Unmapped:      []string{"internal/x/y.go"},
		},
	}, nil)
	got := buf.String()
	if !strings.Contains(got, "capped at 50 of 100") {
		t.Errorf("output %q should report the cap", got)
	}
	if !strings.Contains(got, "--max") {
		t.Errorf("output %q should say how to raise the cap", got)
	}
	if !strings.Contains(got, "internal/x/y.go") {
		t.Errorf("output %q should name the uncovered file", got)
	}
}

// --- end-to-end tests against a real git repo --------------------------------

// THE embed-safety test: everything `select` prints must land in the command's
// writers, because an embedding host (Rivet) redirects those to capture the
// result and shares its real stdout with an MCP stdio stream.
func TestSelectWritesToCommandWritersNotProcessStdout(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)

	for _, format := range []string{"json", "paths", "exec"} {
		t.Run(format, func(t *testing.T) {
			var out, errOut bytes.Buffer
			leaked := captureProcessOutput(t, func() {
				root := NewRootCmd("v")
				root.SetOut(&out)
				root.SetErr(&errOut)
				root.SetArgs([]string{"select", "--format", format, "calc.go"})
				if err := root.Execute(); err != nil {
					t.Fatalf("select --format %s: %v", format, err)
				}
			})

			if leaked != "" {
				t.Errorf("%q leaked to the process stdout/stderr instead of the command writers", leaked)
			}
			if out.Len() == 0 && errOut.Len() == 0 {
				t.Fatal("neither command writer received anything")
			}
			if format == "json" {
				var result selector.SelectResult
				if err := json.Unmarshal(out.Bytes(), &result); err != nil {
					t.Fatalf("stdout buffer is not the JSON result (%v): %q", err, out.String())
				}
			}
		})
	}
}

// The empty-selection JSON must be the same shape as the populated one: a
// consumer doing result.tests.length breaks on null.
func TestSelectJSONNeverEmitsNullSlices(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)

	// A file with no tests at all, and then no files at all.
	for _, args := range [][]string{
		{"select", "--format", "json", "README.md"},
		{"select", "--format", "json", "--staged"},
	} {
		var out, errOut bytes.Buffer
		root := NewRootCmd("v")
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}

		var raw map[string]json.RawMessage
		if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
			t.Fatalf("%v: %v (%q)", args, err, out.String())
		}
		for _, field := range []string{"changed_files", "tests"} {
			if string(raw[field]) == "null" {
				t.Errorf("%v: %q marshalled as null, want []", args, field)
			}
		}
	}
}

// A file that recon has never indexed must not read as "nothing to test".
func TestRunFallbackFailsClosedOnUnprovenSelection(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)

	// config.yaml is not a language recon indexes, so witness cannot say which
	// tests cover it — the exact shape of the go.mod-bump false green.
	writeFile(t, repo, "config.yaml", "feature_flags: {}\n")

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"run", "--fallback", "fail", "config.yaml"})
	err := root.Execute()
	if err == nil {
		t.Fatal("--fallback=fail must not exit 0 on a selection witness cannot prove")
	}
	if !strings.Contains(err.Error(), "cannot prove") {
		t.Errorf("error = %v, want it to say witness cannot prove the selection", err)
	}
}

// The default policy: a change nothing maps to must widen to the whole suite,
// not print "nothing to run" and exit 0.
func TestRunDefaultFallbackWidensToTheFullSuite(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)
	writeFile(t, repo, "config.yaml", "feature_flags: {}\n")

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	// --test-cmd true keeps the test hermetic while still exercising the path.
	root.SetArgs([]string{"run", "--test-cmd", "true", "config.yaml"})
	if err := root.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	got := errOut.String()
	if strings.Contains(got, "nothing to run") {
		t.Errorf("default --fallback exited without running anything: %q", got)
	}
	if !strings.Contains(got, "full suite") {
		t.Errorf("stderr = %q, want the full-suite fallback to be explained", got)
	}
	if !strings.Contains(got, "running 1 command(s)") {
		t.Errorf("stderr = %q, want a command to have been run", got)
	}
}

// `select --format json` reports the gaps in the payload rather than widening,
// but --fallback=fail still has to make the command exit non-zero.
func TestSelectJSONHonoursFallbackFail(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)
	writeFile(t, repo, "config.yaml", "feature_flags: {}\n")

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"select", "--format", "json", "--fallback", "fail", "config.yaml"})
	if err := root.Execute(); err == nil {
		t.Fatal("--fallback=fail must fail the command")
	}

	// The default policy leaves the JSON alone but records the gap.
	out.Reset()
	errOut.Reset()
	root = NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"select", "--format", "json", "config.yaml"})
	if err := root.Execute(); err != nil {
		t.Fatalf("select: %v", err)
	}
	var result selector.SelectResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("%v: %q", err, out.String())
	}
	if len(result.Summary.Unmapped) != 1 || result.Summary.Unmapped[0] != "config.yaml" {
		t.Errorf("summary.unmapped = %v, want [config.yaml]", result.Summary.Unmapped)
	}
	if strings.Contains(errOut.String(), "full suite") {
		t.Errorf("json output does not widen to the full suite; stderr should not claim it does: %q", errOut.String())
	}
}

// TestSelectPathsCannotSwallowTheFallback: `--format paths` has no way to say
// "run the whole suite", and the documented pipeline is
// `witness select --format paths | xargs go test`. Printing nothing and exiting
// 0 there is a green build that ran zero tests — the exact false green the
// fallback exists to prevent — so the exit code has to carry the failure.
func TestSelectPathsCannotSwallowTheFallback(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)
	writeFile(t, repo, "config.yaml", "feature_flags: {}\n")

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"select", "--format", "paths", "config.yaml"})
	err := root.Execute()
	if err == nil {
		t.Fatalf("select --format paths exited 0 with stdout %q while falling back to the full suite", out.String())
	}
	if !strings.Contains(err.Error(), "--format exec") {
		t.Errorf("error = %q, want it to point at the format that can express the fallback", err)
	}

	// --fallback=none is the documented opt-out and must still succeed.
	var quiet bytes.Buffer
	opt := NewRootCmd("v")
	opt.SetOut(&quiet)
	opt.SetErr(io.Discard)
	opt.SetArgs([]string{"select", "--format", "paths", "--fallback", "none", "config.yaml"})
	if err := opt.Execute(); err != nil {
		t.Errorf("--fallback=none must not fail: %v", err)
	}
}

// TestSelectPathsPrintsWhatItFoundBeforeFailing keeps the failure useful: the
// partial selection is still the best answer witness has, so it is printed even
// though the exit code says it is not the whole one.
func TestSelectPathsPrintsWhatItFoundBeforeFailing(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)
	writeFile(t, repo, "calc.go", "package calc\n\n// Add returns a+b.\nfunc Add(a, b int) int { return b + a }\n")
	writeFile(t, repo, "config.yaml", "feature_flags: {}\n")

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"select", "--format", "paths", "--require-coverage"})
	if err := root.Execute(); err == nil {
		t.Fatal("--require-coverage with an uncovered file must not exit 0")
	}
	if !strings.Contains(out.String(), "calc_test.go") {
		t.Errorf("stdout = %q, want the tests witness did find", out.String())
	}
}

// TestRequireCoverageFailsOnAnyUncoveredFile: the common PR shape is a
// dependency bump ALONGSIDE a source edit. By default that passes — tests ran
// — so a gate that wants every changed file accounted for needs its own knob.
func TestRequireCoverageFailsOnAnyUncoveredFile(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)
	writeFile(t, repo, "calc.go", "package calc\n\n// Add returns a+b.\nfunc Add(a, b int) int { return b + a }\n")
	writeFile(t, repo, "config.yaml", "feature_flags: {}\n")

	// Default: the selection has teeth, so the uncovered file is reported but
	// does not fail the run.
	var errOut bytes.Buffer
	base := NewRootCmd("v")
	base.SetOut(io.Discard)
	base.SetErr(&errOut)
	base.SetArgs([]string{"run", "--test-cmd", "true"})
	if err := base.Execute(); err != nil {
		t.Fatalf("default run: %v", err)
	}
	if !strings.Contains(errOut.String(), "config.yaml") {
		t.Errorf("stderr = %q, want the uncovered file named", errOut.String())
	}

	strict := NewRootCmd("v")
	strict.SetOut(io.Discard)
	strict.SetErr(io.Discard)
	strict.SetArgs([]string{"run", "--test-cmd", "true", "--require-coverage", "--fallback", "fail"})
	err := strict.Execute()
	if err == nil {
		t.Fatal("--require-coverage --fallback=fail exited 0 with a changed file no test covers")
	}
	if !strings.Contains(err.Error(), "config.yaml") {
		t.Errorf("error = %q, want it to name the uncovered file", err)
	}
}

// TestUserFiltersDoNotWidenToTheWholeSuite: --kind and --exclude are the user
// saying which tests to skip. Answering an empty result by running every test
// in the repository defeats the flag they just typed, and is not the same class
// of uncertainty as a stale index.
func TestUserFiltersDoNotWidenToTheWholeSuite(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)
	writeFile(t, repo, "calc.go", "package calc\n\n// Add returns a+b.\nfunc Add(a, b int) int { return b + a }\n")

	for _, args := range [][]string{
		{"select", "--format", "exec", "--kind", "e2e"},
		{"select", "--format", "exec", "--exclude", "**"},
	} {
		var out, errOut bytes.Buffer
		root := NewRootCmd("v")
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if strings.Contains(out.String(), "./...") {
			t.Errorf("%v widened to the whole suite (%q); the filter the user typed selected nothing on purpose", args, out.String())
		}
		if !strings.Contains(errOut.String(), "dropped by") {
			t.Errorf("%v: stderr = %q, want it to say the filter emptied the selection", args, errOut.String())
		}
	}
}

// TestPositionalPathsResolveThroughASymlinkedRepoPath: os.Getwd honours $PWD
// (logical) while git's rev-parse --show-toplevel resolves symlinks (physical).
// A repository reached through a symlink then made every positional argument
// fall out of the repo-relative path space — witness reported the file as not
// indexed and selected nothing. Symlinked home directories, symlinked CI
// checkouts and macOS's /tmp all hit it.
func TestPositionalPathsResolveThroughASymlinkedRepoPath(t *testing.T) {
	repo := newTestRepo(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(repo, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	sub := filepath.Join(link, "internal", "deep")
	if err := os.MkdirAll(filepath.Join(repo, "internal", "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"select", "--format", "paths", "--fallback", "none", "../../calc.go"})
	if err := root.Execute(); err != nil {
		t.Fatalf("select through a symlinked path: %v (stderr %q)", err, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "calc_test.go" {
		t.Errorf("select ../../calc.go from %s = %q, want calc_test.go (stderr %q)", sub, got, errOut.String())
	}
}

func TestSelectExecFallsBackToFullSuite(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)
	writeFile(t, repo, "config.yaml", "feature_flags: {}\n")

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"select", "--format", "exec", "config.yaml"})
	if err := root.Execute(); err != nil {
		t.Fatalf("select --format exec: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "go test ./..." {
		t.Errorf("exec output = %q, want the full-suite command", got)
	}
	if !strings.Contains(errOut.String(), "full suite") {
		t.Errorf("the fallback must be explained on stderr, got %q", errOut.String())
	}
}

func TestSelectExecHonoursTestCmdAndPassthrough(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)
	writeFile(t, repo, "config.yaml", "feature_flags: {}\n")

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"select", "--format", "exec", "--test-cmd", "gotestsum --", "config.yaml", "--", "-race"})
	if err := root.Execute(); err != nil {
		t.Fatalf("select --format exec: %v", err)
	}
	got := strings.TrimSpace(out.String())
	if !strings.HasPrefix(got, "gotestsum") || !strings.HasSuffix(got, "-race") {
		t.Errorf("exec output = %q, want the overridden runner with -race appended", got)
	}
}

// TestSelectExecTestCmdAppendsGoPackagesNotFilePaths is the case the previous
// --test-cmd test never reached: a REAL selection. `go test calc_test.go`
// compiles that one file as package command-line-arguments and dies with
// "undefined: Add", so the documented override was unusable for Go — a false
// red rather than a false green, but the flag was useless either way.
func TestSelectExecTestCmdAppendsGoPackagesNotFilePaths(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)
	writeFile(t, repo, "calc.go", "package calc\n\n// Add returns a+b.\nfunc Add(a, b int) int { return b + a }\n")

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"select", "--format", "exec", "--test-cmd", "gotestsum --", "calc.go"})
	if err := root.Execute(); err != nil {
		t.Fatalf("select --format exec: %v", err)
	}
	got := strings.TrimSpace(out.String())
	if !strings.Contains(got, "./...") {
		t.Errorf("command = %q, want the selection as a go package glob", got)
	}
	if strings.Contains(got, "calc_test.go") {
		t.Errorf("command = %q passes a raw .go file to the runner; `go test calc_test.go` fails to build", got)
	}
}

// TestTestCmdRefusesAPolyglotSelection: --test-cmd returns ONE command, so a
// selection spanning two ecosystems would be handed to one runner — the exact
// `mix test ... cart.test.js` bug the per-language grouping exists to prevent.
// Refusing is the only honest answer; running half the suite and exiting 0 is
// not.
func TestTestCmdRefusesAPolyglotSelection(t *testing.T) {
	cmds, err := testCommands("", nil, &selector.SelectResult{
		Tests: []selector.ScoredTest{
			{Path: "test/orders_test.exs"},
			{Path: "assets/src/cart.test.js"},
		},
	}, &selectFlags{testCmd: "mix test"}, false, nil)
	if err == nil {
		t.Fatalf("--test-cmd across two languages produced %v, want an error", cmds)
	}
	for _, want := range []string{"elixir", "node", "--test-cmd"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should name %q", err, want)
		}
	}
}

// TestTestCmdRejectsShellSyntax: witness executes --test-cmd directly, never
// through a shell, so an environment assignment or a pipe is passed to the test
// runner as a literal argument. It failed loudly but blamed the wrong thing;
// now the message names the token and the fix.
func TestTestCmdRejectsShellSyntax(t *testing.T) {
	cases := []struct{ cmd, token string }{
		{"GOFLAGS=-count=1 go test", "GOFLAGS=-count=1"},
		{"go test ./... | head -1", "|"},
		{"go test ./... && echo done", "&&"},
	}
	for _, tc := range cases {
		t.Run(tc.cmd, func(t *testing.T) {
			_, err := testCommands("", nil, &selector.SelectResult{
				Tests: []selector.ScoredTest{{Path: "calc_test.go"}},
			}, &selectFlags{testCmd: tc.cmd}, false, nil)
			if err == nil {
				t.Fatalf("--test-cmd %q was accepted; it is an argv, not a shell line", tc.cmd)
			}
			if !strings.Contains(err.Error(), tc.token) {
				t.Errorf("error %q should name the offending token %q", err, tc.token)
			}
			if !strings.Contains(err.Error(), "sh -c") {
				t.Errorf("error %q should show how to get shell syntax", err)
			}
		})
	}
}

// TestTestCommandsPassesTheRepoRootToTheRunner: Maven, Gradle, sbt, SwiftPM,
// PHPUnit, dart and cargo-in-a-workspace derive their invocation from files in
// the repository — which build file owns the tree, what class the test declares.
// The runner reads them relative to the root testCommands hands it, so a root
// that never arrives costs those languages their commands and turns a runnable
// selection into ErrNoRunner.
func TestTestCommandsPassesTheRepoRootToTheRunner(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "pom.xml", "<project><artifactId>calc</artifactId></project>\n")
	writeFile(t, root, "src/test/java/com/example/CalculatorTest.java",
		"package com.example;\n\nclass CalculatorTest {\n}\n")

	result := &selector.SelectResult{
		Tests: []selector.ScoredTest{{Path: "src/test/java/com/example/CalculatorTest.java"}},
	}

	cmds, err := testCommands(root, nil, result, &selectFlags{}, false, nil)
	if err != nil {
		t.Fatalf("testCommands with the repo root: %v", err)
	}
	if len(cmds) != 1 || cmds[0].String() != "mvn test -Dtest=CalculatorTest" {
		t.Errorf("commands = %v, want [mvn test -Dtest=CalculatorTest] derived from the pom at the root", cmds)
	}

	// Same selection with no root: nothing on disk can be consulted, so the
	// only honest answer is ErrNoRunner. If this ever returns a command, the
	// root has stopped mattering and the assertion above proves nothing.
	if _, err := testCommands("", nil, result, &selectFlags{}, false, nil); !errors.Is(err, runner.ErrNoRunner) {
		t.Errorf("testCommands without a root = %v, want ErrNoRunner", err)
	}
}

// `run` with a passthrough flag must not treat it as a changed file, which is
// how `witness run -- -race` used to select nothing and exit 0.
func TestRunTreatsPassthroughAsRunnerArgsNotChangedFiles(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	// --test-cmd true keeps the test hermetic: no real toolchain is invoked.
	root.SetArgs([]string{"run", "--test-cmd", "true", "calc.go", "--", "-race"})
	if err := root.Execute(); err != nil {
		t.Fatalf("run: %v", err)
	}
	banner := errOut.String()
	if !strings.Contains(banner, "-race") {
		t.Errorf("stderr banner = %q, want -race forwarded to the runner", banner)
	}
	if strings.Contains(banner, `"-race"`) {
		t.Errorf("-race was quoted as a path: %q", banner)
	}
}

// Run from a subdirectory: git reports repo-relative paths, so recon has to be
// rooted at the repository root or every lookup misses and witness reports "no
// relevant tests" for a change it could not see.
func TestSelectFromSubdirectoryResolvesRepoRoot(t *testing.T) {
	repo := newTestRepo(t)
	sub := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// Edit a file at the repository root, then diff from the subdirectory.
	writeFile(t, repo, "calc.go", "package calc\n\n// Add returns a+b.\nfunc Add(a, b int) int { return b + a }\n")
	t.Chdir(sub)

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"select", "--format", "paths"})
	if err := root.Execute(); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got := strings.TrimSpace(out.String()); got != "calc_test.go" {
		t.Errorf("select from %s = %q, want calc_test.go", sub, got)
	}
}

// Positional paths are typed relative to the shell, but git and recon speak
// repo-relative. Both spellings must reach the same file.
func TestPositionalPathsAreResolvedRelativeToTheShell(t *testing.T) {
	repo := newTestRepo(t)
	sub := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	for _, arg := range []string{"../../calc.go", filepath.Join(repo, "calc.go")} {
		var out, errOut bytes.Buffer
		root := NewRootCmd("v")
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs([]string{"select", "--format", "paths", arg})
		if err := root.Execute(); err != nil {
			t.Fatalf("select %s: %v", arg, err)
		}
		if got := strings.TrimSpace(out.String()); got != "calc_test.go" {
			t.Errorf("select %s = %q, want calc_test.go", arg, got)
		}
	}
}

// A failing suite must reach main() as an ExitCodeError carrying the runner's
// code, not as a generic error or a silent success.
func TestRunPropagatesTheRunnerExitCode(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"run", "--test-cmd", "false", "calc.go"})
	err := root.Execute()

	var ce *ExitCodeError
	if !errors.As(err, &ce) {
		t.Fatalf("error = %v, want an ExitCodeError", err)
	}
	if ce.Code != 1 {
		t.Errorf("code = %d, want 1", ce.Code)
	}
	if !strings.Contains(ce.Error(), "exit code 1") {
		t.Errorf("message = %q, want it to name the exit code", ce.Error())
	}
}

// A deleted file is not in recon's index, so its dependents cannot be resolved
// and an empty selection proves nothing.
func TestRunTreatsDeletionsAsUnproven(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)
	if err := os.Remove(filepath.Join(repo, "calc.go")); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"run", "--fallback", "fail"})
	err := root.Execute()
	if err == nil {
		t.Fatal("a deleted source file must not produce a clean exit 0")
	}
	if !strings.Contains(err.Error(), "deleted") {
		t.Errorf("error = %v, want it to mention the deletion", err)
	}
}

func TestRunReportsNoRunnerInsteadOfExecutingPaths(t *testing.T) {
	repo := newTestRepo(t)
	t.Chdir(repo)
	writeFile(t, repo, "Widget.java", "class Widget {}\n")
	writeFile(t, repo, "WidgetTest.java", "class WidgetTest {}\n")

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"run", "--fallback", "none", "WidgetTest.java"})
	err := root.Execute()
	if err == nil {
		t.Skip("recon did not select the java test; nothing to assert")
	}
	if !errors.Is(err, runner.ErrNoRunner) {
		t.Fatalf("error = %v, want ErrNoRunner", err)
	}
	var ce *ExitCodeError
	if errors.As(err, &ce) {
		t.Error("a missing runner is not a test failure and must not carry a test exit code")
	}
}

// A relative --cache-dir used to be resolved by recon against the shell's
// working directory, so the same flag in the same repository named a different
// — and always cold — cache depending on where it was typed, and scattered
// cache directories through the tree. Every other path witness handles is
// repo-root-relative; this one is now too.
func TestRelativeCacheDirResolvesAgainstTheRepoRoot(t *testing.T) {
	repo := newTestRepo(t)
	sub := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(sub)

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"select", "--format", "paths", "--cache-dir", ".witness", filepath.Join(repo, "calc.go")})
	if err := root.Execute(); err != nil {
		t.Fatalf("select --cache-dir .witness: %v (stderr %q)", err, errOut.String())
	}

	entries, err := os.ReadDir(filepath.Join(repo, ".witness"))
	if err != nil || len(entries) == 0 {
		t.Errorf("%s = (%v, %v), want the cache at the repository root", filepath.Join(repo, ".witness"), entries, err)
	}
	if _, err := os.Stat(filepath.Join(sub, ".witness")); err == nil {
		t.Errorf("--cache-dir .witness from %s wrote the cache into the subdirectory", sub)
	}
}

// The one user the change above can surprise is told, once, on stderr: their
// existing cache is no longer the one being read. Rebuilding it is safe (recon's
// cache is derived analysis) but it is not free, and a rebuild nobody announced
// reads as witness having become slow.
func TestRelocatedCacheIsAnnouncedOnlyWhenOneIsThere(t *testing.T) {
	repo := newTestRepo(t)
	sub := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	run := func(t *testing.T) string {
		t.Helper()
		t.Chdir(sub)
		var out, errOut bytes.Buffer
		root := NewRootCmd("v")
		root.SetOut(&out)
		root.SetErr(&errOut)
		root.SetArgs([]string{"select", "--format", "paths", "--cache-dir", ".witness", filepath.Join(repo, "calc.go")})
		if err := root.Execute(); err != nil {
			t.Fatalf("select --cache-dir .witness: %v (stderr %q)", err, errOut.String())
		}
		return errOut.String()
	}

	// Nothing at the old location: nothing to warn about.
	if got := run(t); strings.Contains(got, "cache-dir") {
		t.Errorf("stderr = %q, want no cache note when the old location is empty", got)
	}

	writeFile(t, sub, filepath.Join(".witness", "recon.db"), "stale\n")
	got := run(t)
	if !strings.Contains(got, "relative to the repository root") || !strings.Contains(got, filepath.Join(sub, ".witness")) {
		t.Errorf("stderr = %q, want a note naming the cache that is no longer used (%s)", got, filepath.Join(sub, ".witness"))
	}
}

// From the repository root the flag resolves exactly where it always did, so
// saying anything would be noise on every run.
func TestCacheDirNoteIsSilentAtTheRepoRoot(t *testing.T) {
	repo := newTestRepo(t)
	writeFile(t, repo, filepath.Join(".witness", "recon.db"), "stale\n")
	t.Chdir(repo)

	var out, errOut bytes.Buffer
	root := NewRootCmd("v")
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs([]string{"select", "--format", "paths", "--cache-dir", ".witness", "calc.go"})
	if err := root.Execute(); err != nil {
		t.Fatalf("select --cache-dir .witness: %v (stderr %q)", err, errOut.String())
	}
	if strings.Contains(errOut.String(), "cache-dir") {
		t.Errorf("stderr = %q, want silence: the flag resolves where it always did", errOut.String())
	}
}

func TestRepoCacheDir(t *testing.T) {
	root := filepath.Join(string(filepath.Separator), "repo")
	abs := filepath.Join(string(filepath.Separator), "var", "cache")
	cases := []struct{ in, want string }{
		{"", ""},
		{".cache", filepath.Join(root, ".cache")},
		{filepath.Join("build", "cache"), filepath.Join(root, "build", "cache")},
		// An absolute path is the user naming a location outright.
		{abs, abs},
	}
	for _, tc := range cases {
		if got := repoCacheDir(root, tc.in); got != tc.want {
			t.Errorf("repoCacheDir(%q, %q) = %q, want %q", root, tc.in, got, tc.want)
		}
	}
}

// --- helpers -----------------------------------------------------------------

func newTestRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}

	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	git("init", "-q")
	git("config", "user.email", "witness@example.test")
	git("config", "user.name", "witness")

	// recon's cache lives in the repo; without this it shows up as an untracked
	// change and pollutes every working-tree diff under test.
	writeFile(t, dir, ".gitignore", ".recon/\n.witness/\n")
	writeFile(t, dir, "go.mod", "module example.test/calc\n\ngo 1.25\n")
	writeFile(t, dir, "README.md", "# calc\n")
	writeFile(t, dir, "calc.go", "package calc\n\n// Add returns a+b.\nfunc Add(a, b int) int { return a + b }\n")
	writeFile(t, dir, "calc_test.go",
		"package calc\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"bad\")\n\t}\n}\n")

	git("add", ".")
	git("commit", "-q", "-m", "init")
	return dir
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// captureProcessOutput swaps the process's real stdout/stderr for a pipe while
// fn runs and returns whatever escaped onto them.
func captureProcessOutput(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = w, w

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		r.Close()
		done <- buf.String()
	}()

	// A t.Fatal inside fn unwinds through here, so restore in a defer as well
	// as on the happy path; the second Close is a harmless no-op.
	defer func() {
		os.Stdout, os.Stderr = origOut, origErr
		w.Close()
	}()

	fn()

	os.Stdout, os.Stderr = origOut, origErr
	w.Close()
	return <-done
}
