package synth

import (
	"encoding/binary"
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

func TestByteModeMatchesFormula(t *testing.T) {
	e := mustNew(t, 8000, oneTrack("t&255", ModeByte, 8000)) // 1:1, no resampling
	for i, v := range e.Render(300) {
		if want := (float32(i&255) - 128) / 128; v != want {
			t.Fatalf("sample %d = %v, want %v", i, v, want)
		}
	}
}

func TestResampleHolds(t *testing.T) {
	e := mustNew(t, 32000, oneTrack("t*16", ModeByte, 8000)) // 4 output samples per tick
	out := e.Render(16)
	for i := 0; i < 16; i += 4 {
		for j := 1; j < 4; j++ {
			if out[i+j] != out[i] {
				t.Fatalf("sample %d = %v, want held %v", i+j, out[i+j], out[i])
			}
		}
	}
	if out[0] == out[4] {
		t.Fatal("value did not advance")
	}
}

func TestTracksWithDifferentRatesStayInSync(t *testing.T) {
	s := oneTrack("g", ModeFloat, 8000)
	fast := s.Tracks[0]
	fast.Rate = 44100
	s.Tracks = append(s.Tracks, fast)
	s.Patterns[0].Notes = [][Steps]int{rests(), rests()}
	s.Patterns[0].Notes[0][1] = 60
	s.Patterns[0].Notes[1][1] = 60
	e := mustNew(t, 44100, s)
	stepLen := 44100 * 60 / 120 / 4 // 5512.5 output samples
	e.Render(stepLen + 100)
	sc := make([]float32, 1)
	e.Scope(sc, 0)
	slow := sc[0]
	e.Scope(sc, 1)
	if slow != 1 || sc[0] != 1 {
		t.Fatalf("both tracks should be gated on in step 2: %v %v", slow, sc[0])
	}
}

func TestArrangementOrder(t *testing.T) {
	s := oneTrack("s+16*g", ModeFloat, 8000)
	s.Tracks[0].Formula = "f" // frequency tells us which pattern plays
	a, b := rests(), rests()
	a[0], b[0] = 69, 81 // A4 = 440 Hz, A5 = 880 Hz
	s.Patterns = []Pattern{{Notes: [][Steps]int{a}}, {Notes: [][Steps]int{b}}}
	s.Order = []int{1, 0, 1}
	e := mustNew(t, 8000, s)
	bar := 8000 * 60 / 120 / 4 * Steps
	if got := e.SongFrames(); got != 3*bar {
		t.Fatalf("SongFrames = %d, want %d", got, 3*bar)
	}

	pick := func(slot int) int {
		e.Seek(slot)
		e.Render(10)
		return e.Status().Pattern
	}
	if pick(0) != 1 || pick(1) != 0 || pick(2) != 1 {
		t.Fatal("order not followed")
	}
	e.SetPlayMode(PlayPattern, 0)
	e.Seek(2)
	e.Render(10)
	if st := e.Status(); st.Pattern != 0 || st.Mode != PlayPattern {
		t.Fatalf("pattern loop: %+v", st)
	}

	// The full render wraps exactly once through the order.
	e.SetPlayMode(PlaySong, 0)
	out := e.RenderSong()
	if len(out) != 3*bar || out[5] != 1 || out[bar+5] != 1 {
		t.Fatalf("len %d, gate %v %v", len(out), out[5], out[bar+5])
	}
}

func TestMuteSolo(t *testing.T) {
	s := oneTrack("0.25", ModeFloat, 8000)
	s.Tracks = append(s.Tracks, s.Tracks[0])
	s.Tracks[1].Formula = "0.5"
	e := mustNew(t, 8000, s)
	mix := func() float32 { return e.Render(1)[0] }
	if v := mix(); v != 0.75 {
		t.Fatalf("both: %v", v)
	}
	e.EditSong(func(s *Song) { s.Tracks[1].Mute = true })
	if v := mix(); v != 0.25 {
		t.Fatalf("mute: %v", v)
	}
	e.EditSong(func(s *Song) { s.Tracks[1].Mute = false; s.Tracks[1].Solo = true })
	if v := mix(); v != 0.5 {
		t.Fatalf("solo: %v", v)
	}
	e.EditSong(func(s *Song) { s.Tracks[0].Formula = "1"; s.Tracks[1].Solo = false })
	if v := mix(); v != 1 { // 1 + 0.5, clipped
		t.Fatalf("clip: %v", v)
	}
}

func TestRetriggerAndNoteAge(t *testing.T) {
	s := oneTrack("n", ModeFloat, 8000)
	s.Tracks[0].Formula = "n/1000"
	s.Patterns[0].Notes[0][0] = 60
	s.Patterns[0].Notes[0][1] = 60
	e := mustNew(t, 8000, s)
	out := e.Render(1001) // one step is 1000 samples
	if out[0] != 0 || out[999] != 0.999 || out[1000] != 0 {
		t.Fatalf("n: %v %v %v", out[0], out[999], out[1000])
	}
}

func TestEnvelopeAndPhase(t *testing.T) {
	s := oneTrack("e", ModeFloat, 8000)
	for i := range s.Patterns[0].Notes[0] {
		s.Patterns[0].Notes[0][i] = 69
	}
	e := mustNew(t, 8000, s)
	out := e.Render(1000)
	if out[0] != 1 || math.Abs(float64(out[500]-0.5)) > 1e-6 || out[999] > 0.01 {
		t.Fatalf("envelope %v %v %v", out[0], out[500], out[999])
	}
	s.Tracks[0].Formula = "p/256"
	e = mustNew(t, 8000, s)
	out = e.Render(3)
	if want := float32(440.0 / 8000); math.Abs(float64(out[1]-want)) > 1e-6 {
		t.Fatalf("phase step %v, want %v", out[1], want)
	}
	if ph := e.Status().Phase[0]; ph <= 0 || ph >= 256 {
		t.Fatalf("phase %v", ph)
	}
}

func TestSetFormula(t *testing.T) {
	e := mustNew(t, 44100, DefaultSong())
	if err := e.SetFormula(1, "t>>"); err == nil {
		t.Fatal("want error")
	}
	if e.Song().Tracks[1].Formula != DefaultSong().Tracks[1].Formula {
		t.Fatal("formula replaced")
	}
	if err := e.SetFormula(1, "t&t>>8"); err != nil || e.Song().Tracks[1].Formula != "t&t>>8" {
		t.Fatal(err)
	}
	if err := e.SetFormula(99, "t"); err == nil {
		t.Fatal("want error for missing track")
	}
}

func TestLoadErrorsKeepOldSong(t *testing.T) {
	e := mustNew(t, 44100, DefaultSong())
	bad := DefaultSong()
	bad.Tracks[2].Formula = "t>>"
	err := e.Load(bad)
	if err == nil || !strings.Contains(err.Error(), "track 3 (bass)") {
		t.Fatalf("err = %v", err)
	}
	if e.Song().Tracks[2].Formula != DefaultSong().Tracks[2].Formula {
		t.Fatal("bad load replaced the song")
	}
	if _, err := New(44100, bad); err == nil {
		t.Fatal("New must reject a bad formula")
	}
}

func TestEditSongAddRemoveTracks(t *testing.T) {
	e := mustNew(t, 8000, DefaultSong())
	e.Render(100)
	e.EditSong(func(s *Song) { s.Tracks = append(s.Tracks, NewTrack("new")) })
	s := e.Song()
	if len(s.Tracks) != 5 || len(s.Patterns[1].Notes) != 5 {
		t.Fatalf("tracks %d, pattern rows %d", len(s.Tracks), len(s.Patterns[1].Notes))
	}
	e.EditSong(func(s *Song) {
		s.Tracks = append(s.Tracks[:0], s.Tracks[1:]...)
		for i := range s.Patterns {
			s.Patterns[i].Notes = s.Patterns[i].Notes[1:]
		}
	})
	if s := e.Song(); len(s.Tracks) != 4 || s.Tracks[0].Name != "hats" {
		t.Fatalf("after remove: %d %s", len(s.Tracks), s.Tracks[0].Name)
	}
	e.Render(100) // voices must match tracks, no panic

	e.EditSong(func(s *Song) { s.Tracks[0].Formula = "t>>" })
	if tr := e.Song().Tracks[0]; tr.Formula != silence(tr.Mode) {
		t.Fatalf("invalid formula should be replaced by silence, got %q", tr.Formula)
	}
	if len(e.Status().T) != 4 {
		t.Fatal("status per track")
	}
}

func TestResetRewinds(t *testing.T) {
	e := mustNew(t, 8000, DefaultSong())
	e.Render(20000)
	e.Reset()
	if st := e.Status(); st.T[0] != 0 || st.Slot != 0 || st.Step != 0 {
		t.Fatalf("after reset %+v", st)
	}
}

func TestScopeReturnsLatestPreVolume(t *testing.T) {
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
}

func TestFloatModeClampsAndSilencesNaN(t *testing.T) {
	for formula, want := range map[string]float32{"5": 1, "-5": -1, "0/0": 0} {
		e := mustNew(t, 8000, oneTrack(formula, ModeFloat, 8000))
		if v := e.Render(1)[0]; v != want {
			t.Errorf("%s -> %v, want %v", formula, v, want)
		}
	}
}

func TestReadStereoF32(t *testing.T) {
	e := mustNew(t, 44100, oneTrack("0.5", ModeFloat, 8000))
	buf := make([]byte, 8*4+3) // trailing partial frame is not written
	n, _ := e.Read(buf)
	if n != 32 {
		t.Fatalf("n = %d", n)
	}
	l := math.Float32frombits(binary.LittleEndian.Uint32(buf[0:]))
	r := math.Float32frombits(binary.LittleEndian.Uint32(buf[4:]))
	if l != 0.5 || r != 0.5 {
		t.Fatalf("l=%v r=%v", l, r)
	}
}

func TestDefaultSongIsAudible(t *testing.T) {
	e := mustNew(t, 44100, DefaultSong())
	out := e.RenderSong()
	var peak float32
	for _, v := range out {
		peak = max(peak, float32(math.Abs(float64(v))))
	}
	if peak < 0.2 {
		t.Fatalf("default song peak %v", peak)
	}
	if secs := float64(len(out)) / 44100; secs < 5 || secs > 60 {
		t.Fatalf("default song is %.1fs", secs)
	}
}

func TestPresetsCompile(t *testing.T) {
	for _, pr := range Presets {
		if _, err := New(44100, oneTrack(pr.Formula, pr.Mode, pr.Rate)); err != nil {
			t.Errorf("%s: %v", pr.Name, err)
		}
	}
}

func TestNoteNameAndMode(t *testing.T) {
	if NoteName(60) != "C4" || NoteName(69) != "A4" || NoteName(Rest) != "--" || NoteName(200) != "--" {
		t.Fatal(NoteName(60), NoteName(69))
	}
	if ModeByte.String() != "BYTE" || ModeFloat.String() != "FLOAT" {
		t.Fatal("mode names")
	}
}

func TestShapes(t *testing.T) {
	for _, name := range Shapes {
		w := Shape(name)
		lo, hi := 255.0, 0.0
		for _, v := range w {
			lo, hi = min(lo, v), max(hi, v)
		}
		if lo < 0 || hi > 255 || hi-lo < 200 {
			t.Errorf("%s spans %v..%v", name, lo, hi)
		}
	}
	if w := Shape("square"); w[0] != 255 || w[255] != 0 {
		t.Fatal("square", w[0], w[255])
	}
	if w := Shape("unknown"); w[0] != 128 {
		t.Fatal("unknown shape should be flat", w[0])
	}
}

// Regression: growing the order mid-song used to move playback to another
// slot because the position was taken modulo the order length.
func TestOrderEditKeepsSlot(t *testing.T) {
	e := mustNew(t, 8000, DefaultSong())
	bar := 8000 * 60 / 124 / 4 * Steps
	e.Render(len(DefaultSong().Order)*bar + 3*bar + 100) // second pass, slot 4
	before := e.Status().Slot
	e.EditSong(func(s *Song) { s.Order = append(s.Order, 0) })
	e.Render(10)
	if after := e.Status().Slot; after != before {
		t.Fatalf("slot %d -> %d after appending to the order", before, after)
	}
	// Shrinking below the current slot wraps into range instead of panicking.
	e.EditSong(func(s *Song) { s.Order = s.Order[:2] })
	e.Render(10)
	if st := e.Status(); st.Slot < 0 || st.Slot > 1 {
		t.Fatalf("slot %d outside a 2-slot song", st.Slot)
	}
}

// Regression: leaving pattern mode after many loops used to land on a random
// slot.
func TestPatternLoopThenSongStartsAtSongStart(t *testing.T) {
	e := mustNew(t, 8000, DefaultSong())
	bar := 8000 * 60 / 124 / 4 * Steps
	e.Render(bar / 2) // halfway through slot 0
	e.SetPlayMode(PlayPattern, 1)
	e.Render(5*bar + 10)
	step := e.Status().Step
	e.SetPlayMode(PlaySong, 0)
	e.Render(1)
	if st := e.Status(); st.Slot != 0 || st.Step != step {
		t.Fatalf("after loops: slot %d step %d, want slot 0 step %d", st.Slot, st.Step, step)
	}
}

// Regression: removing a track used to reset the voices of every track to
// its right (silence until the next step, t from another track's rate).
func TestRemoveTrackKeepsOtherVoices(t *testing.T) {
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
	st := e.Status()
	if st.T[0] != hatsT {
		t.Fatalf("hats t = %d after removing kick, want %d", st.T[0], hatsT)
	}
	e.Render(1)
	sc := make([]float32, 1)
	e.Scope(sc, 0)
	if sc[0] == 0 {
		t.Fatal("hats went silent after removing another track")
	}
}

func TestTwoTracksWithSameFormulaKeepOwnVoices(t *testing.T) {
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
		t.Fatalf("remaining track got phase %v, want its own %v", got, phase)
	}
}

