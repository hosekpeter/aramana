# ARAMANA Adaptive Intelligent Triage

A Go backend for an adaptive mental-health triage flow. PostgreSQL is the source of truth,
questions branch on previous answers, a safety gate stops the flow on a high-risk answer, and
answer submission is idempotent and safe under concurrent requests.

## Setup

```bash
docker compose up --build     # API on http://localhost:8080
```

Or against a local PostgreSQL — the defaults match `docker compose up -d db`, so no exports
are needed:

```bash
go run ./cmd/triage
```

Migrations and the seed flow are embedded in the binary (`//go:embed` + `goose`) and applied
on startup; they are idempotent, so restarting against an existing database is safe.

Config comes from the environment via struct tags in `internal/config` (`PORT`, `DB_*`,
`LOG_*`) and is validated at startup, so an invalid value fails fast instead of silently
defaulting. `make help` lists all targets; the useful ones are `make check` (what CI runs),
`make test-race`, `make test-integration`, `make up`/`down`, `make db-up` + `make run`,
`make smoke`, `make psql`. `ui/simple_client.html` is a throwaway page for clicking through
the flow by hand.

## Architecture

Modular layering, closest to hexagonal without the ceremony: the service layer holds the use
cases and depends on interfaces, while database, HTTP and decision logic sit outside it. A
full Clean Architecture split would cost more indirection than it explains at this size.

```
cmd/triage            entry point, bootstrap only
internal/api          HTTP router, handlers, middleware, wire DTOs
internal/service      use cases: transaction boundaries and error translation
internal/flow         pure triage decision logic, no I/O
internal/store        repository plus Querier/UnitOfWork over pgx
internal/model        domain models
internal/dependencies composition root, the only place wiring happens
internal/db           connection pool and embedded migrations
internal/apierr       maps domain errors onto the HTTP contract
internal/config       environment config with validation
internal/httpserver   listener with bounded graceful shutdown
internal/logger       structured logging setup
internal/ids          UUID v7 generation
```

Dependencies point inward: `api` → `service` → `store`/`flow` → `model`. Three decisions
shape the rest:

- **`internal/flow` is a pure function.** `Decide(state, option, nextDomain) Decision` has no
  database, context or clock, so it is table-testable and cannot interleave a read with an
  uncommitted write.
- **One `store.Querier` for pool and transaction.** Satisfied by `*pgxpool.Pool` and `pgx.Tx`
  alike and passed to every repository method, so "query the pool while holding a transaction"
  is not expressible.
- **The service does not know pgx.** It depends on `TriageRepository` and `UnitOfWork`, which
  is what lets the use cases run against an in-memory fake.

## Database Design

`domains` holds the clinical domains; `questions` and `question_options` the questions,
options, scores and routing; `triage_sessions` the state; `session_answers` the answers;
`triage_results` the outcome; `triage_events` the domain events; `idempotency_keys` the
stored responses for retries.

A session points at the question it waits on and the domain it was routed into. An option
optionally points at the next question — that pointer is what makes the flow a graph stored as
data rather than code.

- `questions.domain_code` and `triage_results.primary_domain` are **nullable**: the intake
  question and the shared safety check belong to no domain, and an `'UNKNOWN'` sentinel would
  have to violate the foreign key to `domains`.
- `question_options.score_weighted` separates clinical answers from routing ones, so routing
  cannot inflate the score.
- `UNIQUE (session_id, question_id)` on `session_answers` prevents answering a question twice
  even if the application check is bypassed.
- A partial unique index on `questions(is_entry) WHERE is_entry AND active` guarantees exactly
  one entry question, so no question code is hardcoded.
- IDs are application-generated **UUID v7**: time-ordered and known before the write.
- Versioning is additive: `questions.active` retires a question without orphaning the answers
  referencing it, so historical sessions stay interpretable as answered.

## API

| Method | Path | Description |
| --- | --- | --- |
| `POST` | `/triage/sessions` | start a session, returns the first question (`201`) |
| `POST` | `/triage/sessions/{session_id}/answers` | submit an answer |
| `GET` | `/triage/sessions/{session_id}` | current state (resume) |
| `GET` | `/triage/sessions/{session_id}/result` | final result |
| `GET` | `/health`, `/ready` | liveness, readiness (with a DB ping) |

JSON is **camelCase** both ways. The idempotency key is a field in the body, not a header.

