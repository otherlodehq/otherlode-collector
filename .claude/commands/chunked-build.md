---
description: Build a settled design as sequential chunks, autonomously through the landing order. Each chunk goes through power-mode's review loop from a written brief; then the main session runs the build, commits one chunk per commit, and continues to the next unless the user asked for a breakpoint. Manual invocation only.
argument-hint: [which chunk to build next, or the design section that lists the landing order]
disable-model-invocation: true
---

# Chunked build

Turn an already-settled design into code one chunk at a time. Each chunk
goes through the review loop in `.claude/skills/power-mode/SKILL.md`, from a brief
this session writes, and is committed only after a round with nothing left
to fix. This file
adds how to brief, review and verify a chunk.
`$ARGUMENTS` names the chunk to build next, or the design section whose
landing order says what comes next.

## Who does what

- The main session owns the design, the briefs, the review, the fixes,
  the commit message and the project's status log. Judgement never moves
  to a subagent.
- The implementer, the reviewers and their models follow the review loop
  and the model rule in `.claude/skills/power-mode/SKILL.md`. The implementer hands
  back to the main session; it does not decide. A new reviewer each round,
  so no reviewer checks its own earlier advice.
- Research and fact-finding (reading library sources, compiling scratch
  fixtures, reading `javap` output) go to their own subagents, never to
  the implementer.
- Chunks run one after another, never in parallel. They share the build
  directory and usually a few central files, and the next brief depends on
  what the last review found.

## 0. Preconditions

Do not start a chunk until the design behind it is settled and written
down: a grilling session has emptied its frontier, the decision is in an
ADR, the terms are in `CONTEXT.md`, and the project's design notes carry
the landing order. If any of those is missing, stop and say which. A brief
written against an open question produces a chunk that has to be redone.

## 1. Write the brief

A brief is a file in the job's temp directory, one per chunk, and the same
text goes to the subagent verbatim. It states:

- **Where and how.** Repo path, branch, "work in place", "do not commit",
  and which sections of the design notes and which ADRs to read first.
  Point at the previous chunk's commit when the current shape of a file
  matters.
- **Behaviour to add.** What the code must do, in terms of the project's
  own vocabulary, with the boundary stated as sharply as what is inside it.
- **Facts already verified.** Everything this session has checked in
  bytecode, library source or the running system, marked "do not
  re-derive". The subagent starts cold; facts it has to rediscover are
  facts it may get wrong.
- **Tests to write, failing first.** Named by behaviour, including the
  negative cases that pin what must stay excluded, and the instruction to
  run the whole build at the end and report the result honestly.
- **Do not.** Scope that belongs to a later chunk, files it must not touch
  (glossary, ADRs, design notes), and the project's comment and formatting
  rules.
- **Report back.** Files changed with a reason each, exact test names, the
  build result, and any deviation from the brief with the reason.

## 2. Launch and wait

Launch the implementer with the brief. Do not touch the files it is
working in. When it reports, treat the report as a claim to check, not a result.

The loop runs on its own: brief, launch, review, commit, next brief, next
launch, through the whole landing order, without asking between chunks.
At the start of the run ask the user once whether they want breakpoints,
and if so which: after a named chunk, before anything that changes the
wire schema or a public API, before the first chunk in a second repo, or
none. Stop at exactly those points and report; everywhere else keep
going. Stop regardless when the review loop stops converging (the same
finding returns after it was fixed, or a correctness or security finding
cannot be settled from the code or the library source), when a review
finding contradicts the settled design, or when the next brief would
need a decision the design did not make.

## 3. Review the implementation, not the report

Hand the chunk's uncommitted diff to a fresh reviewer, briefed as step 4
describes, and read the implementation yourself while it runs.
Treat its report as step 4 does.

Read the diff of every main-source file the chunk changed, in full. Trace
the new rule from the one place it is defined through every caller, and
confirm two callers that must agree (a reactive path and a static one, an
encoder and a decoder) really share it. Read the emitted bytecode, the
wire fields or the SQL when that is what the chunk produces; the tests
passing is evidence, not proof.

Then read the tests: do they drive the real pipeline or a stand-in, do they
pin the negative cases, and would they fail if the rule slipped. Check the
fixtures against real compiler output when the chunk depends on a compiler
shape.

Read the subagent's deviations and flagged doubts with particular care.
They are the places it was unsure, and in practice they are where the
review finds something.

Run the `unslop` skill's checks over every comment and KDoc the chunk
added: stock words, em dashes, sentences restating the code, time-relative
wording. Delete every comment that the comment rule in the repo's
`CLAUDE.md` does not allow.

## 4. Fix every finding that holds, regardless of size

The main session implements the fixes itself; it does not send the chunk
back to the implementer. A test that pins a boundary the chunk left
unpinned counts as a finding.

Brief the full reviewer with the settled design, the ADRs, the facts
already verified, and the chunk's uncommitted diff (`git diff HEAD --
<paths>` plus the chunk's untracked files, in every repo the chunk
touched). Ask for findings only, each with its evidence as the review
loop's step 3 defines it. Treat its report as claims, and judge each
finding as that step says.

Before fixing, stage the chunk's own files as the review loop's step 2
says. Fix the whole batch, then verify it as the review loop's step 4
says. A reviewer that verifies a big batch gets the finding list and the
fix diff, not the whole chunk.

Repeat as the review loop says. The commit follows a round with nothing
left to fix, not the last fix.

## 5. Verify yourself

Run the whole build (every module, every test suite) in this session, not
by trusting the report. Rerun the touched test classes after the review's
own edits and read their counts. Run the project's formatter over every
touched file (for this project, the `ktlint` CLI with
`-F`), feeding it the file list through `xargs`:

    { git diff --name-only --diff-filter=d HEAD
      git ls-files --others --exclude-standard
    } | grep '\.kt$' | sort -u | xargs ktlint -F --relative

Pass the list through `xargs`, never an unquoted shell variable: zsh does not
split one, so ktlint gets a single argument, warns "No files matched", and
exits 0 having checked nothing. Treat that warning as a failure. Build the
list from `git ls-files --others` rather than `git status --short`, which
collapses an untracked directory to its own name and would skip every new
file in a new package. Re-run without `-F` afterwards and require exit 0, read
from ktlint itself: piping its output to `tail` and then reading `$?` gives the
pager's status, not ktlint's.

## 6. Commit

One chunk, one commit, directly on the branch the project's notes say to
use. The message says what the chunk does and why in plain sentences, and
names the bytecode or library fact the review added when there is one.
Credit the model with a `Co-Authored-By` line.

Then add a status entry to the project's design notes: what landed, what
the review changed and why, and the facts learned that the design section
did not predict. Update the memory directory only for guidance about the
way of working, not for what the repo already records.

## 7. Report and queue the next brief

Lead with the commit hash and one line on what landed. Then what was read,
what the review changed and why, and the verification results in a short
table if there are numbers. Then write the next chunk's brief and
launch it, unless this is one of the breakpoints the user asked for or
the landing order is finished.
