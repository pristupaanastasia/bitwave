package audiofile

import (
	"bufio"
	"encoding/binary"
	"io"
)

// EncodeWAV writes 16-bit mono samples as an uncompressed PCM WAV stream.
func EncodeWAV(w io.Writer, rate int, samples []int16) error {
	bw := bufio.NewWriter(w)
	dataLen := uint32(len(samples) * 2)
	hdr := []any{
		[4]byte{'R', 'I', 'F', 'F'}, 36 + dataLen, [4]byte{'W', 'A', 'V', 'E'},
		[4]byte{'f', 'm', 't', ' '}, uint32(16),
		uint16(1),        // format tag: PCM
		uint16(1),        // channels
		uint32(rate),     // sample rate
		uint32(rate * 2), // byte rate
		uint16(2),        // block align
		uint16(16),       // bits per sample
		[4]byte{'d', 'a', 't', 'a'}, dataLen,
	}
	for _, v := range hdr {
		if err := binary.Write(bw, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	if err := binary.Write(bw, binary.LittleEndian, samples); err != nil {
		return err
	}
	return bw.Flush()
}
