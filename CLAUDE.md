# otherlode-collector

Receives the Otherlode agent's payloads, applies its processors and forwards
them to a sink. Read the code for how something works; this file holds rules.

## Where to look

- `docs/adr/`: why each decision was made. Read the ADR before changing what
  it decided.
- `STATUS.md`: what is in flight, parked or known to be wrong.
- `docs/site/`: the customer docs, published at otherlode.dev/docs.
  `docs/site/README.md` has the page format.

## Invariants

A change that breaks one of these needs an ADR first.

- **The collector decodes, processes and forwards.** It stores and
  aggregates nothing; that is the backend's job.
- **Redaction fails closed.** With redaction on, no string literal leaves,
  and a field the bindings do not know is stripped.
- **No silent stripping.** A payload that lost a field is marked
  `fields_stripped`, so the backend makes no claim from it.
- **A newer collector serves every older agent.** The collector is
  versioned and released on its own, and an adopter runs the latest.
- **Setting names are frozen once published.** Add a setting; never rename
  one or read an old name as a fallback.

## Rules for a change

- **Bindings follow the agent's schema.** A new wire field in the agent
  needs the bindings bumped here, or redaction strips it.
- **Docs follow behaviour, not every code change.** A change that adds
  behaviour a customer can see or set, or modifies behaviour a `docs/site`
  page documents, updates that page in the same commit. A refactor, a fix
  that restores documented behaviour, or an internal change needs none.
- Run `gofmt -l .`, `go vet ./...` and `go test ./...` before committing.

## Comments

- Doc comments on exported types and functions. An inline `//` comment only
  where the code is genuinely unintuitive: a non-obvious invariant, a
  workaround forced by a constraint, something a reader would misread.
  Don't narrate what the code already says.
- Write in simplified technical English: short sentences, one main clause
  each, plain words, no idioms. Prefer the concrete verb ("this rejects bad
  input") to the nominalization ("this handles rejection of invalid input").
- Run the `humanizer` skill over any comment before treating it as
  finished: no em dashes, no stock AI phrasing ("crucial," "seamless,"
  "robust," "leverage"), no sentence that restates the code.
- Never describe code relative to time ("now," "currently," "previously
  did X," "new in this change"). Say what the code does and why; git holds
  when.
