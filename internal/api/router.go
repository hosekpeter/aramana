package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"aramana/internal/apierr"
	"aramana/internal/dependencies"
	"aramana/internal/logger"
	"aramana/internal/service"
)

var (
	errInvalidRequestPayload = errors.New("invalid request payload")
	errRouteNotFound         = errors.New("route not found")
	errMethodNotAllowed      = errors.New("method not allowed")
)

type handler struct {
	triage    dependencies.TriageService
	readiness dependencies.ReadinessChecker
	logger    *slog.Logger
}

// NewRouter wires the HTTP routes.
func NewRouter(scope dependencies.ServiceScope) http.Handler {
	// Named baseLogger rather than logger so it does not shadow the logger package, which
	// the handlers below use for ErrorAttr.
	baseLogger := scope.Base().Logger()
	h := &handler{
		triage:    scope.Triage(),
		readiness: scope.Readiness(),
		logger:    baseLogger,
	}

	router := chi.NewRouter()
	// Order matters: requestContext assigns the request ID that recoverPanic logs and
	// returns, so it has to run first.
	router.Use(requestContext(baseLogger))
	router.Use(recoverPanic(baseLogger))

	// Without these, chi answers an unknown path with a plain-text body, so a client that
	// mistypes a URL gets a different error shape than every other failure.
	router.NotFound(func(w http.ResponseWriter, r *http.Request) {
		h.writeError(w, r, http.StatusNotFound, apierr.CodeRouteNotFound, "route not found", errRouteNotFound)
	})
	router.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
		h.writeError(w, r, http.StatusMethodNotAllowed, apierr.CodeMethodNotAllowed, "method not allowed for this route", errMethodNotAllowed)
	})

	router.Get("/health", h.health)
	router.Get("/ready", h.ready)

	router.Route("/triage/sessions", func(r chi.Router) {
		r.Post("/", h.createSession)
		r.Get("/{session_id}", h.getSession)
		r.Post("/{session_id}/answers", h.submitAnswer)
		r.Get("/{session_id}/result", h.getResult)
	})

	return router
}

// health is a liveness probe: it answers as long as the process serves traffic.
func (h *handler) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ready is a readiness probe and does check the database, since the service cannot serve
// any triage request without it.
func (h *handler) ready(w http.ResponseWriter, r *http.Request) {
	if err := h.readiness.Ping(r.Context()); err != nil {
		h.writeError(w, r, http.StatusServiceUnavailable, apierr.CodeDBUnavailable, "database is unavailable", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (h *handler) createSession(w http.ResponseWriter, r *http.Request) {
	state, err := h.triage.CreateSession(r.Context())
	if err != nil {
		h.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, toSessionStateResponse(state))
}

func (h *handler) getSession(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := h.sessionIDParam(w, r)
	if !ok {
		return
	}

	state, err := h.triage.GetSession(r.Context(), sessionID)
	if err != nil {
		h.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSessionStateResponse(state))
}

func (h *handler) submitAnswer(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := h.sessionIDParam(w, r)
	if !ok {
		return
	}

	var req AnswerRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			h.writeError(w, r, http.StatusRequestEntityTooLarge, apierr.CodeRequestTooLarge, "request body is too large", err)
			return
		}
		h.writeError(w, r, http.StatusBadRequest, apierr.CodeInvalidRequest, "invalid request payload", err)
		return
	}
	// Reject trailing content, so a body with two concatenated JSON objects cannot be
	// silently accepted as the first one.
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		h.writeError(w, r, http.StatusBadRequest, apierr.CodeInvalidRequest, "invalid request payload", errInvalidRequestPayload)
		return
	}
	if !isValidUUID(req.QuestionID) || !isValidUUID(req.OptionID) {
		h.writeError(w, r, http.StatusBadRequest, apierr.CodeInvalidRequest, "questionId and optionId must be valid UUIDs", errInvalidRequestPayload)
		return
	}

	result, err := h.triage.SubmitAnswer(r.Context(), service.SubmitAnswerCommand{
		SessionID:      sessionID,
		QuestionID:     req.QuestionID,
		OptionID:       req.OptionID,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		h.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toSubmitAnswerResponse(result))
}

func (h *handler) getResult(w http.ResponseWriter, r *http.Request) {
	sessionID, ok := h.sessionIDParam(w, r)
	if !ok {
		return
	}

	result, err := h.triage.GetResult(r.Context(), sessionID)
	if err != nil {
		h.writeDomainError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toResultDTO(result))
}

// sessionIDParam validates the path parameter and writes the error response itself, so
// handlers stay linear.
func (h *handler) sessionIDParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	sessionID := chi.URLParam(r, "session_id")
	if !isValidUUID(sessionID) {
		h.writeError(w, r, http.StatusBadRequest, apierr.CodeInvalidRequest, "sessionId must be a valid UUID", errInvalidRequestPayload)
		return "", false
	}
	return sessionID, true
}

func isValidUUID(v string) bool {
	_, err := uuid.Parse(v)
	return err == nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// writeDomainError translates a service error and writes it.
func (h *handler) writeDomainError(w http.ResponseWriter, r *http.Request, err error) {
	apiError := apierr.FromError(err)
	h.writeError(w, r, apiError.Status, apiError.Code, apiError.Message, err)
}

func (h *handler) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, err error) {
	requestID := requestIDFromContext(r.Context())
	writeJSON(w, status, apierr.Response{Code: code, Message: message, RequestID: requestID})

	log := h.logger.Warn
	if status >= http.StatusInternalServerError {
		log = h.logger.Error
	}

	log("request_failed",
		"request_id", requestID,
		"code", code,
		slog.Group("http",
			"method", r.Method,
			"url", r.URL.Path,
			"status_code", status,
		),
		logger.ErrorAttr(err),
	)
}
