package flow

import (
	"aramana/internal/model"
)

// Kind describes the shape of a decision.
type Kind int

const (
	// KindNextQuestion means the flow continues with NextQuestionID.
	KindNextQuestion Kind = iota
	// KindCompleted means the flow ended normally and Result holds the outcome.
	KindCompleted
	// KindHighRisk means the safety gate fired: the flow stops immediately, no further
	// questions are asked, and an event is emitted.
	KindHighRisk
)

// Decision is the outcome of evaluating one answer against the session state.
type Decision struct {
	Kind Kind
	// NextQuestionID is set only for KindNextQuestion.
	NextQuestionID *string
	// Domain is the session domain after this decision. It is nil while the flow has not
	// routed into a domain yet, and is left unchanged by non-domain questions.
	Domain *string
	// Result is set for KindCompleted and KindHighRisk.
	Result *model.TriageResult
	// Events are the domain events to record in the same transaction.
	Events []Event
}

type Event struct {
	Type    string
	Payload map[string]any
}

// HighRiskDetected is emitted when the safety gate stops the flow.
const HighRiskDetected = "triage.high_risk_detected"

type RuleSet struct {
	MediumScore int
	HighScore   int
}

// DefaultRuleSet returns the scoring thresholds.
func DefaultRuleSet() RuleSet {
	return RuleSet{MediumScore: 2, HighScore: 3}
}

// State is everything Decide needs to know about a session.
type State struct {
	// Domain is the session's current domain, nil if the flow has not routed yet.
	Domain *string
	// Answers includes the selected answer.
	Answers []model.Answer
	Rules   RuleSet
}

func Decide(state State, selected model.Option, nextQuestionDomain *string) Decision {
	domain := resolveDomain(state.Domain, state.Answers)

	if selected.RiskFlag {
		result := HighRiskResult(domain, state.Answers)
		return Decision{
			Kind:   KindHighRisk,
			Domain: domain,
			Result: &result,
			Events: []Event{{
				Type: HighRiskDetected,
				Payload: map[string]any{
					"question_id":  selected.QuestionID,
					"option_id":    selected.ID,
					"option_value": selected.Value,
				},
			}},
		}
	}

	if selected.NextQuestionID != nil {
		next := domain
		if nextQuestionDomain != nil {
			next = nextQuestionDomain
		}
		return Decision{Kind: KindNextQuestion, NextQuestionID: selected.NextQuestionID, Domain: next}
	}

	result := Evaluate(domain, state.Answers, state.Rules)
	return Decision{Kind: KindCompleted, Domain: domain, Result: &result}
}

// HighRiskResult is the outcome of a session stopped by the safety gate. The risk level is
// fixed rather than derived from the score: the gate firing is itself the finding.
func HighRiskResult(domain *string, answers []model.Answer) model.TriageResult {
	return model.TriageResult{
		PrimaryDomain:     domain,
		RiskLevel:         model.RiskHigh,
		RecommendedAction: model.ActionImmediateSupport,
		TotalScore:        totalScore(answers),
	}
}

func ResolveDomain(sessionDomain *string, answers []model.Answer) *string {
	return resolveDomain(sessionDomain, answers)
}

func resolveDomain(sessionDomain *string, answers []model.Answer) *string {
	if sessionDomain != nil {
		return sessionDomain
	}
	for i := len(answers) - 1; i >= 0; i-- {
		if answers[i].DomainCode != nil {
			return answers[i].DomainCode
		}
	}
	return nil
}

func totalScore(answers []model.Answer) int {
	total := 0
	for _, answer := range answers {
		if answer.ScoreWeighted {
			total += answer.Score
		}
	}
	return total
}

// Evaluate turns the answered questions into a final result.
func Evaluate(domain *string, answers []model.Answer, rules RuleSet) model.TriageResult {
	total := totalScore(answers)

	riskLevel := model.RiskLow
	switch {
	case total >= rules.HighScore:
		riskLevel = model.RiskHigh
	case total >= rules.MediumScore:
		riskLevel = model.RiskMedium
	}

	action := model.ActionSelfCareResources
	switch riskLevel {
	case model.RiskHigh:
		action = model.ActionPriorityAssessment
	case model.RiskMedium:
		action = model.ActionSpecialistAssessment
	}

	return model.TriageResult{
		PrimaryDomain:     domain,
		RiskLevel:         riskLevel,
		RecommendedAction: action,
		TotalScore:        total,
	}
}