```bash
SID=$(curl -s -X POST http://localhost:8080/triage/sessions | jq -r .session.sessionId)

curl -s -X POST http://localhost:8080/triage/sessions/$SID/answers \
  -H 'Content-Type: application/json' \
  -d '{"questionId":"...","optionId":"...","idempotencyKey":"client-generated-1"}'

curl -s http://localhost:8080/triage/sessions/$SID/result
```

A submit response carries the session, the next question, the result once there is one, and a
machine-readable `outcome` (`NEXT_QUESTION`, `COMPLETED`, `HIGH_RISK`, `REPLAYED`). The wire
contract is defined by DTOs in `internal/api`, not the domain model, so a client never sees
`score`, `riskFlag` or `nextQuestionId` — those would reveal which answer trips the gate.

Errors share one shape, `{"code","message","requestId"}`, with a stable `code`:

| Status | Codes |
| --- | --- |
| `400` | `invalid_request` (bad JSON, unknown field, malformed UUID), `option_not_found` |
| `404` | `session_not_found`, `route_not_found` |
| `405` | `method_not_allowed` |
| `409` | `question_mismatch`, `session_closed`, `session_not_complete` |
| `413` | `request_too_large` (body over 16 KiB) |
| `422` | `idempotency_key_reused` (same key, different payload) |
| `500` / `503` | `internal_error`, `db_unavailable` |

`requestId` is also returned in `X-Request-ID`, adopted from the client if sent. Internal
errors are logged, never returned.

## Adaptive flow and rules

Questions, options, scores and transitions are rows, not an if/else tree, so the changes the
assessment asks about are data operations: adding a question or option is an `INSERT`,
disabling one is `UPDATE questions SET active = FALSE`, changing a path updates
`next_question_id`, a new domain is an `INSERT` plus its questions. Only the thresholds live
in code, in `flow.RuleSet`.

The intake question routes into a domain; the domain question then either closes the flow or
escalates to the shared safety check, so mild symptoms take a shorter path. `I am not sure`
routes into the depression screening — low mood is the most common presentation and its
question is the least presumptuous thing to ask someone who cannot yet name their problem,
whereas the trauma question would presume an event they never mentioned. The intake is not
score-weighted, so that choice does not bias the score.

Only `score_weighted` answers are summed, giving 0..3 in the shipped flow:

| Score | risk_level | recommended_action |
| --- | --- | --- |
| 0–1 | `LOW` | `SELF_CARE_RESOURCES` |
| 2 | `MEDIUM` | `SPECIALIST_ASSESSMENT` |
| 3+ | `HIGH` | `PRIORITY_SPECIALIST_ASSESSMENT` |

## High-risk detection

An option with `risk_flag` (`Yes` and `I am not sure` on the risk check) means, in one
transaction: the session moves to `HIGH_RISK` with `current_question_id = null`, a result is
stored with `HIGH` / `IMMEDIATE_SUPPORT`, and a `triage.high_risk_detected` event is appended.
Further answers get `409 session_closed`. `flow.Decide` checks `risk_flag` first, so a risk
answer stops the flow even when that option would otherwise route onward.

**`IMMEDIATE_SUPPORT` is reserved for the gate** and absent from the scoring table above. A
completed session can also reach `HIGH` by score, but it recommends
`PRIORITY_SPECIALIST_ASSESSMENT`: one person said they are in danger now, the other explicitly
said they are not but reports severe symptoms, and collapsing the two would leave the stored
result unable to answer "which sessions need the emergency workflow".

## Events

`triage_events` rows carry `event_type`, a `JSONB` payload (triggering question, option and
value, plus session and timestamp) and `created_at`. The type string is the contract; the
payload is additive.

The event is written **inside the same transaction** as the state change that produced it, so
a commit guarantees it is there and a rollback removes it. Writing outside would permit both a
lost event and a phantom one. That is the write half of the outbox pattern and the half that
determines correctness; a test asserts a failed transaction leaves no event.

There is no dispatcher at this scope, so the table is currently an audit log. In production a
relay would read it with `FOR UPDATE SKIP LOCKED`, publish, and mark rows dispatched. Because
rows are already committed, an unavailable consumer only delays events. Consumers must be
idempotent, since at-least-once is all a relay can promise; the event ID is the dedup key. A
broker should be **JetStream**, not NATS Core — Core is fire-and-forget, so a subscriber that
is down when a high-risk event is published never sees it.

## Idempotency

