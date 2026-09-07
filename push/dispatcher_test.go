package push

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type recordingBatchSender struct {
	mu            sync.Mutex
	batches       [][]Target
	inFlight      int
	maxInFlight   int
	responseDelay time.Duration
}

type scriptedBatchSender struct {
	responses []func([]Target) (BatchResult, error)
	calls     [][]Target
}

func (s *scriptedBatchSender) SendBatch(_ context.Context, _ Message, targets []Target) (BatchResult, error) {
	s.calls = append(s.calls, append([]Target(nil), targets...))
	index := len(s.calls) - 1
	if index >= len(s.responses) {
		index = len(s.responses) - 1
	}
	return s.responses[index](targets)
}

func noRetryWait(context.Context, time.Duration) error { return nil }
func noRetryJitter(delay time.Duration) time.Duration  { return delay }

func (s *recordingBatchSender) SendBatch(_ context.Context, _ Message, targets []Target) (BatchResult, error) {
	s.mu.Lock()
	s.inFlight++
	if s.inFlight > s.maxInFlight {
		s.maxInFlight = s.inFlight
	}
	s.batches = append(s.batches, append([]Target(nil), targets...))
	s.mu.Unlock()

	time.Sleep(s.responseDelay)

	result := BatchResult{SuccessCount: len(targets), Results: make([]TargetResult, 0, len(targets))}
	for _, target := range targets {
		result.Results = append(result.Results, TargetResult{Target: target, MessageID: "ok"})
	}
	s.mu.Lock()
	s.inFlight--
	s.mu.Unlock()
	return result, nil
}

func TestDispatcherDeduplicatesChunksAndBoundsConcurrency(t *testing.T) {
	sender := &recordingBatchSender{responseDelay: 5 * time.Millisecond}
	dispatcher, err := NewDispatcher(sender, 2)
	if err != nil {
		t.Fatal(err)
	}

	targets := make([]Target, 0, 1202)
	for i := 0; i < 1201; i++ {
		targets = append(targets, Target{ID: fmt.Sprint(i), Type: TargetFID, Address: fmt.Sprintf("fid-%d", i)})
	}
	targets = append(targets, targets[0])

	result, err := dispatcher.Send(context.Background(), Message{Title: "test"}, targets)
	if err != nil {
		t.Fatal(err)
	}
	if result.SuccessCount != 1201 || len(result.Results) != 1201 {
		t.Fatalf("result = %d successes/%d rows, want 1201/1201", result.SuccessCount, len(result.Results))
	}

	sender.mu.Lock()
	defer sender.mu.Unlock()
	if len(sender.batches) != 3 {
		t.Fatalf("batches = %d, want 3", len(sender.batches))
	}
	for index, batch := range sender.batches {
		if len(batch) > MaxBatchTargets {
			t.Errorf("batch %d has %d targets, max %d", index, len(batch), MaxBatchTargets)
		}
	}
	if sender.maxInFlight > 2 {
		t.Errorf("max concurrency = %d, want <= 2", sender.maxInFlight)
	}
}

func TestNewDispatcherCapsConcurrency(t *testing.T) {
	sender := &recordingBatchSender{}
	dispatcher, err := NewDispatcher(sender, MaxBatchConcurrency+50)
	if err != nil {
		t.Fatal(err)
	}
	if dispatcher.concurrency != MaxBatchConcurrency {
		t.Fatalf("concurrency = %d, want hard cap %d", dispatcher.concurrency, MaxBatchConcurrency)
	}
}

