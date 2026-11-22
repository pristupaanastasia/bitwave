// Package ui is the Ebitengine front end: tracks, formula editor,
// oscilloscope, wave editor, patterns, arrangement and step sequencer.
package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/pristupaanastasia/bitwave/internal/audiofile"
	"github.com/pristupaanastasia/bitwave/internal/clipboard"
	"github.com/pristupaanastasia/bitwave/internal/dialog"
	"github.com/pristupaanastasia/bitwave/internal/expr"
	"github.com/pristupaanastasia/bitwave/internal/synth"
)

const (
	Width      = 1100
	Height     = 880
	SampleRate = 44100

	charW   = 6
	seqRows = 25 // two octaves plus the top note

	// SongFile is the default song file name; the app keeps it in the user
	// config directory (see DefaultSongPath).
	SongFile = "bitwave-song.json"

	autosaveEvery = 2 * time.Second
	frameInterval = time.Second / 30 // redraw rate; audio is unaffected
)

var (
	colBG      = color.RGBA{0x12, 0x14, 0x18, 0xff}
	colPanel   = color.RGBA{0x1b, 0x1e, 0x25, 0xff}
	colPanel2  = color.RGBA{0x22, 0x26, 0x2f, 0xff}
	colGrid    = color.RGBA{0x2e, 0x33, 0x3d, 0xff}
	colBtn     = color.RGBA{0x2f, 0x35, 0x42, 0xff}
	colBtnHot  = color.RGBA{0x3d, 0x45, 0x56, 0xff}
	colBtnOn   = color.RGBA{0x2e, 0x6b, 0x52, 0xff}
	colScope   = color.RGBA{0x5c, 0xff, 0x9d, 0xff}
	colWave    = color.RGBA{0xff, 0xb8, 0x4d, 0xff}
	colNote    = color.RGBA{0x6a, 0xa8, 0xff, 0xff}
	colPlay    = color.RGBA{0xff, 0x5c, 0x8a, 0xff}
	colPlayDim = color.RGBA{0xff, 0x5c, 0x8a, 0x30}
	colError   = color.RGBA{0x8a, 0x22, 0x33, 0xff}
	colMute    = color.RGBA{0x8a, 0x5a, 0x22, 0xff}
	colFocus   = color.RGBA{0x5c, 0xff, 0x9d, 0x60}
	colBlackKy = color.RGBA{0x17, 0x19, 0x1f, 0xff}
)

// Layout, top to bottom.
const (
	rowTransport = 8
	rowTracks    = 36
	rowTrackCtl  = 64
	rowSound     = 92
	rowPatterns  = 436
	rowSeqCtl    = 506
	tabW         = 110
	cellW        = 16
)

var (
	formulaR = rect{10, 124, Width - 20, 24}
	scopeR   = rect{10, 178, 640, 220}
	waveR    = rect{660, 178, Width - 670, 220}
	orderR   = rect{60, 466, synth.MaxOrder * cellW, 22}
	seqR     = rect{50, 552, Width - 60, seqRows * 10}
)

type button struct {
	r       rect
	label   func() string
	onClick func()
	on      func() bool    // highlighted state, optional
	meter   func() float64 // level bar 0..1 along the bottom, optional
	mark    func() bool    // pink underline, e.g. the pattern playing now
}

// slider is a horizontal control dragged with the mouse. Its value is
// normalised to 0..1; get/set map it to the real parameter.
type slider struct {
	r      rect
	label  func() string
	get    func() float64
	set    func(float64)
	dimmed func() bool // drawn faded, e.g. ADSR controls while ADSR is off
}

// Game implements ebiten.Game.
type Game struct {
	eng    *synth.Engine
	player *audio.Player

	track    int // track being edited
	pattern  int // pattern being edited
	playMode synth.PlayMode

	edit        lineEdit
	focus       bool
	showFormula bool // the formula editor is hidden until asked for
	formulaErr  string

	status      string
	statusUntil time.Time
	statusCh    chan string

	bitsView  bool
	scopeMix  bool
	zoom      int // samples shown across the oscilloscope
	preset    int
	lowNote   int // lowest note shown in the sequencer
	slot      int // selected slot of the song strip, -1 if none
	exporting bool

	drawing bool
	lastI   int
	lastV   float64

	buttons  []button
	sliders  []slider
	path     vector.Path // reused for the scope and wave traces
	dragging int         // index of the slider being dragged, -1 if none
	scopeBuf []float32
	blink    int

	levels   []float64 // recent peak per track, for the tab meters
	levelBuf []float32
	status0  synth.Status // playback status of the current frame
	newAt    time.Time    // first click of NEW SONG, which needs a second

	songPath     string // where autosave keeps the song
	askSave      func(prompt, defaultName string) (string, error)
	askOpen      func(prompt string) (string, error)
	dialogCh     chan func() // results of file dialogs, run in Update
	dialogOpen   bool
	savedJSON    []byte // song as last written, to skip unchanged autosaves
	lastAutosave time.Time
	lastDraw     time.Time

	// snap is one copy of the song per frame for labels and drawing, so the
	// UI does not clone the song under the audio lock dozens of times a frame.
	snap synth.Song
}

// New creates the UI around an engine and starts audio playback.
// DefaultSongPath is where the song is saved and reopened from:
// ~/Library/Application Support/bitwave/song.json on macOS.
func DefaultSongPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return SongFile
	}
	return filepath.Join(dir, "bitwave", "song.json")
}

// New creates the UI around an engine, saving the song to songPath, and
// starts audio playback.
func New(eng *synth.Engine, songPath string) (*Game, error) {
	ctx := audio.NewContext(SampleRate)
	player, err := ctx.NewPlayerF32(eng)
	if err != nil {
		return nil, err
	}
	player.SetBufferSize(60 * time.Millisecond)
	player.Play()
	g := newGame(eng)
	g.player = player
	g.songPath = songPath
	g.savedJSON, _ = json.Marshal(eng.Song()) // what is on disk, or nothing new yet
	return g, nil
}

func newGame(eng *synth.Engine) *Game {
	g := &Game{
		eng:      eng,
		zoom:     2048,
		lowNote:  36,
		statusCh: make(chan string, 4),
		preset:   -1,
		scopeMix: true,
		dragging: -1,
		slot:     -1,
		songPath: SongFile,
		askSave:  dialog.Save,
		askOpen:  dialog.Open,
		dialogCh: make(chan func(), 1),
	}
	g.selectTrack(0)
	g.layout()
	return g
}

