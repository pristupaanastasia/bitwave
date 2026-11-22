// Package clipboard reads the system clipboard using the platform's
// command-line tools, which avoids a cgo dependency.
package clipboard

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// Read returns the clipboard text with line breaks turned into spaces, since
// formulas are single-line.
func Read() (string, error) {
	var cmds [][]string
	switch runtime.GOOS {
	case "darwin":
		cmds = [][]string{{"pbpaste"}}
	case "windows":
		cmds = [][]string{{"powershell", "-NoProfile", "-Command", "Get-Clipboard"}}
	default:
		cmds = [][]string{{"wl-paste", "-n"}, {"xclip", "-o", "-selection", "clipboard"}, {"xsel", "-ob"}}
	}
	for _, c := range cmds {
		out, err := exec.Command(c[0], c[1:]...).Output()
		if err == nil {
			return clean(string(out)), nil
		}
	}
	return "", errors.New("clipboard is not available")
}

func clean(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
