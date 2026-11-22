package ui

import (
	"math"

	"github.com/pristupaanastasia/bitwave/internal/expr"
)

// lineEdit is a single-line text buffer with a cursor.
type lineEdit struct {
	text []rune
	cur  int
}

func (l *lineEdit) Set(s string)   { l.text = []rune(s); l.cur = len(l.text) }
func (l *lineEdit) String() string { return string(l.text) }

func (l *lineEdit) Insert(rs []rune) {
	if len(rs) == 0 {
		return
	}
	out := make([]rune, 0, len(l.text)+len(rs))
	out = append(out, l.text[:l.cur]...)
	out = append(out, rs...)
	out = append(out, l.text[l.cur:]...)
	l.text = out
	l.cur += len(rs)
}

func (l *lineEdit) Backspace() bool {
	if l.cur == 0 {
		return false
	}
	l.text = append(l.text[:l.cur-1], l.text[l.cur:]...)
	l.cur--
	return true
}

func (l *lineEdit) Delete() bool {
	if l.cur >= len(l.text) {
		return false
	}
	l.text = append(l.text[:l.cur], l.text[l.cur+1:]...)
	return true
}

func (l *lineEdit) Left()  { l.cur = max(l.cur-1, 0) }
func (l *lineEdit) Right() { l.cur = min(l.cur+1, len(l.text)) }
func (l *lineEdit) Home()  { l.cur = 0 }
func (l *lineEdit) End()   { l.cur = len(l.text) }

// findTrigger returns the index of the first rising zero crossing in
// buf[lo:hi], or lo if there is none, so periodic waves stand still on the
// oscilloscope.
func findTrigger(buf []float32, lo, hi int) int {
	hi = min(hi, len(buf))
	for i := max(lo, 1); i < hi; i++ {
		if buf[i-1] < 0 && buf[i] >= 0 {
			return i
		}
	}
	return lo
}

// columns reduces samples to width columns of (min, max) pairs.
func columns(samples []float32, width int) (lo, hi []float32) {
	lo, hi = make([]float32, width), make([]float32, width)
	n := len(samples)
	for x := range width {
		a := x * n / width
		b := max((x+1)*n/width, a+1)
		mn, mx := float32(math.Inf(1)), float32(math.Inf(-1))
		for _, s := range samples[a:min(b, n)] {
			mn, mx = min(mn, s), max(mx, s)
		}
		lo[x], hi[x] = mn, mx
	}
	return lo, hi
}

// toByte recovers the 0..255 byte from a byte-mode sample in -1..1.
func toByte(s float32) uint8 {
	return uint8(min(max(math.Round(float64(s)*128+128), 0), 255))
}

// paintWave sets wave points between (i0,v0) and (i1,v1) by linear
// interpolation, so fast mouse strokes leave no gaps.
func paintWave(w *[expr.WaveSize]float64, i0 int, v0 float64, i1 int, v1 float64) {
	clampI := func(i int) int { return min(max(i, 0), expr.WaveSize-1) }
	clampV := func(v float64) float64 { return math.Round(min(max(v, 0), 255)) }
	i0, i1 = clampI(i0), clampI(i1)
	if i0 == i1 {
		w[i0] = clampV(v1)
		return
	}
	if i0 > i1 {
		i0, i1, v0, v1 = i1, i0, v1, v0
	}
	for i := i0; i <= i1; i++ {
		k := float64(i-i0) / float64(i1-i0)
		w[i] = clampV(v0 + (v1-v0)*k)
	}
}

type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool {
	return x >= r.x && y >= r.y && x < r.x+r.w && y < r.y+r.h
}
