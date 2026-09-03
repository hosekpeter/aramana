package model

import "time"

// Session lifecycle states.
const (
	StatusInProgress = "IN_PROGRESS"
	StatusCompleted  = "COMPLETED"
	StatusHighRisk   = "HIGH_RISK"
)

// Risk levels of a triage result.
const (
	RiskLow    = "LOW"
	RiskMedium = "MEDIUM"
	RiskHigh   = "HIGH"
)

// Recommended actions, one per risk level.
const (
	ActionSelfCareResources    = "SELF_CARE_RESOURCES"
	ActionSpecialistAssessment = "SPECIALIST_ASSESSMENT"
	// ActionPriorityAssessment is the most urgent action reachable through scoring alone.
	ActionPriorityAssessment = "PRIORITY_SPECIALIST_ASSESSMENT"
	// ActionImmediateSupport is reserved for the safety gate and is never produced by scoring.
	ActionImmediateSupport = "IMMEDIATE_SUPPORT"
)

// Option is one selectable answer to a question.
type Option struct {
	ID         string
	QuestionID string
	Value      string
	Label      string
	// Score is the clinical weight of the option. It only counts towards the total when
	// ScoreWeighted is set.
	Score int
	// NextQuestionID is the question this option routes to, nil when the option ends the
	// flow.
	NextQuestionID *string
	// RiskFlag marks an option that trips the safety gate and stops the flow immediately.
	RiskFlag bool
	// ScoreWeighted separates clinical answers from routing ones. The intake question and
	// the safety check are not symptom measurements, so they must not contribute to the
	// score.
	ScoreWeighted bool
}

// Question is one step of the adaptive flow.
type Question struct {
	ID     string
	Code   string
	Prompt string
	// DomainCode is nil for questions that do not belong to a clinical domain, such as the
	// intake question and the shared safety check.
	DomainCode *string
	// IsEntry marks the question a new session starts on. Exactly one active question
	// carries it, enforced by a partial unique index.
	IsEntry bool
	Options []Option
}

// Session is one triage run.
type Session struct {
	ID     string
	Status string
	// CurrentQuestionID is the question the session waits on, nil once the session is
	// terminal.
	CurrentQuestionID *string
	// CurrentDomain is the clinical domain the flow routed into, nil until it has.
	CurrentDomain    *string
	HighRiskDetected bool
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// SessionUpdate is the set of mutable session fields written by a state transition.
type SessionUpdate struct {
	SessionID         string
	Status            string
	CurrentQuestionID *string
	CurrentDomain     *string
	HighRiskDetected  bool
}

// Answer is one recorded response, denormalised with the question and option metadata the
// decision engine needs so that deciding requires no further queries.
type Answer struct {
	ID           string
	SessionID    string
	QuestionID   string
	QuestionCode string
	OptionID     string
	OptionValue  string
	Score        int
	DomainCode   *string
	// ScoreWeighted mirrors the option's flag: false answers are routing or safety
	// responses and are excluded from the total score.
	ScoreWeighted bool
	AnsweredAt    time.Time
}

// TriageResult is the outcome of a finished session.
type TriageResult struct {
	// PrimaryDomain is nil when the safety gate fired before the flow routed anywhere.
	PrimaryDomain     *string
	RiskLevel         string
	RecommendedAction string
	// TotalScore is kept server-side; the API DTO does not expose it.
	TotalScore int
}

// SessionState is a session together with the question it currently waits on.
type SessionState struct {
	Session         Session
	CurrentQuestion *Question
}

// Event is a domain event recorded in triage_events. Payload is already-encoded JSON so
// the store does no marshalling.
type Event struct {
	ID        string
	SessionID string
	Type      string
	Payload   []byte
}
