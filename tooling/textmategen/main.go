// Command textmategen writes fiig's TextMate grammar from the compiler's
// lexer, the way intellijgen writes the IntelliJ plugin's keywords (ADR 0090
// §2), so a new keyword reaches GitHub, VS Code, and every other TextMate
// highlighter or TestGrammarIsCurrent fails:
//
//	go run ./tooling/textmategen
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"fiig-lang/compiler/lexer"
)

// Target is the generated file, relative to the repository root.
const Target = "ide/textmate/fiig.tmLanguage.json"

// Scopes is the TextMate scope each reserved word is highlighted with.
// The lexer only knows a word is reserved; themes colour a declaration kind
// and a branch differently, so every keyword is placed here by hand, and
// TestEveryKeywordHasAScope fails on one that isn't.
var Scopes = map[string]string{
	"model":      "storage.type",
	"enum":       "storage.type",
	"rule":       "storage.type",
	"conversion": "storage.type",
	"activity":   "storage.type",
	"gateway":    "storage.type",
	"workflow":   "storage.type",
	"adapter":    "storage.type",
	"interface":  "storage.type",
	"capability": "storage.type",
	"formulas":   "storage.type",
	"formula":    "storage.type",
	"config":     "storage.type",

	"return":      "keyword.control",
	"if":          "keyword.control",
	"else":        "keyword.control",
	"match":       "keyword.control",
	"for":         "keyword.control",
	"while":       "keyword.control",
	"break":       "keyword.control",
	"continue":    "keyword.control",
	"retry":       "keyword.control",
	"exit":        "keyword.control",
	"transaction": "keyword.control",
	"concurrent":  "keyword.control",

	"use": "keyword.control.import",
	"as":  "keyword.control.import",

	"and": "keyword.operator.word",
	"or":  "keyword.operator.word",
	"not": "keyword.operator.word",

	"private":       "storage.modifier",
	"requires":      "storage.modifier",
	"idempotent":    "storage.modifier",
	"preserveOrder": "storage.modifier",

	"in":       "keyword.other",
	"limit":    "keyword.other",
	"attempts": "keyword.other",
	"command":  "keyword.other",
	"http":     "keyword.other",
	"cli":      "keyword.other",
}

// Primitives are the built-in scalar types (types.Primitive). They are
// identifiers to the lexer, so they are listed here, not read from it.
var Primitives = []string{"bool", "bytes", "decimal", "float", "int", "string"}

// HTTPMethods are ADR 0021's route verbs (compiler/checker httpMethods),
// identifiers to the lexer as well.
var HTTPMethods = []string{"DELETE", "GET", "PATCH", "POST", "PUT", "QUERY"}

const (
	ident  = `[A-Za-z][A-Za-z0-9_]*`
	before = `(?<![A-Za-z0-9_])`
	after  = `(?![A-Za-z0-9_])`
)

// word matches alternatives as whole words. The lexer's letters are ASCII,
// so this spells the boundary out instead of using Unicode-aware \b.
func word(alternatives ...string) string {
	return before + "(?:" + strings.Join(alternatives, "|") + ")" + after
}

type rule = map[string]any

func match(re, name string) rule { return rule{"match": re, "name": name} }

func captures(names ...string) rule {
	c := rule{}
	for i, n := range names {
		c[fmt.Sprint(i+1)] = rule{"name": n}
	}
	return c
}

// whole names a begin or end match that has no groups of its own.
func whole(name string) rule { return rule{"0": rule{"name": name}} }

func include(name string) rule { return rule{"include": "#" + name} }

// keywordRules groups the keywords by scope, one rule per scope, in a
// stable order.
func keywordRules() []any {
	byScope := map[string][]string{}
	for _, k := range lexer.Keywords() {
		s, ok := Scopes[k]
		if !ok {
			s = "keyword.other"
		}
		byScope[s] = append(byScope[s], k)
	}
	scopes := make([]string, 0, len(byScope))
	for s := range byScope {
		scopes = append(scopes, s)
	}
	sort.Strings(scopes)
	// A reserved word beside a dot is a path segment, like the package
	// in `use acme.http` or `http.WebRequest`, not the keyword.
	all := word(lexer.Keywords()...)
	rules := []any{
		match(`(?<=\.)`+all, "variable.other.fiig"),
		match(all+`(?=\.)`, "variable.other.fiig"),
	}
	for _, s := range scopes {
		rules = append(rules, match(word(byScope[s]...), s+".fiig"))
	}
	return rules
}

