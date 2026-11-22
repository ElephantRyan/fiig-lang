# fiig TextMate grammar

`fiig.tmLanguage.json` highlights `.fiig` files in any editor that reads
TextMate grammars (VS Code, Sublime Text, Zed, and GitHub through Linguist).
Its scope is `source.fiig`.

It is generated from the compiler's lexer, as the IntelliJ plugin's keywords
are (ADR 0090 §2). After adding a keyword, give it a scope in
`tooling/textmategen`'s `Scopes` and run, from the repository root:

    go run ./tooling/textmategen

`tooling/textmategen`'s tests fail until both are done.

What it reads, beyond the lexer's tokens:

- a declaration's kind and name: `gateway charge`, `model Rating`,
  `adapter http accountsApi`, and, at the start of a line, the contextual
  `application Name {` and `test name {`
- an http route's method: `POST "/sessions"`
- type arguments written without a space: `Result<T, E>`; `a < b` is a
  comparison
- a reserved word beside a dot as a name: `use acme.http`, `http.WebRequest`
- ADR 0142's `;`, `!`, `&&`, `||` and `??`, and ADR 0143's contextual `wire`
  at the start of a line, as `principal` is

Other contextual words (`from`, `key`, `then`, ...) are names elsewhere, so
they are not highlighted, as in IntelliJ. Calls are coloured by name and
`(`; colouring them by what they call takes the language server (ADR 0106).
