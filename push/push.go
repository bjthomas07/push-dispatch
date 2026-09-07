// Package push defines provider-neutral push notification messages and targets.
// Provider adapters live in subpackages so application code does not depend on a
// vendor SDK.
package push

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// MaxBatchTargets is Firebase Cloud Messaging's combined FID/token multicast
	// limit. Keeping it in the provider-neutral layer makes every dispatcher obey
	// the narrowest live provider boundary.
	MaxBatchTargets = 500

	DefaultBatchConcurrency = 1
	MaxBatchConcurrency     = 4
)

type Provider string

const (
	ProviderFCM Provider = "fcm"
)

func (p Provider) Valid() bool { return p == ProviderFCM }

type TargetType string

const (
	TargetFID   TargetType = "fid"
	TargetToken TargetType = "token"
)

func (t TargetType) Valid() bool { return t == TargetFID || t == TargetToken }

type Platform string

const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
)

func (p Platform) Valid() bool { return p == PlatformIOS || p == PlatformAndroid }

// Target is one provider address. ID is a server-generated installation ID and
// RecipientID is the owning application user; both are safe to log. Address is
// sensitive and must never be logged.
type Target struct {
	ID          string
	RecipientID string
	Type        TargetType
	Address     string
}

type AppleConfig struct {
	Sound      string `json:"sound,omitempty" firestore:"sound,omitempty"`
	Badge      *int   `json:"badge,omitempty" firestore:"badge,omitempty"`
	CollapseID string `json:"collapseId,omitempty" firestore:"collapseId,omitempty"`
}

type AndroidConfig struct {
	Sound       string `json:"sound,omitempty" firestore:"sound,omitempty"`
	ChannelID   string `json:"channelId,omitempty" firestore:"channelId,omitempty"`
	Icon        string `json:"icon,omitempty" firestore:"icon,omitempty"`
	Color       string `json:"color,omitempty" firestore:"color,omitempty"`
	CollapseKey string `json:"collapseKey,omitempty" firestore:"collapseKey,omitempty"`
}

// Message contains only the portable payload and the platform overrides used by
// the applications. Provider adapters translate it to their SDK types.
type Message struct {
	Title     string                `json:"title,omitempty" firestore:"title,omitempty"`
	Body      string                `json:"body,omitempty" firestore:"body,omitempty"`
	Data      map[string]string     `json:"data,omitempty" firestore:"data,omitempty"`
	Analytics NotificationAnalytics `json:"analytics,omitempty" firestore:"analytics,omitempty"`
	TTL       time.Duration         `json:"ttlNanoseconds,omitempty" firestore:"ttlNanoseconds,omitempty"`
	Apple     AppleConfig           `json:"apple,omitempty" firestore:"apple,omitempty"`
	Android   AndroidConfig         `json:"android,omitempty" firestore:"android,omitempty"`
}

type ErrorCode string

const (
	ErrorNone         ErrorCode = ""
	ErrorUnregistered ErrorCode = "unregistered"
	ErrorTransient    ErrorCode = "transient"
	ErrorRateLimited  ErrorCode = "rate_limited"
	ErrorPermanent    ErrorCode = "permanent"
	ErrorUnknown      ErrorCode = "unknown"
)

type sendError struct {
	code ErrorCode
	err  error
}

func (e *sendError) Error() string { return e.err.Error() }
func (e *sendError) Unwrap() error { return e.err }

// NewSendError preserves a provider-neutral classification for a batch-level
// failure, where no TargetResult exists to carry the code.
func NewSendError(code ErrorCode, err error) error {
	if err == nil {
		return nil
	}
	if code == ErrorNone {
		code = ErrorUnknown
	}
	return &sendError{code: code, err: err}
}

// SendErrorCode returns the provider-neutral classification attached to a
// batch-level error. Unclassified errors are unknown and are not retried.
func SendErrorCode(err error) ErrorCode {
	if err == nil {
		return ErrorNone
	}
	var classified *sendError
	if errors.As(err, &classified) {
		return classified.code
	}
	return ErrorUnknown
}

type TargetResult struct {
	Target    Target
	MessageID string
	Code      ErrorCode
	Err       error
}

type BatchResult struct {
	Results      []TargetResult
	SuccessCount int
	FailureCount int
}

// BatchSender sends one provider batch. Callers must use Dispatcher rather than
// invoking this directly unless they already enforce MaxBatchTargets.
type BatchSender interface {
	SendBatch(context.Context, Message, []Target) (BatchResult, error)
}

func validateTarget(target Target) error {
	if target.Address == "" {
		return fmt.Errorf("push target address is required")
	}
	if !target.Type.Valid() {
		return fmt.Errorf("unsupported push target type %q", target.Type)
	}
	return nil
}