func (g *Game) playing() bool { return g.player != nil && g.player.IsPlaying() }

func (g *Game) togglePlay() {
	if g.player == nil {
		return
	}
	if g.player.IsPlaying() {
		g.player.Pause()
	} else {
		g.player.Play()
	}
}

func (g *Game) cur() synth.Track { return g.eng.Song().Tracks[g.track] }

// snapTrack is the selected track as of the current frame's snapshot.
func (g *Game) snapTrack() synth.Track {
	if g.track < len(g.snap.Tracks) {
		return g.snap.Tracks[g.track]
	}
	return g.cur()
}

func (g *Game) snapFlag(i int, f func(synth.Track) bool) bool {
	return i < len(g.snap.Tracks) && f(g.snap.Tracks[i])
}

func (g *Game) editTrack(fn func(t *synth.Track)) {
	g.eng.EditSong(func(s *synth.Song) { fn(&s.Tracks[g.track]) })
}

func (g *Game) selectTrack(i int) {
	n := len(g.eng.Song().Tracks)
	g.track = min(max(i, 0), n-1)
	g.edit.Set(g.cur().Formula)
	g.formulaErr = ""
	g.preset = -1
}

func (g *Game) selectPattern(i int) {
	g.pattern = min(max(i, 0), len(g.eng.Song().Patterns)-1)
	if g.playMode == synth.PlayPattern {
		g.eng.SetPlayMode(synth.PlayPattern, g.pattern)
	}
}

func (g *Game) togglePlayMode() {
	if g.playMode == synth.PlaySong {
		g.playMode = synth.PlayPattern
	} else {
		g.playMode = synth.PlaySong
	}
	g.eng.SetPlayMode(g.playMode, g.pattern)
}

func (g *Game) addTrack() {
	s := g.eng.Song()
	if len(s.Tracks) >= synth.MaxTracks {
		g.setStatus(fmt.Sprintf("at most %d tracks", synth.MaxTracks))
		return
	}
	g.eng.EditSong(func(s *synth.Song) {
		s.Tracks = append(s.Tracks, synth.NewTrack(fmt.Sprintf("track %d", len(s.Tracks)+1)))
	})
	g.selectTrack(len(s.Tracks))
}

func (g *Game) removeTrack() {
	if len(g.eng.Song().Tracks) <= 1 {
		g.setStatus("a song needs at least one track")
		return
	}
	i := g.track
	g.eng.EditSong(func(s *synth.Song) {
		s.Tracks = append(s.Tracks[:i], s.Tracks[i+1:]...)
		for p := range s.Patterns {
			ns := s.Patterns[p].Notes
			s.Patterns[p].Notes = append(ns[:i], ns[i+1:]...)
		}
	})
	g.selectTrack(i)
}

// newPattern adds a pattern: empty, or a copy of the current one.
func (g *Game) newPattern(copyCurrent bool) {
	if len(g.eng.Song().Patterns) >= synth.MaxPatterns {
		g.setStatus(fmt.Sprintf("at most %d patterns", synth.MaxPatterns))
		return
	}
	src := g.pattern
	g.eng.EditSong(func(s *synth.Song) {
		var p synth.Pattern
		if copyCurrent {
			p.Notes = append(p.Notes, s.Patterns[src].Notes...)
		}
		s.Patterns = append(s.Patterns, p) // sanitize fills empty rows
	})
	g.selectPattern(len(g.eng.Song().Patterns) - 1)
}

func (g *Game) appendToSong() {
	if len(g.eng.Song().Order) >= synth.MaxOrder {
		g.setStatus(fmt.Sprintf("the song is limited to %d slots", synth.MaxOrder))
		return
	}
	p := g.pattern
	g.eng.EditSong(func(s *synth.Song) { s.Order = append(s.Order, p) })
}

// removeFromSong removes the selected slot of the song strip, or the last
// one when nothing is selected.
func (g *Game) removeFromSong() {
	order := g.eng.Song().Order
	if len(order) <= 1 {
		g.setStatus("the song needs one slot; right click it to put another pattern there")
		return
	}
	i := g.slot
	if i < 0 || i >= len(order) {
		i = len(order) - 1
	}
	g.eng.EditSong(func(s *synth.Song) { s.Order = append(s.Order[:i], s.Order[i+1:]...) })
	g.slot = -1
}

// deletePattern removes the current pattern, drops its slots from the song
// and renames the later patterns (C becomes B, ...).
func (g *Game) deletePattern() {
	if len(g.eng.Song().Patterns) <= 1 {
		g.setStatus("the song needs at least one pattern")
		return
	}
	p := g.pattern
	g.eng.EditSong(func(s *synth.Song) {
		s.Patterns = append(s.Patterns[:p], s.Patterns[p+1:]...)
		var order []int
		for _, o := range s.Order {
			switch {
			case o == p:
				continue
			case o > p:
				o--
			}
			order = append(order, o)
		}
		if len(order) == 0 {
			order = []int{0} // the song was only this pattern
		}
		s.Order = order
	})
	g.slot = -1
	g.selectPattern(min(p, len(g.eng.Song().Patterns)-1))
	g.setStatus("deleted pattern " + synth.PatternName(p))
}

// clickOrder handles the arrangement strip: left click jumps there and edits
// that slot's pattern, right click puts the current pattern into the slot.
func (g *Game) clickOrder(mx, my int, right bool) {
	if !orderR.contains(mx, my) {
		return
	}
	slot := (mx - orderR.x) / cellW
	order := g.eng.Song().Order
	if slot >= len(order) {
		return
	}
	if right {
		p := g.pattern
		g.eng.EditSong(func(s *synth.Song) { s.Order[slot] = p })
		return
	}
	g.slot = slot
	g.selectPattern(order[slot])
	g.eng.Seek(slot)
}

func (g *Game) clickSequencer(mx, my int) {
	if !seqR.contains(mx, my) {
		return
	}
	col := (mx - seqR.x) * synth.Steps / seqR.w
	row := (my - seqR.y) * seqRows / seqR.h
	note := g.lowNote + seqRows - 1 - row
	tr, pat := g.track, g.pattern
	g.eng.EditSong(func(s *synth.Song) {
		steps := &s.Patterns[pat].Notes[tr]
		if steps[col] == note {
			steps[col] = synth.Rest
		} else {
			steps[col] = note
		}
	})
}

