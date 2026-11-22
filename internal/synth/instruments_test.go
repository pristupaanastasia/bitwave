package synth

import (
	"math"
	"testing"
)

// instrumentSong plays one instrument: a note on steps 0, 4, 8 and 12.
func instrumentSong(in Instrument) Song {
	t := InstrumentTrack(in)
	t.Volume = 1
	return Song{BPM: 120, Volume: 1, Tracks: []Track{t},
		Patterns: []Pattern{{Notes: [][Steps]int{notes(map[int]int{0: 48, 4: 55, 8: 60, 12: 52})}}},
		Order:    []int{0}}
}

func stats(out []float32) (peak, rms float64, nan bool) {
	for _, v := range out {
		if v != v {
			nan = true
		}
		peak = max(peak, math.Abs(float64(v)))
		rms += float64(v) * float64(v)
	}
	return peak, math.Sqrt(rms / float64(len(out))), nan
}

// testCase is a named check; used where the cases of a feature differ in
// shape and cannot share one struct of inputs.
type testCase struct {
	name  string
	check func(t *testing.T)
}

func run(t *testing.T, tests []testCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

func TestInstruments(t *testing.T) {
	var tests []testCase
	for _, in := range Instruments {
		tests = append(tests, testCase{in.Name + "/audible and clean", func(t *testing.T) {
			peak, rms, nan := stats(mustNew(t, 44100, instrumentSong(in)).RenderSong())
			t.Logf("peak %.2f rms %.3f", peak, rms)
			if nan || peak > 1 || rms < 0.02 {
				t.Errorf("peak %.2f rms %.3f nan %v", peak, rms, nan)
			}
		}})
		tests = append(tests, testCase{in.Name + "/found by name", func(t *testing.T) {
			if Instruments[InstrumentIndex(in.Name)].Name != in.Name {
				t.Error("InstrumentIndex mismatch")
			}
		}})
		for k, name := range in.KnobNames {
			if name == defaultKnobNames()[k] {
				continue // unnamed knob: free for the user's own formulas
			}
			tests = append(tests, testCase{in.Name + "/knob " + name + " changes the sound", func(t *testing.T) {
				base := mustNew(t, 44100, instrumentSong(in)).RenderSong()
				s := instrumentSong(in)
				s.Tracks[0].Knobs[k] = 1 - math.Round(s.Tracks[0].Knobs[k]) // move it far
				out := mustNew(t, 44100, s).RenderSong()
				diff := 0.0
				for i := range out {
					diff += math.Abs(float64(out[i] - base[i]))
				}
				if diff/float64(len(out)) < 1e-3 {
					t.Errorf("k%d has no audible effect", k+1)
				}
			}})
		}
	}
	tests = append(tests,
		testCase{"unknown name", func(t *testing.T) {
			if InstrumentIndex("nope") != -1 {
				t.Error("want -1")
			}
		}},
		testCase{"apply keeps identity and mute/solo", func(t *testing.T) {
			tr := NewTrack("mine")
			tr.ID, tr.Mute, tr.Solo = 7, true, true
			ApplyInstrument(&tr, Instruments[InstrumentIndex("pad")])
			if tr.ID != 7 || !tr.Mute || !tr.Solo || tr.Instrument != "pad" || tr.KnobNames[0] != "detune" || !tr.Env.On {
				t.Errorf("%+v", tr)
			}
		}},
	)
	run(t, tests)
}

func TestADSR(t *testing.T) {
	curve := ADSR{On: true, A: 0.1, D: 0.2, S: 0.5, R: 0.4, Gate: 1}
	level := func(env ADSR, start, dt, want float64, wantDone bool) func(t *testing.T) {
		return func(t *testing.T) {
			got, done := env.level(start, dt, 1) // note held for 1 s
			if math.Abs(got-want) > 1e-9 || done != wantDone {
				t.Errorf("level(%v) = %v done %v, want %v done %v", dt, got, done, want, wantDone)
			}
		}
	}

	// One note on step 0 with a 2-step gate (0.25 s at 120 BPM), then rests.
	engineEnv := func() []float32 {
		s := oneTrack("e", ModeFloat, 8000)
		s.Tracks[0].Env = ADSR{On: true, A: 0.01, D: 0, S: 1, R: 0.1, Gate: 2}
		s.Patterns[0].Notes[0][0] = 60
		return mustNew(t, 8000, s).Render(4000)
	}()
	engine := func(sec, lo, hi float64) func(t *testing.T) {
		return func(t *testing.T) {
			if v := float64(engineEnv[int(sec*8000)]); v < lo || v > hi {
				t.Errorf("e at %.3fs = %v, want %v..%v", sec, v, lo, hi)
			}
		}
	}

	run(t, []testCase{
		{"curve/attack start", level(curve, 0, 0, 0, false)},
		{"curve/mid attack", level(curve, 0, 0.05, 0.5, false)},
		{"curve/attack peak", level(curve, 0, 0.1, 1, false)},
		{"curve/mid decay", level(curve, 0, 0.2, 0.75, false)},
		{"curve/sustain", level(curve, 0, 0.9, 0.5, false)},
		{"curve/mid release", level(curve, 0, 1.2, 0.25, false)},
		{"curve/release over", level(curve, 0, 1.45, 0, true)},
		{"curve/retrigger starts attack at current level", level(curve, 0.6, 0.05, 0.8, false)},
		{"curve/zero attack and decay jump to sustain", level(ADSR{S: 0.3}, 0, 0, 0.3, false)},

		{"engine/note on", engine(0, 0, 0)},
		{"engine/mid attack", engine(0.005, 0.45, 0.55)},
		{"engine/sustain", engine(0.1, 1, 1)},
		{"engine/sustain through rest step", engine(0.2, 1, 1)},
		{"engine/release", engine(0.3, 0.01, 0.99)},
		{"engine/silent after release", engine(0.4, 0, 0)},

		{"engine/pitch kept through release", func(t *testing.T) {
			s := oneTrack("f/1000*g", ModeFloat, 8000)
			s.Tracks[0].Env = ADSR{On: true, S: 1, R: 1, Gate: 1}
			s.Patterns[0].Notes[0][0] = 69
			out := mustNew(t, 8000, s).Render(1500) // step 0 is 1000 samples, then a rest
			if out[1200] != 0.44 {
				t.Errorf("f during release = %v, want 0.44 (440 Hz)", out[1200])
			}
		}},

		{"sanitize/clamps and defaults", func(t *testing.T) {
			s := oneTrack("t", ModeByte, 8000)
			s.Tracks[0].Env = ADSR{A: -1, D: 99, S: 2, R: math.NaN(), Gate: 0}
			got := sanitize(s).Tracks[0].Env
			if want := (ADSR{A: 0, D: MaxDecay, S: 1, R: 0, Gate: 1}); got != want {
				t.Errorf("got %+v, want %+v", got, want)
			}
		}},
	})
}

func TestKnobs(t *testing.T) {
	e := mustNew(t, 8000, oneTrack("k1+k2*2+k3*4+k4*8", ModeFloat, 8000))
	formula := func(knobs [4]float64, want float32) func(t *testing.T) {
		return func(t *testing.T) {
			e.EditSong(func(s *Song) { s.Tracks[0].Knobs = knobs }) // live edit
			if v := e.Render(1)[0]; v != want {
				t.Errorf("got %v, want %v", v, want)
			}
		}
	}
	sanitized := func(knobs [4]float64, names [4]string) Track {
		s := oneTrack("t", ModeByte, 8000)
		s.Tracks[0].Knobs, s.Tracks[0].KnobNames = knobs, names
		return sanitize(s).Tracks[0]
	}

	run(t, []testCase{
		{"formula/all knobs", formula([4]float64{0.0625, 0.0625, 0.0625, 0.0625}, 0.9375)},
		{"formula/k4 off", formula([4]float64{0.0625, 0.0625, 0.0625, 0}, 0.4375)},
		{"formula/only k1", formula([4]float64{0.5, 0, 0, 0}, 0.5)},
		{"sanitize/values clamped, NaN to 0", func(t *testing.T) {
			if got := sanitized([4]float64{-1, 2, math.NaN(), 0.5}, [4]string{}).Knobs; got != [4]float64{0, 1, 0, 0.5} {
				t.Errorf("got %v", got)
			}
		}},
		{"sanitize/empty names filled", func(t *testing.T) {
			if got := sanitized([4]float64{}, [4]string{"a", "", "c", ""}).KnobNames; got != [4]string{"a", "k2", "c", "k4"} {
				t.Errorf("got %v", got)
			}
		}},
	})
}
