# ADR 0091: `fiig test`, Tests Written in fiig

Status: Accepted (2026-11-18)
Date: 2026-11-18
Related: fiig-lang.md §3.2 (composition), §3.9 (files), Milestone 8 ("test
framework with fake capability providers"), ADR 0027 (capabilities
files), ADR 0043 (capability groups; P1/P2 shape checks), ADR 0044
(nothing calls a workflow), ADR 0052 (comparability), ADR 0054 (private
blocks), ADR 0060 (`socket-script`), ADR 0068 (`random-seeded`), ADR 0071
(config; "`fiig test` should take values per test"), bug class B10.

## Context

fiig code has no way to test fiig code. The repository tests its corpus
from Go, through the interpreter, and by running examples with `fiig run`.
A program's author has neither.

Test doubles exist, but only as JSON in a capabilities file: `scripted`
returns canned answers per operation, in order; `socket-script` plays a
network peer (ADR 0060); `random-seeded` repeats a run (ADR 0068). They
are outside the program, unchecked against what they replace, and `Clock`
has none, so code that reads the time can't be tested repeatably.

fiig is already shaped for testing. A rule is pure, so it's tested by
passing it values. Every effect goes through a named capability (`requires
Database`, `requires LedgerApi`), so every place a test needs a double is
a name the checker already knows. No mocking framework has to find the
seams.

### How others do it

| Language | A test is | Assertion | Doubles |
|---|---|---|---|
| Go | `func TestX(t *testing.T)` in `x_test.go` | `if got != want { t.Errorf(...) }` | hand-written types behind interfaces |
| Rust | a `#[test]` function | `assert_eq!(a, b)`, printing both | traits; `mockall` |
| Zig | a `test "name" { }` block | `try expect(...)` | explicit parameters |
| Gleam | a public function ending `_test` in `test/` | `should.equal(a, b)` | none needed for pure code |
| Elixir | `test "..." do ... end` | `assert a == b`, printing both sides | Mox: a mock must fit a declared behaviour |
| Ballerina | an `@test:Config` function | `test:assertEquals` | `test:mock` replaces a function or client |

Every one of them has **one** kind of test, whatever it tests. What
differs between testing a pure function and testing database code is the
setup, not the test. Elixir's Mox is the closest fit for doubles: a mock
must match a declared contract, so it can't drift from the real thing.

## Decision

### 1. A test is a declaration kind, and there is one

`test` is a new declaration kind. A test of a rule and a test of a
gateway are the same kind; what differs is which capabilities the test
must supply, and the checker derives that from what the test calls (§4).

```
// tests/rating_above_ten_is_refused.fiig — a rule: no doubles needed.
test ratingAboveTenIsRefused {
    result = createValidRating(NewRating(item: "tea", score: 11, note: None))
    expect result is Err(problem)
    expect problem == RatingError.Invalid("score", "must be from 1 to 10")
}
```

```
// tests/saved_rating_is_found.fiig — gateways: Database is a fresh
// in-memory database for this test (§5).
test savedRatingIsFound {
    createSchema()?
    saveRating(Rating(item: "tea", score: 7, note: None))
    expect findRating("tea") is Ok(found)
    expect found == Rating(item: "tea", score: 7, note: None)
}
```

Kinds in fiig are told apart by what they may call and who may call them
(§3.2). A test's place is fixed by both:

- **Nothing calls a test.** It's an entry point, as a workflow is.
- **A test may call** formulas, rules, conversions, activities, and
  gateways: what a workflow may call. Not workflows (ADR 0044: nothing
  calls a workflow) and not adapters.
- **A test may call a capability directly**, which no other kind but a
  gateway may (B10), so it can arrange data without writing a gateway for
  it: `Database.execute("INSERT INTO ratings ...")`.
- **A test may hold `transaction` blocks and `concurrent` blocks**, as an
  activity may.

A test is named like a rule (lowerCamel), one per file (§3.9), and may
have a `private { }` block for helpers (ADR 0054).

### 2. Tests live in `tests/`, outside the program

A package's tests are the `.fiig` files under its `tests/` directory, at
any depth. They are checked with the package, and they see its public
declarations and its dependencies' as any file in the package does. They
are not part of the program:

- `fiig run`, `fiig serve`, `fiig openapi`, and the registry (`fiig
  catalog`) leave them out.
