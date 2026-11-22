package ui

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pristupaanastasia/bitwave/internal/expr"
	"github.com/pristupaanastasia/bitwave/internal/synth"
)

func TestLineEdit(t *testing.T) {
	var l lineEdit
	l.Set("t>>8")
	l.Home()
	l.Insert([]rune("("))
	l.End()
	l.Insert([]rune(")&255"))
	if l.String() != "(t>>8)&255" {
		t.Fatal(l.String())
	}
	l.Left()
	l.Left()
	l.Left()
	if !l.Backspace() || l.String() != "(t>>8)255" {
		t.Fatal(l.String())
	}
	if !l.Delete() || l.String() != "(t>>8)55" {
		t.Fatal(l.String())
	}
	l.End()
	if l.Delete() {
		t.Fatal("delete at end")
	}
	l.Right()
	l.Home()
	if l.Backspace() {
		t.Fatal("backspace at start")
	}
	l.Left()
	l.Insert(nil)
	if l.cur != 0 {
		t.Fatal(l.cur)
	}
}

func TestFindTrigger(t *testing.T) {
	buf := []float32{0.5, -0.5, -0.2, 0.1, 0.4}
	if i := findTrigger(buf, 0, len(buf)); i != 3 {
		t.Fatal(i)
	}
	if i := findTrigger([]float32{1, 1, 1}, 0, 3); i != 0 {
		t.Fatal("no crossing should return lo", i)
	}
	if i := findTrigger(buf, 0, 100); i != 3 {
		t.Fatal("hi beyond len", i)
	}
}

func TestColumns(t *testing.T) {
	lo, hi := columns([]float32{0, 1, -1, 0.5}, 2)
	if lo[0] != 0 || hi[0] != 1 || lo[1] != -1 || hi[1] != 0.5 {
		t.Fatal(lo, hi)
	}
	lo, hi = columns([]float32{0.25}, 3) // fewer samples than columns
	for i := range lo {
		if lo[i] != 0.25 || hi[i] != 0.25 {
			t.Fatal(lo, hi)
		}
	}
}

func TestToByte(t *testing.T) {
	for b := range 256 {
		s := (float32(b) - 128) / 128
		if toByte(s) != uint8(b) {
			t.Fatalf("toByte(%v) = %d, want %d", s, toByte(s), b)
		}
	}
	if toByte(5) != 255 || toByte(-5) != 0 {
		t.Fatal("clamp")
	}
}

func TestPaintWave(t *testing.T) {
	var w [expr.WaveSize]float64
	paintWave(&w, 10, 0, 14, 255)
	want := []float64{0, 64, 128, 191, 255}
	for i, v := range want {
		if w[10+i] != v {
			t.Fatalf("w[%d] = %v, want %v", 10+i, w[10+i], v)
		}
	}
	paintWave(&w, 20, 0, 18, 100) // reversed stroke
	if w[18] != 100 || w[20] != 0 {
		t.Fatal(w[18:21])
	}
	paintWave(&w, -5, 0, -5, 999) // clamped
	if w[0] != 255 {
		t.Fatal(w[0])
	}
}

func TestRect(t *testing.T) {
	r := rect{10, 10, 5, 5}
	if !r.contains(10, 10) || !r.contains(14, 14) || r.contains(15, 10) || r.contains(9, 12) {
		t.Fatal("contains")
	}
}

func TestStepRate(t *testing.T) {
	cases := []struct{ r, dir, want int }{
		{8000, 1, 11025}, {8000, -1, 4000}, {1000, -1, 1000}, {48000, 1, 48000},
		{9000, 1, 11025}, {9000, -1, 8000}, {60000, -1, 48000},
	}
	for _, c := range cases {
		if got := stepRate(c.r, c.dir); got != c.want {
			t.Errorf("stepRate(%d,%d) = %d, want %d", c.r, c.dir, got, c.want)
		}
	}
}

// newTestGame builds a Game without an audio player, enough for logic that
// does not touch playback.
func newTestGame(t *testing.T) *Game {
	t.Helper()
	eng, err := synth.New(SampleRate, synth.DefaultSong())
	if err != nil {
		t.Fatal(err)
	}
	return newGame(eng)
}

