package runner

import (
	"testing"
)

func TestFormatCommand(t *testing.T) {
	tests := []struct {
		framework string
		paths     []string
		want      string
	}{
		{"elixir", []string{"test/a_test.exs", "test/b_test.exs"}, "mix test test/a_test.exs test/b_test.exs"},
		{"phoenix", []string{"test/a_test.exs"}, "mix test test/a_test.exs"},
		{"python", []string{"tests/test_a.py"}, "pytest tests/test_a.py"},
		{"ruby", []string{"spec/a_spec.rb"}, "bundle exec rspec spec/a_spec.rb"},
		{"node", []string{"src/a.test.ts"}, "npx jest src/a.test.ts"},
		{"rust", []string{"tests/a.rs"}, "cargo test tests/a.rs"},
		{"", nil, ""},
	}

	for _, tt := range tests {
		got := FormatCommand(tt.framework, tt.paths)
		if got != tt.want {
			t.Errorf("FormatCommand(%q, %v) = %q, want %q", tt.framework, tt.paths, got, tt.want)
		}
	}
}

func TestDetectFramework(t *testing.T) {
	tests := []struct {
		frameworks []string
		want       string
	}{
		{[]string{"Phoenix"}, "elixir"},
		{[]string{"Rails"}, "ruby"},
		{[]string{"React", "Express"}, "node"},
		{[]string{"Django"}, "python"},
		{[]string{"unknown"}, ""},
		{nil, ""},
	}

	for _, tt := range tests {
		got := DetectFramework(tt.frameworks)
		if got != tt.want {
			t.Errorf("DetectFramework(%v) = %q, want %q", tt.frameworks, got, tt.want)
		}
	}
}