- **T1:** a file outside `tests/` that names a declaration inside it is an
  error. A test helper can't leak into the program.
- **T2:** a `test`, `fake`, or `expect` outside `tests/` is an error.
- `fiig test` runs the root package's tests, not its dependencies'.

### 3. `expect`

```
expect <bool expression>
expect <expression> is <pattern>
```

`expect` is a statement, legal only in tests and fakes. It has two forms.

**`expect <bool expression>`** fails the test when the expression is
false. The report gives the file, line, the expression as written, and,
when the expression is a comparison (`==`, `!=`, `<`, `<=`, `>`, `>=`),
the value of each side:

```
FAIL ratingAboveTenIsRefused (tests/rating_above_ten_is_refused.fiig:3)
    tests/rating_above_ten_is_refused.fiig:4: expect problem == RatingError.Invalid("score", "must be from 1 to 10")
      left:  RatingError.Invalid("item", "must not be blank")
      right: RatingError.Invalid("score", "must be from 1 to 10")
```

**`expect <expression> is <pattern>`** fails the test when the value
doesn't match the pattern. The pattern is any pattern a `match` arm takes
(constructors, `_`, names, byte patterns), checked against the
expression's type as an arm is. It is how a test looks inside a `Result`,
which `==` can't compare (ADR 0052), and the names the pattern binds are
in scope for the rest of the block, so the value inside can be checked
next:

```
expect result is Err(problem)
expect problem == RatingError.Invalid("score", "must be from 1 to 10")
```

When it fails, the report gives the value that didn't match:

```
    tests/rating_above_ten_is_refused.fiig:3: expect result is Err(problem)
      value: Ok(Rating(item: "tea", score: 11, note: None))
```

`is` is part of `expect`, not an expression: it can't be negated, joined
with `and`, or used in an `if`. A `match` covers those.

- **A failed `expect` doesn't stop the test.** Every failure is reported,
  as Go's `t.Errorf` does.
- **Except a failed `is` that binds a name.** The lines after it would
  read a name with no value, so the test stops there, as Go's `t.Fatalf`
  does. An `is` that binds nothing (`expect saved is Err(_)`) doesn't
  stop it.
- **A fault does stop it**, and fails it with the fault's message and
  trace. So does an `Err` reaching the end of the test through `?`: a test
  returns nothing, and `?` in a test fails it with the error's value.
- **T3:** `expect`'s operand must be `bool`, and an `is` pattern must fit
  its expression's type, as a `match` arm must. Both sides of a comparison
  must be comparable (ADR 0052): models, enums, and lists compare by value;
  a `Result` doesn't, so a test checks one with `is`.

There is one assertion, with the two forms `match` and `==` already give
the language: `is` checks a shape, and `==` checks a value.

### 4. Doubles are `fake` blocks, checked against what they replace

A test replaces a capability, or a capability group's operations, with a
`fake` block at the start of its body:

```
test chargeIsRecorded {
    fake LedgerApi {
        gateway record(entry: string) -> Result<None, string> {
            expect entry == "charged o-1 as stripe:ch_1"
            return Ok(None)
        }
    }
    fake Stripe {
        gateway charge(request: payments.ChargeRequest) -> Result<string, string> {
            return Ok("stripe:ch_1")
        }
    }
    fake Square {
        gateway charge(request: payments.ChargeRequest) -> Result<string, string> {
            expect false
            return Err("a US merchant never reaches Square")
        }
    }

    reference = takePayment(usMerchant(), payments.ChargeRequest(orderId: "o-1", amount: 500))
    expect reference is Ok(id)
    expect id == "stripe:ch_1"
    expect calls(LedgerApi.record) == 1
}
```