func (g *Game) clearSteps() {
	tr, pat := g.track, g.pattern
	g.eng.EditSong(func(s *synth.Song) {
		for i := range s.Patterns[pat].Notes[tr] {
			s.Patterns[pat].Notes[tr][i] = synth.Rest
		}
	})
}

func (g *Game) loadPreset(i int) {
	n := len(synth.Presets)
	i = (i%n + n) % n
	pr := synth.Presets[i]
	if err := g.eng.SetFormula(g.track, pr.Formula); err != nil {
		g.setStatus("preset error: " + err.Error())
		return
	}
	g.editTrack(func(t *synth.Track) { t.Mode, t.Rate = pr.Mode, pr.Rate })
	g.edit.Set(pr.Formula)
	g.formulaErr = ""
	g.preset = i
}

// loadInstrument replaces the selected track's sound with a template.
// level is the recent peak of track i (0..1), refreshed every frame.
func (g *Game) level(i int) float64 {
	if i < len(g.levels) {
		return g.levels[i]
	}
	return 0
}

// updateLevels measures each track's peak over the last ~20 ms.
func (g *Game) updateLevels(n int) {
	if len(g.levelBuf) == 0 {
		g.levelBuf = make([]float32, 1024)
	}
	g.levels = g.levels[:0]
	for i := range n {
		g.eng.Scope(g.levelBuf, i)
		peak := 0.0
		for _, v := range g.levelBuf {
			peak = max(peak, math.Abs(float64(v)))
		}
		g.levels = append(g.levels, peak)
	}
}

func (g *Game) confirmNew() bool { return time.Since(g.newAt) < 3*time.Second }

// newSong replaces everything with an empty one-track song. It needs two
// clicks within 3 seconds, so a stray click does not erase the work.
func (g *Game) newSong() {
	if !g.confirmNew() {
		g.newAt = time.Now()
		g.setStatus("click CLEAR ALL? again to start an empty song (unsaved changes are lost)")
		return
	}
	g.newAt = time.Time{}
	if err := g.eng.Load(synth.EmptySong()); err != nil {
		g.setStatus("new song failed: " + err.Error())
		return
	}
	g.slot = -1
	g.selectTrack(0)
	g.selectPattern(0)
	g.setStatus("empty song: click the grid to add notes, + TRACK for more instruments")
}

func (g *Game) loadInstrument(i int) {
	n := len(synth.Instruments)
	in := synth.Instruments[(i%n+n)%n]
	g.editTrack(func(t *synth.Track) { synth.ApplyInstrument(t, in) })
	g.edit.Set(in.Formula)
	g.formulaErr = ""
	g.preset = -1
}

func (g *Game) applyFormula() {
	g.preset = -1
	if err := g.eng.SetFormula(g.track, g.edit.String()); err != nil {
		g.formulaErr = err.Error()
	} else {
		g.formulaErr = ""
	}
}

func (g *Game) setStatus(s string) {
	g.status, g.statusUntil = s, time.Now().Add(5*time.Second)
}

// export asks where to save, then renders the whole arrangement in the
// background, so the UI and live audio keep running.
func (g *Game) export(format string) {
	if g.exporting {
		return
	}
	song := g.eng.Song()
	suggested := "bitwave-" + time.Now().Format("20060102-150405") + "." + format
	g.ask(func() (string, error) { return g.askSave("Export the song as "+strings.ToUpper(format), suggested) },
		func(path string) {
			if f := audiofile.FormatOf(path); f != "wav" && f != "flac" {
				path += "." + format
			}
			g.exporting = true
			g.setStatus("rendering song...")
			go func() { g.statusCh <- exportSong(song, path) }()
		})
}

func exportSong(song synth.Song, path string) string {
	e, err := synth.New(SampleRate, song)
	if err != nil {
		return "export failed: " + err.Error()
	}
	samples := e.RenderSong()
	if err := audiofile.WriteFile(path, SampleRate, samples); err != nil {
		return "export failed: " + err.Error()
	}
	return fmt.Sprintf("saved %.1fs song to %s", float64(len(samples))/SampleRate, path)
}

// ask runs a blocking file dialog off the UI thread; then runs on the UI
// thread (from Update) with the chosen path. Cancel does nothing.
func (g *Game) ask(dlg func() (string, error), then func(path string)) {
	if g.dialogOpen {
		return
	}
	g.dialogOpen = true
	go func() {
		path, err := dlg()
		g.dialogCh <- func() {
			g.dialogOpen = false
			switch {
			case errors.Is(err, dialog.ErrCanceled):
			case err != nil:
				g.setStatus("file dialog failed: " + err.Error())
			default:
				then(path)
			}
		}
	}()
}

// save asks for a file name and writes the song there ("Save as").
func (g *Game) save() {
	g.ask(func() (string, error) { return g.askSave("Save the song as", "my song.json") },
		func(path string) {
			if !strings.EqualFold(filepath.Ext(path), ".json") {
				path += ".json"
			}
			if err := g.writeSongTo(path); err != nil {
				g.setStatus("save failed: " + err.Error())
				return
			}
			g.setStatus("saved " + path)
		})
}

// writeSongTo writes the song atomically (temp file + rename), so a crash
// mid-write never leaves a broken file.
func (g *Game) writeSongTo(path string) error {
	b, err := json.MarshalIndent(g.eng.Song(), "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	if path == g.songPath {
		g.savedJSON, _ = json.Marshal(g.eng.Song())
	}
	return nil
}

// autosave writes the song to songPath when it changed, at most every
// autosaveEvery (or right away with force, e.g. when the window closes).
func (g *Game) autosave(now time.Time, force bool) {
	if !force && now.Sub(g.lastAutosave) < autosaveEvery {
		return
	}
	g.lastAutosave = now
	cur, err := json.Marshal(g.eng.Song())
	if err != nil || bytes.Equal(cur, g.savedJSON) {
		return
	}
	if err := g.writeSongTo(g.songPath); err != nil {
		g.setStatus("autosave failed: " + err.Error())
	}
}

// load asks for a song file and opens it.
func (g *Game) load() {
	g.ask(func() (string, error) { return g.askOpen("Open a song") }, g.loadFrom)
}

