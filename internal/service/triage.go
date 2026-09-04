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

	"aramana/internal/flow"
	"aramana/internal/ids"
	"aramana/internal/model"
	"aramana/internal/store"
)

// Outcome is a machine-readable submission result.
type Outcome string

const (
	OutcomeNextQuestion Outcome = "NEXT_QUESTION"
	OutcomeCompleted    Outcome = "COMPLETED"
	OutcomeHighRisk     Outcome = "HIGH_RISK"
	// OutcomeReplayed marks a response loaded from the idempotency store.
	OutcomeReplayed Outcome = "REPLAYED"
)

// defaultTimeout bounds each use case.
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

// SubmitAnswerResult is persisted for idempotency replay and converted to an API DTO.
type SubmitAnswerResult struct {
	Session         model.Session       `json:"session"`
	CurrentQuestion *model.Question     `json:"current_question,omitempty"`
	Result          *model.TriageResult `json:"result,omitempty"`
	HighRisk        bool                `json:"high_risk"`
	Outcome         Outcome             `json:"outcome"`
}

// Service implements the triage use cases.
type Service struct {
	uow     store.UnitOfWork
	rules   flow.RuleSet
	logger  *slog.Logger
	timeout time.Duration
}

// New builds a Service.
func New(uow store.UnitOfWork, logger *slog.Logger) *Service {
	return &Service{
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
	err := s.uow.WithTx(ctx, func(repo store.TriageRepository) error {
		question, err := repo.EntryQuestion(ctx)
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
		if err := repo.CreateSession(ctx, session); err != nil {
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

// GetSession returns the current session state.
func (s *Service) GetSession(ctx context.Context, sessionID string) (*model.SessionState, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	var state *model.SessionState
	err := s.uow.WithTx(ctx, func(repo store.TriageRepository) error {
		session, err := repo.SessionByID(ctx, sessionID)
		if err != nil {
			return translateNotFound(err, ErrSessionNotFound)
		}

		state = &model.SessionState{Session: session}
		if session.CurrentQuestionID == nil {
			return nil
		}

		question, err := repo.QuestionByID(ctx, *session.CurrentQuestionID)
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
	err := s.uow.WithTx(ctx, func(repo store.TriageRepository) error {

		session, err := repo.LockSessionByID(ctx, cmd.SessionID)
		if err != nil {
			return translateNotFound(err, ErrSessionNotFound)
		}

		if cmd.IdempotencyKey != "" {
			replay, err := s.claimIdempotencyKey(ctx, repo, cmd)
			if err != nil {
				return err
			}
			if replay != nil {
				out = replay
				return nil
			}
		}

		result, err := s.applyAnswer(ctx, repo, session, cmd)
		if err != nil {
			return err
		}
		out = result

		if cmd.IdempotencyKey == "" {
			return nil
		}
		return s.storeResponse(ctx, repo, cmd, result)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) claimIdempotencyKey(ctx context.Context, repo store.TriageRepository, cmd SubmitAnswerCommand) (*SubmitAnswerResult, error) {
	acquired, err := repo.ReserveIdempotencyKey(ctx, store.IdempotencyRecord{
		Key:                cmd.IdempotencyKey,
		SessionID:          cmd.SessionID,
		RequestFingerprint: cmd.fingerprint(),
	})
	if err != nil {
		return nil, err
	}
	if acquired {
		return nil, nil
	}

	record, err := repo.FindIdempotentResponse(ctx, cmd.IdempotencyKey)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, ErrQuestionMismatch
		}
		return nil, err
	}
	if record.RequestFingerprint != cmd.fingerprint() {
		return nil, ErrIdempotencyKeyReused
	}

	var stored SubmitAnswerResult
	if err := json.Unmarshal(record.ResponseBody, &stored); err != nil {
		return nil, fmt.Errorf("decode stored idempotent response: %w", err)
	}

	if stored.Outcome == "" {
		return nil, fmt.Errorf("idempotency key %q has no stored response", cmd.IdempotencyKey)
	}
	stored.Outcome = OutcomeReplayed
	return &stored, nil
}

func (s *Service) storeResponse(ctx context.Context, repo store.TriageRepository, cmd SubmitAnswerCommand, result *SubmitAnswerResult) error {
	body, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode idempotent response: %w", err)
	}
	return repo.CompleteIdempotencyKey(ctx, cmd.IdempotencyKey, body)
}

func (s *Service) applyAnswer(ctx context.Context, repo store.TriageRepository, session model.Session, cmd SubmitAnswerCommand) (*SubmitAnswerResult, error) {
	if session.Status != model.StatusInProgress {
		return nil, ErrSessionClosed
	}
	if session.CurrentQuestionID == nil || *session.CurrentQuestionID != cmd.QuestionID {
		return nil, ErrQuestionMismatch
	}

	option, err := repo.OptionForQuestion(ctx, cmd.QuestionID, cmd.OptionID)
	if err != nil {
		return nil, translateNotFound(err, ErrOptionNotFound)
	}

	answer := model.Answer{
		ID:         ids.NewString(),
		SessionID:  cmd.SessionID,
		QuestionID: cmd.QuestionID,
		OptionID:   cmd.OptionID,
	}
	if err := repo.InsertAnswer(ctx, answer); err != nil {
		if errors.Is(err, store.ErrConflict) {
			return nil, ErrQuestionMismatch
		}
		return nil, err
	}

	// The transaction includes the answer inserted above.
	answers, err := repo.AnswersForSession(ctx, cmd.SessionID)
	if err != nil {
		return nil, err
	}

	var nextDomain *string
	if !option.RiskFlag && option.NextQuestionID != nil {
		nextDomain, err = repo.QuestionDomain(ctx, *option.NextQuestionID)
		if err != nil {
			return nil, err
		}
	}

	decision := flow.Decide(flow.State{
		Domain:  session.CurrentDomain,
		Answers: answers,
		Rules:   s.rules,
	}, option, nextDomain)

	return s.persistDecision(ctx, repo, session, decision)
}

// persistDecision writes the transition and its response.
func (s *Service) persistDecision(ctx context.Context, repo store.TriageRepository, session model.Session, decision flow.Decision) (*SubmitAnswerResult, error) {
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

	if err := repo.UpdateSessionState(ctx, update); err != nil {
		return nil, err
	}

	if decision.Result != nil {
		if err := repo.UpsertResult(ctx, session.ID, *decision.Result); err != nil {
			return nil, err
		}
	}

	// Persist outbox events with the state transition.
	for _, event := range decision.Events {
		stored, err := buildEvent(session.ID, event)
		if err != nil {
			return nil, err
		}
		if err := repo.AppendEvent(ctx, stored); err != nil {
			return nil, err
		}
		s.logger.Warn("triage_event_recorded",
			"event_type", event.Type, "session_id", session.ID, "event_id", stored.ID)
	}

	if decision.Kind == flow.KindNextQuestion {
		question, err := repo.QuestionByID(ctx, *decision.NextQuestionID)
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

// GetResult returns a final result.
func (s *Service) GetResult(ctx context.Context, sessionID string) (*model.TriageResult, error) {
	if strings.TrimSpace(sessionID) == "" {
		return nil, ErrInvalidRequest
	}
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	var out *model.TriageResult
	err := s.uow.WithTx(ctx, func(repo store.TriageRepository) error {
		session, err := repo.SessionByID(ctx, sessionID)
		if err != nil {
			return translateNotFound(err, ErrSessionNotFound)
		}

		result, err := repo.ResultForSession(ctx, sessionID)
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

		// Repair terminal sessions created before result persistence was guaranteed.
		repaired, err := s.recomputeResult(ctx, repo, session)
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

func (s *Service) recomputeResult(ctx context.Context, repo store.TriageRepository, session model.Session) (model.TriageResult, error) {
	answers, err := repo.AnswersForSession(ctx, session.ID)
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
	if err := repo.UpsertResult(ctx, session.ID, result); err != nil {
		return model.TriageResult{}, err
	}
	return result, nil
}

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

func translateNotFound(err error, notFound error) error {
	if errors.Is(err, store.ErrNotFound) {
		return notFound
	}
	return err
}
