package synth

import (
	"encoding/binary"
	"fmt"
	"math"
	"strings"
	"testing"
)

// oneTrack builds a single-track song with one pattern full of rests.
func oneTrack(formula string, mode Mode, rate int) Song {
	t := NewTrack("t")
	t.Formula, t.Mode, t.Rate, t.Volume = formula, mode, rate, 1
	return Song{BPM: 120, Volume: 1, Tracks: []Track{t},
		Patterns: []Pattern{{Notes: [][Steps]int{rests()}}}, Order: []int{0}}
}

func mustNew(t *testing.T, rate int, s Song) *Engine {
	t.Helper()
	e, err := New(rate, s)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func scopeAt(e *Engine, which int) float32 {
	sc := make([]float32, 1)
	e.Scope(sc, which)
	return sc[0]
}

func TestRendering(t *testing.T) {
	tests := []testCase{
		{"byte mode is formula & 255", func(t *testing.T) {
			e := mustNew(t, 8000, oneTrack("t&255", ModeByte, 8000)) // 1:1, no resampling
			for i, v := range e.Render(300) {
				if want := (float32(i&255) - 128) / 128; v != want {
					t.Fatalf("sample %d = %v, want %v", i, v, want)
				}
			}
		}},
		{"low rates are held between ticks", func(t *testing.T) {
			out := mustNew(t, 32000, oneTrack("t*16", ModeByte, 8000)).Render(16) // 4 samples per tick
			for i := 0; i < 16; i += 4 {
				for j := 1; j < 4; j++ {
					if out[i+j] != out[i] {
						t.Fatalf("sample %d = %v, want held %v", i+j, out[i+j], out[i])
					}
				}
			}
			if out[0] == out[4] {
				t.Error("value did not advance")
			}
		}},
		{"Read writes interleaved stereo float32", func(t *testing.T) {
			e := mustNew(t, 44100, oneTrack("0.5", ModeFloat, 8000))
			buf := make([]byte, 8*4+3) // trailing partial frame is not written
			if n, _ := e.Read(buf); n != 32 {
				t.Fatalf("n = %d", n)
			}
			l := math.Float32frombits(binary.LittleEndian.Uint32(buf[0:]))
			r := math.Float32frombits(binary.LittleEndian.Uint32(buf[4:]))
			if l != 0.5 || r != 0.5 {
				t.Errorf("l=%v r=%v", l, r)
			}
		}},
		{"scope returns the latest samples, track before volume", func(t *testing.T) {
			s := oneTrack("t/1000", ModeFloat, 8000)
			s.Tracks[0].Volume = 0.5
			e := mustNew(t, 8000, s)
			out := e.Render(500)
			sc, mix := make([]float32, 4), make([]float32, 4)
			e.Scope(sc, 0)
			e.Scope(mix, ScopeMix)
			for i := range sc {
				want := float32(496+i) / 1000
				if math.Abs(float64(sc[i]-want)) > 1e-6 || mix[i] != out[496+i] || out[496+i] != want*0.5 {
					t.Fatalf("scope[%d] track %v mix %v out %v, want %v", i, sc[i], mix[i], out[496+i], want)
				}
			}
		}},
	}
	for _, c := range []struct {
		formula string
		want    float32
	}{{"5", 1}, {"-5", -1}, {"0/0", 0}} {
		tests = append(tests, testCase{"float mode clamps/" + c.formula, func(t *testing.T) {
			if v := mustNew(t, 8000, oneTrack(c.formula, ModeFloat, 8000)).Render(1)[0]; v != c.want {
				t.Errorf("got %v, want %v", v, c.want)
			}
		}})
	}
	run(t, tests)
}

func TestSequencer(t *testing.T) {
	run(t, []testCase{
		{"tracks with different rates stay in sync", func(t *testing.T) {
			s := oneTrack("g", ModeFloat, 8000)
			fast := s.Tracks[0]
			fast.Rate = 44100
			s.Tracks = append(s.Tracks, fast)
			s.Patterns[0].Notes = [][Steps]int{rests(), rests()}
			s.Patterns[0].Notes[0][1] = 60
			s.Patterns[0].Notes[1][1] = 60
			e := mustNew(t, 44100, s)
			e.Render(44100*60/120/4 + 100) // into step 2
			if slow, fast := scopeAt(e, 0), scopeAt(e, 1); slow != 1 || fast != 1 {
				t.Errorf("both tracks should be gated on in step 2: %v %v", slow, fast)
			}
		}},
		{"n counts from each note and resets on retrigger", func(t *testing.T) {
			s := oneTrack("n/1000", ModeFloat, 8000)
			s.Patterns[0].Notes[0][0] = 60
			s.Patterns[0].Notes[0][1] = 60
			out := mustNew(t, 8000, s).Render(1001) // one step is 1000 samples
			if out[0] != 0 || out[999] != 0.999 || out[1000] != 0 {
				t.Errorf("n: %v %v %v", out[0], out[999], out[1000])
			}
		}},
		{"e decays over each step without ADSR", func(t *testing.T) {
			s := oneTrack("e", ModeFloat, 8000)
			for i := range s.Patterns[0].Notes[0] {
				s.Patterns[0].Notes[0][i] = 69
			}
			out := mustNew(t, 8000, s).Render(1000)
			if out[0] != 1 || math.Abs(float64(out[500]-0.5)) > 1e-6 || out[999] > 0.01 {
				t.Errorf("envelope %v %v %v", out[0], out[500], out[999])
			}
		}},
		{"p advances 256 per period of the note", func(t *testing.T) {
			s := oneTrack("p/256", ModeFloat, 8000)
			s.Patterns[0].Notes[0][0] = 69
			e := mustNew(t, 8000, s)
			out := e.Render(3)
			if want := float32(440.0 / 8000); math.Abs(float64(out[1]-want)) > 1e-6 {
				t.Errorf("phase step %v, want %v", out[1], want)
			}
			if ph := e.Status().Phase[0]; ph <= 0 || ph >= 256 {
				t.Errorf("phase %v", ph)
			}
		}},
		{"reset rewinds time and position", func(t *testing.T) {
			e := mustNew(t, 8000, DefaultSong())
			e.Render(20000)
			e.Reset()
			if st := e.Status(); st.T[0] != 0 || st.Slot != 0 || st.Step != 0 {
				t.Errorf("after reset %+v", st)
			}
		}},
	})
}

func TestArrangement(t *testing.T) {
	// Pattern A plays 440 Hz on step 0, B plays 880 Hz; the order is B A B.
	song := func() Song {
		s := oneTrack("f", ModeFloat, 8000)
		a, b := rests(), rests()
		a[0], b[0] = 69, 81
		s.Patterns = []Pattern{{Notes: [][Steps]int{a}}, {Notes: [][Steps]int{b}}}
		s.Order = []int{1, 0, 1}
		return s
	}
	const bar = 8000 * 60 / 120 / 4 * Steps
	demoBar := 8000 * 60 / 124 / 4 * Steps

	tests := []testCase{
		{"song length is the order times a bar", func(t *testing.T) {
			if got := mustNew(t, 8000, song()).SongFrames(); got != 3*bar {
				t.Errorf("SongFrames = %d, want %d", got, 3*bar)
			}
		}},
		{"loop pattern ignores the order", func(t *testing.T) {
			e := mustNew(t, 8000, song())
			e.SetPlayMode(PlayPattern, 0)
			e.Seek(2)
			e.Render(10)
			if st := e.Status(); st.Pattern != 0 || st.Mode != PlayPattern {
				t.Errorf("%+v", st)
			}
		}},
		{"rendering the song passes the order exactly once", func(t *testing.T) {
			out := mustNew(t, 8000, song()).RenderSong()
			if len(out) != 3*bar || out[5] != 1 || out[bar+5] != 1 {
				t.Errorf("len %d, gate %v %v", len(out), out[5], out[bar+5])
			}
		}},
		// Regression: growing the order mid-song used to move playback to
		// another slot because the position was taken modulo the order length.
		{"editing the order keeps the current slot", func(t *testing.T) {
			e := mustNew(t, 8000, DefaultSong())
			e.Render(len(DefaultSong().Order)*demoBar + 3*demoBar + 100) // second pass, slot 4
			before := e.Status().Slot
			e.EditSong(func(s *Song) { s.Order = append(s.Order, 0) })
			e.Render(10)
			if after := e.Status().Slot; after != before {
				t.Errorf("slot %d -> %d after appending to the order", before, after)
			}
		}},
		{"shrinking the order below the position wraps into range", func(t *testing.T) {
			e := mustNew(t, 8000, DefaultSong())
			e.Render(5 * demoBar)
			e.EditSong(func(s *Song) { s.Order = s.Order[:2] })
			e.Render(10)
			if st := e.Status(); st.Slot < 0 || st.Slot > 1 {
				t.Errorf("slot %d outside a 2-slot song", st.Slot)
			}
		}},
		// Regression: leaving pattern mode after many loops used to land on a
		// random slot.
		{"leaving loop pattern resumes at the song start on the same step", func(t *testing.T) {
			e := mustNew(t, 8000, DefaultSong())
			e.Render(demoBar / 2)
			e.SetPlayMode(PlayPattern, 1)
			e.Render(5*demoBar + 10)
			step := e.Status().Step
			e.SetPlayMode(PlaySong, 0)
			e.Render(1)
			if st := e.Status(); st.Slot != 0 || st.Step != step {
				t.Errorf("slot %d step %d, want slot 0 step %d", st.Slot, st.Step, step)
			}
		}},
	}
	for slot, want := range []int{1, 0, 1} {
		tests = append(tests, testCase{fmt.Sprintf("seek to slot %d plays its pattern", slot), func(t *testing.T) {
			e := mustNew(t, 8000, song())
			e.Seek(slot)
			e.Render(10)
			if got := e.Status().Pattern; got != want {
				t.Errorf("pattern %d, want %d", got, want)
			}
		}})
	}
	// Regression: rounding could make the last rendered sample start the
	// first step of the next pass.
	for _, rate := range []int{44100, 48000} {
		tests = append(tests, testCase{fmt.Sprintf("song end never enters the next pass/%d Hz", rate), func(t *testing.T) {
			for bpm := 30.0; bpm <= 300; bpm += 0.5 {
				s := oneTrack("g", ModeFloat, 8000)
				s.BPM = bpm
				s.Patterns[0].Notes[0][0] = 60 // only the first step sounds
				if out := mustNew(t, rate, s).RenderSong(); out[len(out)-1] != 0 {
					t.Fatalf("bpm %v: last sample is in the next pass", bpm)
				}
			}
		}})
	}
	run(t, tests)
}

func TestMixer(t *testing.T) {
	// Track 0 outputs 0.25, track 1 outputs 0.5.
	tests := []struct {
		name string
		edit func(s *Song)
		want float32
	}{
		{"both tracks", func(s *Song) {}, 0.75},
		{"mute", func(s *Song) { s.Tracks[1].Mute = true }, 0.25},
		{"solo", func(s *Song) { s.Tracks[1].Solo = true }, 0.5},
		{"two solos", func(s *Song) { s.Tracks[0].Solo, s.Tracks[1].Solo = true, true }, 0.75},
		{"clipped at 1", func(s *Song) { s.Tracks[0].Formula = "1" }, 1},
		{"master volume", func(s *Song) { s.Volume = 0.5 }, 0.375},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := oneTrack("0.25", ModeFloat, 8000)
			s.Tracks = append(s.Tracks, s.Tracks[0])
			s.Tracks[1].Formula = "0.5"
			s.Patterns[0].Notes = append(s.Patterns[0].Notes, rests())
			e := mustNew(t, 8000, s)
			e.EditSong(tt.edit)
			if v := e.Render(1)[0]; v != tt.want {
				t.Errorf("mix %v, want %v", v, tt.want)
			}
		})
	}
}

