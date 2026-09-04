package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"aramana/internal/apierr"
	"aramana/internal/dependencies"
	"aramana/internal/model"
	"aramana/internal/service"
)

const (
	testSessionID  = "11111111-1111-4111-8111-111111111111"
	testQuestionID = "22222222-2222-4222-8222-222222222222"
	testOptionID   = "33333333-3333-4333-8333-333333333333"
)

// --- test doubles ---------------------------------------------------------------------

type fakeTriage struct {
	state       *model.SessionState
	submitted   *service.SubmitAnswerResult
	result      *model.TriageResult
	err         error
	lastCommand service.SubmitAnswerCommand
	panicOnCall bool
}

func (f *fakeTriage) CreateSession(context.Context) (*model.SessionState, error) {
	if f.panicOnCall {
		panic("boom")
	}
	return f.state, f.err
}

func (f *fakeTriage) GetSession(context.Context, string) (*model.SessionState, error) {
	return f.state, f.err
}

func (f *fakeTriage) SubmitAnswer(_ context.Context, cmd service.SubmitAnswerCommand) (*service.SubmitAnswerResult, error) {
	f.lastCommand = cmd
	return f.submitted, f.err
}

func (f *fakeTriage) GetResult(context.Context, string) (*model.TriageResult, error) {
	return f.result, f.err
}

type fakeReadiness struct{ err error }

func (f fakeReadiness) Ping(context.Context) error { return f.err }

type fakeBase struct{ logger *slog.Logger }

func (f fakeBase) Logger() *slog.Logger { return f.logger }

type fakeScope struct {
	triage    dependencies.TriageService
	readiness dependencies.ReadinessChecker
}

func (f fakeScope) Base() dependencies.Base {
	return fakeBase{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}
func (f fakeScope) Readiness() dependencies.ReadinessChecker { return f.readiness }
func (f fakeScope) Triage() dependencies.TriageService       { return f.triage }
func (f fakeScope) Close()                                   {}

var _ dependencies.ServiceScope = fakeScope{}

func newTestRouter(triage *fakeTriage, readiness error) http.Handler {
	return NewRouter(fakeScope{triage: triage, readiness: fakeReadiness{err: readiness}})
}

func do(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequestWithContext(t.Context(), method, path, reader)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func decodeError(t *testing.T, recorder *httptest.ResponseRecorder) apierr.Response {
	t.Helper()
	var response apierr.Response
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response), recorder.Body.String())
	return response
}

func requireError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	require.Equal(t, status, recorder.Code, recorder.Body.String())
	require.Equal(t, code, decodeError(t, recorder).Code)
}

func sampleState() *model.SessionState {
	return &model.SessionState{
		Session: model.Session{
			ID:                testSessionID,
			Status:            model.StatusInProgress,
			CurrentQuestionID: new(testQuestionID),
			CurrentDomain:     new("DEPRESSION"),
		},
		CurrentQuestion: &model.Question{
			ID:     testQuestionID,
			Code:   "risk_check",
			Prompt: "Are you in immediate danger?",
			Options: []model.Option{
				{ID: testOptionID, Value: "yes", Label: "Yes", Score: 7, RiskFlag: true, NextQuestionID: new("secret-question")},
			},
		},
	}
}

// --- tests ---------------------------------------------------------------------------

func TestCreateSession_Returns201(t *testing.T) {
	t.Parallel()
	router := newTestRouter(&fakeTriage{state: sampleState()}, nil)

	recorder := do(t, router, http.MethodPost, "/triage/sessions", "")

	var response SessionStateResponse
	require.Equal(t, http.StatusCreated, recorder.Code, recorder.Body.String())
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, testSessionID, response.Session.SessionID)
}

// The clinical scoring model must not reach the client: knowing which option carries the
// risk flag, what an answer is worth, and where it routes is server-side information.
func TestResponses_DoNotLeakScoringOrRoutingMetadata(t *testing.T) {
	t.Parallel()
	router := newTestRouter(&fakeTriage{state: sampleState()}, nil)

	body := do(t, router, http.MethodPost, "/triage/sessions", "").Body.String()

	for _, leaked := range []string{"score", "risk_flag", "riskFlag", "next_question", "nextQuestionId", "secret-question", "7"} {
		require.NotContains(t, body, leaked)
	}
	for _, needed := range []string{"sessionId", "currentQuestion", "prompt", "label", "value"} {
		require.Contains(t, body, needed)
	}
}

func TestSubmitAnswer_PassesIdempotencyKeyThrough(t *testing.T) {
	t.Parallel()
	triage := &fakeTriage{submitted: &service.SubmitAnswerResult{
		Session: model.Session{ID: testSessionID, Status: model.StatusInProgress},
		Outcome: service.OutcomeNextQuestion,
	}}
	router := newTestRouter(triage, nil)

	recorder := do(t, router, http.MethodPost, "/triage/sessions/"+testSessionID+"/answers",
		`{"questionId":"`+testQuestionID+`","optionId":"`+testOptionID+`","idempotencyKey":"key-1"}`)

	require.Equal(t, http.StatusOK, recorder.Code, recorder.Body.String())
	require.Equal(t, "key-1", triage.lastCommand.IdempotencyKey)
	require.Equal(t, testSessionID, triage.lastCommand.SessionID)
}

