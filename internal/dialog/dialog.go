// Package dialog shows the system's native "Save as" and "Open" file
// dialogs by running the platform's own tools (osascript on macOS, zenity
// on Linux, PowerShell on Windows), which avoids a cgo dependency.
//
// The calls block until the user answers, so run them off the UI thread.
package dialog

import (
	"errors"
	"os/exec"
	"runtime"
	"strings"
)

// ErrCanceled is returned when the user closes the dialog without choosing.
var ErrCanceled = errors.New("canceled")

// Save asks for a file to write, suggesting defaultName.
func Save(prompt, defaultName string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return run("osascript", "-e", "POSIX path of (choose file name with prompt "+
			appleString(prompt)+" default name "+appleString(defaultName)+")")
	case "windows":
		return run("powershell", "-NoProfile", "-Command", "Add-Type -AssemblyName System.Windows.Forms; "+
			"$d = New-Object System.Windows.Forms.SaveFileDialog; $d.Title = "+psString(prompt)+
			"; $d.FileName = "+psString(defaultName)+"; if ($d.ShowDialog() -eq 'OK') { $d.FileName }")
	default:
		return run("zenity", "--file-selection", "--save", "--confirm-overwrite",
			"--title="+prompt, "--filename="+defaultName)
	}
}

// Open asks for an existing file to read.
func Open(prompt string) (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return run("osascript", "-e", "POSIX path of (choose file with prompt "+appleString(prompt)+")")
	case "windows":
		return run("powershell", "-NoProfile", "-Command", "Add-Type -AssemblyName System.Windows.Forms; "+
			"$d = New-Object System.Windows.Forms.OpenFileDialog; $d.Title = "+psString(prompt)+
			"; if ($d.ShowDialog() -eq 'OK') { $d.FileName }")
	default:
		return run("zenity", "--file-selection", "--title="+prompt)
	}
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	path := strings.TrimSpace(string(out))
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return "", ErrCanceled // all three tools exit non-zero on cancel
	case err != nil:
		return "", err // the tool itself is missing
	case path == "":
		return "", ErrCanceled
	}
	return path, nil
}

// appleString quotes s as an AppleScript string literal.
func appleString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

// psString quotes s as a PowerShell single-quoted string literal.
func psString(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