func TestTrackEditing(t *testing.T) {
	tests := []testCase{
		{"SetFormula rejects a broken formula", func(t *testing.T) {
			e := mustNew(t, 44100, DefaultSong())
			if err := e.SetFormula(1, "t>>"); err == nil {
				t.Fatal("want error")
			}
			if e.Song().Tracks[1].Formula != DefaultSong().Tracks[1].Formula {
				t.Error("formula replaced")
			}
		}},
		{"SetFormula installs a valid formula", func(t *testing.T) {
			e := mustNew(t, 44100, DefaultSong())
			if err := e.SetFormula(1, "t&t>>8"); err != nil || e.Song().Tracks[1].Formula != "t&t>>8" {
				t.Error(err)
			}
		}},
		{"SetFormula on a missing track", func(t *testing.T) {
			if err := mustNew(t, 44100, DefaultSong()).SetFormula(99, "t"); err == nil {
				t.Error("want error")
			}
		}},
		{"a bad load names the track and keeps the old song", func(t *testing.T) {
			e := mustNew(t, 44100, DefaultSong())
			bad := DefaultSong()
			bad.Tracks[2].Formula = "t>>"
			if err := e.Load(bad); err == nil || !strings.Contains(err.Error(), "track 3 (bass)") {
				t.Fatalf("err = %v", err)
			}
			if e.Song().Tracks[2].Formula != DefaultSong().Tracks[2].Formula {
				t.Error("bad load replaced the song")
			}
			if _, err := New(44100, bad); err == nil {
				t.Error("New must reject a bad formula")
			}
		}},
		{"adding and removing tracks keeps voices and rows in step", func(t *testing.T) {
			e := mustNew(t, 8000, DefaultSong())
			e.Render(100)
			e.EditSong(func(s *Song) { s.Tracks = append(s.Tracks, NewTrack("new")) })
			if s := e.Song(); len(s.Tracks) != 5 || len(s.Patterns[1].Notes) != 5 {
				t.Fatalf("tracks %d, pattern rows %d", len(s.Tracks), len(s.Patterns[1].Notes))
			}
			e.EditSong(func(s *Song) {
				s.Tracks = append(s.Tracks[:0], s.Tracks[1:]...)
				for i := range s.Patterns {
					s.Patterns[i].Notes = s.Patterns[i].Notes[1:]
				}
			})
			if s := e.Song(); len(s.Tracks) != 4 || s.Tracks[0].Name != "hats" || len(e.Status().T) != 4 {
				t.Fatalf("after remove: %d %s", len(s.Tracks), s.Tracks[0].Name)
			}
			e.Render(100) // voices must match tracks, no panic
		}},
		// Regression: removing a track used to reset the voices of every
		// track to its right (silence until the next step, t from another
		// track's rate).
		{"removing a track keeps the other voices playing", func(t *testing.T) {
			s := DefaultSong()
			for i := range s.Patterns[0].Notes[1] {
				s.Patterns[0].Notes[1][i] = 60 // hats on every step
			}
			e := mustNew(t, 44100, s)
			e.Render(1000)
			hatsT := e.Status().T[1]
			e.EditSong(func(s *Song) {
				s.Tracks = s.Tracks[1:]
				for i := range s.Patterns {
					s.Patterns[i].Notes = s.Patterns[i].Notes[1:]
				}
			})
			if got := e.Status().T[0]; got != hatsT {
				t.Fatalf("hats t = %d after removing kick, want %d", got, hatsT)
			}
			e.Render(1)
			if scopeAt(e, 0) == 0 {
				t.Error("hats went silent after removing another track")
			}
		}},
		{"two tracks with the same formula keep their own voices", func(t *testing.T) {
			s := oneTrack("p", ModeFloat, 8000)
			s.Tracks = append(s.Tracks, s.Tracks[0])
			s.Patterns[0].Notes = [][Steps]int{rests(), rests()}
			s.Patterns[0].Notes[1][0] = 69
			e := mustNew(t, 8000, s)
			e.Render(50)
			phase := e.Status().Phase[1]
			e.EditSong(func(s *Song) {
				s.Tracks = s.Tracks[1:]
				s.Patterns[0].Notes = s.Patterns[0].Notes[1:]
			})
			if got := e.Status().Phase[0]; got != phase {
				t.Errorf("remaining track got phase %v, want its own %v", got, phase)
			}
		}},
		{"a new track joins the clock and the current note", func(t *testing.T) {
			e := mustNew(t, 44100, DefaultSong())
			e.Render(44100) // one second
			e.EditSong(func(s *Song) {
				tr := NewTrack("new")
				tr.Rate, tr.Formula, tr.Mode = 8000, "g", ModeFloat
				s.Tracks = append(s.Tracks, tr)
				for i := range s.Patterns {
					s.Patterns[i].Notes = append(s.Patterns[i].Notes, notes(map[int]int{}))
					for k := range s.Patterns[i].Notes[4] {
						s.Patterns[i].Notes[4][k] = 60
					}
				}
			})
			if got := e.Status().T[4]; got != 8000 {
				t.Fatalf("new track t = %d, want 8000 (one second at its rate)", got)
			}
			e.Render(1)
			if scopeAt(e, 4) != 1 {
				t.Error("new track should sound in the current step")
			}
		}},
	}
	for _, m := range []Mode{ModeByte, ModeFloat} {
		tests = append(tests, testCase{"a broken formula from EditSong becomes silence/" + m.String(), func(t *testing.T) {
			e := mustNew(t, 8000, oneTrack("t", m, 8000))
			e.EditSong(func(s *Song) { s.Tracks[0].Formula = "t>>" })
			if tr := e.Song().Tracks[0]; tr.Formula != silence(m) {
				t.Errorf("formula %q, want %q", tr.Formula, silence(m))
			}
			if v := e.Render(1)[0]; v != 0 {
				t.Errorf("output %v, want 0", v)
			}
		}})
	}
	run(t, tests)
}

