package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pristupaanastasia/bitwave/internal/dialog"
	"github.com/pristupaanastasia/bitwave/internal/expr"
	"github.com/pristupaanastasia/bitwave/internal/synth"
)

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

func hasButton(g *Game, label string) bool {
	g.layout()
	for _, b := range g.buttons {
		if b.label() == label {
			return true
		}
	}
	return false
}

// gameCase is a named check run against a fresh game.
type gameCase struct {
	name  string
	check func(t *testing.T, g *Game)
}

func runGame(t *testing.T, tests []gameCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { tt.check(t, newTestGame(t)) })
	}
}

func TestLineEdit(t *testing.T) {
	tests := []struct {
		name    string
		start   string
		ops     func(l *lineEdit) bool // returns the result of the last edit, if any
		want    string
		wantOK  bool
		wantCur int // -1 = do not check
	}{
		{"wrap in parens", "t>>8", func(l *lineEdit) bool {
			l.Home()
			l.Insert([]rune("("))
			l.End()
			l.Insert([]rune(")&255"))
			return true
		}, "(t>>8)&255", true, -1},
		{"backspace in the middle", "(t>>8)&255", func(l *lineEdit) bool {
			l.Left()
			l.Left()
			l.Left()
			return l.Backspace()
		}, "(t>>8)255", true, 6},
		{"delete under the cursor", "(t>>8)255", func(l *lineEdit) bool {
			l.Home()
			for range 6 {
				l.Right()
			}
			return l.Delete()
		}, "(t>>8)55", true, 6},
		{"delete at end does nothing", "abc", func(l *lineEdit) bool { l.End(); l.Right(); return l.Delete() }, "abc", false, 3},
		{"backspace at start does nothing", "abc", func(l *lineEdit) bool { l.Home(); l.Left(); return l.Backspace() }, "abc", false, 0},
		{"insert nothing", "abc", func(l *lineEdit) bool { l.Home(); l.Insert(nil); return true }, "abc", true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var l lineEdit
			l.Set(tt.start)
			ok := tt.ops(&l)
			if l.String() != tt.want || ok != tt.wantOK || tt.wantCur >= 0 && l.cur != tt.wantCur {
				t.Errorf("got %q ok %v cur %d, want %q ok %v cur %d", l.String(), ok, l.cur, tt.want, tt.wantOK, tt.wantCur)
			}
		})
	}
}

