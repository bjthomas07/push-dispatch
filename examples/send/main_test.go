package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/bjthomas07/push-dispatch/push"
	pushstore "github.com/bjthomas07/push-dispatch/push/store"
)

type batchSenderFunc func(context.Context, push.Message, []push.Target) (push.BatchResult, error)

func (f batchSenderFunc) SendBatch(ctx context.Context, message push.Message, targets []push.Target) (push.BatchResult, error) {
	return f(ctx, message, targets)
}

func exampleStore(t *testing.T) (context.Context, *pushstore.Store) {
	t.Helper()
	if os.Getenv("FIRESTORE_EMULATOR_HOST") == "" {
		t.Skip("requires mise run test-emulator")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	client, err := firestore.NewClient(ctx, "demo-push-dispatch")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	app := client.Collection("apps").Doc(fmt.Sprintf("example-%d", time.Now().UnixNano()))
	return ctx, pushstore.New(client, pushstore.Config{
		CreateUsers: true,
		AppDocument: func(context.Context) *firestore.DocumentRef { return app },
	})
}

func TestExampleDoesNotSendWithoutEligibleDevices(t *testing.T) {
	ctx, store := exampleStore(t)
	calls := 0
	sender := batchSenderFunc(func(context.Context, push.Message, []push.Target) (push.BatchResult, error) {
		calls++
		return push.BatchResult{}, nil
	})
	var out bytes.Buffer
	err := sendToUser(ctx, store, sender, "test-user", &out)
	if err == nil || !strings.Contains(err.Error(), "no eligible installations") || calls != 0 || out.Len() != 0 {
		t.Fatalf("missing device: err=%v calls=%d output=%q", err, calls, out.String())
	}
}

func TestExampleRetiresOnlyUnregisteredTargetsAndRedactsErrors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		cancelSend bool
	}{
		{name: "per-target failures"},
		{name: "canceled partial response", cancelSend: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testPartialFailure(t, tc.cancelSend)
		})
	}
}

func testPartialFailure(t *testing.T, cancelSend bool) {
	t.Helper()
	ctx, store := exampleStore(t)
	sendCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	const uid = "test-user"
	for _, target := range []string{"fixture-success", "fixture-unregistered", "fixture-permanent"} {
		id, installation, err := push.NewInstallation(push.RegistrationRequest{
			Provider: push.ProviderFCM, TargetType: push.TargetToken, Target: target,
			Platform: push.PlatformAndroid, Permission: push.PermissionGranted,
		}, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err = store.UpsertPushInstallation(ctx, uid, id, installation); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	sender := batchSenderFunc(func(_ context.Context, message push.Message, targets []push.Target) (push.BatchResult, error) {
		calls++
		if err := push.ValidateMessage(message); err != nil {
			return push.BatchResult{}, err
		}
		var result push.BatchResult
		for _, target := range targets {
			r := push.TargetResult{Target: target}
			switch target.Address {
			case "fixture-unregistered":
				r.Code = push.ErrorUnregistered
			case "fixture-permanent":
				if cancelSend {
					// No response for this target: acceptance is unknown.
					continue
				}
				r.Code = push.ErrorPermanent
			}
			if r.Code != push.ErrorNone {
				r.Err = errors.New("provider error containing " + target.Address)
				result.FailureCount++
			} else {
				result.SuccessCount++
			}
			result.Results = append(result.Results, r)
		}
		if cancelSend {
			cancel()
			return result, fmt.Errorf("provider error containing fixture-private: %w", context.Canceled)
		}
		return result, nil
	})
	var out bytes.Buffer
	err := sendToUser(sendCtx, store, sender, uid, &out)
	if err == nil || !strings.Contains(err.Error(), "delivery incomplete") {
		t.Fatalf("partial failure was not reported: %v", err)
	}
	if out.String() != "accepted=1 failed=2\n" || strings.Contains(err.Error(), "fixture-") {
		t.Fatalf("unexpected or unsanitized output: %q, %v", out.String(), err)
	}
	if calls != 1 {
		t.Fatalf("unexpected resend: provider called %d times", calls)
	}
	remaining, err := store.ActiveTargets(ctx, uid, time.Now())
	if err != nil || len(remaining) != 2 {
		t.Fatalf("remaining targets=%d err=%v", len(remaining), err)
	}
	for _, target := range remaining {
		if target.Address == "fixture-unregistered" {
			t.Fatal("unregistered target remains eligible")
		}
	}
}
