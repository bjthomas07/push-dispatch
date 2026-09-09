package firestore

import (
	"context"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/bjthomas07/push-dispatch/push"
	pushstore "github.com/bjthomas07/push-dispatch/push/store"
	"github.com/bjthomas07/push-dispatch/scheduler"
)

func TestEmulatorCustomPathsPreserveApplicationData(t *testing.T) {
	ctx, fs, app := emulator(t)
	users := fs.Collection("custom-users-" + app.ID)
	owners := fs.Collection("custom-owners-" + app.ID)
	userDocument := func(_ context.Context, uid string) *firestore.DocumentRef {
		return users.Doc(uid).Collection("notifications").Doc("state")
	}
	devices := pushstore.New(fs, pushstore.Config{
		CreateUsers: true, UserDocument: userDocument, OwnersCollection: owners,
	})
	if _, err := userDocument(ctx, "first").Set(ctx, map[string]any{"preference": "keep"}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	id, installation, err := push.NewInstallation(push.RegistrationRequest{
		Provider: push.ProviderFCM, TargetType: push.TargetToken, Target: "fixture-token",
		Platform: push.PlatformAndroid, Permission: push.PermissionGranted,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{"first", "second"} {
		if err := devices.UpsertPushInstallation(ctx, uid, id, installation); err != nil {
			t.Fatal(err)
		}
	}
	if targets, err := devices.ActiveTargets(ctx, "first", now); err != nil || len(targets) != 0 {
		t.Fatalf("prior owner remains active: %d targets, %v", len(targets), err)
	}
	if err := devices.HeartbeatPushInstallation(ctx, "second", id, push.InstallationHeartbeat{SeenAt: now}); err != nil {
		t.Fatal(err)
	}
	if target, err := devices.NewestActiveTarget(ctx, "second", now); err != nil || target.ID != id {
		t.Fatalf("custom target lookup: %v", err)
	}
	if err := devices.DisablePushInstallations(ctx, []push.InstallationRef{{UID: "second", InstallationID: id}}, now); err != nil {
		t.Fatal(err)
	}
	if targets, err := devices.ActiveTargets(ctx, "second", now); err != nil || len(targets) != 0 {
		t.Fatalf("disabled target remains active: %d targets, %v", len(targets), err)
	}
	if err := devices.DeleteOwnersForUser(ctx, "second"); err != nil {
		t.Fatal(err)
	}
	remaining, err := owners.Documents(ctx).GetAll()
	if err != nil || len(remaining) != 0 {
		t.Fatalf("owner cleanup: %d records, %v", len(remaining), err)
	}
	snapshot, err := userDocument(ctx, "first").Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if value, err := snapshot.DataAt("preference"); err != nil || value != "keep" {
		t.Fatal("push updates replaced application preferences")
	}
	parents, err := users.Documents(ctx).GetAll()
	if err != nil || len(parents) != 0 {
		t.Fatal("push state unexpectedly created a parent account document")
	}
}

func TestEmulatorCustomScheduleCollection(t *testing.T) {
	ctx, fs, app := emulator(t)
	s := NewWithCollection(fs, fs.Collection("custom-schedules-"+app.ID))
	now := time.Now()
	job, err := scheduler.NewJob(scheduler.Schedule{
		ID: "reminder", RecipientID: "user", At: now, Message: push.Message{Title: "Hello"},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, job); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimDue(ctx, now, job.Shard, 1, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("custom collection claim: %d jobs, %v", len(claimed), err)
	}
	finished := claimed[0]
	finished.State = scheduler.Done
	finished.LeaseToken = ""
	if err := s.Finish(ctx, claimed[0], finished); err != nil {
		t.Fatal(err)
	}
	stored, err := s.Get(ctx, job.RecipientID, job.ID)
	if err != nil || stored.State != scheduler.Done {
		t.Fatalf("custom collection finish: %v", err)
	}
}
