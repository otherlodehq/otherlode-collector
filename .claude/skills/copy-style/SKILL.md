---
name: copy-style
description: "Voice and style for customer-facing copy: web UI strings, customer docs, otherlode.dev site pages, marketing and early-access emails. Use when writing or reviewing any of it. The unslop skill applies first."
---

# Copy style

Customer-facing writing has a voice of its own on top of the plain-writing baseline. Apply `unslop` first, since it covers all writing. Then follow the guide for the kind of copy you're writing. Then check the result against the commitments in the repo's `CLAUDE.md`, which the guides point to and don't repeat.

| Kind of writing | Where it lives | Guide |
| --- | --- | --- |
| Web UI strings | `otherlode-server/web/src` | `ui.md` |
| Customer docs | `docs/site/` in `otherlode-agent`, `otherlode-collector` and `otherlode-server`, and `src/content/docs/start/` in `otherlode.dev` | `docs.md` |
| Site pages, marketing and emails | `otherlode.dev/src/pages`, `otherlode.dev/src/components`, early-access emails, anything that sells the product | `marketing.md` |
| Replies, code comments, ADRs, commit messages, `STATUS.md` | Anywhere | `unslop` alone, plus `technical-writing` for ADRs and READMEs |

The `agent/`, `collector/` and `server/` folders under `otherlode.dev/src/content/docs/` are copies, so edit the source repo's `docs/site/`. A copy and style pass leaves `src/pages/privacy.md` alone.

## Voice shared by every guide

- **Contractions and joined sentences, by judgement.** Use them where a sentence reads more comfortably, and the full form where it reads stiff or needs emphasis. See `technical-writing` "Vary the rhythm" and `unslop` rule 34.
- **Sentence-case headings.** "What it finds", not "What It Finds".
- **The codebase's own names.** Use the terms in `CONTEXT.md` and the names the product shows, and call each thing by one name.
- **British spelling in prose.** "Colour", "initialised" and "licence", as the site and docs spell them. Identifiers keep their own spelling, such as the `never-initialized` route.

## Check the copy

1. Read it aloud once. Rewrite any sentence you stumble on.
2. Check every claim against the code, since a sentence must be true of the shipped product.
3. Run `unslop` over everything you wrote, not only part of it.
4. Show customer-facing copy to the owner before you commit. For a rewrite across many pages, show one page first, as the `power-mode` docs-and-copy playbook says.
