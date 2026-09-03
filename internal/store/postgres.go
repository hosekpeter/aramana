package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"aramana/internal/ids"
	"aramana/internal/model"
)

// uniqueViolation is the PostgreSQL error code for a unique constraint violation.
const uniqueViolation = "23505"

// PostgresRepository is the PostgreSQL implementation of TriageRepository. All SQL for the
// service lives here rather than in the service layer.
type PostgresRepository struct{}

// NewPostgresRepository returns a repository. It holds no connection on purpose — the
// connection always arrives as a Querier argument.
func NewPostgresRepository() *PostgresRepository {
	return &PostgresRepository{}
}

// TxRunner runs transactions against a pgx pool.
type TxRunner struct {
	pool *pgxpool.Pool
}

// NewTxRunner returns a UnitOfWork backed by pool.
func NewTxRunner(pool *pgxpool.Pool) *TxRunner {
	return &TxRunner{pool: pool}
}

// WithTx runs fn inside a transaction. The rollback is deferred rather than conditional, so
// an early return or a panic cannot leave the transaction open; rolling back an already
// committed transaction is a no-op in pgx.
func (r *TxRunner) WithTx(ctx context.Context, fn func(q Querier) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if err := fn(tx); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

const questionColumns = `id, code, prompt, domain_code, is_entry`

func (repo *PostgresRepository) EntryQuestion(ctx context.Context, q Querier) (model.Question, error) {
	row := q.QueryRow(ctx, `SELECT `+questionColumns+` FROM questions WHERE is_entry AND active LIMIT 1`)
	return repo.scanQuestionWithOptions(ctx, q, row)
}

func (repo *PostgresRepository) QuestionByID(ctx context.Context, q Querier, questionID string) (model.Question, error) {
	row := q.QueryRow(ctx, `SELECT `+questionColumns+` FROM questions WHERE id = $1 AND active`, questionID)
	return repo.scanQuestionWithOptions(ctx, q, row)
}

func (repo *PostgresRepository) scanQuestionWithOptions(ctx context.Context, q Querier, row pgx.Row) (model.Question, error) {
	var question model.Question

	if err := row.Scan(&question.ID, &question.Code, &question.Prompt, &question.DomainCode, &question.IsEntry); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.Question{}, ErrNotFound
		}
		return model.Question{}, fmt.Errorf("scan question: %w", err)
	}

	options, err := repo.optionsForQuestion(ctx, q, question.ID)
	if err != nil {
		return model.Question{}, err
	}
	question.Options = options
	return question, nil
}

func (repo *PostgresRepository) optionsForQuestion(ctx context.Context, q Querier, questionID string) ([]model.Option, error) {
	rows, err := q.Query(ctx,
		`SELECT id, question_id, value, label, score, next_question_id, risk_flag, score_weighted
		 FROM question_options WHERE question_id = $1 ORDER BY sort_order, value`, questionID)
	if err != nil {
		return nil, fmt.Errorf("query options: %w", err)
	}
	defer rows.Close()

	options := make([]model.Option, 0, 4)
	for rows.Next() {
		var option model.Option
		if err := rows.Scan(&option.ID, &option.QuestionID, &option.Value, &option.Label,
			&option.Score, &option.NextQuestionID, &option.RiskFlag, &option.ScoreWeighted); err != nil {
			return nil, fmt.Errorf("scan option: %w", err)
		}
		options = append(options, option)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate options: %w", err)
	}
	return options, nil
}

func (repo *PostgresRepository) QuestionDomain(ctx context.Context, q Querier, questionID string) (*string, error) {
	var domain *string
	err := q.QueryRow(ctx, `SELECT domain_code FROM questions WHERE id = $1`, questionID).Scan(&domain)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("query question domain: %w", err)
	}
	return domain, nil
}

