# Releasing

A release is a version tag. Pushing `v0.1.0-alpha.1` runs
`.github/workflows/release.yml`, which:

1. runs both modules' tests;
2. builds the archives with `go run ./tooling/release -version <tag>`:
   `fiig` for linux, darwin (amd64, arm64) and windows (amd64), each with
   the README, license, guides, and the getting-started example, plus
   `SHA256SUMS`;
3. builds the IntelliJ plugin, `fiig-intellij-<tag>.zip` (ADR 0090), and
   publishes it with the archives as a GitHub pre-release;
4. pushes the image `ghcr.io/<owner>/fiig:<tag>` for linux/amd64 and
   linux/arm64.

To build the archives locally, from the repository root (Go only, no other
tools):

```
go run ./tooling/release -version v0.1.0-alpha.1 -out dist
```

`-targets linux/amd64` builds one platform.

Each toolchain carries one Unicode version, which decides a string's
`length()` and case mapping (ADR 0067 §1); `fiig version` prints it. It is
Go's `unicode.Version`, and `compiler/text`'s tables are generated from the
same version (`go generate ./compiler/text`; `TestVersionMatchesGo` fails
when a Go upgrade moves it). When it changes, the release notes say so.

Every push to `main` runs `.github/workflows/ci.yml`: tests, vet, gofmt.

Before tagging, check an archive the way the alpha's exit criterion does:
unpack it on a machine (or a shell) with no Go on `PATH` and follow
`docs/getting-started.md` from the unpacked directory.
