package runtime

import (
	"context"
	cryptorand "crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"io"
	"math/rand/v2"
	"sync"
)

// RandomProvider is the Random primitive (ADR 0068): bytes, int, float, and
// uuid, all drawn from one byte source. NewRandomProvider's source is the
// operating system's secure generator (§1); NewSeededRandomProvider's is a
// seeded ChaCha8, for tests and demos only (§6).
type RandomProvider struct {
	mu     sync.Mutex
	source io.Reader
	// Seeded marks a provider whose draws repeat, which `fiig serve`
	// refuses (§6).
	Seeded bool
}

// NewRandomProvider draws from crypto/rand.
func NewRandomProvider() *RandomProvider {
	return &RandomProvider{source: cryptorand.Reader}
}

// NewSeededRandomProvider draws from ChaCha8 keyed by seed: the same seed
// gives the same draws within one toolchain version.
func NewSeededRandomProvider(seed uint64) *RandomProvider {
	var key [32]byte
	binary.LittleEndian.PutUint64(key[:], seed)
	return &RandomProvider{source: rand.NewChaCha8(key), Seeded: true}
}

// maxRandomBytes is Random.bytes's ceiling, Socket.read's (§2).
const maxRandomBytes = 65536

func (p *RandomProvider) Call(_ context.Context, method string, args []Value) (Value, *Fault) {
	switch method {
	case "bytes":
		count, ok := intArg(args, 0)
		if !ok || len(args) != 1 {
			return nil, Faultf("Random.bytes takes one int, the count")
		}
		if count < 0 || count > maxRandomBytes {
			return nil, Faultf("Random.bytes(count: %d) is outside 0 to %d", count, maxRandomBytes)
		}
		return Bytes{B: p.read(int(count))}, nil
	case "int":
		lo, ok1 := intArg(args, 0)
		hi, ok2 := intArg(args, 1)
		if !ok1 || !ok2 || len(args) != 2 {
			return nil, Faultf("Random.int takes two ints, min and max")
		}
		if hi < lo {
			return nil, Faultf("Random.int(min: %d, max: %d) has no value: max is less than min", lo, hi)
		}
		return Int(p.intBetween(int64(lo), int64(hi))), nil
	case "float":
		// 53 random bits, so every value is a multiple of 2⁻⁵³ below 1.0.
		return Float(float64(p.uint64()>>11) / (1 << 53)), nil
	case "uuid":
		return Str(formatUUID(p.read(16))), nil
	}
	return nil, Faultf("Random has no operation %q", method)
}

func intArg(args []Value, i int) (Int, bool) {
	if i >= len(args) {
		return 0, false
	}
	n, ok := args[i].(Int)
	return n, ok
}

// read draws n bytes. A failing operating-system generator is a fault in
// Go itself (crypto/rand.Read crashes rather than return an error, §2),
// and ChaCha8 never fails.
func (p *RandomProvider) read(n int) []byte {
	b := make([]byte, n)
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, err := io.ReadFull(p.source, b); err != nil {
		panic("Random: " + err.Error())
	}
	return b
}

func (p *RandomProvider) uint64() uint64 {
	return binary.LittleEndian.Uint64(p.read(8))
}

// intBetween is uniform over lo..hi, both included, with no modulo bias:
// a draw in the last, partial copy of the range is redrawn.
func (p *RandomProvider) intBetween(lo, hi int64) int64 {
	span := uint64(hi) - uint64(lo) // wraps correctly for any lo <= hi
	if span == ^uint64(0) {
		return int64(p.uint64())
	}
	n := span + 1
	rem := (^uint64(0)%n + 1) % n // 2⁶⁴ mod n
	for {
		x := p.uint64()
		if rem == 0 || x <= ^uint64(0)-rem {
			return lo + int64(x%n)
		}
	}
}

// formatUUID writes 16 random bytes as a version 4 UUID (RFC 9562 §5.4).
func formatUUID(b []byte) string {
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // variant 10
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
