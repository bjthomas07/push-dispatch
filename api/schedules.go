package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/bjthomas07/push-dispatch/push"
	"github.com/bjthomas07/push-dispatch/scheduler"
)

// Schedules lets authenticated users schedule messages only for their own
// installations. Mount behind your application's rate and schedule-count limits.
// Server code may instead use Store directly for trusted campaigns.
func Schedules(store scheduler.Store, authenticate Authenticate) http.Handler {
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
	mux.HandleFunc("GET /v1/schedules/{id}", wrap(func(w http.ResponseWriter, r *http.Request, uid string) {
		job, err := store.Get(r.Context(), uid, r.PathValue("id"))
		if errors.Is(err, scheduler.ErrNotFound) {
			http.Error(w, "not found", 404)
			return
		}
		if err != nil {
			http.Error(w, "storage unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": job.ID, "daily": job.Daily, "dueAt": job.DueAt, "state": job.State, "message": job.Message})
	}))
	mux.HandleFunc("PUT /v1/schedules/{id}", wrap(func(w http.ResponseWriter, r *http.Request, uid string) {
		var input struct {
			At      time.Time        `json:"at"`
			Daily   *scheduler.Daily `json:"daily"`
			Message push.Message     `json:"message"`
		}
		if !decode(w, r, &input) {
			return
		}
		job, err := scheduler.NewJob(scheduler.Schedule{ID: r.PathValue("id"), RecipientID: uid, At: input.At, Daily: input.Daily, Message: input.Message}, time.Now())
		if err != nil {
			http.Error(w, "invalid schedule", 400)
			return
		}
		if err = store.Put(r.Context(), job); err != nil {
			http.Error(w, "storage unavailable", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": job.ID, "dueAt": job.DueAt})
	}))
	mux.HandleFunc("DELETE /v1/schedules/{id}", wrap(func(w http.ResponseWriter, r *http.Request, uid string) {
		if err := store.Cancel(r.Context(), uid, r.PathValue("id"), time.Now()); err != nil {
			http.Error(w, "storage unavailable", 503)
			return
		}
		w.WriteHeader(204)
	}))
	return mux
}