func (g *Game) loadFrom(path string) {
	s, err := LoadSong(path)
	if err == nil {
		err = g.eng.Load(s)
	}
	if err != nil {
		g.setStatus("load failed: " + err.Error())
		return
	}
	g.slot = -1
	g.selectTrack(0)
	g.selectPattern(0)
	g.setStatus("loaded " + path)
}

// LoadSong reads a project saved with SAVE (or a single-track patch from
// earlier versions).
func LoadSong(path string) (synth.Song, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return synth.Song{}, err
	}
	return synth.ParseSong(b)
}

func (g *Game) layout() {
	g.buttons = g.buttons[:0]
	g.sliders = g.sliders[:0]
	g.snap = g.eng.Song()
	s := g.snap
	x := 10
	add := func(y int, label func() string, w int, fn func()) *button {
		if w == 0 {
			w = len(label())*charW + 14
		}
		g.buttons = append(g.buttons, button{r: rect{x, y, w, 22}, label: label, onClick: fn})
		x += w + 4
		return &g.buttons[len(g.buttons)-1]
	}
	text := func(s string) func() string { return func() string { return s } }

	// Transport and files.
	y := rowTransport
	add(y, func() string {
		if g.playing() {
			return "STOP"
		}
		return "PLAY"
	}, 50, g.togglePlay)
	add(y, text("RESET"), 0, g.eng.Reset)
	add(y, func() string {
		if g.playMode == synth.PlayPattern {
			return "LOOP PATTERN"
		}
		return "PLAY SONG"
	}, 90, g.togglePlayMode)
	x += 8
	add(y, text("-"), 20, func() { g.eng.EditSong(func(s *synth.Song) { s.BPM -= 2 }) })
	add(y, func() string { return fmt.Sprintf("BPM %.0f", g.snap.BPM) }, 62, nil)
	add(y, text("+"), 20, func() { g.eng.EditSong(func(s *synth.Song) { s.BPM += 2 }) })
	x += 8
	add(y, text("-"), 20, func() { g.eng.EditSong(func(s *synth.Song) { s.Volume -= 0.1 }) })
	add(y, func() string { return fmt.Sprintf("MASTER %.0f%%", g.snap.Volume*100) }, 90, nil)
	add(y, text("+"), 20, func() { g.eng.EditSong(func(s *synth.Song) { s.Volume += 0.1 }) })
	x += 8
	add(y, func() string {
		if g.confirmNew() {
			return "CLEAR ALL?"
		}
		return "NEW SONG"
	}, 80, g.newSong)
	x = Width - 10 - 4*94 + 4
	add(y, text("EXPORT WAV"), 90, func() { g.export("wav") })
	add(y, text("EXPORT FLAC"), 90, func() { g.export("flac") })
	add(y, text("SAVE"), 90, g.save)
	add(y, text("LOAD"), 90, g.load)

	// Track tabs: name selects, M mutes, S solos.
	x = 10
	y = rowTracks
	for i, t := range s.Tracks {
		label := fmt.Sprintf("%d %s", i+1, t.Name)
		if len(label) > 10 {
			label = label[:10]
		}
		b := add(y, text(label), tabW-48, func() { g.selectTrack(i) })
		b.on = func() bool { return g.track == i }
		b.meter = func() float64 { return g.level(i) }
		x -= 4
		m := add(y, text("M"), 20, func() {
			g.eng.EditSong(func(s *synth.Song) { s.Tracks[i].Mute = !s.Tracks[i].Mute })
		})
		m.on = func() bool { return g.snapFlag(i, func(t synth.Track) bool { return t.Mute }) }
		x -= 4
		so := add(y, text("S"), 20, func() {
			g.eng.EditSong(func(s *synth.Song) { s.Tracks[i].Solo = !s.Tracks[i].Solo })
		})
		so.on = func() bool { return g.snapFlag(i, func(t synth.Track) bool { return t.Solo }) }
		x += 4
	}
	add(y, text("+ TRACK"), 0, g.addTrack)
	add(y, text("- TRACK"), 0, g.removeTrack)

	// Selected track settings: the instrument and volume always; the
	// formula-level controls only after SHOW FORMULA.
	x = 10
	y = rowTrackCtl
	add(y, func() string { return fmt.Sprintf("TRACK %d", g.track+1) }, 60, nil)
	add(y, text("<"), 20, func() { g.loadInstrument(synth.InstrumentIndex(g.snapTrack().Instrument) - 1) })
	add(y, func() string {
		if in := g.snapTrack().Instrument; in != "" {
			return "INSTRUMENT " + in
		}
		return "INSTRUMENT custom"
	}, 140, nil)
	add(y, text(">"), 20, func() { g.loadInstrument(synth.InstrumentIndex(g.snapTrack().Instrument) + 1) })
	x += 8
	add(y, text("-"), 20, func() { g.editTrack(func(t *synth.Track) { t.Volume -= 0.05 }) })
	add(y, func() string { return fmt.Sprintf("VOL %.0f%%", g.snapTrack().Volume*100) }, 66, nil)
	add(y, text("+"), 20, func() { g.editTrack(func(t *synth.Track) { t.Volume += 0.05 }) })
	x += 8
	add(y, func() string {
		if g.showFormula {
			return "HIDE FORMULA"
		}
		return "SHOW FORMULA"
	}, 90, func() {
		g.showFormula = !g.showFormula
		g.focus = false
	})
	if g.showFormula {
		x += 8
		add(y, func() string { return "MODE " + g.snapTrack().Mode.String() }, 80, func() {
			g.editTrack(func(t *synth.Track) {
				if t.Mode == synth.ModeByte {
					t.Mode = synth.ModeFloat
				} else {
					t.Mode = synth.ModeByte
				}
			})
		})
		x += 4
		add(y, text("-"), 20, func() { g.editTrack(func(t *synth.Track) { t.Rate = stepRate(t.Rate, -1) }) })
		add(y, func() string { return fmt.Sprintf("RATE %d", g.snapTrack().Rate) }, 82, nil)
		add(y, text("+"), 20, func() { g.editTrack(func(t *synth.Track) { t.Rate = stepRate(t.Rate, 1) }) })
		x += 8
		add(y, text("<"), 20, func() { g.loadPreset(g.preset - 1) })
		add(y, func() string {
			if g.preset < 0 {
				return "FORMULA PRESET"
			}
			return synth.Presets[g.preset].Name
		}, 130, nil)
		add(y, text(">"), 20, func() { g.loadPreset(g.preset + 1) })
	}

	g.layoutSound()

	// Oscilloscope and wave shapes.
	x = scopeR.x
	y = scopeR.y + scopeR.h + 6
	add(y, func() string {
		if g.bitsView {
			return "VIEW BITS"
		}
		return "VIEW WAVE"
	}, 80, func() { g.bitsView = !g.bitsView })
	add(y, func() string {
		if g.scopeMix && !g.bitsView {
			return "SHOW MIX"
		}
		return "SHOW TRACK"
	}, 80, func() { g.scopeMix = !g.scopeMix })
	add(y, text("ZOOM -"), 0, func() { g.zoom = min(g.zoom*2, synth.ScopeSize/2) })
	add(y, text("ZOOM +"), 0, func() { g.zoom = max(g.zoom/2, 64) })
	x = waveR.x
	for _, sh := range synth.Shapes {
		add(y, text(strings.ToUpper(sh)), 0, func() {
			g.editTrack(func(t *synth.Track) { t.Wave = synth.Shape(sh) })
		})
	}

	// Patterns.
	x = 10
	y = rowPatterns
	add(y, text("PATTERN"), 50, nil)
	for i := range s.Patterns {
		b := add(y, text(synth.PatternName(i)), 22, func() { g.selectPattern(i) })
		b.on = func() bool { return g.pattern == i }
		b.mark = func() bool { return g.playing() && g.status0.Pattern == i }
	}
	add(y, text("NEW"), 0, func() { g.newPattern(false) })
	add(y, text("COPY"), 0, func() { g.newPattern(true) })
	add(y, text("DELETE"), 0, g.deletePattern)
	x = Width - 10 - 2*94 + 4
	add(y, text("+ TO SONG"), 90, g.appendToSong)
	add(y, text("- FROM SONG"), 90, g.removeFromSong)

	// Sequencer.
	x = seqR.x
	y = rowSeqCtl
	add(y, text("OCT -"), 0, func() { g.lowNote = max(g.lowNote-12, 0) })
	add(y, text("OCT +"), 0, func() { g.lowNote = min(g.lowNote+12, synth.MaxNote-seqRows+1) })
	add(y, text("CLEAR"), 0, g.clearSteps)
}

