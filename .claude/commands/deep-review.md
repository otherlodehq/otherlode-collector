---
description: Deep review of a codebase or subsystem. Verified bugs only, coverage check, comment pass. Manual invocation only.
argument-hint: [scope, e.g. a module or directory; default is the whole project]
disable-model-invocation: true
---

# Deep review

Review the project (or `$ARGUMENTS` when given) for bugs, quality-of-life
gaps, code smells, comment quality, and test coverage. The bar for calling
something a bug is high: most candidates turn out harmless on closer
inspection, and the report must say which ones did.

## Who does what

- The main session does the reading, the reasoning and the verification:
  the candidate list, each eye trace and each failing repro test. Do not
  hand judgement to a subagent.
- Fixes go through the review loop in `.claude/skills/power-mode/SKILL.md`, from a
  brief that names the failing test.
- A fix that is a design change, as power-mode's "Grill every design
  change" defines it, does not happen inside the review. Grill it with
  `/grill-with-docs` first, or list it under recommendations when the user
  is not there.
- Helpers take their model from power-mode's model rule. Fetch-and-dump
  jobs here are disassembling a jar, collecting sources from a dependency
  cache and pulling raw logs. Do work that one command or a script can do
  directly, such as listing test names or parsing a coverage report into a
  table. Give every subagent an exact brief and expected output shape.
- The main session reviews every subagent result before using it, and
  reviews the final edits (read the diff, run the tests) before reporting
  them.

## 1. Read everything in scope

Read every main source file in scope in full, not a sample. Then read the
build scripts, the wire schema or public contracts, the test scaffolding
(test names and fixture shapes, not every assertion), and any design notes
the repo keeps (CLAUDE.md, ADRs, CONTEXT.md). Keep a running candidate list
as you go: one line per suspicion, with the file and the reason.

Track the work with the task tools: one task per candidate, one for
coverage, one for the report.

## 2. Verify each candidate, both ways

A candidate becomes a finding only after both of these, and the report
records both:

1. **Eye trace.** Follow the code path that would trigger it: callers,
   the contract of the API it relies on (read the JDK, library or framework
   source when the contract is the question, do not quote it from memory),
   and the state it depends on. Write down the exact conditions.
2. **Repro test.** Write a test that pins the intended behaviour, run it
   against the unmodified code and confirm it fails for the reason you
   expect. Then fix it through the review loop, and confirm the test passes. A test that was green before the
   fix proves nothing.

If either step shows the candidate is harmless, record it under
"checked, not a bug" with the reason. That list is part of the deliverable;
it is what stops the next reviewer chasing the same ghost.

Severity is about consequences, not code shape: silent wrong output ranks
above a crash, and a crash ranks above a log line. A low-probability bug is
still a bug if the consequence is a hang or wrong data; say how likely it
is.

## 3. Test coverage

Measure real coverage, do not eyeball it. For a Gradle project, apply
JaCoCo through an init script kept in the job's temp directory so the repo
is untouched, fold every test task's and every `jvm-test-suite`'s execution
data into one report per module, and run the whole build with it. Adapt
the same idea to other build tools.

Before reading the numbers, strip the artefacts a coverage tool cannot see:
bytecode that is inlined into other classes (advice classes) never executes
as its own class, classes appended to the bootstrap loader are not
instrumented by the default agent settings, and generated code (protobuf
descriptors) is not the project's own. Report cleaned numbers, and say what
was excluded and why.

Also treat a test that fails only under the coverage agent as a candidate
in its own right: a second bytecode agent ahead of the one under review is
a real deployment shape, not test noise.

Then read the uncovered lines, not just the percentages. For each uncovered
public behaviour decide: worth a test (write it, following the project's
TDD rule), dead code (report it), or a path that only a real deployment
reaches (report it as a known gap). Do not chase a number; chase behaviour
that nobody would notice breaking.

## 4. Comments and docs

Run the `unslop` skill's checks over every comment or doc block you add
or touch, and scan the rest of the codebase for its patterns: stock words,
em dashes, sentences that restate the code, time-relative wording
("now", "previously"), and comments that the comment rule in the repo's
`CLAUDE.md` does not allow. Fix what you find in the files you are already
in; list the rest.

Check user-facing docs against the code: an options table that misses an
option the parser accepts, a README command that no longer exists, an ADR
claim the code contradicts.

## 5. Quality-of-life and smells

Report, and fix through the review loop when small and safe: duplicated logic that has already
drifted, a public API that lets a caller silently get the wrong answer, a
missing log line at a point where the design notes promise one, a test that
cannot fail. Leave larger refactors as recommendations with a reason.

## 6. Verification gate before reporting

- The full build (every module, every test suite) passes with the changes.
- The project's formatter has run over every touched file (for this
  project that is the `ktlint` CLI, run with `-F`):

      { git diff --name-only --diff-filter=d HEAD
        git ls-files --others --exclude-standard
      } | grep '\.kt$' | sort -u | xargs ktlint -F --relative

  Pass the list through `xargs`, never an unquoted shell variable: zsh does not
  split one, so ktlint gets a single argument, warns "No files matched", and
  exits 0 having checked nothing. Treat that warning as a failure. Build the
  list from `git ls-files --others` rather than `git status --short`, which
  collapses an untracked directory to its own name and would skip every new
  file in a new package. Re-run without `-F` afterwards and require exit 0,
  read from ktlint itself: piping its output to `tail` and then reading `$?`
  gives the pager's status, not ktlint's.
- Every finding in the report has its test named, and the test is in the
  tree.

Commit each separate fix on its own, with the test that pins it, unless the
user asks for one commit. Never commit a fix before the gate passes.

## 7. Report

Lead with the outcome in one line. Then, in this order:

1. Verified bugs: what, the evidence from both verification steps, the fix
   and its commit, the test that pins it, and how likely it is in practice.
2. Checked, not bugs: each candidate and the reason it is harmless.
3. Coverage: per-module numbers in a short table, then the uncovered
   behaviour that matters and what was done about it.
4. Quality-of-life and comment fixes made.
5. Recommendations not acted on, with the reason.

Short sentences. No headers in a short report. Numbers in tables, not
prose. The report has to stand on its own for a reader who did not watch
the session.
