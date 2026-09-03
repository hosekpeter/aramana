// Package service orchestrates the triage use cases. It owns transaction boundaries and
// error translation, but no SQL and no decision logic: persistence lives in internal/store
// and the adaptive rules live in internal/flow.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"strings"
	"time"

	"adaptive-intelligent-triage/internal/flow"
	"adaptive-intelligent-triage/internal/ids"
	"adaptive-intelligent-triage/internal/model"
	"adaptive-intelligent-triage/internal/store"
)

// Outcome describes what happened to a submitted answer. It is a stable machine-readable
// value, unlike the free-text message the previous implementation returned.
type Outcome string

const (
	OutcomeNextQuestion Outcome = "NEXT_QUESTION"
	OutcomeCompleted    Outcome = "COMPLETED"
	OutcomeHighRisk     Outcome = "HIGH_RISK"
	// OutcomeReplayed means the request carried an idempotency key that had already been
	// processed, and the original response was returned unchanged.
	OutcomeReplayed Outcome = "REPLAYED"
)

// defaultTimeout bounds every use case. Without it a slow query holds an HTTP handler, a
// pool connection and a row lock for as long as the database takes.
const defaultTimeout = 5 * time.Second

// maxIdempotencyKeyLength matches the column width in the schema.
const maxIdempotencyKeyLength = 255

// SubmitAnswerCommand is the input of SubmitAnswer.
type SubmitAnswerCommand struct {
	SessionID      string
	QuestionID     string
	OptionID       string
	IdempotencyKey string
}

// fingerprint identifies the logical request behind an idempotency key, so a key reused
// with a different payload can be rejected instead of silently replaying the wrong answer.
func (c SubmitAnswerCommand) fingerprint() string {
	sum := sha256.Sum256([]byte(c.SessionID + "\x00" + c.QuestionID + "\x00" + c.OptionID))
	return hex.EncodeToString(sum[:])
}

// SubmitAnswerResult is the outcome of accepting (or replaying) one answer. The JSON tags
// exist because this struct is what gets stored for idempotent replay.
//
// They stay snake_case while the API is camelCase on purpose: this is a persistence format,
// not a wire format. Renaming a tag here would make every already-stored response decode
// into zero values on replay. The client never sees these names — internal/api/dto.go
// translates the struct into the wire types.
type SubmitAnswerResult struct {
	Session         model.Session       `json:"session"`
	CurrentQuestion *model.Question     `json:"current_question,omitempty"`
	Result          *model.TriageResult `json:"result,omitempty"`
	HighRisk        bool                `json:"high_risk"`
	Outcome         Outcome             `json:"outcome"`
}

// Service implements the triage use cases.
type Service struct {
	repo    store.TriageRepository
	uow     store.UnitOfWork
	rules   flow.RuleSet
	logger  *slog.Logger
	timeout time.Duration
}

// New builds a Service. Note that it takes no connection and no pgx type: every database
// access goes through uow, which hands the repository a transaction. That is what prevents
// a query from running outside the transaction that is supposed to contain it.
func New(repo store.TriageRepository, uow store.UnitOfWork, logger *slog.Logger) *Service {
	return &Service{
		repo:    repo,
		uow:     uow,
		rules:   flow.DefaultRuleSet(),
		logger:  logger,
		timeout: defaultTimeout,
	}
}

// CreateSession starts a triage run and returns the entry question.
func (s *Service) CreateSession(ctx context.Context) (*model.SessionState, error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	var state *model.SessionState
	err := s.uow.WithTx(ctx, func(q store.Querier) error {
		question, err := s.repo.EntryQuestion(ctx, q)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return fmt.Errorf("no active entry question configured")
			}
			return err
		}

		session := model.Session{
			ID:                ids.NewString(),
			Status:            model.StatusInProgress,
			CurrentQuestionID: &question.ID,
			CurrentDomain:     question.DomainCode,
		}
		if err := s.repo.CreateSession(ctx, q, session); err != nil {
			return err
		}

		state = &model.SessionState{Session: session, CurrentQuestion: &question}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state, nil
}

// GetSession returns the current state of a session, including the question it waits on.
// This is also the resume path: a client that lost its place reads the current question here.
func (s *Service) GetSession(ctx context.Context, sessionID string) (*model.SessionState, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	var state *model.SessionState
	err := s.uow.WithTx(ctx, func(q store.Querier) error {
		session, err := s.repo.SessionByID(ctx, q, sessionID)
		if err != nil {
			return translateNotFound(err, ErrSessionNotFound)
		}

		state = &model.SessionState{Session: session}
		if session.CurrentQuestionID == nil {
			return nil
		}

		question, err := s.repo.QuestionByID(ctx, q, *session.CurrentQuestionID)
		if err != nil {
			return err
		}
		state.CurrentQuestion = &question
		return nil
	})
	if err != nil {
		return nil, err
	}
	return state, nil
}

