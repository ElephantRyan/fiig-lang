# ADR 0116: Compression Codecs in Go, the Prelude compress Group

Status: Accepted (2026-11-21, by the owner). Implemented 2026-11-20.
Date: 2026-11-20
Related: ADR 0042 (the socket floor), ADR 0049 (prelude groups), ADR
0075 (the crypto group, and CRC-32C's admission in ADR 0113 §2), ADR
0113 §5 (record batches carry their compression bits), ADR 0115 (a
faster interpreter).

## Context

Kafka record batches are usually compressed. A producer that can't
compress uses more network and broker disk than its Java neighbours, and
a consumer that can't decompress can't read a topic other clients write,
since Java producers commonly send lz4, snappy or zstd. Kafka defines
four codecs: gzip (1), snappy (2), lz4 (3) and zstd (4).

ADR 0042 puts a primitive in Go only when fiig can't express it. All four
codecs are expressible in fiig, so admitting them is about speed alone.
Measured on 2026-11-20, the interpreter walks bytes at about 216 ns a
byte (ADR 0115). A compressor does a hash, a table lookup and a compare
at every position, so a fiig compressor would manage well under 1 MB/s
today, and 10 to 30 MB/s after ADR 0115's targets. Go's libraries
compress at hundreds of MB/s and decompress at GB/s. A producer that
compresses every batch can't spend most of a core per 20 MB/s.

The owner chose speed now, using established libraries, with all four
codecs at once.

## Decision

### 1. A prelude `compress` group

Like `crypto`, every file sees it without a `use`, so `compress` is
reserved (G4, K2), and every member is pure.

```fiig
compress.gzip(data: bytes) -> bytes
compress.gunzip(data: bytes, maxSize: int) -> Result<bytes, DecompressError>
compress.snappy(data: bytes) -> bytes
compress.unsnappy(data: bytes, maxSize: int) -> Result<bytes, DecompressError>
compress.lz4(data: bytes) -> bytes
compress.unlz4(data: bytes, maxSize: int) -> Result<bytes, DecompressError>
compress.zstd(data: bytes) -> bytes
compress.unzstd(data: bytes, maxSize: int) -> Result<bytes, DecompressError>
```

The names follow the Unix tools (`gunzip`, `unlz4`, `unzstd`). The
exception to ADR 0042 is narrow, and CRC-32C's test, extended: a codec
fixed by a published format, run over every byte, and measured too slow
in fiig. It isn't a general rule that slow things go native.

### 2. Formats, as Kafka's clients write them

| Member | Format | Library |
|---|---|---|
| `gzip` | gzip (RFC 1952), default level | Go's `compress/gzip` |
| `snappy` | one raw snappy block | `github.com/klauspost/compress/snappy` v1.20.3 |
| `lz4` | the LZ4 frame format: 64 KiB independent blocks, no checksums | `github.com/pierrec/lz4/v4` v4.1.33 |
| `zstd` | one zstd frame, default level | `github.com/klauspost/compress/zstd` v1.20.3 |

These framings were checked against batches Kafka 4.0's Java producer
wrote, read back from the broker's log segment (2026-11-20):

- **lz4** is the standard frame: magic `04 22 4D 18`, FLG `0x60` (version
  1, independent blocks, no block or content checksum), BD `0x40` (64 KiB
  blocks), header checksum `0x82`, blocks, then an end mark of 0.
  `compress.lz4` writes those seven header bytes exactly.
- **snappy** is xerial-framed (snappy-java's `SnappyOutputStream`), not
  raw and not Google's framing format: the magic `82 53 4E 41 50 50 59
  00`, version 1 and compatible version 1 as `i32be`, then chunks, each an
  `i32be` length and one raw snappy block. Xerial is Kafka's convention,
  not snappy's, so `compress.snappy` is the raw block and fiig.kafka
  frames it (§4).

Both libraries are BSD-licensed, and pinned to versions released at
least five weeks earlier. The compiler module had only `golang.org/x/crypto`
before.

### 3. Decompression is bounded and fails as a value

Every decompress names `maxSize`, the most it may produce. A few
kilobytes of zstd can expand to gigabytes, and a consumer reads whatever
any producer sent. More than `maxSize` is `Err(TooLarge)`, found without
producing the rest. Bytes that aren't the format, or a truncated stream,
are `Err(Corrupt)`. Neither is a fault, because a broker's data is
outside the program. A negative `maxSize` is the program's mistake, so
it faults. A zstd frame may ask for a window of at most 64 MiB.

```fiig
enum DecompressError {
    Corrupt(reason: string)
    TooLarge(maxSize: int)
}
```

### 4. fiig.kafka compresses and decompresses

- **Config.** `KafkaServer.compression: Compression | None from env
  "KAFKA_COMPRESSION"`, in Kafka's `compression.type` spelling (`none`,
  `gzip`, `snappy`, `lz4`, `zstd`); unset sends uncompressed. A misspelling
  refuses to start, listing the five.
- **Producing.** `recordBatch` compresses the records section, puts the
  codec in bits 0-2 of the attributes, and computes the CRC over the
  compressed bytes. Snappy is xerial-framed in fiig (`xerialFramed`), in
  32 KiB chunks as snappy-java writes them. That's work per chunk, not per
  byte, so fiig is fast enough for it.
- **Consuming.** `unpackedRecords(compression, data, maxSize)` is ready
  for the consumer: it decompresses a fetched batch's records section, and
  reads snappy either xerial-framed or as one raw block, since producers
  write both. A failure is the new `KafkaError.Decompress(failure)`.

### 5. Verified

- **Known answers.** The Java batches above decompress to their two
  records, in Go (`compiler/runtime`) and through fiig.kafka's xerial path.
- **Round trips and limits.** Every codec round-trips inputs from empty to
  200 KB, and stops at `maxSize`. Junk and truncated streams are
  `Corrupt`. A fuzz test fed four million inputs to every decompressor
  without a panic.
- **Against the broker** (Kafka 4.0, single-node KRaft). For each codec:
  - fiig sent five records, with keys, a header and a null key, and the
    broker accepted them.
  - `kafka-dump-log` named the codec the broker stored.
  - Java's console consumer read back every key, header and value.
  - A 100,000-byte record, four xerial chunks under snappy, read back
    whole.

## Alternatives considered

- **Codecs in fiig.** Too slow today, and still too slow after ADR 0115.
  It stays possible: if the interpreter ever gets close, the members can
  be reimplemented in fiig behind the same names.
- **Hand-written Go.** No new dependencies, but about a day more and
  slower than the libraries' assembly. zstd would have been a project of
  its own.
- **Xerial framing in Go**, inside `compress.snappy`. Kafka's convention
  would then leak into a general primitive.
- **Snappy and lz4 only.** gzip is Go's standard library, and zstd is in
  the same module as snappy, so leaving them out saved little.

## Not yet

Compression levels (every codec uses its default), and Google's snappy
framing format, which Kafka doesn't use.
