# ADR 0002: Go as the Bootstrap Implementation Target

Status: Accepted
Date: 2026-09-22
Supersedes: language.md §4 recommendation (pending spike), Decision record #1 (§8)

## Context

Decision record #1 required choosing between Go and the JVM as the compiler implementation language and initial execution target, before Milestone 1 work begins. A 3-day spike (`0001-go-vs-jvm-spike-plan.md`) measured cold start, bounded concurrent fan-out, HTTP throughput/latency, GC behavior, and cross-platform distribution for both.

A prior concern — that the JVM cannot support import aliasing — was investigated and is false. Import aliasing (language.md §3.10) is a source-level compiler-frontend concern, fully erased before bytecode generation. Kotlin and Scala both alias imports and compile to standard JVM bytecode. This was not a factor in the final decision.

## Decision

Implement the compiler in Go and target Go as the initial execution backend, per language.md §4.

## Rationale

Spike results showed no decisive performance winner:

- Go cold start ~2x faster (28ms vs 58ms avg).
- JVM matched or slightly exceeded Go on warm HTTP throughput and latency (3278 rps/p99 7.5ms vs 3813 rps/p99 5.7ms — close enough to call comparable).
- JVM GC pauses were negligible at this scale (max 1.57ms, G1 default).

The deciding factor was distribution, not runtime speed. Go produces a single static binary per platform (~2.4MB) with no runtime dependency. The JVM path requires either a JRE present on every target machine or a bundled jlink runtime image (~45MB per platform). Given the initial scope (language.md §2) includes CLI applications, scheduled jobs, and batch processing — short-lived processes often run on machines the team doesn't control the runtime environment of — "no install step" is an operational advantage Go provides for free.

This also matches the tiebreak rule already specified in language.md §4: ties go to Go, based on team implementation experience.

## Consequences

- The compiler, tooling, and runtime are implemented in Go.
- Application source still does not import Go directly (language.md §4) — this decision concerns the implementation substrate, not the language's own surface syntax, which remains free to diverge from Go conventions (e.g. `::`-style operators, Java/C#-like declaration forms).
- Goroutines and channels become the substrate for structured concurrency (§3.7); the language runtime must own scheduling, cancellation, and budgets on top of them rather than exposing them directly.
- GraalVM/JVM native-image was not evaluated further; no further JVM spike is planned unless a future milestone surfaces a Go-specific blocker (e.g. GC tuning limits under sustained high-throughput workloads).
- Cross-platform builds use standard `GOOS`/`GOARCH` cross-compilation; no separate packaging step is required per platform.
