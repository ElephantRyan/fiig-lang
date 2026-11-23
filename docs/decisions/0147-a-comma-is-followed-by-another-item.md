# ADR 0147: A Comma Is Followed by Another Item

Status: Accepted (2026-11-23, by the owner). Implemented the same day.
Date: 2026-11-23
Amends: ADR 0145 §2 and §3 (the optional comma after the last
alternative, and in delimited lists).
Related: ADR 0057 (list literals), ADR 0142 §1 (`;` ends a statement).

## Context

ADR 0145 allowed a comma after the last item of every comma list, and
`fiig fmt` wrote one whenever it put the items one per line:

```fiig
return match (liveSession(found)) {
    None -> Err(IdentityError.NotSignedIn),
    identity -> Ok(identity),
}

return http.Request(
    method: http.Method.Get,
    url: url,
    timeoutMillis: 10000,
);
```

That comma was never weighed on its own. ADR 0145 took "a trailing comma
is allowed" from the lists that already had it, and the formatter's
one-per-line layout from Go and Rust, together.

The owner rejected it on 2026-11-23. A comma reads as "more follows", and
after the last item nothing does.

### Precedent

| | Trailing comma | Where |
| --- | --- | --- |
| Go | **required** when the closing bracket is on its own line | composite literals, calls, parameters |
| Rust, Kotlin, Swift, Python, JavaScript | allowed; formatters add it on one-per-line lists | everywhere |
| Java | allowed in array initializers and enum constants, **an error** in calls and parameters | |
| JSON | **an error** | arrays, objects |
| Java `switch`, Kotlin `when`, Elixir `case` | no comma between arms at all | |

The argument for allowing it is the diff: adding an item at the end
changes one line, not two, and reordering items is a plain line move. Go
makes it required for that reason, so a newline can't end a list
(semicolon insertion would otherwise break it).

fiig doesn't need it for parsing: ADR 0142 §1 ends statements with `;`,
so a newline never ends anything. That leaves the diff argument against
reading the code, and fiig is read more often than its lists are
extended.

## Decision

### 1. A comma before a closing bracket is a syntax error

In every comma list (arguments, parameters, list and map literals, type
arguments, binary patterns and expressions, enum variants, match arms,
response arms, and stream, schedule, and startup dispositions), a `,`
whose next token is `)`, `]`, `}`, or `>` is an error at the comma:

```
1:14: a comma is followed by another item, and nothing follows this one before `)`; remove it (ADR 0147)
```

The rule is on the tokens, not on each list's grammar, so no list can be
an exception, and one list added later can't forget it. The parse goes on
as if the comma weren't there, so there is one error per comma and no
cascade.

### 2. `fiig fmt` removes it

The formatter accepts a file whose only errors are these and writes it
without them, as it already formats a file with a second public
declaration (G1). `fiig fmt -w` is the migration.

### 3. One per line, nothing after the last

```fiig
return match (liveSession(found)) {
    None -> Err(IdentityError.NotSignedIn),
    identity -> Ok(identity)
}

return http.Request(
    method: http.Method.Get,
    url: url,
    timeoutMillis: 10000
);
```

A block arm that isn't last still takes its `,` after its `}` (ADR 0145
§2). A comma between items stays required.

## Alternatives considered

**A. Optional, with the formatter leaving it out.** Keeps old code
valid, and is a formatter change only. Rejected: the owner doesn't think
the language should allow it, and two spellings of one list is what ADR
0145 rejected for `;` before a closing `}`.

**B. Only alternatives (arms and variants), not delimited lists.** The
owner chose every list: a multi-line call's last `,` reads the same as an
arm's.

**C. No commas between arms at all, as Java and Kotlin.** Rejected: ADR
0145 added them so that two lines can't silently merge into one
alternative.

## Consequences

- Adding an item at the end of a one-per-line list changes the line above
  it too, to gain a comma. That is the cost, in diffs, of the rule.
- Migrated on 2026-11-23: about 1,400 commas in this repository, across `.fiig`
  files (the stdlib, examples, and test data), fiig source embedded in Go
  tests, and fiig code in `docs/` outside `docs/decisions`. Earlier ADRs keep
  their examples as written. Hearsay and abs-recommendations are separate
  repositories and need `fiig fmt -w`.
- JSON and other non-fiig test fixtures that use a trailing comma on
  purpose (to test that JSON refuses one) are unchanged.
