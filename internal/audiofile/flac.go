package audiofile

import (
	"crypto/md5"
	"encoding/binary"
	"fmt"
	"io"
	"math/bits"
)

// FLAC encoder for 16-bit mono audio.
//
// Each block of samples is encoded with whichever of these is smallest:
//   - CONSTANT subframe (silence, e.g. sequencer rests),
//   - FIXED predictor of order 0..4 with partitioned Rice-coded residuals,
//   - VERBATIM (raw samples, the fallback for noise).
//
// Bytebeat output is piecewise-constant and periodic, so the fixed
// predictors usually compress it several times better than WAV.

const (
	flacBlockSize    = 4096
	flacBPS          = 16
	maxPartitionOrd  = 8
	maxRiceParam     = 14 // 4-bit parameter; 15 is the escape code
	flacFixedMaxOrd  = 4
	streamInfoLength = 34
)

// EncodeFLAC writes 16-bit mono samples as a FLAC stream.
func EncodeFLAC(w io.Writer, rate int, samples []int16) error {
	if rate <= 0 || rate >= 1<<20 {
		return fmt.Errorf("flac: unsupported sample rate %d", rate)
	}
	var frames [][]byte
	minFrame, maxFrame := 0, 0
	for i, start := 0, 0; start < len(samples); i, start = i+1, start+flacBlockSize {
		block := samples[start:min(start+flacBlockSize, len(samples))]
		f := encodeFrame(uint64(i), rate, block)
		if minFrame == 0 || len(f) < minFrame {
			minFrame = len(f)
		}
		maxFrame = max(maxFrame, len(f))
		frames = append(frames, f)
	}

	// MD5 of the raw samples as signed little-endian 16-bit, per the spec.
	h := md5.New()
	raw := make([]byte, 2*len(samples))
	for i, s := range samples {
		binary.LittleEndian.PutUint16(raw[2*i:], uint16(s))
	}
	h.Write(raw)

	blockSize := flacBlockSize
	if len(samples) < flacBlockSize {
		blockSize = max(len(samples), 16)
	}

	var bw bitWriter
	bw.bytes([]byte("fLaC"))
	bw.write(1, 1) // last metadata block
	bw.write(0, 7) // STREAMINFO
	bw.write(streamInfoLength, 24)
	bw.write(uint64(blockSize), 16) // min block size
	bw.write(uint64(blockSize), 16) // max block size
	bw.write(uint64(minFrame), 24)
	bw.write(uint64(maxFrame), 24)
	bw.write(uint64(rate), 20)
	bw.write(0, 3)         // channels - 1
	bw.write(flacBPS-1, 5) // bits per sample - 1
	bw.write(uint64(len(samples)), 36)
	bw.bytes(h.Sum(nil))

	if _, err := w.Write(bw.buf); err != nil {
		return err
	}
	for _, f := range frames {
		if _, err := w.Write(f); err != nil {
			return err
		}
	}
	return nil
}

// rateCodes are the frame-header codes for common sample rates.
var rateCodes = map[int]uint64{
	88200: 0b0001, 176400: 0b0010, 192000: 0b0011, 8000: 0b0100,
	16000: 0b0101, 22050: 0b0110, 24000: 0b0111, 32000: 0b1000,
	44100: 0b1001, 48000: 0b1010, 96000: 0b1011,
}

