### Docs and copy

**Every sentence true of the shipped code. Luke approves customer copy.**

1. Read the code the page describes. Check each claim against it. When the
   code is wrong rather than the page, route it to Bug fix. If the right
   behaviour is a design change, grill it per Design.
2. Write the change yourself, since prose skips the review loop unless
   Luke asks for one. For customer-facing copy, show it before you commit. For a rewrite across many pages, show one
   page as a sample first.
3. Load `copy-style` and follow the guide its table names for the kind of
   copy. A README follows `technical-writing`. For docs, also follow the
   page format in the repo's `docs/site/README.md`, or otherlode.dev's
   `README.md` for `start/` pages. Run `unslop` over everything you wrote,
   not only part of it.
4. Leave out instructions a reader at that level already knows.
5. Keep doc links pointing at public repos. A private repo's path 404s for a
   customer.
6. Wrap-up.

**Reply:** the copy or a link to it, and which claims you checked against
which code.