func (s *Service) SubmitAnswer(ctx context.Context, cmd SubmitAnswerCommand) (*SubmitAnswerResult, error) {
	if strings.TrimSpace(cmd.SessionID) == "" || strings.TrimSpace(cmd.QuestionID) == "" || strings.TrimSpace(cmd.OptionID) == "" {
		return nil, ErrInvalidRequest
	}
	if len(cmd.IdempotencyKey) > maxIdempotencyKeyLength {
		return nil, ErrInvalidRequest
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	var out *SubmitAnswerResult
	err := s.uow.WithTx(ctx, func(q store.Querier) error {
		replay, err := s.replayIfKnown(ctx, q, cmd)
		if err != nil {
			return err
		}
		if replay != nil {
			out = replay
			return nil
		}

		result, err := s.applyAnswer(ctx, q, cmd)
		if err != nil {
			return err
		}
		out = result

		return s.rememberResponse(ctx, q, cmd, result)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// replayIfKnown returns the stored response for an already processed idempotency key, or
// nil when the request has not been seen before.
func (s *Service) replayIfKnown(ctx context.Context, q store.Querier, cmd SubmitAnswerCommand) (*SubmitAnswerResult, error) {
	if cmd.IdempotencyKey == "" {
		return nil, nil
	}

	record, err := s.repo.FindIdempotentResponse(ctx, q, cmd.IdempotencyKey)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if record.RequestFingerprint != cmd.fingerprint() {
		return nil, ErrIdempotencyKeyReused
	}

	var stored SubmitAnswerResult
	if err := json.Unmarshal(record.ResponseBody, &stored); err != nil {
		return nil, fmt.Errorf("decode stored idempotent response: %w", err)
	}
	stored.Outcome = OutcomeReplayed
	return &stored, nil
}

// rememberResponse stores the response so a retry with the same key replays it verbatim.
func (s *Service) rememberResponse(ctx context.Context, q store.Querier, cmd SubmitAnswerCommand, result *SubmitAnswerResult) error {
	if cmd.IdempotencyKey == "" {
		return nil
	}

	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode idempotent response: %w", err)
	}

	err = s.repo.SaveIdempotentResponse(ctx, q, store.IdempotencyRecord{
		Key:                cmd.IdempotencyKey,
		SessionID:          cmd.SessionID,
		RequestFingerprint: cmd.fingerprint(),
		ResponseBody:       body,
	})
	if errors.Is(err, store.ErrConflict) {
		return ErrQuestionMismatch
	}
	return err
}

// applyAnswer performs the validated state transition for one answer.
func (s *Service) applyAnswer(ctx context.Context, q store.Querier, cmd SubmitAnswerCommand) (*SubmitAnswerResult, error) {
	// The row lock is what serialises concurrent answers: the second transaction blocks
	// here until the first commits, then observes the advanced current question.
	session, err := s.repo.LockSessionByID(ctx, q, cmd.SessionID)
	if err != nil {
		return nil, translateNotFound(err, ErrSessionNotFound)
	}
	if session.Status != model.StatusInProgress {
		return nil, ErrSessionClosed
	}
	if session.CurrentQuestionID == nil || *session.CurrentQuestionID != cmd.QuestionID {
		return nil, ErrQuestionMismatch
	}

	option, err := s.repo.OptionForQuestion(ctx, q, cmd.QuestionID, cmd.OptionID)
	if err != nil {
		return nil, translateNotFound(err, ErrOptionNotFound)
	}

	answer := model.Answer{
		ID:         ids.NewString(),
		SessionID:  cmd.SessionID,
		QuestionID: cmd.QuestionID,
		OptionID:   cmd.OptionID,
	}
	if err := s.repo.InsertAnswer(ctx, q, answer); err != nil {
		if errors.Is(err, store.ErrConflict) {
			// The question is already answered in this session, so the flow has moved on.
			return nil, ErrQuestionMismatch
		}
		return nil, err
	}

	// Read through the transaction, so this includes the answer inserted above. Reading
	// through the pool here was what made the final result ignore the last answer.
	answers, err := s.repo.AnswersForSession(ctx, q, cmd.SessionID)
	if err != nil {
		return nil, err
	}

	var nextDomain *string
	if !option.RiskFlag && option.NextQuestionID != nil {
		nextDomain, err = s.repo.QuestionDomain(ctx, q, *option.NextQuestionID)
		if err != nil {
			return nil, err
		}
	}

	decision := flow.Decide(flow.State{
		Domain:  session.CurrentDomain,
		Answers: answers,
		Rules:   s.rules,
	}, option, nextDomain)

	return s.persistDecision(ctx, q, session, decision)
}

// persistDecision writes the transition and builds the response. The response session is
// assembled from the values just written rather than re-read, which avoids a second round
// trip and removes the previous silent error swallowing on the reload path.
func (s *Service) persistDecision(ctx context.Context, q store.Querier, session model.Session, decision flow.Decision) (*SubmitAnswerResult, error) {
	update := model.SessionUpdate{
		SessionID:        session.ID,
		CurrentDomain:    decision.Domain,
		HighRiskDetected: session.HighRiskDetected,
	}

	out := &SubmitAnswerResult{Result: decision.Result}

	switch decision.Kind {
	case flow.KindNextQuestion:
		update.Status = model.StatusInProgress
		update.CurrentQuestionID = decision.NextQuestionID
		out.Outcome = OutcomeNextQuestion
		// The next question is not a result, so it is not part of the decision payload.
		out.Result = nil

	case flow.KindCompleted:
		update.Status = model.StatusCompleted
		update.CurrentQuestionID = nil
		out.Outcome = OutcomeCompleted

	case flow.KindHighRisk:
		update.Status = model.StatusHighRisk
		update.CurrentQuestionID = nil
		update.HighRiskDetected = true
		out.HighRisk = true
		out.Outcome = OutcomeHighRisk

	default:
		return nil, fmt.Errorf("unhandled decision kind %d", decision.Kind)
	}

	if err := s.repo.UpdateSessionState(ctx, q, update); err != nil {
		return nil, err
	}

	if decision.Result != nil {
		if err := s.repo.UpsertResult(ctx, q, session.ID, *decision.Result); err != nil {
			return nil, err
		}
	}

	// Events are written inside this transaction. If the transaction rolls back the event
	// disappears with it, and if it commits the event is guaranteed to be there — neither
	// held when the event was written on a pooled connection.
	for _, event := range decision.Events {
		stored, err := buildEvent(session.ID, event)
		if err != nil {
			return nil, err
		}
		if err := s.repo.AppendEvent(ctx, q, stored); err != nil {
			return nil, err
		}
		s.logger.Warn("triage_event_recorded",
			"event_type", event.Type, "session_id", session.ID, "event_id", stored.ID)
	}

	if decision.Kind == flow.KindNextQuestion {
		question, err := s.repo.QuestionByID(ctx, q, *decision.NextQuestionID)
		if err != nil {
			return nil, err
		}
		out.CurrentQuestion = &question
	}

	out.Session = model.Session{
		ID:                session.ID,
		Status:            update.Status,
		CurrentQuestionID: update.CurrentQuestionID,
		CurrentDomain:     update.CurrentDomain,
		HighRiskDetected:  update.HighRiskDetected,
		CreatedAt:         session.CreatedAt,
		UpdatedAt:         time.Now().UTC(),
	}
	return out, nil
}

// GetResult returns the final result of a session.
//
// A session that is still in progress yields ErrSessionNotComplete, which the API maps to a
// 409. The previous implementation returned an unwrapped error here and the client saw a
// 500 for an ordinary state.
func (s *Service) GetResult(ctx context.Context, sessionID string) (*model.TriageResult, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	var out *model.TriageResult
	err := s.uow.WithTx(ctx, func(q store.Querier) error {
		session, err := s.repo.SessionByID(ctx, q, sessionID)
		if err != nil {
			return translateNotFound(err, ErrSessionNotFound)
		}

		result, err := s.repo.ResultForSession(ctx, q, sessionID)
		if err == nil {
			out = &result
			return nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if session.Status == model.StatusInProgress {
			return ErrSessionNotComplete
		}

		// Terminal session without a stored result. SubmitAnswer always writes one, so
		// this only happens for data written before that guarantee existed; recompute and
		// persist rather than fail.
		repaired, err := s.recomputeResult(ctx, q, session)
		if err != nil {
			return err
		}
		out = &repaired
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) recomputeResult(ctx context.Context, q store.Querier, session model.Session) (model.TriageResult, error) {
	answers, err := s.repo.AnswersForSession(ctx, q, session.ID)
	if err != nil {
		return model.TriageResult{}, err
	}

	domain := flow.ResolveDomain(session.CurrentDomain, answers)
	result := flow.Evaluate(domain, answers, s.rules)
	if session.Status == model.StatusHighRisk || session.HighRiskDetected {
		result = flow.HighRiskResult(domain, answers)
	}

	// session_status, not status: "status" is where the log backend reads the severity of
	// the line, so an application value must not be written there.
	s.logger.Warn("triage_result_recomputed", "session_id", session.ID, "session_status", session.Status)
	if err := s.repo.UpsertResult(ctx, q, session.ID, result); err != nil {
		return model.TriageResult{}, err
	}
	return result, nil
}

// buildEvent serialises a decision event into a stored event record.
func buildEvent(sessionID string, event flow.Event) (model.Event, error) {
	payload := make(map[string]any, len(event.Payload)+2)
	maps.Copy(payload, event.Payload)
	payload["session_id"] = sessionID
	payload["occurred_at"] = time.Now().UTC().Format(time.RFC3339)

	encoded, err := json.Marshal(payload)
	if err != nil {
		return model.Event{}, fmt.Errorf("encode event payload: %w", err)
	}

	return model.Event{
		ID:        ids.NewString(),
		SessionID: sessionID,
		Type:      event.Type,
		Payload:   encoded,
	}, nil
}

// translateNotFound maps a repository miss onto the matching domain error and passes other
// errors through unchanged.
func translateNotFound(err error, notFound error) error {
	if errors.Is(err, store.ErrNotFound) {
		return notFound
	}
	return err
}
