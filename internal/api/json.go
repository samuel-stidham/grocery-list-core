// Package api serves the read-only nutrient API under /v1.
package api

import (
	"encoding/json"
	"net/http"
)

// encodeFailureBody is the fixed 500 body for a value that cannot be encoded.
// It is a literal so that the failure path cannot fail again.
const encodeFailureBody = `{"error":{"code":"internal","message":"response could not be encoded"}}`

// writeJSON encodes the value before touching the response, so an encoding
// failure (a NaN or Inf from an overflowed sum) becomes a 500 with the error
// shape instead of a 200 with an empty body.
func writeJSON(writer http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(encodeFailureBody)
	}

	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}
