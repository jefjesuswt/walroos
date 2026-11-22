package whttp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

func JSON(
	w http.ResponseWriter,
	statusCode int,
	data any,
) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(data); err != nil {
		http.Error(w, `{"error":"internal server error"}`, statusCode)
	}
}

func ErrorJSON(w http.ResponseWriter, statusCode int, message string) {
	JSON(w, statusCode, map[string]string{"error": message})
}

// Decode reads r's JSON body into v (an empty body leaves v as is). On bad JSON it answers 400
// and returns false: the handler stops there.
func Decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		ErrorJSON(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}
