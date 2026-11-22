package audiofile

import (
	"bytes"
	"crypto/md5"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// signals covers the code paths of the encoder: silence (CONSTANT),
// smooth waves (high fixed orders), bytebeat steps, and noise (VERBATIM).
func signals() map[string][]int16 {
	n := 3*flacBlockSize + 1234 // includes a short last frame
	out := map[string][]int16{}
	mk := func(name string, f func(i int) float64) {
		s := make([]int16, n)
		for i := range s {
			s[i] = int16(max(min(f(i), 32767), -32768))
		}
		out[name] = s
	}
	mk("silence", func(int) float64 { return 0 })
	mk("sine", func(i int) float64 { return 30000 * math.Sin(float64(i)/20) })
	mk("bytebeat", func(i int) float64 { t := i / 5; return float64((t*(t>>5|t>>8))&255-128) * 256 })
	seed := uint32(1)
	mk("noise", func(int) float64 { seed = seed*1664525 + 1013904223; return float64(int16(seed >> 16)) })
	mk("extremes", func(i int) float64 {
		if i%2 == 0 {
			return 32767
		}
		return -32768
	})
	out["tiny"] = []int16{5}
	out["short"] = []int16{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	return out
}

func TestFLACRoundTrip(t *testing.T) {
	for name, in := range signals() {
		var b bytes.Buffer
		if err := EncodeFLAC(&b, 44100, in); err != nil {
			t.Fatal(err)
		}
		got, rate, err := decodeFLAC(b.Bytes())
		if err != nil {
			t.Fatalf("%s: decode: %v", name, err)
		}
		if rate != 44100 || len(got) != len(in) {
			t.Fatalf("%s: rate %d len %d, want 44100 %d", name, rate, len(got), len(in))
		}
		// First frame header: block size code 4096, explicit 44.1 kHz rate code.
		if len(in) >= flacBlockSize && b.Bytes()[44] != 0xC9 {
			t.Fatalf("%s: frame header byte %#x", name, b.Bytes()[44])
		}
		for i := range in {
			if got[i] != in[i] {
				t.Fatalf("%s: sample %d = %d, want %d", name, i, got[i], in[i])
			}
		}
		t.Logf("%-9s %6d bytes (wav would be %d)", name, b.Len(), 44+2*len(in))
	}
}

// TestFLACCompressesBytebeat uses a signal shaped like real engine output:
// an 8 kHz bytebeat held to 44.1 kHz at 60% volume.
// TestFLACRates covers every way a sample rate is written in frame headers.
func TestFLACRates(t *testing.T) {
	in := signals()["sine"]
	for _, rate := range []int{8000, 44100, 12000, 11025, 100000, 655350, 655351} {
		var b bytes.Buffer
		if err := EncodeFLAC(&b, rate, in); err != nil {
			t.Fatal(err)
		}
		got, r, err := decodeFLAC(b.Bytes())
		if err != nil || r != rate || len(got) != len(in) {
			t.Fatalf("rate %d: got %d, len %d, err %v", rate, r, len(got), err)
		}
	}
	if err := EncodeFLAC(&bytes.Buffer{}, 0, in); err == nil {
		t.Fatal("want error for rate 0")
	}
}

func TestFLACCompressesBytebeat(t *testing.T) {
	for _, formula := range []func(t int) int{
		func(t int) int { return t & (t >> 8) },
		func(t int) int { return t * (42 & (t >> 10)) },
	} {
		in := make([]int16, 44100*10)
		for i := range in {
			v := formula(i*8000/44100) & 255
			in[i] = int16(float64(v-128) / 128 * 0.6 * 32767)
		}
		var b bytes.Buffer
		EncodeFLAC(&b, 44100, in)
		ratio := float64(2*len(in)) / float64(b.Len())
		t.Logf("compression %.1fx", ratio)
		// Sample-and-hold steps leave sparse large residuals, which Rice coding
		// handles only moderately well; ~1.6-2x is the honest result here.
		if ratio < 1.4 {
			t.Errorf("compression only %.1fx", ratio)
		}
	}
}

// TestFLACWithSystemDecoder cross-checks against Apple's decoder when present.
func TestFLACWithSystemDecoder(t *testing.T) {
	if _, err := exec.LookPath("afconvert"); err != nil {
		t.Skip("afconvert not available")
	}
	dir := t.TempDir()
	if !systemFLACWorks(t, dir) {
		t.Skip("afconvert cannot round-trip its own FLAC output on this machine; " +
			"the system decoder is unusable as a reference here")
	}
	for _, name := range []string{"sine", "bytebeat", "noise", "silence"} {
		in := signals()[name]
		src := filepath.Join(dir, name+".flac")
		dst := filepath.Join(dir, name+".wav")
		f, _ := os.Create(src)
		EncodeFLAC(f, 44100, in)
		f.Close()
		out, err := exec.Command("afconvert", "-f", "WAVE", "-d", "LEI16", src, dst).CombinedOutput()
		if err != nil {
			t.Fatalf("%s: afconvert: %v %s", name, err, out)
		}
		data, _ := os.ReadFile(dst)
		pcm := wavData(t, data)
		if len(pcm) != 2*len(in) {
			t.Fatalf("%s: decoded %d bytes, want %d", name, len(pcm), 2*len(in))
		}
		for j := range in {
			if v := int16(binary.LittleEndian.Uint16(pcm[2*j:])); v != in[j] {
				t.Fatalf("%s: sample %d = %d, want %d", name, j, v, in[j])
			}
		}
	}
}

// systemFLACWorks checks that afconvert can encode a WAV to FLAC with its own
// encoder and decode it back to the same length. Some macOS setups (e.g.
// sandboxes) produce header-only FLAC files, and then the decoder cannot
// judge our output either.
func systemFLACWorks(t *testing.T, dir string) bool {
	t.Helper()
	in := signals()["sine"]
	ref := filepath.Join(dir, "ref.wav")
	f, _ := os.Create(ref)
	EncodeWAV(f, 44100, in)
	f.Close()
	enc := filepath.Join(dir, "ref.flac")
	back := filepath.Join(dir, "ref_back.wav")
	if exec.Command("afconvert", "-f", "flac", "-d", "flac", ref, enc).Run() != nil ||
		exec.Command("afconvert", "-f", "WAVE", "-d", "LEI16", enc, back).Run() != nil {
		return false
	}
	data, err := os.ReadFile(back)
	if err != nil {
		return false
	}
	for i := 12; i+8 <= len(data); {
		size := int(binary.LittleEndian.Uint32(data[i+4:]))
		if string(data[i:i+4]) == "data" {
			return size == 2*len(in)
		}
		i += 8 + size + size%2
	}
	return false
}

// wavData walks the RIFF chunks and returns the "data" payload.
func wavData(t *testing.T, b []byte) []byte {
	t.Helper()
	for i := 12; i+8 <= len(b); {
		size := int(binary.LittleEndian.Uint32(b[i+4:]))
		if string(b[i:i+4]) == "data" {
			return b[i+8 : i+8+size]
		}
		i += 8 + size + size%2
	}
	t.Fatal("no data chunk")
	return nil
}

func TestWAV(t *testing.T) {
	var b bytes.Buffer
	if err := Encode(&b, "wav", 44100, []float32{0, 1, -1, float32(math.NaN()), 2}); err != nil {
		t.Fatal(err)
	}
	d := b.Bytes()
	if len(d) != 44+10 || string(d[0:4]) != "RIFF" || string(d[8:12]) != "WAVE" || string(d[36:40]) != "data" {
		t.Fatalf("bad header, len %d", len(d))
	}
	want := []int16{0, 32767, -32767, 0, 32767}
	for i, w := range want {
		if v := int16(binary.LittleEndian.Uint16(d[44+2*i:])); v != w {
			t.Errorf("sample %d = %d, want %d", i, v, w)
		}
	}
}

func TestWriteFile(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"a.wav", "b.FLAC"} {
		if err := WriteFile(filepath.Join(dir, name), 8000, []float32{0, .5}); err != nil {
			t.Fatal(err)
		}
	}
	if err := WriteFile(filepath.Join(dir, "c.mp3"), 8000, nil); err == nil {
		t.Fatal("want error for mp3")
	}
	if err := Encode(&bytes.Buffer{}, "ogg", 8000, nil); err == nil {
		t.Fatal("want error for ogg")
	}
	if FormatOf("x/Y.Flac") != "flac" {
		t.Fatal(FormatOf("x/Y.Flac"))
	}
}

func TestChecksums(t *testing.T) {
	if c := crc8([]byte("123456789")); c != 0xF4 {
		t.Errorf("crc8 = %#x", c)
	}
	if c := crc16([]byte("123456789")); c != 0xFEE8 {
		t.Errorf("crc16 = %#x", c)
	}
}

func TestUTF8Number(t *testing.T) {
	cases := map[uint64][]byte{
		0:     {0},
		0x7F:  {0x7F},
		0x80:  {0xC2, 0x80},
		0x7FF: {0xDF, 0xBF},
		0x800: {0xE0, 0xA0, 0x80},
	}
	for v, want := range cases {
		if got := utf8Number(v); !bytes.Equal(got, want) {
			t.Errorf("utf8Number(%#x) = %x, want %x", v, got, want)
		}
	}
	for _, v := range []uint64{1 << 16, 1 << 21, 1 << 26, 1<<36 - 1} {
		if got, _ := readUTF8(&bitReader{b: utf8Number(v)}); got != v {
			t.Errorf("round trip %#x = %#x", v, got)
		}
	}
}

// --- minimal FLAC decoder for the subset this package writes ---

type bitReader struct {
	b   []byte
	pos int // bit position
}

func (r *bitReader) read(n int) (uint64, error) {
	var v uint64
	for range n {
		if r.pos >= 8*len(r.b) {
			return 0, errors.New("unexpected end of data")
		}
		v = v<<1 | uint64(r.b[r.pos/8]>>(7-r.pos%8)&1)
		r.pos++
	}
	return v, nil
}

func (r *bitReader) unary() (uint64, error) {
	var q uint64
	for {
		b, err := r.read(1)
		if err != nil {
			return 0, err
		}
		if b == 1 {
			return q, nil
		}
		q++
	}
}

func readUTF8(r *bitReader) (uint64, error) {
	first, err := r.read(8)
	if err != nil {
		return 0, err
	}
	n := bitsLeadingOnes(byte(first))
	if n == 0 {
		return first, nil
	}
	v := first & (0xFF >> (n + 1))
	for range n - 1 {
		c, err := r.read(8)
		if err != nil {
			return 0, err
		}
		v = v<<6 | c&0x3F
	}
	return v, nil
}

func bitsLeadingOnes(b byte) int {
	n := 0
	for b&0x80 != 0 {
		n++
		b <<= 1
	}
	return n
}

func decodeFLAC(data []byte) ([]int16, int, error) {
	if string(data[:4]) != "fLaC" {
		return nil, 0, errors.New("no fLaC marker")
	}
	r := &bitReader{b: data, pos: 32}
	last, _ := r.read(1)
	typ, _ := r.read(7)
	length, _ := r.read(24)
	if last != 1 || typ != 0 || length != streamInfoLength {
		return nil, 0, errors.New("bad STREAMINFO header")
	}
	r.read(16 + 16 + 24 + 24)
	rate, _ := r.read(20)
	ch, _ := r.read(3)
	bps, _ := r.read(5)
	total, _ := r.read(36)
	if ch != 0 || bps != 15 {
		return nil, 0, errors.New("not 16-bit mono")
	}
	sum := data[r.pos/8 : r.pos/8+16]
	r.pos += 128

	var out []int16
	for frame := uint64(0); uint64(len(out)) < total; frame++ {
		start := r.pos / 8
		sync, _ := r.read(16)
		if sync != 0xFFF8 {
			return nil, 0, errors.New("bad sync")
		}
		bsCode, _ := r.read(4)
		rateCode, _ := r.read(4)
		r.read(4 + 3 + 1)
		num, _ := readUTF8(r)
		if num != frame {
			return nil, 0, errors.New("bad frame number")
		}
		n := 4096
		if bsCode == 0b0111 {
			v, _ := r.read(16)
			n = int(v) + 1
		}
		switch rateCode {
		case 0b1100:
			r.read(8)
		case 0b1101, 0b1110:
			r.read(16)
		}
		c8, _ := r.read(8)
		if uint8(c8) != crc8(data[start:r.pos/8-1]) {
			return nil, 0, errors.New("bad header crc")
		}
		block, err := decodeSubframe(r, n)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, block...)
		if r.pos%8 != 0 {
			r.pos += 8 - r.pos%8
		}
		c16, _ := r.read(16)
		if uint16(c16) != crc16(data[start:r.pos/8-2]) {
			return nil, 0, errors.New("bad frame crc")
		}
	}
	raw := make([]byte, 2*len(out))
	for i, s := range out {
		binary.LittleEndian.PutUint16(raw[2*i:], uint16(s))
	}
	if got := md5.Sum(raw); !bytes.Equal(got[:], sum) {
		return nil, 0, errors.New("md5 mismatch")
	}
	return out, int(rate), nil
}

