---
status: accepted
---

# The collector answers only once Otherlode has stored the payload

Decided on 2026-11-02 in a grilling session after the soak test's audit of claims against the order evidence arrives in (server ADR 0087, agent ADR 0082).

The collector answered an agent's payload with 202 once it had queued the payload, then forwarded it from a queue. It dropped the payload when Otherlode answered a plain 500 (one attempt), any 4xx but 429 (at once), or stayed unreachable past the 5-minute retry budget. The agent had seen 202, so it never sent the payload again. A delta batch carries only the probes whose totals changed, so code that ran once, such as startup code or a static initialiser, lost its hit for good, and Otherlode then claimed that code never hit. A dropped manifest chunk left classes the run loaded absent, so Otherlode claimed them never loaded. The agent's delivery state (agent ADR 0068 and 0082) could not help: it ended at the collector.

## Decision

- **The collector forwards while the agent's request waits,** and answers from Otherlode's answer. A 2xx becomes 202. A 429 stays 429, with Otherlode's `Retry-After`. Any other 4xx keeps its status. A 5xx, a timeout or no connection becomes 503 with `Retry-After`. So every payload the agent sees accepted is stored, and every other one stays the agent's: it keeps its counts, reads as pending, and sends again on its next flush.
- **One attempt, with an 8-second deadline,** `OTHERLODE_COLLECTOR_FORWARD_REQUEST_TIMEOUT`. The agent's own attempt gives up after 10 seconds, so the agent always gets the collector's answer. The collector retries nothing: the agent's retries replace its retries.
- **No queue.** The forward queue, its shards and its retry policy go, with their six settings. `OTHERLODE_COLLECTOR_MAX_CONCURRENT_DECODES` bounds the requests in flight, since a request now decodes and forwards in one go, and a request past it gets 503. On shutdown, requests in flight finish within the drain budget and new ones get 503.
- **The collector still writes nothing to disk** and holds a payload only for the length of its request.

## Considered options

- **Keep 202 on queueing and detect loss:** each payload carries how many payloads the collector confirmed before it, and Otherlode treats a run with one missing as no evidence. Rejected: claims stay safe but the run's data is still lost, and it needs a wire field in all three repositories for a path this decision removes.
- **A disk-backed queue.** Rejected: losses become rare, not impossible (a full disk, a lasting 4xx, a lost volume), and the collector would hold payloads at rest, which the docs promise it does not.
- **Retrying a plain 500 in the queue.** Rejected as the whole fix: the retry budget still runs out in a long outage.

## Consequences

- An agent's send now takes as long as Otherlode's ingest of the payload, within 8 seconds.
- While Otherlode is down or slow, every agent reads as counts behind and makes no zero-count claim, which is what is true, and catches up from its own totals when Otherlode answers again.
- The agent's in-memory counts are the only buffer during an outage. A process that stops during one loses what it had not delivered, and reads as one that went silent.
