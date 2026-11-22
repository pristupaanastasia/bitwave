package synth

import (
	"encoding/json"
	"math"
	"testing"
)

func TestSong(t *testing.T) {
	// A song with every kind of out-of-range value.
	broken := func() Song {
		s := DefaultSong()
		s.BPM, s.Volume = 1, 9
		s.Tracks[0].Rate, s.Tracks[0].Volume, s.Tracks[0].Mode = 1, -1, 7
		s.Tracks[0].Wave[0], s.Tracks[0].Wave[1] = math.NaN(), 999
		s.Patterns[0].Notes[0][0] = -5
		s.Patterns[0].Notes[0][1] = 200
		s.Patterns[1].Notes = s.Patterns[1].Notes[:1] // rows missing for 3 tracks
		s.Order = []int{1, 7, -1, 0}
		return s
	}
	fixed := sanitize(broken())
	big := Song{Patterns: make([]Pattern, MaxPatterns+3), Order: make([]int, MaxOrder+10)}
	for range MaxTracks + 3 {
		big.Tracks = append(big.Tracks, NewTrack("x"))
	}

	run(t, []testCase{
		{"sanitize/clamps song and track values", func(t *testing.T) {
			tr := fixed.Tracks[0]
			if fixed.BPM != MinBPM || fixed.Volume != 1 || tr.Rate != MinRate || tr.Volume != 0 || tr.Mode != ModeByte {
				t.Errorf("bpm %v vol %v rate %v tvol %v mode %v", fixed.BPM, fixed.Volume, tr.Rate, tr.Volume, tr.Mode)
			}
		}},
		{"sanitize/wave NaN and overflow", func(t *testing.T) {
			if w := fixed.Tracks[0].Wave; w[0] != 128 || w[1] != 255 {
				t.Error(w[:2])
			}
		}},
		{"sanitize/invalid notes become rests", func(t *testing.T) {
			if n := fixed.Patterns[0].Notes[0]; n[0] != Rest || n[1] != Rest {
				t.Error(n[:2])
			}
		}},
		{"sanitize/missing pattern rows are filled", func(t *testing.T) {
			if ns := fixed.Patterns[1].Notes; len(ns) != len(fixed.Tracks) || ns[3] != rests() {
				t.Errorf("%d rows", len(ns))
			}
		}},
		{"sanitize/order drops unknown patterns", func(t *testing.T) {
			if o := fixed.Order; len(o) != 2 || o[0] != 1 || o[1] != 0 {
				t.Error(o)
			}
		}},
		{"sanitize/does not modify its input", func(t *testing.T) {
			in := broken()
			sanitize(in)
			if in.Patterns[0].Notes[0][0] != -5 {
				t.Error("input mutated")
			}
		}},
		{"sanitize/empty song gets one of everything", func(t *testing.T) {
			got := sanitize(Song{})
			if len(got.Tracks) != 1 || len(got.Patterns) != 1 || len(got.Order) != 1 || len(got.Patterns[0].Notes) != 1 {
				t.Errorf("%+v", got)
			}
		}},
		{"sanitize/limits", func(t *testing.T) {
			got := sanitize(big)
			if len(got.Tracks) != MaxTracks || len(got.Patterns) != MaxPatterns || len(got.Order) != MaxOrder {
				t.Errorf("%d %d %d", len(got.Tracks), len(got.Patterns), len(got.Order))
			}
		}},
		{"clone is deep", func(t *testing.T) {
			s := DefaultSong()
			c := s.Clone()
			c.Tracks[0].Name = "changed"
			c.Patterns[0].Notes[0][0] = 99
			c.Order[0] = 1
			if s.Tracks[0].Name == "changed" || s.Patterns[0].Notes[0][0] == 99 || s.Order[0] == 1 {
				t.Error("clone shares memory")
			}
		}},
		{"parse/round trip", func(t *testing.T) {
			b, _ := json.Marshal(DefaultSong())
			s, err := ParseSong(b)
			if err != nil {
				t.Fatal(err)
			}
			if len(s.Tracks) != 4 || s.Tracks[0].Formula != DefaultSong().Tracks[0].Formula || len(s.Order) != len(DefaultSong().Order) {
				t.Errorf("%+v", s)
			}
		}},
		{"parse/legacy single-track patch", func(t *testing.T) {
			steps := rests()
			steps[3] = 64
			legacy, _ := json.Marshal(map[string]any{
				"formula": "t&t>>8", "mode": 1, "rate": 11025, "bpm": 140, "volume": 0.5, "steps": steps,
			})
			s, err := ParseSong(legacy)
			if err != nil {
				t.Fatal(err)
			}
			tr := s.Tracks[0]
			if len(s.Tracks) != 1 || tr.Formula != "t&t>>8" || tr.Mode != ModeFloat || tr.Rate != 11025 ||
				s.BPM != 140 || s.Volume != 0.5 || s.Patterns[0].Notes[0][3] != 64 {
				t.Errorf("%+v", s)
			}
		}},
	})

	for _, in := range []string{`not json`, `{"bpm":120}`, `{"tracks":[]}`, `{"tracks":"x"}`, `{"formula":1}`} {
		t.Run("parse/rejects "+in, func(t *testing.T) {
			if _, err := ParseSong([]byte(in)); err == nil {
				t.Error("succeeded")
			}
		})
	}
	for i, want := range map[int]string{0: "A", 1: "B", 15: "P"} {
		t.Run("pattern name/"+want, func(t *testing.T) {
			if got := PatternName(i); got != want {
				t.Errorf("got %q", got)
			}
		})
	}
}
