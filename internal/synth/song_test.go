package synth

import (
	"encoding/json"
	"math"
	"testing"
)

func TestSanitize(t *testing.T) {
	s := DefaultSong()
	s.BPM, s.Volume = 1, 9
	s.Tracks[0].Rate, s.Tracks[0].Volume, s.Tracks[0].Mode = 1, -1, 7
	s.Tracks[0].Wave[0], s.Tracks[0].Wave[1] = math.NaN(), 999
	s.Patterns[0].Notes[0][0] = -5
	s.Patterns[0].Notes[0][1] = 200
	s.Patterns[1].Notes = s.Patterns[1].Notes[:1] // rows missing for 3 tracks
	s.Order = []int{1, 7, -1, 0}

	got := sanitize(s)
	tr := got.Tracks[0]
	if got.BPM != MinBPM || got.Volume != 1 || tr.Rate != MinRate || tr.Volume != 0 || tr.Mode != ModeByte {
		t.Fatalf("clamps: %+v", got)
	}
	if tr.Wave[0] != 128 || tr.Wave[1] != 255 {
		t.Fatal("wave", tr.Wave[:2])
	}
	if got.Patterns[0].Notes[0][0] != Rest || got.Patterns[0].Notes[0][1] != Rest {
		t.Fatal("notes", got.Patterns[0].Notes[0][:2])
	}
	if len(got.Patterns[1].Notes) != len(got.Tracks) || got.Patterns[1].Notes[3] != rests() {
		t.Fatal("missing rows not filled")
	}
	if len(got.Order) != 2 || got.Order[0] != 1 || got.Order[1] != 0 {
		t.Fatal("order", got.Order)
	}
	// The input must not be modified.
	if s.Patterns[0].Notes[0][0] != -5 {
		t.Fatal("sanitize mutated its input")
	}
}

func TestSanitizeEmptyAndOversized(t *testing.T) {
	got := sanitize(Song{})
	if len(got.Tracks) != 1 || len(got.Patterns) != 1 || len(got.Order) != 1 || len(got.Patterns[0].Notes) != 1 {
		t.Fatalf("empty: %+v", got)
	}
	big := Song{}
	for range MaxTracks + 3 {
		big.Tracks = append(big.Tracks, NewTrack("x"))
	}
	big.Patterns = make([]Pattern, MaxPatterns+3)
	big.Order = make([]int, MaxOrder+10)
	got = sanitize(big)
	if len(got.Tracks) != MaxTracks || len(got.Patterns) != MaxPatterns || len(got.Order) != MaxOrder {
		t.Fatalf("limits: %d %d %d", len(got.Tracks), len(got.Patterns), len(got.Order))
	}
}

func TestCloneIsDeep(t *testing.T) {
	s := DefaultSong()
	c := s.Clone()
	c.Tracks[0].Name = "changed"
	c.Patterns[0].Notes[0][0] = 99
	c.Order[0] = 1
	if s.Tracks[0].Name == "changed" || s.Patterns[0].Notes[0][0] == 99 || s.Order[0] == 1 {
		t.Fatal("clone shares memory")
	}
}

func TestParseSongRoundTrip(t *testing.T) {
	b, _ := json.Marshal(DefaultSong())
	s, err := ParseSong(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tracks) != 4 || s.Tracks[0].Formula != DefaultSong().Tracks[0].Formula || len(s.Order) != len(DefaultSong().Order) {
		t.Fatalf("%+v", s)
	}
}

func TestParseLegacyPatch(t *testing.T) {
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
		t.Fatalf("%+v", s)
	}
}

func TestParseSongErrors(t *testing.T) {
	for _, in := range []string{`not json`, `{"bpm":120}`, `{"tracks":[]}`, `{"tracks":"x"}`, `{"formula":1}`} {
		if _, err := ParseSong([]byte(in)); err == nil {
			t.Errorf("ParseSong(%s) succeeded", in)
		}
	}
}

func TestPatternName(t *testing.T) {
	if PatternName(0) != "A" || PatternName(15) != "P" {
		t.Fatal(PatternName(0), PatternName(15))
	}
}
