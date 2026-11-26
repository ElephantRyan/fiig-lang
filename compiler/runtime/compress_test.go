package runtime

import (
	"bytes"
	"encoding/hex"
	"math/rand/v2"
	"strings"
	"testing"

	"fiig-lang/compiler/types"
)

func TestEveryCompressFormulaIsImplemented(t *testing.T) {
	declared := map[string]bool{}
	for _, name := range types.CompressFormulaNames() {
		declared[name] = true
		if compressFormulas[name] == nil {
			t.Errorf("compress.%s is in compiler/types's table but has no body", name)
		}
	}
	for name := range compressFormulas {
		if !declared[name] {
			t.Errorf("compress.%s has a body but isn't in compiler/types's table", name)
		}
	}
}

var codecs = [][2]string{{"gzip", "gunzip"}, {"snappy", "unsnappy"}, {"lz4", "unlz4"}, {"zstd", "unzstd"}}

func callCompressFormula(t *testing.T, name string, args ...Value) Value {
	t.Helper()
	v, f := compressFormulas[name](args)
	if f != nil {
		t.Fatalf("compress.%s faulted: %s", name, f.Message)
	}
	return v
}

func decompressed(t *testing.T, v Value) []byte {
	t.Helper()
	r, ok := v.(Result)
	if !ok || !r.Ok {
		t.Fatalf("decompress returned %s, not Ok", v)
	}
	return r.Value.(Bytes).B
}

func decompressFailure(t *testing.T, v Value) string {
	t.Helper()
	r, ok := v.(Result)
	if !ok || r.Ok {
		t.Fatalf("decompress returned %s, not Err", v)
	}
	return r.Value.(EnumValue).Variant
}

// §2: each codec gets back what it was given, from empty to larger than
// one lz4 block, compressible and not.
func TestCompressRoundTrips(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	random := make([]byte, 200_000)
	for i := range random {
		random[i] = byte(rng.Uint32())
	}
	inputs := map[string][]byte{
		"empty":    {},
		"one byte": {7},
		"text":     []byte(strings.Repeat("hello hello hello ", 10_000)),
		"random":   random,
	}
	for _, c := range codecs {
		for name, in := range inputs {
			packed := callCompressFormula(t, c[0], Bytes{B: in}).(Bytes).B
			out := decompressed(t, callCompressFormula(t, c[1], Bytes{B: packed}, Int(len(in))))
			if !bytes.Equal(out, in) {
				t.Errorf("%s then %s of %s: got %d bytes back, want %d", c[0], c[1], name, len(out), len(in))
			}
		}
	}
	text := inputs["text"]
	for _, c := range codecs {
		if n := len(callCompressFormula(t, c[0], Bytes{B: text}).(Bytes).B); n > len(text)/10 {
			t.Errorf("%s left %d bytes of repetitive text as %d", c[0], len(text), n)
		}
	}
}

// §3: more than maxSize is TooLarge, without producing the rest.
func TestDecompressStopsAtMaxSize(t *testing.T) {
	in := bytes.Repeat([]byte{0}, 1<<20)
	for _, c := range codecs {
		packed := callCompressFormula(t, c[0], Bytes{B: in})
		if got := decompressFailure(t, callCompressFormula(t, c[1], packed, Int(len(in)-1))); got != "TooLarge" {
			t.Errorf("%s of %d bytes with maxSize %d: %s, want TooLarge", c[1], len(in), len(in)-1, got)
		}
		if out := decompressed(t, callCompressFormula(t, c[1], packed, Int(len(in)))); len(out) != len(in) {
			t.Errorf("%s with maxSize exactly the size: %d bytes", c[1], len(out))
		}
	}
}

// §3: bytes that aren't the format are Corrupt, not a fault; a negative
// maxSize is the program's mistake, so it faults.
func TestDecompressCorruptAndNegative(t *testing.T) {
	junk := Bytes{B: []byte("this is not compressed at all, in any format")}
	for _, c := range codecs {
		if got := decompressFailure(t, callCompressFormula(t, c[1], junk, Int(1000))); got != "Corrupt" {
			t.Errorf("%s of junk: %s, want Corrupt", c[1], got)
		}
		packed := callCompressFormula(t, c[0], Bytes{B: []byte("hello")}).(Bytes).B
		truncated := Bytes{B: packed[:len(packed)-2]}
		if got := decompressFailure(t, callCompressFormula(t, c[1], truncated, Int(1000))); got != "Corrupt" {
			t.Errorf("%s of a truncated stream: %s, want Corrupt", c[1], got)
		}
		if _, f := compressFormulas[c[1]]([]Value{Bytes{B: packed}, Int(-1)}); f == nil {
			t.Errorf("%s with maxSize -1 didn't fault", c[1])
		}
	}
}

// §2: lz4 writes the frame Java's Kafka producer writes: magic, FLG 0x60
// (version 1, independent blocks, no checksums), BD 0x40 (64 KiB blocks),
// and that descriptor's header checksum 0x82.
func TestLz4WritesKafkasFrame(t *testing.T) {
	packed := callCompressFormula(t, "lz4", Bytes{B: []byte("hello")}).(Bytes).B
	if want := "04224d18604082"; hex.EncodeToString(packed[:7]) != want {
		t.Errorf("lz4 frame starts %x, want %s", packed[:7], want)
	}
}

// §2: the record sections of two batches Java's Kafka producer wrote
// (kafka-console-producer, Kafka 4.0, --compression-codec snappy and lz4),
// read from the broker's log segment. Each holds two records whose value is
// "hello hello hello hello hello". Snappy's is xerial-framed: com.degeneratedevelopment.kafka
// unframes it, so this test takes its one chunk's block.
func TestDecompressJavaKafkaBatches(t *testing.T) {
	const (
		javaSnappy = "82534e415050590000000001000000010000001a482c46000000013a68656c6c6f205a0600100046002a027e2400"
		javaLz4    = "04224d186040821f000000cf46000000013a68656c6c6f200600045f004600200224000850656c6c6f0000000000"
	)
	value := []byte(strings.Repeat("hello ", 4) + "hello")
	check := func(name string, records []byte) {
		if len(records) != 72 || bytes.Count(records, value) != 2 || records[0] != 0x46 {
			t.Errorf("%s: got %x, want two 36-byte records holding %q", name, records, value)
		}
	}
	framed, _ := hex.DecodeString(javaSnappy)
	block := framed[16+4:] // xerial's 16-byte header, then the chunk's i32 length
	check("snappy", decompressed(t, callCompressFormula(t, "unsnappy", Bytes{B: block}, Int(1000))))
	lz, _ := hex.DecodeString(javaLz4)
	check("lz4", decompressed(t, callCompressFormula(t, "unlz4", Bytes{B: lz}, Int(1000))))
}

func FuzzDecompressNeverPanics(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte("hello"))
	for _, c := range codecs {
		v, _ := compressFormulas[c[0]]([]Value{Bytes{B: []byte("hello hello hello")}})
		f.Add(v.(Bytes).B)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		for _, c := range codecs {
			v, fault := compressFormulas[c[1]]([]Value{Bytes{B: data}, Int(1 << 16)})
			if fault != nil {
				t.Fatalf("%s faulted: %s", c[1], fault.Message)
			}
			if _, ok := v.(Result); !ok {
				t.Fatalf("%s returned %s", c[1], v)
			}
		}
	})
}
