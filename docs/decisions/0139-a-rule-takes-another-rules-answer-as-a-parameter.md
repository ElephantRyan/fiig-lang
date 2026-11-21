# ADR 0139: A Rule Takes Another Rule's Answer as a Parameter

Status: Accepted (2026-11-21; the owner approved it). Implemented 2026-11-21: B7's message and the `docs/layers.md` paragraph (Hearsay's move is Lane A's).
Date: 2026-11-21
Related: ADR 0029 (rules call nothing; conversions compose), ADR 0030
(activity), ADR 0049 (formulas; format checks vs policy checks), ADR 0083
(models declare no invariants), ADR 0129 (enum values), ADR 0137
(principal), Hearsay GAPS G58.

## Context

Hearsay has one policy that four places need: whether an Audiobookshelf
account may administer.

```fiig
// src/absclient/CanAdminister.fiig
rule canAdminister(type: AbsUserType) -> bool {
    match type {
        AbsUserType.Root -> true
        AbsUserType.Admin -> true
        AbsUserType.User -> false
        AbsUserType.Guest -> false
    }
}
```

Three callers are activities (`requireAdmin` twice, `runSetup`), and they
are fine. The fourth, `syncTargets` ("a person syncs their own list; an
admin may name anyone"), was a rule. The checker refused it:

```
[B7] rule may not call rule "client.canAdminister"; a rule calls only
formulas and methods, so make syncTargets a conversion, ...
```

So `syncTargets` became a `conversion`. G58 asks for one of two things: let
a rule call a rule, or give enums methods (`type.canAdminister()`).

Since then, F2 (`4e5ec74`) added a second suggestion to the message: make
the callee a formula if it is a small pure helper.

### What is actually wrong

Both suggestions in the message go against fiig's own rules for this case:

- **"Make it a conversion."** ADR 0029 gives conversions calls to rules so
  that they can *reshape data*, one level deeper in an object graph.
  `syncTargets` reshapes nothing. It combines two decisions: "is this person
  an admin" and "admin or self". ADR 0029 names exactly this ("composing
  business decisions") as a different kind of composition, and gives it to
  `activity`. The conversion label now says something false about the code.
- **"Make it a formula."** ADR 0049 says a formula is for a *format check*
  ("two companies would not disagree about it"), and that a judgment is a
  rule. Who may administer Hearsay is Hearsay's call. Another app on the
  same ABS server could give it to admins only, or to a named group.

So the language has no hole here. What went wrong is the advice. ADR 0029's
reasoning already contains an answer that the message never offers: the
decision `syncTargets` depends on comes in from whoever coordinates the
decisions.

```fiig
// src/sync/SyncTargets.fiig: a rule again
rule syncTargets(me: accounts.Identity, admin: bool, requested: string | None, everyone: List<string>) -> Result<List<string>, SyncError> {
    match requested {
        None -> {
            if not admin {
                return Ok([me.userId])
            }
            return Ok(everyone)
        }
        named -> {
            if named != me.userId and not admin {
                return Err(SyncError.NotAllowed)
            }
            return Ok([named])
        }
    }
}

// src/sync/LoadSyncPlans.fiig: the activity composes the two decisions
targets = syncTargets(me, admin: client.canAdminister(me.userType), requested: userId, everyone: everyoneToSync(rows, states))?
```

The test gets simpler too. It used to build an admin `Identity` and a
non-admin one to steer a decision that `syncTargets` doesn't own. It can now
say `admin: true` and `admin: false` directly. `canAdminister` is tested
once, in its own file.

Hearsay's corpus has this one case. Of its 73 conversions, two call a rule:
`syncTargets` (this case) and `absPathId`, which calls `validId` on its way
to building a path segment. That one *is* reshaping data, so it is a real
conversion.

## Precedent

- **Functional core, imperative shell** (Bernhardt, 2012), and the
  "decisions as data" style that grew from it: a pure function doesn't go
  looking for the facts it decides on. The shell gathers them and passes
  them in. This is ADR 0029's split under another name, with `activity` as
  the shell.
- **DMN (OMG Decision Model and Notation).** A decision may depend on
  another decision, but only through a declared *information requirement*
  in the decision requirements graph. A decision never calls another one
  from inside its own logic. The graph, not the decision table, holds the
  composition. That is the activity's role in fiig.
- **Rule engines (Drools, Jess).** Rules never call rules. One rule's
  conclusion becomes a fact, and other rules match on that fact. Chaining
  happens outside the rule bodies.
- **The DDD Specification pattern** (Evans and Fowler) is the opposite
  precedent: specifications compose freely (`and`, `or`, `not`). It is the
  strongest case for reversing ADR 0029. But it sits in languages with no
  separate coordinator, so composition must happen *somewhere* in the
  domain layer. fiig has one.
- **Enum methods** in Java, Kotlin, Swift and Rust (`impl AbsUserType`)
  would put `canAdminister` on the type. Those languages have no rule
  layer: a method on the type is where their domain logic lives.

## Decision

1. **No language change.** A rule still calls only formulas and methods
   (ADRs 0029, 0049). G58 is answered by the shape above: a rule that needs
   another rule's answer takes it as a parameter. The activity that calls
   the rule calls the other rule too, and passes the answer by name.
2. **B7's message for a rule calling a rule offers that shape first**,
   and offers "make it a conversion" only when the caller is reshaping data:

   ```
   [B7] rule may not call rule "client.canAdminister"; a rule doesn't
   combine decisions, so take its answer as a parameter (`admin: bool`)
   and call client.canAdminister from the activity that calls syncTargets
   (ADR 0029, ADR 0139). If syncTargets only reshapes data, it can be a
   conversion; if canAdminister is a format check, it can be a formula
   (ADR 0049).
   ```

   The parameter name in the hint comes from the callee's name with a
   leading `can`/`is`/`has` dropped, falling back to the callee's name.
   It's a hint, not a rename.
3. **`docs/layers.md`** gets one paragraph for this shape: "a rule that
   depends on another rule's answer".
4. Hearsay moves `syncTargets` back to a `rule` (Lane A).

## Rationale

The real cost G58 reports is one declaration moved to the wrong kind,
because the error message told it to. A parameter costs one argument at
the one call site. In return, the rule's signature says what it depends on,
and its test steers that dependency directly.

ADR 0029's argument still holds, and this case shows it. `syncTargets`
mixes two policies. Splitting them is what lets "who is an admin" change
(an ABS group, a Hearsay setting) without touching "admin or self".

## Alternatives considered

- **Let a rule call a rule (reverse ADR 0029).** It removes the message
  instead of fixing the advice. It also reopens what ADR 0029 closed:
  decisions composing inside rules leave `activity` without its job. Rules
  would also need their own cycle check (B9 covers activities only). The
  test cost moves the wrong way too: each rule's test must arrange its
  callees' inputs, which is the steering the parameter removes. One case in
  a 33-rule application doesn't justify it. Revisit if a real case comes up
  where the composed decision has no activity above it to compose it.
- **Enum methods (`type.canAdminister()`).** This puts policy into a type
  declaration, which ADR 0083 rejected for models, for the same reason: in
  fiig, business logic lives in rules. It would also give
  `AbsUserType`, a mirror of ABS's wire type in `absclient`, an opinion
  about Hearsay. Two apps on the same ABS couldn't disagree about it. And
  a method on a closed type is reachable from every rule, so the "rules
  don't compose decisions" line becomes "unless one of them is spelled as a
  method". ADR 0129's `values()` stays fine: it is data about the type, not
  a judgment.
- **Make the callee a formula.** Fine for a format check, and the message
  keeps suggesting it in that case. For `canAdminister` it would hide a
  policy in the tier that ADR 0049 keeps for things nobody disagrees about.
- **A rule may call rules in its own package only.** This makes the
  boundary a matter of where a file sits. It answers no different question
  from the full reversal.

## Consequences

- `compiler/checker/checker.go`: B7's rule-calls-rule message (above), and
  its test.
- `docs/layers.md`: the "a rule that depends on another rule's answer"
  paragraph.
- Hearsay: `syncTargets` is a `rule` taking `admin:`, `LoadSyncPlans`
  passes `client.canAdminister(me.userType)`, and its test passes
  `admin: true` or `admin: false`. G58 closes as "answered by ADR 0139".

## Open questions

- After ADR 0137, `requireAdmin` may become the principal binder for the
  admin routes. Then `admin` could come from the principal instead of a
  rule call in the activity. That doesn't change this decision, only where
  the answer comes from.

## Implementation note (2026-11-21)

B7's rule-calls-rule message reads as §2 gives it, and a rule calling a
conversion keeps its own message. The parameter hint follows §2's rule:
`canAdminister` gives `administer`, not the `admin` of §2's example. That
example is the parameter Hearsay chose, which a hint can't guess.
