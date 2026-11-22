package synth

import "testing"

// BenchmarkRealtimeSecond renders one second of the demo song at 44.1 kHz:
// the audio thread's work per second of playback.
func BenchmarkRealtimeSecond(b *testing.B) {
	e, err := New(44100, DefaultSong())
	if err != nil {
		b.Fatal(err)
	}
	buf := make([]byte, 44100*8)
	for b.Loop() {
		e.Read(buf)
	}
}