`POST /answers` accepts an optional `idempotencyKey`, claimed with a single
`INSERT ... ON CONFLICT (key) DO NOTHING`. The caller that inserts the row owns the request,
processes the answer and attaches the response before committing; a caller that loses the
claim processes nothing and returns the stored response with `outcome: REPLAYED`. The full
body is stored, so a retry gets a byte-for-byte identical response, and a sha256 fingerprint
of the request is stored with it — the same key with a different payload is `422`, not a
silent replay of someone else's answer.

Two ordering rules are load-bearing:

- **The claim happens after the session row lock**, which is what makes simultaneous retries
  queue instead of race. Looking the key up before the lock — as an earlier version did —
  leaves the lookup outside any serialisation, so overlapping retries all find the key absent
  and all but one are rejected with a conflict for a request that actually succeeded. That is
  exactly the unstable-network case.
- **The claim happens before state validation**, because by then the original has advanced the
  flow, so a current-question check would reject the retry and the stored response would be
  unreachable.

One statement rather than a read-then-write, because the same key on **two different sessions**
shares no lock: there a read-then-write lets both transactions think the key is free and the
loser dies on the primary key — a `500` for what is really a `422`. An integration test covers
that and fails against the old version.

Without a key a resubmit is `409 question_mismatch`, enforced by the unique constraint, so a
keyless retry still cannot double-advance the flow — it just cannot recover the response.

## Concurrency

One session open on two devices, both answering at once: `SELECT ... FOR UPDATE` on the
session row serialises the transactions, so the second blocks and then sees the advanced
current question. **The first committed submit wins; the other gets `409 question_mismatch`**
with nothing applied, and the unique constraint is an independent second defence.

The whole use case — lock, idempotency claim, insert, read answers, decide, result, event — is
one transaction, and reads go through its `Querier`, so the decision sees the answer just
inserted. Locks are per session, so distinct sessions never serialise. Lock order is fixed
(session row, then key), so there is no deadlock cycle.

Verified by `make test-integration` against real PostgreSQL under the race detector, 8
concurrent clients per scenario:

| Scenario | Result |
| --- | --- |
| different answers, same question | 1 accepted, 7 × `question_mismatch`, 1 answer row, state = winner's |
| identical retries, same key | all 8 succeed, 1 processed + 7 replayed, 1 answer row |
| same key, different payloads | 1 accepted, rest `idempotency_key_reused` |
| answers to 8 different sessions | all succeed |

## Failure handling

- **PostgreSQL slow** — a 5 s context timeout per use case plus a server-side
  `statement_timeout`, so a slow query cannot hold a handler, a pool connection or a row lock
  indefinitely. `MaxConns` is explicit (20) rather than machine-dependent, and `/ready` starts
  failing so an orchestrator stops routing traffic.
- **External service unavailable** — the triage path has no external dependency by design, so
  there is nothing to cascade from. Anything added later belongs off the request path; a
  synchronous one would need its own timeout, a circuit breaker and a defined degraded answer.
- **Request times out mid-submission** — the idempotency case above: the retry either replays
  the stored response or is processed as the original. Either way the flow advances once.
- **Event cannot be delivered** — events are committed atomically with the state change, so
  delivery failure only delays them.

Also: the rollback is `defer`red rather than conditional, so no early return or panic leaves a
transaction open; panics become the standard `500` envelope with a `request_id`; bodies are
capped at 16 KiB; shutdown drains in-flight requests with a 10 s bound.

## Security

Request and response bodies are **never logged**, being sensitive health data, and neither is
the event payload — only `event_id`, through which it can be found in `triage_events`. Scores
and `risk_flag` stay server-side, errors carry a stable `code` rather than the underlying
error, and the container runs as an unprivileged user.

Out of scope here, required for production: **authentication** (an OIDC token validated in
middleware, `subject_id` on sessions); **authorisation** — today any caller holding a session
UUID can advance it, acceptable only because the ID is an unguessable v7 UUID, so every
handler must check the authenticated subject owns the session; **sensitive data** (column
encryption, retention, pruning `idempotency_keys`, which hold full response bodies); **API
security** (TLS, rate limiting, a CORS allow-list — the current middleware is permissive for
local UI development); **auditability** (extend the append-only `triage_events` to record
access); and **secrets** from a manager with rotation rather than environment variables.

## Logging and monitoring