// Grammar is the grammar as JSON-ready values.
func Grammar() rule {
	return rule{
		"$schema":   "https://raw.githubusercontent.com/martinring/tmlanguage/master/tmlanguage.json",
		"name":      "fiig",
		"scopeName": "source.fiig",
		"fileTypes": []string{"fiig"},
		"patterns": []any{
			include("comment"),
			include("string"),
			include("declaration"),
			include("keyword"),
			include("constant"),
			include("type"),
			include("call"),
			include("number"),
			include("operator"),
			include("punctuation"),
			include("identifier"),
			include("invalid"),
		},
		"repository": rule{
			"comment": match(`//.*$`, "comment.line.double-slash.fiig"),

			// compiler/lexer scanStringPart: escapes, and `${...}` holding
			// any expression, strings and braces included.
			"string": rule{
				"name":          "string.quoted.double.fiig",
				"begin":         `"`,
				"end":           `"`,
				"beginCaptures": whole("punctuation.definition.string.begin.fiig"),
				"endCaptures":   whole("punctuation.definition.string.end.fiig"),
				"patterns": []any{
					match(`\\["\\ntr$]`, "constant.character.escape.fiig"),
					match(`\\.?`, "invalid.illegal.escape.fiig"),
					include("interpolation"),
				},
			},
			"interpolation": rule{
				"name":          "meta.interpolation.fiig",
				"contentName":   "source.fiig",
				"begin":         `\$\{`,
				"end":           `\}`,
				"beginCaptures": whole("punctuation.section.interpolation.begin.fiig"),
				"endCaptures":   whole("punctuation.section.interpolation.end.fiig"),
				"patterns":      []any{include("nested-braces"), rule{"include": "$self"}},
			},
			// A '{' inside an interpolation is a block, so the '}' that
			// closes it does not close the interpolation.
			"nested-braces": rule{
				"begin":         `\{`,
				"end":           `\}`,
				"beginCaptures": whole("punctuation.section.block.begin.fiig"),
				"endCaptures":   whole("punctuation.section.block.end.fiig"),
				"patterns":      []any{include("nested-braces"), rule{"include": "$self"}},
			},

			// A declaration's kind and name. `application` and `test` are
			// contextual (compiler/parser), so they are only read at the
			// start of a line, where the parser reads them.
			"declaration": rule{"patterns": []any{
				rule{
					"match":    `(` + word("rule", "conversion", "activity", "gateway", "workflow", "formula") + `)\s+(` + ident + `)`,
					"captures": captures("storage.type.fiig", "entity.name.function.fiig"),
				},
				rule{
					"match":    `(` + word("adapter") + `)\s+(` + ident + `)\s+(` + ident + `)`,
					"captures": captures("storage.type.fiig", "keyword.other.fiig", "entity.name.function.fiig"),
				},
				rule{
					"match":    `(` + word("model", "enum", "interface", "capability", "config", "formulas") + `)\s+(` + ident + `)`,
					"captures": captures("storage.type.fiig", "entity.name.type.fiig"),
				},
				rule{
					"match":    `^\s*(application)\s+(` + ident + `)(?=\s*\{)`,
					"captures": captures("storage.type.fiig", "entity.name.type.fiig"),
				},
				rule{
					"match":    `^\s*(test)\s+(` + ident + `)(?=\s*\{)`,
					"captures": captures("storage.type.fiig", "entity.name.function.fiig"),
				},
				// `principal` is contextual too: at the top level (ADR 0141)
				// and at the start of an adapter body line (ADR 0137), before
				// the principal's name, or the one an adapter names.
				rule{
					"match":    `^\s*(principal)\s+(` + ident + `)`,
					"captures": captures("storage.type.fiig", "entity.name.function.fiig"),
				},
				// An http adapter's route: `POST "/sessions" -> logIn(...)`.
				rule{
					"match":    `^\s*(` + strings.Join(HTTPMethods, "|") + `)(?=\s+")`,
					"captures": captures("keyword.other.http-method.fiig"),
				},
			}},

			"keyword": rule{"patterns": keywordRules()},

			"constant": rule{"patterns": []any{
				match(word("true", "false"), "constant.language.boolean.fiig"),
				match(word("None"), "constant.language.none.fiig"),
				match(before+`_`+after, "variable.language.wildcard.fiig"),
			}},

			"type": rule{"patterns": []any{
				// A type's arguments, written without a space: `Result<T, E>`.
				// A comparison has spaces (fiig fmt), so `a < b` stays one.
				rule{
					"name":          "meta.generic.fiig",
					"begin":         before + `([A-Z][A-Za-z0-9_]*)(<)(?!<)`,
					"end":           `>`,
					"beginCaptures": captures("entity.name.type.fiig", "punctuation.definition.generic.begin.fiig"),
					"endCaptures":   whole("punctuation.definition.generic.end.fiig"),
					"patterns":      []any{rule{"include": "$self"}},
				},
				match(`(?<!\.)`+word(Primitives...), "support.type.primitive.fiig"),
				match(before+`[A-Z][A-Za-z0-9_]*`+after, "entity.name.type.fiig"),
			}},

			"call": match(before+`[a-z][A-Za-z0-9_]*(?=\s*\()`, "entity.name.function.call.fiig"),

			// compiler/lexer scanNumber: a fraction needs a digit on both
			// sides of the dot (ADR 0048 §2.4).
			"number": rule{"patterns": []any{
				match(before+`0[xX][0-9A-Fa-f]+`+after, "constant.numeric.integer.hexadecimal.fiig"),
				match(before+`[0-9]+(?:\.[0-9]+)?[eE][+-]?[0-9]+`+after, "constant.numeric.float.fiig"),
				match(before+`[0-9]+\.[0-9]+`+after, "constant.numeric.float.fiig"),
				match(before+`[0-9]+`+after, "constant.numeric.integer.fiig"),
			}},

			"operator": rule{"patterns": []any{
				match(`->`, "keyword.operator.arrow.fiig"),
				// ADR 0058: a binary pattern's `<<` is one token and its `>>`
				// two, but they read as a pair.
				match(`<<`, "punctuation.definition.binary.begin.fiig"),
				match(`>>`, "punctuation.definition.binary.end.fiig"),
				match(`==|!=|<=|>=|<|>`, "keyword.operator.comparison.fiig"),
				match(`=`, "keyword.operator.assignment.fiig"),
				match(`[-+*/%]`, "keyword.operator.arithmetic.fiig"),
				match(`\?`, "keyword.operator.propagate.fiig"),
				match(`\|`, "keyword.operator.union.fiig"),
			}},

			"punctuation": rule{"patterns": []any{
				match(`\{`, "punctuation.section.block.begin.fiig"),
				match(`\}`, "punctuation.section.block.end.fiig"),
				match(`\(`, "punctuation.section.parens.begin.fiig"),
				match(`\)`, "punctuation.section.parens.end.fiig"),
				match(`\[`, "punctuation.section.brackets.begin.fiig"),
				match(`\]`, "punctuation.section.brackets.end.fiig"),
				match(`,`, "punctuation.separator.comma.fiig"),
				match(`:`, "punctuation.separator.colon.fiig"),
				match(`\.`, "punctuation.accessor.fiig"),
			}},

			"identifier": match(ident, "variable.other.fiig"),

			// Anything else is a character compiler/lexer rejects.
			"invalid": match(`\S`, "invalid.illegal.character.fiig"),
		},
	}
}

// Generate is the file's contents.
func Generate() string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(Grammar()); err != nil {
		panic(err)
	}
	return b.String()
}

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	}
	path := filepath.Join(root, filepath.FromSlash(Target))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := os.WriteFile(path, []byte(Generate()), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
