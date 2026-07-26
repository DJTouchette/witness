package runner

import (
	"errors"
	"regexp"
	"strings"
)

// Gradle and sbt select tests by fully qualified class name, and neither the
// package nor the class is recoverable from a path: src/test/kotlin holds
// com.example.OrderTest as readily as it holds OrderTest in the default
// package, and Kotlin, Scala and Swift all allow a file whose types are named
// nothing like the file. So witness reads both out of the file itself.
//
// The parsing below is deliberately shallow — comments stripped, then a line
// scan — and every shape it is not sure about is an error, never a best guess.
// A wrong FQCN is not a loud failure everywhere: `sbt 'testOnly no.Such.Class'`
// prints "No tests to run" and exits 0, which is exactly the false green this
// package exists to prevent.

var (
	// errPackageBlock rejects Scala's brace-delimited package syntax. A file may
	// hold several sibling blocks, and which one encloses the test class is not
	// knowable without parsing the language for real.
	errPackageBlock = errors.New("the file uses a brace-delimited package block, so witness cannot tell which package the test class is in")

	// errPackageMalformed rejects a package clause the scan does not recognise
	// (a backquoted Kotlin identifier, say) rather than truncating it into a
	// package name that points nowhere.
	errPackageMalformed = errors.New("the file's package declaration could not be read")

	// errPackageSeveral rejects several package clauses in a language that
	// allows only one, which means the scan has misread the file.
	errPackageSeveral = errors.New("the file declares more than one package")

	// errNoTypeDecl reports a file with no class witness can name. Maven, Gradle,
	// sbt and swift-test all select by type name; without one there is nothing to
	// put in the filter.
	errNoTypeDecl = errors.New("the file declares no class or object witness can name, and a test filter needs one")

	// errAmbiguousTypeDecl reports several candidate types, none named after the
	// file. Filtering on the wrong one runs the wrong tests, or none.
	errAmbiguousTypeDecl = errors.New("the file declares several types and none is named after the file, so witness cannot tell which to filter on")

	// errNestedTypeDecl rejects a file whose test type is declared inside
	// another type. `object Fixtures { class NestedSpec }` is addressed
	// com.example.Fixtures$NestedSpec at runtime, not com.example.NestedSpec,
	// and the dotted form is exactly the filter sbt reports as success having
	// run nothing. The scan here is a line scan, not a parser, so it can see
	// the nesting but cannot be trusted to reconstruct the runtime name.
	errNestedTypeDecl = errors.New("the file declares its type inside another type, whose runtime name uses a $ separator witness cannot derive")
)

// packageClauseRe matches a whole package clause line, after comments have been
// stripped. Anchoring both ends is the point: a line that begins with `package`
// and does not match is reported as unreadable instead of half-parsed.
var packageClauseRe = regexp.MustCompile(`^package[ \t]+([A-Za-z_$][A-Za-z0-9_$]*(?:[ \t]*\.[ \t]*[A-Za-z_$][A-Za-z0-9_$]*)*)[ \t]*([;{]?)[ \t]*$`)

// identifierRe is a plain type name: anything else (a backquoted Kotlin name,
// an operator) is refused rather than guessed at.
var identifierRe = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

// typeKeywords introduce a named type in the languages witness filters by name.
var typeKeywords = map[string]bool{
	"class":     true,
	"object":    true,
	"interface": true,
	"trait":     true,
	"enum":      true,
	"record":    true,
	"struct":    true,
	"actor":     true,
}

// declModifiers may sit between the start of a line and a type keyword. An
// unknown token there means the line is not a declaration witness understands,
// and it is skipped rather than mined for a name.
var declModifiers = map[string]bool{
	"abstract":    true,
	"actual":      true,
	"annotation":  true,
	"case":        true,
	"companion":   true,
	"data":        true,
	"dynamic":     true,
	"expect":      true,
	"fileprivate": true,
	"final":       true,
	"implicit":    true,
	"indirect":    true,
	"inline":      true,
	"inner":       true,
	"internal":    true,
	"open":        true,
	"override":    true,
	"private":     true,
	"protected":   true,
	"public":      true,
	"sealed":      true,
	"static":      true,
	"strictfp":    true,
	"value":       true,
}

