// Package ui is the Ebitengine front end: tracks, formula editor,
// oscilloscope, wave editor, patterns, arrangement and step sequencer.
package ui

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/color"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/hajimehoshi/ebiten/v2"
	"github.com/hajimehoshi/ebiten/v2/audio"
	"github.com/hajimehoshi/ebiten/v2/ebitenutil"
	"github.com/hajimehoshi/ebiten/v2/inpututil"
	"github.com/hajimehoshi/ebiten/v2/vector"

	"github.com/pristupaanastasia/bitwave/internal/audiofile"
	"github.com/pristupaanastasia/bitwave/internal/clipboard"
	"github.com/pristupaanastasia/bitwave/internal/expr"
	"github.com/pristupaanastasia/bitwave/internal/synth"
)

const (
	Width      = 1100
	Height     = 830
	SampleRate = 44100

	charW   = 6
	seqRows = 25 // two octaves plus the top note

	// SongFile is where SAVE and LOAD keep the project.
	SongFile = "bitwave-song.json"
	// legacyFile is the single-track file of earlier versions; LOAD falls
	// back to it.
	legacyFile = "bitwave-patch.json"
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
	rowPatterns  = 404
	rowSeqCtl    = 474
	tabW         = 110
	cellW        = 16
)

var (
	formulaR = rect{10, 92, Width - 20, 24}
	scopeR   = rect{10, 146, 640, 220}
	waveR    = rect{660, 146, Width - 670, 220}
	orderR   = rect{60, 434, synth.MaxOrder * cellW, 22}
	seqR     = rect{50, 520, Width - 60, seqRows * 10}
)

type button struct {
	r       rect
	label   func() string
	onClick func()
	on      func() bool // highlighted state, optional
}

// Game implements ebiten.Game.
type Game struct {
	eng    *synth.Engine
	player *audio.Player

	track    int // track being edited
	pattern  int // pattern being edited
	playMode synth.PlayMode

	edit       lineEdit
	focus      bool
	formulaErr string

	status      string
	statusUntil time.Time
	statusCh    chan string

	bitsView  bool
	scopeMix  bool
	zoom      int // samples shown across the oscilloscope
	preset    int
	lowNote   int // lowest note shown in the sequencer
	exporting bool

	drawing bool
	lastI   int
	lastV   float64

	buttons  []button
	scopeBuf []float32
	blink    int

	// snap is one copy of the song per frame for labels and drawing, so the
	// UI does not clone the song under the audio lock dozens of times a frame.
	snap synth.Song
}

