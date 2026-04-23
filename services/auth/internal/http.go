package internal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

const maxRequestBody = 1 << 20

func NewHTTP(svc *Service) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := svc.Ready(ctx); err != nil {
			writeError(w, r, http.StatusServiceUnavailable, err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
	})

	mux.HandleFunc("/tenants", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}

		var req TenantInput
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, r, http.StatusBadRequest, err)
			return
		}

		if err := svc.EnsureTenant(r.Context(), req.Tenant); err != nil {
			writeError(w, r, statusForError(err), err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"tenant": stringsLowerTrim(req.Tenant),
			"status": "ready",
		})
	})

	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}

		var req RegisterInput
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, r, http.StatusBadRequest, err)
			return
		}

		if err := svc.Register(r.Context(), req); err != nil {
			writeError(w, r, statusForError(err), err)
			return
		}

		writeJSON(w, http.StatusCreated, map[string]string{
			"status": "created",
		})
	})

	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w, http.MethodPost)
			return
		}

		var req LoginInput
		if err := decodeJSON(w, r, &req); err != nil {
			writeError(w, r, http.StatusBadRequest, err)
			return
		}

		token, err := svc.Login(r.Context(), req)
		if err != nil {
			writeError(w, r, statusForError(err), err)
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{
			"token_type": "Bearer",
			"token":      token,
		})
	})

	return mux
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return ErrInvalidRequest
		}
		return ErrInvalidRequest
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrInvalidRequest
	}

	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)

	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(v)
}

func writeError(w http.ResponseWriter, r *http.Request, status int, err error) {
	if status >= 500 {
		log.Printf("%s %s: %v", r.Method, r.URL.Path, err)
	}

	message := err.Error()
	if status >= 500 {
		message = http.StatusText(status)
	}

	http.Error(w, message, status)
}

func statusForError(err error) int {
	switch {
	case errors.Is(err, ErrInvalidRequest):
		return http.StatusBadRequest
	case errors.Is(err, ErrInvalidTenant):
		return http.StatusBadRequest
	case errors.Is(err, ErrTenantNotFound):
		return http.StatusNotFound
	case errors.Is(err, ErrInvalidEmail):
		return http.StatusBadRequest
	case errors.Is(err, ErrPasswordTooShort):
		return http.StatusBadRequest
	case errors.Is(err, ErrInvalidCredentials):
		return http.StatusUnauthorized
	case errors.Is(err, ErrEmailAlreadyExists):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

func methodNotAllowed(w http.ResponseWriter, allowed string) {
	w.Header().Set("Allow", allowed)
	http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
}

func stringsLowerTrim(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
