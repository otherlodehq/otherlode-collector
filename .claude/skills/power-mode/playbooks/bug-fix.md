### Bug fix

**Root cause first. The failing test lands before the fix in history.**

1. Pull every repo. Reproduce it yourself. Rerun the failing CI job
   locally, or drive the real stack. Read the logs and the library source the code
   relies on.
2. Find the root cause and say whether it lives in product code or only in
   a check. If it reveals a new compiler shape or a product gap, that is a
   design question. Route to Design or offer a STATUS todo.
3. When the cause is a flake, fix the test so it cannot flake. Never skip it
   or loosen it.
4. Write the failing test and the fix through the review loop. Commit the
   failing test first, with a message that says what it pins.
   Then commit the fix on top.
5. Run the full build and the original repro. Paste failing-then-passing
   output.
6. When a fix changes behaviour a design decided, check the ADR first. If
   they conflict, ask.
7. Wrap-up.

**Reply:** what broke, the root cause with `file:line`, the fix, and the
verification output. If Luke asked only for a diagnosis, stop after step 2.
