package synth

import (
	"math"

	"github.com/pristupaanastasia/bitwave/internal/expr"
)

// Shapes lists the built-in wave shapes for the wave editor.
var Shapes = []string{"sine", "square", "saw", "triangle", "noise"}

// Shape returns a wavetable for a named shape, values in 0..255.
func Shape(name string) [expr.WaveSize]float64 {
	var w [expr.WaveSize]float64
	seed := uint32(2463534242)
	for i := range w {
		x := float64(i) / expr.WaveSize
		var v float64 // -1..1
		switch name {
		case "sine":
			v = math.Sin(2 * math.Pi * x)
		case "square":
			v = 1
			if x >= 0.5 {
				v = -1
			}
		case "saw":
			v = 2*x - 1
		case "triangle":
			v = 1 - 4*math.Abs(x-0.5)
		case "noise":
			seed ^= seed << 13
			seed ^= seed >> 17
			seed ^= seed << 5
			v = float64(seed)/math.MaxUint32*2 - 1
		}
		w[i] = math.Round(127.5 + v*127.5)
	}
	return w
}

// Preset is a named formula. Presets that use the sequencer read p, e or f;
// the rest are pure functions of t.
type Preset struct {
	Name    string
	Formula string
	Mode    Mode
	Rate    int
}

var Presets = []Preset{
	{"drawn wave + seq", "128+(w(p)-128)*e", ModeByte, 8000},
	{"wave + bit crush", "(128+(w(p)-128)*e)&(0xF0|t>>12&15)", ModeByte, 8000},
	{"wave x fm", "128+(w(p+(w(p*2)-128)*e)-128)*e", ModeByte, 16000},
	{"seq square bits", "(p&128)*e|t>>4&t>>7&15", ModeByte, 8000},
	{"floatbeat sine seq", "sin(p/128*PI)*e*.7", ModeFloat, 44100},
	{"42 melody", "t*(42&t>>10)", ModeByte, 8000},
	{"sierpinski", "t&t>>8", ModeByte, 8000},
	{"crowd", "(t*(t>>5|t>>8))>>(t>>16)", ModeByte, 8000},
	{"viznut 1", "t*((t>>12|t>>8)&63&t>>4)", ModeByte, 8000},
	{"xpansive", "(t*5&t>>7)|(t*3&t>>10)", ModeByte, 8000},
	{"arp array", "t*[1,1.5,2,1.25][t>>12&3]&128|t>>5&t>>9", ModeByte, 8000},
	{"noise hats", "(random()*255&(t>>8&1?255:0)>>(t>>4&7))+(t*[3,4,5,4][t>>13&3]&64)", ModeByte, 8000},
}
