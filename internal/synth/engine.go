// Package synth turns a song (tracks of bytebeat formulas, drawn wavetables
// and patterns of notes arranged in order) into an audio stream.
package synth

import (
	"encoding/binary"
	"fmt"
	"math"
	"strconv"
	"sync"

	"github.com/pristupaanastasia/bitwave/internal/expr"
)

// Mode selects how a formula result becomes a sample.
type Mode int

const (
	// ModeByte is classic bytebeat: the result is truncated to 0..255.
	ModeByte Mode = iota
	// ModeFloat is "floatbeat": the result is used directly, clamped to -1..1.
	ModeFloat
)

func (m Mode) String() string {
	if m == ModeFloat {
		return "FLOAT"
	}
	return "BYTE"
}

// PlayMode selects what the transport plays.
type PlayMode int

const (
	PlaySong    PlayMode = iota // follow the arrangement
	PlayPattern                 // loop one pattern, for editing
)

const (
	Steps     = 16
	Rest      = -1
	ScopeSize = 1 << 15
	// ScopeMix selects the master mix in Scope; 0..MaxTracks-1 select a
	// track's own output.
	ScopeMix = -1

	MinRate = 1000
	MaxRate = 48000
	MinBPM  = 30
	MaxBPM  = 300
	MinNote = 0
	MaxNote = 127
)

// voice is the playback state of one track.
type voice struct {
	id   int // Track.ID this voice belongs to
	src  string
	prog *expr.Program
	env  expr.Env
	tick int64 // formula time t
	age  int64 // ticks since the current note started (n)
	acc  int   // resampling accumulator
	note int
	cur  float32 // current sample, held between formula ticks
}

func (v *voice) reset(outRate int) {
	v.tick, v.age, v.acc, v.note = 0, 0, outRate, Rest
	v.env.P = 0
	v.env.Seed(1)
}

// Engine generates audio. All methods are safe for concurrent use: the
// audio driver calls Read from its own goroutine while the UI edits.
type Engine struct {
	mu      sync.Mutex
	outRate int
	song    Song
	voices  []voice

	mode    PlayMode
	loopPat int
	// stepPos is the position in steps, kept within the song (or within the
	// pattern in PlayPattern), so edits to the order never jump elsewhere.
	stepPos float64
	lastAbs int64 // last step index that triggered notes
	clock   int64 // output samples since Reset, for aligning new voices
	nextID  int

	scope    [MaxTracks + 1][ScopeSize]float32 // [0] mix, [1+i] track i
	scopePos int
}

// New creates an engine rendering at outRate Hz. It fails if any track's
// formula does not compile.
func New(outRate int, s Song) (*Engine, error) {
	e := &Engine{outRate: outRate}
	if err := e.Load(s); err != nil {
		return nil, err
	}
	return e, nil
}

func compileAll(s Song) ([]*expr.Program, error) {
	progs := make([]*expr.Program, len(s.Tracks))
	for i, t := range s.Tracks {
		p, err := expr.Compile(t.Formula)
		if err != nil {
			return nil, fmt.Errorf("track %d (%s): %w", i+1, t.Name, err)
		}
		progs[i] = p
	}
	return progs, nil
}

// Load replaces the whole song and rewinds. On error the old song is kept.
func (e *Engine) Load(s Song) error {
	s = sanitize(s)
	progs, err := compileAll(s)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.song = s
	e.assignIDsLocked()
	e.voices = make([]voice, len(s.Tracks))
	for i := range e.voices {
		v := &e.voices[i]
		v.id, v.src, v.prog = e.song.Tracks[i].ID, s.Tracks[i].Formula, progs[i]
	}
	e.loopPat = min(e.loopPat, len(s.Patterns)-1)
	e.resetLocked()
	return nil
}

// assignIDsLocked gives every track without a unique ID a fresh one.
func (e *Engine) assignIDsLocked() {
	seen := map[int]bool{}
	for i := range e.song.Tracks {
		t := &e.song.Tracks[i]
		if t.ID == 0 || seen[t.ID] {
			e.nextID++
			t.ID = e.nextID
		}
		seen[t.ID] = true
	}
}

// silence is the formula used when an edited formula does not compile.
func silence(m Mode) string {
	if m == ModeFloat {
		return "0"
	}
	return "128"
}

// Song returns a deep copy of the current song.
func (e *Engine) Song() Song {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.song.Clone()
}