// declaredPackage returns the package a source file declares, "" for the
// default package.
//
// chain allows Scala's stacked clauses — `package com.example` followed by
// `package orders` names package com.example.orders — which Java and Kotlin do
// not have; seeing two clauses in those is a sign the scan misread the file, so
// it is an error there.
func declaredPackage(src []byte, chain bool) (string, error) {
	var names []string
	for _, line := range strings.Split(string(src), "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "package") {
			continue
		}
		rest := trimmed[len("package"):]
		if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
			// `packageName` or `package;`: not a clause.
			continue
		}
		// Scala's `package object utils` declares an object inside the current
		// package; "object" is not a package name.
		if fields := strings.Fields(rest); len(fields) > 0 && fields[0] == "object" {
			continue
		}
		match := packageClauseRe.FindStringSubmatch(trimmed)
		if match == nil {
			return "", errPackageMalformed
		}
		if match[2] == "{" {
			return "", errPackageBlock
		}
		names = append(names, strings.Join(strings.FieldsFunc(match[1], func(r rune) bool {
			return r == '.' || r == ' ' || r == '\t'
		}), "."))
	}

	switch {
	case len(names) == 0:
		return "", nil
	case len(names) == 1:
		return names[0], nil
	case chain:
		return strings.Join(names, "."), nil
	default:
		return "", errPackageSeveral
	}
}

// typeDecl is one type declaration found in a source file, with the brace
// nesting depth it sits at — 0 for a top-level type.
//
// Depth is not decoration. A suite nested inside another type is not addressed
// by the dotted name the file reads like: sbt and JUnit both want
// Outer$Inner, and `sbt 'testOnly com.example.NestedSpec'` for a suite declared
// inside `object Fixtures` matches nothing and exits 0.
type typeDecl struct {
	name  string
	depth int
}

// declaredTypes lists the types a source file declares, in file order, each
// with the nesting depth it was found at.
func declaredTypes(src []byte) []typeDecl {
	var decls []typeDecl
	depth := 0
	for _, line := range strings.Split(string(src), "\n") {
		// The depth a declaration sits at is the one in force where the line
		// STARTS: `class Foo {` opens its own brace and is still top level.
		if name := declaredLineName(line); name != "" {
			decls = append(decls, typeDecl{name: name, depth: depth})
		}
		depth += braceDelta(line)
		if depth < 0 {
			// Unbalanced input (a truncated read, a brace inside an unhandled
			// literal). Clamping keeps later declarations at a plausible depth
			// instead of driving them negative.
			depth = 0
		}
	}
	return decls
}

// declaredLineName returns the type a single line declares, or "".
func declaredLineName(line string) string {
	fields := strings.Fields(line)
	for i, field := range fields {
		if !typeKeywords[field] {
			if declModifiers[field] || strings.HasPrefix(field, "@") {
				continue
			}
			// Anything else before the keyword (an assignment, a call)
			// means this is not a declaration.
			return ""
		}
		if i+1 >= len(fields) {
			return ""
		}
		// `enum class Foo`, `case object Bar`: keep walking to the keyword
		// that actually precedes the name.
		if typeKeywords[fields[i+1]] {
			continue
		}
		return declaredName(fields[i+1])
	}
	return ""
}

// braceDelta counts the braces a line opens minus the ones it closes, skipping
// string and character literals so a `{` in a message — or a Kotlin `"${x}"`
// template — cannot shift the nesting.
func braceDelta(line string) int {
	src := []byte(line)
	delta := 0
	for i := 0; i < len(src); {
		switch src[i] {
		case '{':
			delta++
			i++
		case '}':
			delta--
			i++
		case '"', '\'':
			i = literalEnd(src, i)
		default:
			i++
		}
	}
	return delta
}

