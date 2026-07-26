package runner

import (
	"errors"
	"reflect"
	"testing"
)

func TestDeclaredPackage(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		chain   bool
		want    string
		wantErr error
	}{
		{
			name: "java",
			src:  "package com.example.orders;\n\nclass OrderTest {}\n",
			want: "com.example.orders",
		},
		{
			name: "kotlin without a semicolon",
			src:  "package com.example\n\nclass OrderTest\n",
			want: "com.example",
		},
		{
			// The default package is a real answer: `--tests OrderTest` and
			// `testOnly OrderTest` are both correct without one.
			name: "no package clause is the default package",
			src:  "import kotlin.test.Test\n\nclass OrderTest\n",
			want: "",
		},
		{
			name:  "scala stacks clauses",
			src:   "package com.example\npackage orders\n\nclass OrderSpec\n",
			chain: true,
			want:  "com.example.orders",
		},
		{
			// Scala's package object is an object inside the current package,
			// not a package of its own.
			name:  "package object is not a package clause",
			src:   "package com.example\n\npackage object orders {\n}\n",
			chain: true,
			want:  "com.example",
		},
		{
			name: "a commented-out clause is not a clause",
			src:  "// package com.wrong\n/* package com.alsowrong */\npackage com.example;\n\nclass OrderTest {}\n",
			want: "com.example",
		},
		{
			name: "spaces around the dots",
			src:  "package com . example ;\n\nclass OrderTest {}\n",
			want: "com.example",
		},
		{
			// Sibling brace blocks make the enclosing package unknowable, and a
			// wrong FQCN in `sbt testOnly` is reported as success.
			name:    "brace-delimited block",
			src:     "package com.example {\n  class OrderSpec\n}\n",
			chain:   true,
			wantErr: errPackageBlock,
		},
		{
			// A backquoted Kotlin package component: truncating it would name a
			// package that does not exist.
			name:    "unreadable clause",
			src:     "package com.`example package`.orders\n\nclass OrderTest\n",
			wantErr: errPackageMalformed,
		},
		{
			// Two clauses in a language that allows one means the scan misread
			// the file, and nothing derived from it can be trusted.
			name:    "two clauses where only one is legal",
			src:     "package com.example;\npackage com.other;\n\nclass OrderTest {}\n",
			wantErr: errPackageSeveral,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := declaredPackage(stripComments([]byte(tt.src), tt.chain), tt.chain)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err == nil && got != tt.want {
				t.Errorf("declaredPackage = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDeclaredTypes(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []typeDecl
	}{
		{
			name: "java modifiers",
			src:  "public final class CalculatorTest extends BaseTest {\n}\n",
			want: []typeDecl{{name: "CalculatorTest"}},
		},
		{
			name: "annotation on the same line",
			src:  "@RunWith(SpringRunner::class) class OrderTest {\n}\n",
			want: []typeDecl{{name: "OrderTest"}},
		},
		{
			name: "kotlin enum class walks past the first keyword",
			src:  "enum class Status {\n    OK\n}\n",
			want: []typeDecl{{name: "Status"}},
		},
		{
			name: "data class and case object",
			src:  "data class Order(val id: Int)\ncase object Empty\n",
			want: []typeDecl{{name: "Order"}, {name: "Empty"}},
		},
		{
			name: "companion object contributes no name",
			src:  "class OrderTest {\n    companion object {\n    }\n}\n",
			want: []typeDecl{{name: "OrderTest"}},
		},
		{
			name: "generics and constructor parameters are trimmed",
			src:  "class Repo<T>(val db: Db) : Base<T>() {\n}\n",
			want: []typeDecl{{name: "Repo"}},
		},
		{
			name: "swift suite",
			src:  "final class CalculatorTests: XCTestCase {\n}\n",
			want: []typeDecl{{name: "CalculatorTests"}},
		},
		{
			name: "swift struct suite",
			src:  "@Suite struct CheckoutFlowTests {\n}\n",
			want: []typeDecl{{name: "CheckoutFlowTests"}},
		},
		{
			// A string that reads like a declaration is not one.
			name: "a declaration in a string literal",
			src:  "val sample = \"class Ghost {}\"\n\nclass OrderTest {\n}\n",
			want: []typeDecl{{name: "OrderTest"}},
		},
		{
			// A backquoted name is refused rather than truncated to its first
			// word, which would filter on a type that does not exist.
			name: "backquoted name",
			src:  "class `order test` {\n}\n",
			want: nil,
		},
		{
			name: "no declaration at all",
			src:  "val fixtures = listOf(1, 2, 3)\n",
			want: nil,
		},
		{
			// Several top-level test classes in one file is legal Java, and
			// idiomatic Scala. All of them have to be seen: witness selects the
			// FILE, so a class it never names is a test that never runs.
			name: "two top-level classes",
			src:  "class OrdersSpec extends AnyFunSuite {\n  test(\"a\") {}\n}\n\nclass RefundsSpec extends AnyFunSuite {\n  test(\"b\") {}\n}\n",
			want: []typeDecl{{name: "OrdersSpec"}, {name: "RefundsSpec"}},
		},
		{
			// The runtime name of this suite is Fixtures$NestedSpec, not
			// NestedSpec — the depth is what lets the caller refuse it instead
			// of emitting a dotted name that matches nothing.
			name: "a suite declared inside an object",
			src:  "object Fixtures {\n  class NestedSpec extends AnyFunSuite {\n    test(\"x\") { assert(false) }\n  }\n}\n",
			want: []typeDecl{{name: "Fixtures"}, {name: "NestedSpec", depth: 1}},
		},
		{
			name: "a JUnit 5 nested class sits under its outer class",
			src:  "class CalculatorTest {\n    @Nested\n    class Addition {\n    }\n}\n",
			want: []typeDecl{{name: "CalculatorTest"}, {name: "Addition", depth: 1}},
		},
		{
			// A brace inside a literal is not nesting; counting it would push
			// every later declaration down a level and get it refused.
			name: "braces inside literals do not nest",
			src:  "val open = \"{\"\nval tmpl = \"${count}\"\nclass OrderTest {\n}\n",
			want: []typeDecl{{name: "OrderTest"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := declaredTypes(stripComments([]byte(tt.src), true))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("declaredTypes = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFilterTypes(t *testing.T) {
	tests := []struct {
		name    string
		base    string
		types   []typeDecl
		want    []string
		wantErr error
	}{
		{
			// The whole file was selected, so the whole file has to be named.
			// Returning only "OrderTest" here is how a failing sibling class in
			// the very file witness picked went unrun and the gate went green.
			name:  "every top-level type comes back, not just the file's own",
			base:  "OrderTest",
			types: []typeDecl{{name: "Fixtures"}, {name: "OrderTest"}, {name: "Helper"}},
			want:  []string{"Fixtures", "OrderTest", "Helper"},
		},
		{
			name:  "a lone type is taken at its word",
			base:  "CacheBehaviour",
			types: []typeDecl{{name: "CacheSpec"}},
			want:  []string{"CacheSpec"},
		},
		{
			// A JUnit 5 @Nested class runs as part of its outer class, which is
			// the one the file is named after and the one in the filter.
			name:  "a nested class under a matching outer name is not a refusal",
			base:  "CalculatorTest",
			types: []typeDecl{{name: "CalculatorTest"}, {name: "Addition", depth: 1}},
			want:  []string{"CalculatorTest"},
		},
		{
			name:    "nothing to name",
			base:    "Fixtures",
			types:   nil,
			wantErr: errNoTypeDecl,
		},
		{
			name:    "several and none matching",
			base:    "Suites",
			types:   []typeDecl{{name: "OrderTest"}, {name: "CartTest"}},
			wantErr: errAmbiguousTypeDecl,
		},
		{
			// com.example.NestedSpec is not the suite's name — Fixtures$NestedSpec
			// is — and sbt reports the miss as success.
			name:    "a suite nested inside an object",
			base:    "NestedSpec",
			types:   []typeDecl{{name: "Fixtures"}, {name: "NestedSpec", depth: 1}},
			wantErr: errNestedTypeDecl,
		},
		{
			name:    "only nested types",
			base:    "Suites",
			types:   []typeDecl{{name: "OrderSpec", depth: 1}},
			wantErr: errNestedTypeDecl,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := filterTypes(tt.base, tt.types)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if err == nil && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("filterTypes = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStripComments(t *testing.T) {
	tests := []struct {
		name   string
		src    string
		nested bool
		want   string
	}{
		{
			name: "line comment",
			src:  "class A {} // trailing\nclass B {}\n",
			want: "class A {} \nclass B {}\n",
		},
		{
			// A URL in a string is not a comment; eating it would swallow the
			// rest of the line, and with it a declaration.
			name: "a slash-slash inside a string survives",
			src:  "val url = \"https://example.com\"\nclass A {}\n",
			want: "val url = \"https://example.com\"\nclass A {}\n",
		},
		{
			name: "java block comments do not nest",
			src:  "/* /* */ class A {}\n",
			want: " class A {}\n",
		},
		{
			// Kotlin, Scala and Swift nest them, so the inner close does not end
			// the comment.
			name:   "kotlin block comments nest",
			src:    "/* /* */ class Ghost */ class A {}\n",
			nested: true,
			want:   " class A {}\n",
		},
		{
			name: "a character literal holding a quote",
			src:  "val quote = '\"'\nclass A {}\n",
			want: "val quote = '\"'\nclass A {}\n",
		},
		{
			name:   "triple-quoted string",
			src:    "val doc = \"\"\"\n// not a comment\nclass Ghost\n\"\"\"\nclass A {}\n",
			nested: true,
			want:   "val doc = \"\"\"\n// not a comment\nclass Ghost\n\"\"\"\nclass A {}\n",
		},
		{
			// One stray quote must not swallow the file: the literal ends at the
			// newline, so the declaration below it is still found.
			name: "unterminated string literal",
			src:  "val s = \"oops\nclass A {}\n",
			want: "val s = \"oops\nclass A {}\n",
		},
		{
			name: "escaped quote inside a string",
			src:  "val s = \"a \\\" class Ghost\"\nclass A {}\n",
			want: "val s = \"a \\\" class Ghost\"\nclass A {}\n",
		},
		{
			// An unterminated triple quote swallows the rest of the file, so no
			// type is found and the arm refuses — loud, not wrong.
			name:   "unterminated triple-quoted string",
			src:    "val doc = \"\"\"\nclass Ghost\n",
			nested: true,
			want:   "val doc = \"\"\"\nclass Ghost\n",
		},
		{
			name: "block comment keeps the line structure",
			src:  "/* one\ntwo */class A {}\n",
			want: "\nclass A {}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := string(stripComments([]byte(tt.src), tt.nested)); got != tt.want {
				t.Errorf("stripComments = %q, want %q", got, tt.want)
			}
		})
	}
}
