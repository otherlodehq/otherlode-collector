---
name: nightwork
description: Build a settled design unattended, chunk by chunk, through /chunked-build's review loop, and finish with a plain report of what landed, which forks were decided and what needs the user. Requires a completed grilling or a written brief. Manual invocation only.
argument-hint: [path to the brief or design section, when the grilling did not happen in this session]
disable-model-invocation: true
---

# Nightwork

Run a whole landing order while the user is away, then hand them a report
they can act on in a few minutes. The user is not there to answer
questions, so this skill starts only from a design that is already
settled, and anything that needs the user waits for the report instead of
blocking the run.

This skill points to three others. None of them can be started through the
Skill tool, so read each file and follow it:

- `/grill-me`: `.claude/skills/grilling/SKILL.md`, falling back to
  `~/.claude/skills/grilling/SKILL.md`.
- `/chunked-build`: `.claude/commands/chunked-build.md`, falling back to
  `~/.claude/commands/chunked-build.md`.
- `unslop`: `.claude/skills/unslop/SKILL.md`, falling back to
  `~/.claude/skills/unslop/SKILL.md`.

## 1. Check the brief before anything else

Start only when one of these holds:

- This session ran `/grill-me` (or `grilling`) to an empty frontier, and the
  user confirmed the shared understanding.
- `$ARGUMENTS` names a written brief or design section that states the goal,
  the decisions with their reasons, what is out of scope, and a landing order
  of chunks.

Then check the brief the way `/chunked-build` step 0 checks a design: the
decisions are written where the project keeps them (ADRs, `CONTEXT.md`, the
design notes), and each chunk in the landing order can be briefed without a
decision the brief did not make.

If the check fails, do not start any work. Name what is missing and suggest
`/grill-me`. A run that starts from an open question builds the wrong thing
for a whole night.

If the grilling happened in this session but its decisions are not written
down yet, write them first (ADR, `CONTEXT.md`, landing order), commit, and
treat that commit as chunk zero.

## 2. Print the plan and start

Print, in a few lines: the landing order, the repos it touches, and the
guardrails in step 5. Then start at once. Do not ask the user anything,
including the breakpoint question in `/chunked-build` step 2. A nightwork run
has no breakpoints.

Remind the user in the same message that a permission prompt stalls the run
until they answer it, so the session needs auto mode or an allowlist that
covers the build, `git` and the subagents.

## 3. Build each chunk with /chunked-build

Follow `/chunked-build` steps 1 to 7 for every chunk: a Sonnet subagent
implements from a written brief, this session reviews the implementation and
fixes what it finds, and a fresh Opus reviewer reads every non-trivial diff.
Repeat review and fix until both this session and the reviewer are happy.
There is no fixed number of rounds.

Keep a run log in the job's temp directory as you go, one entry per chunk:
the commit, every fork and what was chosen, and every item for the user. The
report is built from this log, not from memory at the end.

## 4. Decide forks, or park them

A chunk often turns up a choice the brief did not cover. Decide it yourself
when every one of these is true:

- The choice stays inside the brief's scope and agrees with its decisions.
- It is easy to reverse later: no published wire field, public API, data
  migration or release depends on it.
- You can name a reason that the brief or the codebase supports.

Record the decision where the project records decisions (the ADR, the status
log), and in the run log with the reason and the option you rejected.

Otherwise park it. Parking means:

1. Commit nothing from that chunk to the main branch. Put its work on a
   branch named `nightwork/<chunk>` and push that branch, so nothing is lost.
2. Write the question in the run log: the options, your recommendation, and
   what each option would change.
3. Skip every later chunk that depends on it, and continue with the chunks
   that do not.

Park a chunk the same way when the review loop stops converging: the same
finding returns after it was fixed, or this session and the reviewer
disagree and the code or the library source cannot settle it.

## 5. Guardrails

Never, during a nightwork run:

- cut a release, push a tag, or publish a package;
- force-push, rewrite history, or delete a branch;
- add a dependency or change CI unless the brief says to;
- send anything outside the repos named in the plan;
- weaken, skip or delete a test to make a build pass.

When the run needs one of these, park the chunk and ask in the report.

## 6. Write the report

When the landing order is done, or every remaining chunk is parked, write
the report. Read the `unslop` skill and apply it to the whole report before
showing it. Write for someone who was asleep: every item says what happened
and where to look, with real commit hashes, file paths and branch names.

Use these sections, in this order, and leave out any that is empty:

1. **Needs your attention.** First, because it is the part the user must act
   on. Each item is a parked chunk, a question only the user can answer, a
   gap you found that the brief did not cover, or something you could not do.
   Each says why it stopped, what you recommend, and where the work is.
2. **What landed.** One line per chunk: repo, commit hash and what the chunk
   does.
3. **Forks and decisions.** Each choice you made: the question, the option
   you picked, the reason, and the option you rejected. Mark any you would
   like the user to confirm.
4. **Verification.** The build, test and formatter results for each repo,
   read in this session, with counts.

End with the next step you recommend, in one sentence.

Show the report as the final message of the session, and also save it as
`nightwork-<date>.md` in the job's temp directory.