func encodeFrame(num uint64, rate int, block []int16) []byte {
	var bw bitWriter
	bw.write(0xFFF8, 16) // sync code, reserved bit, fixed block size
	if len(block) == flacBlockSize {
		bw.write(0b1100, 4) // 256 * 2^(12-8) = 4096
	} else {
		bw.write(0b0111, 4) // 16-bit (size-1) follows the header
	}
	// The sample rate is always written explicitly: code 0 ("see
	// STREAMINFO") is valid FLAC, but Apple's decoder (QuickTime, Finder,
	// afconvert) then decodes nothing.
	var extra func()
	switch code, ok := rateCodes[rate]; {
	case ok:
		bw.write(code, 4)
	case rate%1000 == 0 && rate/1000 < 256:
		bw.write(0b1100, 4) // 8-bit rate in kHz follows
		extra = func() { bw.write(uint64(rate/1000), 8) }
	case rate < 1<<16:
		bw.write(0b1101, 4) // 16-bit rate in Hz follows
		extra = func() { bw.write(uint64(rate), 16) }
	case rate%10 == 0 && rate/10 < 1<<16:
		bw.write(0b1110, 4) // 16-bit rate in tens of Hz follows
		extra = func() { bw.write(uint64(rate/10), 16) }
	default:
		bw.write(0, 4) // only STREAMINFO can express this rate
	}
	bw.write(0, 4)     // channel assignment: mono
	bw.write(0b100, 3) // 16 bits per sample
	bw.write(0, 1)     // reserved
	bw.bytes(utf8Number(num))
	if len(block) != flacBlockSize {
		bw.write(uint64(len(block)-1), 16)
	}
	if extra != nil {
		extra()
	}
	bw.write(uint64(crc8(bw.buf)), 8)

	writeSubframe(&bw, block)

	bw.align()
	bw.write(uint64(crc16(bw.buf)), 16)
	return bw.buf
}

func writeSubframe(bw *bitWriter, block []int16) {
	n := len(block)
	constant := true
	for _, s := range block[1:] {
		if s != block[0] {
			constant = false
			break
		}
	}
	if constant {
		bw.write(0, 8) // pad bit, type CONSTANT, no wasted bits
		bw.write(uint64(uint16(block[0])), flacBPS)
		return
	}

	bestOrder, bestBits := -1, uint64(n*flacBPS) // verbatim cost
	var bestPart residualPlan
	res := make([]uint64, n)
	for order := 0; order <= min(flacFixedMaxOrd, n-1); order++ {
		fixedResidual(block, order, res)
		plan := planResidual(res[order:], n, order)
		cost := uint64(order*flacBPS) + plan.bits
		if cost < bestBits {
			bestOrder, bestBits, bestPart = order, cost, plan
		}
	}

	if bestOrder < 0 {
		bw.write(0b00000010, 8) // VERBATIM
		for _, s := range block {
			bw.write(uint64(uint16(s)), flacBPS)
		}
		return
	}

	bw.write(uint64(0b00010000|bestOrder<<1), 8) // FIXED, order in low bits
	for _, s := range block[:bestOrder] {
		bw.write(uint64(uint16(s)), flacBPS)
	}
	fixedResidual(block, bestOrder, res)
	u := res[bestOrder:]
	bw.write(0, 2) // RICE coding, 4-bit parameters
	bw.write(uint64(bestPart.order), 4)
	parts := 1 << bestPart.order
	pos := 0
	for p := range parts {
		cnt := n >> bestPart.order
		if p == 0 {
			cnt -= bestOrder
		}
		k := uint(bestPart.params[p])
		bw.write(uint64(k), 4)
		for _, v := range u[pos : pos+cnt] {
			bw.zeros(v >> k)
			bw.write(1, 1)
			if k > 0 {
				bw.write(v&(1<<k-1), k)
			}
		}
		pos += cnt
	}
}

// fixedResidual computes zigzag-encoded residuals of the FLAC fixed
// predictor of the given order into res[order:].
func fixedResidual(x []int16, order int, res []uint64) {
	for i := order; i < len(x); i++ {
		a := int64(x[i])
		var r int64
		switch order {
		case 0:
			r = a
		case 1:
			r = a - int64(x[i-1])
		case 2:
			r = a - 2*int64(x[i-1]) + int64(x[i-2])
		case 3:
			r = a - 3*int64(x[i-1]) + 3*int64(x[i-2]) - int64(x[i-3])
		case 4:
			r = a - 4*int64(x[i-1]) + 6*int64(x[i-2]) - 4*int64(x[i-3]) + int64(x[i-4])
		}
		res[i] = uint64(r<<1) ^ uint64(r>>63)
	}
}

