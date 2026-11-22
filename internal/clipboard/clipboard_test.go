package clipboard

import "testing"

func TestClipboard(t *testing.T) {
	cleanCases := []struct {
		name, in, want string
	}{
		{"newlines and tabs become spaces", "  t*(42&t>>10)\n\t| t>>4\r\n", "t*(42&t>>10) | t>>4"},
		{"runs of spaces collapse", "t  &   255", "t & 255"},
		{"empty", " \n\t", ""},
	}
	for _, tt := range cleanCases {
		t.Run("clean/"+tt.name, func(t *testing.T) {
			if got := clean(tt.in); got != tt.want {
				t.Errorf("clean(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
	t.Run("read returns cleaned text", func(t *testing.T) {
		// The clipboard may be unavailable (CI, sandbox); either outcome is
		// fine, but a result must already be cleaned.
		if s, err := Read(); err == nil && s != clean(s) {
			t.Errorf("not cleaned: %q", s)
		}
	})
}
