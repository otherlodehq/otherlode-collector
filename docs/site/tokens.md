---
title: Set and rotate tokens
description: Set the agent token from a variable or a file, rotate it without refusing an agent, set and rotate the forward key, and see how token files and mounted secrets are re-read.
order: 30
---

The collector holds two separate secrets. The agent token is what agents present to the collector. The forward key is what the collector presents to the backend it forwards to. They need not match, and each has its own variable and its own file variable. A collector with redaction on holds a third secret, which it never sends. See [replace branch and site keys](redaction#replace-branch-and-site-keys).

This page sets and rotates both. Agents send `Authorization: Bearer <token>` on every request to `/v1/otherlode/deltas`, `/v1/otherlode/manifest` and `/v1/otherlode/static-baseline`. A request with no matching token gets `401` with `WWW-Authenticate: Bearer realm="otherlode-collector"`, and the collector counts it in `otherlode_collector_auth_rejected_total`. `/healthz` and `/metrics` never need a token. See [monitoring](monitoring).

## Set the agent token

The collector refuses to start with no agent token. Set one of two variables.

| Variable | Holds |
|---|---|
| `OTHERLODE_COLLECTOR_AUTH_TOKEN` | One token, or a comma-separated list of tokens. |
| `OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE` | The path of a file with one token per line. |

Setting both stops the collector at startup. So does setting neither, unless you also set `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH` to a true value such as `1`, which runs the ingest routes with no authentication. Use that opt-out on a machine nothing else can reach.

```bash
OTHERLODE_COLLECTOR_AUTH_TOKEN=s3cret
```

In a variable, spaces around each token are trimmed. A token cannot contain a comma. An empty entry, such as `a,,b`, a trailing comma, or a value of only spaces, stops the collector at startup with an error like this one.

```text
{"level":"ERROR","msg":"OTHERLODE_COLLECTOR_AUTH_TOKEN: entry 2 of 3 is empty"}
```

Set the same token on every agent as [`authToken`](/docs/agent/configuration#authtoken). The collector compares the token exactly, so a different case or a stray character is a mismatch.

## Keep a token out of the environment

A token in a variable shows up in container inspection and to anyone who can read the process's environment. To avoid that, put the token in a file and name the file.

```bash
OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE=/run/secrets/collector-tokens
```

The collector reads the file like this:

- One token per line.
- Blank lines and lines whose first non-space character is `#` are skipped. A token in a file cannot start with `#`.
- Spaces around each line are trimmed.
- A UTF-8 byte order mark at the start of the file is dropped.
- The path must be a regular file. A symlink to a regular file works. A directory or a named pipe does not.
- The file can be at most 1 MiB (1048576 bytes).

```text
# agents on the old token until the fleet has rolled
old-token
new-token
```

The collector reads the file again every 30 seconds. A token you add or remove applies without a restart. The log records the change by count and never prints a token.

```text
{"level":"INFO","msg":"token file changed","file":"auth","tokens":2}
```

### When a file cannot be read

At startup the collector has no earlier value to fall back on. A file that cannot be read, or that holds no token, stops the collector. This holds even with `OTHERLODE_COLLECTOR_INSECURE_NO_AUTH` set, because you asked for that file. The error starts with the variable name.

```text
{"level":"ERROR","msg":"OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE: open /run/secrets/collector-tokens: no such file or directory"}
```

After startup, a re-read that fails keeps the tokens the collector last read. A failure is a file that is missing, unreadable, not a regular file, over 1 MiB, or empty of tokens. The empty case includes a file that holds only comments. Keeping the last good tokens means a bad re-read cannot lock every agent out. Each failed re-read logs a warning and adds 1 to `otherlode_collector_token_reload_failures_total{file="auth"}`.

```text
{"level":"WARN","msg":"token file re-read failed; keeping the last good value","file":"auth","error":"open /run/secrets/collector-tokens: no such file or directory"}
```

The first good re-read after a failure logs `token file re-read recovered`. The counter shows `0` from startup, so an alert on its increase sees the first failure. Alert on it, because the collector keeps serving on the old tokens and nothing else tells you the new ones are not in.

A re-read can catch a file that is half written, and the collector applies whatever tokens it finds. A cut-off token then fails to match until the next re-read. To avoid this, write a new file and rename it over the old one.

## Rotate the agent token

You can rotate the token without refusing any agent. The collector accepts a request that matches any listed token, so old and new agents both get through while the fleet moves.

1. Add the new token beside the old one. In a variable, list both and restart the collector. In a file, add a line. The collector picks it up within 30 seconds.

	```bash
	OTHERLODE_COLLECTOR_AUTH_TOKEN=old-token,new-token
	```

2. Set the new token as `authToken` on every agent and restart the agents.
3. Watch `otherlode_collector_auth_rejected_total`. It stops rising once no agent presents the old token.
4. Remove the old token. In a variable, set only the new token and restart. In a file, delete the line.

A request that carries a removed token gets `401` from the next request on. Move every agent before you remove the old token. See [the agent's configuration](/docs/agent/configuration#authtoken) for the agent side.

## Change a mounted secret

The collector reads the path it was given, every 30 seconds, and takes the file's current content. Whether a new value reaches that path depends on how the secret is mounted. This is platform behaviour, not collector behaviour.

| How the file arrives | What happens when you change the secret |
|---|---|
| Kubernetes secret mounted as a volume | Kubernetes updates the files in place after its next sync, often a minute or two. The collector sees the new value at its next re-read after that. |
| Kubernetes secret mounted with `subPath` | Kubernetes never updates the file. The collector keeps the old tokens until the pod restarts. |
| A single file bind-mounted into a container, such as a Compose `secrets:` entry with `file:` or `-v ./tokens:/run/secrets/tokens` | The container holds the file it saw at start. Edit that file in place, or mount its directory instead. Renaming a new file over it leaves the container reading the old one, with no warning. |
| Docker Swarm secret | A secret never changes in place. Rotating one replaces the container, and the new container reads the new file at startup. |
| A plain file on the host, or a file in a mounted directory | Write a new file and rename it over the old one. The collector opens the path on every re-read, so it sees the new file whole. |

Check that a rotation took effect by looking for the `token file changed` log line, or by sending a request with the new token.

## Set the forward key

The forward key applies only when you set `OTHERLODE_COLLECTOR_FORWARD_URL`. The collector sends it as `Authorization: Bearer <key>` on each forwarded request. Create the key in Otherlode, as [Send data from your collector](/docs/server/connect) describes.

| Variable | Holds |
|---|---|
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN` | The key. Spaces around it are trimmed. |
| `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE` | The path of a file that holds the key. |

Each of these stops the collector at startup:

- Setting both variables.
- Setting either variable while `OTHERLODE_COLLECTOR_FORWARD_URL` is empty.
- A `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN` of only spaces.
- A key with a control character, which an HTTP header cannot carry.
- A file that cannot be read, or that does not hold exactly one key.

The file follows the same rules as an agent token file, with one difference. It must hold exactly one key, because the collector sends one key at a time. The overlap during a rotation lives on the Otherlode side, where a tenant can hold several live keys.

```bash
OTHERLODE_COLLECTOR_FORWARD_URL=https://ingest.otherlode.dev
OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN_FILE=/run/secrets/forward-key
```

The collector re-reads a forward key file every 30 seconds and sends the new key from the next request on. A re-read that fails keeps the last good key. A file with no key, two or more keys, or a control character counts as a failure. It logs the same warning as for an agent file, with `"file":"forward"`, and adds 1 to `otherlode_collector_token_reload_failures_total{file="forward"}`. [Change a mounted secret](#change-a-mounted-secret) applies to this file too.

```text
{"level":"WARN","msg":"token file re-read failed; keeping the last good value","file":"forward","error":"/run/secrets/forward-key: found 2 tokens, want exactly one"}
```

## Rotate the forward key

1. Create a new `ingest` key in Otherlode and leave the old one live. The steps are in [Replace a key without a gap](/docs/server/api-keys#replace-a-key-without-a-gap).
2. Give the collector the new key. With a file, replace the key in it and wait up to 30 seconds. With a variable, set the new value and restart.
3. Confirm that forwarding works with the new key.
4. Revoke the old key in Otherlode.

Do not put the old and new key in the file together. The collector refuses a file with two keys, keeps the key it had, and logs the warning above.

## Related pages

- [Deploy the collector](deploy) sets the first agent token.
- [Configuration](configuration) lists every variable.
- [Forwarding](forwarding) covers what happens after the `202`.
- [Monitoring](monitoring) covers `otherlode_collector_auth_rejected_total` and the reload counter.
- [Troubleshooting](troubleshooting) covers agents that get `401` and a rotation that has not taken effect.
- [Agent configuration](/docs/agent/configuration#authtoken) sets the token on the agent.
- [API keys](/docs/server/api-keys) covers creating and revoking Otherlode keys.
