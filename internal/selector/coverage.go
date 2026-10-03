package selector

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// CanAffectTests is shared by selection and planning so documentation cannot
// accidentally trigger an unrelated language's suite.
func CanAffectTests(path string) bool { return canAffectTests(path) }

// CoverageReasons describes uncertainty in a selection. A co-change match does
// not discharge a missing dependency edge, nor does one mapped file cover the
// other files in a change. Callers may still plan whole manifest suites.
func CoverageReasons(r *SelectResult) []string {
	if r == nil {
		return []string{"no selection"}
	}
	var out []string
	if r.Summary.AnalysisError != "" {
		out = append(out, "analysis failed: "+r.Summary.AnalysisError)
	}
	if len(r.Summary.NotIndexed) > 0 {
		out = append(out, "not indexed: "+strings.Join(r.Summary.NotIndexed, ", "))
	}
	if len(r.Summary.Unmapped) > 0 {
		out = append(out, "unmapped: "+strings.Join(r.Summary.Unmapped, ", "))
	}
	if r.Summary.Truncated > 0 {
		out = append(out, fmt.Sprintf("selection truncated by %d tests", r.Summary.Truncated))
	}
	out = append(out, r.Summary.Diagnostics...)
	return out
}

var rustTestAttribute = regexp.MustCompile(`^\s*#\s*\[\s*(cfg\s*\(\s*test\s*\)|test)\s*\]`)

func inlineRustTest(root, p string) bool {
	if root == "" || filepath.Ext(p) != ".rs" || filepath.IsAbs(p) || p == ".." || strings.HasPrefix(filepath.Clean(p), ".."+string(filepath.Separator)) {
		return false
	}
	f, err := os.Open(filepath.Join(root, p))
	if err != nil {
		return false
	}
	defer f.Close()
	s := bufio.NewScanner(io.LimitReader(f, 16<<20))
	s.Buffer(make([]byte, 4096), 1<<20)
	for s.Scan() {
		if rustTestAttribute.Match(s.Bytes()) {
			return true
		}
	}
	return false
}
