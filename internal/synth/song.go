package synth

import (
	"encoding/json"
	"fmt"

	"github.com/pristupaanastasia/bitwave/internal/expr"
)

const (
	MaxTracks   = 8
	MaxPatterns = 16 // named A..P
	MaxOrder    = 64
)

// Track is one instrument: a formula with its own wave, rate and level.
type Track struct {
	// ID identifies the track inside a running engine, so its voice follows
	// it when other tracks are added or removed. Assigned by the engine.
	ID      int                    `json:"-"`
	Name    string                 `json:"name"`
	Formula string                 `json:"formula"`
	Mode    Mode                   `json:"mode"`
	Rate    int                    `json:"rate"` // t increments per second
	Wave    [expr.WaveSize]float64 `json:"wave"` // 0..255
	Volume  float64                `json:"volume"`
	Mute    bool                   `json:"mute"`
	Solo    bool                   `json:"solo"`
}

// Pattern holds 16 steps of notes for every track: Notes[track][step].
type Pattern struct {
	Notes [][Steps]int `json:"notes"`
}

// Song is the complete, serialisable project.
type Song struct {
	BPM      float64   `json:"bpm"`
	Volume   float64   `json:"volume"` // master
	Tracks   []Track   `json:"tracks"`
	Patterns []Pattern `json:"patterns"`
	Order    []int     `json:"order"` // arrangement: pattern indices in play order
}

// PatternName returns "A".."P".
func PatternName(i int) string { return string(rune('A' + i)) }

func rests() [Steps]int {
	var s [Steps]int
	for i := range s {
		s[i] = Rest
	}
	return s
}

func notes(spec map[int]int) [Steps]int {
	s := rests()
	for step, n := range spec {
		s[step] = n
	}
	return s
}

// NewTrack returns a track that plays its drawn wave through the sequencer.
func NewTrack(name string) Track {
	return Track{
		Name:    name,
		Formula: "128+(w(p)-128)*e",
		Mode:    ModeByte,
		Rate:    8000,
		Wave:    Shape("saw"),
		Volume:  0.7,
	}
}

// DefaultSong is a small demo: drums, hats, bass and lead over two patterns.
func DefaultSong() Song {
	drums := Track{
		Name: "kick",
		// A sine whose pitch falls from ~200 Hz to 50 Hz with a fast decay.
		Formula: "g*sin(2*PI*(50*n/r+5*(1-exp(-n/r*30))))*exp(-n/r*9)",
		Mode:    ModeFloat, Rate: 44100, Wave: Shape("sine"), Volume: 0.9,
	}
	hats := Track{
		Name:    "hats",
		Formula: "128+(random()*255-128)*e**6*g",
		Mode:    ModeByte, Rate: 22050, Wave: Shape("noise"), Volume: 0.35,
	}
	bass := NewTrack("bass")
	bass.Formula = "128+(w(p)-128)*min(1,e*3)"
	bass.Volume = 0.6
	lead := NewTrack("lead")
	lead.Formula = "128+(w(p)-128)*e*e"
	lead.Wave = Shape("square")
	lead.Volume = 0.35

	kick := notes(map[int]int{0: 36, 4: 36, 8: 36, 12: 36})
	hat := notes(map[int]int{2: 60, 6: 60, 10: 60, 14: 60})
	bassA := notes(map[int]int{0: 36, 3: 36, 6: 43, 8: 39, 11: 39, 14: 41})
	leadB := notes(map[int]int{0: 72, 2: 75, 4: 79, 6: 77, 8: 75, 10: 72, 12: 70, 14: 72})

	return Song{
		BPM:    124,
		Volume: 0.6,
		Tracks: []Track{drums, hats, bass, lead},
		Patterns: []Pattern{
			{Notes: [][Steps]int{kick, rests(), bassA, rests()}},
			{Notes: [][Steps]int{kick, hat, bassA, leadB}},
		},
		Order: []int{0, 0, 1, 1, 1, 1, 0},
	}
}