func TestPresetsAndShapes(t *testing.T) {
	tests := []testCase{
		{"default song is audible and a sensible length", func(t *testing.T) {
			out := mustNew(t, 44100, DefaultSong()).RenderSong()
			peak, _, nan := stats(out)
			if nan || peak < 0.2 || peak > 1 {
				t.Errorf("peak %v nan %v", peak, nan)
			}
			if secs := float64(len(out)) / 44100; secs < 5 || secs > 60 {
				t.Errorf("default song is %.1fs", secs)
			}
		}},
		{"square shape", func(t *testing.T) {
			if w := Shape("square"); w[0] != 255 || w[255] != 0 {
				t.Error(w[0], w[255])
			}
		}},
		{"unknown shape is flat", func(t *testing.T) {
			if w := Shape("unknown"); w[0] != 128 {
				t.Error(w[0])
			}
		}},
	}
	for _, pr := range Presets {
		tests = append(tests, testCase{"preset compiles/" + pr.Name, func(t *testing.T) {
			if _, err := New(44100, oneTrack(pr.Formula, pr.Mode, pr.Rate)); err != nil {
				t.Error(err)
			}
		}})
	}
	for _, name := range Shapes {
		tests = append(tests, testCase{"shape spans the range/" + name, func(t *testing.T) {
			lo, hi := 255.0, 0.0
			for _, v := range Shape(name) {
				lo, hi = min(lo, v), max(hi, v)
			}
			if lo < 0 || hi > 255 || hi-lo < 200 {
				t.Errorf("spans %v..%v", lo, hi)
			}
		}})
	}
	for _, c := range []struct {
		got, want string
	}{
		{NoteName(60), "C4"},
		{NoteName(69), "A4"},
		{NoteName(Rest), "--"},
		{NoteName(200), "--"},
		{ModeByte.String(), "BYTE"},
		{ModeFloat.String(), "FLOAT"},
	} {
		tests = append(tests, testCase{"name/" + c.want, func(t *testing.T) {
			if c.got != c.want {
				t.Errorf("got %q, want %q", c.got, c.want)
			}
		}})
	}
	run(t, tests)
}
