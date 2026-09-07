package push

import (
	"context"
	"errors"
	"time"
)

// InstallationStore is the authenticated registration boundary. Adapters own
// persistence and atomic target ownership; mobile clients never write it directly.
type InstallationStore interface {
	UpsertPushInstallation(context.Context, string, string, Installation) error
	HeartbeatPushInstallation(context.Context, string, string, InstallationHeartbeat) error
	DisablePushInstallation(context.Context, string, string, time.Time) error
}

func UnregisteredInstallationRefs(result BatchResult) []InstallationRef {
	var refs []InstallationRef
	for _, r := range result.Results {
		if r.Code == ErrorUnregistered && r.Target.RecipientID != "" && r.Target.ID != "" {
			refs = append(refs, InstallationRef{UID: r.Target.RecipientID, InstallationID: r.Target.ID})
		}
	}
	return refs
}

var (
	ErrInstallationNotFound  = errors.New("push installation not found")
	ErrUserNotFound          = errors.New("push installation user not found")
	ErrAccountDeletionFenced = errors.New("account deletion prevents push registration")
)
