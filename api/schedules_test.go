package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bjthomas07/push-dispatch/scheduler"
)

type schedulesFake struct{ jobs map[string]scheduler.Job }

func (s *schedulesFake) Put(_ context.Context, j scheduler.Job) error {
	s.jobs[scheduler.Key(j.RecipientID, j.ID)] = j
	return nil
}
func (s *schedulesFake) Get(_ context.Context, uid, id string) (scheduler.Job, error) {
	j, ok := s.jobs[scheduler.Key(uid, id)]
	if !ok {
		return j, scheduler.ErrNotFound
	}
	return j, nil
}
func (s *schedulesFake) Cancel(_ context.Context, uid, id string, _ time.Time) error {
	delete(s.jobs, scheduler.Key(uid, id))
	return nil
}
func (s *schedulesFake) ClaimDue(context.Context, time.Time, int, int, time.Duration) ([]scheduler.Job, error) {
	panic("not an HTTP operation")
}
func (s *schedulesFake) Finish(context.Context, scheduler.Job, scheduler.Job) error {
	panic("not an HTTP operation")
}

func TestScheduleAPIIsOwnerScopedAndRoundTripsPreferences(t *testing.T) {
	store := &schedulesFake{jobs: make(map[string]scheduler.Job)}
	user := "alice"
	h := Schedules(store, func(*http.Request) (string, error) { return user, nil })
	request := func(method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(method, "/v1/schedules/morning", strings.NewReader(body)))
		return w
	}
	body := `{"daily":{"localTime":830,"timezone":"America/New_York"},"message":{"title":"Hi","body":"Reminder"}}`
	if w := request("PUT", body); w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	if w := request("GET", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"localTime":830`) {
		t.Fatal(w.Code, w.Body.String())
	}
	user = "bob"
	if w := request("GET", ""); w.Code != 404 {
		t.Fatal("another account read the schedule")
	}
	if w := request("DELETE", ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	user = "alice"
	if w := request("GET", ""); w.Code != 200 {
		t.Fatal("another account canceled the schedule")
	}
	for _, bad := range []string{
		strings.TrimSuffix(body, "}") + `,"recipientId":"bob"}`,
		strings.ReplaceAll(body, "830", "2360"),
		strings.ReplaceAll(body, "America/New_York", "invalid"),
		strings.ReplaceAll(body, `"title":"Hi"`, `"title":"Hi","data":{"notification_id":"spoofed"}`),
	} {
		if w := request("PUT", bad); w.Code != 400 {
			t.Fatal("accepted invalid schedule", w.Code, w.Body.String())
		}
	}
	if w := request("DELETE", ""); w.Code != 204 {
		t.Fatal(w.Code)
	}
	if w := request("GET", ""); w.Code != 404 {
		t.Fatal("deleted schedule still accessible")
	}
}
