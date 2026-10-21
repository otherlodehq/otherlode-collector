### UI

**Mock it, let Luke pick, pin it with e2e, then build.**

1. Start the stack with `scripts/dev-stack.sh up` (see `verify-ui`).
2. For a visual change, mock two or three options in the running page with
   injected CSS. Send screenshots at 1440x900 and at a phone width of 390.
   Luke picks. Do not build an option he has not picked, and do not decide
   an open question ("always open or collapsible?") while waiting. If the
   change also alters what the API serves or how something is judged,
   grill it per Design first.
3. Write the e2e spec in `web/e2e/` first and see it fail.
4. Build it through the review loop, chunked per Build when it is bigger
   than one component.
5. Run every `verify-ui` check and every web check in the server
   `CLAUDE.md`. Look at your own screenshots before sending them.
6. UI copy follows `copy-style`'s `ui.md`. It states facts. Cut any line
   that explains the product, since the docs do that.
7. Wrap-up.

**Reply:** the screenshots, what changed, the e2e result.
