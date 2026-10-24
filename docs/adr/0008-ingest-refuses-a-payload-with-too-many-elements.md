---
status: accepted
---

# Ingest refuses a payload with too many elements before it decodes it

Decided on 2026-10-24 in a grilling session that followed a security audit of the collector.

A protobuf body decodes to far more memory than its size. An empty element of a repeated message field costs 2 bytes on the wire and a whole struct once decoded, so one 16 MiB body of empty probe locations decodes to gigabytes. The 16 MiB body limit and the decode cap bound the bytes read and the number of decodes, not the size of one decode. Any holder of an agent token could therefore make one request take more memory than the collector's container holds.

The hosted server already counts elements before it decodes, in its own middleware, with limits it measured through its real ingest route. The collector takes the same counter.

## The design

- **Count before decoding.** After the handler reads the body, and before it takes a decode slot, it walks the wire format against the payload's message descriptor. It counts every embedded message and every element of a repeated field, at every depth. The resource counts as one. The walk allocates nothing per element.
- **A fixed limit per payload kind.** 500,000 elements for a delta batch, 100,000 for a manifest, 200,000 for a static baseline and 100,000 for a class listing. These are the server's limits. At one element under each limit, the server measured a peak of at most about 230 MiB for one request.
- **Refuse with 413.** A payload over its limit gets 413 and the body `payload has too many elements`, and the collector counts it under the reason `too_many_elements`. The walk also refuses a body nested deeper than 32 levels, since the schema nests five.
- **The limits are exported.** `ingest` exports the four limits so that the server and the agent's tests can name them.
- **The server keeps its own check.** The server runs `ingest.Handler` in its own process, so the count runs there on every request it receives, whatever the collector in front of it did. The server never relies on a collector having counted. Its limit on body bytes held across requests stays in the server.

## Considered options

- **Estimate the decoded heap from struct sizes.** Rejected: a second mechanism beside the server's, with numbers nobody measured. The element count is what the decode allocates per element, and the server's numbers already bound it.
- **An operator setting for the limits.** Rejected: a setting name is frozen once published, and the limits follow from the schema and the agent's chunk sizes, not from a deployment. A setting can be added later if a deployment needs one.
- **Rely on `GOMEMLIMIT` and a container limit.** Rejected as the only control: `GOMEMLIMIT` is a soft target for the garbage collector and cannot bound memory that is still reachable, so a container limit only turns the overrun into an OOM kill.

## Consequences

- One decode takes at most about 230 MiB. The worst-case memory sum in `docs/site/limits.md` uses that figure in place of the open-ended decoding term.
- A newer collector still serves every older agent. The agent's chunks stay far below these limits, apart from a single class heavier than a limit, which the server already refuses.
- The counter's source is public. Its limits were already visible to anyone who sends a payload and reads the 413.
