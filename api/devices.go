// Package api supplies authenticated installation routes with the same wire
// contract as the native libraries. Authentication and HTTP hosting are app-owned.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bjthomas07/push-dispatch/push"
)

// Authenticate must verify credentials and return a stable, trusted user ID.
// It must never read a user ID from a client-controlled header without verification.
type Authenticate func(*http.Request) (string, error)

func Devices(store push.InstallationStore, authenticate Authenticate) http.Handler {
	mux := http.NewServeMux()
	wrap := func(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if store == nil || authenticate == nil {
				http.Error(w, "service unavailable", 503)
				return
			}
			uid, err := authenticate(r)
			if err != nil || uid == "" || len(uid) > 128 || strings.ContainsAny(uid, "/\x00") || uid == "." || uid == ".." {
				http.Error(w, "unauthorized", 401)
				return
			}
			next(w, r, uid)
		}
	}
	mux.HandleFunc("POST /v1/devices", wrap(func(w http.ResponseWriter, r *http.Request, uid string) {
		var request push.RegistrationRequest
		if !decode(w, r, &request) {
			return
		}
		id, installation, err := push.NewInstallation(request, time.Now())
		if err != nil {
			http.Error(w, "invalid registration", 400)
			return
		}
		if err = store.UpsertPushInstallation(r.Context(), uid, id, installation); err != nil {
			storeError(w, err)
			return
		}
		receipt(w, id)
	}))
	mux.HandleFunc("POST /v1/devices/{id}/heartbeat", wrap(func(w http.ResponseWriter, r *http.Request, uid string) {
		var request push.HeartbeatRequest
		if !decode(w, r, &request) {
			return
		}
		if !validID(r.PathValue("id")) || push.ValidateHeartbeat(request) != nil {
			http.Error(w, "invalid heartbeat", 400)
			return
		}
		err := store.HeartbeatPushInstallation(r.Context(), uid, r.PathValue("id"), push.InstallationHeartbeat{Permission: request.Permission, Timezone: request.Timezone, Enabled: request.Enabled, SeenAt: time.Now()})
		if err != nil {
			storeError(w, err)
			return
		}
		receipt(w, r.PathValue("id"))
	}))
	mux.HandleFunc("DELETE /v1/devices/{id}", wrap(func(w http.ResponseWriter, r *http.Request, uid string) {
		if !validID(r.PathValue("id")) {
			http.Error(w, "invalid installation", 400)
			return
		}
		err := store.DisablePushInstallation(r.Context(), uid, r.PathValue("id"), time.Now())
		if err != nil && !errors.Is(err, push.ErrInstallationNotFound) {
			storeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	return mux
}

func validID(id string) bool {
	if len(id) != 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'f' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "invalid JSON", 400)
		return false
	}
	return true
}
func receipt(w http.ResponseWriter, id string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(push.RegistrationResponse{InstallationID: id})
}
func storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, push.ErrInstallationNotFound), errors.Is(err, push.ErrUserNotFound):
		http.Error(w, "not found", 404)
	case errors.Is(err, push.ErrAccountDeletionFenced):
		http.Error(w, "registration blocked", 409)
	default:
		http.Error(w, "storage unavailable", 503)
	}
}
