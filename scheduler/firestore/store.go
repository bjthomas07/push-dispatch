// Package firestore implements durable, sharded scheduler storage on Firestore.
package firestore

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/firestore"
	"github.com/bjthomas07/push-dispatch/scheduler"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	Collection       = "pushSchedules"
	stateField       = "state"
	shardField       = "shard"
	availableAtField = "availableAt"
)

var ErrLeaseLost = errors.New("scheduler lease no longer owned")

type Store struct {
	client *firestore.Client
	jobs   *firestore.CollectionRef
}

// New namespaces every schedule under an app document, e.g. apps/my-app.
func New(client *firestore.Client, app *firestore.DocumentRef) *Store {
	return &Store{client: client, jobs: app.Collection(Collection)}
}

func (s *Store) Put(ctx context.Context, job scheduler.Job) error {
	// A replacement clears any previous claim. An in-flight push may already
	// have left the process, but the old worker cannot overwrite the new schedule.
	_, err := s.jobs.Doc(scheduler.Key(job.RecipientID, job.ID)).Set(ctx, job)
	return err
}

func (s *Store) Get(ctx context.Context, uid, id string) (scheduler.Job, error) {
	doc, err := s.jobs.Doc(scheduler.Key(uid, id)).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return scheduler.Job{}, scheduler.ErrNotFound
	}
	if err != nil {
		return scheduler.Job{}, err
	}
	var job scheduler.Job
	err = doc.DataTo(&job)
	return job, err
}

func (s *Store) Cancel(ctx context.Context, uid, id string, now time.Time) error {
	_, err := s.jobs.Doc(scheduler.Key(uid, id)).Update(ctx, []firestore.Update{
		{Path: stateField, Value: scheduler.Canceled},
		{Path: "leaseToken", Value: ""},
		{Path: "updatedAt", Value: now},
	})
	if status.Code(err) == codes.NotFound {
		return nil
	}
	return err
}

func (s *Store) ClaimDue(ctx context.Context, now time.Time, shard, limit int, lease time.Duration) ([]scheduler.Job, error) {
	if shard < 0 || shard >= scheduler.ShardCount || limit < 1 || limit > 1000 || lease <= 0 {
		return nil, fmt.Errorf("invalid claim options")
	}
	docs, err := s.jobs.Where(stateField, "==", scheduler.Ready).Where(shardField, "==", shard).
		Where(availableAtField, "<=", now).OrderBy(availableAtField, firestore.Asc).Limit(limit).Documents(ctx).GetAll()
	if err != nil {
		return nil, err
	}
	claimed := make([]scheduler.Job, 0, len(docs))
	for _, doc := range docs {
		var job scheduler.Job
		var won bool
		token := rand.Text()
		err = s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
			won = false // Firestore can execute this callback more than once.
			snapshot, err := tx.Get(doc.Ref)
			if status.Code(err) == codes.NotFound {
				return nil
			}
			if err != nil {
				return err
			}
			if err = snapshot.DataTo(&job); err != nil {
				return err
			}
			if job.State != scheduler.Ready || job.Shard != shard || job.AvailableAt.After(now) {
				return nil
			}
			job.LeaseToken = token
			job.AvailableAt = now.Add(lease)
			job.UpdatedAt = now
			if err = tx.Set(doc.Ref, job); err != nil {
				return err
			}
			won = true
			return nil
		})
		if err != nil {
			return claimed, err
		}
		if won {
			claimed = append(claimed, job)
		}
	}
	return claimed, nil
}

func (s *Store) Finish(ctx context.Context, claimed, updated scheduler.Job) error {
	ref := s.jobs.Doc(scheduler.Key(claimed.RecipientID, claimed.ID))
	return s.client.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return ErrLeaseLost
		}
		if err != nil {
			return err
		}
		var current scheduler.Job
		if err = doc.DataTo(&current); err != nil {
			return err
		}
		if claimed.LeaseToken == "" || current.LeaseToken != claimed.LeaseToken || current.State != scheduler.Ready {
			return ErrLeaseLost
		}
		if updated.ID != current.ID || updated.RecipientID != current.RecipientID {
			return fmt.Errorf("cannot change claimed identity")
		}
		return tx.Set(ref, updated)
	})
}
