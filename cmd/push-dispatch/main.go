// push-dispatch is a small GCP host for the library. Credentials are obtained
// through Application Default Credentials; no service-account keys are bundled.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cloud.google.com/go/firestore"
	firebase "firebase.google.com/go/v4"
	"github.com/bjthomas07/push-dispatch/api"
	"github.com/bjthomas07/push-dispatch/push"
	"github.com/bjthomas07/push-dispatch/push/fcm"
	pushstore "github.com/bjthomas07/push-dispatch/push/store"
	"github.com/bjthomas07/push-dispatch/scheduler"
	schedstore "github.com/bjthomas07/push-dispatch/scheduler/firestore"
)

const help = `push-dispatch: self-hosted mobile push and reminders

Usage: push-dispatch <command> [flags]
  serve       Firebase-authenticated device and schedule API
  tick        Process due schedules (Cloud Run Job or cron)
  schedule    Upsert a schedule from JSON on stdin
  cancel      Cancel --user USER --id SCHEDULE
  send        Send JSON {"recipientId":"...","message":{...}} from stdin

All commands: --project PROJECT --app APP (or GOOGLE_CLOUD_PROJECT, PUSH_APP)
serve: --listen :8080 (PORT respected), --client-schedules (opt-in)
tick: --shard 0..31 (default all, partitioned across Cloud Run tasks), --limit 100
Library API and JSON examples: docs/usage.md
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "push-dispatch:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		_, err := io.WriteString(out, help)
		return err
	}
	command := args[0]
	switch command {
	case "serve", "tick", "schedule", "cancel", "send":
	default:
		return fmt.Errorf("unknown command %q; use --help", command)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(out)
	project := flags.String("project", os.Getenv("GOOGLE_CLOUD_PROJECT"), "Firebase/GCP project ID")
	appID := flags.String("app", os.Getenv("PUSH_APP"), "app namespace")
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	clientSchedules := flags.Bool("client-schedules", false, "enable self-service schedules; enforce application quotas at your gateway")
	listen := flags.String("listen", ":"+port, "HTTP bind address")
	shard := flags.Int("shard", -1, "scheduler shard, -1 partitions all shards across Cloud Run tasks")
	limit := flags.Int("limit", 100, "maximum jobs per shard")
	uid := flags.String("user", "", "recipient ID for cancel")
	id := flags.String("id", "", "schedule ID for cancel")
	if err := flags.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	if *project == "" || !pathID(*appID) {
		return fmt.Errorf("--project and a path-safe --app are required")
	}
	if *shard < -1 || *shard >= scheduler.ShardCount || *limit < 1 || *limit > 1000 {
		return fmt.Errorf("invalid shard or limit")
	}
	fs, err := firestore.NewClient(ctx, *project)
	if err != nil {
		return err
	}
	defer fs.Close()
	appRef := fs.Collection("apps").Doc(*appID)
	devices := pushstore.New(fs, pushstore.Config{CreateUsers: true, AppDocument: func(context.Context) *firestore.DocumentRef { return appRef }})
	schedules := schedstore.New(fs, appRef)
	encode := func(v any) error { return json.NewEncoder(out).Encode(v) }
	switch command {
	case "schedule":
		var input scheduler.Schedule
		if err = readJSON(in, &input); err != nil {
			return err
		}
		job, err := scheduler.NewJob(input, time.Now())
		if err != nil {
			return err
		}
		if err = schedules.Put(ctx, job); err != nil {
			return err
		}
		return encode(map[string]any{"id": job.ID, "dueAt": job.DueAt, "shard": job.Shard})
	case "cancel":
		if !pathID(*uid) || !pathID(*id) {
			return fmt.Errorf("--user and --id are required")
		}
		return schedules.Cancel(ctx, *uid, *id, time.Now())
	case "serve":
		app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: *project})
		if err != nil {
			return err
		}
		auth, err := app.Auth(ctx)
		if err != nil {
			return err
		}
		authenticate := func(r *http.Request) (string, error) {
			header := r.Header.Get("Authorization")
			if !strings.HasPrefix(header, "Bearer ") {
				return "", fmt.Errorf("missing bearer token")
			}
			token, err := auth.VerifyIDTokenAndCheckRevoked(r.Context(), strings.TrimPrefix(header, "Bearer "))
			if err != nil {
				return "", err
			}
			return token.UID, nil
		}
		mux := http.NewServeMux()
		mux.Handle("/v1/devices", api.Devices(devices, authenticate))
		mux.Handle("/v1/devices/", api.Devices(devices, authenticate))
		if *clientSchedules {
			mux.Handle("/v1/schedules/", api.Schedules(schedules, authenticate))
		}
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
		server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 << 10}
		done := make(chan error, 1)
		go func() { done <- server.ListenAndServe() }()
		select {
		case err := <-done:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return server.Shutdown(shutdown)
		}
	}
	sender, err := fcm.New(ctx, *project)
	if err != nil {
		return err
	}
	dispatcher, err := push.NewDispatcher(sender, 1)
	if err != nil {
		return err
	}
	if command == "send" {
		var input struct {
			RecipientID string       `json:"recipientId"`
			Message     push.Message `json:"message"`
		}
		if err = readJSON(in, &input); err != nil {
			return err
		}
		if !pathID(input.RecipientID) {
			return fmt.Errorf("recipientId is required")
		}
		if err = push.ValidateMessage(input.Message); err != nil {
			return err
		}
		targets, err := devices.ActiveTargets(ctx, input.RecipientID, time.Now())
		if err != nil {
			return err
		}
		sendCtx, cancel := context.WithTimeout(ctx, 4*time.Minute)
		defer cancel()
		result, err := dispatcher.Send(sendCtx, input.Message, targets)
		cleanupErr := devices.DisablePushInstallations(ctx, push.UnregisteredInstallationRefs(result), time.Now())
		// Never print targets, tokens, or provider error strings.
		if encodeErr := encode(map[string]int{"accepted": result.SuccessCount, "failed": result.FailureCount}); encodeErr != nil {
			return encodeErr
		}
		if err != nil || cleanupErr != nil || result.FailureCount > 0 {
			return fmt.Errorf("delivery incomplete; inspect counts")
		}
		return nil
	}
	worker := scheduler.Worker{Store: schedules, Targets: devices, Sender: dispatcher}
	first, last, stride, err := shardRange(*shard, os.Getenv("CLOUD_RUN_TASK_INDEX"), os.Getenv("CLOUD_RUN_TASK_COUNT"))
	if err != nil {
		return err
	}
	for n := first; n <= last; n += stride {
		report, err := worker.Tick(ctx, n, *limit)
		if e := encode(map[string]any{"shard": n, "report": report}); e != nil {
			return e
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func pathID(v string) bool {
	return v != "" && len(v) <= 128 && v != "." && v != ".." && strings.TrimSpace(v) == v && !strings.ContainsAny(v, "/\x00")
}
func readJSON(r io.Reader, v any) error {
	data, err := io.ReadAll(io.LimitReader(r, (64<<10)+1))
	if err != nil {
		return err
	}
	if len(data) > 64<<10 {
		return fmt.Errorf("JSON exceeds 64 KiB")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if d.Decode(new(any)) != io.EOF {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}

// Every shard is assigned once for 1..32 Cloud Run tasks. Explicit --shard is
// useful with external schedulers that partition work themselves.
func shardRange(explicit int, index, count string) (int, int, int, error) {
	if explicit >= 0 {
		return explicit, explicit, 1, nil
	}
	if index == "" && count == "" {
		return 0, scheduler.ShardCount - 1, 1, nil
	}
	i, e1 := strconv.Atoi(index)
	n, e2 := strconv.Atoi(count)
	if e1 != nil || e2 != nil || n < 1 || n > scheduler.ShardCount || i < 0 || i >= n {
		return 0, 0, 0, fmt.Errorf("invalid Cloud Run task partition")
	}
	return i, scheduler.ShardCount - 1, n, nil
}