// SetFormula compiles and installs a formula for one track. On error the
// old formula keeps playing.
func (e *Engine) SetFormula(track int, src string) error {
	prog, err := expr.Compile(src)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if track < 0 || track >= len(e.song.Tracks) {
		return fmt.Errorf("no track %d", track+1)
	}
	e.song.Tracks[track].Formula = src
	e.voices[track].src, e.voices[track].prog = src, prog
	return nil
}

// EditSong applies fn to the song under the lock, then clamps values and
// re-syncs voices with tracks. Voices follow their track by ID, so adding or
// removing a track does not disturb the others. A formula changed by fn that
// does not compile is replaced by silence.
func (e *Engine) EditSong(fn func(s *Song)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	fn(&e.song)
	e.song = sanitize(e.song)
	e.assignIDsLocked()

	old := make(map[int]voice, len(e.voices))
	for _, v := range e.voices {
		old[v.id] = v
	}
	e.voices = make([]voice, len(e.song.Tracks))
	e.loopPat = min(e.loopPat, len(e.song.Patterns)-1)
	e.wrapLocked()
	abs := int64(e.stepPos)
	pat := &e.song.Patterns[e.patternAt(abs)]
	for i := range e.voices {
		t := &e.song.Tracks[i]
		v, ok := old[t.ID]
		if !ok {
			// A new track: align its t with the song clock and let it join
			// the note of the current step instead of waiting for the next.
			v.reset(e.outRate)
			v.id = t.ID
			v.tick = e.clock * int64(t.Rate) / int64(e.outRate)
			v.note = pat.Notes[i][abs%Steps]
		}
		if v.prog == nil || v.src != t.Formula {
			prog, err := expr.Compile(t.Formula)
			if err != nil {
				t.Formula = silence(t.Mode)
				prog, _ = expr.Compile(t.Formula)
			}
			v.src, v.prog = t.Formula, prog
		}
		e.voices[i] = v
	}
}

// SetPlayMode switches between following the arrangement and looping one
// pattern. The position inside the bar is kept, so the switch is seamless.
func (e *Engine) SetPlayMode(m PlayMode, pattern int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.mode = m
	e.loopPat = min(max(pattern, 0), len(e.song.Patterns)-1)
	e.wrapLocked()
}

// loopSteps is the length of what the transport is looping, in steps.
func (e *Engine) loopSteps() float64 {
	if e.mode == PlayPattern {
		return Steps
	}
	return float64(len(e.song.Order) * Steps)
}

// wrapLocked brings stepPos back inside the loop, e.g. after the order got
// shorter or the play mode changed.
func (e *Engine) wrapLocked() {
	if n := e.loopSteps(); e.stepPos >= n {
		e.stepPos = math.Mod(e.stepPos, n)
	}
}

// Seek jumps to the start of an arrangement slot.
func (e *Engine) Seek(slot int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	slot = min(max(slot, 0), len(e.song.Order)-1)
	e.stepPos = float64(slot * Steps)
	e.lastAbs = -1
	e.wrapLocked()
}

// Reset rewinds to the start of the song and t=0.
func (e *Engine) Reset() {
	e.mu.Lock()
	e.resetLocked()
	e.mu.Unlock()
}

func (e *Engine) resetLocked() {
	e.stepPos, e.lastAbs, e.clock = 0, -1, 0
	for i := range e.voices {
		e.voices[i].reset(e.outRate)
	}
}

// Status is a snapshot of the playback position for the UI.
type Status struct {
	Mode    PlayMode
	Step    int // 0..15 inside the pattern
	Slot    int // arrangement position
	Pattern int // pattern being played
	T       []int64
	Phase   []float64 // p mod 256 per track: the wave position being played
}

func (e *Engine) Status() Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	abs := max(e.lastAbs, 0)
	st := Status{
		Mode:  e.mode,
		Step:  int(abs % Steps),
		Slot:  min(int(abs/Steps), len(e.song.Order)-1),
		T:     make([]int64, len(e.voices)),
		Phase: make([]float64, len(e.voices)),
	}
	st.Pattern = e.patternAt(abs)
	for i := range e.voices {
		st.T[i] = e.voices[i].tick
		st.Phase[i] = math.Mod(e.voices[i].env.P, expr.WaveSize)
	}
	return st
}

// SongFrames is the length of one pass through the arrangement in output
// samples. It rounds down, so the last sample never starts the next pass.
func (e *Engine) SongFrames() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return int(float64(len(e.song.Order)*Steps) * e.samplesPerStep())
}

