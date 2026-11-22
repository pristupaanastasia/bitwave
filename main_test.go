package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pristupaanastasia/bitwave/internal/synth"
)

func TestBuildSong(t *testing.T) {
	s, err := buildSong("", "t&t>>8", "byte", 16000, 90)
	if err != nil {
		t.Fatal(err)
	}
	tr := s.Tracks[0]
	if tr.Formula != "t&t>>8" || tr.Mode != synth.ModeByte || tr.Rate != 16000 || s.BPM != 90 {
		t.Fatalf("%+v", tr)
	}
	if s, _ := buildSong("", "", "float", 0, 0); s.Tracks[0].Formula != synth.DefaultSong().Tracks[0].Formula || s.Tracks[0].Mode != synth.ModeFloat {
		t.Fatal("defaults changed")
	}
	if s, _ := buildSong("", "", "", 0, 0); len(s.Tracks) != len(synth.DefaultSong().Tracks) {
		t.Fatal("no flags should open the demo song")
	}
}

// Regression: a bare -formula used to replace the demo's FLOAT kick track,
// turning a bytebeat into a DC square wave mixed with the other tracks.
func TestBareFormulaIsPlainBytebeat(t *testing.T) {
	s, err := buildSong("", "t&255", "", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Tracks) != 1 || s.Tracks[0].Mode != synth.ModeByte || s.Tracks[0].Rate != 8000 {
		t.Fatalf("%+v", s.Tracks)
	}
	e, _ := synth.New(8000, s)
	for i, v := range e.Render(300) {
		if want := (float32(i&255) - 128) / 128; v != want {
			t.Fatalf("sample %d = %v, want %v", i, v, want)
		}
	}
	out := filepath.Join(t.TempDir(), "f.wav")
	if err := run([]string{"-formula", "t&255", "-o", out}); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(out); st.Size() != 44+2*30*44100 {
		t.Fatalf("bare formula render is %d bytes, want 30s", st.Size())
	}
	if _, err := buildSong("", "", "loud", 0, 0); err == nil {
		t.Fatal("want mode error")
	}
	if _, err := buildSong(filepath.Join(t.TempDir(), "missing.json"), "", "", 0, 0); err == nil {
		t.Fatal("want load error")
	}
}

func TestRenderWholeSong(t *testing.T) {
	dir := t.TempDir()
	song := filepath.Join(dir, "song.json")
	b, _ := json.Marshal(synth.DefaultSong())
	os.WriteFile(song, b, 0o644)
	out := filepath.Join(dir, "song.wav")
	if err := run([]string{"-load", song, "-o", out}); err != nil {
		t.Fatal(err)
	}
	st, _ := os.Stat(out)
	e, _ := synth.New(44100, synth.DefaultSong())
	if want := int64(44 + 2*e.SongFrames()); st.Size() != want {
		t.Fatalf("wav size %d, want %d (whole song)", st.Size(), want)
	}
}

func TestRunRender(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "x.flac")
	if err := run([]string{"-formula", "t*(42&t>>10)", "-seconds", "0.5", "-o", out}); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(out); err != nil || st.Size() < 100 {
		t.Fatal(st, err)
	}
	for _, args := range [][]string{
		{"-o", out, "-seconds", "-1"},
		{"-o", out, "-formula", "t>>"},
		{"-o", filepath.Join(dir, "x.ogg"), "-seconds", "0.1"},
		{"-nope"},
		{"-mode", "x"},
	} {
		if err := run(args); err == nil {
			t.Errorf("run(%v) succeeded, want error", args)
		}
	}
}
