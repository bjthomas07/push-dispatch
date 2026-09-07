// Package scheduler runs durable one-off and daily local-time notifications.
// Storage and delivery are interfaces; the Firestore adapter lives in firestore.
package scheduler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/bjthomas07/push-dispatch/push"
)

type State string

var ErrNotFound = errors.New("schedule not found")

const (
	Ready       State = "ready"
	Done        State = "done"
	Failed      State = "failed"
	Canceled    State = "canceled"
	ShardCount        = 32
	MaxAttempts       = 5
)

// Daily uses HHMM and an IANA timezone. Nonexistent wall times are skipped;
// repeated wall times fire at the first occurrence only.
type Daily struct {
	LocalTime int    `json:"localTime" firestore:"localTime"`
	Timezone  string `json:"timezone" firestore:"timezone"`
}

func (d Daily) Validate() error {
	if d.LocalTime < 0 || d.LocalTime > 2359 || d.LocalTime%100 > 59 {
		return fmt.Errorf("localTime must be HHMM")
	}
	if d.Timezone == "" || len(d.Timezone) > 128 || d.Timezone == "Local" {
		return fmt.Errorf("an IANA timezone is required")
	}
	_, err := time.LoadLocation(d.Timezone)
	return err
}

// Next returns the first daily occurrence strictly after after. Iterating real
// instants avoids time.Date's unspecified choice around daylight-saving changes.
func (d Daily) Next(after time.Time) (time.Time, error) {
	if err := d.Validate(); err != nil {
		return time.Time{}, err
	}
	loc, _ := time.LoadLocation(d.Timezone)
	local := after.In(loc)
	for day := 0; day < 8; day++ {
		date := time.Date(local.Year(), local.Month(), local.Day()+day, 12, 0, 0, 0, time.UTC)
		start := date.Add(-36 * time.Hour)
		end := date.Add(36 * time.Hour)
		for t := start; t.Before(end); t = t.Add(time.Minute) {
			wall := t.In(loc)
			if wall.Year() == date.Year() && wall.YearDay() == date.YearDay() && wall.Hour()*100+wall.Minute() == d.LocalTime {
				if t.After(after) {
					return t.UTC(), nil
				}
				break // never send twice during a repeated local hour
			}
		}
	}
	return time.Time{}, fmt.Errorf("no daily occurrence in the next week")
}

type Schedule struct {
	ID          string       `json:"id"`
	RecipientID string       `json:"recipientId"`
	At          time.Time    `json:"at,omitempty"`
	Daily       *Daily       `json:"daily,omitempty"`
	Message     push.Message `json:"message"`
}

func (s Schedule) Validate() error {
	for _, v := range []string{s.ID, s.RecipientID} {
		if v == "" || len(v) > 128 || strings.TrimSpace(v) != v || strings.ContainsAny(v, "/\x00") || v == "." || v == ".." {
			return fmt.Errorf("id and recipientId must be nonempty path-safe identifiers, at most 128 bytes")
		}
	}
	if s.Daily != nil {
		if !s.At.IsZero() {
			return fmt.Errorf("provide either at or daily")
		}
		if err := s.Daily.Validate(); err != nil {
			return err
		}
	} else if s.At.IsZero() {
		return fmt.Errorf("at or daily is required")
	}
	for _, k := range []string{push.NotificationDataKeyNotificationID, push.NotificationDataKeySchemaVersion, push.NotificationDataKeyApp, push.NotificationDataKeyKind, push.NotificationDataKeyAnalyticsLabel} {
		if _, ok := s.Message.Data[k]; ok {
			return fmt.Errorf("scheduler owns data key %s", k)
		}
	}
	return push.ValidateMessage(messageForOccurrence(s.Message, s.RecipientID, s.ID, time.Unix(0, 0)))
}

// Job is adapter data, not a client-writeable API. AvailableAt is the indexed
// retry/lease deadline; DueAt remains stable for the current occurrence.
type Job struct {
	ID                  string       `firestore:"id"`
	RecipientID         string       `firestore:"recipientId"`
	Daily               *Daily       `firestore:"daily,omitempty"`
	Message             push.Message `firestore:"message"`
	State               State        `firestore:"state"`
	Shard               int          `firestore:"shard"`
	DueAt               time.Time    `firestore:"dueAt"`
	AvailableAt         time.Time    `firestore:"availableAt"`
	LeaseToken          string       `firestore:"leaseToken"`
	Attempts            int          `firestore:"attempts"`
	Completed           []string     `firestore:"completed"`
	InvalidTargets      []string     `firestore:"invalidTargets"`
	LastError           string       `firestore:"lastError"`
	HasPermanentFailure bool         `firestore:"hasPermanentFailure"`
	UpdatedAt           time.Time    `firestore:"updatedAt"`
}

func Key(uid, id string) string {
	h := sha256.Sum256([]byte(uid + "\x00" + id))
	return hex.EncodeToString(h[:])
}

func NewJob(s Schedule, now time.Time) (Job, error) {
	if err := s.Validate(); err != nil {
		return Job{}, err
	}
	due := s.At.UTC()
	if s.Daily != nil {
		var err error
		due, err = s.Daily.Next(now)
		if err != nil {
			return Job{}, err
		}
	}
	h := sha256.Sum256([]byte(Key(s.RecipientID, s.ID)))
	return Job{ID: s.ID, RecipientID: s.RecipientID, Daily: s.Daily, Message: s.Message, State: Ready, Shard: int(h[0]) % ShardCount, DueAt: due, AvailableAt: due, UpdatedAt: now}, nil
}

// Store must claim and finish with atomic compare-and-set semantics. A stale
// lease cannot finish, overwrite a replacement, or resurrect a canceled job.
type Store interface {
	Put(context.Context, Job) error
	Get(context.Context, string, string) (Job, error)
	Cancel(context.Context, string, string, time.Time) error
	ClaimDue(context.Context, time.Time, int, int, time.Duration) ([]Job, error)
	Finish(context.Context, Job, Job) error
}

type Targets interface {
	ActiveTargets(context.Context, string, time.Time) ([]push.Target, error)
	DisablePushInstallations(context.Context, []push.InstallationRef, time.Time) error
}

type Sender interface {
	Send(context.Context, push.Message, []push.Target) (push.BatchResult, error)
}

// Build the full provider envelope during validation as well as delivery, so
// accepted schedules cannot exceed the payload budget after metadata is added.
func messageForOccurrence(message push.Message, uid, id string, due time.Time) push.Message {
	if message.TTL == 0 {
		message.TTL = time.Hour
	}
	message.Analytics.NotificationID = Key(Key(uid, id), due.UTC().Format(time.RFC3339Nano))
	if message.Analytics.App == "" {
		message.Analytics.App = "default"
	}
	if message.Analytics.Kind == "" {
		message.Analytics.Kind = "scheduled"
	}
	if message.Analytics.Label == "" {
		message.Analytics.Label = "scheduled"
	}
	return message
}
