package store

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"aramana/internal/db"
	"aramana/internal/ids"
	"aramana/internal/model"
)

const testDatabaseURLEnv = "TEST_DATABASE_URL"

var (
	testPool *pgxpool.Pool
	testRepo *PostgresRepository
	testUoW  *TxRunner
)

func TestMain(m *testing.M) {
	dsn := os.Getenv(testDatabaseURLEnv)
	if dsn != "" {
		ctx := context.Background()
		pool, err := pgxpool.New(ctx, dsn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "connect integration database: %v\n", err)
			os.Exit(1)
		}
		if err := pool.Ping(ctx); err != nil {
			pool.Close()
			fmt.Fprintf(os.Stderr, "ping integration database: %v\n", err)
			os.Exit(1)
		}
		if err := db.RunMigrations(ctx, pool); err != nil {
			pool.Close()
			fmt.Fprintf(os.Stderr, "migrate integration database: %v\n", err)
			os.Exit(1)
		}

		testPool = pool
		testRepo = NewPostgresRepository(pool)
		testUoW = NewTxRunner(pool)
	}

	code := m.Run()
	if testPool != nil {
		testPool.Close()
	}
	os.Exit(code)
}

func integrationRepository(t *testing.T) (*PostgresRepository, *pgxpool.Pool) {
	t.Helper()
	if testPool == nil {
		t.Skipf("%s is not set, skipping PostgreSQL integration test", testDatabaseURLEnv)
	}
	return testRepo, testPool
}

func TestPostgresRepositoryEntryQuestion(t *testing.T) {
	t.Parallel()

	repo, _ := integrationRepository(t)
	question, err := repo.EntryQuestion(t.Context())

	require.NoError(t, err)
	assert.True(t, question.IsEntry)
	assert.Equal(t, "main_reason", question.Code)
	assert.Len(t, question.Options, 4)
	assert.Equal(t, []string{"sad", "anxious", "trauma", "unsure"}, optionValues(question.Options))
}

func TestPostgresRepositoryOptionOwnership(t *testing.T) {
	t.Parallel()

	repo, _ := integrationRepository(t)
	entry, err := repo.EntryQuestion(t.Context())
	require.NoError(t, err)
	require.NotNil(t, entry.Options[0].NextQuestionID)

	option, err := repo.OptionForQuestion(t.Context(), entry.ID, entry.Options[0].ID)
	require.NoError(t, err)
	assert.Equal(t, entry.Options[0].ID, option.ID)

	_, err = repo.OptionForQuestion(t.Context(), *entry.Options[0].NextQuestionID, entry.Options[0].ID)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestPostgresRepositoryPersistsOneTransaction(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	repo, pool := integrationRepository(t)
	entry, err := repo.EntryQuestion(ctx)
	require.NoError(t, err)

	session := model.Session{
		ID:                ids.NewString(),
		Status:            model.StatusInProgress,
		CurrentQuestionID: &entry.ID,
	}
	answer := model.Answer{
		ID:         ids.NewString(),
		SessionID:  session.ID,
		QuestionID: entry.ID,
		OptionID:   entry.Options[0].ID,
	}
	result := model.TriageResult{
		RiskLevel:         model.RiskLow,
		RecommendedAction: model.ActionSelfCareResources,
	}
	record := IdempotencyRecord{
		Key:                "repository-" + ids.NewString(),
		SessionID:          session.ID,
		RequestFingerprint: "request-fingerprint",
	}
	response := []byte(`{"outcome":"NEXT_QUESTION"}`)

	err = testUoW.WithTx(ctx, func(txRepo TriageRepository) error {
		require.NoError(t, txRepo.CreateSession(ctx, session))
		require.NoError(t, txRepo.InsertAnswer(ctx, answer))
		require.NoError(t, txRepo.UpdateSessionState(ctx, model.SessionUpdate{
			SessionID: session.ID,
			Status:    model.StatusCompleted}))

		require.NoError(t, txRepo.UpsertResult(ctx, session.ID, result))

		require.NoError(t, txRepo.AppendEvent(ctx, model.Event{
			ID: ids.NewString(), SessionID: session.ID, Type: "repository.test", Payload: []byte(`{}`),
		}))

		reserved, err := txRepo.ReserveIdempotencyKey(ctx, record)
		require.NoError(t, err)
		assert.True(t, reserved)

		return txRepo.CompleteIdempotencyKey(ctx, record.Key, response)
	})
	require.NoError(t, err)

	persistedSession, err := repo.SessionByID(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, model.StatusCompleted, persistedSession.Status)
	assert.Nil(t, persistedSession.CurrentQuestionID)

	answers, err := repo.AnswersForSession(ctx, session.ID)
	require.NoError(t, err)
	require.Len(t, answers, 1)
	assert.Equal(t, entry.Options[0].Value, answers[0].OptionValue)
	assert.False(t, answers[0].ScoreWeighted)

	persistedResult, err := repo.ResultForSession(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, result, persistedResult)

	persistedRecord, err := repo.FindIdempotentResponse(ctx, record.Key)
	require.NoError(t, err)
	assert.JSONEq(t, string(response), string(persistedRecord.ResponseBody))

	var events int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM triage_events WHERE session_id = $1 AND event_type = 'repository.test'`, session.ID).
		Scan(&events))
	assert.Equal(t, 1, events)
}

func optionValues(options []model.Option) []string {
	values := make([]string, 0, len(options))
	for _, option := range options {
		values = append(values, option.Value)
	}
	return values
}
