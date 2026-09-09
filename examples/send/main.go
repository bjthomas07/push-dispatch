// This example sends one real notification to a registered user's active devices.
// Run with mise run example-send after completing docs/quickstart.md.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/bjthomas07/push-dispatch/push"
	"github.com/bjthomas07/push-dispatch/push/fcm"
	pushstore "github.com/bjthomas07/push-dispatch/push/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	if err := run(ctx, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "send example:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, out io.Writer) error {
	project, app, uid := os.Getenv("GOOGLE_CLOUD_PROJECT"), os.Getenv("PUSH_APP"), os.Getenv("PUSH_USER_ID")
	if project == "" || !pathID(app) || !pathID(uid) {
		return errors.New("set GOOGLE_CLOUD_PROJECT, PUSH_APP, and PUSH_USER_ID; app and user must be path-safe IDs")
	}
	client, err := firestore.NewClient(ctx, project)
	if err != nil {
		return errors.New("initialize Firestore: check Application Default Credentials")
	}
	defer client.Close()
	appRef := client.Collection("apps").Doc(app)
	store := pushstore.New(client, pushstore.Config{
		AppDocument: func(context.Context) *firestore.DocumentRef { return appRef },
	})
	sender, err := fcm.New(ctx, project)
	if err != nil {
		return errors.New("initialize FCM: check Application Default Credentials and project")
	}
	return sendToUser(ctx, store, sender, uid, out)
}

func sendToUser(ctx context.Context, store *pushstore.Store, sender push.BatchSender, uid string, out io.Writer) error {
	targets, err := store.ActiveTargets(ctx, uid, time.Now())
	if err != nil {
		return errors.New("read installations: check project, app namespace, and Firestore IAM")
	}
	if len(targets) == 0 {
		return errors.New("no eligible installations: register a device with permission granted first")
	}
	dispatcher, err := push.NewDispatcher(sender, push.DefaultBatchConcurrency)
	if err != nil {
		return err
	}
	result, sendErr := dispatcher.Send(ctx, push.Message{
		Title: "Hello from push-dispatch",
		Body:  "Your first notification is ready.",
		TTL:   time.Hour,
		Apple: push.AppleConfig{Sound: "default"},
		Android: push.AndroidConfig{
			ChannelID: "reminders", Sound: "default",
		},
	}, targets)

	// Some targets may succeed even when Send returns an error. Only an explicit
	// unregistered result retires a target; payload/configuration errors do not.
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	cleanupErr := store.DisablePushInstallations(cleanupCtx, push.UnregisteredInstallationRefs(result), time.Now())
	if _, err := fmt.Fprintf(out, "accepted=%d failed=%d\n", result.SuccessCount, result.FailureCount); err != nil {
		return err
	}
	// Provider errors can contain addresses. Report counts without printing
	// raw errors, message IDs, or target values.
	if cleanupErr != nil {
		return errors.New("installation cleanup failed; inspect Firestore IAM; accepted targets may already have received this message")
	}
	if sendErr != nil || result.FailureCount > 0 {
		return errors.New("delivery incomplete; see docs/troubleshooting.md; accepted targets may already have received this message")
	}
	return nil
}

func pathID(value string) bool {
	return value != "" && len(value) <= 128 && strings.TrimSpace(value) == value &&
		value != "." && value != ".." && !strings.ContainsAny(value, "/\x00")
}
