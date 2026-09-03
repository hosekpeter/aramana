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
	SessionID         string  `json:"sessionId"`
	Status            string  `json:"status"`
	CurrentQuestionID *string `json:"currentQuestionId"`
	CurrentDomain     *string `json:"currentDomain"`
	HighRiskDetected  bool    `json:"highRiskDetected"`
}

// ResultDTO is the final triage outcome. TotalScore is intentionally omitted: it is an
// internal calibration detail, and the risk level is the part a client should act on.
type ResultDTO struct {
	PrimaryDomain     *string `json:"primaryDomain"`
	RiskLevel         string  `json:"riskLevel"`
	RecommendedAction string  `json:"recommendedAction"`
}

// SessionStateResponse is returned by session creation and session retrieval.
type SessionStateResponse struct {
	Session         SessionDTO   `json:"session"`
	CurrentQuestion *QuestionDTO `json:"currentQuestion"`
}

// AnswerRequest is the body of POST /triage/sessions/{session_id}/answers.
type AnswerRequest struct {
	QuestionID     string `json:"questionId"`
	OptionID       string `json:"optionId"`
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

// SubmitAnswerResponse is the body returned after an accepted or replayed answer.
type SubmitAnswerResponse struct {
	Session         SessionDTO   `json:"session"`
	CurrentQuestion *QuestionDTO `json:"currentQuestion"`
	Result          *ResultDTO   `json:"result"`
	HighRisk        bool         `json:"highRisk"`
	// Outcome is a machine-readable status: NEXT_QUESTION, COMPLETED, HIGH_RISK or REPLAYED.
	Outcome string `json:"outcome"`
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