// layoutSound builds the knob and ADSR controls of the selected track.
func (g *Game) layoutSound() {
	x, y := 10, rowSound
	addSlider := func(w int, label func() string, get func() float64, set func(float64), dimmed func() bool) {
		g.sliders = append(g.sliders, slider{rect{x, y, w, 22}, label, get, set, dimmed})
		x += w + 4
	}
	for k := range 4 {
		addSlider(150,
			func() string {
				t := g.snapTrack()
				return fmt.Sprintf("%s %.2f", t.KnobNames[k], t.Knobs[k])
			},
			func() float64 { return g.snapTrack().Knobs[k] },
			func(v float64) { g.editTrack(func(t *synth.Track) { t.Knobs[k] = v }) },
			nil)
	}

	b := button{r: rect{x, y, 66, 22}, label: func() string {
		if g.snapTrack().Env.On {
			return "ADSR ON"
		}
		return "ADSR OFF"
	}, onClick: func() { g.editTrack(func(t *synth.Track) { t.Env.On = !t.Env.On }) }}
	b.on = func() bool { return g.snapTrack().Env.On }
	g.buttons = append(g.buttons, b)
	x += 70

	off := func() bool { return !g.snapTrack().Env.On }
	env := func(fn func(e *synth.ADSR)) { g.editTrack(func(t *synth.Track) { fn(&t.Env) }) }
	for _, p := range envParams {
		addSlider(70,
			func() string { return p.label(g.snapTrack().Env) },
			func() float64 { return p.get(g.snapTrack().Env) },
			func(v float64) { env(func(e *synth.ADSR) { p.set(e, v) }) },
			off)
	}
}

// envParam maps a 0..1 slider to an envelope field. Times use a square
// curve so short values, which matter most, get most of the travel.
type envParam struct {
	label func(synth.ADSR) string
	get   func(synth.ADSR) float64
	set   func(*synth.ADSR, float64)
}

var envParams = []envParam{
	{
		func(e synth.ADSR) string { return fmt.Sprintf("A %.2fs", e.A) },
		func(e synth.ADSR) float64 { return math.Sqrt(e.A / maxEnvTime) },
		func(e *synth.ADSR, v float64) { e.A = v * v * maxEnvTime },
	},
	{
		func(e synth.ADSR) string { return fmt.Sprintf("D %.2fs", e.D) },
		func(e synth.ADSR) float64 { return math.Sqrt(e.D / maxEnvTime) },
		func(e *synth.ADSR, v float64) { e.D = v * v * maxEnvTime },
	},
	{
		func(e synth.ADSR) string { return fmt.Sprintf("S %.2f", e.S) },
		func(e synth.ADSR) float64 { return e.S },
		func(e *synth.ADSR, v float64) { e.S = v },
	},
	{
		func(e synth.ADSR) string { return fmt.Sprintf("R %.2fs", e.R) },
		func(e synth.ADSR) float64 { return math.Sqrt(e.R / maxRelease) },
		func(e *synth.ADSR, v float64) { e.R = v * v * maxRelease },
	},
	{
		// Gate in steps on an exponential scale: 0.25 .. 8.
		func(e synth.ADSR) string { return fmt.Sprintf("GATE %.2g", e.Gate) },
		func(e synth.ADSR) float64 { return math.Log2(e.Gate/synth.MinGate) / gateOctaves },
		func(e *synth.ADSR, v float64) { e.Gate = synth.MinGate * math.Pow(2, v*gateOctaves) },
	},
}

const (
	maxEnvTime  = 2.0 // seconds at the end of the A and D sliders
	maxRelease  = 4.0
	gateOctaves = 5 // 0.25 * 2^5 = 8 steps
)

// sliderAt returns the slider under (mx, my), or -1.
func (g *Game) sliderAt(mx, my int) int {
	for i, sl := range g.sliders {
		if sl.r.contains(mx, my) {
			return i
		}
	}
	return -1
}

// dragSlider sets slider i from the mouse x position.
func (g *Game) dragSlider(i, mx int) {
	if i < 0 || i >= len(g.sliders) {
		return
	}
	sl := g.sliders[i]
	sl.set(min(max(float64(mx-sl.r.x)/float64(sl.r.w-1), 0), 1))
}

