package runtime

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"testing"

	"fiig-lang/compiler/checker"
	"fiig-lang/compiler/parser"
	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0115 §2: the workloads the interpreter's speed is judged by. Run
// with
//
//	go test ./compiler/runtime -run '^$' -bench . -benchmem
//
// and compare against the numbers in ADR 0115's results section.

// benchInterp parses and checks srcs as one package and returns an
// interpreter for it.
func benchInterp(b *testing.B, srcs ...string) *Interp {
	b.Helper()
	var files []*syntax.File
	for _, s := range srcs {
		f, errs := parser.Parse(s)
		if len(errs) > 0 {
			b.Fatalf("parse: %v", errs)
		}
		files = append(files, f)
	}
	universe, conflicts := types.BuildUniverse(files)
	if len(conflicts) > 0 {
		b.Fatalf("name conflicts: %+v", conflicts)
	}
	if diags := checker.Check(files, nil); len(diags) > 0 {
		b.Fatalf("check: %v", diags)
	}
	return newTestInterp(universe, nil)
}

func benchCall(b *testing.B, in *Interp, name string, args map[string]Value) {
	b.Helper()
	for b.Loop() {
		if _, f := in.Call(context.Background(), name, args); f != nil {
			b.Fatalf("%s: %s", name, f.Message)
		}
	}
}

// bytes: one add and one modulo a byte, over 1 MiB.
func BenchmarkBytes(b *testing.B) {
	data := make([]byte, 1<<20)
	for i := range data {
		data[i] = byte(i)
	}
	in := benchInterp(b, `rule walk(data: bytes) -> int {
    sum: int = 0;
    for (b in data) {
        sum = (sum + b) % 65521;
    }
    return sum;
}`)
	b.SetBytes(int64(len(data)))
	benchCall(b, in, "walk", map[string]Value{"data": Bytes{B: data}})
}

// build: appending 10,000 elements to a list and 10,000 bytes to bytes.
func BenchmarkBuild(b *testing.B) {
	in := benchInterp(b, `rule build(n: int) -> int {
    list: List<int> = [];
    data: bytes = <<>>;
    i: int = 0;
    while (i < n) {
        list = append(list, i);
        data = <<data: bytes, i % 256: u8>>;
        i = i + 1;
    }
    return list.length() + data.length();
}`)
	benchCall(b, in, "build", map[string]Value{"n": Int(10_000)})
}

// records: building 10,000 models and reading their fields.
func BenchmarkRecords(b *testing.B) {
	in := benchInterp(b, `model Point {
    x: int
    y: int
}`, `rule records(n: int) -> int {
    total: int = 0;
    i: int = 0;
    while (i < n) {
        p = Point(x: i, y: i + 1);
        total = total + p.x * p.y % 7;
        i = i + 1;
    }
    return total;
}`)
	benchCall(b, in, "records", map[string]Value{"n": Int(10_000)})
}

// murmur2: fiig.kafka's partitioner (ADR 0113 §3), over 10,000 keys.
func BenchmarkMurmur2(b *testing.B) {
	src, err := os.ReadFile("../stdlib/kafka/src/Murmur2.fiig")
	if err != nil {
		b.Fatal(err)
	}
	in := benchInterp(b, string(src), `activity hashAll(keys: List<bytes>) -> int {
    total: int = 0;
    for (key in keys) {
        total = (total + murmur2(key)) % 1000000007;
    }
    return total;
}`)
	keys := make([]Value, 10_000)
	for i := range keys {
		keys[i] = Bytes{B: []byte(fmt.Sprintf("customer-%d", i))}
	}
	benchCall(b, in, "hashAll", map[string]Value{"keys": List{Elems: keys}})
}

// calls: an activity calling a conversion calling a private helper,
// 100,000 times.
func BenchmarkCalls(b *testing.B) {
	in := benchInterp(b, `conversion step(n: int) -> int {
    return twice(n) + 1;
}

private {
    twice(n: int) -> int {
        return n * 2;
    }
}`, `activity calls(n: int) -> int {
    total: int = 0;
    i: int = 0;
    while (i < n) {
        total = (total + step(i)) % 1000003;
        i = i + 1;
    }
    return total;
}`)
	benchCall(b, in, "calls", map[string]Value{"n": Int(100_000)})
}

// parse: decoding 10,000 Postgres DataRow messages of three columns with
// binary patterns, the way fiig.postgres reads a result.
func BenchmarkParse(b *testing.B) {
	var stream []byte
	for i := range 10_000 {
		cols := [][]byte{[]byte(fmt.Sprint(i)), []byte("customer name"), []byte("2026-11-20")}
		body := binary.BigEndian.AppendUint16(nil, uint16(len(cols)))
		for _, c := range cols {
			body = binary.BigEndian.AppendUint32(body, uint32(len(c)))
			body = append(body, c...)
		}
		stream = append(stream, 'D')
		stream = binary.BigEndian.AppendUint32(stream, uint32(len(body)+4))
		stream = append(stream, body...)
	}
	in := benchInterp(b, `rule rows(stream: bytes) -> int {
    rest: bytes = stream;
    width: int = 0;
    while (rest.length() > 0) {
        match (rest) {
            <<68: u8, length: i32be, body: bytes(length - 4), more: bytes>> -> {
                cols: bytes = body.slice(2, body.length());
                while (cols.length() > 0) {
                    match (cols) {
                        <<n: i32be, value: bytes(n), after: bytes>> -> {
                            width = width + value.length();
                            cols = after;
                        }
                        _ -> {
                            return 0 - 1;
                        }
                    }
                }
                rest = more;
            }
            _ -> {
                return 0 - 1;
            }
        }
    }
    return width;
}`)
	b.SetBytes(int64(len(stream)))
	benchCall(b, in, "rows", map[string]Value{"stream": Bytes{B: stream}})
}
