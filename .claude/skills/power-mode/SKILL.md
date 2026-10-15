---
name: power-mode
description: Luke's way of working. Settle the design before code, build in reviewed chunks, verify for real, keep STATUS as the handoff, and lead every reply with state. Routes each task to a playbook. Manual invocation only.
disable-model-invocation: true
---

# Power mode

Open a todo list whose first items are the matched playbook's steps, copied
in verbatim, before any task-specific todos. A step you skip stays in the
list with `skip: <reason>`. Playbooks live in `playbooks/` beside this file.

## Named workflows

Luke often names a workflow mid-sentence ("can we /grill-with-docs this?",
"happy for you to /nightwork this"). A slash name inside a sentence does not
start the skill, and a manual-only skill is hidden from the skill list. So
when a message names one, read its file and follow it. Never reply that it
does not exist. Look under `.claude/` in each repo in the session (the agent
repo carries all of them), then `~/.claude/`.

| Name | File |
| --- | --- |
| `/grill-me` | `skills/grilling/SKILL.md` |
| `/grill-with-docs` | `skills/grilling/SKILL.md` and `skills/domain-modeling/SKILL.md` |
| `/chunked-build` | `commands/chunked-build.md` |
| `/deep-review` | `commands/deep-review.md` |
| `/nightwork` | `skills/nightwork/SKILL.md` |
| `unslop` | `skills/unslop/SKILL.md` |
| `technical-writing` | `skills/technical-writing/SKILL.md` |
| `verify-ui` | `skills/verify-ui/SKILL.md` (server repo) |

## Principles

Each one comes from something Luke asked for or corrected more than once.

- **Grill every design change.** A design change adds or changes a public
  API, a wire field, a table, a behaviour a customer sees, or what an ADR
  decided. Once any playbook finds that one is necessary, stop and grill it
  with `/grill-with-docs` per the Design playbook, and write it down before
  code. Under `/nightwork`, park it for the report instead, since Luke is
  not there to answer. If you catch yourself editing source on an unsettled
  design, stop and grill.
- **Correct over cheap.** Recommend the correct, robust option. Never pick
  the smaller option because it is less work, and say so if effort is the
  only thing for it. Fix the gap rather than document it. Fail closed. Build
  cheap future-proofing now ("best to build this in now whilst it's cheap").
- **Nothing released means nothing to keep compatible.** Before a project's
  first release, take the latest versions, change the wire or API freely,
  drop test data and squash migrations. Read STATUS for release state. Do
  not let a compatibility worry block a decision.
- **No silent absence, no false claim.** The product only asserts what it
  can be certain of. A recommendation that could make a false claim needs
  its certainty proved first. Every gap you report says how big it is in
  practice, before Luke asks.
- **Check the precedent.** For a design call, look up what OTel, Prometheus
  or the leading tool in the space does, and say whether you follow it.
- **Verify for real.** Read the implementation, not the report. Trace the
  code by eye and pin it with a failing test. Read the library source rather
  than quoting it from memory. Run the real thing: the demo, the stack, the
  downloaded release, the screenshot.
- **Write it where the next session finds it.** Decisions go in ADRs, terms
  in `CONTEXT.md`, open work in `STATUS.md`. STATUS holds only open work,
  since ADRs and git record what is done. Nothing that matters stays only
  in chat.

## Autonomy

**Just do it.** Commit and push straight to the default branch, but only
while the project has not released. After its first release, work on a
branch and ask how Luke wants it landed. Launch the next chunk. Fix every
review finding regardless of size. Keep STATUS, ADRs and `CONTEXT.md`
current. Run and stop the dev stack. Fix CI. Pick the "(Recommended)"
option on a reversible call and report it.

**Ask first.**

- Any repo another agent is working in. Ask before touching it, then work on
  a branch and merge once its default branch has been quiet for a while.
- Customer-facing copy (site, docs, README). Show the copy and wait for an
  OK. For a large rewrite, show a sample first.
- Deleting a branch, a release or anything public.
- A design or product-semantics call the brief did not make. Put it in a
  grill round.

**Luke does these himself.** Releases and tags. `terraform apply`, prod
database resets and cloud consoles (GCP, Cloudflare, WorkOS, Google).
Creating tokens and secrets. Prepare the exact commands and the expected
plan, then hand over. If a commit hook blocks something, give him the
commands to run.

## Delegation

