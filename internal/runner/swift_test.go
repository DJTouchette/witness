package runner

import (
	"reflect"
	"strings"
	"testing"
)

// swiftSource is a minimal XCTest case.
func swiftSource(class string) string {
	return "import XCTest\n@testable import App\n\n" +
		"final class " + class + ": XCTestCase {\n" +
		"    func testWorks() { XCTAssertTrue(true) }\n" +
		"}\n"
}

func TestFormatCommand_Swift(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		paths []string
		want  [][]string
	}{
		{
			// --filter takes a test NAME, so the suite has to be read out of the
			// file; the path is not a thing SwiftPM accepts at all.
			name: "filter names the XCTestCase",
			files: map[string]string{
				"Package.swift":                        "// swift-tools-version:5.9\n",
				"Tests/AppTests/CalculatorTests.swift": swiftSource("CalculatorTests"),
			},
			paths: []string{"Tests/AppTests/CalculatorTests.swift"},
			want:  [][]string{{"swift", "test", "--filter", "CalculatorTests"}},
		},
		{
			// One command per suite rather than a repeated --filter: nothing here
			// has to assume the option can be given twice, and ExecuteAll runs
			// them all even when the first is red.
			name: "one command per suite, sorted",
			files: map[string]string{
				"Package.swift":                        "// swift-tools-version:5.9\n",
				"Tests/AppTests/CalculatorTests.swift": swiftSource("CalculatorTests"),
				"Tests/AppTests/OrderTests.swift":      swiftSource("OrderTests"),
			},
			paths: []string{"Tests/AppTests/OrderTests.swift", "Tests/AppTests/CalculatorTests.swift"},
			want: [][]string{
				{"swift", "test", "--filter", "CalculatorTests"},
				{"swift", "test", "--filter", "OrderTests"},
			},
		},
		{
			// swift-testing suites are structs, and Swift does not require the
			// file to be named after them.
			name: "a struct suite named unlike its file",
			files: map[string]string{
				"Package.swift": "// swift-tools-version:5.9\n",
				"Tests/AppTests/Checkout.swift": "import Testing\n\n" +
					"@Suite struct CheckoutFlowTests {\n    @Test func works() {}\n}\n",
			},
			paths: []string{"Tests/AppTests/Checkout.swift"},
			want:  [][]string{{"swift", "test", "--filter", "CheckoutFlowTests"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, tt.files)
			got := argvsOf(mustFormat(t, root, "swift", tt.paths))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("FormatCommand(swift) = %v, want %v", got, tt.want)
			}
		})
	}
}

// Two XCTestCase subclasses in one file, one of them named after the file: the
// filter has to cover both. --filter matching nothing is not reliably an error
// in XCTest, so the sibling suite would simply never run and the file witness
// selected would report a pass it did not earn.
func TestFormatCommand_SwiftNamesEverySuiteInTheFile(t *testing.T) {
	root := writeTree(t, map[string]string{
		"Package.swift": "// swift-tools-version:5.9\n",
		"Tests/AppTests/CalculatorTests.swift": "import XCTest\n@testable import App\n\n" +
			"final class CalculatorTests: XCTestCase {\n    func testAdds() {}\n}\n\n" +
			"final class RefundTests: XCTestCase {\n    func testRefunds() {}\n}\n",
	})
	got := argvsOf(mustFormat(t, root, "swift", []string{"Tests/AppTests/CalculatorTests.swift"}))
	want := [][]string{
		{"swift", "test", "--filter", "CalculatorTests"},
		{"swift", "test", "--filter", "RefundTests"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("FormatCommand(swift) = %v, want %v\nthe second suite in the same file must be run too", got, want)
	}
}

func TestFormatCommand_SwiftRefusals(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		path       string
		wantReason string
	}{
		{
			// An Xcode project needs `xcodebuild -scheme X -destination Y`, and
			// neither the scheme nor the destination is in the path.
			name:       "no Package.swift",
			files:      map[string]string{"Tests/AppTests/CalculatorTests.swift": swiftSource("CalculatorTests")},
			path:       "Tests/AppTests/CalculatorTests.swift",
			wantReason: "Package.swift",
		},
		{
			// Free-standing @Test functions with no enclosing suite: there is no
			// type name to filter on, and inventing one runs nothing.
			name: "no suite type to name",
			files: map[string]string{
				"Package.swift": "// swift-tools-version:5.9\n",
				"Tests/AppTests/Checkout.swift": "import Testing\n\n" +
					"@Test func checkoutWorks() {}\n",
			},
			path:       "Tests/AppTests/Checkout.swift",
			wantReason: "declares no class",
		},
		{
			name:       "file missing from the tree",
			files:      map[string]string{"Package.swift": "// swift-tools-version:5.9\n"},
			path:       "Tests/AppTests/GoneTests.swift",
			wantReason: "could not be read",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := writeTree(t, tt.files)
			err := mustNoRunner(t, root, "swift", []string{tt.path})
			if !strings.Contains(err.Reason, tt.wantReason) {
				t.Errorf("Reason = %q, want it to mention %q", err.Reason, tt.wantReason)
			}
		})
	}
}
