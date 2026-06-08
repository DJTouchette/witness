package cli

import "testing"

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

func TestSelectAndRunShareFlags(t *testing.T) {
	root := NewRootCmd("v")
	for _, name := range []string{"select", "run"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatalf("find %q: %v", name, err)
		}
		for _, flag := range []string{"depth", "min-score", "max", "staged", "since", "co-change-min", "fan-out-cap", "exclude", "kind"} {
			if cmd.Flags().Lookup(flag) == nil {
				t.Errorf("%s is missing --%s", name, flag)
			}
		}
	}

	// `select` has --format; `run` does not.
	sel, _, _ := root.Find([]string{"select"})
	if sel.Flags().Lookup("format") == nil {
		t.Error("select should have --format")
	}
	run, _, _ := root.Find([]string{"run"})
	if run.Flags().Lookup("format") != nil {
		t.Error("run should not have --format")
	}
}
