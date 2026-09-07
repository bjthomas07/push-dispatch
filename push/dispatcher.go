package push

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"time"
)

const (
	// DefaultSendAttempts includes the first provider call. The Admin SDK already
	// retries transport-level failures; these attempts cover remaining classified
	// transient and quota responses while keeping successful/permanent results out.
	DefaultSendAttempts      = 3
	transientRetryBackoff    = time.Second
	rateLimitRetryBackoff    = 60 * time.Second
	maxRetryJitterPercentage = 10
)

type retryWaitFunc func(context.Context, time.Duration) error
type retryJitterFunc func(time.Duration) time.Duration

// Dispatcher deduplicates targets, chunks them to the provider limit, and keeps
// the number of provider batches in flight bounded. The FCM Go SDK has its own
// 50-worker pool inside each multicast call, so production should normally use
// the default single batch in flight.
type Dispatcher struct {
	sender      BatchSender
	concurrency int
	maxAttempts int
	retryWait   retryWaitFunc
	retryJitter retryJitterFunc
}

func NewDispatcher(sender BatchSender, concurrency int) (*Dispatcher, error) {
	return newDispatcherWithRetry(sender, concurrency, DefaultSendAttempts, waitForRetry, boundedRetryJitter)
}

// newDispatcherWithRetry is the deterministic test seam for retry timing. Production
// callers use NewDispatcher and receive bounded exponential backoff.
func newDispatcherWithRetry(sender BatchSender, concurrency, maxAttempts int, retryWait retryWaitFunc, retryJitter retryJitterFunc) (*Dispatcher, error) {
	if sender == nil {
		return nil, fmt.Errorf("push batch sender is required")
	}
	if concurrency <= 0 {
		concurrency = DefaultBatchConcurrency
	}
	if concurrency > MaxBatchConcurrency {
		concurrency = MaxBatchConcurrency
	}
	if maxAttempts <= 0 {
		maxAttempts = DefaultSendAttempts
	}
	if retryWait == nil {
		retryWait = waitForRetry
	}
	if retryJitter == nil {
		retryJitter = boundedRetryJitter
	}
	return &Dispatcher{
		sender: sender, concurrency: concurrency, maxAttempts: maxAttempts,
		retryWait: retryWait, retryJitter: retryJitter,
	}, nil
}

func (d *Dispatcher) Send(ctx context.Context, message Message, targets []Target) (BatchResult, error) {
	unique, err := deduplicateTargets(targets)
	if err != nil {
		return BatchResult{}, err
	}
	if len(unique) == 0 {
		return BatchResult{}, nil
	}

	batches := chunkTargets(unique, MaxBatchTargets)
	type batchJob struct {
		index   int
		targets []Target
	}
	type batchOutcome struct {
		index  int
		result BatchResult
		err    error
	}

	jobs := make(chan batchJob, len(batches))
	outcomes := make(chan batchOutcome, len(batches))
	workers := d.concurrency
	if workers > len(batches) {
		workers = len(batches)
	}
	for worker := 0; worker < workers; worker++ {
		go func() {
			for job := range jobs {
				result, sendErr := d.sendBatch(ctx, message, job.targets)
				outcomes <- batchOutcome{index: job.index, result: result, err: sendErr}
			}
		}()
	}
	for index, batch := range batches {
		jobs <- batchJob{index: index, targets: batch}
	}
	close(jobs)

	ordered := make([]batchOutcome, len(batches))
	for range batches {
		outcome := <-outcomes
		ordered[outcome.index] = outcome
	}

	var combined BatchResult
	var sendErrors []error
	for _, outcome := range ordered {
		combined.Results = append(combined.Results, outcome.result.Results...)
		combined.SuccessCount += outcome.result.SuccessCount
		combined.FailureCount += outcome.result.FailureCount
		if outcome.err != nil {
			sendErrors = append(sendErrors, outcome.err)
		}
	}
	return combined, errors.Join(sendErrors...)
}

