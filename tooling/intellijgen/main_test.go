package main

import (
	"os"
	"path/filepath"
	"testing"
)

// ADR 0090 §2: the plugin's keywords are the compiler's.
func TestKeywordsAreCurrent(t *testing.T) {
	got, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(Target)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != Generate() {
		t.Fatalf("%s is out of date with compiler/lexer's keywords; run `go run ./tooling/intellijgen`", Target)
	}
}