// press clicks the first button with the given label.
func press(t *testing.T, g *Game, label string) {
	t.Helper()
	g.layout()
	for _, b := range g.buttons {
		if b.onClick != nil && b.label() == label {
			g.click(b.r.x+1, b.r.y+1, false)
			return
		}
	}
	t.Fatalf("no button %q", label)
}

func TestTrackSelectionLoadsFormula(t *testing.T) {
	g := newTestGame(t)
	press(t, g, "3 bass")
	if g.track != 2 || g.edit.String() != synth.DefaultSong().Tracks[2].Formula {
		t.Fatalf("track %d formula %q", g.track, g.edit.String())
	}
	g.edit.Set("t&t>>8")
	g.applyFormula()
	if g.eng.Song().Tracks[2].Formula != "t&t>>8" || g.eng.Song().Tracks[0].Formula == "t&t>>8" {
		t.Fatal("formula went to the wrong track")
	}
	g.edit.Set("t>>")
	g.applyFormula()
	if g.formulaErr == "" || g.eng.Song().Tracks[2].Formula != "t&t>>8" {
		t.Fatal("bad formula must keep the old one")
	}
}

func TestMuteSoloButtons(t *testing.T) {
	g := newTestGame(t)
	press(t, g, "M")
	press(t, g, "S")
	tr := g.eng.Song().Tracks[0]
	if !tr.Mute || !tr.Solo {
		t.Fatalf("mute %v solo %v", tr.Mute, tr.Solo)
	}
}

func TestAddRemoveTrack(t *testing.T) {
	g := newTestGame(t)
	press(t, g, "+ TRACK")
	s := g.eng.Song()
	if len(s.Tracks) != 5 || g.track != 4 || len(s.Patterns[0].Notes) != 5 {
		t.Fatalf("tracks %d selected %d", len(s.Tracks), g.track)
	}
	for range synth.MaxTracks {
		g.addTrack()
	}
	if len(g.eng.Song().Tracks) != synth.MaxTracks || !strings.Contains(g.status, "at most") {
		t.Fatal("track limit", len(g.eng.Song().Tracks))
	}

	g.selectTrack(1) // hats
	press(t, g, "- TRACK")
	s = g.eng.Song()
	if len(s.Tracks) != synth.MaxTracks-1 || s.Tracks[1].Name != "bass" || len(s.Patterns[1].Notes) != len(s.Tracks) {
		t.Fatalf("after remove: %d tracks, [1]=%s", len(s.Tracks), s.Tracks[1].Name)
	}
	// The bass notes moved up with the track.
	if s.Patterns[0].Notes[1] != synth.DefaultSong().Patterns[0].Notes[2] {
		t.Fatal("notes did not follow the removed track")
	}
	for range synth.MaxTracks {
		g.removeTrack()
	}
	if len(g.eng.Song().Tracks) != 1 || !strings.Contains(g.status, "at least one") {
		t.Fatal("must keep one track")
	}
}

