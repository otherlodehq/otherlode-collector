### Review

**Follow `/deep-review`. Verified bugs only, and fix them.**

1. Agree the scope: whole project, one module, or one heuristic Luke named.
2. Follow `/deep-review`. Each candidate needs an eye trace and a repro test
   that fails before the fix. Keep the "checked, not a bug" list.
3. Fix the verified bugs and the quality-of-life findings in the same pass,
   through the review loop. This session keeps the eye trace and the
   failing repro test. Luke expects fixes, not only a report. Ask before a
   large refactor. When a finding's fix is a design change, grill it per Design
   before fixing it.
4. Check the fixes against `CLAUDE.md`, the ADRs and the invariants.
5. Commit each separate fix on its own unless Luke asks for one commit.
6. Wrap-up.

**Reply:** the `/deep-review` report, with commit hashes for each fix.
