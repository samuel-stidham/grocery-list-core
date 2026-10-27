package api

import (
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWriteJSONEncodeFailureIsAFixed500(t *testing.T) {
	recorder := httptest.NewRecorder()

	writeJSON(recorder, http.StatusOK, map[string]float64{"amount": math.Inf(1)})

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", recorder.Code)
	}

	if recorder.Body.String() != encodeFailureBody {
		t.Fatalf("body %q", recorder.Body.String())
	}

	if recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("content type %q", recorder.Header().Get("Content-Type"))
	}
}
