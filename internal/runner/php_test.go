package runner

import (
	"reflect"
	"strings"
	"testing"
)

func TestFormatCommand_PHP(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		paths []string
		want  [][]string
	}{
		{
			// PHPUnit takes paths, so unlike the JVM arms nothing is read out of
			// the test file — only the runner has to be derived.
			name: "phpunit takes the paths",
			files: map[string]string{
				"composer.json": `{"require-dev": {"phpunit/phpunit": "^10.5"}}`,
			},
			paths: []string{"tests/Unit/OrderTest.php", "tests/Feature/CheckoutTest.php"},
			want:  [][]string{{"vendor/bin/phpunit", "tests/Unit/OrderTest.php", "tests/Feature/CheckoutTest.php"}},
		},
		{
			name: "phpunit as a runtime requirement",
			files: map[string]string{
				"composer.json": `{"require": {"phpunit/phpunit": "^9.6"}}`,
			},
			paths: []string{"tests/OrderTest.php"},
			want:  [][]string{{"vendor/bin/phpunit", "tests/OrderTest.php"}},
		},
		{
			// A Pest project has phpunit vendored too, and `vendor/bin/phpunit`
			// there runs the PHPUnit classes while silently ignoring every Pest
			// test in the same directory.
			name: "pest wins where both are declared",
			files: map[string]string{
				"composer.json": `{"require-dev": {"pestphp/pest": "^2.0", "phpunit/phpunit": "^10.5"}}`,
			},
			paths: []string{"tests/Feature/CheckoutTest.php"},
			want:  [][]string{{"vendor/bin/pest", "tests/Feature/CheckoutTest.php"}},
		},
		{
			// A path with a space or a shell metacharacter is safe: the argv goes
			// straight to exec, never through a shell.
			name: "paths are passed through verbatim",
			files: map[string]string{
				"composer.json": `{"require-dev": {"phpunit/phpunit": "^10.5"}}`,
			},
			paths: []string{"tests/My Suite/Order$Test.php"},
			want:  [][]string{{"vendor/bin/phpunit", "tests/My Suite/Order$Test.php"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, tt.files)
			got := argvsOf(mustFormat(t, root, "php", tt.paths))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FormatCommand(php) = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFormatCommand_PHPRefusals(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		wantReason string
	}{
		{
			name:       "no composer.json",
			files:      map[string]string{"tests/OrderTest.php": "<?php\n"},
			wantReason: "no composer.json",
		},
		{
			// Codeception, phpspec, Behat: all real, none invoked like phpunit.
			// Guessing phpunit here would run a suite that is not the project's.
			name:       "a runner witness does not know",
			files:      map[string]string{"composer.json": `{"require-dev": {"codeception/codeception": "^5.0"}}`},
			wantReason: "neither phpunit/phpunit nor pestphp/pest",
		},
		{
			name:       "unparseable composer.json",
			files:      map[string]string{"composer.json": "{not json"},
			wantReason: "could not be parsed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, tt.files)
			err := mustNoRunner(t, root, "php", []string{"tests/OrderTest.php"})
			if !strings.Contains(err.Reason, tt.wantReason) {
				t.Errorf("Reason = %q, want it to mention %q", err.Reason, tt.wantReason)
			}
		})
	}
}