func (repo *PostgresRepository) OptionForQuestion(ctx context.Context, q Querier, questionID, optionID string) (model.Option, error) {
	var option model.Option
	err := q.QueryRow(ctx,
		`SELECT id, question_id, value, label, score, next_question_id, risk_flag, score_weighted
		 FROM question_options WHERE id = $1 AND question_id = $2`, optionID, questionID).
		Scan(&option.ID, &option.QuestionID, &option.Value, &option.Label,
			&option.Score, &option.NextQuestionID, &option.RiskFlag, &option.ScoreWeighted)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.Option{}, ErrNotFound
		}
		return model.Option{}, fmt.Errorf("query option: %w", err)
	}
	return option, nil
}

func (repo *PostgresRepository) CreateSession(ctx context.Context, q Querier, session model.Session) error {
	_, err := q.Exec(ctx,
		`INSERT INTO triage_sessions (id, status, current_question_id, current_domain_code, high_risk_detected)
		 VALUES ($1, $2, $3, $4, $5)`,
		session.ID, session.Status, session.CurrentQuestionID, session.CurrentDomain, session.HighRiskDetected)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

const sessionColumns = `id, status, current_question_id, current_domain_code, high_risk_detected, created_at, updated_at`

func (repo *PostgresRepository) SessionByID(ctx context.Context, q Querier, sessionID string) (model.Session, error) {
	return repo.scanSession(q.QueryRow(ctx, `SELECT `+sessionColumns+` FROM triage_sessions WHERE id = $1`, sessionID))
}

func (repo *PostgresRepository) LockSessionByID(ctx context.Context, q Querier, sessionID string) (model.Session, error) {
	return repo.scanSession(q.QueryRow(ctx, `SELECT `+sessionColumns+` FROM triage_sessions WHERE id = $1 FOR UPDATE`, sessionID))
}

func (repo *PostgresRepository) scanSession(row pgx.Row) (model.Session, error) {
	var session model.Session
	err := row.Scan(&session.ID, &session.Status, &session.CurrentQuestionID,
		&session.CurrentDomain, &session.HighRiskDetected, &session.CreatedAt, &session.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.Session{}, ErrNotFound
		}
		return model.Session{}, fmt.Errorf("scan session: %w", err)
	}
	return session, nil
}

func (repo *PostgresRepository) UpdateSessionState(ctx context.Context, q Querier, update model.SessionUpdate) error {
	_, err := q.Exec(ctx,
		`UPDATE triage_sessions
		 SET status = $1, current_question_id = $2, current_domain_code = $3,
		     high_risk_detected = $4, updated_at = NOW()
		 WHERE id = $5`,
		update.Status, update.CurrentQuestionID, update.CurrentDomain, update.HighRiskDetected, update.SessionID)
	if err != nil {
		return fmt.Errorf("update session state: %w", err)
	}
	return nil
}

func (repo *PostgresRepository) InsertAnswer(ctx context.Context, q Querier, answer model.Answer) error {
	_, err := q.Exec(ctx,
		`INSERT INTO session_answers (id, session_id, question_id, option_id)
		 VALUES ($1, $2, $3, $4)`,
		answer.ID, answer.SessionID, answer.QuestionID, answer.OptionID)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrConflict
		}
		return fmt.Errorf("insert answer: %w", err)
	}
	return nil
}

// AnswersForSession returns every answer of a session, normalized with the scoring and
// routing metadata the decision engine needs. Called with a transaction, it sees answers
// written earlier in that same transaction — which is what makes the final result account
// for the answer currently being processed.
func (repo *PostgresRepository) AnswersForSession(ctx context.Context, q Querier, sessionID string) ([]model.Answer, error) {
	rows, err := q.Query(ctx,
		`SELECT sa.id, sa.session_id, sa.question_id, que.code, sa.option_id, opt.value,
		        opt.score, que.domain_code, opt.score_weighted, sa.answered_at
		 FROM session_answers sa
		 JOIN question_options opt ON opt.id = sa.option_id
		 JOIN questions que ON que.id = sa.question_id
		 WHERE sa.session_id = $1
		 ORDER BY sa.answered_at, sa.id`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("query answers: %w", err)
	}
	defer rows.Close()

	answers := make([]model.Answer, 0, 8)
	for rows.Next() {
		var answer model.Answer
		if err := rows.Scan(&answer.ID, &answer.SessionID, &answer.QuestionID, &answer.QuestionCode,
			&answer.OptionID, &answer.OptionValue, &answer.Score, &answer.DomainCode,
			&answer.ScoreWeighted, &answer.AnsweredAt); err != nil {
			return nil, fmt.Errorf("scan answer: %w", err)
		}
		answers = append(answers, answer)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate answers: %w", err)
	}
	return answers, nil
}

