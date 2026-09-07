package scheduler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/bjthomas07/push-dispatch/push"
)

type fakeTargets struct {
	targets    []push.Target
	err        error
	cleanupErr error
	disabled   []push.InstallationRef
}

func (f *fakeTargets) ActiveTargets(context.Context, string, time.Time) ([]push.Target, error) {
	return f.targets, f.err
}
func (f *fakeTargets) DisablePushInstallations(_ context.Context, refs []push.InstallationRef, _ time.Time) error {
	f.disabled = append(f.disabled, refs...)
	return f.cleanupErr
}

type senderFunc func(context.Context, push.Message, []push.Target) (push.BatchResult, error)

func (f senderFunc) Send(c context.Context, m push.Message, t []push.Target) (push.BatchResult, error) {
	return f(c, m, t)
}

type fakeStore struct {
	job         Job
	ackCanceled bool
}

func (s *fakeStore) Get(context.Context, string, string) (Job, error) { return s.job, nil }

func (s *fakeStore) Put(_ context.Context, j Job) error                      { s.job = j; return nil }
func (s *fakeStore) Cancel(context.Context, string, string, time.Time) error { return nil }
func (s *fakeStore) ClaimDue(_ context.Context, now time.Time, _ int, _ int, lease time.Duration) ([]Job, error) {
	if s.job.State != Ready || s.job.AvailableAt.After(now) {
		return nil, nil
	}
	s.job.LeaseToken = "lease"
	s.job.AvailableAt = now.Add(lease)
	return []Job{s.job}, nil
}
func (s *fakeStore) Finish(ctx context.Context, _ Job, j Job) error {
	s.ackCanceled = ctx.Err() != nil
	s.job = j
	return nil
}

func TestWorkerPersistsPartialSuccessAndRetriesOnlyFailedTargets(t *testing.T) {
	now := instant("2026-09-07T12:00:00Z")
	a := push.Target{ID: "a", RecipientID: "user", Type: push.TargetToken, Address: "a"}
	b := push.Target{ID: "b", RecipientID: "user", Type: push.TargetToken, Address: "b"}
	store := &fakeStore{job: Job{ID: "reminder", RecipientID: "user", State: Ready, DueAt: now, AvailableAt: now, Message: push.Message{Title: "test"}}}
	targets := &fakeTargets{targets: []push.Target{a, b}}
	var notificationID string
	calls := 0
	w := Worker{Store: store, Targets: targets, Now: func() time.Time { return now }, Sender: senderFunc(func(_ context.Context, m push.Message, ts []push.Target) (push.BatchResult, error) {
		calls++
		if calls == 1 {
			notificationID = m.Analytics.NotificationID
			return push.BatchResult{Results: []push.TargetResult{{Target: a}, {Target: b, Code: push.ErrorTransient, Err: errors.New("retry")}}}, errors.New("partial")
		}
		if len(ts) != 1 || ts[0] != b {
			t.Fatalf("resent successful target: %+v", ts)
		}
		if m.Analytics.NotificationID != notificationID {
			t.Fatal("retry changed occurrence identity")
		}
		return push.BatchResult{Results: []push.TargetResult{{Target: b}}}, nil
	})}
	first, err := w.Tick(context.Background(), 0, 1)
	if err != nil || first.Retrying != 1 || len(store.job.Completed) != 1 {
		t.Fatalf("first %+v job %+v err %v", first, store.job, err)
	}
	now = now.Add(time.Minute)
	second, err := w.Tick(context.Background(), 0, 1)
	if err != nil || second.Completed != 1 || store.job.State != Done || store.job.LastError != "" {
		t.Fatalf("second %+v job %+v err %v", second, store.job, err)
	}
}

