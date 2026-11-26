# ADR 0003: Project Name and File Extension

Status: Accepted. The one pending mechanical follow-up, renaming the
project directory to `fiig-lang`, has since been done (audit, 2026-11-25).
Date: 2026-09-22

## Decision

Working name: **Fiig**
Source file extension: **.fiig**
Primary domain: **fiig.dev** (confirmed available)

## Rationale

"Fiig" is a playful respelling of **figh**, the Scottish Gaelic verb meaning "to weave/knit" (verbal noun *fighe*, related noun *figheadair* = weaver). It matches the project's registry-first composition thesis (language.md §1: composing existing operations, "weaving" workflows from existing rules/steps) without colliding with any existing language, framework, or company.

`fiig.com` redirects to an unrelated business — not a blocker, not a real conflict, and `.dev` is the more idiomatic TLD for a language/tooling project anyway.

## Alternatives considered and rejected

Extensive search turned up that almost every direct or translated word for "weave/composition" is already in active use in the developer-tooling / AI-agent-orchestration space as of 2026 — this space is unusually saturated right now:

- **Weave** — multiple existing programming languages/tools, plus W&B Weave (LLM observability, direct audience overlap)
- **Awana** (Quechua "weave") — awana.com/.dev both taken; awana.io is an active dev-recruiting company
- **Weft, Heddle, Skein** (loom vocabulary) — each already used by a real AI-orchestration language or framework (e.g. WeaveMindAI/weft, getheddle.dev)
- **Rivet, Cairn, Trove, Mortar** (construction/joining metaphors) — each is a real, active language or framework
- **Trama, Trame, Telar** (Spanish/French/Italian for weft/loom) — each has a real, sometimes popular, existing project (e.g. Kitware/trame)
- **Chaka, Ayni, Illa, Atiy, Tinku, Yanapa, Kotu, Yupana, Chanin, Suti** (Quechua candidates) — ruled out individually for either direct project conflicts (Illa/ILLA low-code platform, Ayni Protocol, Yupana code-intelligence tool) or real trademark risk (Suti/SutiSoft, an established enterprise software company using "Suti" as a product prefix)
- **Sooti** — rejected: phonetically collides with SOTI (enterprise MDM software company) and the trademarked "Sooty" children's puppet franchise, and doesn't actually dodge the SutiSoft conflict it was meant to avoid

## Pending actions (mechanical, not decisions)

1. **Rename the project root directory** from `language` to `fiig-lang`. *Done since: the repository is `fiig-lang` (audit, 2026-11-25).* Attempted 2026-09-22 and failed with "Device or resource busy" — the IDE had the folder open and held a lock. Needs the IDE/project closed first, then:
   ```
   mv "C:\Users\ryans\IdeaProjects\language" "C:\Users\ryans\IdeaProjects\fiig-lang"
   ```
2. Already done ahead of the rename: `.idea/language.iml` renamed to `.idea/fiig-lang.iml`, and `.idea/modules.xml` updated to reference the new filename. These use `$PROJECT_DIR$`-relative paths, so they're unaffected by the pending directory rename.
3. Already done: `language.md` and `docs/charter.md` updated to record "Fiig" as the working name.
4. Not yet done: nothing else references the old name in prose (spikes, other ADRs refer to `language.md` by filename, which is not changing).

## Next planned step after the rename

Per language.md §6 backlog, naming (#2) is now resolved. Next is **#3: write the ten canonical Milestone 0 example programs** (plus the compensation/atomic-block example flagged earlier as a candidate 11th).