func TestNewTrackJoinsClockAndCurrentNote(t *testing.T) {
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
	if tt := e.Status().T[4]; tt != 8000 {
		t.Fatalf("new track t = %d, want 8000 (one second at its rate)", tt)
	}
	e.Render(1)
	sc := make([]float32, 1)
	e.Scope(sc, 4)
	if sc[0] != 1 {
		t.Fatal("new track should sound in the current step")
	}
}

func TestBrokenFormulaFallsBackToSilencePerMode(t *testing.T) {
	for _, m := range []Mode{ModeByte, ModeFloat} {
		e := mustNew(t, 8000, oneTrack("t", m, 8000))
		e.EditSong(func(s *Song) { s.Tracks[0].Formula = "t>>" })
		if v := e.Render(1)[0]; v != 0 {
			t.Fatalf("%v: fallback output %v, want 0", m, v)
		}
	}
}

// Regression: rounding could make the last rendered sample start the first
// step of the next pass.
func TestRenderSongNeverEntersNextPass(t *testing.T) {
	for _, rate := range []int{44100, 48000} {
		for bpm := 30.0; bpm <= 300; bpm += 0.5 {
			s := oneTrack("g", ModeFloat, 8000)
			s.BPM = bpm
			s.Patterns[0].Notes[0][0] = 60 // only the first step sounds
			e := mustNew(t, rate, s)
			out := e.RenderSong()
			if out[len(out)-1] != 0 {
				t.Fatalf("rate %d bpm %v: last sample is in the next pass", rate, bpm)
			}
		}
	}
}