func TestScopeHelpers(t *testing.T) {
	buf := []float32{0.5, -0.5, -0.2, 0.1, 0.4}
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"trigger on rising zero crossing", func(t *testing.T) {
			if i := findTrigger(buf, 0, len(buf)); i != 3 {
				t.Error(i)
			}
		}},
		{"trigger without crossing returns lo", func(t *testing.T) {
			if i := findTrigger([]float32{1, 1, 1}, 0, 3); i != 0 {
				t.Error(i)
			}
		}},
		{"trigger with hi beyond the buffer", func(t *testing.T) {
			if i := findTrigger(buf, 0, 100); i != 3 {
				t.Error(i)
			}
		}},
		{"columns keep min and max", func(t *testing.T) {
			lo, hi := columns([]float32{0, 1, -1, 0.5}, 2)
			if lo[0] != 0 || hi[0] != 1 || lo[1] != -1 || hi[1] != 0.5 {
				t.Error(lo, hi)
			}
		}},
		{"columns with fewer samples than pixels", func(t *testing.T) {
			lo, hi := columns([]float32{0.25}, 3)
			for i := range lo {
				if lo[i] != 0.25 || hi[i] != 0.25 {
					t.Error(lo, hi)
				}
			}
		}},
		{"toByte inverts byte-mode output", func(t *testing.T) {
			for b := range 256 {
				if s := (float32(b) - 128) / 128; toByte(s) != uint8(b) {
					t.Fatalf("toByte(%v) = %d, want %d", s, toByte(s), b)
				}
			}
		}},
		{"toByte clamps", func(t *testing.T) {
			if toByte(5) != 255 || toByte(-5) != 0 {
				t.Error(toByte(5), toByte(-5))
			}
		}},
		{"rect contains its edges only", func(t *testing.T) {
			r := rect{10, 10, 5, 5}
			if !r.contains(10, 10) || !r.contains(14, 14) || r.contains(15, 10) || r.contains(9, 12) {
				t.Error("contains")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

func TestWaveDrawing(t *testing.T) {
	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{"stroke interpolates", func(t *testing.T) {
			var w [expr.WaveSize]float64
			paintWave(&w, 10, 0, 14, 255)
			for i, v := range []float64{0, 64, 128, 191, 255} {
				if w[10+i] != v {
					t.Errorf("w[%d] = %v, want %v", 10+i, w[10+i], v)
				}
			}
		}},
		{"reversed stroke", func(t *testing.T) {
			var w [expr.WaveSize]float64
			paintWave(&w, 20, 0, 18, 100)
			if w[18] != 100 || w[20] != 0 {
				t.Error(w[18:21])
			}
		}},
		{"index and value are clamped", func(t *testing.T) {
			var w [expr.WaveSize]float64
			paintWave(&w, -5, 0, -5, 999)
			if w[0] != 255 {
				t.Error(w[0])
			}
		}},
		{"mouse stroke across the panel", func(t *testing.T) {
			g := newTestGame(t)
			g.lastI = -1
			g.paint(waveR.x, waveR.y)                   // top-left: index 0, value 255
			g.paint(waveR.x+waveR.w-1, waveR.y+waveR.h) // bottom-right: index 255, value 0
			w := g.eng.Song().Tracks[0].Wave
			if w[0] != 255 || w[255] != 0 || w[128] < 120 || w[128] > 135 {
				t.Error(w[0], w[128], w[255])
			}
		}},
		{"shape buttons", func(t *testing.T) {
			g := newTestGame(t)
			g.selectTrack(2)
			press(t, g, "SINE")
			if g.eng.Song().Tracks[2].Wave != synth.Shape("sine") {
				t.Error("shape not applied")
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

func TestFormulaVisibility(t *testing.T) {
	runGame(t, []gameCase{
		{"hidden by default with no formula controls", func(t *testing.T, g *Game) {
			if g.showFormula || hasButton(g, "MODE "+g.cur().Mode.String()) || hasButton(g, "FORMULA PRESET") {
				t.Error("formula controls visible by default")
			}
		}},
		{"clicking the formula row does not focus it while hidden", func(t *testing.T, g *Game) {
			g.click(formulaR.x+5, formulaR.y+5, false)
			if g.focus {
				t.Error("focused a hidden editor")
			}
		}},
		{"SHOW FORMULA reveals the editor", func(t *testing.T, g *Game) {
			press(t, g, "SHOW FORMULA")
			if !g.showFormula || !hasButton(g, "FORMULA PRESET") {
				t.Fatal("not shown")
			}
			g.click(formulaR.x+5, formulaR.y+5, false)
			if !g.focus {
				t.Error("editor did not take focus")
			}
		}},
		{"HIDE FORMULA drops focus", func(t *testing.T, g *Game) {
			press(t, g, "SHOW FORMULA")
			g.click(formulaR.x+5, formulaR.y+5, false)
			press(t, g, "HIDE FORMULA")
			if g.showFormula || g.focus {
				t.Error("still visible or focused")
			}
		}},
		{"formula goes to the selected track", func(t *testing.T, g *Game) {
			press(t, g, "3 bass")
			if g.track != 2 || g.edit.String() != synth.DefaultSong().Tracks[2].Formula {
				t.Fatalf("track %d formula %q", g.track, g.edit.String())
			}
			g.edit.Set("t&t>>8")
			g.applyFormula()
			if s := g.eng.Song(); s.Tracks[2].Formula != "t&t>>8" || s.Tracks[0].Formula == "t&t>>8" {
				t.Error("formula went to the wrong track")
			}
		}},
		{"a broken formula keeps the old one", func(t *testing.T, g *Game) {
			g.edit.Set("t>>")
			g.applyFormula()
			if g.formulaErr == "" || g.eng.Song().Tracks[0].Formula != synth.DefaultSong().Tracks[0].Formula {
				t.Error("bad formula replaced the old one")
			}
		}},
		{"mode, rate and presets once shown", func(t *testing.T, g *Game) {
			g.selectTrack(2) // bass: byte, 8000
			press(t, g, "SHOW FORMULA")
			press(t, g, "MODE BYTE")
			if g.eng.Song().Tracks[2].Mode != synth.ModeFloat {
				t.Error("mode")
			}
			g.editTrack(func(tr *synth.Track) { tr.Rate = stepRate(tr.Rate, 1) })
			if g.eng.Song().Tracks[2].Rate != 11025 {
				t.Error("rate")
			}
			g.loadPreset(0)
			if g.preset != 0 || g.eng.Song().Tracks[2].Formula != synth.Presets[0].Formula {
				t.Error("preset")
			}
			g.loadPreset(-1)
			if g.preset != len(synth.Presets)-1 {
				t.Error("preset wrap")
			}
		}},
	})
}

func TestStepRate(t *testing.T) {
	tests := []struct{ rate, dir, want int }{
		{8000, 1, 11025},
		{8000, -1, 4000},
		{1000, -1, 1000},
		{48000, 1, 48000},
		{9000, 1, 11025},
		{9000, -1, 8000},
		{60000, -1, 48000},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d/%+d", tt.rate, tt.dir), func(t *testing.T) {
			if got := stepRate(tt.rate, tt.dir); got != tt.want {
				t.Errorf("stepRate(%d,%d) = %d, want %d", tt.rate, tt.dir, got, tt.want)
			}
		})
	}
}

func TestTracks(t *testing.T) {
	runGame(t, []gameCase{
		{"mute and solo buttons", func(t *testing.T, g *Game) {
			press(t, g, "M")
			press(t, g, "S")
			if tr := g.eng.Song().Tracks[0]; !tr.Mute || !tr.Solo {
				t.Errorf("mute %v solo %v", tr.Mute, tr.Solo)
			}
		}},
		{"add a track selects it", func(t *testing.T, g *Game) {
			press(t, g, "+ TRACK")
			if s := g.eng.Song(); len(s.Tracks) != 5 || g.track != 4 || len(s.Patterns[0].Notes) != 5 {
				t.Errorf("tracks %d selected %d", len(s.Tracks), g.track)
			}
		}},
		{"track limit", func(t *testing.T, g *Game) {
			for range synth.MaxTracks {
				g.addTrack()
			}
			if len(g.eng.Song().Tracks) != synth.MaxTracks || !strings.Contains(g.status, "at most") {
				t.Error(len(g.eng.Song().Tracks), g.status)
			}
		}},
		{"remove a track, its neighbours' notes move with them", func(t *testing.T, g *Game) {
			g.selectTrack(1) // hats
			press(t, g, "- TRACK")
			s := g.eng.Song()
			if len(s.Tracks) != 3 || s.Tracks[1].Name != "bass" || len(s.Patterns[1].Notes) != 3 {
				t.Fatalf("%d tracks, [1]=%s", len(s.Tracks), s.Tracks[1].Name)
			}
			if s.Patterns[0].Notes[1] != synth.DefaultSong().Patterns[0].Notes[2] {
				t.Error("notes did not follow the track")
			}
		}},
		{"the last track cannot be removed", func(t *testing.T, g *Game) {
			for range synth.MaxTracks {
				g.removeTrack()
			}
			if len(g.eng.Song().Tracks) != 1 || !strings.Contains(g.status, "at least one") {
				t.Error(len(g.eng.Song().Tracks), g.status)
			}
		}},
		{"BPM buttons", func(t *testing.T, g *Game) {
			press(t, g, "+") // first "+" on screen is BPM
			if g.eng.Song().BPM != synth.DefaultSong().BPM+2 {
				t.Error(g.eng.Song().BPM)
			}
		}},
		{"scope view toggles", func(t *testing.T, g *Game) {
			press(t, g, "SHOW MIX")
			press(t, g, "VIEW WAVE") // bits always show the selected track
			press(t, g, "ZOOM -")
			press(t, g, "ZOOM +")
			press(t, g, "RESET")
			press(t, g, "PLAY") // no player in tests: no-op
			if !g.bitsView || g.scopeMix || g.zoom != 2048 {
				t.Errorf("bits %v mix %v zoom %d", g.bitsView, g.scopeMix, g.zoom)
			}
		}},
	})
}

func TestPatternsAndSong(t *testing.T) {
	runGame(t, []gameCase{
		{"select, copy and create patterns", func(t *testing.T, g *Game) {
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
			if s := g.eng.Song(); len(s.Patterns) != 4 || s.Patterns[3].Notes[0][0] != synth.Rest {
				t.Error("new pattern should be empty")
			}
		}},
		{"append to and remove from the song", func(t *testing.T, g *Game) {
			g.selectPattern(1)
			before := len(g.eng.Song().Order)
			press(t, g, "+ TO SONG")
			if s := g.eng.Song(); len(s.Order) != before+1 || s.Order[before] != 1 {
				t.Fatal("append", s.Order)
			}
			press(t, g, "- FROM SONG") // nothing selected: removes the last slot
			if len(g.eng.Song().Order) != before {
				t.Error("remove last")
			}
		}},
		// Regression: the first A slot could not be removed from the song.
		{"remove the selected slot, even the first A", func(t *testing.T, g *Game) {
			g.click(orderR.x+1, orderR.y+1, false) // select slot 0 (A)
			press(t, g, "- FROM SONG")
			want := synth.DefaultSong().Order[1:]
			if got := g.eng.Song().Order; len(got) != len(want) || got[0] != want[0] || g.slot != -1 {
				t.Errorf("order %v, want %v", got, want)
			}
		}},
		{"the last slot stays", func(t *testing.T, g *Game) {
			g.eng.EditSong(func(s *synth.Song) { s.Order = []int{1} })
			press(t, g, "- FROM SONG")
			if got := g.eng.Song().Order; len(got) != 1 || !strings.Contains(g.status, "needs one slot") {
				t.Error(got, g.status)
			}
		}},
		// Regression: patterns could not be deleted at all.
		{"delete pattern A removes it from the song and renames the rest", func(t *testing.T, g *Game) {
			bNotes := g.eng.Song().Patterns[1].Notes
			g.selectPattern(0)
			press(t, g, "DELETE")
			s := g.eng.Song()
			if len(s.Patterns) != 1 || s.Patterns[0].Notes[3] != bNotes[3] {
				t.Fatal("B did not become A")
			}
			for _, o := range s.Order {
				if o != 0 {
					t.Fatalf("order %v should only hold the former B", s.Order)
				}
			}
			if want := 4; len(s.Order) != want { // the demo has four B slots
				t.Errorf("order %v, want %d slots", s.Order, want)
			}
		}},
		{"delete a middle pattern shifts later slots", func(t *testing.T, g *Game) {
			g.newPattern(false) // C
			g.eng.EditSong(func(s *synth.Song) { s.Order = []int{0, 2, 1, 2} })
			g.selectPattern(1)
			g.deletePattern()
			if got := g.eng.Song().Order; len(got) != 3 || got[0] != 0 || got[1] != 1 || got[2] != 1 {
				t.Errorf("order %v, want [0 1 1]", got)
			}
		}},
		{"deleting the only pattern of the song leaves one slot", func(t *testing.T, g *Game) {
			g.eng.EditSong(func(s *synth.Song) { s.Order = []int{1, 1} })
			g.selectPattern(1)
			g.deletePattern()
			if got := g.eng.Song().Order; len(got) != 1 || got[0] != 0 {
				t.Errorf("order %v", got)
			}
		}},
		{"the last pattern cannot be deleted", func(t *testing.T, g *Game) {
			g.deletePattern()
			g.deletePattern()
			if len(g.eng.Song().Patterns) != 1 || !strings.Contains(g.status, "at least one pattern") {
				t.Error(len(g.eng.Song().Patterns), g.status)
			}
		}},
		{"right click puts the current pattern into a slot", func(t *testing.T, g *Game) {
			g.selectPattern(1)
			g.click(orderR.x+1, orderR.y+1, true)
			if g.eng.Song().Order[0] != 1 {
				t.Error(g.eng.Song().Order)
			}
		}},
		{"left click selects the slot's pattern and seeks", func(t *testing.T, g *Game) {
			g.click(orderR.x+2*cellW+1, orderR.y+1, false)
			if g.pattern != g.eng.Song().Order[2] {
				t.Fatal("did not select the slot's pattern")
			}
			g.eng.Render(10)
			if st := g.eng.Status(); st.Slot != 2 {
				t.Errorf("slot %d", st.Slot)
			}
		}},
		{"click past the song end does nothing", func(t *testing.T, g *Game) {
			before := g.pattern
			g.click(orderR.x+orderR.w-1, orderR.y+1, false)
			if g.pattern != before {
				t.Error("changed pattern")
			}
		}},
		{"pattern limit", func(t *testing.T, g *Game) {
			for range synth.MaxPatterns {
				g.newPattern(false)
			}
			if len(g.eng.Song().Patterns) != synth.MaxPatterns || !strings.Contains(g.status, "at most") {
				t.Error(len(g.eng.Song().Patterns), g.status)
			}
		}},
		{"song length limit", func(t *testing.T, g *Game) {
			for range synth.MaxOrder {
				g.appendToSong()
			}
			if len(g.eng.Song().Order) != synth.MaxOrder || !strings.Contains(g.status, "limited") {
				t.Error(len(g.eng.Song().Order), g.status)
			}
		}},
		{"loop pattern follows the selected pattern", func(t *testing.T, g *Game) {
			press(t, g, "PLAY SONG") // switches to pattern loop
			press(t, g, "B")
			g.eng.Render(10)
			if st := g.eng.Status(); st.Mode != synth.PlayPattern || st.Pattern != 1 {
				t.Fatalf("%+v", st)
			}
			press(t, g, "LOOP PATTERN")
			if g.playMode != synth.PlaySong {
				t.Error("back to song")
			}
		}},
		{"grid click toggles a note of the selected track and pattern", func(t *testing.T, g *Game) {
			g.selectTrack(3)
			g.selectPattern(0)
			colW, rowH := seqR.w/synth.Steps, seqR.h/seqRows
			x, y := seqR.x+colW/2, seqR.y+rowH/2 // top row, first column
			g.click(x, y, false)
			top := g.lowNote + seqRows - 1
			if s := g.eng.Song(); s.Patterns[0].Notes[3][0] != top || s.Patterns[1].Notes[3][0] == top {
				t.Fatal("note landed in the wrong place")
			}
			g.click(x, y, false)
			if g.eng.Song().Patterns[0].Notes[3][0] != synth.Rest {
				t.Error("toggle off")
			}
		}},
		{"clear empties the selected track in the pattern", func(t *testing.T, g *Game) {
			press(t, g, "CLEAR")
			for _, n := range g.eng.Song().Patterns[0].Notes[0] {
				if n != synth.Rest {
					t.Fatal("not cleared")
				}
			}
		}},
		{"octave buttons", func(t *testing.T, g *Game) {
			press(t, g, "OCT +")
			press(t, g, "OCT -")
			press(t, g, "OCT -")
			if g.lowNote != 24 {
				t.Error(g.lowNote)
			}
		}},
	})
}

// answer makes the next file dialogs return path (or err) without showing
// anything, and returns a func that runs the dialog result like Update does.
func answer(t *testing.T, g *Game, path string, err error) func() {
	t.Helper()
	g.askSave = func(string, string) (string, error) { return path, err }
	g.askOpen = func(string) (string, error) { return path, err }
	return func() {
		t.Helper()
		select {
		case fn := <-g.dialogCh:
			fn()
		case <-time.After(5 * time.Second):
			t.Fatal("dialog result never arrived")
		}
	}
}

// waitExport waits for a background export and returns its status line.
func waitExport(t *testing.T, g *Game) string {
	t.Helper()
	select {
	case s := <-g.statusCh:
		g.exporting = false
		return s
	case <-time.After(20 * time.Second):
		t.Fatal("export timed out")
		return ""
	}
}

func TestFiles(t *testing.T) {
	runGame(t, []gameCase{
		{"save as asks for a name and adds .json", func(t *testing.T, g *Game) {
			dir := t.TempDir()
			done := answer(t, g, filepath.Join(dir, "my beat"), nil)
			g.eng.EditSong(func(s *synth.Song) { s.BPM = 140 })
			press(t, g, "SAVE")
			done()
			want := filepath.Join(dir, "my beat.json")
			if g.status != "saved "+want {
				t.Fatal(g.status)
			}
			if s, err := LoadSong(want); err != nil || s.BPM != 140 {
				t.Errorf("saved file: %v bpm %v", err, s.BPM)
			}
		}},
		{"load opens the chosen file", func(t *testing.T, g *Game) {
			path := filepath.Join(t.TempDir(), "song.json")
			src := synth.DefaultSong()
			src.BPM, src.Order = 150, []int{1, 0}
			b, _ := json.Marshal(src)
			os.WriteFile(path, b, 0o644)
			g.selectTrack(3)
			done := answer(t, g, path, nil)
			press(t, g, "LOAD")
			done()
			if s := g.eng.Song(); s.BPM != 150 || len(s.Order) != 2 || g.track != 0 || g.status != "loaded "+path {
				t.Errorf("bpm %v order %v track %d status %q", s.BPM, s.Order, g.track, g.status)
			}
		}},
		{"load opens an old single-track patch", func(t *testing.T, g *Game) {
			path := filepath.Join(t.TempDir(), "old.json")
			os.WriteFile(path, []byte(`{"formula":"t*(42&t>>10)","bpm":100}`), 0o644)
			done := answer(t, g, path, nil)
			g.load()
			done()
			if s := g.eng.Song(); len(s.Tracks) != 1 || s.Tracks[0].Formula != "t*(42&t>>10)" || s.BPM != 100 {
				t.Errorf("%s %+v", g.status, s.Tracks)
			}
		}},
		{"a broken song file is reported", func(t *testing.T, g *Game) {
			path := filepath.Join(t.TempDir(), "bad.json")
			os.WriteFile(path, []byte(`{"tracks":[{"formula":"t>>"}]}`), 0o644)
			done := answer(t, g, path, nil)
			g.load()
			done()
			if !strings.HasPrefix(g.status, "load failed") {
				t.Error(g.status)
			}
		}},
		{"cancel does nothing", func(t *testing.T, g *Game) {
			done := answer(t, g, "", dialog.ErrCanceled)
			before := g.status
			g.save()
			done()
			if g.status != before || g.dialogOpen {
				t.Errorf("status %q open %v", g.status, g.dialogOpen)
			}
		}},
		{"a failing dialog is reported", func(t *testing.T, g *Game) {
			done := answer(t, g, "", errors.New("no osascript"))
			g.load()
			done()
			if !strings.Contains(g.status, "file dialog failed") {
				t.Error(g.status)
			}
		}},
		{"only one dialog at a time", func(t *testing.T, g *Game) {
			release := make(chan struct{})
			g.askOpen = func(string) (string, error) { <-release; return "", dialog.ErrCanceled }
			g.load()
			g.load() // ignored while the first is open
			close(release)
			(<-g.dialogCh)()
			select {
			case <-g.dialogCh:
				t.Error("a second dialog was opened")
			case <-time.After(50 * time.Millisecond):
			}
		}},
		{"export wav and flac to chosen names", func(t *testing.T, g *Game) {
			dir := t.TempDir()
			g.eng.EditSong(func(s *synth.Song) { s.Order = []int{0}; s.BPM = 300 }) // short song
			tests := []struct{ format, answer, want string }{
				{"wav", "track one.wav", "track one.wav"},
				{"flac", "track two", "track two.flac"}, // extension added
			}
			for _, tt := range tests {
				done := answer(t, g, filepath.Join(dir, tt.answer), nil)
				g.export(tt.format)
				done()
				g.export(tt.format) // ignored while the first export runs
				if s := waitExport(t, g); s != "saved 0.8s song to "+filepath.Join(dir, tt.want) {
					t.Fatal(s)
				}
				if _, err := os.Stat(filepath.Join(dir, tt.want)); err != nil {
					t.Error(err)
				}
			}
		}},
		{"export errors", func(t *testing.T, g *Game) {
			dir := t.TempDir()
			if s := exportSong(g.eng.Song(), filepath.Join(dir, "x.mp3")); !strings.HasPrefix(s, "export failed") {
				t.Error(s)
			}
			bad := g.eng.Song()
			bad.Tracks[0].Formula = "t>>"
			if s := exportSong(bad, filepath.Join(dir, "x.wav")); !strings.HasPrefix(s, "export failed") {
				t.Error(s)
			}
		}},
		{"autosave writes only changes, at most every 2 s", func(t *testing.T, g *Game) {
			g.songPath = filepath.Join(t.TempDir(), "auto", "song.json")
			now := time.Now()
			g.autosave(now, false)
			if _, err := os.Stat(g.songPath); err != nil {
				t.Fatal("first autosave should write the song:", err)
			}
			stat := func() time.Time { st, _ := os.Stat(g.songPath); return st.ModTime() }
			first := stat()
			g.eng.EditSong(func(s *synth.Song) { s.BPM = 99 })
			g.autosave(now.Add(time.Second), false) // too soon
			if s, _ := LoadSong(g.songPath); s.BPM == 99 {
				t.Fatal("wrote before the interval")
			}
			g.autosave(now.Add(3*time.Second), false)
			if s, _ := LoadSong(g.songPath); s.BPM != 99 {
				t.Fatal("change was not saved")
			}
			time.Sleep(10 * time.Millisecond)
			mod := stat()
			g.autosave(now.Add(6*time.Second), false) // nothing changed
			if stat() != mod || first.IsZero() {
				t.Error("rewrote an unchanged song")
			}
		}},
		{"autosave on close ignores the interval", func(t *testing.T, g *Game) {
			g.songPath = filepath.Join(t.TempDir(), "song.json")
			now := time.Now()
			g.autosave(now, false)
			g.eng.EditSong(func(s *synth.Song) { s.BPM = 77 })
			g.autosave(now, true)
			if s, _ := LoadSong(g.songPath); s.BPM != 77 {
				t.Error("forced autosave did not write")
			}
		}},
		{"autosave failure is reported", func(t *testing.T, g *Game) {
			file := filepath.Join(t.TempDir(), "file")
			os.WriteFile(file, nil, 0o644)
			g.songPath = filepath.Join(file, "song.json") // parent is a file
			g.autosave(time.Now(), true)
			if !strings.HasPrefix(g.status, "autosave failed") {
				t.Error(g.status)
			}
		}},
		{"default song path is in the config directory", func(t *testing.T, g *Game) {
			if p := DefaultSongPath(); !strings.HasSuffix(p, filepath.Join("bitwave", "song.json")) {
				t.Error(p)
			}
		}},
	})
}

func TestSoundControls(t *testing.T) {
	sliderNamed := func(t *testing.T, g *Game, prefix string) int {
		t.Helper()
		g.layout()
		for i, sl := range g.sliders {
			if strings.HasPrefix(sl.label(), prefix) {
				return i
			}
		}
		t.Fatalf("no slider %q", prefix)
		return -1
	}
	runGame(t, []gameCase{
		{"instrument next replaces sound and formula", func(t *testing.T, g *Game) {
			press(t, g, ">") // first ">" is the instrument selector
			tr := g.eng.Song().Tracks[0]
			if tr.Instrument != "snare" || tr.Name != "snare" || g.edit.String() != tr.Formula || tr.KnobNames[1] != "noise" {
				t.Errorf("%+v", tr)
			}
		}},
		{"instrument prev wraps around", func(t *testing.T, g *Game) {
			press(t, g, "<")
			if got := g.eng.Song().Tracks[0].Instrument; got != synth.Instruments[len(synth.Instruments)-1].Name {
				t.Errorf("got %q", got)
			}
		}},
		{"knob sliders use the instrument's names", func(t *testing.T, g *Game) {
			sliderNamed(t, g, "pitch") // kick: pitch, punch, length, drive
			sliderNamed(t, g, "drive")
		}},
		{"click and drag sets a knob", func(t *testing.T, g *Game) {
			i := sliderNamed(t, g, "pitch")
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
		{"volume buttons", func(t *testing.T, g *Game) {
			before := g.cur().Volume
			g.editTrack(func(tr *synth.Track) { tr.Volume -= 0.05 })
			if math.Abs(g.cur().Volume-(before-0.05)) > 1e-9 {
				t.Error(g.cur().Volume)
			}
		}},
		{"ADSR toggle and dimmed sliders", func(t *testing.T, g *Game) {
			g.selectTrack(2) // bass: ADSR on
			press(t, g, "ADSR ON")
			if g.eng.Song().Tracks[2].Env.On {
				t.Fatal("still on")
			}
			if i := sliderNamed(t, g, "A "); !g.sliders[i].dimmed() {
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
	})
}

func TestWhatIsPlaying(t *testing.T) {
	runGame(t, []gameCase{
		{"tab meter shows a sounding track", func(t *testing.T, g *Game) {
			// The kick hits on steps 1, 5, 9, 13; step 5 starts at 0.484 s at
			// 124 BPM, so 0.5 s is just after a hit.
			g.eng.Render(SampleRate / 2)
			g.updateLevels(len(g.eng.Song().Tracks))
			if g.level(0) < 0.1 {
				t.Errorf("kick level %v while it plays", g.level(0))
			}
			if g.level(99) != 0 {
				t.Error("missing track should read 0")
			}
		}},
		{"tab meter is quiet for a muted-out silent track", func(t *testing.T, g *Game) {
			g.eng.EditSong(func(s *synth.Song) {
				for i := range s.Patterns {
					s.Patterns[i].Notes[3] = [synth.Steps]int{-1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1, -1}
				}
			})
			g.eng.Render(SampleRate / 4)
			g.updateLevels(len(g.eng.Song().Tracks))
			if g.level(3) > 0.01 {
				t.Errorf("lead without notes has level %v", g.level(3))
			}
		}},
		{"new song needs a second click", func(t *testing.T, g *Game) {
			press(t, g, "NEW SONG")
			if len(g.eng.Song().Tracks) != 4 || !g.confirmNew() || !strings.Contains(g.status, "again") {
				t.Fatal("first click must only ask")
			}
			press(t, g, "CLEAR ALL?")
			s := g.eng.Song()
			if len(s.Tracks) != 1 || len(s.Patterns) != 1 || len(s.Order) != 1 || g.confirmNew() {
				t.Fatalf("%d tracks %d patterns %d slots", len(s.Tracks), len(s.Patterns), len(s.Order))
			}
			for _, n := range s.Patterns[0].Notes[0] {
				if n != synth.Rest {
					t.Fatal("new song should have no notes")
				}
			}
		}},
		{"new song is silent", func(t *testing.T, g *Game) {
			g.newSong()
			g.newSong()
			if peak, _ := maxAbs(g.eng.Render(SampleRate)); peak != 0 {
				t.Errorf("peak %v", peak)
			}
		}},
		{"the confirmation expires", func(t *testing.T, g *Game) {
			g.newSong()
			g.newAt = time.Now().Add(-5 * time.Second)
			g.newSong() // counts as a first click again
			if len(g.eng.Song().Tracks) != 4 {
				t.Error("cleared after the confirmation expired")
			}
		}},
	})
}

func maxAbs(out []float32) (float64, int) {
	peak, at := 0.0, -1
	for i, v := range out {
		if a := math.Abs(float64(v)); a > peak {
			peak, at = a, i
		}
	}
	return peak, at
}
