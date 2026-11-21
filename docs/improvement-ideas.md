# Improvement ideas

A log of improvements found by a sweep of the language on 2026-11-21: a dozen
probe programs run against the toolchain, then each open question checked
against the ADRs. An idea here is not a decision. Each has the evidence it
came from, a size, and what it would need, so the roadmap can order them.

Sizes: **S** a day or less, **M** a few days, **L** a week or more.
"ADR" means it needs a decision before it is built.

## What the sweep found

### Probes that ran (`fiig check` and `fiig serve`, 2026-11-21)

| Tried | Result |
| --- | --- |
| `break`, `continue`, `while`; `a or b` on an optional; `"a" < "b"`; `list.join`; `"""` strings | work |
| `padLeft` / `padRight` on a string | no such method |
| `Set<string>` | no such type |
| list `distinct`, `reverse`, `take`, `indexOf`; `zip` | none exist |
| listing an enum's variants (`Level.values()`) | no such operation |
| a map literal `{"a": 1}` | a 7-line parse cascade that never says maps have no literal |
| a public rule calling itself | refused (B7), by design: see question 1 |
| a 12-package app with errors in several | `fiig check` reports one failing package per run |
| a route whose workflow faults, under `fiig serve` | the client gets `{"fault":"division by zero (in boom)"}` and **the server log has no line about it** |

The last row is the most important finding, and was not among the questions.

### The eight questions, checked against the ADRs

1. **Is recursion out? No.** ADR 0054 §6 allows recursion in a **private
   helper**, for every kind, with a call-depth fault at 1,000 frames. A public
   rule can't call itself (B7), so a rule that needs recursion puts it in a
   helper. My probe tested the wrong thing. What is missing is the message:
   B7 says "rule may not call rule" and doesn't say where the recursion can
   go (idea B2).
2. **Literal patterns.** Undecided, and the open question is recorded (ADR
   0091 open question 6, `tour.md` known gaps, ADR 0058). The ADR 0091 note
   lists the four things a literal pattern touches: exhaustiveness, floats
   (ADR 0048), decimal scale (ADR 0064), unnormalized strings (ADR 0067).
   Restricting the feature to `int`, `string` and `bool` meets three of the
   four by leaving the awkward types out, and the fourth by requiring a
   catch-all arm (idea B1).
3. **`n: int = 0` as the mark of a mutable local.** Decided: ADR 0053 §2
   records `var` as "rejected by the user". No action.
4. **Moment and span both `int`.** Decided with a trigger: ADR 0123's
   alternatives call a `time` primitive "the right answer if a real program
   reaches the boundary and finds `int` wrong there", and note that
   abs-recommendations hasn't. No action until a program does (idea B6 is to
   collect that evidence).
5. **Observability.** No ADR covers it. What exists: a `Tracer` that records
   every call as a span (id, parent, kind, outcome, duration, detail), used
   only by `fiig run -trace`. `fiig serve` doesn't set it, `compiler/adapter`
   has no log calls, and ADR 0026's "Known issue (2026-10-29)" says the HTTP
   adapter sends `fault.String()` to the client and was "Unfixed". It still
   is (ideas A1 to A4).
6. **Built-in health routes.** Mostly answered by the design. `fiig serve`
   opens its listeners after the startup routes pass (ADR 0124 §2), so a
   process that is listening is ready, and a TCP or HTTP probe on any route
   works. What an app can't do is report which routes started degraded;
   ADR 0127 gives it that through config. A built-in status route is a
   convenience, not a need (idea A5).
7. **Server push.** No ADR mentions server-sent events or WebSockets. A route
   answers once, with a value (ADR 0044), and there is no stream the other
   way. It is a design question and not a gap (idea E2).
8. **JWT verification.** ADR 0075 limits crypto to SHA-256 HMAC, Argon2id and
   SHA-256, and records SHA-1/SHA-512 (open question 1) and public-key
   signatures as not built. The precedent for adding more is ADR 0116:
   compression went into a prelude group in Go, with the owner's reason being
   speed. `crypto` is already such a group (ideas D1, D2).

## Ideas

### A. Operating a program