func (repo *PostgresRepository) UpsertResult(ctx context.Context, q Querier, sessionID string, result model.TriageResult) error {
	_, err := q.Exec(ctx,
		`INSERT INTO triage_results (id, session_id, primary_domain, risk_level, recommended_action, total_score)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (session_id) DO UPDATE SET
		     primary_domain = EXCLUDED.primary_domain,
		     risk_level = EXCLUDED.risk_level,
		     recommended_action = EXCLUDED.recommended_action,
		     total_score = EXCLUDED.total_score`,
		ids.NewString(), sessionID, result.PrimaryDomain, result.RiskLevel, result.RecommendedAction, result.TotalScore)
	if err != nil {
		return fmt.Errorf("upsert result: %w", err)
	}
	return nil
}

func (repo *PostgresRepository) ResultForSession(ctx context.Context, q Querier, sessionID string) (model.TriageResult, error) {
	var result model.TriageResult
	err := q.QueryRow(ctx,
		`SELECT primary_domain, risk_level, recommended_action, total_score
		 FROM triage_results WHERE session_id = $1`, sessionID).
		Scan(&result.PrimaryDomain, &result.RiskLevel, &result.RecommendedAction, &result.TotalScore)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.TriageResult{}, ErrNotFound
		}
		return model.TriageResult{}, fmt.Errorf("query result: %w", err)
	}
	return result, nil
}

func (repo *PostgresRepository) AppendEvent(ctx context.Context, q Querier, event model.Event) error {
	_, err := q.Exec(ctx,
		`INSERT INTO triage_events (id, session_id, event_type, payload) VALUES ($1, $2, $3, $4)`,
		event.ID, event.SessionID, event.Type, event.Payload)
	if err != nil {
		return fmt.Errorf("append event: %w", err)
	}
	return nil
}

func (repo *PostgresRepository) FindIdempotentResponse(ctx context.Context, q Querier, key string) (IdempotencyRecord, error) {
	var record IdempotencyRecord
	err := q.QueryRow(ctx,
		`SELECT key, session_id, request_fingerprint, response_body FROM idempotency_keys WHERE key = $1`, key).
		Scan(&record.Key, &record.SessionID, &record.RequestFingerprint, &record.ResponseBody)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return IdempotencyRecord{}, ErrNotFound
		}
		return IdempotencyRecord{}, fmt.Errorf("query idempotency key: %w", err)
	}
	return record, nil
}

func (repo *PostgresRepository) ReserveIdempotencyKey(ctx context.Context, q Querier, record IdempotencyRecord) (bool, error) {
	tag, err := q.Exec(ctx,
		`INSERT INTO idempotency_keys (key, session_id, request_fingerprint, response_body)
		 VALUES ($1, $2, $3, 'null'::jsonb)
		 ON CONFLICT (key) DO NOTHING`,
		record.Key, record.SessionID, record.RequestFingerprint)
	if err != nil {
		return false, fmt.Errorf("reserve idempotency key: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (repo *PostgresRepository) CompleteIdempotencyKey(ctx context.Context, q Querier, key string, response []byte) error {
	tag, err := q.Exec(ctx,
		`UPDATE idempotency_keys SET response_body = $2 WHERE key = $1`, key, response)
	if err != nil {
		return fmt.Errorf("complete idempotency key: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("complete idempotency key %q: reservation is gone", key)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == uniqueViolation
	}
	return false
}
