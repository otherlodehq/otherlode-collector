---
title: Set the environment and namespace
description: Label every payload with an environment or a service namespace from the collector, choose whether the collector's value or the agent's wins, read the mismatch counters, and see how test runs behave.
order: 50
---

The collector can write an environment and a service namespace onto every payload that passes through it. Use the environment to label a whole deployment such as `prod` or `staging` without touching each agent. Use the namespace to give one team's agents the same namespace. Both settings are off by default.

Each setting has a second variable that decides what happens when the agent already named a value. The default is `insert`, which keeps the agent's value. The other choice is `upsert`, which replaces it.

For the options on the agent's side, see [service identity](/docs/agent/configuration#service-identity). For how the server uses the environment, and what a run with none looks like, see [scope and environments](/docs/server/scope). The server's [connect page](/docs/server/connect#name-the-environment) has a short version of the environment setting. This page is the full one.

## What the settings do

| Variable | Default | Accepted values |
|---|---|---|
| `OTHERLODE_COLLECTOR_ENVIRONMENT` | Empty, so the collector leaves the environment alone | Any text that is valid UTF-8 |
| `OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION` | `insert` | `insert` or `upsert`, in any letter case |
| `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE` | Empty, so the collector leaves the namespace alone | Any text that is valid UTF-8, except `.` and `..` |
| `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE_ACTION` | `insert` | `insert` or `upsert`, in any letter case |

The collector reads these once, at startup. Restart it after you change them. It trims the spaces around each value and each action. A value or an action that is blank after trimming counts as not set.

The collector stamps all three payload types: delta batches, manifests, and static baselines. When it starts with a value set, it logs one line at the `info` level, such as `stamping environment on ingested payloads` or `stamping service namespace on ingested payloads`.

### How a value is stamped

For each payload, the collector compares its own value with the one the agent sent.

| The agent sent | `insert` | `upsert` |
|---|---|---|
| Nothing | The collector writes its value | The collector writes its value |
| The same value as the collector | Nothing changes | Nothing changes for a namespace. For an environment, the collector writes its own spelling |
| A different value | The agent's value stays, and the mismatch counter goes up | The collector writes its value, and the mismatch counter goes up |

The counter goes up under both actions. It counts payloads, not agents, so an agent that sends a batch every 60 seconds adds a count every 60 seconds.

### How the two settings compare values

The environment and the namespace do not compare quite the same way.

| | Environment | Namespace |
|---|---|---|
| Spaces around the collector's value | Trimmed at startup | Trimmed at startup |
| Spaces around the agent's value | Trimmed before the comparison | Trimmed before the comparison |
| Letter case | Ignored | Counts as a difference |
| An agent value of only spaces | Counts as nothing, so the collector writes its value | Counts as nothing, so the collector writes its value |

The collector compares environments the way the server does. The server trims and lowercases an environment name, so `Prod`, ` prod ` and `prod` are one environment there, and none of them is a mismatch here. Under `insert` the agent's `Prod` stays, and the server reads it as `prod`. Under `upsert` the collector writes its own `prod`.

When the namespaces match after trimming, the collector leaves the agent's text as it was, under both actions. An agent that sends ` team-a ` keeps those spaces.

## Label every payload with an environment

Run one collector for each environment and give each its own value. Agents behind the collector then report to that environment without setting `environment` themselves.

```bash
OTHERLODE_COLLECTOR_ENVIRONMENT=prod
```

An agent that names its own environment keeps it. To make the collector's value win for every agent behind it, add the action:

```bash
OTHERLODE_COLLECTOR_ENVIRONMENT=prod
OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION=upsert
```

Use `upsert` when the collector's value must win for every agent behind it, for example when a shared agent configuration sets the same `environment` for every deployment. Use `insert` when some agents should name their own.

The server fixes a run's environment from the first payload it receives for that run. Restart the agents after you change the collector's environment, so new runs start in the new one. [Scope and environments](/docs/server/scope#how-a-run-gets-its-environment) has the detail.

## Give one team's agents a namespace

A service is known by its namespace and its name together. If one collector serves one team, set the team's namespace there instead of on every agent.

```bash
OTHERLODE_COLLECTOR_SERVICE_NAMESPACE=payments
```

Add `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE_ACTION=upsert` to overwrite a namespace an agent set. The two actions work as the table above shows.

A namespace of `.` or `..` is refused. The ingest endpoints reject it from an agent for the same reason, because a service is read at a URL path and browsers drop those segments.

## Serve every namespace from one collector

Leave `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE` unset. The collector then passes each agent's namespace through unchanged, and one collector can serve many teams. An agent with no namespace stays in the unspecified namespace.

## Watch for mismatches

Two counters on `/metrics` count the payloads where the agent named a different value. Both have the label `payload`, whose value is `deltas`, `manifest` or `static_baseline`.

| Counter | Counts |
|---|---|
| `otherlode_collector_environment_mismatch_total` | Payloads whose environment differs from the collector's |
| `otherlode_collector_namespace_mismatch_total` | Payloads whose namespace differs from the collector's |

A count that keeps rising means some agent disagrees with its collector. Under `insert` that agent's service sits in the environment or namespace it chose. Under `upsert` the collector has moved it. Either way, find the agent and decide which value is right.

The first time a service sends a given value that differs from the collector's, the collector logs a `WARN` line that names the service, instance and run. Every later payload of that service with that value logs the same line at `DEBUG`, so the default `info` level shows each disagreement once.

```text
{"time":"...","level":"WARN","msg":"agent environment does not match the collector's configured environment","namespace":"payments","service":"demo","instance":"i1","run":"r1","payload":"deltas","agent_environment":"Staging","collector_environment":"prod","action":"insert"}
```

The namespace line reads `agent namespace does not match the collector's configured namespace`, with `agent_namespace` and `collector_namespace`. See [monitoring](monitoring) for the rest of the metrics.

## Handle test runs

An agent with `testRun` on and no environment of its own reports the environment `test`. Both the [configuration](/docs/agent/configuration#service-identity) and the [test runs page](/docs/agent/test-runs) say so. A collector with its own environment therefore sees `test` as a different value from its own.

- Under `insert`, the run keeps `test`. The collector counts each of its payloads as an environment mismatch.
- Under `upsert`, the collector overwrites `test` with its own value, and counts each payload as a mismatch.

Under `upsert`, the run no longer reports `test` as its environment, so the environment does not mark it as a test run. The `test_run` flag still does. It travels with every payload, and the collector never changes it. The server reads that flag to keep the run out of every finding about production.

To avoid the mismatch counts, point the test JVM at a collector with no environment set. If you use one collector for both, expect the counter to rise with every test-run payload and read it with that in mind.

## Startup errors

The collector checks both settings before it listens. Any error here stops it with exit code 1. Each message appears as the `msg` of an `ERROR` line.

| Mistake | Message |
|---|---|
| A value that is not valid UTF-8 | `OTHERLODE_COLLECTOR_ENVIRONMENT is not valid UTF-8` or `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE is not valid UTF-8` |
| An action other than `insert` or `upsert` | `OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION: invalid action "x": must be "insert" or "upsert"` |
| An action with no value | `OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION is set but OTHERLODE_COLLECTOR_ENVIRONMENT is empty; set OTHERLODE_COLLECTOR_ENVIRONMENT or unset OTHERLODE_COLLECTOR_ENVIRONMENT_ACTION` |
| A namespace of `.` or `..` | `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE is ".", which no URL path can name` |

The namespace variables give the same messages with the `OTHERLODE_COLLECTOR_SERVICE_NAMESPACE` names. The collector trims the action, so `upsert ` with a trailing space is `upsert`. An action made of only spaces counts as not set.

## Related pages

- [Configuration](configuration) lists every setting.
- [Redaction](redaction) covers the other processor on the path.
- [Monitoring](monitoring) covers the metrics endpoint.
- [Scope and environments](/docs/server/scope) explains how the server uses the environment.
- [Test runs](/docs/agent/test-runs) covers the agent's side of a test run.
