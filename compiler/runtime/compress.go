package runtime

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"sync"

	"github.com/klauspost/compress/snappy"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"

	"fiig-lang/compiler/syntax"
	"fiig-lang/compiler/types"
)

// ADR 0116: the prelude compress group's bodies, by member, over argument
// values in parameter order. Each compresses into the format Kafka's own
// clients write (§2), and each decompress stops at maxSize bytes, so a
// small batch can't expand into gigabytes (§3).

type compressFunc func(args []Value) (Value, *Fault)

// zstdMaxWindow is the largest window a zstd frame may ask a decompress to
// hold (§3). Kafka's producers write 8 MiB or less.
const zstdMaxWindow = 64 << 20

var (
	zstdEncoder, _ = zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	zstdDecoders   = sync.Pool{New: func() any {
		d, _ := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxWindow(zstdMaxWindow))
		return d
	}}
	lz4Writers = sync.Pool{New: func() any {
		w := lz4.NewWriter(nil)
		// Kafka's frame (§2): 64 KiB independent blocks, no content
		// checksum, as Java's KafkaLZ4BlockOutputStream writes.
		_ = w.Apply(lz4.BlockSizeOption(lz4.Block64Kb), lz4.ChecksumOption(false), lz4.ConcurrencyOption(1))
		return w
	}}
	lz4Readers  = sync.Pool{New: func() any { return lz4.NewReader(nil) }}
	gzipWriters = sync.Pool{New: func() any { return gzip.NewWriter(nil) }}
)

var compressFormulas = map[string]compressFunc{
	"gzip": func(args []Value) (Value, *Fault) {
		var out bytes.Buffer
		w := gzipWriters.Get().(*gzip.Writer)
		defer gzipWriters.Put(w)
		w.Reset(&out)
		if _, err := w.Write(args[0].(Bytes).B); err != nil {
			return nil, Faultf("compress.gzip: %v", err)
		}
		if err := w.Close(); err != nil {
			return nil, Faultf("compress.gzip: %v", err)
		}
		return Bytes{B: out.Bytes()}, nil
	},
	"gunzip": func(args []Value) (Value, *Fault) {
		return decompress("gunzip", args, func(data []byte) (io.Reader, func(), error) {
			r, err := gzip.NewReader(bytes.NewReader(data))
			return r, func() {}, err
		})
	},
	"snappy": func(args []Value) (Value, *Fault) {
		return Bytes{B: snappy.Encode(nil, args[0].(Bytes).B)}, nil
	},
	"unsnappy": func(args []Value) (Value, *Fault) {
		data, maxSize := args[0].(Bytes).B, int(args[1].(Int))
		if maxSize < 0 {
			return nil, Faultf("compress.unsnappy's maxSize is %d; it can't be negative", maxSize)
		}
		n, err := snappy.DecodedLen(data)
		if err != nil {
			return decompressErr("Corrupt", Str(err.Error())), nil
		}
		if n > maxSize {
			return decompressErr("TooLarge", Int(maxSize)), nil
		}
		out, err := snappy.Decode(nil, data)
		if err != nil {
			return decompressErr("Corrupt", Str(err.Error())), nil
		}
		return Result{Ok: true, Value: Bytes{B: out}}, nil
	},
	"lz4": func(args []Value) (Value, *Fault) {
		var out bytes.Buffer
		w := lz4Writers.Get().(*lz4.Writer)
		defer lz4Writers.Put(w)
		w.Reset(&out)
		if _, err := w.Write(args[0].(Bytes).B); err != nil {
			return nil, Faultf("compress.lz4: %v", err)
		}
		if err := w.Close(); err != nil {
			return nil, Faultf("compress.lz4: %v", err)
		}
		return Bytes{B: out.Bytes()}, nil
	},
	"unlz4": func(args []Value) (Value, *Fault) {
		return decompress("unlz4", args, func(data []byte) (io.Reader, func(), error) {
			r := lz4Readers.Get().(*lz4.Reader)
			r.Reset(bytes.NewReader(data))
			return r, func() { lz4Readers.Put(r) }, nil
		})
	},
	"zstd": func(args []Value) (Value, *Fault) {
		return Bytes{B: zstdEncoder.EncodeAll(args[0].(Bytes).B, nil)}, nil
	},
	"unzstd": func(args []Value) (Value, *Fault) {
		return decompress("unzstd", args, func(data []byte) (io.Reader, func(), error) {
			d := zstdDecoders.Get().(*zstd.Decoder)
			if err := d.Reset(bytes.NewReader(data)); err != nil {
				zstdDecoders.Put(d)
				return nil, func() {}, err
			}
			return d, func() { _ = d.Reset(nil); zstdDecoders.Put(d) }, nil
		})
	},
}

// decompress reads what open's reader produces from args' data, up to
// args' maxSize bytes: Ok with the bytes, Err(Corrupt) for data that isn't
// the format, or Err(TooLarge) for more than maxSize.
func decompress(name string, args []Value, open func([]byte) (io.Reader, func(), error)) (Value, *Fault) {
	data, maxSize := args[0].(Bytes).B, int(args[1].(Int))
	if maxSize < 0 {
		return nil, Faultf("compress.%s's maxSize is %d; it can't be negative", name, maxSize)
	}
	r, done, err := open(data)
	defer done()
	if err != nil {
		return decompressErr("Corrupt", Str(err.Error())), nil
	}
	out, err := io.ReadAll(io.LimitReader(r, int64(maxSize)+1))
	if err != nil {
		return decompressErr("Corrupt", Str(err.Error())), nil
	}
	if len(out) > maxSize {
		return decompressErr("TooLarge", Int(maxSize)), nil
	}
	return Result{Ok: true, Value: Bytes{B: out}}, nil
}

func decompressErr(variant string, field Value) Value {
	return Result{Ok: false, Value: EnumValue{TypeName: "DecompressError", Variant: variant, Fields: []Value{field}, Decl: types.DecompressErrorDecl}}
}

// callCompress evaluates `compress.member(args)`.
func (in *Interp) callCompress(ctx context.Context, fa *syntax.FieldAccess, args []syntax.Arg, env *Env) (Value, bool, *Fault) {
	m, ok := types.CompressFormula(fa.Field)
	body := compressFormulas[fa.Field]
	if !ok || body == nil {
		return nil, false, Faultf("the prelude compress group has no formula %q", fa.Field)
	}
	vals, earlyReturn, fault := in.evalArgsPositional(ctx, args, env)
	if fault != nil || earlyReturn {
		return nil, earlyReturn, fault
	}
	if vals, fault = placeArgs("compress."+fa.Field, m.ParamNames(), m.Required(), args, vals); fault != nil {
		return nil, false, fault
	}
	for i, p := range m.Params {
		if !cryptoArgFits(vals[i], p.Type) {
			return nil, false, Faultf("compress.%s's %s is %s, not a %s", fa.Field, p.Name, vals[i], p.Type)
		}
	}
	v, fault := body(vals)
	return v, false, fault
}
