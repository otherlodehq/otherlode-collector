# Customer docs

This guide covers the pages under `docs/site/` in the agent, collector and server repos, and `src/content/docs/start/` in `otherlode.dev`. `unslop` and `SKILL.md` apply first. Each repo's `CLAUDE.md` holds the commitments. The page format is in the repo's `docs/site/README.md`, or for `start/` pages in otherlode.dev's `README.md`.

## Who reads it

An engineer who runs the agent or reads the server's findings. They're competent, they arrive from search or a link, and they want one answer. Write the page they came for and nothing around it.

## Follow technical-writing first

Read `technical-writing` and use it. Pick the Diátaxis mode for each page. Ours are mostly how-to pages, such as attach, configure and troubleshoot, and reference pages, such as options, endpoints and findings. Keep the modes apart and link between them.

This guide adds the following.

- **Second person, present tense.** "You set `staticBaselineEnabled`", not "the user should set". Say who does what.
- **Every sentence is true of the code, and you checked it.** Read the code the page describes. If the code is wrong, that's a bug, so route it to a fix. Don't document the gap.
- **Behaviour, never layout.** A page says what a feature does and how to use it. It doesn't say where a part sits, what a long name loses at a narrow width or what a phone shows. What a status colour or tooltip tells the reader is behaviour, so it stays.
- **Plain facts, not hedges.** The agent reports observations, so say what it saw. Don't write "should", "may" or "typically" where the code is certain. Where it's conditional, state the condition.
- **Public links only.** A link to a repo points at a public one, since a private repo's path 404s for a customer. Link between pages by site path, such as `/docs/agent/classes#never-loaded`.

## Page format

A page is Markdown with `title`, `description` (one sentence) and `order` in its frontmatter. `title` becomes the `h1`, so the body starts at `##`. Headings are sentence case. Raw HTML with `<script>`, `<style>` or `style=` fails the site build.

The file name is the URL, so never rename a page. A rename reads as one page removed and another added, and the release menu can't link them. A change that adds behaviour a customer can see or set, or alters what a page documents, updates that page in the same commit.

## Where each set is edited

- **Agent and collector.** You edit them in the repo's `docs/site/`. Each release copies it into `otherlode.dev` at `src/content/docs/<section>/<tag>/`. When a release's copy is wrong about that release, fix it there, and fix `docs/site/` too if the mistake is still in it. The sync never replaces a release already on master.
- **Server.** You edit them only in `otherlode-server/docs/site/`, since the next sync replaces the copy in `otherlode.dev`.
- **Start.** You edit them in `otherlode.dev` at `src/content/docs/start/`.

## Before and after

Stiff passive to easy.

- Before: "Redaction is performed by the collector. String literals are removed from payloads prior to transmission, and fields that are not recognised are dropped."
- After: "With blanking on, the collector blanks the literals your settings select and drops every field its bindings don't know before it forwards a payload. A payload that lost a field carries `fields_stripped`."

Layout to behaviour.

- Before: "A grey badge to the right of the instance name shows when the instance has sent no counts."
- After: "An instance whose latest run hasn't delivered a delta batch shows **No counts received yet**."

Hedged to a plain fact.

- Before: "A class might be reported as never loaded if the baseline is probably complete."
- After: "A class that a complete baseline declares and no run in scope loaded is a candidate for **Never loaded**. That section lists every condition the server checks before it makes the claim."
