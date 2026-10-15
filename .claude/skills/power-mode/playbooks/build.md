### Build

**Follow `/chunked-build`. This playbook adds Luke's standing answers to it.**

1. Check the preconditions in `/chunked-build` step 0. If the design is not
   written down, route to Design.
2. Run the review loop for each chunk. Sonnet implements, a fresh
   reviewer reviews, this session fixes what holds, and big fixes go back
   to a fresh reviewer until both are happy.
3. Read the implementation in full, not the report. Check it against the
   library source it depends on.
4. Verify each chunk on the real thing. Run the full build and tests. For a
   server change, run the stack and look at it (see the UI playbook).
5. Commit one chunk per commit and push to the default branch, unless the
   project has released (then a branch, and ask how to land it) or another
   agent is working in that repo. There, use a branch and merge
   once its default branch has been quiet.
6. Keep going chunk to chunk without asking. Ask once at the start about
   breakpoints. Luke's usual answer is none. Stop when a chunk needs a
   design change the brief did not make, grill it per Design, then rewrite the
   remaining briefs.
7. When Luke is away or says `/nightwork`, follow the nightwork skill
   instead. It already holds the run log, forks, parking and report.
8. Wrap-up.

**Reply:** per chunk, the commit hash and one line on what landed, what the
review changed and why, and the verification counts in a table.
