# Web UI copy

This guide covers every string in `otherlode-server/web/src`: labels, buttons, empty states, errors, badges and tooltips. `unslop` and `SKILL.md` apply first. `otherlode-server/CLAUDE.md` holds the commitments under "UI copy states facts" and "The browser renders served facts".

## Who reads it

Someone in the middle of a task. They opened the page to check a number, find a service or fix a setting, and they read a string only when they need it. Give them the fact and nothing else.

## Voice

This is the tersest of the three guides. State the fact in as few words as the reader needs. A status colour and a number beat a sentence, so reach for a badge or a count first. The UI never explains or documents the product. `docs/site` does that, and the UI links to it.

## What a string may do

- State a fact: "Enforce SSO: on".
- Name a status: "Counts behind", "Never called".
- Give a number: "3 instances".
- Link to a docs page ("Docs").

## What a string may not do

- Explain how the product works in general, such as how a status is judged or what a column means. There are three exceptions:
  - A finding page's one-line definition in its header.
  - A status's tooltip or second line, in one sentence. It names the fact behind the status, and it may add one clause on what that fact means for this item's findings: "No delta batch has arrived, so no zero-count claim rests on this run." The general mechanics stay in the docs: "the counts it has are kept and summed".
  - A setting's second line. It may say what turning the setting on or off does to data, sign-in and who can do what: "When it is on, a person who signs in through SSO … gets a viewer at once." Setup steps and caveats stay in the docs: "You prove a domain with a DNS record".
- Tell the person how to fix a problem. A tooltip names the problem, and the fix is on a docs page. A tooltip cannot hold a link, so a "Docs" link sits beside the badge.
- Add a caution, banner or second line that teaches. If a person needs the background, link the docs page.
- Describe the layout or tell the person where to look ("see the table below").
- Soften or hedge ("Note that", "Please", "It looks like").

A confirmation may say what the action will do to the person's data or sessions, since that's a fact about the action. It doesn't say how the product works in general.

## Labels, buttons, empty states and errors

- **Sentence case** everywhere. "Verified domains", not "Verified Domains". Product terms keep their own case ("WorkOS", "SSO"), and a value the server sends, such as a WorkOS state, renders as sent.
- **A label is a noun.** A button is a verb phrase that says what happens: "Set up SSO", "Verify a domain", "Stop enforcing SSO".
- **An empty state says what's missing**, in the product's own words. "No domain yet." "No service has reported yet." It doesn't say how to fill it.
- **An error says what happened and what the person can do.** Name the thing, state the result and give the next step if there is one. "The Admin Portal could not be opened." "The domains could not be refreshed from WorkOS.", followed by the server's reason. Never "Oops" or "Something went wrong", and never blame the person.
- **Names come from the server.** The browser renders the names the server sends and never decodes one. It may join names the server sent with fixed words, as in "`handleCheckout` in DemoServerMain.kt" or "first lambda in `main` at line 12" (ADR 0035, ADR 0036), and builds no other label. A string that needs a new name waits for the server to send it.

## Before and after

An explanation to a fact, with the explanation moved to the docs.

- Before: "No run in scope. Runs drop out of scope when their environment, version or last report falls outside what you picked."
- After: "No run in scope." Link "Docs" to the scope page if a person needs the background.

A caution to a status.

- Before: "Heads up: this run hasn't delivered all its counts yet, so its numbers may be incomplete."
- After: a "Counts behind since" badge with its time.

A vague error to a specific one.

- Before: "Something went wrong while refreshing."
- After: "The domains could not be refreshed from WorkOS." Add the server's reason as the next sentence.

A sentence to a number and a status.

- Before: "This service has three instances, and two of them sent a complete baseline."
- After: "3, 2 with a complete baseline".
