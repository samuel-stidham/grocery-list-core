package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
)

// ErrNutrientMissing means the database lacks one of the nutrients the API
// always reports, so it was not built by this importer.
var ErrNutrientMissing = errors.New("database lacks a required nutrient")

// Error codes used in the error body. The HTTP status says how bad, the code
// says what kind, and the message says what to change.
const (
	codeBadRequest = "bad_request"
	codeNotFound   = "not_found"
	codeMethod     = "method_not_allowed"
	codeForbidden  = "forbidden"
	codeInternal   = "internal"
)

// internalMessage is the only text a 5xx body carries. The real error goes to
// the log, because driver errors can include the database path.
const internalMessage = "internal error"

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(writer http.ResponseWriter, status int, code, message string) {
	writeJSON(writer, status, errorBody{Error: errorDetail{Code: code, Message: message}})
}

// internalError logs the cause with the route and answers with the fixed
// message. Every handler's 500 path goes through here. A client that went
// away cancels the request context, and its query fails with that
// cancellation. That is not a server fault, so it is logged at info.
func (server *Server) internalError(writer http.ResponseWriter, request *http.Request, err error) {
	level, message := slog.LevelError, "request failed"
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		level, message = slog.LevelInfo, "request cancelled"
	}

	server.logger.LogAttrs(context.WithoutCancel(request.Context()), level, message,
		slog.String("method", request.Method),
		slog.String("path", request.URL.Path),
		slog.String("error", err.Error()),
	)
	writeError(writer, http.StatusInternalServerError, codeInternal, internalMessage)
}
