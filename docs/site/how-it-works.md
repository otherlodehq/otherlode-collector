---
title: How the collector works
description: Why the collector sits in your network and keeps nothing, why it answers 202 only once Otherlode has stored the payload and 503 otherwise, why it refuses to start on a bad setting, and what happens when the collector and the agent are different versions.
order: 100
---

The collector is a small, optional HTTP service between your agents and Otherlode. [Add a collector](add) says when you need one. This page explains the choices behind it, so you can judge what it does with your data and how it behaves when something is wrong. For the settings, see [forwarding](forwarding), [redaction](redaction), [tokens](tokens) and [limits](limits).

## The collector is the last stop before data leaves your network

When agents send through the collector, only the collector sends to Otherlode, and your applications never need a route to the internet. Everything that decides what leaves happens in one place that you run and configure.

That place does four things with each payload. It decodes the protobuf body and checks that it names a service, an instance and a run. It processes the payload by stamping the environment and service namespace, and by blanking string literals and replacing branch and site keys, if you turned those on. It forwards the result and waits for Otherlode's answer. Then it forgets it.

```text
agent --> collector (decode, check, process) --> Otherlode
          your network
```

The collector writes no payload to disk. It holds a payload in memory only for the length of its request. With no forward URL set, it logs each payload's identity and sizes and does nothing else with it.

The consequence is that nothing at rest in the collector can leak, be backed up by mistake or need a retention policy. The cost is that the collector cannot hold a payload for later. The next section says why that costs nothing.

## It answers 202 only once Otherlode has stored the payload

The collector forwards each payload to Otherlode while the agent's request waits. It makes one attempt and answers from Otherlode's answer. A `202` from Otherlode becomes `202`. A `429` stays `429`, with Otherlode's `Retry-After`. Any other `4xx` keeps its status. A `5xx`, a timeout, no connection, a full forward limit or a shutdown becomes `503` with `Retry-After: 5`. [Forwarding](forwarding) has the full table.

The reason is what the agent does with the answer. The agent sends running totals, not changes, and it remembers the total the collector last confirmed for each probe. A probe goes into a payload only when its total differs from that remembered value. See [what the agent sends](/docs/agent/data-sent#when-the-receiver-is-down-or-refuses-a-payload).

So the answer decides what the agent does next.

- Any answer but `202` leaves the remembered total where it was. The agent builds the payload again on its next flush, with the live totals.
- A `202` moves the remembered total forward. The agent will not mention that probe again until its count changes.

A probe that runs once a quarter shows the difference. If the collector answered `202` before Otherlode had the payload, and the payload was then lost, the agent would consider that probe reported. Its count would not change, so nothing would resend it, and Otherlode would see a probe that looks unused. A payload that is refused costs one flush interval. A payload that is acknowledged and lost can cost the finding.

So every payload an agent sees accepted is stored, and every other payload stays with the agent. The collector has no queue, retries nothing and drops nothing. The agent resends what Otherlode did not store, and the agent's memory is the only buffer. While Otherlode is down, every agent reads as behind and makes no zero-count claim, which is what is true. Each catches up from its own totals when Otherlode answers again. The one loss is an agent process that stops during the outage. Its undelivered counts go with it, and it reads in Otherlode as one that went silent.

The price is time. An agent's send takes as long as Otherlode's ingest of the payload, within 8 seconds by default. A request has 9 seconds in all, so the agent gets the collector's answer before it gives up. A slow Otherlode shows as `503` answers and a rise in `forward_failed_total`, not as a growing queue in the collector.

## It fails closed

Every setting is read once at startup. If the collector cannot tell what you meant, it stops with an error and exits with code 1. It does not guess, and it does not run in a weaker mode.

- With no agent token set, it refuses to start. You opt out with `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH` and accept an open endpoint. See [tokens](tokens).
- An invalid number, duration, pattern, boolean or URL stops startup. Setting a forward key with no forward URL stops it too, because you meant to forward.
- A typo in `OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS` cannot leave blanking off. The typo stops startup. A typo in `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH` stops startup too.
- With blanking on and no usable secret, it refuses to start, because plain branch and site keys let anyone test guesses at a blanked literal. A secret set with blanking off stops startup too, because you meant to protect the keys. See [replace branch and site keys](redaction#replace-branch-and-site-keys).
- With blanking on, a string part of a kind the collector does not recognise counts as a literal, so the blanking settings apply to it. The collector also drops any field it does not recognise. A newer agent that adds a field able to carry text cannot get it past an older collector.

A collector that quietly ran without a token, or without the blanking you asked for, would be the more dangerous failure. A container that exits at startup is easy to see and easy to fix.

The agent makes the opposite choice. It runs inside your application, so no invalid option stops your application. It logs a warning and uses the default. See [the agent's configuration](/docs/agent/configuration). The collector is a separate process whose only job is to apply your rules, so stopping is safe there.

## Run the latest collector

The collector has its own version, released apart from the agent. Run the `latest` image tag, and update it when you update agents.

The code shows what happens in each direction.

- **A newer collector, an older agent.** The older agent's payload decodes as before. A field the agent does not send is absent, which protobuf reads as unset. Nothing is stripped and nothing is flagged.
- **An older collector, a newer agent, blanking off.** The collector decodes the payload and forwards it with the new fields intact. It changes nothing it does not know.
- **An older collector, a newer agent, blanking on.** The collector drops the fields it does not know, sets `fields_stripped` on the payload and logs one WARN per run. Otherlode stores the run and makes no claim from it, because a dropped field may be one it judges by. See [a run from an older collector makes no claim](/docs/server/how-it-works#a-run-with-dropped-or-unknown-fields-makes-no-claim).

The code gives no promise beyond that. Upgrading the collector fixes the third case on the next run. See [redaction](redaction) for the log line and the fix.

## What it does not do

- It does not serve TLS. Put a TLS proxy in front of it if agents reach it over a network you do not trust.
- It does not aggregate or merge counts. Each payload leaves as one payload.
- It does not keep a queue, in memory or on disk, and it retries nothing.
