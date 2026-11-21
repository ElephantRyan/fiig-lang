# Bug-class catalog

Status: Milestone 0 complete (23/25 attested at close); 26/28 now, as
Milestone 2+ work adds entries beyond Milestone 0's original pass — catalog
stays open as new bug classes are identified in later milestones

Delivers Milestone 0's last outstanding item (`fiig-lang.md` §5: "A catalog
of bug classes the compiler must reject"). Built by walking every "must/
cannot/may not/prohibited" statement in `fiig-lang.md` §3 and Milestone 2's
own "Programs that must fail" list (`fiig-lang.md:298-307`), not just the
ten required examples. Each entry is marked **attested** (an
`invalid-programs/*.fiig` file shows it) or **deferred** (asserted by a
design principle, but no example exists yet, for a stated reason).

Milestone 2's exit criteria ("the checker rejects every invalid example with
one focused diagnostic") already commits to catching the type/effect/
transaction/concurrency categories at compile time — transaction-scope checking
and effect propagation are Milestone 2 deliverables, not deferred to
runtime. Retry legality is grouped here too even though it depends on a
capability's declared idempotency (Milestone 5-era metadata) because
Milestone 0's required example 10 explicitly groups it with the others.

## A. Type errors

| # | Bug class | Enforced by | Status |
|---|---|---|---|
| A1 | Passing a value of one type where another is required (e.g. an `int` to `getCustomer`, or a `Customer` where a `string` is expected); models are nominal, and there are no implicit conversions | §3.3 nominal typing | Attested — `type_error.fiig` (`lookupWrongId`). Also attested by `retrieveCustomersWrongError`, once concurrency result typing (`fiig-lang.md` line 361, `checker.inferConcurrent`) started inferring a `concurrent` block's real `BatchResult<Key, Success, Failure>` instead of leaving it `Unknown` — the same A1 check, just reachable in a shape it couldn't see before. Two numeric cases joined with `float` (ADR 0048): arithmetic mixing an `int` and a `float` (ADR 0019's addendum; `invalid-programs/float_error.fiig`), and a fractional constant landing as an `int`, such as `return 2.5` from an `int` rule (§2.4). Both are in `compiler/checker`'s `float_test.go`. A third joined with the prelude `math` group (ADR 0049 §2.4): a call matching none of a member's forms, such as `math.max(count, ratio)` with an `int` and a `float`, or `math.sqrt(n)` with an `int` (`invalid-programs/math_error.fiig`; `math_test.go`). Two more joined with `decimal` (ADR 0064): a constant with no exact decimal value landing as one (`1.0 / 3.0`, `math.pi`), and `/` on two decimals, which names `math.divide` (`invalid-programs/decimal_error.fiig`; `checker/decimal_test.go`). |
| A2 | Returning `None` where a non-optional type is required | §3.4 | Attested — `type_error.fiig` (`returnAbsentCustomer`) |
| A3 | Omitting a required model field | §3.5 | Attested — `type_error.fiig` (`buildIncompleteCustomer`) |
| A4 | Relying on an implicit conversion between two distinct types | §3.3 "no implicit conversions" | Attested — `type_error.fiig` (`useEmailAsName`) |
| A5 | Using `Any` or an unchecked cast | §3.3 | Deferred — the language has no surface syntax for a cast to misuse, so this is currently unrepresentable by construction rather than a checked rejection. Revisit only if a future milestone adds cast syntax. |
| A6 | Two explicitly-named `conversion` declarations collide (same declared name, or otherwise indistinguishable canonical identity) | §3.3, ADR 0004 | Attested — `type_error.fiig` (two `orderToSummary` conversions). ADR 0006 already settled that error propagation never auto-selects a conversion by type, so the broader "ambiguous automatic selection" risk this entry used to name no longer applies anywhere in the language; what's checked now is the narrower remainder (a plain naming collision between two conversions). Built on the same cross-file duplicate-name detection every declaration kind already gets (`types.BuildUniverse`), tagged A6 specifically when both colliding declarations are conversions — any other kind of name collision stays untagged infrastructure, since the catalog has no numbered entry for those. |
| A7 | Passing undecoded external/untyped data (e.g. raw HTTP body) directly into domain code | §3.3 "external untyped data must be decoded before entering domain code" | Attested — `type_error.fiig` (`customerEmailApiBroken`). `docs/decisions/0017-adapter-response-mapping.md` already confirmed path-segment binding into a `value` type is decoding, by construction; `docs/decisions/0021-adapter-request-binding.md` settled the request-body side by applying the same reasoning in reverse — a primitive type has no fields to decode a body against, so only a `model` is legal there. ADR 0069 §5 narrows it to bodies read as JSON: a route that `consumes` any other type (XML, CSV) binds the body raw, as `string` or `bytes`, for a rule to parse, since there is nothing for the adapter to decode it into; binding such a body to a model is A7 the other way round. ADR 0077 adds `bytes` on a JSON route, the body as it arrived for a signature check before `json.decode`; `string` there stays A7, since it would hand JSON around as text. Checked by `checker.checkBodyBinding`. |
| A8 | Using a structurally-typed anonymous record where a public declaration requires a named type | §3.3 (structural typing is "anonymous local" only) | Attested — `type_error.fiig` (`summarizeOrderShape`) |
| A9 | A `match` doesn't cover every case of a type the checker can fully enumerate — a missing `Result` arm (`Ok`/`Err`), a missing `enum` variant (a typed error's or any other enum's, ADR 0040 — the check is the same either way), or (for a `T \| None` union) a missing `None` arm | `fiig-lang.md` line 286 ("Exhaustive pattern matching," a Milestone 2 deliverable in its own right — not a §3 "must/cannot" statement like every other entry here, since exhaustiveness is an inherent property of having sum types at all rather than a rule someone could violate by writing an escape hatch) | Attested — `type_error.fiig` (`describeCustomerError`, missing `CustomerError.Invalid`; `describeRegion`, missing `Region.APAC` — a non-error enum, added once ADR 0040 generalized `error` to `enum`). A bare binding name (`found`) or `_` is a catch-all covering every case *except* against a `T \| None` union, where grammar.md §5 documents it as covering only the non-None remainder — `None` still needs its own arm there, the one case this check treats specially. Checked by `checker.checkExhaustive`; since ADR 0082 typed `Database` queries the corpus resolves real `T \| None` matches (`get_customer.fiig`, `find_session.fiig`), whose `None` arms the check sees; a missing one is covered by `compiler/checker/checker_test.go`'s `TestExhaustiveMatchNoneUnion`, not an `invalid-programs` fixture. |
| A10 | A model's embedded models promote the same field name at the same depth (or the same model is embedded twice), and no explicit field of the outer model shadows it (ADR 0045 — reported at the embedding model's declaration whether or not the name is ever read, unlike Go, which errors only on use) | Attested — `compiler/checker`'s `TestEmbeddingShadowingAndSameDepthCollision`. No corpus model embeds anything (ADR 0045: additive, no migration), so there is no `invalid-programs` fixture. |
| A12 | An empty list literal `[]`, or an empty map literal `[:]` (ADR 0065 §5), with no expected type to give it an element type (ADR 0057 §2); likewise `json.decode` (ADR 0073 §2) and a `Database.queryOne`/`queryMany` with no row type to read into (ADR 0082 §5) | Attested — `compiler/checker`'s `TestEmptyListTakesItsTypeFromContext` and `TestMapMisuse`; `database_error.fiig` (`readLedgers`). Fix: a typed declaration, `names: List<string> = []`, `row: Account \| None = Database.queryOne(...)` |
| A18 | A map literal repeats a constant key (ADR 0065 §5): two keys the compiler can evaluate are equal, compared by value, so `1.5` and `1.50` repeat. A literal lists distinct keys; keeping the last, as Python and JavaScript do, loses one silently. A repeated key only known at run time is a fault | Attested — `compiler/checker`'s `TestMapMisuse`; the fault by `compiler/runtime`'s `TestMapPrograms` |
| A13 | The result of `append` is discarded (ADR 0057 §3): `append` returns a new list and leaves its argument unchanged, so a bare `append(xs, x)` statement does nothing. Go makes the same call a compile error. Widened by ADR 0065 §6 to every closed-table method (`xs.sorted()`, `name.isBlank()`), all of which are pure | Attested — `compiler/checker`'s `TestDiscardedAppendIsA13` and `TestDiscardedMethodResultIsA13` |
| A14 | A binary segment the compiler can reject before running (ADR 0058): an unknown or ill-formed encoding (`u32` without a byte order, `u8be`, `u64`, a signed sub-byte field), a `bytes`/`utf8` segment or the end of the segments not on a byte boundary, sizeless `bytes` before the last segment, `utf8` without a size in a pattern, a size using anything but literals and earlier segment names, a name bound twice, or a literal that cannot fit its width (`300: u8`) | Attested — `compiler/checker`'s `TestSegmentErrorsAreA14`, one source per rule. A match using a binary pattern with no catch-all arm is A9, since no set of binary patterns covers every `bytes` value |
| A15 | A headed enum's variant does not supply exactly its header's fields as constants (ADR 0046 §4, §4a, §6): a field missing, unknown, supplied twice, or positional; a value that is a call, arithmetic, or an interpolated string; a payload (`A(id: string)`) on a headed enum; or a `key` field whose type is not `string`, `int`, or a list of one | Attested — `compiler/checker`'s `TestHeaderConstantErrorsAreA15`, one source per rule |
| A16 | Two variants of a headed enum share a value in a `key` field, or share any element of a list-typed `key` field, so the derived lookup could not tell them apart (ADR 0046 §5, §7). Non-key fields may repeat | Attested — `compiler/checker`'s `TestDuplicateKeyValuesAreA16` |
| A17 | A key lookup that is not exactly one named argument naming a `key` field (`DeductionSource(layout: x)`, `DeductionSource(raw)`), or a lookup on an enum with no header (ADR 0046 §5) | Attested — `compiler/checker`'s `TestKeyLookupMisuseIsA17` |
| A11 | A model embeds itself, directly or through other models (ADR 0045 — a structural cycle on types, distinct from activity call cycles, B9) | Attested — `compiler/checker`'s `TestEmbeddingCycleIsA11AtEveryMember`, reported at every model on the cycle |

