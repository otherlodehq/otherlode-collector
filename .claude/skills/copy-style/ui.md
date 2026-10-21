# Web UI copy

This guide covers every string in `otherlode-server/web/src`: labels, buttons, empty states, errors, badges and tooltips. `unslop` and `SKILL.md` apply first. `otherlode-server/CLAUDE.md` holds the commitments under "UI copy states facts" and "The browser renders served facts".

## Who reads it

Someone in the middle of a task. They opened the page to check a number, find a service or fix a setting, and they read a string only if it's in the way. Give them the fact and get out of the way.

## Voice

This is the tersest of the three guides. State the fact in as few words as the reader needs. Use contractions only where the full form reads stiff. A status colour and a number beat a sentence, so reach for a badge or a count first. The UI never explains or documents the product. `docs/site` does that, and the UI links to it.

## What a string may do

- State a fact: "Enforce SSO: on".
- Name a status: "verified", "pending", "failed".
- Give a number: "3 instances".
- Link to a docs page ("Docs").

## What a string may not do

- Explain how the product works, such as how a status is judged or what a column means.
- Add a caution, banner or second line that teaches. If a person needs the background, link the docs page.
- Describe the layout or tell the person where to look ("see the table below").
- Soften or hedge ("Note that", "Please", "It looks like").

A confirmation may say what the action will do to the person's data or sessions, since that's a fact about the action. It doesn't say how the product works in general.

## Labels, buttons, empty states and errors

- **Sentence case** everywhere. "Verified domains", not "Verified Domains". Product terms keep their own case ("WorkOS", "SSO").
- **A label is a noun.** A button is a verb phrase that says what happens: "Set up SSO", "Verify a domain", "Stop enforcing SSO".
- **An empty state says what's missing**, in the product's own words. "No domain yet." "No service has reported yet." It doesn't say how to fill it.
- **An error says what happened and what the person can do.** Name the thing, state the result and give the next step if there is one. "The Admin Portal could not be opened." "Another tenant holds example.com, so it was not added. Contact support to move it." Never "Oops" or "Something went wrong", and never blame the person.
- **Names come from the server.** The browser renders what the server sent. It never decodes a name or builds a label the server didn't send. If a string needs a new name, the server sends it, and the UI shows it as given.
- **Use the codebase's terms** from `CONTEXT.md`, and one name per thing.

## Before and after

An explanation to a fact, with the explanation moved to the docs.

- Before: "No runs in scope. Runs drop out of scope when they fall outside the version or window you picked."
- After: "No runs in scope." Link "Docs" to the scope page if a person needs the background.

A caution to a status.

- Before: "Heads up: this run hasn't delivered all its counts yet, so its numbers may be incomplete."
- After: a "Counts behind" badge.

A vague error to a specific one.

- Before: "Something went wrong while refreshing."
- After: "The domains could not be refreshed from WorkOS." Add the server's reason as the next sentence.

A sentence to a number and a status.

- Before: "This service has three instances and all of them are reporting normally."
- After: "3 instances", with a "live" badge.