Structured `log/slog` output shaped for an aggregator: `timestamp`, `status` (severity) and
`message` replace slog's `time`/`level`/`msg` via `ReplaceAttr`, HTTP details are grouped
under `http.*` — deliberately not under `status`, where a `500` would be read as severity —
`duration` is nanoseconds, and `error.kind`/`message`/`stack` is the shape error tracking
groups on. Each request emits `request_finished`, plus `request_failed` on error, joined by
`request_id` (adopted from `X-Request-ID` if sent).

Worth monitoring: latency and error rate per endpoint, session completion rate, `HIGH_RISK`
detection count (a sudden change is either an incident or a flow regression), undispatched
event backlog once a relay exists, query latency, pool utilisation, lock wait time.

## Testing

```bash
go test ./...          # unit tests, no infrastructure needed
make test-race         # same, with the race detector
make test-integration  # concurrency tests against real PostgreSQL
```

Each layer is tested where its failures actually occur, rather than for line coverage.

- **`internal/flow`** — table tests of the decision logic: the gate beating routing, the
  shared risk check not losing the domain, unweighted answers excluded from the score,
  thresholds reachable, the two HIGH paths distinguishable.
- **`internal/service`** — use cases against an in-memory fake whose unit of work does
  snapshot/restore, giving it real rollback semantics: idempotency and replay, key reuse,
  closed sessions, question mismatch, the result including the answer being submitted, and
  persistence failures propagated rather than swallowed.
- **`internal/api`** — error mapping, request IDs, oversized bodies, and an assertion that
  `score` and `risk_flag` never reach the response.
- **Integration tests** — the concurrency and idempotency races against real PostgreSQL,
  because a fake cannot reproduce row locking or a unique index blocking a concurrent insert,
  and those are the mechanisms the guarantees rest on. They skip unless `TEST_DATABASE_URL`
  is set, so `go test ./...` needs no infrastructure.

`scripts/` holds manual HTTP walkthroughs; see `README_TESTS.md`.

## Design Decisions

- **Decision logic as a pure function** — costs one extra type and loading state up front,
  buys testability without a database and no reads interleaved with uncommitted writes.
- **One `Querier` for pool and transaction** — slightly more verbose signatures make a whole
  class of bug unexpressible.
- **Storing the full response body for idempotency** rather than a "processed" flag — costs
  space, gives a retry exactly the original response.
- **Claiming the key with `ON CONFLICT` inside the locked transaction** — atomic on its own,
  and correct for keys that cross sessions.
- **`IMMEDIATE_SUPPORT` reserved for the gate** — four actions instead of three so a consumer
  can tell a crisis from a severe score.
- **Events written in the transaction, not published outside it** — the write is atomic with
  the state change, so a dispatcher can be added later without risking a lost or phantom event.
- **No rule engine** — thresholds in `flow.RuleSet`, transitions in the database; the data
  model already covers the changes asked about.
- **OpenTelemetry dropped** — it had been configured without a TracerProvider, so every span
  was a no-op. Better absent than fake.

## Trade-offs

Deliberately not built:

- **Authentication and authorisation** — documented above instead; it would have consumed the
  time that went into concurrency and idempotency and is not exercisable without an IdP.
- **Event dispatcher / broker** — the transactional write determines soundness; the relay is
  mechanical.
- **OpenAPI document** — the tables above are the contract. First item below.
- **Small flow per domain** — one screening question plus the shared gate. The point was to
  show the flow is data, not to author a clinical instrument, which is also why the scoring
  model is simple.
- **Retention/pruning** — `idempotency_keys` and `triage_events` grow unbounded; fine at this
  size, but production needs a policy, the idempotency table for privacy as well as size.

## Future Improvements

- OpenAPI specification and a generated client
- Event delivery to NATS JetStream: relay with `FOR UPDATE SKIP LOCKED`, a `dispatched_at`
  column and an attempt cap
- Authentication and per-subject authorisation, with `subject_id` on sessions
- `env` and `version` in log lines from deployment metadata — compose already sets `DD_*` for
  the agent, but the application does not read them yet
- OpenTelemetry tracing with a real exporter, plus latency/error-rate/completion-rate metrics
- CI running `make check` and the integration tests
- Flow versioning and A/B question variants, building on `active` and additive rows
- Retention and pruning for `idempotency_keys` and `triage_events`
- A rule engine, if clinical experts need conditions richer than "this option leads to that
  question"; `flow.Decide` is the only place that would change