## B. Effect errors (call-graph violations)

The composition matrix (ADR 0029/0031/0044/0049): Formula→Formula only;
Rule→Formula only; Conversion→Rule/Conversion; Activity→Rule/Conversion/
Gateway/Activity; Gateway→Rule/Conversion only (never another Gateway);
Workflow→Rule/Conversion/Activity/Gateway (nothing may call a Workflow);
Adapter→Workflow only. Every kind may also call a formula.

| # | Bug class | Status |
|---|---|---|
| B1 | A rule calls a gateway. A `sorted(x in xs): key` key is held to a rule's calls in every kind of declaration (ADR 0065 §3), so a key that calls a gateway is B1 with its own message | Attested — `effect_error.fiig` (`summarizeCustomerName`); the key case by `TestSortedFormMisuse` |
| B2 | A rule calls a workflow | Attested — `effect_error.fiig` (`summarizeViaWorkflow`) |
| B3 | A gateway calls another gateway directly ("gateways may not coordinate other application gateways," §3.2 — called out as its own bullet, separate from the general matrix, suggesting it's an easy mistake worth its own diagnostic) | Attested — `effect_error.fiig` (`insertAndNotify`) |
| B4 | A gateway calls a workflow of its own package, or another package's workflow without requiring that package (ADR 0079: a workflow is called only from outside its package, by an adapter or by a gateway that `requires` the package) | Attested — `effect_error.fiig` (`notifyViaWorkflow`); the cross-package case by `compiler/checker`'s `TestForeignWorkflowNeedsTheGatewaysRequires` |
| B5 | A conversion calls a gateway, a workflow, or an activity | Attested — `effect_error.fiig` (`customerToSummaryWithLookup`) |
| B6 | An adapter calls something other than an exported workflow (e.g. a gateway directly) | Attested — `effect_error.fiig` (`customerHttpApiBroken`) |
| B7 | A rule calls another rule or a conversion (ADR 0029 — rules call nothing that carries judgment or effects, since ADR 0049 narrowed "nothing at all" to admit formulas; that composition is an `Activity`'s job) | Attested — `effect_error.fiig` (`computeDiscount`) |
| B8 | An activity or workflow calls a workflow (ADR 0044 — a workflow is the adapter boundary, called only by an adapter or, from another package, by a gateway that requires that package (ADR 0079); this replaces ADR 0030's "an activity calls a gateway, another activity, or a workflow", which ADR 0044 made legal for the first two) | Attested — `effect_error.fiig` (`lookupViaWorkflow`). A workflow calling a workflow is covered by `compiler/checker`'s `TestWorkflowIsUncallableAndOnlyAdapterBoundary`. A *test* may call a workflow of any package of its own module (ADR 0102 §3a, 0108 §2a), and is refused only for another module's (`TestATestMayCallASiblingsWorkflow`). |
| B9 | An activity is part of a call cycle through activities in other files (ADR 0044 — activities may now call each other, so recursion is expressible; the first call-graph cycle check in fiig, since rules call nothing and conversion recursion is structural). Since ADR 0054 §6 an activity calling itself, directly or through its own private helpers, is allowed and bounded by the runtime's call depth limit; only a cycle that leaves the file is B9 | Attested — `effect_error.fiig` (`pingCustomer`/`pongCustomer`, reported at both); `TestActivityCycleThroughHelpersIsStillB9` for a cycle through a helper |
| B10 | A rule, conversion, activity or workflow calls a primitive capability directly (`Socket.connect(...)`). Only a gateway touches a capability (§3.1), and since ADR 0060 typed `Socket`'s operations the checker can see the call; before that, a direct capability call from any kind was unmodeled and passed silently. ADR 0068 §4 splits the primitives: `Clock` and `Random` are local effects, which an activity or a gateway calls directly, so for them B10 is a rule, conversion, formula, or workflow calling one (`Random.int(...)` in a rule; the rule takes the draw as a parameter). ADR 0071 §2 widens it to reading config (`Sessions.maxAge`) anywhere but an activity or a gateway. ADR 0082 §2 widens it to every capability with no declaration in fiig (`Database`, `EmailSender`), which before took an unchecked "opaque capability call" path anywhere | Attested — `socket_error.fiig` (`peekAtDatabase`), `database_error.fiig` (`purgeSessions`), `TestNativeCapabilitiesOutsideGatewaysAreB10`, `random_error.fiig` (`rollDie`); `TestSocketOutsideItsGatewayIsB10OrP7` and `TestLocalEffectsOutsideTheShellAreB10` for every kind; `config_error.fiig` (`retryLimit`), `TestConfigOutsideActivitiesAndGatewaysIsB10` |
| B11 | A formula calls anything other than a formula: a rule, conversion, activity, gateway, workflow, or capability group (ADR 0049 §2.2 restriction 2). A formula is the bottom of the composition lattice, so it can't become a channel for reaching something a rule is forbidden to reach. A primitive capability call from a formula is B10, like any non-gateway | Attested — `formula_error.fiig` (`brokenChecks.shipsFree`); `compiler/checker`'s `TestFormulaCallsOnlyFormulasB11` for a capability group |
| B12 | A workflow calls a gateway directly (ADR 0107). A workflow decides what a step's result means; the step itself is an activity, so the workflow doesn't depend on how it's done, and a shared step can move without touching it | Attested — `compiler/checker`'s `TestWorkflowMayNotCallAGateway` |

## C. Transaction errors

C3 is intentionally absent, not skipped by accident — it was dropped
outright, not deferred. See "Explicitly out of scope" below for why.

| # | Bug class | Status |
|---|---|---|
| C1 | A network-capability gateway is called inside a `transaction` block | Attested — `transaction_error.fiig` (`reserveAndCharge`) |
| C2 | A `transaction` block nests inside another | Attested — `transaction_error.fiig` (`doubleReserve`) |
| C4 | A gateway whose capability doesn't match the `transaction` block's named capability is called inside it (the general case C1 is one instance of — any gateway from a second capability, not just a network one) | §3.8 "one transaction block controls exactly one transactional capability" — Attested — `transaction_error.fiig` (`createAndEmailInTransaction`) |
| C5 | A workflow or activity is called from inside a `transaction` block | §3.8 "general workflow calls are ... prohibited inside transaction blocks" — distinct from C1 (gateways). ADR 0044 made a workflow call illegal everywhere (B8), so the activity case is what keeps this class independently attested; an activity may *contain* a transaction block, so letting one be *called* inside another would nest one — Attested — `transaction_error.fiig` (`changeEmailAndReread`, calling `summarizeCustomer`) |

## D. Concurrency errors

| # | Bug class | Status |
|---|---|---|
| D1 | A `concurrent` block requests `limit: unbounded` (or otherwise opts out of bounding) | Attested — `concurrency_error.fiig` (`retrieveAllCustomers`) |
| D2 | A nested `concurrent` block requests a limit that could multiply past its parent's budget | §3.7 "nested concurrency shares hierarchical budgets and cannot multiply without bound" — Attested — `concurrency_error.fiig` (`retrieveCustomersNested`) |

## E. Retry errors

| # | Bug class | Status |
|---|---|---|
| E1 | `retry(...)` wraps a gateway not declared idempotent | Attested — `retry_error.fiig` |

## F. Error-handling errors

| # | Bug class | Status |
|---|---|---|
| F1 | A typed error (`Result`'s `Err` case) is left unhandled — no `?`, no `match`, silently dropped | Milestone 2 list item — Attested — `error_handling_error.fiig`. Originally only checked a bare top-level statement; a dropped `Result` inside a non-producing `match` arm's bare-expression body escaped it entirely until the LLM composition trial (`docs/specification/llm-composition-trial.md`) found the gap by accident — a naive but plausible composition passed the checker only because of it. Fixed in `checkUnhandledResultExpr`, both call sites now share one check. **Known gap (found 2026-10-27, ADR 0052 review):** a `Result` bound to a name and never matched or propagated (`r = parse(s)` with `r` unused) still passes the checker; F1 sees only a bare statement. Unfixed. |

## G. File and import errors

| # | Bug class | Status |
|---|---|---|
| G1 | More than one primary semantic declaration in a single file | §3.9 | Attested — `module_structure_error.fiig`. Since ADR 0054 a parse error, reported at each extra declaration (the grammar allows one public declaration and at most one `private` block). Parse errors carry the G1 tag, so the checker's per-file counts still see it. |
| G2 | A wildcard import | §3.10 "unqualified wildcard imports should be prohibited"; a parse error since ADR 0050 §2.4, which removed `.*` from the grammar | Attested — `wildcard_import_error.fiig`; `compiler/parser`'s `TestWildcardImportIsAParseError` |
| G3 | Two imports in one file bound to the same alias | §3.10 — a qualified reference has to name exactly one package | Attested — `compiler/checker`'s `TestDuplicateImportAliasIsG3`. Found 2026-10-12 by asking how `createCustomer` resolves when two declarations claim it, and whether imported packages could collide the same way. They could, silently: the second import overwrote the first in `ResolveFileImports`, and the only symptom was an `unresolved type` on whichever package lost — a diagnostic pointing at code that was correct. Both forms count, and the implicit one is the dangerous half: `use company.time` and `use standard.time` both default their alias to `time` without either line mentioning the name they collide on. Checked in `CheckFile` next to G2 rather than where the overwrite happens, since `ResolveFileImports` is also the runtime's and returns early with no package context. |
| G4 | An import alias shares a name with something else in its file's scope: a parameter or binding of a declaration in that file, a declaration in the package, or (ADR 0043 §6b) a capitalized binding sharing a declaration's name | ADR 0043 §3a — once a capability group is a value (`return Stripe`), a bare name must mean exactly one thing | Attested — `compiler/checker`'s `TestAliasCollisionsAreG4`. A collision never resolves by shadowing in either direction; the fix is always `use path as OtherName`. An alias meeting a local in the same file is reported at both sites; an alias meeting a declaration in another file is reported at the import, naming the declaration's kind. Fires on every import, provider or not: whether an import is a provider is a manifest question, and a naming rule shouldn't need the manifest. Without package context (the whole corpus checked as one universe) an unresolved group import is not compared against a same-named group, since it may be that very group. The prelude `math` group (ADR 0049 §2.5) is the one name every file sees without a `use`, so an import alias, parameter, binding, or loop or pattern variable named `math` is G4 too (`TestMathNameIsReserved`). |

## N. Naming errors (ADR 0043 §5c, §6)

Case in fiig is load-bearing where a reader relies on it: a lowercase dotted prefix never reaches the outside world, `Upper.lower(...)` may, and `Upper.Upper` is an enum variant. These keep that true. (ADR 0050's L1 named the first of them; that ID is taken by `break`/`continue` outside a loop, ADR 0053, so the case rules are numbered here.)

| # | Bug class | Status |
|---|---|---|
| N1 | A name's case contradicts what it names: a capability group or interface not capitalized, a capability-group alias not keeping its group's case (`use acme.http.Client as client`), or a package alias capitalized (`use acme.http as Http` — a package is never itself a capability) | Attested — `capability_error.fiig` (`refundDesk`); `compiler/checker`'s `TestCapabilityGroupsAcrossPackages` for both alias forms |
| N2 | A member's case contradicts its kind: an enum variant not capitalized, or a capability operation or interface member capitalized | Attested — `capability_error.fiig` (`refundDesk.Issue`); `TestCaseRulesAreN1N2N3` for the enum form. The corpus already conformed. |
| N3 | A binding holding a capability (an interface, a capability group, or a list of either) is not capitalized, or a binding holding anything else is | Attested — `capability_error.fiig` (`refundLowercase`); `TestCaseRulesAreN1N2N3` for the second direction. A capitalized binding whose type the checker can't model (an opaque capability result) is left alone. |

## M. Comparison errors (ADR 0052)

`==` and `!=` take two operands of one type, and ordering takes `int` or `string` (and `float`, once ADR 0048 is implemented). These were unchecked until 2026-10-27: `1 == "hello"` type-checked and evaluated to a silent `false`, and ordering a model faulted at runtime.

| # | Bug class | Status |
|---|---|---|
| M1 | A comparison on an operand that is not comparable: one holding a `Connection` at any depth (ADR 0060), a capability value (an interface-typed provider or a capability group; ADR 0052, 2026-10-27), a `Result` at any depth (ADR 0052, 2026-10-27), or, under `==` and `!=`, a `float` at any depth (ADR 0051 §2.3, ADR 0048), with the diagnostic naming the path to it (`Order.total.amount is float`); `==` on a `List<float>` names `math.allClose`. `List.contains` compares with `==`, so the same element types are M1 there, with `math.containsClose` named for `List<float>` (ADR 0065 §1) | Attested for handles and `Result` — `compiler/checker`'s `TestHandlesAreNotComparableM1`, `TestConnectionIsNotComparable`, and `TestResultsAreNotComparableM1`. Both walks are H5's (`types.Holds`, via `HoldsConnection` and `HoldsResult`). The float half is attested by `invalid-programs/float_error.fiig` and `TestFloatEqualityIsM1`, through models, lists, unions, and enum payloads; its walk (`checker.floatPath`) also enters enum payloads, which `types.Holds` doesn't. A `float` is still ordered (M3). |
| M2 | `==`, `!=`, or an ordering operator on operands of different types | Attested — `compiler/checker`'s `TestComparisonOperandsAreM2AndM3`. One side being assignable to the other counts as the same type, so `x == None` stays legal for `x: T \| None`. Corpus migration was zero. |
| M3 | `<`, `<=`, `>`, `>=` on an operand that is not `int`, `float` (ADR 0048), `decimal` (ADR 0064), or `string` (a model, enum, `bool`, list, or union), and `sorted()`/`sortedDescending()` on a list whose element isn't one of those (ADR 0065 §2; `TestListQueryMisuse`) | Attested — same test. Replaces a runtime Fault (`"are not ordered"`), which stays in the interpreter for unchecked input. |

## K. Formula and builtin errors (ADR 0049)

| # | Bug class | Status |
|---|---|---|
| K1 | A method call whose receiver type has no such method in its builtin table (ADR 0049 §2.6): `42.isBlank()`, `name.reverse()`, or any method on a model, enum, union, or other type with no table; and an operation a typed primitive doesn't have (`Random.choose(xs)`, ADR 0068 §5), or `Database` (`Database.exec`, ADR 0082 §1) | Attested — `compiler/checker`'s `TestMethodNotInTableIsK1`, `TestRandomOperationsAreTyped`; `database_error.fiig` (`removeBySql`). Was a runtime fault the checker never saw, and for a receiver that wasn't a bare name the checker didn't type the call at all. The table lives in `compiler/types` (`types.MethodsOf`) and the runtime dispatches through it (§2.8); `TestEveryBuiltinMethodIsImplemented` keeps the runtime's bodies and the declared signatures in step. No corpus migration. |
| K2 | A declaration, public or private, named after something the prelude binds in every file: a primitive type (`int`, `string`, ...), a prelude type (`Result`, `List`, `BatchResult`, `None`, `Connection`, `SocketError`, `Rounding`), `Ok`/`Err`, or the `math` group. Type resolution and calls reach the prelude's meaning first, so the declaration was silently unreachable; a package's own `SocketError` instead silently replaced the prelude's | Attested — `math_error.fiig` (a private `model Result`); `compiler/checker`'s `TestDeclarationNamedAfterPrelude`. The list is `types.PreludeName`. No corpus migration. |
| K3 | A `formula` that returns a `Result`, or a union holding one (ADR 0049 §2.2 restriction 3): a formula can only fault, never fail in domain terms, and a check with a reason to give is a rule | Attested — `formula_error.fiig` (`brokenChecks.parseEmail`); `compiler/checker`'s `TestFormulaRestrictionsAreK3K4K5` |
| K4 | A `formula` with a `requires` clause (§2.2 restriction 1). Not in the grammar; the parser accepts it so the diagnostic can name the tier's purpose instead of reporting an unexpected token | Attested — `formula_error.fiig` (`brokenChecks.lookupDomain`) |
| K5 | A bodiless `formula` (§2.1). Only the prelude `math` group's members have no body, and they are a compiler table rather than source, so every bodiless member in source is K5 | Attested — `formula_error.fiig` (`brokenChecks.normalize`) |

## P. Capability and provider errors (ADR 0043)

| # | Bug class | Status |
|---|---|---|
| P1 | A manifest's `provides` maps an interface to a capability group that lacks one of its operations | Attested — `compiler/checker`'s `TestProvidesCompletenessAndSignatureAreP1AndP2`. Checked by `types.BindProvides` when a package is checked or loaded; an entry that fails binds nothing, so the group is then not assignable to the interface either. |
| P2 | A group's gateway differs from the interface member it fulfills: parameter count, a parameter's name, or a parameter or return type | Attested — same test. Types are compared by identity, each side resolved in its own package, so a same-shaped look-alike declared locally does not match. Names must match because a provider call binds arguments by the interface's names. |
| P3 | A rule or conversion invokes a provider (`Provider.charge(...)`) | ADR 0043 §4 — selecting a provider is not an effect, invoking one is | Attested — `capability_error.fiig` (`refundInRule`). A rule calling a capability group directly is B1 (a conversion, B5), the same as calling any gateway. |
| P4 | Another package references a gateway that is not in a capability group, by call (`http.loose(...)`) or by path-tail import (`use acme.http.loose`) | ADR 0043 §5 — a package's capability surface is exactly its groups | Attested — `TestCapabilityGroupsAcrossPackages`. Needs two packages, so no `invalid-programs/` fixture. No corpus package exported a loose gateway, so nothing migrated. |
| P5 | Capability groups in one package whose gateways require each other in a cycle | Attested — `capability_error.fiig` (`RefundPing`/`RefundPong`, reported at both). Between packages the loader already rejects dependency cycles. |
| P6 | A model or enum field holds a capability | ADR 0043 §6c — providers can use models, but models don't dictate how they are used | Attested — `capability_error.fiig` (`RefundPolicy.provider`) |
| P7 | A gateway uses a capability other than the one it requires: another group, a provider value, or (ADR 0060) a primitive such as `Socket`, or (ADR 0082 §2) a capability with no declaration in fiig, such as `Database` or `EmailSender`. Not `Clock` or `Random`, which are local effects any gateway may read (ADR 0068 §4) | ADR 0010 + ADR 0043 §5 + ADR 0060 + ADR 0082 | Attested — `capability_error.fiig` (`refundDirect`); `database_error.fiig` (`recordSent`, `mailFromDatabase`); `compiler/checker`'s `TestSocketOutsideItsGatewayIsB10OrP7` for the primitive form |
| P8 | A gateway `requires Clock` or `requires Random` | ADR 0068 §4 — a local effect reads the program's own machine, so it isn't a capability to require; the clause would do nothing and tell a reader the gateway reaches another system | Attested — `random_error.fiig` (`newApiKey`); `TestRequiringALocalEffectIsP8`. No corpus gateway required either, so nothing migrated. |

## H. Registry / capability errors

| # | Bug class | Status |
|---|---|---|
| H1 | An imported gateway gains a capability its published contract didn't declare | `fiig-lang.md` line 433 (Milestone 6 exit criteria) | Attested — `compiler/loader`'s `TestLoadRejectsUndeclaredDependencyCapability`/`TestLoadRejectsUndeclaredOwnCapability` (docs/decisions/0035-capability-security-review.md). Inherently a cross-package, build-level scenario (an application's own manifest allowlist vs. its full resolved dependency closure's actual gateway requirements), so it's attested by a synthetic two-package workspace rather than a single `invalid-programs/*.fiig` file the way every other entry here is — the same reason this entry originally gave for deferring it. |
| H2 | A resolved build requires a capability that is neither one of the closed primitives (`Socket`, `Clock`, `FileSystem`, `Random`; ADR 0060 folded `TlsSocket` into `Socket`) nor listed in the application manifest's `nativeCapabilities` (ADR 0042 — the floor is the socket, so a domain capability needs a fiig fulfiller; until ADR 0043 provides them, the grandfather list is the explicit, shrinking record of which ones are still Go-only) | Attested — `compiler/loader`'s `TestLoadRejectsDomainCapabilityWithoutNativeEntry`; the positive cases are `TestLoadAcceptsDeclaredDependencyCapability` and `TestLoadAcceptsPrimitiveCapabilityWithoutNativeEntry`. Enforced where H1 is, in the loader, because the manifest lives there: a loose directory with no manifest (the whole `examples/` corpus) is not a package build and is not checked. |

| H4 | A private member escapes its file (ADR 0054 §3): named in its public entry's signature at any depth (a parameter, the return type, a generic argument, a union member), or referenced from another file | Attested — `compiler/checker`'s `TestPrivateTypeInPublicSignatureIsH4` and `TestPrivateMemberReferencedFromAnotherFileIsH4`. The transitive half needs no walk through other models: a public model lives in its own file and can't see the private type, so the only types that can hold one are the file's own, and the signature check reaches every one of those. A reference from another file would resolve to nothing anyway (private members are file-scoped); H4 replaces the "undefined name" it would otherwise get with a message naming the owning file. Like G3, no `invalid-programs/` fixture: it needs two files. |
| H5 | A `Connection`, or a model holding one at any depth, appears in a workflow's signature (ADR 0060 §2): a workflow is the adapter boundary (ADR 0044), and a socket handle can be neither serialized nor handed in from outside | Attested — `socket_error.fiig` (`resumeSession`); `TestConnectionInWorkflowSignatureIsH5` for a nested model and a return type |
| H6 | A source file sits directly in `src/` of an `application` module (ADR 0108): an application declares nothing at the top level, so every declaration lives in a folder, one per domain. A library's top level is its public face and is unaffected | Attested — `compiler/loader`'s `TestAnApplicationHasNoTopLevelDeclarations`; the language server reports it as you type. Moved: the seven corpus applications that had top-level files |

## W. Wire errors (ADR 0069 §6)

What the HTTP adapter refuses to write. Each is a fault at run time (a 500,
with the reason in the trace, and nothing of the response sent), because
Go's `net/http` would otherwise quietly change or drop the value and hide
the bug that produced it; the checker reports the ones it can see in
constants. They live in `compiler/httpspec`, shared by the checker and the
adapter.

| # | Bug class | Status |
|---|---|---|
| W1 | A response HTTP can't carry: a status outside 200 to 599, a body on a 204 or 304, a header name that isn't an RFC 9110 token, a header the adapter owns (`Content-Type` from `produces`, `Content-Length`, `Set-Cookie` from `cookies`, and every `Access-Control-*` header, from `fiig serve -allow-origin`, ADR 0070 §7), two header names that differ only by case, or a header value with a control character other than tab (CR and LF are header injection, CWE-113) | Attested — `wire_error.fiig` (`htmlPage`); `compiler/checker`'s `TestWireConstantsAreChecked`; the run-time faults in `compiler/adapter`'s `TestTheAdapterRefusesWhatHTTPCantCarry` |
| W2 | A cookie a browser would refuse or read as more than it says: a name that isn't a token, a value outside RFC 6265's cookie-octets, a `path` with `;` or a control character, a `domain` that isn't a hostname, a negative `maxAge`, `sameSite: AnySite` with `sentOverPlainHttp: true`, a `__Secure-` name without `Secure`, a `__Host-` name without `Secure` or with a `domain` or a `path` other than `/`, or two cookies in one response with the same name, path, and domain | Attested — `wire_error.fiig` (`embedCookie`), and the same two tests. The checker checks each field it can see, and the browser rules only when every field they read is known (a left-out field is its known default) |
| W3 | An enum variant's payload field whose JSON member is `variant` (ADR 0072 §3): one named `variant`, or one read `from json "variant"` (ADR 0073 §7b). An enum with payloads is sent in JSON as an object whose `variant` member names the variant, so the field would be sent twice. A field named `variant` with a `from json` name of its own is fine. Unlike W1 and W2 it is checked on the declaration, whether or not an adapter sends the enum, since any value can be encoded | Attested — `wire_error.fiig` (`Delivery.Shipped`); `compiler/checker`'s `TestJ2AndW3OnJsonNames` |
| W4 | A route argument read from text (a path segment, query value, header, cookie, or command-line word) whose type text can't be read as: anything but `string`, `int`, `float`, `decimal`, `bool`, or an enum without payloads, optionally `\| None` (ADR 0080 §6). An enum with payloads has no single-text form, and a model or list, which passed before this class existed, failed every request with a 400 | Attested — `wire_error.fiig` (`deliveryApi`); `compiler/checker`'s `TestW4ReadFromText` |


## V. Config errors (ADR 0071)

A config declaration the runtime couldn't read once, before the program
starts. Reading one outside an activity or a gateway is B10, like `Clock`.

| # | Bug class | Status |
|---|---|---|
| V1 | A config field of a type an environment variable's text can't be read as (anything but `string`, `int`, `float`, `decimal`, `bool`, an enum, or `Secret`, optionally `\| None`), an environment variable name that isn't one, a default that isn't a constant or doesn't fit the field's type (A1), a default on a `Secret`, which would put the secret in the source, or a field declared twice | Attested — `config_error.fiig` (`Billing.apiKey`); `compiler/checker`'s `TestConfigDeclarationsAreV1`. What only the deployment can get wrong (unset, or `86400s` for an int) is the startup report, every problem at once (`compiler/runtime`'s `TestEveryConfigProblemIsReportedAtOnce`) |
| V2 | A config's `when` (ADR 0122) that isn't a bool made of comparisons (`==`, `!=`), `and`, `or`, `not`, literals, enum variants, and fields of config declarations that have no `when` of their own; or that reads its own fields | Attested — `compiler/checker`'s `TestAWhenOutsideTheSubsetIsV2`, `TestAWhenIsABool`, `TestAWhenCantReadConfigThatHasAWhen`. The runtime half, a config whose condition is false and is read anyway, is a fault: `compiler/runtime`'s `TestAConfigWhoseWhenIsFalseIsNotRequired` |

## S. Secret errors (ADR 0071 §4)

A `Secret` is a config value that must never be shown.

| # | Bug class | Status |
|---|---|---|
| S1 | A `Secret` shown: interpolated into a string, or able to cross the adapter boundary in a workflow's parameters or result, or a response body. (`==` on one is M1, since it would leak the secret through timing; `.matches()` compares in constant time) | Attested — `config_error.fiig` (`describeKey`); `TestSecretIsNeverShown` |
| S2 | `.reveal()` outside a gateway, the kind that hands values to the outside world. Anywhere else a `Secret` is passed on, matched, or given to an operation that takes one, so no rule can turn it into a `string` and return it | Attested — `config_error.fiig` (`keyText`); `TestSecretIsNeverShown` |

## J. JSON errors (ADR 0073)

| # | Bug class | Status |
|---|---|---|
| J1 | `json.decode` into, or `json.encode` of, a type JSON can't carry (§8): one holding a `Secret`, a `Connection`, a capability or interface value, or a `BatchResult`, and, to decode, a `Map` whose keys aren't `string`. Reported at the call with the part at fault (`json.decode into Session: field token is a Secret`), and at a route whose JSON request body is such a type, which before was found only by the first request. A `Secret` in a body is S1's, at the workflow. `bytes` was J1 until ADR 0078 gave it base64 | Attested — `json_error.fiig` (`readCounts`); `compiler/checker`'s `TestJ1` |
| J2 | Two fields of one model or one variant reading the same JSON member (§7b), or two variants of one enum with one spelling (ADR 0080 §1, `A from json "B"` beside a variant `B`), as `a: string from json "x"` beside `x: string`, including through embedding. Decoding would fill both from one member, and encoding would send it twice | Attested — `json_error.fiig` (`Upload.type`); `TestJ2AndW3OnJsonNames` |
| J3 | A union whose members read the same JSON, wherever J1 looks (a request or response body, `json.decode`, `json.encode`, at any depth): two enums with a text form in common, two enums with payloads sharing a variant's spelling, or a variant spelled `Ok` or `Err` beside a `Result` (ADR 0081). Decoding would take whichever member is written first. An enum beside `string` is not J3: its spellings decode as the enum (§3) | Attested — `json_error.fiig` (`readLoss`); `compiler/checker`'s `TestJ3RefusesUnionsWhoseMembersReadTheSameJSON` |

## Q. Query errors (ADR 0082)

| # | Bug class | Status |
|---|---|---|
| Q1 | A `Database` call's SQL text is not a string literal, or interpolates with `${...}` (§3). A value reaches the database only as a bound parameter, so a query built from a value, the shape SQL injection takes, is refused at check time | Attested — `database_error.fiig` (`removeBySql`); `compiler/checker`'s `TestDatabaseMisuse` |
| Q2 | A value bound to a `Database` call that SQL can't bind (§4): a model, list, map, payload enum, `Connection`, or `Secret` (a gateway that means to store one calls `.reveal()`). Bindable: `int`, `float`, `string`, `bool`, `decimal` (as TEXT), `bytes` (as a BLOB), a payload-free enum (its text form), and `None` | Attested — `database_error.fiig` (`removeBySql`); `TestDatabaseBindsScalarsEnumsAndNone` |
| Q3 | A query's row type that SQL can't fill (§5): a model with a field one column can't hold (a list, a nested model, a union other than `T \| None`), or a `queryOne` read as two types beside `None`, or a model with two fields that would read one column, since a column matches a field ignoring case and underscores (ADR 0094 §3: `userId` and `user_id`). A row's mismatch with the stored values is a fault at the query instead (§6-§8), since the checker doesn't know the schema | Attested — `database_error.fiig` (`readLedgers`); `TestDatabaseMisuse` |

## T. Test errors (ADR 0091)

A test lives under its package's `tests/` directory, and its fakes are
checked against what they replace. Each needs a `tests/` path, which no
`invalid-programs/` file has, so `compiler/checker`'s `TestTestBugClasses`
attests them (T2 also by any program file).

| # | Bug class | Status |
|---|---|---|
| T1 | A file outside `tests/` names a declaration inside it (§2): a test helper leaking into the program | Attested — `TestTestBugClasses` |
| T2 | A `test`, `fake`, or `expect` outside `tests/` (§2) | Attested — `TestTestBugClasses` |
| T3 | `expect` on a value that isn't `bool`, or `expect x is P` where no value of x's type can match P: `Ok`/`Err` on a non-Result, a variant of another enum or one that doesn't exist, `None` on a type without it (§3). A `match` arm gets this from A9; an `is` has no other arms | Attested — `TestTestBugClasses` |
| T4 | A fake operation whose name, parameters, or result differ from the real one's (§4), as P1/P2 check a provider | Attested — `TestTestBugClasses` |
| T5 | A fake the test never reaches (§4), such as one left behind by a refactor | Attested — `TestTestBugClasses` |
| T6 | A test reaches an operation its fake lacks, or a provider a rule can return that it doesn't fake (§4) | Attested — `TestTestBugClasses` |
| T7 | A fake of a capability with no fiig declaration (§4): there's nothing to check it against | Attested — `TestTestBugClasses` |

## I. Stream, schedule and startup adapter errors (ADR 0047, ADR 0111, ADR 0112, ADR 0124)

A stream route binds one element from a source, names it by a key, and
says what happens to every outcome of its workflow, since a stream has no
caller to hand an error to. A schedule route (I5-I7, ADR 0112's sections)
fires a workflow at times its schedule names, with the same obligation.

| # | Bug class | Status |
|---|---|---|
| I1 | A stream route's key is missing for a model element, isn't a field of the model, or is a field text can't carry (W4's types); or a `bytes` element's route has a key (§3) | Attested — `compiler/checker`'s `TestStreamRouteBugClasses` |
| I2 | A stream route has no mapping, or its mapping doesn't cover `Ok` and every variant of the workflow's `Err` enum; or a `retry(attempts: N)` with N below 1 (§3) | Attested — `TestStreamRouteBugClasses` |
| I3 | A stream route's call, or its workflow, doesn't take exactly one argument: there is one element to bind it from (§6) | Attested — `TestStreamRouteBugClasses` |
| I4 | A stream element that is neither a model, decoded from the payload's JSON, nor `bytes`, the payload as it arrived (§3) | Attested — `TestStreamElementMustBeAModelOrBytes` |
| I5 | A schedule route's `every` isn't a duration, is under 1 s, or doesn't divide a day; or its `cron` isn't five valid fields, or never fires (`0 0 31 2 *`) (§1) | Attested — `compiler/checker`'s `TestScheduleRouteBugClasses` |
| I6 | A scheduled workflow takes anything but nothing or one `int`, the firing time, or the route's arguments don't match it (§1) | Attested — `TestScheduleRouteBugClasses` |
| I7 | A schedule or startup route has no mapping, or its mapping doesn't cover `Ok` and every variant of the workflow's `Err` enum (ADR 0112 §2, ADR 0124 §1) | Attested — `TestScheduleRouteBugClasses`, `TestStartupRouteBugClasses` |
| I8 | A stream route names no `group`, or doesn't say `from earliest` or `from latest` (ADR 0111 §3a): the group is state the broker holds, so it is written down rather than derived from where the adapter lives | Attested — `TestStreamRouteBugClasses` |
| I9 | A startup route's workflow takes arguments, or the route gives any: a startup route has no caller to supply them (ADR 0124 §1) | Attested — `TestStartupRouteBugClasses` |

## Explicitly out of scope (not statically checkable)

Some §3 statements read like rules but aren't things a compiler can reject —
worth naming so they aren't mistaken for missing catalog entries:

- **No `null`** (§3.4) — there's no `null` keyword in the language to misuse;
  this is true by construction, not by a check.
- **No unchecked, Java-style exceptions** (§3.6) — same: nothing to reject
  because the construct doesn't exist.
- **Prefer separate lifecycle models over temporarily-invalid objects**
  (§3.5, e.g. `NewCustomer` vs. `Customer`) — a style guideline, not a
  checkable property; nothing stops a developer from modeling it differently.
  Likewise ADR 0083's rule that makes the valid model
  (`createValidRating`): any code may still construct a `Rating` directly.
- **Failure after an earlier commit requires explicit compensation, not
  rollback** (§3.8) — the *language* can make compensation easy to write
  (`place_and_ship_order.fiig`), but whether a given compensation is
  *correct* (actually undoes the right things) isn't something a compiler
  can verify. This is a discipline the language supports, not a bug class it
  rejects.
- **Multiple operations against one capability staying consistent without a
  `transaction` block** (§3.8; formerly numbered C3) — dropped outright, not
  deferred. The semantic rule itself still stands (single writes are always
  safe; `transaction` blocks are only needed when multiple operations must
  stay consistent together), but no implementation was found that checks it
  soundly. A real check needs two things: telling a mutating gateway call apart
  from a non-mutating one (`StepDecl` has no such marker, and this part
  genuinely is just missing infrastructure a future capability-contract
  system could supply), and telling *related* operations apart from
  *unrelated* ones on the same capability (two independent
  `updateCustomerEmail` calls for different customers are not a hazard, and
  nothing short of entity-level data-flow tracking — a much bigger feature
  than capability contracts, and not on any roadmap — could tell them apart
  from the real hazard this was meant to catch). That second gap is the same
  kind of judgment call as compensation correctness above, not a temporary
  blocker, so this is staying out of the numbered catalog rather than sitting
  in it as a perpetual "deferred."

## Coverage against Milestone 0 exit criteria

`fiig-lang.md`'s exit criteria requires "at least five important bug classes
... cannot be represented as valid programs" — comfortably satisfied: 24 of
25 identified bug classes are now attested, 23 by a real `invalid-programs/`
example across all eight categories plus H1 by `compiler/loader`'s own test
suite (a build-level, cross-package scenario no single file can represent).
The remaining one (A5) is deliberately deferred, for a stated structural
reason rather than oversight — no syntax exists yet to misuse. Doesn't block
Milestone 0's exit criteria. (C3 was dropped outright rather than counted
here at all — see
"Explicitly out of scope" above.)

A9 was added afterward, during Milestone 2 itself rather than Milestone 0's
original pass — it comes from `fiig-lang.md`'s Milestone 2 deliverable list
(line 286), not a §3 "must/cannot" statement the way every other entry here
does, since exhaustiveness is a property of having sum types at all, not a
rule a §3 principle asserts. The catalog stands at 25/26 as a result, now
only A5 deferred.
