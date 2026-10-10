---
title: Blank out string literals
description: What the collector blanks in branch conditions and case labels, the two settings that turn blanking on with pattern examples, the secret that re-keys branch and site keys, the case keys it clears, the header it sends upstream, the unknown fields it drops while blanking is on, and the counters and logs that show it working.
order: 60
---

A branch condition can hold text from your code, such as `"ENABLE_LEGACY_DISCOUNT"` in an environment lookup, or a hard-coded key your code compares against input. The agent sends it as it is. The collector can replace that text with `…` before any payload leaves your network. Blanking is off until you set one of two environment variables. Blanking also needs a secret, which the collector uses to replace the agent's branch and site keys. See [replace branch and site keys](#replace-branch-and-site-keys).

Blanking happens only in the collector. The agent has no such setting, so an agent that posts straight to a backend sends literals and keys as they are.

## What the collector blanks

A string literal part is a piece of a branch condition that holds a string constant from your class files, or the label of a `when` or `switch` case on a string. [What the agent sends](/docs/agent/data-sent#condition-text-and-string-literals) describes what the agent puts in one.

The collector looks at the condition parts in two places, in every branch site of the manifest and of the static baseline:

- The condition of the branch site.
- The case label of each outcome of the site.

A part is blanked when its kind is neither code nor placeholder, and the settings select it. A part of a kind the collector does not recognise counts as a literal, so it is blanked too. The part keeps its kind, and its text becomes `…`.

Class, method, parameter and file names, line numbers, route templates and library paths pass through as they arrive. A condition's code parts stay readable, so a blanked condition still shows its shape.

## Choose what to blank

The collector blanks a literal when it matches a pattern in `OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES`, or when `OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS` is true. The collector reads both once at startup. Blanking is on when at least one pattern is set or when `OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS` is true. While blanking is on, the collector also needs `OTHERLODE_COLLECTOR_REDACT_SECRET` or `OTHERLODE_COLLECTOR_REDACT_SECRET_FILE`, or it does not start.

| Variable | Default | Accepted values | Effect |
|---|---|---|---|
| `OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES` | unset | One regular expression per line, in Go syntax | Blanks each literal that a pattern matches |
| `OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS` | unset (off) | `1`, `t`, `T`, `TRUE`, `true`, `True`, `0`, `f`, `F`, `FALSE`, `false`, `False` | `true` blanks every literal |

### Blank every literal

```bash
OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS=true
OTHERLODE_COLLECTOR_REDACT_SECRET_FILE=/run/secrets/otherlode-redact-secret
```

Use this when you do not want to judge which literals are safe to send.

### Blank literals that match a pattern

Put one pattern on each line. Patterns use [Go regular expression syntax](https://pkg.go.dev/regexp/syntax). A pattern matches anywhere in the literal's text unless you anchor it with `^` or `$`. The collector tests each pattern against one literal at a time, and the text is the string without quotes. Blank lines are skipped. Any other line is used as written, so spaces at its start or end belong to the pattern.

```bash
OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES='^sk_live_
(?i)password
^[0-9]{4}$'
```

These three patterns blank a literal that starts with `sk_live_`, a literal that contains `password` in any case, and a literal of exactly four digits. A literal that matches no pattern is sent as it is.

### Startup errors

A value the collector cannot use stops startup with exit code 1. A typo cannot leave blanking off. The error is one JSON log line, and its `msg` field holds the text.

A pattern that does not compile names its line number, counted from 1 with blank lines included:

```text
OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES line 3: error parsing regexp: missing closing ): `(oops`
```

A value that is not a boolean:

```text
OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS: strconv.ParseBool: parsing "yes": invalid syntax
```

The secret has its own startup errors. See [secret errors at startup](#secret-errors-at-startup).

When blanking is on, the collector logs this line at startup. `secret_fingerprint` names the secret without giving it away. See [compare the fingerprints](#compare-the-fingerprints).

```text
{"time":"...","level":"INFO","msg":"redacting string literals and re-keying branch and site keys in ingested payloads","blocked_values":3,"all_literals":false,"secret_fingerprint":"765616210e4aabd4"}
```

## Check that blanking works

The counter `otherlode_collector_redacted_literals_total` on `/metrics` counts the parts the collector blanked. `otherlode_collector_redacted_case_keys_total` counts the case keys it cleared. See [case keys that are a hash code](#case-keys-that-are-a-hash-code). The `payload` label of both is `manifest` or `static_baseline`. Each stays at zero until a payload arrives with something to blank or clear.

No counter and no log line holds a literal's text, a key or the secret.

## Unknown fields are dropped while blanking is on

The collector cannot see a literal inside a field it does not know. So while either setting is on, it drops every protobuf field it does not know from every payload, delta batches included. If a payload lost at least one field, the collector sets `fields_stripped` on the payload's resource attributes and counts the payload in `otherlode_collector_fields_stripped_total`. Its `payload` label is `deltas`, `manifest` or `static_baseline`.

The collector logs one warning for each run, with the first payload that loses a field. The line carries the `namespace`, `service`, `instance`, `run`, `agent_version` and `payload` fields:

```text
{"time":"...","level":"WARN","msg":"dropped fields this collector does not know; its bindings are older than the agent's, so the server withholds findings from this run; upgrade to the latest collector","namespace":"...","service":"...","instance":"...","run":"...","agent_version":"...","payload":"..."}
```

A dropped field can be one that the server judges by. The server stores a marked run and shows it, but counts it in no finding. See [a run from an older collector makes no claim](/docs/server/how-it-works#a-run-from-an-older-collector-makes-no-claim).

To fix it, run the latest collector. With both settings off, the collector drops nothing and marks nothing.

## Replace branch and site keys

The agent names each branch outcome with a `branch_key` and each branch site with a `site_key`. Each key is a digest of the site's bytecode, and that bytecode includes the site's string literals. See [branch and site keys](/docs/agent/data-sent#branch-and-site-keys) in the agent's docs. Every other input to a key is in the payload. So anyone who holds a blanked payload and a plain key can hash a guess at the blanked literal and compare the result with the key. A short literal, such as a flag name or a host name, falls to a few guesses.

So while blanking is on, the collector replaces every `branch_key` and `site_key` in manifests and static baselines with HMAC-SHA256 of the key under a secret. It keeps the first 16 bytes, written as 32 lowercase hex characters, the same shape the agent sends. Equal keys stay equal, so Otherlode still joins one branch across builds and instances. Without the secret, nobody can test a guess against a key. An empty key stays empty.

### Set the secret

Set one of two variables. Setting both stops startup.

| Variable | Holds |
|---|---|
| `OTHERLODE_COLLECTOR_REDACT_SECRET` | The secret. The collector trims spaces around it. |
| `OTHERLODE_COLLECTOR_REDACT_SECRET_FILE` | The path of a file that holds the secret on one line. The file follows the rules of a [token file](tokens#keep-a-token-out-of-the-environment). Blank lines and lines that start with `#` are skipped, and spaces around the line are trimmed. |

The secret must follow these rules, or the collector does not start:

- It is at least 32 bytes long.
- It holds no control character, such as a tab or a newline inside the value.
- It differs from every agent token and from the forward key. Every agent holds an agent token, and Otherlode holds the forward key, so neither can keep the keys secret.
- It is set only while blanking is on. A secret with both blanking settings off stops startup, because you meant to protect the keys and nothing would.

Make a secret with `openssl`. It prints 64 hex characters, which is 64 bytes.

```bash
openssl rand -hex 32
```

The collector reads the secret once, at startup. Unlike the token files, it never reads the file again, because a secret that changed during a run would change every key from then on. A changed file takes effect at the next restart.

### Use one secret for every collector of a tenant

Give the same secret to every collector that sends to one Otherlode tenant. That includes the collector in each environment and the collector that your test runs report through. Two collectors with different secrets give one branch two different keys, so Otherlode keeps two histories for that branch.

A JVM that reaches Otherlode without a redacting collector sends plain keys. A test JVM that posts straight to Otherlode, or through a collector with blanking off, sends the plain keys of every production class it loads. Those keys undo the HMAC for the literals in those classes. Point the test JVM at a collector with the same blanking settings and the same secret as production's. [Test runs](/docs/agent/test-runs) covers the agent's side.

### Compare the fingerprints

A collector cannot see another collector's secret. It logs a fingerprint of its own instead, as `secret_fingerprint` in the startup line above. The fingerprint is 16 lowercase hex characters, taken from an HMAC of a fixed text under the secret. Two collectors with the same secret log the same fingerprint. Compare the fingerprints of all the collectors of a tenant to check that they share one secret.

### Changing the secret starts new history

Turning blanking on changes every key. So does a new secret. Otherlode then treats every branch and site as new. The history it holds under the old keys stops growing, and the new keys start their own. No setting changes the secret without that break.

Pick the secret once, before you turn blanking on for a tenant, and keep it.

### Case keys that are a hash code

A `switch` on a string that the agent cannot read back reaches the collector as a switch on the string's `hashCode()`. Each case key of such a switch is the hash code of one string, usually one of your literals. A hash code is a 32-bit digest, so it also lets its holder test guesses. See [switches](/docs/agent/methods-and-branches#switches) in the agent's docs.

While blanking is on, the collector clears two kinds of case key.

- Every case key of a site the agent marks `string_hash_code_switch`. The collector cannot tell which literal a key hashes, so it clears every key of the site, even when the blanking settings would let that literal through.
- A case key that equals the Java hash code of a literal that the collector blanked in the same method. This rule covers an agent that does not send the mark.

A cleared case has no key, so Otherlode shows it unnamed and cannot match it across builds. A case of an integer switch whose key equals the hash code of a literal blanked in the same method loses its key too. From an agent that does not send the mark, a hash code whose literal is not in the payload is not cleared. That happens when the agent could not write the condition of the `equals` check that holds the literal.

`otherlode_collector_redacted_case_keys_total` counts the cleared case keys of both kinds.

### Let Otherlode refuse payloads that were not blanked

While blanking is on, the collector sends the secret's fingerprint in an `Otherlode-Redaction` header on every request it forwards, for all three payloads. With blanking off it sends no such header.

A tenant can have Otherlode require the header. Otherlode then refuses a payload that has no header, such as one an agent posted to it directly, with `403` and the error code `redaction_required`. It refuses a payload with another fingerprint with `403` and `redaction_secret_mismatch`. The collector does not retry a `403`. It drops the payload and logs a warning that carries the code as `backend_error`:

```text
{"time":"...","level":"WARN","msg":"dropping payload: permanent failure","namespace":"","service":"checkout","instance":"3f1c9a","path":"/v1/otherlode/deltas","error":"backend returned 403 Forbidden","backend_error":"redaction_secret_mismatch"}
```

`redaction_required` means this collector has blanking off. `redaction_secret_mismatch` means its secret differs from the one Otherlode expects. Fix the setting and restart the collector. The dropped payloads do not come back. [Forwarding](forwarding#dropped-payloads) covers drops.

The header catches a setup mistake. It does not prove which collector sent a payload, because the fingerprint is in every forwarded request and in the startup log.

### Secret errors at startup

Each of these stops startup with exit code 1, as one `ERROR` line whose `msg` field holds the text. No error quotes the secret.

| Mistake | Message |
|---|---|
| Blanking is on and no secret is set | `redaction is on but neither OTHERLODE_COLLECTOR_REDACT_SECRET nor OTHERLODE_COLLECTOR_REDACT_SECRET_FILE is set; refusing to start, since plain branch and site keys let anyone who holds them test guesses at a redacted literal` |
| Both variables are set | `OTHERLODE_COLLECTOR_REDACT_SECRET and OTHERLODE_COLLECTOR_REDACT_SECRET_FILE are both set; set one` |
| The secret is too short | `OTHERLODE_COLLECTOR_REDACT_SECRET: the secret is 5 bytes, want at least 32` |
| The secret holds a control character | `OTHERLODE_COLLECTOR_REDACT_SECRET: the secret holds a control character at byte 16` |
| The variable holds only spaces | `OTHERLODE_COLLECTOR_REDACT_SECRET holds only spaces` |
| The secret is an agent token | `OTHERLODE_COLLECTOR_REDACT_SECRET equals an agent auth token, which every agent holds; use a secret of its own` |
| The secret is the forward key | `OTHERLODE_COLLECTOR_REDACT_SECRET equals the forward key, which the backend holds; use a secret of its own` |
| A secret is set and blanking is off | `OTHERLODE_COLLECTOR_REDACT_SECRET or OTHERLODE_COLLECTOR_REDACT_SECRET_FILE is set but redaction is off; set OTHERLODE_COLLECTOR_REDACT_BLOCKED_VALUES or OTHERLODE_COLLECTOR_REDACT_ALL_LITERALS, or unset the secret variable` |
| The file holds two secrets | `OTHERLODE_COLLECTOR_REDACT_SECRET_FILE: /run/secrets/otherlode-redact-secret: found 2 tokens, want exactly one` |

A secret from the file gives the same messages under the name `OTHERLODE_COLLECTOR_REDACT_SECRET_FILE`. A file that is missing, is not a regular file or is larger than 1 MiB gives the file errors of a token file.

## Related pages

- [Configuration](configuration) lists every collector setting.
- [What the collector sends](data-sent) lists everything the collector changes in a payload.
- [Monitoring](monitoring) covers `/metrics` and the log format.
- [Troubleshooting](troubleshooting) covers the dropped-fields warning by symptom.
- [Set and rotate tokens](tokens) covers the agent token and the forward key, which the secret must not equal.
- [What the agent sends](/docs/agent/data-sent) lists what the agent puts in each payload.
