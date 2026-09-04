package flow

import (
	"testing"

	"github.com/stretchr/testify/require"

	"aramana/internal/model"
)

func answer(questionCode string, score int, domain *string, weighted bool) model.Answer {
	return model.Answer{
		QuestionCode:  questionCode,
		Score:         score,
		DomainCode:    domain,
		ScoreWeighted: weighted,
	}
}

func TestDecide_HighRisk(t *testing.T) {
	t.Parallel()

	decision := Decide(
		State{Domain: new("DEPRESSION")},
		model.Option{ID: "opt-risk", QuestionID: "q-risk", Value: "yes", RiskFlag: true, NextQuestionID: new("q-next")},
		new("ANXIETY"),
	)

	require.Equal(t, KindHighRisk, decision.Kind)
	require.Nil(t, decision.NextQuestionID)
	require.NotNil(t, decision.Result)
	require.Equal(t, model.RiskHigh, decision.Result.RiskLevel)
	require.Equal(t, model.ActionImmediateSupport, decision.Result.RecommendedAction)
	require.Len(t, decision.Events, 1)
	require.Equal(t, HighRiskDetected, decision.Events[0].Type)
	require.Equal(t, "opt-risk", decision.Events[0].Payload["option_id"])
}

func TestDecide_Routing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		state      State
		nextDomain *string
		wantDomain string
	}{
		{
			name:       "keeps the current domain",
			state:      State{Domain: new("ANXIETY")},
			wantDomain: "ANXIETY",
		},
		{
			name:       "uses the next question domain",
			nextDomain: new("DEPRESSION"),
			wantDomain: "DEPRESSION",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			decision := Decide(
				tc.state,
				model.Option{ID: "opt", QuestionID: "q", NextQuestionID: new("q-next")},
				tc.nextDomain,
			)

			require.Equal(t, KindNextQuestion, decision.Kind)
			require.NotNil(t, decision.Domain)
			require.Equal(t, tc.wantDomain, *decision.Domain)
		})
	}
}

func TestDecide_UsesLastAnsweredDomain(t *testing.T) {
	t.Parallel()

	decision := Decide(
		State{Answers: []model.Answer{answer("anxiety_worry", 2, new("ANXIETY"), true)}},
		model.Option{ID: "opt", QuestionID: "q"},
		nil,
	)

	require.NotNil(t, decision.Result)
	require.NotNil(t, decision.Result.PrimaryDomain)
	require.Equal(t, "ANXIETY", *decision.Result.PrimaryDomain)
}

func TestEvaluate_RiskLevels(t *testing.T) {
	t.Parallel()

	rules := DefaultRuleSet()
	domain := new("DEPRESSION")
	cases := []struct {
		name    string
		answers []model.Answer
		risk    string
		action  string
	}{
		{"low", []model.Answer{answer("q", 1, domain, true)}, model.RiskLow, model.ActionSelfCareResources},
		{"medium", []model.Answer{answer("q", rules.MediumScore, domain, true)}, model.RiskMedium, model.ActionSpecialistAssessment},
		{"high", []model.Answer{answer("q", rules.HighScore, domain, true)}, model.RiskHigh, model.ActionPriorityAssessment},
		{
			"does not count an intake answer",
			[]model.Answer{answer("main_reason", 9, nil, false), answer("q", 1, domain, true)},
			model.RiskLow,
			model.ActionSelfCareResources,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := Evaluate(domain, tc.answers, rules)
			require.Equal(t, tc.risk, result.RiskLevel)
			require.Equal(t, tc.action, result.RecommendedAction)
		})
	}
}

func TestHighRiskResult_IsDifferentFromScoredHighRisk(t *testing.T) {
	t.Parallel()

	rules := DefaultRuleSet()
	domain := new("DEPRESSION")
	answers := []model.Answer{answer("q", rules.HighScore, domain, true)}

	scored := Evaluate(domain, answers, rules)
	gated := HighRiskResult(domain, answers)

	require.Equal(t, model.RiskHigh, scored.RiskLevel)
	require.Equal(t, model.RiskHigh, gated.RiskLevel)
	require.Equal(t, model.ActionPriorityAssessment, scored.RecommendedAction)
	require.Equal(t, model.ActionImmediateSupport, gated.RecommendedAction)
}