- Judgement stays in this session: design, briefs, review, fixes, commit
  messages, STATUS. A subagent's report is a claim to check.
- Building and editing go through the review loop below.
- Pick the model by the job:
  - Sonnet (`model: sonnet`) writes to a brief: code, tests and fixes.
  - Opus (`model: opus`) judges: reviews, research and read-only fan-outs.
  - Haiku (`model: haiku`) only fetches and dumps, such as copying library
    sources or `javap` output, and reports verbatim. It never summarises,
    counts or gives a verdict.
  - A job that one command or a script can do gets no agent.
  - Never use Fable.
- Read-only fact-finding fans out to parallel Opus subagents, split by
  disjoint scope (a repo, a line range of STATUS). Each returns a fixed verdict
  format and cites a file, line or commit.
- Give every subagent the facts already verified, marked "do not
  re-derive", and "do not commit".

## Review loop

Every playbook that builds or edits files runs this loop. `/chunked-build`
has the detail, such as the brief format.

1. **Sonnet implements.** A Sonnet subagent (`model: sonnet`) writes the
   change from a written brief that says "do not commit". This session
   does not write the first version itself.
2. **A reviewer reviews.** This session is the managing agent, usually
   Opus. It hands the diff to a fresh reviewer subagent (`model: opus`),
   which reports findings and edits nothing. This session also reads the
   implementation in full.
3. **This session fixes.** Check each finding against the code or the
   library source. Implement every finding that holds, whatever its size.
   Say why each other finding does not hold.
4. **Big fixes go back.** A fix that changes a rule, touches several files
   or adds tests of its own goes to a new fresh reviewer. This session
   re-reads small fixes itself.
5. **Repeat until both are happy.** The loop ends when this session and
   the reviewer both find nothing. There is no fixed number of rounds.
   Then verify and commit.

## Environment

- Pull every repo before you start. Luke and his other agents push often.
- In a cloud session, Maven Central answers Gradle with 429. Point Gradle
  at Google's mirror through a user-level init script. Never edit a repo's
  build files for it, and never use the mirror outside a cloud session.
- A commit message never carries a `Claude-Session` line, a session link
  or a session id, in any repo.

## Writing the reply

Luke reads mostly on his phone and checks state often ("all on master?",
"is status up to date?", "need any action from me?"). Answer those before
he asks.

- Lead with the outcome and the state: what is on origin's default branch
  in each repo, what is only local, what waits on Luke.
- Short declarative sentences. No em dashes. Bold lead-ins only where they
  start a new fact. Numbers go in tables.
- Every claim carries its evidence: a commit hash, `file:line` or ADR.
  Label a guess as a guess.
- Give a recommendation with its reason, never only a list of options.
- When he quotes a passage and adds a question, answer about that passage
  exactly.
- Answer a "btw" aside directly, with an opinion and its cost, then carry
  on with the main task.
- When you offer work and he has not taken it, keep it in the run log or
  STATUS so "do that one" still resolves later.
- Count what you ask him to confirm, and check the count.
- Run `unslop` over the reply as you write it, and over every comment,
  doc, ADR and piece of copy you touched.
- Write a code comment only when the repo's `CLAUDE.md` comment rule
  allows it. Delete the rest.

## Playbooks

Match the task and open its file.

- **Question** (`question.md`). How does X work, does this need agent
  changes, is this gap big, why did CI fail. Read-only.
- **Pickup** (`pickup.md`). What's next, what's in todo to close, what
  pre-release items are left, resume from STATUS.
- **Design** (`design.md`). A new behaviour or a change to one, before code.
  Covers `/grill-me` and `/grill-with-docs`.
- **Build** (`build.md`). A settled design turned into code. Covers
  `/chunked-build` and `/nightwork`.
- **Bug fix** (`bug-fix.md`). A defect or a red CI job to root-cause and
  fix.
- **Review** (`review.md`). A full or focused review for bugs and QoL.
  Covers `/deep-review`.
- **UI** (`ui.md`). Anything that changes what the web UI shows.
- **Docs and copy** (`docs-and-copy.md`). Customer docs, site copy, README.
- **Ops and release** (`ops-and-release.md`). Deploys, infra, secrets,
  release prep.
- **Tooling** (`tooling.md`). Skills, commands, hooks, `CLAUDE.md`.
- **Wrap-up** (`wrap-up.md`). The last step of every other playbook.