func TestDispatcherRejectsInvalidTarget(t *testing.T) {
	dispatcher, err := NewDispatcher(&recordingBatchSender{}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.Send(context.Background(), Message{}, []Target{{Type: "other", Address: "x"}}); err == nil {
		t.Fatal("invalid target error = nil")
	}
}

func TestDispatcherRetriesOnlyTransientTargets(t *testing.T) {
	targets := []Target{
		{ID: "successful", Type: TargetToken, Address: "token-success"},
		{ID: "transient", Type: TargetToken, Address: "token-transient"},
		{ID: "unregistered", Type: TargetFID, Address: "fid-unregistered"},
	}
	sender := &scriptedBatchSender{responses: []func([]Target) (BatchResult, error){
		func(got []Target) (BatchResult, error) {
			return BatchResult{Results: []TargetResult{
				{Target: got[0], MessageID: "message-success"},
				{Target: got[1], Code: ErrorTransient, Err: fmt.Errorf("temporarily unavailable")},
				{Target: got[2], Code: ErrorUnregistered, Err: fmt.Errorf("not registered")},
			}}, nil
		},
		func(got []Target) (BatchResult, error) {
			return BatchResult{Results: []TargetResult{{Target: got[0], MessageID: "message-retry"}}}, nil
		},
	}}
	dispatcher, err := newDispatcherWithRetry(sender, 1, 3, noRetryWait, noRetryJitter)
	if err != nil {
		t.Fatal(err)
	}

	result, err := dispatcher.Send(context.Background(), Message{}, targets)
	if err != nil {
		t.Fatal(err)
	}
	if len(sender.calls) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(sender.calls))
	}
	if len(sender.calls[1]) != 1 || sender.calls[1][0].ID != "transient" {
		t.Fatalf("retry targets = %+v, want only transient target", sender.calls[1])
	}
	if result.SuccessCount != 2 || result.FailureCount != 1 || len(result.Results) != 3 {
		t.Fatalf("combined result = %+v, want 2 successes and 1 final failure", result)
	}
	if result.Results[0].Target.ID != "successful" || result.Results[1].Target.ID != "transient" || result.Results[2].Code != ErrorUnregistered {
		t.Fatalf("combined target results = %+v", result.Results)
	}
}

func TestDispatcherRetriesTopLevelFailureWithoutSleeping(t *testing.T) {
	targets := []Target{{ID: "one", Type: TargetToken, Address: "token-one"}}
	sender := &scriptedBatchSender{responses: []func([]Target) (BatchResult, error){
		func([]Target) (BatchResult, error) {
			return BatchResult{}, NewSendError(ErrorTransient, fmt.Errorf("provider unavailable"))
		},
		func(got []Target) (BatchResult, error) {
			return BatchResult{Results: []TargetResult{{Target: got[0], MessageID: "accepted"}}}, nil
		},
	}}
	dispatcher, err := newDispatcherWithRetry(sender, 1, 3, noRetryWait, noRetryJitter)
	if err != nil {
		t.Fatal(err)
	}
	result, err := dispatcher.Send(context.Background(), Message{}, targets)
	if err != nil || result.SuccessCount != 1 || len(sender.calls) != 2 {
		t.Fatalf("result/error/calls = %+v/%v/%d, want success after one retry", result, err, len(sender.calls))
	}
}

func TestDispatcherPropagatesExhaustedTransientFailure(t *testing.T) {
	targets := []Target{{ID: "one", Type: TargetFID, Address: "fid-one"}}
	sender := &scriptedBatchSender{responses: []func([]Target) (BatchResult, error){
		func(got []Target) (BatchResult, error) {
			return BatchResult{Results: []TargetResult{{Target: got[0], Code: ErrorTransient, Err: fmt.Errorf("quota")}}}, nil
		},
	}}
	dispatcher, err := newDispatcherWithRetry(sender, 1, 3, noRetryWait, noRetryJitter)
	if err != nil {
		t.Fatal(err)
	}
	result, err := dispatcher.Send(context.Background(), Message{}, targets)
	if err == nil {
		t.Fatal("exhausted transient error = nil")
	}
	if len(sender.calls) != 3 {
		t.Fatalf("provider calls = %d, want exactly 3", len(sender.calls))
	}
	if result.SuccessCount != 0 || result.FailureCount != 1 || result.Results[0].Code != ErrorTransient {
		t.Fatalf("result = %+v, want one unresolved transient", result)
	}
}

func TestDispatcherRateLimitUsesSixtySecondExponentialBackoff(t *testing.T) {
	target := Target{ID: "one", Type: TargetFID, Address: "fid-one"}
	sender := &scriptedBatchSender{responses: []func([]Target) (BatchResult, error){
		func(got []Target) (BatchResult, error) {
			return BatchResult{Results: []TargetResult{{
				Target: got[0], Code: ErrorRateLimited, Err: fmt.Errorf("quota exceeded"),
			}}}, nil
		},
		func(got []Target) (BatchResult, error) {
			return BatchResult{Results: []TargetResult{{Target: got[0], MessageID: "accepted"}}}, nil
		},
	}}
	var waits []time.Duration
	wait := func(_ context.Context, delay time.Duration) error {
		waits = append(waits, delay)
		return nil
	}
	dispatcher, err := newDispatcherWithRetry(sender, 1, 3, wait, noRetryJitter)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.Send(context.Background(), Message{}, []Target{target}); err != nil {
		t.Fatal(err)
	}
	if len(waits) != 1 || waits[0] != 60*time.Second {
		t.Fatalf("rate-limit waits = %v, want [1m0s]", waits)
	}
	if retryBackoff(ErrorRateLimited, 2) != 120*time.Second {
		t.Fatalf("second rate-limit backoff = %v, want 2m0s", retryBackoff(ErrorRateLimited, 2))
	}
	maxTotalWait := retryBackoff(ErrorRateLimited, 1)*110/100 + retryBackoff(ErrorRateLimited, 2)*110/100
	if maxTotalWait >= 300*time.Second {
		t.Fatalf("maximum jittered quota waits = %v, must fit within 5-minute job timeout", maxTotalWait)
	}
}