type residualPlan struct {
	order  int
	params []int
	bits   uint64
}

// planResidual chooses the partition order and Rice parameters that
// minimise the coded size of the residuals u (length n-predOrder).
func planResidual(u []uint64, n, predOrder int) residualPlan {
	best := residualPlan{bits: ^uint64(0)}
	for po := 0; po <= maxPartitionOrd; po++ {
		parts := 1 << po
		if n%parts != 0 || n>>po <= predOrder {
			break
		}
		plan := residualPlan{order: po, params: make([]int, parts), bits: 2 + 4}
		pos := 0
		for p := range parts {
			cnt := n >> po
			if p == 0 {
				cnt -= predOrder
			}
			k, b := bestRice(u[pos : pos+cnt])
			plan.params[p] = k
			plan.bits += 4 + b
			pos += cnt
		}
		if plan.bits < best.bits {
			best = plan
		}
	}
	return best
}

// bestRice returns the Rice parameter with the smallest cost for u, and
// that cost in bits.
func bestRice(u []uint64) (int, uint64) {
	if len(u) == 0 {
		return 0, 0
	}
	var sum uint64
	for _, v := range u {
		sum += v
	}
	est := 0
	if mean := sum / uint64(len(u)); mean > 0 {
		est = bits.Len64(mean) - 1
	}
	// Clamp the search window so it is never empty, even when the residuals
	// are so large that the ideal parameter exceeds the 4-bit maximum.
	lo := min(max(est-1, 0), maxRiceParam)
	hi := min(est+1, maxRiceParam)
	bestK, bestBits := 0, ^uint64(0)
	for k := lo; k <= hi; k++ {
		b := uint64(len(u)) * uint64(k+1)
		for _, v := range u {
			b += v >> uint(k)
		}
		if b < bestBits {
			bestK, bestBits = k, b
		}
	}
	return bestK, bestBits
}

// utf8Number encodes a frame number with FLAC's extended UTF-8 scheme.
func utf8Number(v uint64) []byte {
	if v < 0x80 {
		return []byte{byte(v)}
	}
	n := 2
	for v >= 1<<(5*n+1) && n < 7 {
		n++
	}
	out := make([]byte, n)
	for i := n - 1; i > 0; i-- {
		out[i] = 0x80 | byte(v&0x3F)
		v >>= 6
	}
	out[0] = byte(0xFF<<(8-n)) | byte(v)
	return out
}

func crc8(b []byte) uint8 {
	var c uint8
	for _, x := range b {
		c ^= x
		for range 8 {
			if c&0x80 != 0 {
				c = c<<1 ^ 0x07
			} else {
				c <<= 1
			}
		}
	}
	return c
}

func crc16(b []byte) uint16 {
	var c uint16
	for _, x := range b {
		c ^= uint16(x) << 8
		for range 8 {
			if c&0x8000 != 0 {
				c = c<<1 ^ 0x8005
			} else {
				c <<= 1
			}
		}
	}
	return c
}

// bitWriter packs bits MSB first.
type bitWriter struct {
	buf []byte
	acc uint64
	n   uint // bits pending in acc, always < 8 between calls
}

func (b *bitWriter) write(v uint64, n uint) {
	b.acc = b.acc<<n | v&(1<<n-1)
	b.n += n
	for b.n >= 8 {
		b.n -= 8
		b.buf = append(b.buf, byte(b.acc>>b.n))
	}
	b.acc &= 1<<b.n - 1
}

func (b *bitWriter) zeros(n uint64) {
	for n > 32 {
		b.write(0, 32)
		n -= 32
	}
	b.write(0, uint(n))
}

func (b *bitWriter) align() {
	if b.n > 0 {
		b.write(0, 8-b.n)
	}
}

func (b *bitWriter) bytes(p []byte) {
	for _, x := range p {
		b.write(uint64(x), 8)
	}
}
