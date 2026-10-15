### Design

**Luke owns the decisions. You own the facts and the recommendations.**

1. Pull every repo. Read `CONTEXT.md`, the ADRs and STATUS for the area.
   Launch background subagents for any fact the questions depend on.
2. Run the grilling skill (with domain-modeling for `/grill-with-docs`).
   Ask the whole frontier each round, numbered, with a recommended answer
   and its reason. Ask the questions that need no pending fact first.
3. Recommend the correct option, not the cheaper one. If a recommendation
   leans on effort, a release deadline or compatibility before release,
   say so. Luke usually overrules those.
4. For any choice that could produce a false claim, prove the certainty
   before recommending it.
5. Commit glossary terms as they settle.
6. When the frontier is empty, show a table of what was agreed and where
   each part gets written, per repo. Wait for Luke to confirm.
7. Write the ADRs, the `CONTEXT.md` terms and the landing order in STATUS.
   Commit and push them as chunk zero.
8. Ask whether to build now, attended, or as `/nightwork`.

**Reply:** each round as the grilling skill formats it. At the end, the
decisions table and anything that changed from what was discussed.