func TestPatternsAndArrangement(t *testing.T) {
	g := newTestGame(t)
	press(t, g, "B")
	if g.pattern != 1 {
		t.Fatal(g.pattern)
	}
	press(t, g, "COPY")
	s := g.eng.Song()
	if len(s.Patterns) != 3 || g.pattern != 2 || s.Patterns[2].Notes[3] != s.Patterns[1].Notes[3] {
		t.Fatal("copy pattern")
	}
	press(t, g, "NEW")
	s = g.eng.Song()
	if len(s.Patterns) != 4 || s.Patterns[3].Notes[0][0] != synth.Rest {
		t.Fatal("new pattern should be empty")
	}

	before := len(s.Order)
	press(t, g, "+ TO SONG")
	s = g.eng.Song()
	if len(s.Order) != before+1 || s.Order[before] != 3 {
		t.Fatal("append", s.Order)
	}
	press(t, g, "- FROM SONG")
	if len(g.eng.Song().Order) != before {
		t.Fatal("remove last")
	}

	// Right click on slot 0 puts the current pattern (D) there.
	g.click(orderR.x+1, orderR.y+1, true)
	if g.eng.Song().Order[0] != 3 {
		t.Fatal("right click", g.eng.Song().Order)
	}
	// Left click on slot 2 selects its pattern and seeks there.
	g.click(orderR.x+2*cellW+1, orderR.y+1, false)
	if g.pattern != g.eng.Song().Order[2] {
		t.Fatal("left click did not select the slot's pattern")
	}
	g.eng.Render(10)
	if st := g.eng.Status(); st.Slot != 2 {
		t.Fatalf("seek: slot %d", st.Slot)
	}
	g.click(orderR.x+orderR.w-1, orderR.y+1, false) // empty slot: no-op

	for range synth.MaxPatterns {
		g.newPattern(false)
	}
	if len(g.eng.Song().Patterns) != synth.MaxPatterns || !strings.Contains(g.status, "at most") {
		t.Fatal("pattern limit")
	}
	for range synth.MaxOrder {
		g.appendToSong()
	}
	if len(g.eng.Song().Order) != synth.MaxOrder || !strings.Contains(g.status, "limited") {
		t.Fatal("order limit")
	}
}

func TestPlayModeFollowsPattern(t *testing.T) {
	g := newTestGame(t)
	press(t, g, "PLAY SONG") // switches to pattern loop
	if g.playMode != synth.PlayPattern {
		t.Fatal("mode")
	}
	press(t, g, "B")
	g.eng.Render(10)
	if st := g.eng.Status(); st.Mode != synth.PlayPattern || st.Pattern != 1 {
		t.Fatalf("%+v", st)
	}
	press(t, g, "LOOP PATTERN")
	if g.playMode != synth.PlaySong {
		t.Fatal("back to song")
	}
}

func TestSequencerEditsSelectedTrackAndPattern(t *testing.T) {
	g := newTestGame(t)
	g.selectTrack(3)
	g.selectPattern(0)
	colW, rowH := seqR.w/synth.Steps, seqR.h/seqRows
	x, y := seqR.x+colW/2, seqR.y+rowH/2 // top row, first column
	g.click(x, y, false)
	top := g.lowNote + seqRows - 1
	s := g.eng.Song()
	if s.Patterns[0].Notes[3][0] != top || s.Patterns[1].Notes[3][0] == top {
		t.Fatal("note landed in the wrong place")
	}
	g.click(x, y, false)
	if g.eng.Song().Patterns[0].Notes[3][0] != synth.Rest {
		t.Fatal("toggle off")
	}
	g.selectTrack(0)
	press(t, g, "CLEAR")
	if g.eng.Song().Patterns[0].Notes[0] != [synth.Steps]int{-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1} {
		t.Fatal("clear")
	}
	press(t, g, "OCT +")
	press(t, g, "OCT -")
	press(t, g, "OCT -")
	if g.lowNote != 24 {
		t.Fatal(g.lowNote)
	}
}

func TestTrackControls(t *testing.T) {
	g := newTestGame(t)
	g.selectTrack(2) // bass: byte, 8000
	press(t, g, "MODE BYTE")
	press(t, g, "+")
	tr := g.eng.Song().Tracks[2]
	if tr.Mode != synth.ModeFloat {
		t.Fatal("mode")
	}
	// The first "+" on screen is BPM; check it moved and the track's rate
	// buttons work through stepRate.
	if g.eng.Song().BPM != synth.DefaultSong().BPM+2 {
		t.Fatal("bpm", g.eng.Song().BPM)
	}
	g.editTrack(func(tr *synth.Track) { tr.Rate = stepRate(tr.Rate, 1) })
	if g.eng.Song().Tracks[2].Rate != 11025 {
		t.Fatal("rate")
	}
	press(t, g, "SINE")
	if g.eng.Song().Tracks[2].Wave != synth.Shape("sine") {
		t.Fatal("shape")
	}
	press(t, g, ">")
	if g.preset != 0 || g.eng.Song().Tracks[2].Formula != synth.Presets[0].Formula {
		t.Fatal("preset")
	}
	press(t, g, "<")
	if g.preset != len(synth.Presets)-1 {
		t.Fatal("preset wrap")
	}
	press(t, g, "SHOW MIX")
	press(t, g, "VIEW WAVE") // bits always show the selected track
	press(t, g, "ZOOM -")
	press(t, g, "ZOOM +")
	press(t, g, "RESET")
	press(t, g, "PLAY") // no player in tests: no-op
	if !g.bitsView || g.scopeMix || g.zoom != 2048 {
		t.Fatal("view toggles")
	}
}

