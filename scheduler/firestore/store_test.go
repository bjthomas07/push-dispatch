package firestore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/bjthomas07/push-dispatch/push"
	pushstore "github.com/bjthomas07/push-dispatch/push/store"
	"github.com/bjthomas07/push-dispatch/scheduler"
)

func emulator(t *testing.T) (context.Context, *firestore.Client, *firestore.DocumentRef) {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires Firestore emulator; see mise run test-emulator")
	}
	ctx := context.Background()
	fs, err := firestore.NewClient(ctx, "demo-push-dispatch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fs.Close() })
	return ctx, fs, fs.Collection("apps").Doc(fmt.Sprintf("test-%d", time.Now().UnixNano()))
}

func TestEmulatorConcurrentClaimsLeaseExpiryAndFencing(t *testing.T) {
	ctx, fs, app := emulator(t)
	s := New(fs, app)
	now := time.Now().UTC().Truncate(time.Second)
	job, err := scheduler.NewJob(scheduler.Schedule{ID: "test", RecipientID: "user", At: now, Message: push.Message{Title: "hello"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Put(ctx, job); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	claims := make(chan scheduler.Job, 8)
	errs := make(chan error, 8)
	for n := 0; n < 8; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			jobs, err := s.ClaimDue(ctx, now, job.Shard, 1, time.Minute)
			if err != nil {
				errs <- err
			}
			for _, j := range jobs {
				claims <- j
			}
		}()
	}
	wg.Wait()
	close(claims)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	if len(claims) != 1 {
		t.Fatalf("expected one owner, got %d", len(claims))
	}
	first := <-claims
	if got, err := s.ClaimDue(ctx, now.Add(59*time.Second), job.Shard, 1, time.Minute); err != nil || len(got) != 0 {
		t.Fatalf("early reclaim %+v %v", got, err)
	}
	second, err := s.ClaimDue(ctx, now.Add(time.Minute), job.Shard, 1, time.Minute)
	if err != nil || len(second) != 1 {
		t.Fatalf("expired lease %+v %v", second, err)
	}
	if err = s.Finish(ctx, first, job); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("stale worker finished: %v", err)
	}
	if err = s.Cancel(ctx, job.RecipientID, job.ID, now); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(ctx, second[0], job); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("canceled job resurrected: %v", err)
	}
	if err = s.Put(ctx, job); err != nil {
		t.Fatal(err)
	}
	if err = s.Finish(ctx, second[0], job); !errors.Is(err, ErrLeaseLost) {
		t.Fatalf("replacement overwritten: %v", err)
	}
}

func TestEmulatorInstallationOwnershipAndAccountSwitch(t *testing.T) {
	ctx, fs, app := emulator(t)
	s := pushstore.New(fs, pushstore.Config{CreateUsers: true, AppDocument: func(context.Context) *firestore.DocumentRef { return app }})
	now := time.Now()
	id, i, err := push.NewInstallation(push.RegistrationRequest{Provider: push.ProviderFCM, TargetType: push.TargetToken, Target: "test-token", Platform: push.PlatformAndroid, Permission: push.PermissionGranted}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertPushInstallation(ctx, "alice", id, i); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertPushInstallation(ctx, "bob", id, i); err != nil {
		t.Fatal(err)
	}
	alice, err := s.ActiveTargets(ctx, "alice", now)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := s.ActiveTargets(ctx, "bob", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(alice) != 0 || len(bob) != 1 {
		t.Fatalf("duplicate ownership: alice=%d bob=%d", len(alice), len(bob))
	}
	if err = s.HeartbeatPushInstallation(ctx, "alice", id, push.InstallationHeartbeat{SeenAt: now}); !errors.Is(err, push.ErrInstallationNotFound) {
		t.Fatalf("old account revived token: %v", err)
	}
	// A delayed unbind from the old account must leave the new owner active.
	if err = s.DisablePushInstallation(ctx, "alice", id, now); err != nil {
		t.Fatal(err)
	}
	bob, err = s.ActiveTargets(ctx, "bob", now)
	if err != nil || len(bob) != 1 {
		t.Fatalf("old owner disabled new owner: %v", err)
	}
	if err = s.DisablePushInstallations(ctx, []push.InstallationRef{{UID: "bob", InstallationID: id}}, now); err != nil {
		t.Fatal(err)
	}
	bob, err = s.ActiveTargets(ctx, "bob", now)
	if err != nil || len(bob) != 0 {
		t.Fatalf("unregistered cleanup: %v", err)
	}
}

func TestEmulatorDeliveryMarkersSurviveRegistration(t *testing.T) {
	ctx, fs, app := emulator(t)
	s := pushstore.New(fs, pushstore.Config{CreateUsers: true, AppDocument: func(context.Context) *firestore.DocumentRef { return app }})
	now := time.Now()
	id, i, err := push.NewInstallation(push.RegistrationRequest{Provider: push.ProviderFCM, TargetType: push.TargetFID, Target: "fid", Platform: push.PlatformIOS, Permission: push.PermissionGranted}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertPushInstallation(ctx, "user", id, i); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkPushInstallationsDelivered(ctx, []push.InstallationRef{{UID: "user", InstallationID: id}}, "morning", "2026-09-07"); err != nil {
		t.Fatal(err)
	}
	if err = s.UpsertPushInstallation(ctx, "user", id, i); err != nil {
		t.Fatal(err)
	}
	doc, err := app.Collection("users").Doc("user").Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Installations map[string]push.Installation `firestore:"pushInstallations"`
	}
	if err = doc.DataTo(&stored); err != nil {
		t.Fatal(err)
	}
	if !stored.Installations[id].DeliveredFor("morning", "2026-09-07") {
		t.Fatal("registration erased delivery marker")
	}
}

func TestEmulatorPruningReplacesMapAndKeepsOtherUserFields(t *testing.T) {
	ctx, fs, app := emulator(t)
	s := pushstore.New(fs, pushstore.Config{CreateUsers: true, AppDocument: func(context.Context) *firestore.DocumentRef { return app }})
	user := app.Collection("users").Doc("user")
	if _, err := user.Set(ctx, map[string]any{"profile": "preserved"}); err != nil {
		t.Fatal(err)
	}
	var firstID string
	for n := 0; n < push.MaxInstallationsPerUser+1; n++ {
		now := time.Now().Add(time.Duration(n) * time.Second)
		id, i, err := push.NewInstallation(push.RegistrationRequest{Provider: push.ProviderFCM, TargetType: push.TargetToken, Target: fmt.Sprintf("target-%d", n), Platform: push.PlatformAndroid}, now)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			firstID = id
		}
		if err = s.UpsertPushInstallation(ctx, "user", id, i); err != nil {
			t.Fatal(err)
		}
	}
	doc, err := user.Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var stored struct {
		Installations map[string]push.Installation `firestore:"pushInstallations"`
		Profile       string                       `firestore:"profile"`
	}
	if err = doc.DataTo(&stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.Installations) != push.MaxInstallationsPerUser || stored.Profile != "preserved" {
		t.Fatalf("incorrect merge: %+v", stored)
	}
	if _, ok := stored.Installations[firstID]; ok {
		t.Fatal("pruned map member survived merge")
	}
}
