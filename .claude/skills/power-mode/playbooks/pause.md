### Pause

**Leave a stop that a fresh session can resume from without asking Luke.**
Run it when Luke says stop or pause, before a `/clear` he announces, or when
the context or the container may not last. Never on "keep going".

1. Stop at a safe point. Finish the current step or back out of it. Start
   nothing new. Stop running subagents, and keep their briefs and last
   reports for the note.
2. Make the work durable. Commit unfinished work to a `wip/<topic>` branch
   in each repo it touches, and push it. Never put unfinished work on the
   default branch. Say in the commit body whether the build passes.
3. Write a resume note in the work's STATUS entry: the goal, what landed
   (commits), what is verified, what is in progress (branch and files), the
   next step, the decisions made and still open, work offered but not
   taken, and anything that surprised you. Point to briefs by their
   content, not a temp path, since the container may go.
4. Commit and push STATUS to the default branch. Wrap-up.

**Reply:** where the work stopped, what is on which branch, the STATUS
entry to resume from, and the first step on resume.
