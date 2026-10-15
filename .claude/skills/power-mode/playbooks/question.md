### Question

**Read-only. The deliverable is an answer with evidence, not a change.**

1. Pull every repo, then read the code, the ADRs and STATUS for the area.
   Fan out to parallel subagents when the answer spans repos.
2. Answer in this shape: a short answer first (yes or no where the question
   allows), then why, with `file:line` and ADR citations, then what follows
   from it.
3. When the answer exposes a gap, say how big it is in practice and how
   likely it is to bite. Say which repos a fix would touch.
4. End with a choice and its size: fix it now (and whether it needs a grill
   first), or add a STATUS todo. Do not start the fix. Luke often narrows
   it to a todo.
5. When the question is "should we do X or Y", give a recommendation, the
   precedent from OTel or the leading tool, and a short tradeoff table.

**Reply:** the answer, the evidence, the gap's real size, and the fix-or-todo
choice.