func stepRate(r, dir int) int {
	rates := []int{1000, 2000, 4000, 8000, 11025, 16000, 22050, 32000, 44100, 48000}
	for i, v := range rates {
		if v >= r {
			if v == r {
				i += dir
			} else if dir < 0 {
				i--
			}
			return rates[min(max(i, 0), len(rates)-1)]
		}
	}
	return rates[len(rates)-1]
}

// click dispatches a mouse click to buttons and editors.
func (g *Game) click(mx, my int, right bool) {
	g.layout()
	if right {
		g.clickOrder(mx, my, true)
		return
	}
	g.focus = g.showFormula && formulaR.contains(mx, my)
	if i := g.sliderAt(mx, my); i >= 0 {
		g.dragging = i
		g.dragSlider(i, mx)
		return
	}
	for _, b := range g.buttons {
		if b.onClick != nil && b.r.contains(mx, my) {
			b.onClick()
			break
		}
	}
	g.clickOrder(mx, my, false)
	g.clickSequencer(mx, my)
}

// Update handles input. It runs at 60 TPS.
func (g *Game) Update() error {
	if ebiten.IsWindowBeingClosed() {
		g.autosave(time.Now(), true)
		return ebiten.Termination
	}
	g.autosave(time.Now(), false)
	g.blink++
	select {
	case s := <-g.statusCh:
		g.exporting = false
		g.setStatus(s)
	case fn := <-g.dialogCh:
		fn()
	default:
	}
	mx, my := ebiten.CursorPosition()
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		g.click(mx, my, false)
	}
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonRight) {
		g.click(mx, my, true)
	}
	if g.dragging >= 0 {
		if ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
			g.dragSlider(g.dragging, mx)
		} else {
			g.dragging = -1
		}
	}
	g.updateWaveDrawing(mx, my)
	if g.focus {
		g.updateTyping()
	} else if inpututil.IsKeyJustPressed(ebiten.KeySpace) {
		g.togglePlay()
	}
	g.layout()
	return nil
}

func (g *Game) updateWaveDrawing(mx, my int) {
	if !ebiten.IsMouseButtonPressed(ebiten.MouseButtonLeft) {
		g.drawing = false
		return
	}
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) && waveR.contains(mx, my) {
		g.drawing = true
		g.lastI = -1
	}
	if g.drawing {
		g.paint(mx, my)
	}
}

// paint draws on the wave editor from the last mouse position to (mx, my).
func (g *Game) paint(mx, my int) {
	i := (mx - waveR.x) * expr.WaveSize / waveR.w
	v := 255 * (1 - float64(my-waveR.y)/float64(waveR.h))
	li, lv := g.lastI, g.lastV
	if li < 0 {
		li, lv = i, v
	}
	g.editTrack(func(t *synth.Track) { paintWave(&t.Wave, li, lv, i, v) })
	g.lastI, g.lastV = i, v
}

func repeat(k ebiten.Key) bool {
	d := inpututil.KeyPressDuration(k)
	return d == 1 || d >= 24 && d%3 == 0
}

func (g *Game) updateTyping() {
	changed := false
	var rs []rune
	for _, r := range ebiten.AppendInputChars(nil) {
		if r >= ' ' && r < 0x7f {
			rs = append(rs, r)
		}
	}
	mod := ebiten.IsKeyPressed(ebiten.KeyMeta) || ebiten.IsKeyPressed(ebiten.KeyControl)
	if mod && inpututil.IsKeyJustPressed(ebiten.KeyV) {
		s, err := clipboard.Read()
		if err != nil {
			g.setStatus(err.Error())
		} else {
			rs = []rune(s)
		}
	}
	if mod && inpututil.IsKeyJustPressed(ebiten.KeyA) {
		g.edit.Set("")
		changed = true
	}
	if len(rs) > 0 {
		g.edit.Insert(rs)
		changed = true
	}
	if repeat(ebiten.KeyBackspace) {
		changed = g.edit.Backspace() || changed
	}
	if repeat(ebiten.KeyDelete) {
		changed = g.edit.Delete() || changed
	}
	if repeat(ebiten.KeyArrowLeft) {
		g.edit.Left()
	}
	if repeat(ebiten.KeyArrowRight) {
		g.edit.Right()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyHome) || inpututil.IsKeyJustPressed(ebiten.KeyArrowUp) {
		g.edit.Home()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEnd) || inpututil.IsKeyJustPressed(ebiten.KeyArrowDown) {
		g.edit.End()
	}
	if inpututil.IsKeyJustPressed(ebiten.KeyEnter) || inpututil.IsKeyJustPressed(ebiten.KeyEscape) {
		g.focus = false
	}
	if changed {
		g.applyFormula() // live coding: every valid edit is heard immediately
	}
}

// Layout implements ebiten.Game with a fixed logical size.
func (g *Game) Layout(int, int) (int, int) { return Width, Height }

// Draw renders the whole window.
// The screen is not cleared between frames (see main), so skipping a frame
// keeps the previous picture: this caps redraws at 30 per second.
func (g *Game) Draw(dst *ebiten.Image) {
	if now := time.Now(); now.Sub(g.lastDraw) < frameInterval {
		return
	} else {
		g.lastDraw = now
	}
	dst.Fill(colBG)
	g.snap = g.eng.Song()
	song := g.snap
	st := g.eng.Status()
	g.status0 = st
	g.updateLevels(len(song.Tracks))
	mx, my := ebiten.CursorPosition()
	for _, b := range g.buttons {
		label := b.label()
		if b.onClick != nil {
			c := colBtn
			switch {
			case b.on != nil && b.on() && label == "M":
				c = colMute
			case b.on != nil && b.on():
				c = colBtnOn
			case b.r.contains(mx, my):
				c = colBtnHot
			}
			fill(dst, b.r, c)
		}
		if b.meter != nil {
			if lv := b.meter(); lv > 0.01 {
				vector.FillRect(dst, float32(b.r.x), float32(b.r.y+b.r.h-3), float32(lv)*float32(b.r.w), 3, colScope, false)
			}
		}
		if b.mark != nil && b.mark() {
			vector.FillRect(dst, float32(b.r.x), float32(b.r.y+b.r.h-3), float32(b.r.w), 3, colPlay, false)
		}
		ebitenutil.DebugPrintAt(dst, label, b.r.x+(b.r.w-len(label)*charW)/2, b.r.y+3)
	}
	g.drawSliders(dst)
	g.drawFormula(dst, st)
	g.drawScope(dst)
	g.drawWave(dst, song, st)
	g.drawOrder(dst, song, st)
	g.drawSequencer(dst, song, st)

	help := "SPACE play/stop   pick a track tab, then INSTRUMENT < > for its sound   drag the knobs and A D S R GATE to shape it\n" +
		"click the grid to place notes   PATTERN A B ... switch, DELETE removes one   SONG: click = select & jump, right click = put pattern, - FROM SONG removes the selected\n" +
		"draw in the orange panel to change the wave (bass, pluck)   M mute  S solo   EXPORT saves the whole song"
	if g.showFormula {
		help = "vars: t time  p note phase  f Hz  g gate  e envelope  n ticks since note  s step  r rate  k1..k4 knobs  w(x) drawn wave\n" +
			"ops: + - * / % ** & | ^ ~ << >> >>> < > == != && || !  a?b:c  [a,b,c][i]  sin cos tanh abs floor min max pow exp random ...\n" +
			"click the formula to edit (Cmd/Ctrl+V paste), every valid change is heard at once   HIDE FORMULA to go back"
	}
	ebitenutil.DebugPrintAt(dst, help, 10, Height-50)
}

