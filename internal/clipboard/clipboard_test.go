package clipboard

import "testing"

func TestClean(t *testing.T) {
	if got := clean("  t*(42&t>>10)\n\t| t>>4\r\n"); got != "t*(42&t>>10) | t>>4" {
		t.Fatalf("%q", got)
	}
}

func TestRead(t *testing.T) {
	// The clipboard may be unavailable (CI, sandbox); either outcome is fine,
	// but a result must already be cleaned.
	s, err := Read()
	if err == nil && s != clean(s) {
		t.Fatalf("not cleaned: %q", s)
	}
}