func decodeSubframe(r *bitReader, n int) ([]int16, error) {
	hdr, _ := r.read(8)
	typ := hdr >> 1 & 0x3F
	sample := func() int64 { v, _ := r.read(16); return int64(int16(v)) }
	out := make([]int64, n)
	switch {
	case typ == 0:
		v := sample()
		for i := range out {
			out[i] = v
		}
	case typ == 1:
		for i := range out {
			out[i] = sample()
		}
	case typ>>3 == 1:
		order := int(typ & 7)
		for i := range order {
			out[i] = sample()
		}
		method, _ := r.read(2)
		if method != 0 {
			return nil, errors.New("unsupported residual method")
		}
		po, _ := r.read(4)
		i := order
		for p := range 1 << po {
			cnt := n >> po
			if p == 0 {
				cnt -= order
			}
			k, _ := r.read(4)
			for range cnt {
				q, err := r.unary()
				if err != nil {
					return nil, err
				}
				low, _ := r.read(int(k))
				u := q<<k | low
				res := int64(u>>1) ^ -int64(u&1)
				x := out
				switch order {
				case 0:
					x[i] = res
				case 1:
					x[i] = res + x[i-1]
				case 2:
					x[i] = res + 2*x[i-1] - x[i-2]
				case 3:
					x[i] = res + 3*x[i-1] - 3*x[i-2] + x[i-3]
				case 4:
					x[i] = res + 4*x[i-1] - 6*x[i-2] + 4*x[i-3] - x[i-4]
				}
				i++
			}
		}
	default:
		return nil, errors.New("unsupported subframe type")
	}
	s := make([]int16, n)
	for i, v := range out {
		s[i] = int16(v)
	}
	return s, nil
}
