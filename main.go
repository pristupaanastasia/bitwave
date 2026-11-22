// Command bitwave is a bytebeat synthesizer: write formulas with bitwise
// operations, draw a waveform, sequence notes, watch the oscilloscope and
// export to WAV or FLAC.
//
//	bitwave                          open the editor
//	bitwave -load song.json          open a saved song
//	bitwave -load song.json -o x.flac render the whole song without a window
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/hajimehoshi/ebiten/v2"

	"github.com/pristupaanastasia/bitwave/internal/audiofile"
	"github.com/pristupaanastasia/bitwave/internal/synth"
	"github.com/pristupaanastasia/bitwave/internal/ui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "bitwave:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("bitwave", flag.ContinueOnError)
	load := fs.String("load", "", "song JSON file to open (as written by SAVE)")
	formula := fs.String("formula", "", "formula for track 1, e.g. 't*(42&t>>10)'")
	mode := fs.String("mode", "", "output mode of track 1: byte or float")
	rate := fs.Int("rate", 0, "formula sample rate of track 1 in Hz (t per second)")
	bpm := fs.Float64("bpm", 0, "tempo")
	out := fs.String("o", "", "render to this .wav or .flac file instead of opening the window")
	seconds := fs.Float64("seconds", 0, "length to render with -o; 0 renders the whole song once (30s for a bare -formula)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	song, err := buildSong(*load, *formula, *mode, *rate, *bpm)
	if err != nil {
		return err
	}
	if *out != "" {
		if *seconds == 0 && *formula != "" && *load == "" {
			*seconds = 30 // a bare formula has no arrangement to measure
		}
		return render(song, *out, *seconds)
	}

	eng, err := synth.New(ui.SampleRate, song)
	if err != nil {
		return err
	}
	game, err := ui.New(eng)
	if err != nil {
		return err
	}
	ebiten.SetWindowTitle("bitwave")
	ebiten.SetWindowSize(ui.Width, ui.Height)
	ebiten.SetWindowResizingMode(ebiten.WindowResizingModeEnabled)
	return ebiten.RunGame(game)
}

// buildSong returns the song to open or render. A -formula without -load
// starts a fresh one-track bytebeat song (BYTE, 8000 Hz) instead of
// replacing the demo's kick drum.
func buildSong(load, formula, mode string, rate int, bpm float64) (synth.Song, error) {
	song := synth.DefaultSong()
	switch {
	case load != "":
		var err error
		if song, err = ui.LoadSong(load); err != nil {
			return song, err
		}
	case formula != "":
		t := synth.NewTrack("track 1")
		t.Volume = 1
		song = synth.Song{BPM: song.BPM, Volume: 1, Tracks: []synth.Track{t},
			Patterns: []synth.Pattern{{}}, Order: []int{0}}
	}
	t := &song.Tracks[0]
	if formula != "" {
		t.Formula = formula
	}
	switch mode {
	case "":
	case "byte":
		t.Mode = synth.ModeByte
	case "float":
		t.Mode = synth.ModeFloat
	default:
		return song, fmt.Errorf("unknown mode %q (use byte or float)", mode)
	}
	if rate != 0 {
		t.Rate = rate
	}
	if bpm != 0 {
		song.BPM = bpm
	}
	return song, nil
}

func render(song synth.Song, path string, seconds float64) error {
	if seconds < 0 || seconds > 3600 {
		return fmt.Errorf("-seconds must be in [0, 3600]")
	}
	eng, err := synth.New(ui.SampleRate, song)
	if err != nil {
		return err
	}
	var samples []float32
	if seconds == 0 {
		samples = eng.RenderSong()
	} else {
		samples = eng.Render(int(seconds * ui.SampleRate))
	}
	if err := audiofile.WriteFile(path, ui.SampleRate, samples); err != nil {
		return err
	}
	fmt.Printf("wrote %.1fs to %s\n", float64(len(samples))/ui.SampleRate, path)
	return nil
}
