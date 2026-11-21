package main

import (
	"os"
	"path/filepath"
	"testing"

	"fiig-lang/compiler/lexer"
)

// The grammar's keywords are the compiler's (ADR 0090 §2).
func TestGrammarIsCurrent(t *testing.T) {
	got, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(Target)))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != Generate() {
		t.Fatalf("%s is out of date with compiler/lexer's keywords; run `go run ./tooling/textmategen`", Target)
	}
}

func TestEveryKeywordHasAScope(t *testing.T) {
	reserved := map[string]bool{}
	for _, k := range lexer.Keywords() {
		reserved[k] = true
		if _, ok := Scopes[k]; !ok {
			t.Errorf("keyword %q has no TextMate scope in Scopes", k)
		}
	}
	for k := range Scopes {
		if !reserved[k] {
			t.Errorf("Scopes lists %q, which compiler/lexer does not reserve", k)
		}
	}
}