// RenderSong rewinds and renders the whole arrangement once.
func (e *Engine) RenderSong() []float32 {
	e.SetPlayMode(PlaySong, 0)
	e.Reset()
	return e.Render(e.SongFrames())
}

// Scope copies the most recent len(dst) samples of the mix (ScopeMix) or of
// one track (before its volume, -1..1) into dst, oldest first.
func (e *Engine) Scope(dst []float32, which int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	buf := &e.scope[min(max(which+1, 0), MaxTracks)]
	n := min(len(dst), ScopeSize)
	start := e.scopePos - n
	for i := range n {
		dst[i] = buf[(start+i+ScopeSize)%ScopeSize]
	}
}

// Read implements io.Reader producing interleaved stereo float32
// little-endian samples, the format expected by ebiten's NewPlayerF32.
func (e *Engine) Read(buf []byte) (int, error) {
	frames := len(buf) / 8
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range frames {
		bits := math.Float32bits(e.nextLocked())
		binary.LittleEndian.PutUint32(buf[i*8:], bits)
		binary.LittleEndian.PutUint32(buf[i*8+4:], bits)
	}
	return frames * 8, nil
}

// Render produces mono samples in -1..1 for offline export.
func (e *Engine) Render(frames int) []float32 {
	out := make([]float32, frames)
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range out {
		out[i] = e.nextLocked()
	}
	return out
}

func (e *Engine) samplesPerStep() float64 {
	return float64(e.outRate) * 60 / e.song.BPM / 4 // 16th notes
}

func (e *Engine) patternAt(abs int64) int {
	if e.mode == PlayPattern {
		return e.loopPat
	}
	return e.song.Order[min(int(abs/Steps), len(e.song.Order)-1)]
}

// nextLocked returns the next output sample of the mix.
func (e *Engine) nextLocked() float32 {
	s := &e.song
	abs := int64(e.stepPos)
	step := int(abs % Steps)
	pat := &s.Patterns[e.patternAt(abs)]
	if abs != e.lastAbs {
		e.lastAbs = abs
		for i := range e.voices {
			v := &e.voices[i]
			v.note = pat.Notes[i][step]
			if v.note != Rest {
				v.env.P, v.age = 0, 0 // retrigger
			}
		}
	}
	envelope := 1 - (e.stepPos - float64(abs))

	solo := false
	for _, t := range s.Tracks {
		solo = solo || t.Solo
	}
	var mix float32
	for i := range e.voices {
		v, t := &e.voices[i], &s.Tracks[i]
		for v.acc >= e.outRate {
			v.acc -= e.outRate
			v.cur = v.tickLocked(t, step, envelope)
		}
		v.acc += t.Rate
		e.scope[i+1][e.scopePos] = v.cur
		if !t.Mute && (!solo || t.Solo) {
			mix += v.cur * float32(t.Volume)
		}
	}
	mix = min(max(mix*float32(s.Volume), -1), 1)
	e.scope[0][e.scopePos] = mix
	e.scopePos = (e.scopePos + 1) % ScopeSize

	e.clock++
	e.stepPos += 1 / e.samplesPerStep()
	if n := e.loopSteps(); e.stepPos >= n {
		e.stepPos -= n
	}
	return mix
}

func (v *voice) tickLocked(t *Track, step int, envelope float64) float32 {
	rate := float64(t.Rate)
	env := &v.env
	env.T, env.S, env.R, env.N = float64(v.tick), float64(step), rate, float64(v.age)
	env.Wave = &t.Wave
	if v.note == Rest {
		env.F, env.G, env.E = 0, 0, 0
	} else {
		env.F, env.G, env.E = NoteHz(v.note), 1, envelope
	}

	x := v.prog.Eval(env)
	env.P += expr.WaveSize * env.F / rate
	v.tick++
	v.age++

	if t.Mode == ModeByte {
		return float32((float64(expr.ToInt32(x)&255) - 128) / 128)
	}
	if math.IsNaN(x) {
		return 0
	}
	return float32(min(max(x, -1), 1))
}

// NoteHz converts a MIDI note number to a frequency (A4 = 69 = 440 Hz).
func NoteHz(note int) float64 { return 440 * math.Pow(2, float64(note-69)/12) }

var noteNames = [...]string{"C", "C#", "D", "D#", "E", "F", "F#", "G", "G#", "A", "A#", "B"}

// NoteName returns e.g. "C4" for 60.
func NoteName(note int) string {
	if note < MinNote || note > MaxNote {
		return "--"
	}
	return noteNames[note%12] + strconv.Itoa(note/12-1)
}
