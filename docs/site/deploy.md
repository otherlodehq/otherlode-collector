---
title: Deploy the collector
description: Run the container image with an agent token, put a TLS proxy in front of it, confirm it is up, try it with no forward URL, point agents at it, and forward to Otherlode.
order: 10
---

The collector is a single container image that sits between your agents and Otherlode. It listens for the agents' pushes on port 4319, and it needs one setting to start: a token the agents send. This page takes you from pulling the image to a collector your agents report to.

## Run the image

The image is `ghcr.io/otherlodehq/otherlode-collector`. It is built for `linux/amd64` and `linux/arm64`. It runs as a non-root user from a distroless base with no shell, and it exposes port 4319.

Make a secret for the agents to send, then start the collector with it:

```bash
TOKEN=$(openssl rand -hex 32)
docker run -d --name otherlode-collector \
	-p 4319:4319 \
	-e OTHERLODE_COLLECTOR_AUTH_TOKEN="$TOKEN" \
	ghcr.io/otherlodehq/otherlode-collector:latest
```

Keep the value of `TOKEN`, because you set the same value on every agent. To list more than one token, separate them with commas. [Set and rotate tokens](tokens) covers lists, token files and rotation.

The collector refuses to start with no token. It logs this error and exits with code 1:

```text
neither OTHERLODE_COLLECTOR_AUTH_TOKEN nor OTHERLODE_COLLECTOR_AUTH_TOKEN_FILE is set; refusing to start without auth (set OTHERLODE_COLLECTOR_INSECURE_NO_AUTH=1 to run unauthenticated)
```

`OTHERLODE_COLLECTOR_INSECURE_NO_AUTH=1` lets it start with no token, and the collector then accepts a push from anyone who can reach it. Use that only on your own machine.

The collector listens on `:4319` unless you set `OTHERLODE_COLLECTOR_ADDR`. Every setting is an environment variable, listed in [the configuration](configuration).

### Run it on Kubernetes

This Deployment keeps the token in a Secret and probes `/healthz` over HTTP. The image has no shell, so a probe cannot run a script. A probe that runs `/otherlode-collector healthcheck` as an exec works too.

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: otherlode-collector
spec:
  replicas: 1
  selector:
    matchLabels:
      app: otherlode-collector
  template:
    metadata:
      labels:
        app: otherlode-collector
    spec:
      containers:
        - name: collector
          image: ghcr.io/otherlodehq/otherlode-collector:latest
          ports:
            - containerPort: 4319
          env:
            - name: OTHERLODE_COLLECTOR_AUTH_TOKEN
              valueFrom:
                secretKeyRef:
                  name: otherlode-collector
                  key: agent-token
          readinessProbe:
            httpGet:
              path: /healthz
              port: 4319
          livenessProbe:
            httpGet:
              path: /healthz
              port: 4319
```

Put a Service in front of it on port 4319. Agents in the cluster then use the Service's address as their `exportUrl`.

## Put a TLS proxy in front

The collector serves plain HTTP and has no TLS setting. For any network you do not control, terminate TLS in a reverse proxy, ingress or load balancer in front of it, and give agents the `https://` address.

Without that, everything an agent sends crosses the network in clear. That includes the agent token in the `Authorization` header and every payload, which names your service, its classes and methods, and its dependencies. The agent logs a warning at startup when it sends a token over `http://`.

The collector reads the client address for its per-address rate limit. Behind a proxy, every agent can look like one address until you tell the collector which header carries the real one. [Rate and memory limits](limits) explains the header and when to trust it.

## Confirm it is up

Request the health route. It answers `200` with an empty body:

```bash
curl -i http://localhost:4319/healthz
```

At the default `info` log level the collector also writes one JSON line to standard output when it starts listening. The `addr` field is the listen address and `version` is the collector's version:

```text
{"time":"...","level":"INFO","msg":"otherlode-collector listening","addr":":4319","version":"..."}
```

The image runs the same check itself. Its `HEALTHCHECK` runs `/otherlode-collector healthcheck` every 10 seconds, after a 10 second start period, and `docker ps` shows the result. [Health checks, metrics and logs](monitoring) covers the subcommand.

## Try it with no forward URL

With no `OTHERLODE_COLLECTOR_FORWARD_URL`, the collector starts, accepts agent pushes, and forwards nothing. For each push it logs one `INFO` line with the service, the run, and the size of each list in the payload. It does not log the payload itself. The lines are `received delta batch`, `received probe manifest` and `received static baseline`.

At startup it also logs this warning:

```text
{"time":"...","level":"WARN","msg":"OTHERLODE_COLLECTOR_FORWARD_URL not set; ingest payloads are only logged, not forwarded"}
```

Use this mode to check that your agents reach the collector before you connect it to Otherlode. Nothing leaves your network in this mode, and nothing arrives at Otherlode.

## Point agents at it

Set two agent options. [`exportUrl`](/docs/agent/configuration#exporturl) is the collector's address, for example `https://collector.example.com`. [`authToken`](/docs/agent/configuration#authtoken) is one of the tokens you gave the collector. Restart each service for the new options to apply.

If an agent gets a `401` or cannot connect, see [troubleshooting](troubleshooting).

## Forward to Otherlode

To send what the collector receives on to Otherlode, set `OTHERLODE_COLLECTOR_FORWARD_URL` and `OTHERLODE_COLLECTOR_FORWARD_AUTH_TOKEN` as [Send data from your collector](/docs/server/connect) describes. Restart the collector. The startup warning above no longer appears. [Forwarding](forwarding) describes what happens to each push after the collector accepts it, and [set and rotate tokens](tokens) covers changing the forward key.

## Run the latest release

Run the latest collector. [How the collector works](how-it-works) explains why. Each release is tagged `X.Y.Z`, `X.Y` and `latest`. A tag such as `0.1` follows the patch releases of that minor version. `latest` follows every release.

## Next steps

- [Configuration](configuration) lists every setting.
- [Set and rotate tokens](tokens) covers token files and rotation.
- [Rate and memory limits](limits) covers the rate limit and the client address header.
- [Health checks, metrics and logs](monitoring) covers what to watch.
- [Blank out string literals](redaction) covers blanking literals before payloads leave your network, and the secret that blanking needs.
