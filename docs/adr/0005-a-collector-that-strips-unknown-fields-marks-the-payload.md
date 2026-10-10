---
status: accepted
---

# A collector that strips unknown fields marks the payload

Decided on 2026-10-04 in a grilling session with Luke, reviewing agent ADR 0054's wire surfaces before release (agent `STATUS.md`, pre-release checklist item 4). This amends ADR 0001.

While redaction is on, ADR 0001 strips every field this collector's bindings do not know, so that a newer agent's literal-carrying field fails closed. A newer field can also carry meaning the server judges by. Agent ADR 0054 added `unread_shape`: an older collector with redaction on would have dropped it, and the server would have read every unread shape as the adopter's code and reported it as a confident never-hit, the failure agent ADR 0007 rules out. Whatever detection the first released collectors lack cannot be added to them later, so the mark is in from the start.

When the redaction processor removes at least one unknown field from a payload, it sets `ResourceAttributes.fields_stripped` on that payload and logs one WARNING per instance naming the agent version. The server keeps the data, makes no never-hit or cluster claim from a run that carried the flag, and labels the run as sent through a collector older than its agent (server ADR 0054). Agent, testkit and collector are released in lockstep (agent ADR 0057), so the fix the label points to is one upgrade.

## Considered options

- **The flag, with the server still judging and showing a banner.** Rejected: a banner beside a wrong finding is still a wrong finding.
- **Rejecting a payload with unknown fields while redaction is on.** Rejected: every agent upgrade that got ahead of its collector would lose data until the collector caught up.
- **Documenting "the collector must be at least as new as the agent" and nothing else.** Rejected: it leaves the silent case in place.

## Consequences

- An unknown enum number is not an unknown field and is never stripped, so it does not set the flag; ADR 0001's fail-closed rule for literal kinds is unchanged.
- With redaction off, nothing is stripped and nothing is flagged: Go protobuf forwards unknown fields as it received them.

## Amended on 2026-10-05: the latest collector, not the agent's version

The collector is versioned on its own (ADR 0006), so the fix a stripped run points to is upgrading to the latest collector, which serves every older agent. The WARNING and the README say that instead of naming the agent's version.

## Amended on 2026-10-10: one WARNING per run

The redaction processor logs the WARNING once per run id, not once per instance. A run is what the server labels, and an instance restarted under a pinned instance ID starts a new run that needs its own warning.
