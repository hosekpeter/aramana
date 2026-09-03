-- +goose Up
-- +goose StatementBegin

CREATE TYPE triage_session_status AS ENUM ('IN_PROGRESS', 'HIGH_RISK', 'COMPLETED');

CREATE TABLE domains (
    id UUID PRIMARY KEY,
    code VARCHAR(50) NOT NULL UNIQUE,
    name VARCHAR(200) NOT NULL,
    description TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE questions (
    id UUID PRIMARY KEY,
    code VARCHAR(100) NOT NULL UNIQUE,
    prompt TEXT NOT NULL,
    -- NULL means the question is not domain-specific (intake routing, safety check) and
    -- therefore does not change the session's domain. This replaces the previous
    -- 'UNKNOWN' sentinel, which had no row in domains and violated the foreign key.
    domain_code VARCHAR(50) NULL REFERENCES domains(code),
    -- Entry point of the flow. Exactly one active question may be the entry, so the
    -- service never has to hardcode a question code.
    is_entry BOOLEAN NOT NULL DEFAULT FALSE,
    -- Retiring a question is a soft delete: existing session_answers keep pointing at it.
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX idx_questions_single_entry ON questions(is_entry) WHERE is_entry AND active;

CREATE TABLE question_options (
    id UUID PRIMARY KEY,
    question_id UUID NOT NULL REFERENCES questions(id) ON DELETE CASCADE,
    value VARCHAR(100) NOT NULL,
    label TEXT NOT NULL,
    score INT NOT NULL DEFAULT 0,
    next_question_id UUID NULL REFERENCES questions(id),
    risk_flag BOOLEAN NOT NULL DEFAULT FALSE,
    -- Options with score_weighted = FALSE are routing-only and do not contribute to the
    -- clinical score (e.g., the entry question, which only picks a domain).
    score_weighted BOOLEAN NOT NULL DEFAULT TRUE,
    sort_order INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (question_id, value)
);

CREATE TABLE triage_sessions (
    id UUID PRIMARY KEY,
    status triage_session_status NOT NULL DEFAULT 'IN_PROGRESS',
    current_question_id UUID NULL REFERENCES questions(id),
    current_domain_code VARCHAR(50) NULL REFERENCES domains(code),
    high_risk_detected BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE session_answers (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES triage_sessions(id) ON DELETE CASCADE,
    question_id UUID NOT NULL REFERENCES questions(id),
    option_id UUID NOT NULL REFERENCES question_options(id),
    answered_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    -- A question is answered at most once per session. The current flow is a DAG, so this
    -- is a real invariant; supporting loops would mean replacing this with an attempt counter.
    UNIQUE (session_id, question_id)
);

CREATE TABLE triage_results (
    id UUID PRIMARY KEY,
    session_id UUID NOT NULL UNIQUE REFERENCES triage_sessions(id) ON DELETE CASCADE,
    -- Nullable: the safety gate can in principle fire before the flow has routed into a
    -- domain, and a result without a domain is more honest than a sentinel value.
    primary_domain VARCHAR(50) NULL REFERENCES domains(code),
    risk_level VARCHAR(30) NOT NULL,
    recommended_action VARCHAR(100) NOT NULL,
    total_score INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Domain events. Appended in the same transaction as the state change that produced them,
-- so a committed transaction always has its event and a rolled back one has none.
CREATE TABLE triage_events (
    id UUID PRIMARY KEY,
    session_id UUID NULL REFERENCES triage_sessions(id) ON DELETE CASCADE,
    event_type VARCHAR(100) NOT NULL,
    payload JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Idempotency records for POST /answers. The stored response is replayed verbatim, so a
-- retried request cannot advance the flow twice and cannot observe a different outcome.
CREATE TABLE idempotency_keys (
    key VARCHAR(255) PRIMARY KEY,
    session_id UUID NOT NULL REFERENCES triage_sessions(id) ON DELETE CASCADE,
    request_fingerprint VARCHAR(64) NOT NULL,
    response_body JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_triage_sessions_status ON triage_sessions(status);
CREATE INDEX idx_question_options_question_id ON question_options(question_id);
CREATE INDEX idx_session_answers_session_id ON session_answers(session_id);
CREATE INDEX idx_triage_events_session_id ON triage_events(session_id);
CREATE INDEX idx_idempotency_keys_session_id ON idempotency_keys(session_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

DROP TABLE IF EXISTS idempotency_keys;
DROP TABLE IF EXISTS triage_events;
DROP TABLE IF EXISTS triage_results;
DROP TABLE IF EXISTS session_answers;
DROP TABLE IF EXISTS triage_sessions;
DROP TABLE IF EXISTS question_options;
DROP TABLE IF EXISTS questions;
DROP TABLE IF EXISTS domains;
DROP TYPE IF EXISTS triage_session_status;

-- +goose StatementEnd
