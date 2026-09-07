package pushstore

import (
	"context"
	"github.com/bjthomas07/push-dispatch/push"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sort"
	"time"
)

// ActiveTargets reads one bounded installation map, including all active devices.
func (s *Store) ActiveTargets(ctx context.Context, uid string, now time.Time) ([]push.Target, error) {
	doc, err := s.userDoc(ctx, uid).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var stored installationDocument
	if err = doc.DataTo(&stored); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(stored.Installations))
	for id := range stored.Installations {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var targets []push.Target
	for _, id := range ids {
		i := stored.Installations[id]
		if i.ActiveAt(now) {
			targets = append(targets, push.Target{ID: id, RecipientID: uid, Type: i.TargetType, Address: i.Address()})
		}
	}
	return targets, nil
}