// New creates the UI around an engine and starts audio playback.
func New(eng *synth.Engine) (*Game, error) {
	ctx := audio.NewContext(SampleRate)
	player, err := ctx.NewPlayerF32(eng)
	if err != nil {
		return nil, err
	}
	player.SetBufferSize(60 * time.Millisecond)
	player.Play()
	g := newGame(eng)
	g.player = player
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

func (g *Game) removeLastFromSong() {
	g.eng.EditSong(func(s *synth.Song) {
		if len(s.Order) > 1 {
			s.Order = s.Order[:len(s.Order)-1]
		}
	})
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

// export renders the whole arrangement to a file in the background, so the
// UI and live audio keep running.
func (g *Game) export(format string) {
	if g.exporting {
		return
	}
	g.exporting = true
	song := g.eng.Song()
	g.setStatus("rendering song...")
	go func() {
		g.statusCh <- exportSong(song, format, time.Now())
	}()
}

func exportSong(song synth.Song, format string, now time.Time) string {
	e, err := synth.New(SampleRate, song)
	if err != nil {
		return "export failed: " + err.Error()
	}
	name := "bitwave-" + now.Format("20060102-150405") + "." + format
	samples := e.RenderSong()
	if err := audiofile.WriteFile(name, SampleRate, samples); err != nil {
		return "export failed: " + err.Error()
	}
	return fmt.Sprintf("saved %.1fs song to %s", float64(len(samples))/SampleRate, name)
}

func (g *Game) save() {
	b, err := json.MarshalIndent(g.eng.Song(), "", "  ")
	if err == nil {
		err = os.WriteFile(SongFile, b, 0o644)
	}
	if err != nil {
		g.setStatus("save failed: " + err.Error())
		return
	}
	g.setStatus("saved " + SongFile)
}

func (g *Game) load() {
	name := SongFile
	s, err := LoadSong(name)
	if errors.Is(err, fs.ErrNotExist) {
		name = legacyFile
		s, err = LoadSong(name)
	}
	if err == nil {
		err = g.eng.Load(s)
	}
	if err != nil {
		g.setStatus("load failed: " + err.Error())
		return
	}
	g.selectTrack(0)
	g.selectPattern(0)
	g.setStatus("loaded " + name)
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

	// Selected track settings.
	x = 10
	y = rowTrackCtl
	add(y, func() string { return fmt.Sprintf("TRACK %d", g.track+1) }, 60, nil)
	add(y, func() string { return "MODE " + g.snapTrack().Mode.String() }, 80, func() {
		g.editTrack(func(t *synth.Track) {
			if t.Mode == synth.ModeByte {
				t.Mode = synth.ModeFloat
			} else {
				t.Mode = synth.ModeByte
			}
		})
	})
	x += 8
	add(y, text("-"), 20, func() { g.editTrack(func(t *synth.Track) { t.Rate = stepRate(t.Rate, -1) }) })
	add(y, func() string { return fmt.Sprintf("RATE %d", g.snapTrack().Rate) }, 82, nil)
	add(y, text("+"), 20, func() { g.editTrack(func(t *synth.Track) { t.Rate = stepRate(t.Rate, 1) }) })
	x += 8
	add(y, text("-"), 20, func() { g.editTrack(func(t *synth.Track) { t.Volume -= 0.05 }) })
	add(y, func() string { return fmt.Sprintf("VOL %.0f%%", g.snapTrack().Volume*100) }, 66, nil)
	add(y, text("+"), 20, func() { g.editTrack(func(t *synth.Track) { t.Volume += 0.05 }) })
	x += 8
	add(y, text("<"), 20, func() { g.loadPreset(g.preset - 1) })
	add(y, func() string {
		if g.preset < 0 {
			return "FORMULA PRESET"
		}
		return synth.Presets[g.preset].Name
	}, 130, nil)
	add(y, text(">"), 20, func() { g.loadPreset(g.preset + 1) })

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
	}
	add(y, text("NEW"), 0, func() { g.newPattern(false) })
	add(y, text("COPY"), 0, func() { g.newPattern(true) })
	x = Width - 10 - 2*94 + 4
	add(y, text("+ TO SONG"), 90, g.appendToSong)
	add(y, text("- FROM SONG"), 90, g.removeLastFromSong)

	// Sequencer.
	x = seqR.x
	y = rowSeqCtl
	add(y, text("OCT -"), 0, func() { g.lowNote = max(g.lowNote-12, 0) })
	add(y, text("OCT +"), 0, func() { g.lowNote = min(g.lowNote+12, synth.MaxNote-seqRows+1) })
	add(y, text("CLEAR"), 0, g.clearSteps)
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
	g.focus = formulaR.contains(mx, my)
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
	g.blink++
	select {
	case s := <-g.statusCh:
		g.exporting = false
		g.setStatus(s)
	default:
	}
	mx, my := ebiten.CursorPosition()
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonLeft) {
		g.click(mx, my, false)
	}
	if inpututil.IsMouseButtonJustPressed(ebiten.MouseButtonRight) {
		g.click(mx, my, true)
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
func (g *Game) Draw(dst *ebiten.Image) {
	dst.Fill(colBG)
	g.snap = g.eng.Song()
	song := g.snap
	st := g.eng.Status()
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
		ebitenutil.DebugPrintAt(dst, label, b.r.x+(b.r.w-len(label)*charW)/2, b.r.y+3)
	}
	g.drawFormula(dst, st)
	g.drawScope(dst)
	g.drawWave(dst, song, st)
	g.drawOrder(dst, song, st)
	g.drawSequencer(dst, song, st)

	help := "vars: t time  p note phase (256/period)  f Hz  g gate  e envelope  n ticks since note  s step  r rate   w(x) drawn wave\n" +
		"ops: + - * / % ** & | ^ ~ << >> >>> < > == != && || !  a?b:c  [a,b,c][i]  sin cos abs floor min max pow exp random ...\n" +
		"SPACE play/stop  click formula to edit (Cmd/Ctrl+V paste)  draw in the wave panel  SONG strip: click = jump, right click = put pattern"
	ebitenutil.DebugPrintAt(dst, help, 10, Height-50)
}

func (g *Game) drawFormula(dst *ebiten.Image, st synth.Status) {
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

	y := formulaR.y + formulaR.h + 4
	switch {
	case g.formulaErr != "":
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
	lo, hi := columns(win, r.w)
	sy := func(v float32) float32 { return mid - v*float32(r.h)/2*0.95 }
	var prev float32
	for x := range r.w {
		fx := float32(r.x + x)
		vector.StrokeLine(dst, fx, sy(lo[x]), fx, sy(hi[x])+1, 1, colScope, false)
		c := (lo[x] + hi[x]) / 2
		if x > 0 {
			vector.StrokeLine(dst, fx-1, sy(prev), fx, sy(c), 1, colScope, true)
		}
		prev = c
	}
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
	for i := 1; i < expr.WaveSize; i++ {
		vector.StrokeLine(dst, px(i-1), py(w[i-1]), px(i), py(w[i]), 2, colWave, true)
	}
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

func fill(dst *ebiten.Image, r rect, c color.Color) {
	vector.FillRect(dst, float32(r.x), float32(r.y), float32(r.w), float32(r.h), c, false)
}