func (g *Game) drawSliders(dst *ebiten.Image) {
	for i, sl := range g.sliders {
		fg := colWave
		if sl.dimmed != nil && sl.dimmed() {
			fg = colBtnHot
		}
		fill(dst, sl.r, colPanel2)
		w := float32(sl.get()) * float32(sl.r.w)
		vector.FillRect(dst, float32(sl.r.x), float32(sl.r.y+sl.r.h-4), w, 4, fg, false)
		vector.FillRect(dst, float32(sl.r.x)+w-1, float32(sl.r.y), 2, float32(sl.r.h), fg, false)
		if i == g.dragging {
			vector.StrokeRect(dst, float32(sl.r.x), float32(sl.r.y), float32(sl.r.w), float32(sl.r.h), 1, colFocus, false)
		}
		ebitenutil.DebugPrintAt(dst, sl.label(), sl.r.x+4, sl.r.y+2)
	}
}

func (g *Game) drawFormula(dst *ebiten.Image, st synth.Status) {
	if !g.showFormula {
		// Without the editor, the row only shows playback status.
		g.drawStatus(dst, st, formulaR.y+4)
		return
	}
	fill(dst, formulaR, colPanel2)
	if g.focus {
		vector.StrokeRect(dst, float32(formulaR.x), float32(formulaR.y), float32(formulaR.w), float32(formulaR.h), 1, colFocus, false)
	}
	maxChars := (formulaR.w - 16) / charW
	start := max(0, g.edit.cur-maxChars)
	end := min(len(g.edit.text), start+maxChars)
	ebitenutil.DebugPrintAt(dst, string(g.edit.text[start:end]), formulaR.x+8, formulaR.y+4)
	if g.focus && g.blink/30%2 == 0 {
		cx := float32(formulaR.x + 8 + (g.edit.cur-start)*charW)
		vector.StrokeLine(dst, cx, float32(formulaR.y+4), cx, float32(formulaR.y+20), 1, colScope, false)
	}

	g.drawStatus(dst, st, formulaR.y+formulaR.h+4)
}

// drawStatus shows a formula error, a recent status message, or the
// playback position.
func (g *Game) drawStatus(dst *ebiten.Image, st synth.Status, y int) {
	switch {
	case g.showFormula && g.formulaErr != "":
		msg := "error " + g.formulaErr + " (still playing the last valid formula)"
		fill(dst, rect{formulaR.x, y, len(msg)*charW + 12, 22}, colError)
		ebitenutil.DebugPrintAt(dst, msg, formulaR.x+6, y+3)
	case g.status != "" && time.Now().Before(g.statusUntil):
		ebitenutil.DebugPrintAt(dst, g.status, formulaR.x+6, y+3)
	default:
		t := int64(0)
		if g.track < len(st.T) {
			t = st.T[g.track]
		}
		ebitenutil.DebugPrintAt(dst, fmt.Sprintf("t=%d  pattern %s  step %d  slot %d",
			t, synth.PatternName(st.Pattern), st.Step+1, st.Slot+1), formulaR.x+6, y+3)
	}
}

func (g *Game) drawScope(dst *ebiten.Image) {
	r := scopeR
	fill(dst, r, colPanel)
	if len(g.scopeBuf) != 2*g.zoom {
		g.scopeBuf = make([]float32, 2*g.zoom)
	}
	src := g.track
	if g.scopeMix && !g.bitsView {
		src = synth.ScopeMix
	}
	g.eng.Scope(g.scopeBuf, src)
	trig := findTrigger(g.scopeBuf, 0, g.zoom)
	win := g.scopeBuf[trig : trig+g.zoom]

	if g.bitsView {
		// Each row is one bit of the track's output byte, bit 7 on top: this
		// makes the structure of bitwise formulas directly visible.
		rowH := float32(r.h) / 8
		for bit := range 8 {
			y := float32(r.y) + float32(7-bit)*rowH
			ebitenutil.DebugPrintAt(dst, fmt.Sprint(bit), r.x+3, int(y+rowH/2)-8)
			runStart := -1
			for x := 0; x <= r.w; x++ {
				on := x < r.w && toByte(win[x*len(win)/r.w])>>bit&1 == 1
				if on && runStart < 0 {
					runStart = x
				} else if !on && runStart >= 0 {
					vector.FillRect(dst, float32(r.x+runStart), y+1, float32(x-runStart), rowH-2, colScope, false)
					runStart = -1
				}
			}
		}
		ebitenutil.DebugPrintAt(dst, fmt.Sprintf("track %d bits", g.track+1), r.x+r.w-100, r.y+4)
		return
	}

	mid := float32(r.y) + float32(r.h)/2
	vector.StrokeLine(dst, float32(r.x), mid, float32(r.x+r.w), mid, 1, colGrid, false)
	for i := 1; i < 8; i++ {
		x := float32(r.x + i*r.w/8)
		vector.StrokeLine(dst, x, float32(r.y), x, float32(r.y+r.h), 1, colGrid, false)
	}
	// One path for the whole trace: a vertical min-max bar per pixel
	// column joined by the column midpoints. Drawing it in one call instead
	// of a line per column keeps the CPU (and the laptop) cool.
	lo, hi := columns(win, r.w)
	sy := func(v float32) float32 { return mid - v*float32(r.h)/2*0.95 }
	g.path.Reset()
	for x := range r.w {
		fx := float32(r.x + x)
		g.path.MoveTo(fx, sy(lo[x]))
		g.path.LineTo(fx, sy(hi[x])+1)
	}
	for x := range r.w {
		fx, c := float32(r.x+x), sy((lo[x]+hi[x])/2)
		if x == 0 {
			g.path.MoveTo(fx, c)
		} else {
			g.path.LineTo(fx, c)
		}
	}
	strokePath(dst, &g.path, 1, colScope)
	what := "mix"
	if src != synth.ScopeMix {
		what = fmt.Sprintf("track %d", g.track+1)
	}
	ebitenutil.DebugPrintAt(dst, fmt.Sprintf("%s  %.1f ms", what, float64(g.zoom)*1000/SampleRate), r.x+r.w-130, r.y+4)
}

