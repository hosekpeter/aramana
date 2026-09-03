package api

import (
	"aramana/internal/model"
	"aramana/internal/service"
)

type OptionDTO struct {
	ID    string `json:"id"`
	Value string `json:"value"`
	Label string `json:"label"`
}

// QuestionDTO is one step of the flow.
type QuestionDTO struct {
	ID      string      `json:"id"`
	Code    string      `json:"code"`
	Prompt  string      `json:"prompt"`
	Options []OptionDTO `json:"options"`
}

// SessionDTO is the client-visible session state.
type SessionDTO struct {
	SessionID string `json:"sessionId"`
	// Status is IN_PROGRESS while the session accepts answers. COMPLETED and HIGH_RISK are
	// terminal; a further answer returns 409 session_closed.
	Status string `json:"status" enums:"IN_PROGRESS,COMPLETED,HIGH_RISK"`
	// CurrentQuestionID is the question the session waits on, null once it is terminal.
	CurrentQuestionID *string `json:"currentQuestionId"`
	// CurrentDomain is null until the intake answer routes the session into a domain.
	CurrentDomain *string `json:"currentDomain" enums:"DEPRESSION,ANXIETY,TRAUMA"`
	// HighRiskDetected is true once the safety gate has fired for this session.
	HighRiskDetected bool `json:"highRiskDetected"`
}

// HealthResponse is the body of the liveness and readiness probes.
type HealthResponse struct {
	Status string `json:"status" enums:"ok,ready"`
}

// ResultDTO is the final triage outcome. TotalScore is intentionally omitted: it is an
// internal calibration detail, and the risk level is the part a client should act on.
type ResultDTO struct {
	// PrimaryDomain is null if the safety gate fired before the flow routed into a domain.
	PrimaryDomain *string `json:"primaryDomain" enums:"DEPRESSION,ANXIETY,TRAUMA"`
	RiskLevel     string  `json:"riskLevel" enums:"LOW,MEDIUM,HIGH"`
	// RecommendedAction distinguishes the two routes to HIGH: IMMEDIATE_SUPPORT is produced
	// only by the safety gate, PRIORITY_SPECIALIST_ASSESSMENT is the most urgent action
	// reachable through scoring alone.
	RecommendedAction string `json:"recommendedAction" enums:"SELF_CARE_RESOURCES,SPECIALIST_ASSESSMENT,PRIORITY_SPECIALIST_ASSESSMENT,IMMEDIATE_SUPPORT"`
}

// SessionStateResponse is returned by session creation and session retrieval.
type SessionStateResponse struct {
	Session         SessionDTO   `json:"session"`
	CurrentQuestion *QuestionDTO `json:"currentQuestion"`
}

// AnswerRequest is the body of POST /triage/sessions/{session_id}/answers. Unknown fields
// are rejected with 400 invalid_request.
type AnswerRequest struct {
	// QuestionID must be the question the session currently waits on.
	QuestionID string `json:"questionId" format:"uuid"`
	// OptionID must be one of that question's options.
	OptionID string `json:"optionId" format:"uuid"`
	// IdempotencyKey makes the request safe to retry. Reusing it with a different payload is
	// rejected with 422; omitting it means a retry can only be rejected, not replayed.
	IdempotencyKey string `json:"idempotencyKey,omitempty" maxLength:"255"`
}

// SubmitAnswerResponse is the body returned after an accepted or replayed answer.
type SubmitAnswerResponse struct {
	Session         SessionDTO   `json:"session"`
	CurrentQuestion *QuestionDTO `json:"currentQuestion"`
	// Result is present for COMPLETED and HIGH_RISK, null while the flow continues.
	Result *ResultDTO `json:"result"`
	// HighRisk is true when this answer tripped the safety gate.
	HighRisk bool `json:"highRisk"`
	// Outcome is a machine-readable status: NEXT_QUESTION, COMPLETED, HIGH_RISK or REPLAYED.
	Outcome string `json:"outcome" enums:"NEXT_QUESTION,COMPLETED,HIGH_RISK,REPLAYED"`
}

func toOptionDTOs(options []model.Option) []OptionDTO {
	out := make([]OptionDTO, 0, len(options))
	for _, option := range options {
		out = append(out, OptionDTO{ID: option.ID, Value: option.Value, Label: option.Label})
	}
	return out
}

func toQuestionDTO(question *model.Question) *QuestionDTO {
	if question == nil {
		return nil
	}
	return &QuestionDTO{
		ID:      question.ID,
		Code:    question.Code,
		Prompt:  question.Prompt,
		Options: toOptionDTOs(question.Options),
	}
}

func toSessionDTO(session model.Session) SessionDTO {
	return SessionDTO{
		SessionID:         session.ID,
		Status:            session.Status,
		CurrentQuestionID: session.CurrentQuestionID,
		CurrentDomain:     session.CurrentDomain,
		HighRiskDetected:  session.HighRiskDetected,
	}
}

func toResultDTO(result *model.TriageResult) *ResultDTO {
	if result == nil {
		return nil
	}
	return &ResultDTO{
		PrimaryDomain:     result.PrimaryDomain,
		RiskLevel:         result.RiskLevel,
		RecommendedAction: result.RecommendedAction,
	}
}

func toSessionStateResponse(state *model.SessionState) SessionStateResponse {
	return SessionStateResponse{
		Session:         toSessionDTO(state.Session),
		CurrentQuestion: toQuestionDTO(state.CurrentQuestion),
	}
}

func toSubmitAnswerResponse(result *service.SubmitAnswerResult) SubmitAnswerResponse {
	return SubmitAnswerResponse{
		Session:         toSessionDTO(result.Session),
		CurrentQuestion: toQuestionDTO(result.CurrentQuestion),
		Result:          toResultDTO(result.Result),
		HighRisk:        result.HighRisk,
		Outcome:         string(result.Outcome),
	}
}
