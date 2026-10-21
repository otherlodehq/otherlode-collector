### Tooling

**Skills and commands are vendored into every repo, so a cloud session has
them.**

1. A new skill or command goes into each repo's `.claude/` as a vendored
   copy, with its licence and an entry in `.claude/README.md` when it comes
   from upstream.
2. Workflow skills are manual-only (`disable-model-invocation: true`)
   unless Luke asks otherwise. Add each one to the named workflows table in
   `power-mode` so a mid-sentence mention still finds it.
3. When Luke corrects a workflow ("fix everything regardless of size", "it
   should be recursive"), edit the skill or command so the next run has it.
   Do not keep it only in memory.
4. A hook that misfires on ordinary commands gets fixed or removed, not
   worked around.
5. A hook or script goes through the review loop. Skill and command prose
   doesn't, unless Luke asks for a review. Run `unslop` over agent-facing
   prose.
6. Commit and push to every repo that carries the copy. Wrap-up.

**Reply:** what changed in which repos, and what the next session will now
do differently.