func TestPaint(t *testing.T) {
	g := newTestGame(t)
	g.lastI = -1
	g.paint(waveR.x, waveR.y)                   // top-left: index 0, value 255
	g.paint(waveR.x+waveR.w-1, waveR.y+waveR.h) // bottom-right: index 255, value 0
	w := g.eng.Song().Tracks[0].Wave
	if w[0] != 255 || w[255] != 0 || w[128] < 120 || w[128] > 135 {
		t.Fatal(w[0], w[128], w[255])
	}
}

func inTempDir(t *testing.T) {
	t.Helper()
	wd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })
}

func TestSaveLoad(t *testing.T) {
	inTempDir(t)
	g := newTestGame(t)
	press(t, g, "LOAD") // nothing saved yet
	if !strings.HasPrefix(g.status, "load failed") {
		t.Fatal(g.status)
	}
	g.eng.SetFormula(1, "t&t>>8")
	g.eng.EditSong(func(s *synth.Song) { s.BPM = 140; s.Order = []int{1, 0} })
	press(t, g, "SAVE")
	if g.status != "saved "+SongFile {
		t.Fatal(g.status)
	}

	g2 := newTestGame(t)
	g2.selectTrack(3)
	press(t, g2, "LOAD")
	s := g2.eng.Song()
	if s.Tracks[1].Formula != "t&t>>8" || s.BPM != 140 || len(s.Order) != 2 || g2.track != 0 {
		t.Fatalf("loaded %+v", s.Order)
	}

	os.WriteFile(SongFile, []byte(`{"tracks":[{"formula":"t>>"}]}`), 0o644)
	g2.load()
	if !strings.HasPrefix(g2.status, "load failed") {
		t.Fatal(g2.status)
	}
}

func TestLoadFallsBackToLegacyPatch(t *testing.T) {
	inTempDir(t)
	os.WriteFile(legacyFile, []byte(`{"formula":"t*(42&t>>10)","bpm":100}`), 0o644)
	g := newTestGame(t)
	g.load()
	s := g.eng.Song()
	if g.status != "loaded "+legacyFile || len(s.Tracks) != 1 || s.Tracks[0].Formula != "t*(42&t>>10)" || s.BPM != 100 {
		t.Fatalf("%s %+v", g.status, s.Tracks)
	}
}

func TestExportWholeSong(t *testing.T) {
	inTempDir(t)
	g := newTestGame(t)
	g.eng.EditSong(func(s *synth.Song) { s.Order = []int{0}; s.BPM = 300 }) // short song
	for _, format := range []string{"wav", "flac"} {
		g.export(format)
		g.export(format) // ignored while the first export runs
		select {
		case s := <-g.statusCh:
			g.exporting = false
			if !strings.HasPrefix(s, "saved 0.8s song") {
				t.Fatal(s)
			}
		case <-time.After(20 * time.Second):
			t.Fatal("export timed out")
		}
		files, _ := filepath.Glob("bitwave-*." + format)
		if len(files) != 1 {
			t.Fatalf("%s files: %v", format, files)
		}
	}
	if s := exportSong(g.eng.Song(), "mp3", time.Now()); !strings.HasPrefix(s, "export failed") {
		t.Fatal(s)
	}
	bad := g.eng.Song()
	bad.Tracks[0].Formula = "t>>"
	if s := exportSong(bad, "wav", time.Now()); !strings.HasPrefix(s, "export failed") {
		t.Fatal(s)
	}
}

