---
status: accepted
---

# The forwarder logs a notice the backend sends with an accepted payload

Decided on 2026-10-29 in a grilling session on the hosted server's service cap (server ADR 0083).

The hosted server stores a payload from a service past its plan's service cap, or from an environment past its environment cap, and judges neither. It answers 202, as for any payload it took, since a refusal would be retried for good. The forwarder reads nothing from a 2xx response, so the collector's log said nothing, and a team learned of the cap only from the UI. The server's plans require the collector's log to name a service over the cap.

## The design

- **A sink can return notices.** Each `ingest.Sink` method returns the notices for the payload it took, beside its error. A notice is a short lowercase name from a closed set that `ingest` exports: `service-over-cap` and `environment-not-tracked`.
- **The handler sends them on the 202.** It writes each notice the sink returned as an `Otherlode-Notice` header on the 202. A response with no notice is unchanged.
- **The forwarder logs and counts them.** When a 2xx response from the backend carries `Otherlode-Notice`, the forwarder logs a WARN that names the notice and the item's service, at most once an hour for each notice and service. It counts every notice in `otherlode_collector_forward_notices_total`, labelled by notice. A value outside the set counts as `unknown`, and its log line shows the value only when it is at most 64 characters of lowercase letters and hyphens.
- **The collector's own sink returns none.** The forwarder queues a payload before the backend answers, so a notice reaches the collector's log, not the agent that sent the payload.

## Considered options

- **A JSON body on the 202.** Rejected: a body needs a format and a size limit, and the forwarder would read every 2xx body. A header carries one name.
- **A notice attached to the request context.** Rejected: a sink would report through a side channel the `Sink` interface does not show, and a sink that forgets to call it compiles.
- **Pass the notice back to the agent.** Rejected: the forwarder answers the agent before it posts to the backend, so the notice does not exist yet. The agent also has no log a team reads for this.
- **Log every notice.** Rejected: a service past the cap sends a payload every flush, so its log line would repeat every few seconds.

## Consequences

- A collector before this change ignores the header, so it serves every backend as before.
- A newer server can add a notice. An older collector counts it as `unknown` and logs it, so it is visible before the collector knows its name.
- `docs/site/troubleshooting.md` names each log line, and `docs/site/monitoring.md` lists the metric.