func TestWorkerUnregisteredCleanupAndPermanentFailures(t *testing.T) {
	for _, code := range []push.ErrorCode{push.ErrorUnregistered, push.ErrorPermanent} {
		t.Run(string(code), func(t *testing.T) {
			now := time.Now()
			target := push.Target{ID: "a", RecipientID: "user", Type: push.TargetToken, Address: "a"}
			targets := &fakeTargets{targets: []push.Target{target}}
			w := Worker{Targets: targets, Now: func() time.Time { return now }, Sender: senderFunc(func(context.Context, push.Message, []push.Target) (push.BatchResult, error) {
				return push.BatchResult{Results: []push.TargetResult{{Target: target, Code: code, Err: errors.New("failed")}}}, nil
			})}
			got := w.deliver(context.Background(), Job{ID: "r", RecipientID: "user", State: Ready, DueAt: now, Message: push.Message{Title: "test"}})
			if code == push.ErrorUnregistered && (len(targets.disabled) != 1 || got.State != Done) {
				t.Fatalf("unregistered %+v", got)
			}
			if code == push.ErrorPermanent && (len(targets.disabled) != 0 || got.State != Failed) {
				t.Fatalf("permanent %+v", got)
			}
		})
	}
}

func TestWorkerCapsRetriesAndRejectsShortLease(t *testing.T) {
	now := time.Now()
	w := Worker{Targets: &fakeTargets{err: errors.New("offline")}, Now: func() time.Time { return now }}
	j := w.deliver(context.Background(), Job{State: Ready, Attempts: MaxAttempts - 1})
	if j.State != Failed {
		t.Fatalf("unbounded retries %+v", j)
	}
	w.Store = &fakeStore{}
	w.Sender = senderFunc(func(context.Context, push.Message, []push.Target) (push.BatchResult, error) { panic("must not send") })
	w.Lease = time.Second
	if _, err := w.Tick(context.Background(), 0, 1); err == nil {
		t.Fatal("accepted short lease")
	}
}

func TestWorkerAcknowledgesAfterShutdown(t *testing.T) {
	now := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &fakeStore{job: Job{ID: "r", RecipientID: "u", State: Ready, DueAt: now, AvailableAt: now}}
	w := Worker{Store: store, Targets: &fakeTargets{}, Now: func() time.Time { return now }, Sender: senderFunc(func(context.Context, push.Message, []push.Target) (push.BatchResult, error) {
		cancel()
		return push.BatchResult{}, nil
	})}
	if _, err := w.Tick(ctx, 0, 1); err != nil {
		t.Fatal(err)
	}
	if store.ackCanceled || store.job.State != Done {
		t.Fatal("completion lost on shutdown")
	}
}

func TestWorkerRetriesInvalidTargetCleanupWithoutResending(t *testing.T) {
	now := time.Now()
	target := push.Target{ID: "a", RecipientID: "user", Type: push.TargetToken, Address: "a"}
	targets := &fakeTargets{targets: []push.Target{target}, cleanupErr: errors.New("storage offline")}
	calls := 0
	w := Worker{Targets: targets, Now: func() time.Time { return now }, Sender: senderFunc(func(_ context.Context, _ push.Message, ts []push.Target) (push.BatchResult, error) {
		calls++
		if calls == 1 {
			return push.BatchResult{Results: []push.TargetResult{{Target: target, Code: push.ErrorUnregistered, Err: errors.New("gone")}}}, nil
		}
		if len(ts) != 0 {
			t.Fatal("resent known invalid target")
		}
		return push.BatchResult{}, nil
	})}
	first := w.deliver(context.Background(), Job{ID: "r", RecipientID: "user", State: Ready, DueAt: now})
	if len(first.InvalidTargets) != 1 || first.State != Ready {
		t.Fatalf("cleanup was lost: %+v", first)
	}
	targets.cleanupErr = nil
	second := w.deliver(context.Background(), first)
	if len(second.InvalidTargets) != 0 || len(targets.disabled) != 2 || second.State != Done {
		t.Fatalf("cleanup not retried: %+v", second)
	}
}