func TestSoundControls(t *testing.T) {
	sliderNamed := func(g *Game, prefix string) int {
		g.layout()
		for i, sl := range g.sliders {
			if strings.HasPrefix(sl.label(), prefix) {
				return i
			}
		}
		t.Fatalf("no slider %q", prefix)
		return -1
	}
	cases := []struct {
		name  string
		check func(t *testing.T, g *Game)
	}{
		{"instrument next replaces sound and formula", func(t *testing.T, g *Game) {
			g.selectTrack(0) // kick
			press(t, g, ">") // first ">" on the track row is the formula preset
			g.loadInstrument(synth.InstrumentIndex("kick") + 1)
			tr := g.eng.Song().Tracks[0]
			if tr.Instrument != "snare" || tr.Name != "snare" || g.edit.String() != tr.Formula || tr.KnobNames[1] != "noise" {
				t.Errorf("%+v", tr)
			}
		}},
		{"instrument prev wraps around", func(t *testing.T, g *Game) {
			g.selectTrack(0)
			g.loadInstrument(-1)
			if got := g.eng.Song().Tracks[0].Instrument; got != synth.Instruments[len(synth.Instruments)-1].Name {
				t.Errorf("got %q", got)
			}
		}},
		{"knob slider label uses instrument names", func(t *testing.T, g *Game) {
			g.selectTrack(0) // kick: pitch, punch, length, drive
			sliderNamed(g, "pitch")
			sliderNamed(g, "drive")
		}},
		{"click and drag sets a knob", func(t *testing.T, g *Game) {
			g.selectTrack(0)
			i := sliderNamed(g, "pitch")
			r := g.sliders[i].r
			g.click(r.x, r.y+5, false) // left edge: 0
			if g.dragging != i || g.eng.Song().Tracks[0].Knobs[0] != 0 {
				t.Fatalf("dragging %d knob %v", g.dragging, g.eng.Song().Tracks[0].Knobs[0])
			}
			g.dragSlider(i, r.x+r.w-1) // right edge: 1
			if v := g.eng.Song().Tracks[0].Knobs[0]; v != 1 {
				t.Errorf("after drag %v", v)
			}
			g.dragSlider(i, r.x+r.w+500) // clamped
			g.dragSlider(99, 0)          // out of range: ignored
			if v := g.eng.Song().Tracks[0].Knobs[0]; v != 1 {
				t.Errorf("clamp %v", v)
			}
		}},
		{"ADSR toggle and dimmed sliders", func(t *testing.T, g *Game) {
			g.selectTrack(2) // bass: ADSR on
			press(t, g, "ADSR ON")
			if g.eng.Song().Tracks[2].Env.On {
				t.Fatal("still on")
			}
			g.layout()
			if i := sliderNamed(g, "A "); !g.sliders[i].dimmed() {
				t.Error("A slider should be dimmed with ADSR off")
			}
			press(t, g, "ADSR OFF")
			if !g.eng.Song().Tracks[2].Env.On {
				t.Error("not back on")
			}
		}},
		{"envelope sliders round-trip", func(t *testing.T, g *Game) {
			for _, p := range envParams {
				for _, v := range []float64{0, 0.25, 0.5, 1} {
					var e synth.ADSR
					p.set(&e, v)
					if got := p.get(e); math.Abs(got-v) > 1e-9 {
						t.Errorf("%s: get(set(%v)) = %v", p.label(e), v, got)
					}
				}
			}
		}},
		{"gate slider spans 0.25 to 8 steps", func(t *testing.T, g *Game) {
			var e synth.ADSR
			gate := envParams[len(envParams)-1]
			gate.set(&e, 0)
			lo := e.Gate
			gate.set(&e, 1)
			if lo != 0.25 || e.Gate != 8 {
				t.Errorf("gate %v..%v", lo, e.Gate)
			}
		}},
		{"no slider under empty space", func(t *testing.T, g *Game) {
			g.layout()
			if g.sliderAt(Width-1, Height-1) != -1 {
				t.Error("found a slider in the corner")
			}
		}},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) { tt.check(t, newTestGame(t)) })
	}
}
