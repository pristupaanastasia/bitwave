# bitwave

An electronic music maker built on bytebeat. You get several tracks,
patterns arranged into a song, an oscilloscope, waves you draw with the mouse,
and export to WAV or FLAC. You can make music entirely with instruments,
knobs and the step sequencer. Writing formulas is optional.

## Getting started

Requires Go 1.27 or newer. On Linux, Ebitengine also needs
`libasound2-dev libgl1-mesa-dev xorg-dev`.

```sh
go run .                                         # open the editor
go build -o bitwave . && ./bitwave               # or build a binary first
```

The app opens your last song, or an empty one on the first run. To make a
beat:

1. Click the grid at the bottom to place notes on track `1 kick`.
2. Press `SPACE` to play or stop.
3. Use `+ TRACK` to add a track, then `INSTRUMENT < >` to pick its sound
   (hat, bass, lead, ...).
4. Drag the knobs and the `A D S R GATE` sliders to shape the sound.

Your song is saved automatically (see [Saving](#saving-and-export)).

## How a song is built

- **Tracks** (up to 8) each have an instrument, four knobs, an envelope,
  a volume and **M**ute / **S**olo buttons. While a track makes sound, a green
  bar flickers under its tab.
- **Patterns** A–P (up to 16) each hold 16 steps of notes for every track.
- **The song** (the `SONG` strip) is a chain of patterns, e.g. `A A B B A`.
  `EXPORT` renders this chain from start to end.

## Shaping sound without formulas

- **INSTRUMENT `< >`** picks a template: kick, snare, hat, bass, lead, pad,
  pluck or bytebeat. It sets up the whole sound of the track.
- **Knobs k1–k4** (the row under the track settings) are named for the
  instrument: kick has pitch / punch / length / drive, bass has square / crush
  / sub, and so on. Drag a knob with the mouse and you hear the change at once.
- **ADSR ON/OFF** and the **A D S R** sliders set attack, decay, sustain level
  and release. **GATE** sets how long a note is held, in steps (0.25–8).
- **Wave panel** (orange): draw a waveform with the mouse, or click
  SINE / SQUARE / SAW / TRIANGLE / NOISE. Instruments that play the drawn wave,
  such as bass and pluck, use it right away.

## The interface

- **Transport**: `PLAY`/`STOP`, `RESET` (back to the start), `PLAY SONG` ⇄
  `LOOP PATTERN` (loop the pattern you are editing), `BPM`, `MASTER` volume.
  `NEW SONG` starts over with an empty song. It asks for a second click,
  `CLEAR ALL?`, so one stray click cannot erase your work.
- **Track tabs**: click a name to select the track. `M` and `S` mute and solo
  it. `+ TRACK` and `- TRACK` add or remove tracks.
- **Pattern buttons A B …**: pick the pattern to edit. A pink underline marks
  the pattern playing right now. `NEW` adds an empty pattern, `COPY` duplicates
  the current one, `DELETE` removes it. Deleting also removes its slots from
  the song, and later letters move up (C becomes B).
- **SONG strip**:
  - Left click a slot to select it, jump there and open its pattern.
  - Right click a slot to put the current pattern into it.
  - `+ TO SONG` appends the current pattern.
  - `- FROM SONG` removes the selected slot, or the last slot if none is
    selected.
- **Sequencer grid**: shows the notes of the selected track in the selected
  pattern. Click to set or clear a note. `OCT ±` scrolls octaves, `CLEAR`
  empties the row.
- **Oscilloscope**: `VIEW WAVE` / `VIEW BITS` shows either the waveform or
  the 8 bits of the track's output byte, as bit planes. `SHOW MIX` /
  `SHOW TRACK` and `ZOOM ±` change what it shows and how much.

## Saving and export

- **Autosave**: the song is saved every 2 seconds after a change, and when
  the window closes. It goes to
  `~/Library/Application Support/bitwave/song.json` (macOS), or the matching
  user config directory on Linux and Windows. The app reopens it on start.
- **SAVE** opens the system "Save as" dialog, so you can keep copies under
  any name (`.json` is added if missing). **LOAD** opens a song file. Older
  single-track `bitwave-patch.json` files open too.
- **EXPORT WAV / EXPORT FLAC** asks where to save, then renders the whole
  song in the background while you keep playing.
  - WAV is uncompressed 16-bit mono PCM at 44.1 kHz.
  - FLAC is lossless and usually 1.5–2.5× smaller for bytebeat. It uses
    bitwave's own pure-Go encoder, checked bit-exact against Apple's decoder
    and `mewkiz/flac`.
  - Both open in QuickTime, VLC, browsers and any DAW.

From the command line:

```sh
./bitwave -load song.json -o song.flac        # render a saved song once
./bitwave -formula 't*(42&t>>10)' -o x.wav    # one formula, 30 s (or -seconds N)
./bitwave -help                               # all flags
```

## Formulas (optional)

The formula editor is hidden by default. **SHOW FORMULA** reveals it, along
with `MODE`, `RATE` and formula presets. Each track is a formula evaluated
`RATE` times per second. Every valid edit is heard immediately. If the
formula has an error, the last valid one keeps playing. `Cmd/Ctrl+V` pastes.

The semantics follow JavaScript, so formulas from bytebeat sites work as is.

| Variable | Meaning |
|---|---|
| `t` | time in samples (grows by `RATE` per second) |
| `p` | phase of the current note: +256 per period |
| `f` | note frequency in Hz (0 on a rest) |
| `g` | gate: 1 while a note sounds, 0 on a rest |
| `e` | envelope: the ADSR when on, otherwise 1 → 0 over each step |
| `n` | ticks since the current note started (handy for drums) |
| `s` | step number 0..15 |
| `r` | `RATE` |
| `k1`…`k4` | the track's knobs, 0..1 |
| `w(x)` | the drawn wave: 256 points, values 0..255 |

Operators: `+ - * / % ** & | ^ ~ << >> >>> < > <= >= == != && || ! a?b:c`.

Arrays: `[a,b,c][i]`. As in JS, an index out of range is `NaN`, which is 0
in bit operations, so mask it: `[a,b,c,d][t>>12&3]`.

Functions: `sin cos tan tanh abs floor ceil round sqrt pow min max log exp
random …`. A `Math.` prefix is allowed.

- **MODE BYTE**: classic bytebeat, the result `& 255` → 0..255.
- **MODE FLOAT**: the result is used directly, clamped to −1..1.

Examples:

- `128+(w(p)-128)*e`: the drawn wave played by the notes
- `t*(42&t>>10)` and `(t*(t>>5|t>>8))>>(t>>16)`: classic bytebeats
- `sin(p/128*PI)*e*.7` (FLOAT): a sine played by the notes

## Development

```sh
scripts/install-hooks.sh   # once after cloning
scripts/coverage.sh        # tests + the 70% coverage gate
go test -race ./...
```

- Tests are table-driven, one test per feature: cases in a slice, run with
  `for … range` and `t.Run`.
- The `pre-push` hook refuses pushes to `main` / `master` and pushes with
  coverage below 70%. CI (`.github/workflows/ci.yml`) runs the same check.
  `--no-verify` skips the hook, so enable branch protection for `main` on
  GitHub as well.
- The `.claude/agents/reviewer.md` subagent reviews code changes.

Layout:

| Package | What it does |
|---|---|
| `internal/expr` | formula lexer, parser and compiler to closures |
| `internal/synth` | song model, instruments, ADSR, audio engine |
| `internal/audiofile` | WAV and FLAC writers |
| `internal/ui` | the Ebitengine interface |
| `internal/dialog` | native Save/Open dialogs (osascript, zenity, PowerShell) |
| `internal/clipboard` | paste support without cgo |
