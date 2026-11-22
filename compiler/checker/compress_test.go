package checker

import "testing"

// ADR 0116: the prelude compress group.

func TestCompressChecksClean(t *testing.T) {
	diags := checkSrc(t, `rule packed(records: bytes) -> bytes {
    return compress.lz4(records);
}`, `rule unpacked(data: bytes) -> bytes | None {
    return match (compress.unzstd(data, maxSize: 1048576)) {
        Ok(out) -> out,
        Err(DecompressError.TooLarge(_)) -> None,
        Err(DecompressError.Corrupt(_)) -> None,
    }
}`, `conversion roundTrip(data: bytes) -> Result<bytes, DecompressError> {
    return compress.unsnappy(compress.snappy(data), 65536);
}`, `rule gzipped(data: bytes) -> Result<bytes, DecompressError> {
    return compress.gunzip(compress.gzip(data), maxSize: 100);
}`)
	if len(diags) > 0 {
		t.Fatalf("diags = %v", diags)
	}
}

func TestCompressRefusals(t *testing.T) {
	cases := []struct {
		name, src, rule, fragment string
	}{
		{"string data", `rule r(s: string) -> bytes {
    return compress.gzip(s);
}`, "A1", "expected bytes"},
		{"no maxSize", `rule r(d: bytes) -> Result<bytes, DecompressError> {
    return compress.unlz4(d);
}`, "", "compress.unlz4 takes 2 argument(s), got 1"},
		{"unknown", `rule r(d: bytes) -> bytes {
    return compress.brotli(d);
}`, "", `no formula "brotli"`},
		{"not called", `rule r(d: bytes) -> bool {
    f = compress.zstd;
    return true;
}`, "", "call it, e.g. compress.zstd(...)"},
		{"local named compress", `rule r(compress: int) -> int {
    return compress;
}`, "G4", "prelude compress group"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			wantDiag(t, checkSrc(t, c.src), c.rule, c.fragment)
		})
	}
	wantDiag(t, checkSrc(t, `rule compress(a: int) -> int {
    return a;
}`), "K2", "the prelude compress group")
	wantDiag(t, checkSrc(t, `enum DecompressError {
    Other,
}`), "K2", "a prelude type")
}
