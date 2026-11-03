# Spike Plan: Go vs JVM Bootstrap Target

Status: Proposed
Timebox: 3 working days
Decides: Decision record #1 (language.md §8)

## Goal

Pick the target runtime with real numbers instead of instinct, on the axes that matter for our scope: HTTP services, CLI, queue consumers, scheduled jobs, batch.

## Build the same four things twice (Go, Java 21+ virtual threads)

1. **Cold-start CLI**: trivial program, parses args, does nothing else. Measure wall-clock from process start to exit, 100 runs.
2. **Bounded concurrent fan-out**: simulate `retrieveCustomers` over 10,000 IDs, each a fake 5ms I/O call, bounded to 50 concurrent. Measure total time, peak memory, peak goroutines/vthreads.
3. **HTTP service under load**: one endpoint, hits the same fake 5ms downstream call, load test at fixed RPS for 60s. Measure p50/p99 latency, memory, GC pause time (JVM only).
4. **Cross-compile + package**: build a distributable artifact for Windows/Linux/macOS from one dev machine. Measure artifact size and steps required.

## Score each on

| Axis | Go | JVM |
|---|---|---|
| Cold start (avg / p50 / p99) | 28ms / 28ms / 88ms | 58ms / 58ms / 68ms |
| Fan-out (10k items, concurrency 50) | 1.07s, peak 50 concurrent, 2.1MB heap | 1.24s, peak 50 concurrent, 10.6MB heap |
| HTTP load (10s, concurrency 20) | 3813 rps, p50 5.1ms, p99 5.7ms, 0 errors | 3278 rps, p50 5.9ms, p99 7.5ms, 0 errors |
| GC pause (max) | n/a (non-generational, not measured) | 1.57ms max, ~0.4-1.5ms typical |
| Cross-platform artifact | 1 command per OS/arch, ~2.4MB static binary, no runtime dependency | 913B jar, requires JRE on target; self-contained jlink image ~45MB |
| Debugger/profiler quality (subjective, 1-5) | 3 (delve, less mature) | 5 (JFR, async-profiler, decades of tooling) |
| Compiler-writer ergonomics (subjective, 1-5) | not scored this spike | not scored this spike |

Notes:
- Cold start p99 on Go had one outlier run (88ms) not reproduced on repeat spot checks; likely first-run disk cache, not steady-state.
- HTTP load test client hit Windows loopback TCP port exhaustion at concurrency 50; re-run at concurrency 20 to get a clean, comparable, error-free result. This is a client/OS artifact on Windows loopback, not a server-side difference, and affects both targets equally.
- All numbers are single-machine, single-run spikes for relative comparison, not production capacity planning.

## Explicit non-goals for the spike

- Not testing our own language's syntax or type checker — plain Go and Java code only.
- Not evaluating GraalVM native-image (separate spike if JVM wins but startup is disqualifying).
- Not tuning either runtime beyond defaults + one obvious flag pass (e.g., `-XX:+UseZGC`).

## Decision rule

- If JVM cold start is within ~2x of Go's *and* fan-out/HTTP numbers favor JVM, JVM wins on the merits Go was assumed to own.
- If JVM cold start is 5x+ slower and CLI/batch/scheduled-job scope is a large share of expected real usage, Go wins even if JVM throughput is better.
- Ties go to Go, per our stated implementation-experience default (language.md §4).

## Output

A filled-in table above, a one-paragraph recommendation, and an ADR (`0002-bootstrap-target.md`) recording the final call.

## Result

Numbers don't show a decisive winner on raw performance — Go is ~2x faster cold start, JVM is competitive on throughput/latency once warm, GC pauses are a non-issue at this scale. Given that, distribution wins the tiebreak: Go ships a single ~2.4MB static binary per platform with zero runtime dependency; the JVM path requires either a JRE on every target machine or bundling a ~45MB jlink image per platform. For a language whose initial scope is CLI tools, scheduled jobs, and batch processing alongside services, "no runtime to install" is a real operational win Go gets for free. Recommendation: **Go**, per decision rule tiebreak in language.md §4.