| # | Idea | Size | Needs |
| --- | --- | --- | --- |
| A1 | **Stop sending faults to the client.** A fault answers a bare `500` with a request id; the full fault, with its declaration stack, goes to the server log. It closes ADR 0026's known issue: the body names internal declarations and can name data (a duplicate key). | S | amend ADR 0026 |
| A2 | **A log line per request and per fault**: method, path, status, duration, request id. Accept an incoming `X-Request-Id` or make one, and return it. A flag turns the access line off. | S | ADR (what is logged) |
| A3 | **`fiig serve` records spans** with the existing `Tracer`, and logs the span tree of a request that faulted. | S | with A2 |
| A4 | **Metrics from the spans.** Counts and durations by declaration, kind and outcome, plus startup, schedule and stream state, as Prometheus text on a separate port. No program change, since the spans already carry it. | M | ADR |
| A5 | **A status route** `fiig serve` adds: version, startup routes that passed or degraded, the loops that are running. | S | ADR (exposure, naming) |

**Status (2026-11-21): A1 and A2 are done (ADR 0128, Proposed).** A3 is
partly done: the log carries the fault's declaration stack, and the span tree
waits on a tracer that can be scoped to one request (ADR 0128 open question
1). A4 and A5 hang on the same `Observer`.

### B. The language

| # | Idea | Size | Needs |
| --- | --- | --- | --- |
| B1 | **Literal patterns** for `int`, `string` and `bool`, in `match` and `is`. A match with a literal arm needs a catch-all arm (`_` or a name). Floats and decimals are refused with a message that says to compare. `404 -> ...` replaces an `if` chain, and `expect r is Err(Invalid("score", _))` is one line. | M | ADR (ADR 0091 open question 6) |
| B2 | **B7 and B3 say where recursion goes**: "put the recursion in a private helper (ADR 0054 §6)". | S | none |
| B3 | **A table of things fiig leaves out, with a one-line message each**: a map literal, `if` as an expression, a ternary, a lambda, `++`, `&&` and `\|\|`. One error, naming what to use. The cascades (GAPS G1, G9, G10, G33, and the map literal above) are the commonest bad message, and an LLM reads each line literally. | S | none |
| B4 | **`fiig check` reports every failing package** in one run. | S | none |
| B5 | **A batch of small methods**: string `padLeft`, `padRight`; list `distinct`, `reverse`, `take`, `drop`, `indexOf`; a way to list an enum's variants. Each is a table row and a runtime body, as ADR 0093 did. `Set` and `zip` need a type (a set, a pair) and are their own questions. | M | ADR (one, listing them) |
| B6 | **Collect time evidence**: when a real program mixes a moment and a span, or a unit, record it against ADR 0123's trigger. | S | none |

### C. Data

| # | Idea | Size | Needs |
| --- | --- | --- | --- |
| C1 | **Check queries against the schema.** Migrations are plain SQL files (ADR 0096) and every query is a literal (Q1), and a test already applies a package's migrations to an in-memory SQLite database. `fiig check -schema` would apply them there and prepare each query, finding a misspelt column or table before run time. Postgres-only SQL would use `-caps`, as `fiig test` does. ADR 0096's consequences say this "is its own ADR". | M | ADR |
| C2 | **Data-dependent SQL by enum.** An enum whose variants carry constant SQL text (ADR 0046 §4a) can be spliced into a query, since only its constants can appear, so a dynamic `ORDER BY` needs no builder and stays injection-safe. ADR 0082 open question 1 names this candidate. | M | ADR |

### D. Security

| # | Idea | Size | Needs |
| --- | --- | --- | --- |
| D1 | **More `crypto`, then `fiig.jwt`.** HMAC with SHA-1 and SHA-512 (ADR 0075 open question 1), then RSA, ECDSA and Ed25519 verification, AES-GCM and HKDF, as members of the prelude group, in Go. A `fiig.jwt` library in fiig over them and `fiig.http`, with a JWKS fetch, would verify an OIDC id token. | L | ADR |
| D2 | **A typed digest that refuses `==`**, so ADR 0075 §3's timing rule is a check (open question 2). | M | ADR |

### E. Delivery and reach

| # | Idea | Size | Needs |
| --- | --- | --- | --- |
| E1 | **`fiig build`**: one artifact holding the program and the toolchain, with a checksum, in place of running the interpreter on source. Not investigated: I haven't read how the alpha image is made. | ? | discussion |
| E2 | **Server push.** Decide it as a non-goal for now, with long polling as the documented answer, or design an outbound stream adapter. | ADR | discussion |

### Not new: already tracked elsewhere

- A request-body `Secret`, and a database-backed config source (ADR 0126,
  GAPS G52).
- A deploy id a platform supplies, and pruning `fiig_locks` (ADR 0124, ADR
  0125).
- Time zones and pattern strings (ADR 0123 open questions).
