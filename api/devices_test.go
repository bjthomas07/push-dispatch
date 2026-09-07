package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bjthomas07/push-dispatch/push"
)

type devicesFake struct {
	uid   string
	err   error
	calls int
}

func (s *devicesFake) UpsertPushInstallation(_ context.Context, uid, _ string, _ push.Installation) error {
	s.uid = uid
	s.calls++
	return s.err
}
func (s *devicesFake) HeartbeatPushInstallation(_ context.Context, uid, _ string, _ push.InstallationHeartbeat) error {
	s.uid = uid
	s.calls++
	return s.err
}
func (s *devicesFake) DisablePushInstallation(_ context.Context, uid, _ string, _ time.Time) error {
	s.uid = uid
	s.calls++
	return s.err
}

func TestDevicesUsesAuthenticatedOwnerAndStrictJSON(t *testing.T) {
	valid := `{"provider":"fcm","targetType":"token","target":"abc","platform":"android","permission":"granted","timezone":"UTC","enabled":true}`
	for _, tt := range []struct {
		name, body string
		authErr    bool
		status     int
	}{
		{"register", valid, false, 200},
		{"missing auth", valid, true, 401},
		{"spoofed owner", strings.TrimSuffix(valid, "}") + `,"uid":"victim"}`, false, 400},
		{"second object", valid + ` {}`, false, 400},
		{"invalid permission", strings.ReplaceAll(valid, "granted", "maybe"), false, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := &devicesFake{}
			h := Devices(s, func(*http.Request) (string, error) {
				if tt.authErr {
					return "", errors.New("invalid")
				}
				return "alice", nil
			})
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("POST", "/v1/devices", strings.NewReader(tt.body)))
			if w.Code != tt.status {
				t.Fatalf("got %d: %s", w.Code, w.Body.String())
			}
			if tt.status == 200 && s.uid != "alice" {
				t.Fatal("wrong owner")
			}
			if tt.status != 200 && s.calls != 0 {
				t.Fatal("invalid request reached store")
			}
		})
	}
}

func TestMissingHeartbeatTriggersNativeRecoveryAndUnbindIsIdempotent(t *testing.T) {
	s := &devicesFake{err: push.ErrInstallationNotFound}
	h := Devices(s, func(*http.Request) (string, error) { return "alice", nil })
	id := strings.Repeat("a", 64)
	for _, tt := range []struct {
		method, path string
		status       int
	}{{"POST", "/v1/devices/" + id + "/heartbeat", 404}, {"DELETE", "/v1/devices/" + id, 204}} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(tt.method, tt.path, strings.NewReader(`{}`)))
		if w.Code != tt.status {
			t.Fatalf("got %d", w.Code)
		}
	}
}
