// Package audiofile writes rendered audio as WAV (uncompressed PCM) or FLAC
// (lossless compression), both 16-bit mono.
package audiofile

import (
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
)

// Quantize converts -1..1 float samples to 16-bit PCM. NaN becomes silence
// and out-of-range values are clipped.
func Quantize(samples []float32) []int16 {
	out := make([]int16, len(samples))
	for i, s := range samples {
		if s != s { // NaN
			s = 0
		}
		s = min(max(s, -1), 1)
		out[i] = int16(math.Round(float64(s) * 32767))
	}
	return out
}

// Encode writes samples in the given format ("wav" or "flac").
func Encode(w io.Writer, format string, rate int, samples []float32) error {
	pcm := Quantize(samples)
	switch format {
	case "wav":
		return EncodeWAV(w, rate, pcm)
	case "flac":
		return EncodeFLAC(w, rate, pcm)
	}
	return fmt.Errorf("unsupported format %q (use .wav or .flac)", format)
}

// FormatOf returns the format implied by a file name's extension.
func FormatOf(path string) string {
	return strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
}

// WriteFile writes samples to path, choosing the format from the extension.
func WriteFile(path string, rate int, samples []float32) error {
	format := FormatOf(path)
	if format != "wav" && format != "flac" {
		return fmt.Errorf("unsupported format %q (use .wav or .flac)", format)
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := Encode(f, format, rate, samples); err != nil {
		f.Close()
		os.Remove(path)
		return err
	}
	return f.Close()
}
