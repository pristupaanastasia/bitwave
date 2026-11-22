package dialog

import (
	"errors"
	"testing"
)

func TestDialog(t *testing.T) {
	quoting := []struct {
		name, in, apple, ps string
	}{
		{"plain", "song.json", `"song.json"`, `'song.json'`},
		{"double quotes", `my "best" song`, `"my \"best\" song"`, `'my "best" song'`},
		{"single quote", "it's", `"it's"`, `'it''s'`},
		{"backslash", `a\b`, `"a\\b"`, `'a\b'`},
	}
	for _, tt := range quoting {
		t.Run("quote/"+tt.name, func(t *testing.T) {
			if got := appleString(tt.in); got != tt.apple {
				t.Errorf("AppleScript %s, want %s", got, tt.apple)
			}
			if got := psString(tt.in); got != tt.ps {
				t.Errorf("PowerShell %s, want %s", got, tt.ps)
			}
		})
	}

	runs := []struct {
		name    string
		cmd     []string
		want    string
		wantErr error
	}{
		{"path is trimmed", []string{"echo", " /tmp/song.json "}, "/tmp/song.json", nil},
		{"non-zero exit is a cancel", []string{"false"}, "", ErrCanceled},
		{"empty answer is a cancel", []string{"true"}, "", ErrCanceled},
	}
	for _, tt := range runs {
		t.Run("run/"+tt.name, func(t *testing.T) {
			got, err := run(tt.cmd[0], tt.cmd[1:]...)
			if got != tt.want || !errors.Is(err, tt.wantErr) {
				t.Errorf("got %q, %v; want %q, %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
	t.Run("run/missing tool is a real error", func(t *testing.T) {
		if _, err := run("bitwave-no-such-tool"); err == nil || errors.Is(err, ErrCanceled) {
			t.Errorf("err = %v", err)
		}
	})
}
