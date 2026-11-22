package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pristupaanastasia/bitwave/internal/synth"
)

func TestBuildSong(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	tests := []struct {
		name                string
		load, formula, mode string
		rate                int
		bpm                 float64
		wantErr             bool
		check               func(t *testing.T, s synth.Song)
	}{
		// The app starts silent: one track and no notes.
		{name: "no flags open an empty song", check: func(t *testing.T, s synth.Song) {
			if len(s.Tracks) != 1 || len(s.Order) != 1 {
				t.Fatalf("%d tracks %d slots", len(s.Tracks), len(s.Order))
			}
			e, _ := synth.New(44100, s)
			for _, v := range e.Render(44100) {
				if v != 0 {
					t.Fatal("the starting song is not silent")
				}
			}
		}},
		{name: "mode applies to the first track", mode: "byte", check: func(t *testing.T, s synth.Song) {
			if s.Tracks[0].Formula != synth.EmptySong().Tracks[0].Formula || s.Tracks[0].Mode != synth.ModeByte {
				t.Errorf("%+v", s.Tracks[0])
			}
		}},
		{name: "formula with mode, rate and bpm", formula: "t&t>>8", mode: "float", rate: 16000, bpm: 90,
			check: func(t *testing.T, s synth.Song) {
				tr := s.Tracks[0]
				if tr.Formula != "t&t>>8" || tr.Mode != synth.ModeFloat || tr.Rate != 16000 || s.BPM != 90 {
					t.Errorf("%+v bpm %v", tr, s.BPM)
				}
			}},
		// Regression: a bare -formula used to replace the demo's FLOAT kick
		// track, turning a bytebeat into a DC square wave.
		{name: "bare formula is a plain one-track bytebeat", formula: "t&255", check: func(t *testing.T, s synth.Song) {
			if len(s.Tracks) != 1 || s.Tracks[0].Mode != synth.ModeByte || s.Tracks[0].Rate != 8000 {
				t.Fatalf("%+v", s.Tracks)
			}
			e, _ := synth.New(8000, s)
			for i, v := range e.Render(300) {
				if want := (float32(i&255) - 128) / 128; v != want {
					t.Fatalf("sample %d = %v, want %v", i, v, want)
				}
			}
		}},
		{name: "unknown mode", mode: "loud", wantErr: true},
		{name: "missing song file", load: missing, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := buildSong(tt.load, tt.formula, tt.mode, tt.rate, tt.bpm)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.check != nil {
				tt.check(t, s)
			}
		})
	}
}

func TestRun(t *testing.T) {
	dir := t.TempDir()
	songFile := filepath.Join(dir, "song.json")
	b, _ := json.Marshal(synth.DefaultSong())
	if err := os.WriteFile(songFile, b, 0o644); err != nil {
		t.Fatal(err)
	}
	demo, _ := synth.New(44100, synth.DefaultSong())
	out := func(name string) string { return filepath.Join(dir, name) }

	tests := []struct {
		name     string
		args     []string
		file     string
		wantSize int64 // exact size, 0 = only check it is non-trivial
		wantErr  bool
	}{
		{"whole song from file", []string{"-load", songFile, "-o", out("song.wav")}, out("song.wav"), int64(44 + 2*demo.SongFrames()), false},
		{"bare formula renders 30s", []string{"-formula", "t&255", "-o", out("f.wav")}, out("f.wav"), 44 + 2*30*44100, false},
		{"flac with explicit length", []string{"-formula", "t*(42&t>>10)", "-seconds", "0.5", "-o", out("x.flac")}, out("x.flac"), 0, false},
		{"negative seconds", []string{"-o", out("e1.wav"), "-seconds", "-1"}, "", 0, true},
		{"bad formula", []string{"-o", out("e2.wav"), "-formula", "t>>"}, "", 0, true},
		{"unsupported format", []string{"-o", out("x.ogg"), "-seconds", "0.1"}, "", 0, true},
		{"unknown flag", []string{"-nope"}, "", 0, true},
		{"-help is not an error", []string{"-help"}, "", 0, false},
		{"unknown mode", []string{"-mode", "x"}, "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := run(tt.args)
			if (err != nil) != tt.wantErr {
				t.Fatalf("run(%v) err = %v, wantErr %v", tt.args, err, tt.wantErr)
			}
			if tt.file == "" {
				return
			}
			st, err := os.Stat(tt.file)
			switch {
			case err != nil:
				t.Fatal(err)
			case tt.wantSize != 0 && st.Size() != tt.wantSize:
				t.Errorf("size %d, want %d", st.Size(), tt.wantSize)
			case st.Size() < 100:
				t.Errorf("size %d is too small", st.Size())
			}
		})
	}
}
