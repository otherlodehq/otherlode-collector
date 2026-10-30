# Customer docs

This guide covers the pages under `docs/site/` in the agent, collector and server repos, and `src/content/docs/start/` in `otherlode.dev`. `unslop` and `SKILL.md` apply first. Each repo's `CLAUDE.md` holds the commitments. The page format is in the repo's `docs/site/README.md`, or for `start/` pages in otherlode.dev's `README.md`.

## Who reads it

An engineer who runs the agent or reads the server's findings. They're competent, they arrive from search or a link, and they want one answer. Write the page they came for and nothing around it. Leave out what they'd work out for themselves or wouldn't miss.

## Follow technical-writing first

Read `technical-writing` and use it. Pick the Diátaxis mode for each page. Ours are mostly how-to pages, such as attach, configure and troubleshoot, and reference pages, such as options, endpoints and findings. Keep the modes apart and link between them.

This guide adds the following.

- **Second person, present tense.** "You set `staticBaselineEnabled`", not "the user should set". Say who does what.
- **Every sentence is true of the code, and you checked it.** Read the code the page describes. If the code is wrong, that's a bug, so route it to a fix. Don't document the gap.
- **Write what the screen can't show.** A page says what a feature does and how to use it: what a status means, how a thing is judged, what a count includes, why something is missing. It leaves out what the screen already shows: where a part sits and how it looks, and a control that explains itself, such as a filter box, paging or a row that expands. Name a part so a reader can find it ("the tenant menu in the top bar"), and say what a colour or tooltip tells them. Quote a message a reader may search for. A how-to still ends with the check that it worked.
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

What the screen shows to what it can't.

- Before: "**Last seen**: How long ago any production run of the service last reported, such as "3 days ago". The tooltip holds the exact UTC time. A service with no runs left reads "never"."
- After: "**Last seen**: How long ago any production run of the service last reported. A service whose runs have all expired reads "never"."

Hedged to a plain fact.

- Before: "A class might be reported as never loaded if the baseline is probably complete."
- After: "A class that a complete baseline declares and no run in scope loaded is a candidate for **Never loaded**. That section lists every condition the server checks before it makes the claim."