func TestSubmitAnswer_ExposesOutcomeAsAStableValue(t *testing.T) {
	t.Parallel()
	router := newTestRouter(&fakeTriage{submitted: &service.SubmitAnswerResult{
		Session:  model.Session{ID: testSessionID, Status: model.StatusHighRisk, HighRiskDetected: true},
		HighRisk: true,
		Outcome:  service.OutcomeHighRisk,
		Result: &model.TriageResult{
			PrimaryDomain: new("DEPRESSION"), RiskLevel: model.RiskHigh,
			RecommendedAction: model.ActionImmediateSupport, TotalScore: 3,
		},
	}}, nil)

	recorder := do(t, router, http.MethodPost, "/triage/sessions/"+testSessionID+"/answers",
		`{"questionId":"`+testQuestionID+`","optionId":"`+testOptionID+`"}`)

	var response SubmitAnswerResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.Equal(t, string(service.OutcomeHighRisk), response.Outcome)
	require.True(t, response.HighRisk)
	require.NotNil(t, response.Result)
	require.Equal(t, model.RiskHigh, response.Result.RiskLevel)
	for _, leaked := range []string{"total_score", "totalScore"} {
		require.NotContains(t, recorder.Body.String(), leaked)
	}
}

func TestDomainErrors_MapToTheDocumentedStatusesAndCodes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{service.ErrSessionNotFound, http.StatusNotFound, apierr.CodeSessionNotFound},
		{service.ErrSessionClosed, http.StatusConflict, apierr.CodeSessionClosed},
		{service.ErrQuestionMismatch, http.StatusConflict, apierr.CodeQuestionMismatch},
		{service.ErrSessionNotComplete, http.StatusConflict, apierr.CodeSessionNotComplete},
		{service.ErrIdempotencyKeyReused, http.StatusUnprocessableEntity, apierr.CodeIdempotencyKeyReused},
		{service.ErrOptionNotFound, http.StatusBadRequest, apierr.CodeOptionNotFound},
		{service.ErrInvalidRequest, http.StatusBadRequest, apierr.CodeInvalidRequest},
		{errors.New("something unmapped"), http.StatusInternalServerError, apierr.CodeInternalError},
	}

	for _, tc := range cases {
		t.Run(tc.wantCode, func(t *testing.T) {
			t.Parallel()
			router := newTestRouter(&fakeTriage{err: tc.err}, nil)

			recorder := do(t, router, http.MethodPost, "/triage/sessions/"+testSessionID+"/answers",
				`{"questionId":"`+testQuestionID+`","optionId":"`+testOptionID+`"}`)

			requireError(t, recorder, tc.wantStatus, tc.wantCode)
		})
	}
}

// An unmapped error must not expose its message to the client.
func TestInternalError_DoesNotLeakTheUnderlyingMessage(t *testing.T) {
	t.Parallel()
	router := newTestRouter(&fakeTriage{err: errors.New("pq: relation does not exist")}, nil)

	recorder := do(t, router, http.MethodGet, "/triage/sessions/"+testSessionID, "")

	require.NotContains(t, recorder.Body.String(), "relation does not exist")
}

func TestGetResult_NotCompleteIsAConflictNotAServerError(t *testing.T) {
	t.Parallel()
	router := newTestRouter(&fakeTriage{err: service.ErrSessionNotComplete}, nil)

	recorder := do(t, router, http.MethodGet, "/triage/sessions/"+testSessionID+"/result", "")

	requireError(t, recorder, http.StatusConflict, apierr.CodeSessionNotComplete)
}

func TestInvalidPathAndPayload_AreRejectedWithInvalidRequest(t *testing.T) {
	t.Parallel()
	validBody := `{"questionId":"` + testQuestionID + `","optionId":"` + testOptionID + `"}`

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"session id is not a uuid", http.MethodGet, "/triage/sessions/not-a-uuid", ""},
		{"answers on a non-uuid session", http.MethodPost, "/triage/sessions/not-a-uuid/answers", validBody},
		{"result of a non-uuid session", http.MethodGet, "/triage/sessions/not-a-uuid/result", ""},
		{"malformed json", http.MethodPost, "/triage/sessions/" + testSessionID + "/answers", `{"questionId":`},
		{"unknown field", http.MethodPost, "/triage/sessions/" + testSessionID + "/answers",
			`{"questionId":"` + testQuestionID + `","optionId":"` + testOptionID + `","extra":1}`},
		{"trailing json", http.MethodPost, "/triage/sessions/" + testSessionID + "/answers", validBody + `{"more":1}`},
		{"missing option id", http.MethodPost, "/triage/sessions/" + testSessionID + "/answers",
			`{"questionId":"` + testQuestionID + `"}`},
		{"question id is not a uuid", http.MethodPost, "/triage/sessions/" + testSessionID + "/answers",
			`{"questionId":"nope","optionId":"` + testOptionID + `"}`},
		{"empty body", http.MethodPost, "/triage/sessions/" + testSessionID + "/answers", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			router := newTestRouter(&fakeTriage{submitted: &service.SubmitAnswerResult{}}, nil)

			recorder := do(t, router, tc.method, tc.path, tc.body)

			requireError(t, recorder, http.StatusBadRequest, apierr.CodeInvalidRequest)
		})
	}
}

