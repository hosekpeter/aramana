# ARAMANA Adaptive Intelligent Triage

Backend service for an adaptive mental-health triage flow. It supports Depression & Mood,
Anxiety & OCD, and Trauma & Stress. Questions branch from previous answers, progress is
persisted in PostgreSQL, and high-risk answers stop the normal flow immediately.

## Setup

Requirements: Docker with Compose, or Go 1.26 and PostgreSQL 16.

```bash
make up
curl http://localhost:8080/health
```

The API runs at `http://localhost:8080`. Migrations and seed data are embedded in the binary
and run automatically when the application starts.

Useful commands:

```bash
make logs              # follow application logs
make down              # stop containers and remove the database volume
make db-up && make run # run PostgreSQL in Docker and the API locally
make check             # format check, vet, unit tests and build
make test-integration  # repository tests against PostgreSQL
make docs              # regenerate Swagger files
```

Configuration is read from environment variables. The main values are `PORT`, `DB_HOST`,
`DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_NAME`, `DB_SSLMODE`, `DB_MAX_CONNS`,
`DB_MIN_CONNS`, `LOG_LEVEL`, `LOG_FORMAT`, and `LOG_SERVICE`. Defaults work with the
provided Docker Compose setup.

## Architecture

The project uses a small modular architecture:

```text
HTTP API -> service use cases -> repository -> PostgreSQL
                    |
                    +-> pure flow decision logic
```

The service layer owns transaction boundaries and depends on interfaces, not pgx. The flow
package has no I/O and converts the current state plus a selected option into the next state.
This keeps business rules easy to test without introducing unnecessary framework layers.

## Project Structure

| Path | Responsibility |
| --- | --- |
| `cmd/triage` | application bootstrap |
| `internal/api` | router, handlers, middleware and DTOs |
| `internal/service` | use cases and transaction orchestration |
| `internal/flow` | adaptive-flow and scoring decisions |
| `internal/store` | PostgreSQL repository and unit of work |
| `internal/model` | domain models |
| `internal/db` | connection pool and embedded Goose migrations |
| `internal/config` | environment configuration and validation |
| `internal/dependencies` | dependency wiring |
| `docs` | generated Swagger specification |

## Database Design

The main entities are:

- `domains`, `questions`, and `question_options`: flow definition, scoring, risk flags,
  and links to the next question.
- `triage_sessions`: current question, selected domain, status, and high-risk flag.
- `session_answers`: persisted user progress.
- `triage_results`: one final result per session.
- `triage_events`: high-risk events written with the state change.
- `idempotency_keys`: request fingerprint and stored response used for retry.

An option's `next_question_id` makes the assessment a database-driven graph instead of a
hardcoded chain of conditions. Foreign keys protect relationships, UUID v7 values are used
for IDs, and `UNIQUE (session_id, question_id)` prevents duplicate answers. A partial unique
index allows only one active entry question.

`questions.active` supports retiring a question without breaking historical answers.
Larger-scale flow versioning is deliberately left for a future version.

## API

| Method | Endpoint | Purpose |
| --- | --- | --- |
| `POST` | `/triage/sessions` | create a session and return the first question |
| `POST` | `/triage/sessions/{session_id}/answers` | submit the current answer |
| `GET` | `/triage/sessions/{session_id}` | resume from the saved state |
| `GET` | `/triage/sessions/{session_id}/result` | get the final result |
| `GET` | `/health` | liveness |
| `GET` | `/ready` | readiness including a database check |

