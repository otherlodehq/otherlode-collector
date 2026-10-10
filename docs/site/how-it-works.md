---
title: How the collector works
description: Why the collector sits in your network and keeps nothing, why it answers 503 instead of buffering, how it keeps one instance's payloads in order, why it refuses to start on a bad setting, and what happens when the collector and the agent are different versions.
order: 100
---

The collector is a small HTTP service between your agents and Otherlode. This page explains the choices behind it, so you can judge what it does with your data and how it behaves when something is wrong. For the settings, see [forwarding](forwarding), [redaction](redaction), [tokens](tokens) and [limits](limits).

## The collector is the last stop before data leaves your network

Agents send to the collector, and only the collector sends to Otherlode. Your applications never need a route to the internet. Everything that decides what leaves happens in one place that you run and configure.

That place does four things with each payload. It decodes the protobuf body and checks that it names a service, an instance and a run. It processes the payload by stamping the environment and service namespace, and by blanking string literals and replacing branch and site keys, if you turned those on. It forwards the result. Then it forgets it.

```text
agent --> collector (decode, check, process) --> Otherlode
          your network
```

The collector writes no payload to disk. It holds payloads in memory only while they wait in the forwarding queue, and a restart drops whatever the shutdown drain could not deliver. With no forward URL set, it logs each payload's identity and sizes and does nothing else with it.

The consequence is that nothing at rest in the collector can leak, be backed up by mistake or need a retention policy. The cost is that a crash loses whatever sat in the queue. The next section says why that cost is small.

## It answers 503 when it cannot take a payload

When a payload's queue is full, or the collector is shutting down, it answers 503 and queues nothing. The same happens when every decode slot stays busy for 5 seconds. The collector does not grow its queue and does not drop an older payload to make room.

The reason is what the agent does with the answer. The agent sends running totals, not changes, and it remembers the total the collector last confirmed for each probe. A probe goes into a payload only when its total differs from that remembered value. See [what the agent sends](/docs/agent/data-sent#when-the-collector-is-down-or-refuses-a-payload).

So the answer decides what the agent does next.

- A 503 leaves the remembered total where it was. The agent builds the payload again on its next flush, with the live totals.
- A 202 moves the remembered total forward. The agent will not mention that probe again until its count changes.

A probe that runs once a quarter shows the difference. If the collector answered 202 and then lost the payload, the agent would consider that probe reported. Its count would not change, so nothing would resend it, and Otherlode would see a probe that looks unused. A refused payload costs one flush interval. An acknowledged and lost payload can cost the finding.

The collector therefore answers 202 only after the payload is in its queue, and it refuses otherwise. A payload can still be lost after the 202 if the collector crashes, if Otherlode refuses it for good, or if it stays unreachable past the retry budget. Each of those drops is counted and logged, and [forwarding](forwarding) lists them. The queue has a fixed size, so a slow Otherlode cannot make the collector grow without limit.

## Payloads from one instance keep their order

The collector splits its forwarding queue into shards, 8 by default. It picks a shard by hashing the service namespace, service name and instance id of each payload, and one worker per shard sends in order. The run id is left out of the hash, so an instance that restarts stays on the same shard.

Two things follow. First, an instance's manifest, delta batches and static baseline reach Otherlode in the order they arrived. Second, a slow or failing Otherlode response holds up only the instances that hash to the same shard. The others keep flowing. An instance whose queue is full gets 503 while the rest still get 202.

Sharing a shard is by chance, not by design. With 8 shards, roughly one instance in eight shares a queue with any given instance. Raise the shard count if a fleet is large and a stuck instance affects too many neighbours. See [forwarding](forwarding) for the variables.

## It fails closed

Every setting is read once at startup. If the collector cannot tell what you meant, it stops with an error and exits with code 1. It does not guess, and it does not run in a weaker mode.

- With no agent token set, it refuses to start. You opt out with `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH` and accept an open endpoint. See [tokens](tokens).
- An invalid number, duration, pattern, boolean or URL stops startup. Setting a forward key with no forward URL stops it too, because you meant to forward.
- A typo in `OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS` cannot leave blanking off. The typo stops startup. A typo in `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH` stops startup too.
- With blanking on and no usable secret, it refuses to start, because plain branch and site keys let anyone test guesses at a blanked literal. A secret set with blanking off stops startup too, because you meant to protect the keys. See [replace branch and site keys](redaction#replace-branch-and-site-keys).
- With blanking on, a string part of a kind the collector does not recognise is treated as a literal and blanked. The collector drops any field it does not recognise. A newer agent that adds a field able to carry text cannot get it past an older collector.

A collector that quietly ran without a token, or without the blanking you asked for, would be the more dangerous failure. A container that exits at startup is easy to see and easy to fix.

The agent makes the opposite choice. It runs inside your application, so no invalid option stops your application. It logs a warning and uses the default. See [the agent's configuration](/docs/agent/configuration). The collector is a separate process whose only job is to apply your rules, so stopping is safe there.

## Run the latest collector

The collector has its own version, released apart from the agent. Run the `latest` image tag, and update it when you update agents.

The code shows what happens in each direction.

- **A newer collector, an older agent.** The older agent's payload decodes as before. A field the agent does not send is absent, which protobuf reads as unset. Nothing is stripped and nothing is flagged.
- **An older collector, a newer agent, blanking off.** The collector decodes the payload and forwards it with the new fields intact. It changes nothing it does not know.
- **An older collector, a newer agent, blanking on.** The collector drops the fields it does not know, sets `fields_stripped` on the payload and logs one WARN per run. The server stores the run and makes no claim from it, because a dropped field may be one the server judges by. See [a run from an older collector makes no claim](/docs/server/how-it-works#a-run-from-an-older-collector-makes-no-claim).

The code gives no promise beyond that. Upgrading the collector fixes the third case on the next run. See [redaction](redaction) for the log line and the fix.

## What it does not do

- It does not serve TLS. Put a TLS proxy in front of it if agents reach it over a network you do not trust.
- It does not aggregate or merge counts. Each payload leaves as one payload.
- It does not keep a queue on disk. The queue is in memory and bounded.
