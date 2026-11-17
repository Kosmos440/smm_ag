package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
)

const maxRequestSize = 32 << 10

type errorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type errorResponse struct {
	Error errorBody `json:"error"`
}

type listResponse[T any] struct {
	Items  []T `json:"items"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Printf("HTTP response write failed: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Error: errorBody{Code: code, Message: message}})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Use application/json.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestSize)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeDecodeError(w, err)
		return false
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		writeDecodeError(w, err)
		return false
	}
	return true
}

func writeDecodeError(w http.ResponseWriter, err error) {
	var limitErr *http.MaxBytesError
	if errors.As(err, &limitErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "JSON body exceeds 32 KiB.")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_json", "Expected a single JSON object with supported fields.")
}

func parseID(w http.ResponseWriter, name, value string) (int64, bool) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid_id", name+" must be a positive integer.")
		return 0, false
	}
	return id, true
}

func parsePage(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	limit, offset := 50, 0
	var err error
	query := r.URL.Query()
	if query.Has("limit") {
		limit, err = strconv.Atoi(query.Get("limit"))
		if err != nil || limit < 1 || limit > 100 {
			writeError(w, http.StatusBadRequest, "invalid_page", "limit must be between 1 and 100.")
			return 0, 0, false
		}
	}
	if query.Has("offset") {
		offset, err = strconv.Atoi(query.Get("offset"))
		if err != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, "invalid_page", "offset must be a non-negative integer.")
			return 0, 0, false
		}
	}
	return limit, offset, true
}

func writeList[T any](w http.ResponseWriter, items []T, limit, offset int) {
	if items == nil {
		items = []T{}
	}
	writeJSON(w, http.StatusOK, listResponse[T]{Items: items, Limit: limit, Offset: offset})
}
