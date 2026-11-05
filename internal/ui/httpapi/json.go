package httpapi

import (
	"encoding/json"
	"net/http"
)

func decode(r *http.Request, v any) error { return json.NewDecoder(r.Body).Decode(v) }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// respond — 204 при успехе, иначе ошибка с заданным статусом.
func respond(w http.ResponseWriter, err error, status int) {
	if err != nil {
		writeError(w, status, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