func TestOversizedBody_IsRejectedWithoutBeingRead(t *testing.T) {
	t.Parallel()
	router := newTestRouter(&fakeTriage{}, nil)

	oversized := `{"questionId":"` + testQuestionID + `","optionId":"` +
		strings.Repeat("a", maxRequestBodyBytes+1) + `"}`
	recorder := do(t, router, http.MethodPost, "/triage/sessions/"+testSessionID+"/answers", oversized)

	requireError(t, recorder, http.StatusRequestEntityTooLarge, apierr.CodeRequestTooLarge)
}

func TestRequestID_IsGeneratedAndEchoed(t *testing.T) {
	t.Parallel()
	router := newTestRouter(&fakeTriage{state: sampleState()}, nil)

	recorder := do(t, router, http.MethodPost, "/triage/sessions", "")

	require.NotEmpty(t, recorder.Header().Get("X-Request-ID"))
}

func TestRequestID_IsAdoptedFromTheClientAndReturnedInErrors(t *testing.T) {
	t.Parallel()
	router := newTestRouter(&fakeTriage{err: service.ErrSessionNotFound}, nil)

	req := httptest.NewRequest(http.MethodGet, "/triage/sessions/"+testSessionID, nil)
	req.Header.Set("X-Request-ID", "caller-supplied-id")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, "caller-supplied-id", recorder.Header().Get("X-Request-ID"))
	require.Equal(t, "caller-supplied-id", decodeError(t, recorder).RequestID)
}

func TestHealthAndReadiness(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		path       string
		readiness  error
		wantStatus int
		wantCode   string
	}{
		{"health ignores database", "/health", errors.New("database down"), http.StatusOK, ""},
		{"ready fails with database", "/ready", errors.New("database down"), http.StatusServiceUnavailable, apierr.CodeDBUnavailable},
		{"ready succeeds", "/ready", nil, http.StatusOK, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			recorder := do(t, newTestRouter(&fakeTriage{}, tc.readiness), http.MethodGet, tc.path, "")
			require.Equal(t, tc.wantStatus, recorder.Code, recorder.Body.String())
			if tc.wantCode != "" {
				require.Equal(t, tc.wantCode, decodeError(t, recorder).Code)
			}
		})
	}
}

func TestDocumentationServesEmbeddedSwagger(t *testing.T) {
	t.Parallel()
	router := newTestRouter(&fakeTriage{}, nil)

	jsonResponse := do(t, router, http.MethodGet, "/documentation/swagger.json", "")
	require.Equal(t, http.StatusOK, jsonResponse.Code, jsonResponse.Body.String())

	var spec struct {
		Swagger string `json:"swagger"`
		Info    struct {
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"info"`
		Paths map[string]json.RawMessage `json:"paths"`
	}
	require.NoError(t, json.Unmarshal(jsonResponse.Body.Bytes(), &spec))
	require.Equal(t, "2.0", spec.Swagger)
	require.Equal(t, "ARAMANA Adaptive Intelligent Triage API", spec.Info.Title)
	require.Equal(t, "1.0.0", spec.Info.Version)
	require.Contains(t, spec.Paths, "/triage/sessions")

	yamlResponse := do(t, router, http.MethodGet, "/documentation/swagger.yaml", "")
	require.Equal(t, http.StatusOK, yamlResponse.Code, yamlResponse.Body.String())
	require.Contains(t, yamlResponse.Body.String(), "swagger: \"2.0\"")
}

// A panic must come back in the same error envelope as everything else, with its request id.
func TestPanic_IsRecoveredIntoTheStandardErrorBody(t *testing.T) {
	t.Parallel()
	router := newTestRouter(&fakeTriage{panicOnCall: true}, nil)

	req := httptest.NewRequest(http.MethodPost, "/triage/sessions", nil)
	req.Header.Set("X-Request-ID", "panic-request")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	require.Equal(t, http.StatusInternalServerError, recorder.Code)
	response := decodeError(t, recorder)
	require.Equal(t, apierr.CodeInternalError, response.Code)
	require.Equal(t, "panic-request", response.RequestID)
}
