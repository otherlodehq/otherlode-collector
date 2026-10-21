### Perf

**Every claim about speed or cost carries a number you can explain.**

1. Pull every repo. Measure a baseline on the shape that matters, such as
   a benchmark preset or a real service's size, and on the tier that
   matters when the claim is about production. Say where you measured,
   since a container's local Postgres is not Cloud SQL.
2. Explain the number before you act on it. Name what bounds it: a
   database core, a lock, the disk, the network or the load generator.
   Take it from a profile, `EXPLAIN (ANALYZE, BUFFERS)` or counters read
   during the run, not from reading the source. Rule out what else the
   number could be measuring, such as errors, skipped or cached work, code
   that never ran, an untuned side or noise. Keep the run count and the
   spread with it.
3. Try the fixes cheapest first, and stop when one meets the target.
   1. Don't do it. Stop work whose result nothing uses.
   2. Do it, but don't do it again.
   3. Do it less.
   4. Do it later.
   5. Do it where nobody waits.
   6. Do it concurrently.
   7. Do it cheaper.
4. Change one thing at a time and measure after each. Keep only what moved
   the number. A change that "might help" and measured nothing is reverted.
5. A fix that changes a result, or what the product claims, is not a perf
   fix. Grill it per Design when it's hard to undo. Code goes through the
   review loop with the tests that prove results are unchanged.
6. Wrap-up.

**Reply:** the baseline, the new number, the delta, what bounds the new
number, where it was measured and how many runs, in a table.
