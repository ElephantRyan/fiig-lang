package lsp

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"fiig-lang/compiler/lexer"
)

// ADR 0089 §6: the protocol counts a line's characters in UTF-16 code
// units unless the client accepts UTF-8 or UTF-32; the compiler counts code
// points and keeps each position's byte offset, from which every encoding
// is computed.

type encoding string

const (
	utf8Enc  encoding = "utf-8"
	utf16Enc encoding = "utf-16"
	utf32Enc encoding = "utf-32"
)

// chooseEncoding picks UTF-32 (fiig's code points), then UTF-8, from what
// the client offers, and UTF-16 otherwise.
func chooseEncoding(offered []string) encoding {
	for _, want := range []encoding{utf32Enc, utf8Enc} {
		for _, o := range offered {
			if encoding(o) == want {
				return want
			}
		}
	}
	return utf16Enc
}

// units counts s in enc's units.
func units(s string, enc encoding) int {
	switch enc {
	case utf8Enc:
		return len(s)
	case utf32Enc:
		return utf8.RuneCountInString(s)
	}
	n := 0
	for _, r := range s {
		n += utf16.RuneLen(r)
	}
	return n
}

// lineStart is the byte offset of the start of the line holding offset.
func lineStart(src string, offset int) int {
	offset = min(max(offset, 0), len(src))
	return strings.LastIndexByte(src[:offset], '\n') + 1
}

// toPosition converts a byte offset in src to a protocol position.
func toPosition(src string, offset int, enc encoding) position {
	offset = min(max(offset, 0), len(src))
	start := lineStart(src, offset)
	return position{Line: strings.Count(src[:start], "\n"), Character: units(src[start:offset], enc)}
}

// fromPosition converts a protocol position to a byte offset in src,
// clamped to the line's end.
func fromPosition(src string, p position, enc encoding) int {
	offset := 0
	for line := 0; line < p.Line; line++ {
		i := strings.IndexByte(src[offset:], '\n')
		if i < 0 {
			return len(src)
		}
		offset += i + 1
	}
	end := strings.IndexByte(src[offset:], '\n')
	if end < 0 {
		end = len(src) - offset
	}
	line := src[offset : offset+end]
	count := 0
	for i, r := range line {
		if count >= p.Character {
			return offset + i
		}
		switch enc {
		case utf8Enc:
			count += utf8.RuneLen(r)
		case utf32Enc:
			count++
		default:
			count += utf16.RuneLen(r)
		}
	}
	return offset + len(line)
}

// rangeOf converts the compiler's start and end to a protocol range.
func rangeOf(src string, start, end lexer.Pos, enc encoding) lspRange {
	if end.Offset < start.Offset {
		end = start
	}
	return lspRange{Start: toPosition(src, start.Offset, enc), End: toPosition(src, end.Offset, enc)}
}

// uriToPath turns a file URI into a clean local path.
func uriToPath(uri string) (string, bool) {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" {
		return "", false
	}
	p := u.Path
	if runtime.GOOS == "windows" && len(p) >= 3 && p[0] == '/' && p[2] == ':' {
		p = p[1:]
	}
	return filepath.Clean(filepath.FromSlash(p)), true
}

// pathToURI turns a local path into a file URI.
func pathToURI(path string) string {
	p := filepath.ToSlash(path)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// key is a path as map keys compare it: case-insensitively on Windows.
func key(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		return strings.ToLower(path)
	}
	return path
}