func TestBoundedRetryJitterNeverShortensOrExceedsTenPercent(t *testing.T) {
	for _, base := range []time.Duration{time.Second, 60 * time.Second, 120 * time.Second} {
		for range 100 {
			delay := boundedRetryJitter(base)
			if delay < base || delay > base*110/100 {
				t.Fatalf("jittered delay %v outside [%v, %v]", delay, base, base*110/100)
			}
		}
	}
}

func TestDispatcherRetryJitterIsInjectedAndContextCancellationStopsRetries(t *testing.T) {
	target := Target{ID: "one", Type: TargetToken, Address: "token-one"}
	sender := &scriptedBatchSender{responses: []func([]Target) (BatchResult, error){
		func(got []Target) (BatchResult, error) {
			return BatchResult{Results: []TargetResult{{
				Target: got[0], Code: ErrorTransient, Err: fmt.Errorf("unavailable"),
			}}}, nil
		},
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var gotDelay time.Duration
	wait := func(ctx context.Context, delay time.Duration) error {
		gotDelay = delay
		return ctx.Err()
	}
	jitter := func(delay time.Duration) time.Duration { return delay + 100*time.Millisecond }
	dispatcher, err := newDispatcherWithRetry(sender, 1, 3, wait, jitter)
	if err != nil {
		t.Fatal(err)
	}
	_, err = dispatcher.Send(ctx, Message{}, []Target{target})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled retry error = %v, want context.Canceled", err)
	}
	if gotDelay != 1100*time.Millisecond {
		t.Fatalf("injected jitter delay = %v, want 1.1s", gotDelay)
	}
	if len(sender.calls) != 1 {
		t.Fatalf("provider calls after cancellation = %d, want 1", len(sender.calls))
	}
}

func TestUnclassifiedTopLevelFailureIsNotRetried(t *testing.T) {
	target := Target{ID: "one", Type: TargetToken, Address: "token-one"}
	sender := &scriptedBatchSender{responses: []func([]Target) (BatchResult, error){
		func([]Target) (BatchResult, error) { return BatchResult{}, fmt.Errorf("unknown failure") },
	}}
	dispatcher, err := newDispatcherWithRetry(sender, 1, 3, noRetryWait, noRetryJitter)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dispatcher.Send(context.Background(), Message{}, []Target{target}); err == nil {
		t.Fatal("unknown top-level failure error = nil")
	}
	if len(sender.calls) != 1 {
		t.Fatalf("unknown failure provider calls = %d, want no retry", len(sender.calls))
	}
}

func TestUnknownTargetFailureIsNotRetriedAndRemainsUnresolved(t *testing.T) {
	target := Target{ID: "one", Type: TargetFID, Address: "fid-one"}
	sender := &scriptedBatchSender{responses: []func([]Target) (BatchResult, error){
		func(got []Target) (BatchResult, error) {
			return BatchResult{Results: []TargetResult{{
				Target: got[0], Code: ErrorUnknown, Err: fmt.Errorf("provider configuration failed"),
			}}}, nil
		},
	}}
	dispatcher, err := newDispatcherWithRetry(sender, 1, 3, noRetryWait, noRetryJitter)
	if err != nil {
		t.Fatal(err)
	}
	result, err := dispatcher.Send(context.Background(), Message{}, []Target{target})
	if err == nil {
		t.Fatal("unknown target failure error = nil")
	}
	if len(sender.calls) != 1 {
		t.Fatalf("unknown target provider calls = %d, want no retry", len(sender.calls))
	}
	if result.FailureCount != 1 || result.Results[0].Code != ErrorUnknown {
		t.Fatalf("result = %+v, want one unresolved unknown failure", result)
	}
}
