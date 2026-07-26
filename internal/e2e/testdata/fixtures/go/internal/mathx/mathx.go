// Package mathx is the helper the calc tests import. Editing it must select
// calc's tests even though nothing in calc's own package changed.
package mathx

// Double returns n + n.
func Double(n int) int {
	return n + n
}