// sendBatch retries only unresolved transient or rate-limited targets. A successful
// target is added to finalResults immediately and can never re-enter pending during
// this invocation. Permanent and unregistered failures are final too.
func (d *Dispatcher) sendBatch(ctx context.Context, message Message, targets []Target) (BatchResult, error) {
	pending := append([]Target(nil), targets...)
	finalResults := make(map[string]TargetResult, len(targets))
	var lastErr error

	for attempt := 1; attempt <= d.maxAttempts && len(pending) > 0; attempt++ {
		result, sendErr := d.sender.SendBatch(ctx, message, pending)
		lastErr = sendErr
		pendingByKey := make(map[string]Target, len(pending))
		for _, target := range pending {
			pendingByKey[targetKey(target)] = target
		}

		retryByKey := make(map[string]Target)
		retryCodeByKey := make(map[string]ErrorCode)
		accounted := make(map[string]struct{}, len(result.Results))
		for _, targetResult := range result.Results {
			key := targetKey(targetResult.Target)
			target, ok := pendingByKey[key]
			if !ok {
				continue
			}
			accounted[key] = struct{}{}
			// Preserve the original target metadata even if a provider adapter returns
			// only its delivery address in a result.
			targetResult.Target = target
			if targetResult.Err != nil && targetResult.Code == ErrorNone {
				targetResult.Code = ErrorUnknown
			}
			if retryableErrorCode(targetResult.Code) && attempt < d.maxAttempts {
				retryByKey[key] = target
				retryCodeByKey[key] = targetResult.Code
				continue
			}
			finalResults[key] = targetResult
		}

		// A top-level provider error normally has no per-target response. Any target
		// without a result is unresolved and eligible for at-least-once retry; an
		// ambiguous acceptance can produce a duplicate. Accounted successes are
		// deliberately excluded.
		for key, target := range pendingByKey {
			if _, ok := accounted[key]; ok {
				continue
			}
			missingCode := ErrorTransient
			if sendErr != nil {
				missingCode = SendErrorCode(sendErr)
			}
			if retryableErrorCode(missingCode) && attempt < d.maxAttempts {
				retryByKey[key] = target
				retryCodeByKey[key] = missingCode
				continue
			}
			missingErr := sendErr
			if missingErr == nil {
				missingErr = fmt.Errorf("push provider omitted a target result")
			}
			finalResults[key] = TargetResult{Target: target, Code: missingCode, Err: missingErr}
		}

		if len(retryByKey) == 0 {
			pending = nil
			break
		}
		pending = pending[:0]
		// Rebuild in the original batch order, both for deterministic tests and for
		// stable provider request ordering during incident diagnosis.
		for _, target := range targets {
			if retryTarget, ok := retryByKey[targetKey(target)]; ok {
				pending = append(pending, retryTarget)
			}
		}
		retryCode := ErrorTransient
		for key := range retryByKey {
			if retryCodeByKey[key] == ErrorRateLimited {
				retryCode = ErrorRateLimited
				break
			}
		}
		delay := d.retryJitter(retryBackoff(retryCode, attempt))
		if err := d.retryWait(ctx, delay); err != nil {
			lastErr = err
			for _, target := range pending {
				code := retryCodeByKey[targetKey(target)]
				finalResults[targetKey(target)] = TargetResult{Target: target, Code: code, Err: err}
			}
			pending = nil
			break
		}
	}

	combined := BatchResult{Results: make([]TargetResult, 0, len(targets))}
	var unresolved int
	for _, target := range targets {
		result, ok := finalResults[targetKey(target)]
		if !ok {
			result = TargetResult{Target: target, Code: ErrorTransient, Err: lastErr}
		}
		combined.Results = append(combined.Results, result)
		if result.Err == nil && result.Code == ErrorNone {
			combined.SuccessCount++
			continue
		}
		combined.FailureCount++
		if result.Code == ErrorTransient || result.Code == ErrorRateLimited || result.Code == ErrorUnknown {
			unresolved++
		}
	}
	if unresolved > 0 {
		if lastErr != nil {
			return combined, fmt.Errorf("%d push target(s) remain unresolved after %d attempts: %w", unresolved, d.maxAttempts, lastErr)
		}
		return combined, fmt.Errorf("%d push target(s) remain unresolved after %d attempts", unresolved, d.maxAttempts)
	}
	return combined, nil
}

func targetKey(target Target) string {
	return string(target.Type) + "\x00" + target.Address
}

func retryableErrorCode(code ErrorCode) bool {
	return code == ErrorTransient || code == ErrorRateLimited
}

func retryBackoff(code ErrorCode, completedAttempt int) time.Duration {
	base := transientRetryBackoff
	if code == ErrorRateLimited {
		base = rateLimitRetryBackoff
	}
	return base << min(completedAttempt-1, 2)
}

// boundedRetryJitter only adds delay, so FCM quota retries never occur before the
// required 60-second floor. Ten percent keeps the two production waits below 198
// seconds total, leaving room inside the scheduled job's 300-second task timeout.
func boundedRetryJitter(base time.Duration) time.Duration {
	bound := base * maxRetryJitterPercentage / 100
	if bound <= 0 {
		return base
	}
	return base + time.Duration(rand.Int64N(int64(bound)+1))
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func deduplicateTargets(targets []Target) ([]Target, error) {
	seen := make(map[string]struct{}, len(targets))
	unique := make([]Target, 0, len(targets))
	for _, target := range targets {
		if err := validateTarget(target); err != nil {
			return nil, err
		}
		key := targetKey(target)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, target)
	}
	return unique, nil
}

func chunkTargets(targets []Target, size int) [][]Target {
	if len(targets) == 0 {
		return nil
	}
	chunks := make([][]Target, 0, (len(targets)+size-1)/size)
	for start := 0; start < len(targets); start += size {
		end := start + size
		if end > len(targets) {
			end = len(targets)
		}
		chunks = append(chunks, targets[start:end])
	}
	return chunks
}
