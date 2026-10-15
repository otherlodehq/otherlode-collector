### Pickup

**STATUS is the ledger. Check it against the code before trusting it.**

1. Pull every repo. Confirm the scope Luke named (one repo's STATUS or
   all). Do not widen it.
2. For a short list, read it here. For a long STATUS, fan out one subagent
   per disjoint line range. Each reports, per `###` entry, the title and
   line, a verdict of CLOSE, PARTLY or OPEN, the evidence (commit, file or
   ADR), and the still-open sentences quoted verbatim.
3. Present the result grouped as: close now, close after moving a leftover,
   keep open, and found but missing from STATUS. Recommend one item to do
   next and why. Edit nothing yet.
4. On the go-ahead, prune. STATUS keeps only open work. Delete closed
   entries and the finished history inside open ones. Leave no pointer to
   removed text.
5. When the picked item needs design, route to Design. When it is settled,
   route to Build.
6. Wrap-up.

**Reply:** the grouped verdicts with line numbers, the one recommendation,
and after a prune the line counts before and after.
