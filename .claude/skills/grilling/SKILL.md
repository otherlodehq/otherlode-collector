---
name: grilling
description: Grill the user relentlessly about a plan, decision, or idea. Use when the user wants to stress-test their thinking, or uses any 'grill' trigger phrases.
---

Interview the user relentlessly until you reach a shared understanding. Map this as a **design tree**: every decision branches into the decisions that hang off it.

Work the tree in **rounds**. The **frontier** is every decision whose prerequisites are already settled: the questions you can ask _now_ without guessing at answers you haven't heard yet. Settle the whole frontier in one round, but split it in two.

- **Questions** are the decisions only the user can make: a call that's hard to undo (a published wire field or API, a data migration, a release, a price or plan, what a claim means to a customer), or a product or preference call no experiment can settle. Number each one and give your recommended answer.
- **Decided** is everything else. Decide it yourself, from the codebase or by running something, and list it in a table so the user can follow every decision without answering it. A decided row stands unless the user changes it.

Then wait for the user's answers to the questions before the next round. A round with no questions still shows its Decided table, and goes on to the next round once the user has seen it.

Format a round like so:

```
❓ **Q1** - **<question title>**: <question body, might be multiple paragraphs, including multiple choices>

➡️ <your recommended answer>

---

❓ **Q2** - **<question title>**: <question body, might be multiple paragraphs, including multiple choices>

➡️ <your recommended answer>

---

✅ **Decided** (say if you want any changed)

| # | Decision | Why |
| --- | --- | --- |
| D1 | <what you decided> | <the reason, or what you ran to settle it> |
```

Each round the user answers reshapes the tree: settled decisions, theirs and yours, push the frontier outward and unblock questions that depended on them. A Decided row the user changes is theirs from then on, and anything downstream of it is asked again. Recompute the frontier and ask the next round. A question whose answer depends on another question still open in this round belongs to a _later_ round, not this one.

Finding _facts_ is your job, never the user's. When a frontier question needs a fact from the environment (filesystem, tools, etc.), dispatch a sub-agent to find it; don't ask the user for anything you could look up yourself. Don't block on it: a running exploration is an unsettled prerequisite, so only the questions downstream of it wait for the sub-agent to report; ask the rest of the frontier now. The _decisions_ are the user's: put each to them and wait.

The session is done when the frontier is empty: every branch of the design tree visited, nothing left silently assumed. Do not act on it until the user confirms you have reached a shared understanding.
