package apierr

import (
	"errors"
	"net/http"

	"aramana/internal/service"
)

// Stable machine-readable error codes. Clients branch on these, not on the message.
const (
	CodeInvalidRequest       = "invalid_request"
	CodeOptionNotFound       = "option_not_found"
	CodeSessionNotFound      = "session_not_found"
	CodeQuestionMismatch     = "question_mismatch"
	CodeSessionClosed        = "session_closed"
	CodeSessionNotComplete   = "session_not_complete"
	CodeIdempotencyKeyReused = "idempotency_key_reused"
	CodeRequestTooLarge      = "request_too_large"
	CodeRouteNotFound        = "route_not_found"
	CodeMethodNotAllowed     = "method_not_allowed"
	CodeDBUnavailable        = "db_unavailable"
	CodeInternalError        = "internal_error"
)

// Response is the body of every error reply.
type Response struct {
	// Code is the stable machine-readable error code. Clients branch on this.
	Code string `json:"code" enums:"invalid_request,option_not_found,session_not_found,question_mismatch,session_closed,session_not_complete,idempotency_key_reused,request_too_large,route_not_found,method_not_allowed,db_unavailable,internal_error"`
	// Message is human-readable and not stable; do not branch on it.
	Message string `json:"message"`
	// RequestID is the correlation ID, also returned in the X-Request-ID header.
	RequestID string `json:"requestId"`
}

// APIError is the HTTP shape of a domain error.
type APIError struct {
	Status  int
	Code    string
	Message string
}

func FromError(err error) APIError {
	switch {
	case errors.Is(err, service.ErrSessionNotFound):
		return APIError{http.StatusNotFound, CodeSessionNotFound, "triage session not found"}
	case errors.Is(err, service.ErrSessionClosed):
		return APIError{http.StatusConflict, CodeSessionClosed, "triage session is closed"}
	case errors.Is(err, service.ErrQuestionMismatch):
		return APIError{http.StatusConflict, CodeQuestionMismatch, "submitted answer does not match current question"}
	case errors.Is(err, service.ErrSessionNotComplete):
		return APIError{http.StatusConflict, CodeSessionNotComplete, "triage session is not complete yet"}
	case errors.Is(err, service.ErrIdempotencyKeyReused):
		return APIError{http.StatusUnprocessableEntity, CodeIdempotencyKeyReused, "idempotency key was already used with a different payload"}
	case errors.Is(err, service.ErrOptionNotFound):
		return APIError{http.StatusBadRequest, CodeOptionNotFound, "selected option does not belong to question"}
	case errors.Is(err, service.ErrInvalidRequest):
		return APIError{http.StatusBadRequest, CodeInvalidRequest, "invalid request"}
	default:
		return APIError{http.StatusInternalServerError, CodeInternalError, "internal server error"}
	}
}