// Clone returns a deep copy, so callers can read a song while the audio
// thread keeps using the original.
func (s Song) Clone() Song {
	c := s
	c.Tracks = append([]Track(nil), s.Tracks...)
	c.Patterns = make([]Pattern, len(s.Patterns))
	for i, p := range s.Patterns {
		c.Patterns[i].Notes = append([][Steps]int(nil), p.Notes...)
	}
	c.Order = append([]int(nil), s.Order...)
	return c
}

// sanitize clamps every value into range and restores the invariants the
// engine relies on: at least one track, pattern and order slot, and every
// pattern has a note row per track.
func sanitize(s Song) Song {
	s = s.Clone()
	s.BPM = min(max(s.BPM, MinBPM), MaxBPM)
	s.Volume = min(max(s.Volume, 0), 1)

	if len(s.Tracks) == 0 {
		s.Tracks = []Track{NewTrack("track 1")}
	}
	s.Tracks = s.Tracks[:min(len(s.Tracks), MaxTracks)]
	for i := range s.Tracks {
		t := &s.Tracks[i]
		t.Rate = min(max(t.Rate, MinRate), MaxRate)
		t.Volume = min(max(t.Volume, 0), 1)
		if t.Mode != ModeByte && t.Mode != ModeFloat {
			t.Mode = ModeByte
		}
		for j, v := range t.Wave {
			if v != v { // NaN
				t.Wave[j] = 128
			} else {
				t.Wave[j] = min(max(v, 0), 255)
			}
		}
	}

	if len(s.Patterns) == 0 {
		s.Patterns = []Pattern{{}}
	}
	s.Patterns = s.Patterns[:min(len(s.Patterns), MaxPatterns)]
	for i := range s.Patterns {
		ns := s.Patterns[i].Notes
		ns = ns[:min(len(ns), len(s.Tracks))]
		for len(ns) < len(s.Tracks) {
			ns = append(ns, rests())
		}
		for t := range ns {
			for k, n := range ns[t] {
				if n < MinNote || n > MaxNote {
					ns[t][k] = Rest // also covers garbage from hand-edited files
				}
			}
		}
		s.Patterns[i].Notes = ns
	}

	order := s.Order[:0:0]
	for _, p := range s.Order {
		if p >= 0 && p < len(s.Patterns) {
			order = append(order, p)
		}
	}
	if len(order) == 0 {
		order = []int{0}
	}
	s.Order = order[:min(len(order), MaxOrder)]
	return s
}

// legacyPatch is the single-track format written by earlier versions.
type legacyPatch struct {
	Formula string                 `json:"formula"`
	Mode    Mode                   `json:"mode"`
	Rate    int                    `json:"rate"`
	BPM     float64                `json:"bpm"`
	Steps   [Steps]int             `json:"steps"`
	Wave    [expr.WaveSize]float64 `json:"wave"`
	Volume  float64                `json:"volume"`
}

// ParseSong decodes a song file. Files from the single-track version are
// converted to a one-track song.
func ParseSong(data []byte) (Song, error) {
	var probe struct {
		Tracks  json.RawMessage `json:"tracks"`
		Formula *string         `json:"formula"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Song{}, err
	}
	if probe.Tracks == nil && probe.Formula != nil {
		var lp legacyPatch
		if err := json.Unmarshal(data, &lp); err != nil {
			return Song{}, err
		}
		t := NewTrack("track 1")
		t.Formula, t.Mode, t.Rate, t.Wave = lp.Formula, lp.Mode, lp.Rate, lp.Wave
		t.Volume = 1
		return sanitize(Song{
			BPM: lp.BPM, Volume: lp.Volume, Tracks: []Track{t},
			Patterns: []Pattern{{Notes: [][Steps]int{lp.Steps}}}, Order: []int{0},
		}), nil
	}
	var s Song
	if err := json.Unmarshal(data, &s); err != nil {
		return Song{}, err
	}
	if len(s.Tracks) == 0 {
		return Song{}, fmt.Errorf("song has no tracks")
	}
	return sanitize(s), nil
}
