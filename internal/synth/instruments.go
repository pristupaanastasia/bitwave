package synth

// Instrument is a track template: a formula written around the knobs k1..k4,
// so the sound can be shaped from the UI without editing the formula.
type Instrument struct {
	Name      string
	Formula   string
	Mode      Mode
	Rate      int
	Wave      string // shape name for w(x)
	Volume    float64
	Knobs     [4]float64
	KnobNames [4]string
	Env       ADSR
}

// drumEnv keeps the gate open long enough for drum tails: the formulas
// shape their own decay from n, the envelope only avoids cutting them off.
var drumEnv = ADSR{On: true, S: 1, R: 0.3, Gate: 4}

// Instruments is the list shown in the INSTRUMENT selector.
var Instruments = []Instrument{
	{
		Name: "kick", Mode: ModeFloat, Rate: 44100, Wave: "sine", Volume: 0.9,
		// A sine with a falling pitch sweep, decay and tanh drive.
		Formula:   "e*tanh((1+k4*5)*sin(2*PI*((40+k1*60)*n/r+(2+k2*10)*(1-exp(-n/r*30)))))*exp(-n/r*(4+(1-k3)*20))",
		Knobs:     [4]float64{0.2, 0.3, 0.45, 0.15},
		KnobNames: [4]string{"pitch", "punch", "length", "drive"},
		Env:       drumEnv,
	},
	{
		Name: "snare", Mode: ModeFloat, Rate: 44100, Wave: "noise", Volume: 0.6,
		// A tuned body plus a noise burst, mixed by k2.
		Formula:   "e*tanh((1+k4*4)*((1-k2)*sin(2*PI*(160+k1*160)*n/r)*exp(-n/r*25)+k2*(random()*2-1)*exp(-n/r*(6+(1-k3)*40))))",
		Knobs:     [4]float64{0.4, 0.6, 0.4, 0.2},
		KnobNames: [4]string{"tone", "noise", "length", "snap"},
		Env:       drumEnv,
	},
	{
		Name: "hat", Mode: ModeFloat, Rate: 44100, Wave: "noise", Volume: 0.35,
		// Noise with a short decay; k2 quantises it to fewer levels.
		Formula:   "e*floor((random()*2-1)*(2+(1-k2)*30))/(2+(1-k2)*30)*exp(-n/r*(15+(1-k1)*150))",
		Knobs:     [4]float64{0.3, 0.2, 0, 0},
		KnobNames: [4]string{"length", "crush", "k3", "k4"},
		Env:       ADSR{On: true, S: 1, R: 0.1, Gate: 2},
	},
	{
		Name: "bass", Mode: ModeByte, Rate: 8000, Wave: "saw", Volume: 0.6,
		// Drawn wave blended with a square (k1), bit-crushed (k2), with an
		// optional sub octave (k3 above half).
		Formula:   "(128+((w(k3>.5?p/2:p)-128)*(1-k1)+((k3>.5?p/2:p)&128?96:-96)*k1)*e)&(255<<int(k2*5)&255)",
		Knobs:     [4]float64{0.25, 0, 0, 0},
		KnobNames: [4]string{"square", "crush", "sub", "k4"},
		Env:       ADSR{On: true, A: 0.005, D: 0.2, S: 0.6, R: 0.1, Gate: 1},
	},
	{
		Name: "lead", Mode: ModeByte, Rate: 16000, Wave: "square", Volume: 0.35,
		// Pulse wave whose width is modulated by an LFO (depth k1, rate k2).
		Formula:   "(128+(((p&255)<128+k1*100*sin(t/r*2*PI*(.5+k2*8)))?90:-90)*e)&(255<<int(k3*5)&255)",
		Knobs:     [4]float64{0.5, 0.3, 0, 0},
		KnobNames: [4]string{"pwm", "pwm rate", "crush", "k4"},
		Env:       ADSR{On: true, A: 0.01, D: 0.1, S: 0.7, R: 0.2, Gate: 1},
	},
	{
		Name: "pad", Mode: ModeFloat, Rate: 44100, Wave: "saw", Volume: 0.4,
		// Three detuned saws (k1) plus a sub sine (k2), slow envelope.
		Formula:   "e*(.3*((p/256%1)+(p*(1+k1*.02)/256%1)+(p*(1-k1*.02)/256%1)-1.5)+.4*k2*sin(p/256*PI))",
		Knobs:     [4]float64{0.4, 0.3, 0, 0},
		KnobNames: [4]string{"detune", "sub", "k3", "k4"},
		Env:       ADSR{On: true, A: 0.5, D: 0.4, S: 0.8, R: 1.2, Gate: 4},
	},
	{
		Name: "pluck", Mode: ModeByte, Rate: 16000, Wave: "triangle", Volume: 0.5,
		// Drawn wave with a sharp decay; k1 makes it snappier.
		Formula:   "(128+(w(p)-128)*e**(1+k1*4))&(255<<int(k2*5)&255)",
		Knobs:     [4]float64{0.4, 0, 0, 0},
		KnobNames: [4]string{"snap", "crush", "k3", "k4"},
		Env:       ADSR{On: true, D: 0.35, S: 0, R: 0.05, Gate: 1},
	},
	{
		Name: "bytebeat", Mode: ModeByte, Rate: 8000, Wave: "saw", Volume: 0.5,
		// A classic formula of t only; k1 changes how fast the melody moves.
		Formula:   "t*(42&t>>(9+int(k1*3)))",
		Knobs:     [4]float64{1. / 3, 0, 0, 0},
		KnobNames: [4]string{"speed", "k2", "k3", "k4"},
		Env:       ADSR{Gate: 1},
	},
}

// InstrumentTrack returns a new track made from an instrument template.
func InstrumentTrack(in Instrument) Track {
	t := Track{Name: in.Name}
	ApplyInstrument(&t, in)
	return t
}

// ApplyInstrument replaces a track's sound with an instrument template,
// keeping its mute/solo state and identity.
func ApplyInstrument(t *Track, in Instrument) {
	t.Name = in.Name
	t.Instrument = in.Name
	t.Formula, t.Mode, t.Rate = in.Formula, in.Mode, in.Rate
	t.Wave = Shape(in.Wave)
	t.Volume = in.Volume
	t.Knobs, t.KnobNames, t.Env = in.Knobs, in.KnobNames, in.Env
}

// InstrumentIndex returns the position of a named instrument, or -1.
func InstrumentIndex(name string) int {
	for i, in := range Instruments {
		if in.Name == name {
			return i
		}
	}
	return -1
}
