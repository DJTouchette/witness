package calc

import (
	"testing"

	"example.test/calc/internal/mathx"
)

func TestAdd(t *testing.T) {
	if got := Add(1, 2); got != 3 {
		t.Fatalf("Add(1, 2) = %d, want 3", got)
	}
	if got := mathx.Double(2); got != 4 {
		t.Fatalf("Double(2) = %d, want 4", got)
	}
}
