# Marketing copy

This guide covers the otherlode.dev pages, early-access emails and anything else that sells the product. `unslop` and `SKILL.md` apply first.

## Who reads it

An engineer or engineering lead deciding whether to try a tool that finds code a Java, Kotlin or Scala service never runs in production. They're busy, they've read a lot of vendor pages and they want to know what the tool observes, where it runs and what leaves their network. Write for that person, and answer those questions before they ask.

## Voice

This is the warmest of the three guides. Say "you" and "we", use contractions and have a view. A headline makes one point in a few words ("Every replica, one answer.", "We're in early access."). Be confident without hype. A plain fact beats an adjective, and a concrete example, such as an endpoint nobody calls, beats both.

`unslop` rules 7 and 31 ban the usual hype words. Add "powerful", "unlock", "supercharge", "next-generation" and "best-in-class".

## What it may claim

Every claim is true of the shipped product today. `otherlode.dev/CLAUDE.md` holds the rules under "Claims", "Third parties, cookies and analytics" and "Privacy policy". Read it before you write. It bans planned features and invented numbers, customers or testimonials, and it keeps the privacy policy out of any style pass.

The maintenance and vulnerability-surface angle comes from "What the product is sold on" in ADR 0072 (`otherlode-server/docs/adr/`). Every method, endpoint and jar costs review, upgrade and patch work whether production runs it or not, and Otherlode shows which ones it never ran. The ADR sets three limits on this copy:

- The product observes and never decides. Say what the agent saw, such as "never loaded in production", and never say code is "safe to remove".
- The product doesn't match dependencies to CVEs. Never say it finds vulnerabilities, or that a CVE in an unloaded library can't be exploited.
- Give no count of removed CVEs or saved hours until a customer has measured one.

Copy about plans follows the ADR and the records that amend it, since they set what each plan holds. Publish no price until `otherlode-server/STATUS.md` stops calling the figures starting figures.

## Before and after

Hype to a plain claim.

- Before: "Otherlode is a powerful platform that seamlessly uncovers dead code across your entire estate."
- After: "Run the agent on every replica. Code only counts as never run when none of them ran it."

Stiff to easy.

- Before: "The collector is capable of blanking out string literals prior to the transmission of reports from your network."
- After: "The collector can blank out string literals before reports leave your network."

A feature list to a benefit stated as a fact.

- Before: "Features include endpoint tracking, branch coverage and dependency analysis."
- After: "See the endpoint nobody calls, the branch that's never true and the jar nothing loads."

An over-claim to an observation.

- Before: "Find dead code and delete it safely."
- After: "Find the code production never ran, then decide what goes."

## Headlines and buttons

- Headlines are sentence case and short. A headline that makes a point ends in a full stop ("Delete it in one go."). A label for a section doesn't ("What it finds", "How it works").
- A button is a verb phrase that says what happens next: "Ask for early access", "Send request", "Go to the homepage". A nav link can be a noun ("Early access", "Docs").
- Read `src/pages/index.astro` for the current voice before you add a page.
