package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bjthomas07/push-dispatch/push"
)

type Worker struct {
	Store   Store
	Targets Targets
	Sender  Sender
	// Each claim is processed sequentially. Tick claims one job at a time so
	// queued work cannot outlive its lease while waiting behind provider retries.
	Lease       time.Duration
	SendTimeout time.Duration
	Now         func() time.Time
}

type Report struct{ Claimed, Completed, Retrying, Failed int }

// Tick drains at most limit jobs on a shard. Several workers may safely overlap.
// Limits bound work per invocation; call again while a shard has a backlog.
func (w Worker) Tick(ctx context.Context, shard, limit int) (Report, error) {
	var report Report
	if w.Store == nil || w.Targets == nil || w.Sender == nil {
		return report, fmt.Errorf("store, targets, and sender are required")
	}
	if shard < 0 || shard >= ShardCount || limit < 1 || limit > 1000 {
		return report, fmt.Errorf("invalid shard or limit")
	}
	if w.Now == nil {
		w.Now = time.Now
	}
	if w.Lease == 0 {
		w.Lease = 5 * time.Minute
	}
	if w.SendTimeout == 0 {
		w.SendTimeout = 4 * time.Minute
	}
	if w.SendTimeout <= 0 || w.Lease < w.SendTimeout+30*time.Second {
		return report, fmt.Errorf("lease must exceed send timeout by at least 30 seconds")
	}
	for n := 0; n < limit; n++ {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		jobs, err := w.Store.ClaimDue(ctx, w.Now(), shard, 1, w.Lease)
		if err != nil {
			return report, err
		}
		if len(jobs) == 0 {
			break
		}
		job := jobs[0]
		report.Claimed++
		attemptCtx, cancel := context.WithTimeout(ctx, w.SendTimeout)
		updated := w.deliver(attemptCtx, job)
		cancel()
		// Keep acknowledgement independent of a canceled send context. Provider
		// results already obtained must be saved even when a shutdown starts.
		ackCtx, ackCancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		err = w.Store.Finish(ackCtx, job, updated)
		ackCancel()
		if err != nil {
			return report, err
		}
		switch {
		case updated.State == Failed:
			report.Failed++
		case updated.Attempts > 0:
			report.Retrying++
		default:
			report.Completed++
		}
	}
	return report, nil
}

func (w Worker) deliver(ctx context.Context, job Job) Job {
	updated := job
	updated.LastError = ""
	updated.Completed = append([]string(nil), job.Completed...)
	updated.InvalidTargets = append([]string(nil), job.InvalidTargets...)
	updated.LeaseToken = ""
	updated.Attempts++
	updated.UpdatedAt = w.Now()
	targets, err := w.Targets.ActiveTargets(ctx, job.RecipientID, w.Now())
	if err != nil {
		return w.retry(updated, "target_lookup")
	}
	seen := make(map[string]bool, len(job.Completed))
	for _, id := range job.Completed {
		seen[id] = true
	}
	pending := make([]push.Target, 0, len(targets))
	for _, target := range targets {
		if !seen[target.ID] {
			pending = append(pending, target)
		}
	}
	message := messageForOccurrence(job.Message, job.RecipientID, job.ID, job.DueAt)
	result, sendErr := w.Sender.Send(ctx, message, pending)
	// Only adapter results for this attempt's exact target may acknowledge it.
	expected := make(map[string]push.Target, len(pending))
	for _, t := range pending {
		expected[t.ID] = t
	}
	unresolved := len(pending)
	var invalid []push.InstallationRef
	for _, id := range job.InvalidTargets {
		invalid = append(invalid, push.InstallationRef{UID: job.RecipientID, InstallationID: id})
	}
	permanent := false
	for _, r := range result.Results {
		t, ok := expected[r.Target.ID]
		if !ok || t != r.Target {
			continue
		}
		delete(expected, t.ID)
		if r.Err == nil && r.Code == push.ErrorNone || r.Code == push.ErrorUnregistered || r.Code == push.ErrorPermanent {
			updated.Completed = append(updated.Completed, t.ID)
			unresolved--
			if r.Code == push.ErrorPermanent {
				permanent = true
			}
			if r.Code == push.ErrorUnregistered {
				invalid = append(invalid, push.InstallationRef{UID: t.RecipientID, InstallationID: t.ID})
				updated.InvalidTargets = append(updated.InvalidTargets, t.ID)
			}
		}
	}
	if len(invalid) > 0 {
		if err = w.Targets.DisablePushInstallations(ctx, invalid, w.Now()); err != nil {
			sendErr = errors.Join(sendErr, err)
		} else {
			updated.InvalidTargets = nil
		}
	}
	if permanent {
		updated.HasPermanentFailure = true
	}
	if unresolved > 0 || sendErr != nil {
		return w.retry(updated, "delivery_unresolved")
	}
	if job.Daily != nil {
		next, err := job.Daily.Next(w.Now())
		if err != nil {
			updated.State = Failed
			updated.LastError = "invalid_daily_schedule"
			return updated
		}
		updated.DueAt = next
		updated.AvailableAt = next
		updated.Completed = nil
		updated.Attempts = 0
		updated.State = Ready
		if updated.HasPermanentFailure {
			updated.LastError = "permanent_delivery_failure"
		}
		updated.HasPermanentFailure = false
	} else {
		updated.State = Done
		updated.Attempts = 0
		if updated.HasPermanentFailure {
			updated.State = Failed
			updated.LastError = "permanent_delivery_failure"
		}
	}
	return updated
}

func (w Worker) retry(job Job, code string) Job {
	job.LastError = code
	if job.Attempts >= MaxAttempts {
		job.State = Failed
		return job
	}
	job.AvailableAt = w.Now().Add(time.Minute * time.Duration(1<<min(job.Attempts-1, 4)))
	return job
}
