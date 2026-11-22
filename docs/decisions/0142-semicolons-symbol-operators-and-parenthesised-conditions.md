# ADR 0142: Semicolons, Symbol Operators, and Parenthesised Conditions

Status: Accepted (2026-11-21; the owner asked for it to be implemented).
Step 1 (§5) done in fiig-lang: the new forms parse and run beside the
old ones, `fiig fmt` writes only the new ones, and the standard library,
the examples and the editors are rewritten. Left for step 1: the examples
in the other lane, Hearsay (its owner's), and the fiig snippets in Go
tests and docs. Step 2, the old forms as errors, follows.
Date: 2026-11-21
Amends: ADR 0011 (boolean operators), ADR 0099 (`or` gives a default),
ADR 0013 (`if`/`else` chains), ADR 0053 (`for`), ADR 0059 (`while`).
Related: charter principle 10 (strict about what a program says), ADR 0006
(`?`), ADR 0090 (IntelliJ lexer), `docs/specification/grammar.md` §5.

## Context

Three parts of fiig's surface syntax were never decided on their merits.

1. **Statements have no terminator, and line breaks mean nothing.** The
   lexer skips newlines like any other space, so the parser finds where a
   statement ends only from where an expression can't continue. When it can
   continue, two lines silently become one:

   ```fiig
   x = a
   (b)        // parsed, formatted, and run as `x = a(b)`
   ```

   ```fiig
   x = a
   -b         // parsed as `x = a - b`
   ```

   Neither is an error. Every language that drops `;` fixes this one way or
   another: Go's lexer inserts a `;` at a newline after a name, a literal,
   `)`, `]` or `}`; Kotlin and Swift treat a newline as a terminator where
   one can end; JavaScript's automatic semicolon insertion gets it wrong
   in exactly this `(` case. Fiig does none of these.
2. **`not`, `and` and `or` were never weighed against `!`, `&&` and
   `||`.** ADR 0011 chose infix operators over methods like `.and()`. The
   word spelling came with `not`, which was already in the early grammar.
   Since then ADR 0099 gave `or` a second meaning, a default for `T | None`
   (`dryRun or true`), so one word now means two different things.
3. **Conditions are bare** (`if x > 0 {`), as in Go, Rust and Swift. That
   was inherited, not chosen.

### How others do it

| | Statement end | Boolean operators | Default for a missing value | Condition parentheses |
| --- | --- | --- | --- | --- |
| Java, C# | `;` required | `! && \|\|` | C#: `??` | required |
| Rust | `;` required; the tail expression has none | `! && \|\|` | `.unwrap_or(x)` | none |
| Go | newline (lexer inserts `;`) | `! && \|\|` | none | none |
| Kotlin, Swift | newline; `;` allowed | `! && \|\|` | `?:`, `??` | Kotlin required, Swift none |
| TypeScript | `;` optional (ASI) | `! && \|\|` | `??` | required |
| Python | newline | `not and or` | `or` (any falsy value) | none |

C and Java need condition parentheses because their braces are optional.
Go and Rust dropped them because braces are required. Fiig requires braces,
so in fiig the parentheses are a readability choice, not a parsing need.

## Decision

### 1. A statement ends with `;`

Every statement in a block ends with `;`:

```fiig
workflow syncCollections(me: accounts.Identity, userId: string | None, dryRun: bool | None) -> Result<List<SyncResult>, SyncError> {
    if (!settings.mirrorEnabled()) {
        return Err(SyncError.MirroringOff);
    }

    plans = loadSyncPlans(me, userId)?;
    abs = mapErr(e in settings.loadAbsTarget()): syncErrorOfSettings(e)?;
    return Ok(applyPlans(abs, plans));
}
```

- **Takes `;`:** a binding or reassignment, `return`, `break`, `continue`,
  a call or other expression used as a statement, and a test's `expect`.
- **Takes none: a statement that ends in `}`.** That is one rule with no
  list to remember: `if`, `else`, `for`, `while`, `match`, `transaction`,
  `concurrent` and `retry` statements, and also a `return` or a binding
  whose value is a `match` (§2). Declarations, model fields, enum
  variants, imports, adapter routes and principals, and match or
  response-mapping arms aren't statements and take none either.
- **In a test** (Hearsay's question): a faked config's values, `fake
  SignInConfig { authMode: AuthMode.Abs }`, are fields, like a model's,
  and take none. A faked gateway's body and the test's own body are
  blocks of statements, so their statements take `;` as any other block's
  do. The formatter tells them apart by the declaration it is printing,
  not by guessing from the text.
- A missing `;` is a syntax error that names the fix: "a statement ends with
  `;`". Fixing the error is what the formatter can't do, because the file
  doesn't parse.

### 2. A value leaves a function only through `return`, and `match` is an expression

Today a block may end with a bare expression or a bare `match` whose arms
are the function's result, with no `return` (grammar §5). That implicit
value is removed. Instead, `match` becomes an expression, so the common
case keeps its short arms:

```fiig
conversion absPathId(id: string) -> Result<string, AbsError> {
    if (!validId(id)) {
        return Err(AbsError.NotFound);
    }
    return match (http.pathSegment(id)) {
        None -> Err(AbsError.NotFound)
        safe -> Ok(safe)
    }
}
```

- **`match` is a value after `return` and as a binding's value**:

  ```fiig
  label = match (status) {
      401 -> "unauthorized"
      404 -> "not found"
      _ -> "rejected"
  }
  ```

  A `match` written as a statement on its own, with arms that run for
  their effects, is unchanged.
- **Arms are values.** An arm whose body is a block must leave the
  function (`return`) or fault, as today:

  ```fiig
  return match (sent) {
      Err(failure) -> Err(AbsError.Unreachable(failure: failure))
      Ok(reply) -> {
          if (reply.status != 200) {
              return Err(absFailure(reply.status));
          }
          return Ok(reply);
      }
  }
  ```

  After `return match`, a `return` in an arm's block leaves the function
  with the value the `match` would have produced, so the two agree. In a
  binding, a `return` in an arm leaves the function rather than giving the
  name its value, so a binding's `match` can't produce a value from a
  multi-line arm. Java answers that with `yield`. Fiig leaves it out until
  real code needs it (Open questions).
- **The closing `}` of a `match` ends the statement.** Nothing follows it
  on the same statement: `match (x) { ... }(b)` is not a call, so the merge
  in Context can't come back through a `match`.
- **A block's last line is never its value**, so `;` never changes meaning,
  unlike Rust's tail expression (Alternatives).

This ends the `x = a` / `(b)` merge: `x = a;` then `(b);` is two
statements, and `x = a` followed by `(b)` with no `;` is a syntax error at
the `(`.

### 3. `!`, `&&`, `||`, and `??` for a default

| Was | Now | Meaning |
| --- | --- | --- |
| `not x` | `!x` | bool negation |
| `a and b` | `a && b` | bool, short-circuit |
| `a or b` (bool) | `a \|\| b` | bool, short-circuit |
| `a or b` (`T \| None`, ADR 0099) | `a ?? b` | `a` if it isn't `None`, else `b` |

- **`||` is bool only, and `??` gives the default.** That separates ADR
  0099's two meanings of `or`. A reader of `a ?? b` knows `a` may be `None`
  without looking up its type. The checker rules are ADR 0099's, moved
  across: `??` takes a `T | None` on the left and a `T` on the right, and
  is a type error on anything else.
- **Precedence**, highest first: `!` and unary `-`; `* / %`; `+ -`;
  comparisons; `&&`; `||`; `??`. A comparison takes no chaining, as now.
  Mixing `??` with `&&` or `||` without parentheses is a syntax error,
  as in JavaScript and TypeScript, because the intended grouping is never
  obvious.
- **`??` binds looser than arithmetic and comparisons** (Hearsay's
  question): `count ?? 0 + 1` is `count ?? (0 + 1)`, as in C# and Swift.
  That is the grouping `or` has today, since `or` too sits below `+`, so
  the rewrite from `or` to `??` never changes what a line means. Where an
  `or`-as-default has an `&&` or `||` beside it, the rewrite adds the
  parentheses this section requires. The other grouping is written
  `(count ?? 0) + 1`.
- **Lexing.** `!` alone, `&&`, `||` and `??` become tokens. `!=` stays one
  token. `??` is read before `?`, so `f()??fallback` is a default. To
  propagate and then default, write `f()? ?? fallback`.
- `not`, `and` and `or` stay reserved, so old code gets an error that names
  the replacement ("`and` is now `&&`, ADR 0142") rather than an unknown
  name. Freeing them as identifiers is a later decision.

### 4. Conditions and loop heads are parenthesised

```fiig
if (score > 7 && !flagged) {
    ...
} else if (score > 3) {
    ...
} else {
    ...
}

while (left > 0) { ... }
outer: for (order in orders) { ... }
for (key, value in limits) { ... }
match (row) {
    None -> ...
    found -> ...
}
```

- `if`, `else if`, `while`, `for` and `match` require the parentheses. A
  bare `if x {` is a syntax error that names the fix.
- A `for` puts its whole head in them, `(name in expression)`, as Kotlin
  does, and a label stays outside (`outer: for (...)`).
- `match` takes them too, so every construct that branches on a value reads
  the same way.

### 5. How the change lands

The parser can't require the new forms on the same day it learns them,
because nothing would parse. So it lands in two steps:

1. **Both forms parse, and `fiig fmt` writes only the new ones.** The
   formatter adds `;`, parentheses and the symbol operators, and turns
   `or`-as-default into `??` using the checker's types. That last one needs
   type information, so `fiig fmt` asks the checker when the file
   type-checks, and leaves an ambiguous `or` for the old-form error to
   catch otherwise. It never skips one quietly (Hearsay's question): for
   each `or` it leaves, it prints the file and line and that the file
   doesn't type-check, so `||` and `??` can't be told apart there, and it
   exits non-zero. Step 2 waits until no such line is left in fiig-lang
   or in an application being migrated. `fiig fmt -w` then runs over fiig-lang's `examples/`,
   `libraries/` and standard library, the fiig snippets in Go tests and in
   `docs/`, and Hearsay.
2. **The old forms become errors**, each naming its replacement, in the next
   release.

The IntelliJ lexer, the TextMate grammar (`tooling/textmategen`), the
language server's semantic tokens, and the IntelliJ plugin's templates and
colour demo all change with step 1.

## Rationale

- **A statement's end is said, not inferred** (principle 10, strict about
  what a program says). The merge in Context is a real bug class that the
  current grammar can't rule out. A required `;` rules it out, and it is
  the most widely read convention among Java, C#, Rust and C programmers.
- **A function's value is always said with `return`.** With no implicit
  last value, a `;` never changes what a line means, and the reader of a
  function finds every value it can produce by looking for `return`.
  Making `match` an expression keeps that from costing a block per arm.
  This is the shape Kotlin (`return when (x) { ... }`) and Java 14
  (`return switch (x) { ... };`) chose.
- **One meaning per operator.** `||` is boolean or, and `??` is the
  default. Today `or` is both.
- **The symbols are what most programmers already read**: every C-family
  language, Rust, Go, Kotlin, Swift and TypeScript. The cost is that `!x`
  is easier to miss than `not x`. The formatter puts no space between
  `!` and its operand, as everywhere else, and IntelliJ colours operators.
- **Parentheses mark the condition.** With `&&` and `||` inside it, a
  parenthesised condition reads as one unit before the `{`.

## Alternatives considered

- **A newline ends a statement (Go, Kotlin, Swift).** This also fixes the
  merge, with no `;` to type. Rejected on the owner's preference for
  explicit terminators. It also means a line may continue only after an
  operator, `(` or `,`, which is its own rule to learn.
- **Optional `;` that `fiig fmt` always writes.** You'd see semicolons
  everywhere, but there would be two accepted spellings of one thing, and
  the merge bug would still need a newline rule underneath.
- **Rust's tail expression: a block's last expression, written without
  `;`, is its value.** Keeps today's bare-`match` endings as they are. But
  it is the one place where `;` changes meaning: `base + "/10"` is the
  value and `base + "/10";` is a statement, so the function has none.
  Rust's type checker catches that, and fiig's would too, but `return`
  everywhere has no such rule to learn.
- **`return` everywhere, with `match` still a statement.** Uniform, but an
  arm can't be a bare `return`, so every arm of a final `match` would become
  a `{ return ...; }` block.
- **`;` after `return match (...) { ... }`, as Java writes it.** Java ends
  every `return` with `;`. Fiig's rule, that a statement ending in `}` takes
  none, has no exception to remember.
- **`||` also as the default (JavaScript).** Keeps ADR 0099's double
  meaning, which is what JavaScript's `??` was added to undo.
- **Keep `not`/`and`/`or`.** The words read well, and Python shows they
  scale. Rejected on the owner's preference, and because `or` would still
  mean two things.
- **Parentheses added by `fiig fmt` but not required.** That would make it
  layout, like the blank line after a block. Rejected: the owner wants one
  way to write a condition.

## Consequences

- Grammar §5: `statement` takes `;` (§1), `if`/`while`/`for`/`match` heads
  are parenthesised (§4), `match` is an expression after `return` and in
  a binding (§2), and the expression grammar has `!`, `&&`, `||` and `??`
  in place of `not`, `and` and `or` (§3).
- Lexer: `Semicolon`, `Bang`, `AndAnd`, `OrOr`, `QuestionQuestion` tokens.
- Checker: ADR 0099's rules move from `or` to `??`. A `match` used as a
  value is checked like one: every arm's value fits the expected type,
  and an arm block must leave the function or fault. A function with a
  result type that can reach its end without `return` is an error ("this
  function can end without returning a value"), replacing the implicit
  last value.
- Formatter: writes `;`, `if (...)`, and the symbols, and in the rewrite
  (§5) puts `return` before a function body's final bare `match` or bare
  expression, which is always its value today. Indentation and the blank
  line after a block are unchanged.
- Every `.fiig` file in fiig-lang and Hearsay is rewritten once, by
  `fiig fmt -w` (§5). Code samples in accepted ADRs keep the syntax of their
  day. The specification and the getting-started docs are updated.
- The other agent's in-progress branches need the same rewrite before they
  merge, so step 1 lands when both lanes have pushed what they have.

## Open questions

- **A multi-line arm in a binding's `match` (§2).** A `return` there
  leaves the function, so such an arm can't give the name its value. When
  real code needs one, the answer is Java's `yield` (`Ok(reply) -> {
  ...; yield reply; }`), which gives the arm's value without making a
  block's last line special. Until then, write the arm as a helper
  call.
- **`match` arms and route arms** stay newline-separated. The merge in
  Context can't happen there today, because no pattern starts with `(` or
  `-`. If literal patterns ever allow negative numbers, arms need a
  separator too, probably `,` as in Rust.
- **Freeing `not`, `and` and `or`** as identifiers, after a release with
  the old-form errors.