// declaredName trims a declaration's name token down to the identifier —
// `Foo(val`, `Foo<T>`, `Foo:` and `Foo{` all name Foo — and returns "" for
// anything that is not a plain identifier.
func declaredName(token string) string {
	name := strings.TrimRight(token, ",")
	if cut := strings.IndexAny(name, "({<:;,="); cut >= 0 {
		name = name[:cut]
	}
	if !identifierRe.MatchString(name) {
		return ""
	}
	return name
}

// filterTypes picks the type names to put in a build tool's filter for a test
// file whose base name (without extension) is base.
//
// EVERY top-level type comes back, not just the one the file is named after.
// witness selects whole FILES, and Java, Kotlin and Scala all allow several
// top-level test classes in one of them; naming only the one matching the base
// name leaves the siblings unrun, and a build that ran neither
// `class RefundsTest` nor its failing assertion still exits 0. Naming a type
// with no tests in it costs an extra pattern — surefire, Gradle and sbt all
// accept a filter list where only some entries match — while dropping one costs
// the gate.
//
// The base name wins when the file declares it, which is the overwhelming case
// and the only one Java allows for a public class. A file declaring exactly one
// type is taken at its word even when the names differ, since there is nothing
// to confuse it with. Anything else is refused: a filter naming a type that is
// not there matches no test, and sbt reports that as success.
func filterTypes(base string, types []typeDecl) ([]string, error) {
	var top []string
	nested := false
	for _, decl := range types {
		if decl.depth == 0 {
			top = append(top, decl.name)
			continue
		}
		nested = true
	}

	for _, name := range top {
		if name == base {
			return top, nil
		}
	}
	if nested {
		// The file's own name matches nothing at the top level, so the type the
		// filter wants is plausibly one of the nested ones — and that is the
		// name witness cannot spell.
		return nil, errNestedTypeDecl
	}
	switch len(top) {
	case 0:
		return nil, errNoTypeDecl
	case 1:
		return top, nil
	default:
		return nil, errAmbiguousTypeDecl
	}
}

// stripComments blanks out comments so a commented-out `package` clause or
// class declaration cannot be read as a real one, keeping newlines so the
// result is still line-addressable.
//
// String literals are copied through untouched: a URL in a Kotlin string would
// otherwise start a line comment and swallow the rest of the line. nested says
// whether block comments nest, which they do in Kotlin, Scala and Swift but not
// in Java, where /* /* */ ends at the first close.
func stripComments(src []byte, nested bool) []byte {
	out := make([]byte, 0, len(src))
	depth := 0
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case depth > 0:
			if nested && c == '/' && i+1 < len(src) && src[i+1] == '*' {
				depth++
				i += 2
				continue
			}
			if c == '*' && i+1 < len(src) && src[i+1] == '/' {
				depth--
				i += 2
				continue
			}
			if c == '\n' {
				out = append(out, '\n')
			}
			i++
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			depth++
			i += 2
		case c == '"' || c == '\'':
			end := literalEnd(src, i)
			out = append(out, src[i:end]...)
			i = end
		default:
			out = append(out, c)
			i++
		}
	}
	return out
}

// literalEnd returns the index just past the string or character literal
// starting at i. An unterminated literal ends at the newline, so one stray
// quote cannot swallow the whole file.
func literalEnd(src []byte, i int) int {
	quote := src[i]
	if quote == '"' && strings.HasPrefix(string(src[i:]), `"""`) {
		if end := strings.Index(string(src[i+3:]), `"""`); end >= 0 {
			return i + 3 + end + 3
		}
		return len(src)
	}
	for j := i + 1; j < len(src); j++ {
		switch src[j] {
		case '\\':
			j++
		case '\n':
			return j
		case quote:
			return j + 1
		}
	}
	return len(src)
}