func (g *Game) drawWave(dst *ebiten.Image, song synth.Song, st synth.Status) {
	r := waveR
	fill(dst, r, colPanel)
	mid := float32(r.y) + float32(r.h)/2
	vector.StrokeLine(dst, float32(r.x), mid, float32(r.x+r.w), mid, 1, colGrid, false)
	w := song.Tracks[g.track].Wave
	px := func(i int) float32 { return float32(r.x) + float32(i)*float32(r.w)/expr.WaveSize }
	py := func(v float64) float32 { return float32(r.y) + float32(r.h)*float32(1-v/255) }
	g.path.Reset()
	g.path.MoveTo(px(0), py(w[0]))
	for i := 1; i < expr.WaveSize; i++ {
		g.path.LineTo(px(i), py(w[i]))
	}
	strokePath(dst, &g.path, 2, colWave)
	if g.playing() && g.track < len(st.Phase) {
		x := px(int(st.Phase[g.track]))
		vector.StrokeLine(dst, x, float32(r.y), x, float32(r.y+r.h), 1, colPlay, false)
	}
	ebitenutil.DebugPrintAt(dst, fmt.Sprintf("track %d wave  w(x)", g.track+1), r.x+4, r.y+4)
}

func (g *Game) drawOrder(dst *ebiten.Image, song synth.Song, st synth.Status) {
	r := orderR
	ebitenutil.DebugPrintAt(dst, "SONG", 12, r.y+3)
	fill(dst, rect{r.x, r.y, r.w, r.h}, colPanel)
	for i, p := range song.Order {
		cell := rect{r.x + i*cellW, r.y, cellW - 1, r.h}
		c := colBtn
		switch {
		case st.Mode == synth.PlaySong && i == st.Slot && g.playing():
			c = colPlay
		case p == g.pattern:
			c = colBtnOn
		}
		fill(dst, cell, c)
		if i == g.slot { // selected: "- FROM SONG" removes this one
			vector.StrokeRect(dst, float32(cell.x), float32(cell.y), float32(cell.w), float32(cell.h), 2, colWave, false)
		}
		ebitenutil.DebugPrintAt(dst, synth.PatternName(p), cell.x+5, cell.y+3)
	}
	secs := float64(len(song.Order)*synth.Steps) * 60 / song.BPM / 4
	ebitenutil.DebugPrintAt(dst, fmt.Sprintf("%d bars  %d:%04.1f", len(song.Order), int(secs)/60, secs-float64(int(secs)/60*60)),
		r.x, r.y+r.h+2)
}

func (g *Game) drawSequencer(dst *ebiten.Image, song synth.Song, st synth.Status) {
	r := seqR
	colW := float32(r.w) / synth.Steps
	rowH := float32(r.h) / seqRows

	label := fmt.Sprintf("pattern %s  track %d %s", synth.PatternName(g.pattern), g.track+1, song.Tracks[g.track].Name)
	ebitenutil.DebugPrintAt(dst, label, Width-10-len(label)*charW, rowSeqCtl+3)

	for row := range seqRows {
		note := g.lowNote + seqRows - 1 - row
		y := float32(r.y) + float32(row)*rowH
		c := colPanel
		switch note % 12 {
		case 1, 3, 6, 8, 10:
			c = colBlackKy
		}
		vector.FillRect(dst, float32(r.x), y, float32(r.w), rowH, c, false)
		if note%12 == 0 {
			ebitenutil.DebugPrintAt(dst, synth.NoteName(note), 12, int(y)-5)
		}
	}
	for col := 0; col <= synth.Steps; col++ {
		x := float32(r.x) + float32(col)*colW
		c := colGrid
		if col%4 == 0 {
			c = colBtnHot
		}
		vector.StrokeLine(dst, x, float32(r.y), x, float32(r.y+r.h), 1, c, false)
	}
	if g.playing() && st.Pattern == g.pattern {
		x := float32(r.x) + float32(st.Step)*colW
		vector.FillRect(dst, x, float32(r.y), colW, float32(r.h), colPlayDim, false)
	}
	for col, note := range song.Patterns[g.pattern].Notes[g.track] {
		x := float32(r.x) + float32(col)*colW
		ebitenutil.DebugPrintAt(dst, synth.NoteName(note), int(x)+int(colW)/2-9, r.y-18)
		if note == synth.Rest {
			continue
		}
		row := g.lowNote + seqRows - 1 - note
		if row < 0 || row >= seqRows {
			// Off-screen note: mark the edge it is beyond.
			y := float32(r.y)
			if row >= seqRows {
				y = float32(r.y+r.h) - 3
			}
			vector.FillRect(dst, x+2, y, colW-4, 3, colNote, false)
			continue
		}
		vector.FillRect(dst, x+2, float32(r.y)+float32(row)*rowH+1, colW-4, rowH-2, colNote, false)
	}
}

func strokePath(dst *ebiten.Image, p *vector.Path, width float32, c color.Color) {
	op := &vector.DrawPathOptions{AntiAlias: true}
	op.ColorScale.ScaleWithColor(c)
	vector.StrokePath(dst, p, &vector.StrokeOptions{Width: width, LineJoin: vector.LineJoinRound}, op)
}

func fill(dst *ebiten.Image, r rect, c color.Color) {
	vector.FillRect(dst, float32(r.x), float32(r.y), float32(r.w), float32(r.h), c, false)
}