Swagger UI is available at
[`/documentation`](http://localhost:8080/documentation). The generated specifications are
served as JSON and YAML under `/documentation/swagger.json` and
`/documentation/swagger.yaml`.

Answer request:

```json
{
  "questionId": "uuid",
  "optionId": "uuid",
  "idempotencyKey": "client-generated-key"
}
```

The response outcome is `NEXT_QUESTION`, `COMPLETED`, `HIGH_RISK`, or `REPLAYED`.
Errors use a consistent shape:

```json
{"code": "question_mismatch", "message": "...", "requestId": "uuid"}
```

JSON fields use camelCase. Scoring values, routing targets, and risk flags are not exposed to
the client.

## Adaptive Flow

The first answer routes the session to one of the three domains. `I am not sure` uses the
depression screening as a neutral fallback and does not affect scoring. Mild responses finish
the assessment; more concerning responses continue to the shared safety question.

Only score-weighted symptom answers contribute to the result:

| Score | Risk | Recommended action |
| --- | --- | --- |
| 0-1 | `LOW` | `SELF_CARE_RESOURCES` |
| 2 | `MEDIUM` | `SPECIALIST_ASSESSMENT` |
| 3+ | `HIGH` | `PRIORITY_SPECIALIST_ASSESSMENT` |

Answering `Yes` or `I am not sure` to the safety question sets the session to `HIGH_RISK`,
stores `IMMEDIATE_SUPPORT`, records `triage.high_risk_detected`, and prevents further
answers. The separate action distinguishes an immediate safety risk from a high symptom
score.

## Design Decisions

- Flow transitions, options, domain selection, scores, and risk flags are stored as data.
  Only the small scoring thresholds remain in Go.
- `flow.Decide` is a pure function, while the service handles persistence and transactions.
- `UnitOfWork` gives the service a repository bound to the active transaction, preventing
  accidental reads through the connection pool while a transaction is in progress.
- The high-risk event is inserted in the same transaction as the session update. This is the
  write side of the transactional outbox pattern.

No broker is required for the assessment. In production, a relay would publish unprocessed
rows from `triage_events` to JetStream; unavailable consumers could then catch up later and
deduplicate by event ID.

## Concurrency

`POST /answers` locks the session row with `SELECT ... FOR UPDATE`. Concurrent submissions
for the same question are therefore serialized: the first successful submission wins,
while the other sees that the session advanced and receives
`409 question_mismatch`. The unique answer constraint is a second database-level guard.
Different sessions use different row locks and can proceed independently.

## Idempotency

`idempotencyKey` is optional and belongs in the JSON request body. Inside the same
transaction, the service reserves it with `INSERT ... ON CONFLICT DO NOTHING`, stores a
SHA-256 fingerprint of session/question/option, and saves the successful response.

A retry with the same key and payload does not insert another answer; it returns the same
state and result with `outcome: REPLAYED`. Reusing the key for another payload returns
`422 idempotency_key_reused`. Without a key, database constraints still prevent duplicate
progress, but the original response cannot be replayed.

## Failure Handling, Security, and Operations

Use cases and SQL statements have five-second timeouts, readiness checks PostgreSQL, request
bodies are limited to 16 KiB, panics become a standard `500` response, and shutdown is
bounded. A timed-out answer can be retried safely with its idempotency key. A high-risk event
cannot be lost between state update and event storage because both writes commit together.

Logs are structured with `slog` and include request IDs, HTTP status, duration, and safe error
details. Request and response bodies are not logged. `X-Request-ID` is accepted or generated
and returned to the client.

Authentication and authorization are outside this assessment. A production version should
validate an OIDC token, bind every session to its owner, restrict CORS, use TLS and managed
secrets, encrypt sensitive data, define retention, and audit access. Useful metrics include
latency, error rate, database latency, completion rate, high-risk count, and event backlog.

## Testing

```bash
go test ./...          # unit tests; integration tests skip without a database URL
make test-race         # tests with the race detector
make test-integration  # starts PostgreSQL and runs repository integration tests
make check             # complete local quality check
```

Tests cover flow decisions, scoring, high-risk handling, invalid answers, state transitions,
idempotency and rollback behavior, HTTP contracts, configuration, and PostgreSQL persistence.

## Trade-offs

- The shipped assessment contains a small representative flow, not a clinical questionnaire.
- There is no authentication platform or event publisher because neither is required for the
  core flow.
- Rules support direct option-to-question transitions, not arbitrary expressions.
- Full flow versioning, data retention, and an administration API are not implemented.

## Future Improvements

- Add black-box Go E2E tests for simultaneous submissions and complete user journeys.
- Add explicit flow versions and immutable question/option revisions.
- Publish outbox events through NATS JetStream with retries and delivery tracking.
- Add authentication, ownership checks, audit records, encryption, and retention policies.
- Add production metrics, tracing, dashboards, and CI for unit and integration tests.