- **The name is the seam.** `fake LedgerApi` replaces every use of
  `LedgerApi` reached from this test, at any depth: a gateway's `requires
  LedgerApi`, a provider value chosen by a rule (ADR 0043 §3), a call in
  an imported package. Nothing else changes.
- **A fake is checked against what it replaces (T4).** Each operation's
  name, parameters, and result must be the real one's, as P1/P2 check a
  provider against an interface. A fake of a capability the test can't
  reach is an error too (T5), so a fake left behind by a refactor is
  found.
- **Only a capability declared in fiig can be faked (T7).** A host
  capability with no declaration, such as an `EmailSender` a Go host
  registers, has no signature to check a fake against, so `fake
  EmailSender { ... }` is an error saying to declare it first. This is
  the direction B10 and P7 already push: a capability the program uses
  should have a declaration in fiig. `Socket`, `Clock`, `Random`, and
  `Database` are declared by the toolchain, so they can be faked.
- **A fake must have every operation the test reaches (T6).** The checker
  knows which operations the test's calls reach; a missing one is an
  error naming the call path, not a fault at run time.
- **Every provider a rule can return is reached.** When a rule chooses a
  provider (ADR 0043 §3), the checker can't know which one this test's
  values select, so it counts every provider the rule returns as reached.
  A test of `takePayment` must fake both `Stripe` and `Square`, because
  `choosePaymentProvider` returns either; faking only the one the test
  means to hit is a T6 error naming the other. The fake for the provider
  the test doesn't expect to reach can be one line that fails:

  ```
  fake Square {
      gateway charge(request: payments.ChargeRequest) -> Result<string, string> {
          expect false
          return Err("not reached")
      }
  }
  ```
- **A fake operation is a gateway body.** It may call rules, conversions,
  and formulas, `expect` on its arguments, and use the test's `Database`
  to keep state between calls.
- **`calls(Capability.operation)`** is the number of times the test
  called that faked operation so far, as an `int`.
- **A config is faked the same way**, with values instead of operations
  (ADR 0071's per-test values):

  ```
  fake StripeAccount {
      apiKey: "sk_test_1"
  }
  ```

  A `Secret` field takes a string literal here and nowhere else.

### 5. What a test gets without asking

Every test starts from nothing it shares with another test:

| Capability | In a test, unless faked |
|---|---|
| `Clock` | a test clock at 2026-01-01T00:00:00Z; `Clock.sleep(ms)` moves it forward and returns at once. `fake Clock { now: <ms> }` sets the start. |
| `Random` | seeded from the test's name, so a run repeats (ADR 0068's `random-seeded`). `fake Random { seed: <n> }` picks another. |
| `Database` (and every capability the package's capabilities file gives an `sql` or `driver` type) | a new in-memory SQLite database, empty |
| `Socket` | none: a test never opens a network connection on its own. Calling it fails the test, naming the call path, unless it's faked or `-caps` gives a real one. |
| any other capability | none: calling it fails the test, naming the capability, unless it's faked or `-caps` gives it |
| config | none: reading a value that isn't faked fails the test naming the variable, unless `-env-file` gives it |

`-caps file.json` and `-env-file file.env` on `fiig test` give every test
real providers and values instead, for integration tests against a real
Postgres or a test service. A `fake` in a test still wins over them.

- **The test clock doesn't follow the real date.** A test of "has this
  token expired?" sees 2026-01-01 whatever day it runs, which is what
  makes it repeat. A test that depends on the date sets it with `fake
  Clock { now: ... }`.
- **No test reaches the network by default.** A test that would open a
  connection without a fake or `-caps` fails rather than connecting, so a
  test run never depends on, or writes to, a service it didn't name. An
  integration test names its services in `-caps`.

### 6. The command

```
fiig test [-run pattern] [-caps file.json] [-env-file file.env] [-timeout 30s] [-v] [-json] [-workspace dir] [dir]
```

- Checks the package with its tests, then runs every test; `-run` keeps
  the tests whose name contains the pattern.
- **Tests run in parallel and in no fixed order**, which §5 makes safe.
  Each has its own capabilities, fakes, database, clock, and call counts.
- **Except when `-caps` gives a database.** Every test would then share
  one real database, and a test that deletes rows could break one running
  beside it, so the tests that reach a database from `-caps` run one at a
  time, in name order. Tests that don't reach it still run in parallel.
  Cleaning up between them is the tests' own job.
- Each test runs under a deadline, 30 seconds unless `-timeout` says
  otherwise, so a `while` loop that never ends (ADR 0059) fails its test
  instead of hanging the run.
- Prints one line per failure and a summary; `-v` prints every test.
  `-json` prints one JSON object per test (name, file, line, outcome,
  failures, duration), for the IntelliJ plugin's ▶ on a test and for CI.
- Exits 0 when every test passes, 1 when any fails, 2 when the package
  doesn't check.

### 7. Corpus

`examples/getting-started/tests/` gets tests of a rule, a gateway against
the in-memory database, and an activity, and `examples/checkout-app/tests/`
gets the `takePayment` test above, faking imported capability groups.
Faking a config is tested in `tooling/cli/testcmd`: in checkout-app the
only configs are Stripe's and Square's, which a test can't reach once it
fakes both providers, as §4 requires.

## Consequences

- A program's author can test it in fiig, with doubles the checker keeps
  honest. A fake that no longer matches the capability it replaces is a
  compile error, not a test that passes against a fiction.
- The eleventh declaration kind and three contextual keywords: `test` and
  `fake` at the start of a declaration or body, `expect` at the start of a
  statement. No identifier in the corpus is lost.
- Tests are the one kind that may both call a capability and coordinate
  (§1). That is deliberate: a test is outside the program, and B10 exists
  to keep effects out of the program's logic, not out of its tests.
- Rules need no setup to test, which makes the advice to put judgment in
  rules (ADR 0083) cheaper to follow.
- `tooling/runprogram`'s `scripted` JSON double stays for `fiig run`; it
  isn't the testing story.

- A test of code that chooses between providers fakes every provider it
  could choose (§4). That is more to write, but a test can't pass because
  it silently reached a real provider nobody faked.
- Bug classes T1 to T7 are new (§2–§4).

## Alternatives considered

- **A test kind per thing tested** (`test rule`, `test gateway`). Every
  language surveyed has one; the differences are setup, which §4 and §5
  derive from the calls.
- **Tests as activities with a naming convention** (Gleam's `_test`). An
  activity can't call a capability or hold a fake, would be callable from
  the program, and would sit in the registry.
- **Tests attached to the declaration** (Pyret's `where:` block in the
  rule's own file). Good for rules, but a gateway's fakes would live in
  production source, and one test often crosses several declarations.
- **Doubles in JSON** (extending `scripted`). No new syntax, but the
  doubles are unchecked, can't compute an answer from their arguments, and
  live outside the source a reader opens.
- **Unchecked fakes of undeclared host capabilities.** They'd make
  `EmailSender` testable today, but a fake nobody checks is the JSON
  double again, in fiig syntax (§4, T7).
- **A rolled-back transaction around each test against a real database**,
  so `-caps` tests could stay parallel. It fails for code that commits its
  own `transaction` blocks, and for retry (ADR 0088), which needs a real
  commit to fail; running those tests one at a time works for all of them.
- **`expect a == b` on any two values, `Result` included.** It reads
  best, and Rust, Elixir, and Gleam allow it. But ADR 0052 keeps `Result`
  out of `==` everywhere so that an error is handled rather than compared,
  and making tests an exception would give `==` two meanings.
- **Literal patterns** (`expect result is Err(RatingError.Invalid("score",
  _))`), checking shape and value on one line. They'd change `match`
  everywhere: exhaustiveness with literal arms, float patterns (ADR 0048),
  decimal scale (ADR 0064), unnormalized strings (ADR 0067). That's a
  language change for its own ADR (open question 6); if it's accepted,
  `is` takes literal patterns with no change here.
- **A mocking library with expectations** (`expect(LedgerApi.record).once()`).
  `calls(...)` and `expect` inside the fake say the same without a second
  assertion language.

## Open questions

1. **Recording arguments.** `calls` counts; a test that wants every
   argument a fake saw stores them in its `Database`. A typed
   `received(LedgerApi.record)` list needs a type for an argument list,
   which fiig doesn't have.
2. **Postgres-only SQL** fails against the in-memory SQLite database; such
   a test needs `-caps` pointing at a real Postgres today.
3. **Testing workflows and adapters**, such as sending an HTTP request to
   an adapter in the same process.
4. **Snapshot tests, coverage, and benchmarks.**
5. **Shared fakes.** A fake used by many tests is repeated in each. A
   public `fake` declaration in `tests/`, used by name, would share it.
6. **Literal patterns.** `is` binds a value and `==` checks it, two lines
   where one would do. Literal patterns would make it one line, and would
   let program code `match` on values (`404 -> ...`, `200 -> ...`) instead
   of an `if` chain. They need their own ADR.
