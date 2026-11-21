package runtime

import (
	"encoding/binary"
	"testing"

	"fiig-lang/compiler/syntax"
)

// ADR 0115 §4: a whole-byte field on a byte boundary is written and read a
// byte at a time; the result is the same bits as encoding/binary gives, and
// as the bit-at-a-time path gives one bit off a boundary.
func TestAlignedFieldsMatchTheBitPath(t *testing.T) {
	values := []int64{0, 1, 127, -1, -128, 255, 0x1234, -0x1234, 0x7fffffff, -0x80000000, 0x0102030405060708}
	for _, bits := range []int{8, 16, 32, 64} {
		for _, little := range []bool{false, true} {
			for _, signed := range []bool{false, true} {
				enc := syntax.Encoding{Kind: syntax.SegInt, Bits: bits, Little: little, Signed: signed}
				for _, v := range values {
					if !intFits(v, enc) {
						continue
					}
					var aligned bitWriter
					aligned.writeInt(v, enc)
					want := make([]byte, 8)
					if little {
						binary.LittleEndian.PutUint64(want, uint64(v))
						want = want[:bits/8]
					} else {
						binary.BigEndian.PutUint64(want, uint64(v))
						want = want[8-bits/8:]
					}
					if string(aligned.buf) != string(want) {
						t.Fatalf("%d bits, little %v: wrote %x, want %x", bits, little, aligned.buf, want)
					}
					// One bit off a boundary takes the bit path.
					var shifted bitWriter
					shifted.writeBits(1, 1)
					shifted.writeInt(v, enc)
					shifted.writeBits(0, 7)
					r := bitReader{data: shifted.buf}
					r.readBits(1)
					if got := r.readInt(enc); got != v {
						t.Fatalf("%d bits, little %v: bit path read %d, want %d", bits, little, got, v)
					}
					r = bitReader{data: aligned.buf}
					if got := r.readInt(enc); got != v || r.remaining() != 0 {
						t.Fatalf("%d bits, little %v: aligned read %d, want %d", bits, little, got, v)
					}
				}
			}
		}
	}
}
