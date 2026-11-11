---
title: Add a collector
description: When to put a collector between your agents and Otherlode, what it adds and what it doesn't, and how to move an agent onto or off a collector.
order: 5
---

An agent can send straight to Otherlode at `https://ingest.otherlode.dev` with an ingest key, as the [quickstart](/docs/start/quickstart) does. A collector is an optional service you run in your network, between your agents and Otherlode. Add one when you need any of the things below.

## What a collector adds

- **Blanking.** The collector can replace the string literals in branch conditions and in string `switch` and `when` case labels before a payload leaves your network, and re-key branch and site keys under a secret only it holds. An agent that sends straight to Otherlode sends literals as your code holds them. See [Blank out string literals](redaction). While your tenant [requires redacted payloads](/docs/server/require-redaction), Otherlode refuses every payload that did not come through a collector with blanking on.
- **One place that holds the ingest key.** Your JVMs hold a token for the collector, so a JVM's environment never holds your ingest key. See [Set and rotate tokens](tokens).
- **One host that sends to the internet.** Your applications send only to the collector, inside your network. A JVM with no route to the internet still reports.
- **One place to name the environment.** The collector can fill in the environment and the service namespace on every payload it forwards. See [Set the environment and namespace](environment-and-namespace).

A collector does not make delivery safer. It keeps no queue and holds a payload only while the agent waits for Otherlode's answer, so an agent resends what Otherlode did not store whether or not a collector sits in between. [How the collector works](how-it-works) explains why.

## Move an agent onto a collector

1. Deploy the collector with an ingest key as its forward token. See [Deploy the collector](deploy).
2. Set each agent's `exportUrl` to the collector's URL, and `OTHERLODE_AUTH_TOKEN` to the collector's agent token in place of the ingest key. Restart the JVM.
3. Check that the service still reports. See [Confirm the service appears](/docs/server/connect#confirm-the-service-appears).
4. If each deployment had an ingest key of its own, revoke the keys the agents held and keep the collector's. See [Revoke a key](/docs/server/api-keys#revoke-a-key).

The run that starts after the restart sends through the collector. Runs that sent straight to Otherlode before it keep their data.

## Move an agent off a collector

Set the agent's `exportUrl` to `https://ingest.otherlode.dev` and `OTHERLODE_AUTH_TOKEN` to an ingest key, then restart the JVM. While your tenant requires redacted payloads, Otherlode refuses what the agent sends this way.
