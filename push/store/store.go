package pushstore

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"cloud.google.com/go/firestore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/bjthomas07/push-dispatch/push"
)

const (
	UsersCollection    = "users"
	OwnersCollection   = "pushTargetOwners"
	InstallationsField = "pushInstallations"
	OwnerUIDField      = "uid"
	UpdatedAtField     = "updatedAt"
)

var (
	ErrInstallationNotFound  = push.ErrInstallationNotFound
	ErrUserNotFound          = push.ErrUserNotFound
	ErrAccountDeletionFenced = push.ErrAccountDeletionFenced
)

type AppDocumentFunc func(context.Context) *firestore.DocumentRef

type Config struct {
	// CreateUsers permits authenticated first-registration to create a minimal user document.
	CreateUsers bool
	AppDocument AppDocumentFunc
	// UserDocument and OwnersCollection override the default app-relative paths.
	// Use them when an existing application keeps push state in a separate document.
	UserDocument             func(context.Context, string) *firestore.DocumentRef
	OwnersCollection         *firestore.CollectionRef
	DeleteRequestsCollection string
	DeleteRequestStatusField string
	BlockedDeleteStatuses    map[string]bool
}

type Store struct {
	createUsers              bool
	fs                       *firestore.Client
	appDocument              AppDocumentFunc
	userDocument             func(context.Context, string) *firestore.DocumentRef
	owners                   *firestore.CollectionRef
	deleteRequestsCollection string
	deleteRequestStatusField string
	blockedDeleteStatuses    map[string]bool
}

func New(fs *firestore.Client, config Config) *Store {
	return &Store{createUsers: config.CreateUsers,
		fs: fs, appDocument: config.AppDocument,
		userDocument: config.UserDocument, owners: config.OwnersCollection,
		deleteRequestsCollection: config.DeleteRequestsCollection,
		deleteRequestStatusField: config.DeleteRequestStatusField,
		blockedDeleteStatuses:    config.BlockedDeleteStatuses,
	}
}

type installationDocument struct {
	Installations map[string]push.Installation `firestore:"pushInstallations"`
}

type ownerDocument struct {
	UID       string    `firestore:"uid"`
	UpdatedAt time.Time `firestore:"updatedAt"`
}

func (s *Store) userDoc(ctx context.Context, uid string) *firestore.DocumentRef {
	if s.userDocument != nil {
		return s.userDocument(ctx, uid)
	}
	return s.appDocument(ctx).Collection(UsersCollection).Doc(uid)
}

func (s *Store) ownerCollection(ctx context.Context) *firestore.CollectionRef {
	if s.owners != nil {
		return s.owners
	}
	return s.appDocument(ctx).Collection(OwnersCollection)
}

func (s *Store) UpsertPushInstallation(ctx context.Context, uid, installationID string, installation push.Installation) error {
	if installation.UpdatedAt.IsZero() {
		installation.UpdatedAt = time.Now()
	}
	if installation.CreatedAt.IsZero() {
		installation.CreatedAt = installation.UpdatedAt
	}
	userRef := s.userDoc(ctx, uid)
	ownerRef := s.ownerCollection(ctx).Doc(installationID)
	original := installation
	return s.fs.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		installation := original
		if s.deleteRequestsCollection != "" {
			deletionRef := s.appDocument(ctx).Collection(s.deleteRequestsCollection).Doc(uid)
			if doc, err := tx.Get(deletionRef); err == nil {
				blocked := len(s.blockedDeleteStatuses) == 0
				if !blocked && s.deleteRequestStatusField != "" {
					statusValue, fieldErr := doc.DataAt(s.deleteRequestStatusField)
					statusName, ok := statusValue.(string)
					blocked = fieldErr != nil || deleteStatusBlocked(statusName, ok, s.blockedDeleteStatuses)
				}
				if blocked {
					return ErrAccountDeletionFenced
				}
			} else if status.Code(err) != codes.NotFound {
				return fmt.Errorf("get account deletion fence: %w", err)
			}
		}

		userDoc, err := tx.Get(userRef)
		missingUser := status.Code(err) == codes.NotFound
		if missingUser && !s.createUsers {
			return ErrUserNotFound
		}
		if err != nil && !missingUser {
			return fmt.Errorf("get push installation user: %w", err)
		}
		var stored installationDocument
		if !missingUser {
			if err := userDoc.DataTo(&stored); err != nil {
				return fmt.Errorf("decode push installations: %w", err)
			}
		}
		if stored.Installations == nil {
			stored.Installations = make(map[string]push.Installation)
		}

		ownerUID := ""
		ownerDoc, ownerErr := tx.Get(ownerRef)
		if ownerErr == nil {
			var owner ownerDocument
			if err := ownerDoc.DataTo(&owner); err != nil {
				return fmt.Errorf("decode push target owner: %w", err)
			}
			ownerUID = owner.UID
		} else if status.Code(ownerErr) != codes.NotFound {
			return fmt.Errorf("get push target owner: %w", ownerErr)
		}

		var priorOwnerRef *firestore.DocumentRef
		var priorInstallations map[string]push.Installation
		if ownerUID != "" && ownerUID != uid {
			priorOwnerRef = s.userDoc(ctx, ownerUID)
			priorDoc, err := tx.Get(priorOwnerRef)
			if err == nil {
				var prior installationDocument
				if err := priorDoc.DataTo(&prior); err != nil {
					return fmt.Errorf("decode prior push target owner: %w", err)
				}
				priorInstallations = prior.Installations
			} else if status.Code(err) != codes.NotFound {
				return fmt.Errorf("get prior push target owner: %w", err)
			}
		}

		if existing, ok := stored.Installations[installationID]; ok {
			installation = push.PreserveRegistrationState(existing, installation)
		}
		stored.Installations[installationID] = installation
		var prunedIDs []string
		stored.Installations, prunedIDs = prune(stored.Installations, installationID, installation.UpdatedAt)
		prunedOwnerRefs, err := s.ownedPrunedTargetRefs(ctx, tx, uid, prunedIDs)
		if err != nil {
			return err
		}

		if priorOwnerRef != nil {
			if retired, changed := retire(priorInstallations, installationID, installation.UpdatedAt); changed {
				if err := tx.Update(priorOwnerRef, installationUpdates(retired, installation.UpdatedAt)); err != nil {
					return err
				}
			}
		}
		if err := tx.Set(userRef, map[string]any{InstallationsField: stored.Installations, UpdatedAtField: installation.UpdatedAt}, firestore.Merge(firestore.FieldPath{InstallationsField}, firestore.FieldPath{UpdatedAtField})); err != nil {
			return err
		}
		if err := tx.Set(ownerRef, ownerDocument{UID: uid, UpdatedAt: installation.UpdatedAt}); err != nil {
			return err
		}
		for _, ref := range prunedOwnerRefs {
			if err := tx.Delete(ref); err != nil {
				return err
			}
		}
		return nil
	})
}

func deleteStatusBlocked(statusName string, valid bool, configured map[string]bool) bool {
	if !valid {
		return true
	}
	blocked, known := configured[statusName]
	return !known || blocked
}

func (s *Store) HeartbeatPushInstallation(ctx context.Context, uid, installationID string, heartbeat push.InstallationHeartbeat) error {
	ref := s.userDoc(ctx, uid)
	return s.fs.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				return ErrInstallationNotFound
			}
			return fmt.Errorf("get user for push heartbeat: %w", err)
		}
		var stored installationDocument
		if err := doc.DataTo(&stored); err != nil {
			return fmt.Errorf("decode push installations: %w", err)
		}
		installation, ok := stored.Installations[installationID]
		if !ok || installation.Address() == "" {
			return ErrInstallationNotFound
		}
		if heartbeat.Permission != nil {
			installation.Permission = *heartbeat.Permission
		}
		if heartbeat.Timezone != nil {
			installation.Timezone = *heartbeat.Timezone
		}
		if heartbeat.Enabled != nil {
			installation.Enabled = *heartbeat.Enabled
			if installation.Enabled {
				installation.DisabledAt = time.Time{}
			} else {
				installation.DisabledAt = heartbeat.SeenAt
			}
		}
		installation.LastSeenAt = heartbeat.SeenAt
		installation.UpdatedAt = heartbeat.SeenAt
		stored.Installations[installationID] = installation
		var prunedIDs []string
		stored.Installations, prunedIDs = prune(stored.Installations, installationID, heartbeat.SeenAt)
		prunedOwnerRefs, err := s.ownedPrunedTargetRefs(ctx, tx, uid, prunedIDs)
		if err != nil {
			return err
		}
		if err := tx.Update(ref, installationUpdates(stored.Installations, heartbeat.SeenAt)); err != nil {
			return err
		}
		for _, ownerRef := range prunedOwnerRefs {
			if err := tx.Delete(ownerRef); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ownedPrunedTargetRefs(ctx context.Context, tx *firestore.Transaction, uid string, installationIDs []string) ([]*firestore.DocumentRef, error) {
	sort.Strings(installationIDs)
	refs := make([]*firestore.DocumentRef, 0, len(installationIDs))
	for _, installationID := range installationIDs {
		ref := s.ownerCollection(ctx).Doc(installationID)
		doc, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("get pruned push target owner: %w", err)
		}
		var owner ownerDocument
		if err := doc.DataTo(&owner); err != nil {
			return nil, fmt.Errorf("decode pruned push target owner: %w", err)
		}
		if ownerBelongsTo(owner, uid) {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

func ownerBelongsTo(owner ownerDocument, uid string) bool {
	return uid != "" && owner.UID == uid
}

func (s *Store) DisablePushInstallation(ctx context.Context, uid, installationID string, disabledAt time.Time) error {
	return s.disable(ctx, uid, map[string]struct{}{installationID: {}}, disabledAt, true)
}

func (s *Store) DisablePushInstallations(ctx context.Context, refs []push.InstallationRef, disabledAt time.Time) error {
	byUser := make(map[string]map[string]struct{})
	for _, ref := range refs {
		if ref.UID == "" || ref.InstallationID == "" {
			continue
		}
		if byUser[ref.UID] == nil {
			byUser[ref.UID] = make(map[string]struct{})
		}
		byUser[ref.UID][ref.InstallationID] = struct{}{}
	}
	var errs []error
	for uid, ids := range byUser {
		if err := s.disable(ctx, uid, ids, disabledAt, false); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Store) MarkPushInstallationsDelivered(ctx context.Context, refs []push.InstallationRef, key, value string) error {
	if key == "" || value == "" || len(key) > 64 || len(value) > 128 {
		return fmt.Errorf("invalid push delivery marker")
	}
	byUser := make(map[string]map[string]struct{})
	for _, ref := range refs {
		if ref.UID == "" || ref.InstallationID == "" {
			continue
		}
		if byUser[ref.UID] == nil {
			byUser[ref.UID] = make(map[string]struct{})
		}
		byUser[ref.UID][ref.InstallationID] = struct{}{}
	}
	var errs []error
	for uid, ids := range byUser {
		if err := s.markDelivered(ctx, uid, ids, key, value); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Store) markDelivered(ctx context.Context, uid string, ids map[string]struct{}, key, value string) error {
	ref := s.userDoc(ctx, uid)
	return s.fs.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get user for push delivery marker: %w", err)
		}
		var stored installationDocument
		if err := doc.DataTo(&stored); err != nil {
			return fmt.Errorf("decode push installations for delivery marker: %w", err)
		}
		changed := false
		for id := range ids {
			installation, ok := stored.Installations[id]
			if !ok || installation.Address() == "" || installation.DeliveredFor(key, value) {
				continue
			}
			if installation.Delivered == nil {
				installation.Delivered = make(map[string]string)
			}
			installation.Delivered[key] = value
			stored.Installations[id] = installation
			changed = true
		}
		if !changed {
			return nil
		}
		return tx.Update(ref, []firestore.Update{{Path: InstallationsField, Value: stored.Installations}})
	})
}

func (s *Store) disable(ctx context.Context, uid string, ids map[string]struct{}, disabledAt time.Time, requireMatch bool) error {
	ref := s.userDoc(ctx, uid)
	return s.fs.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)
		if err != nil {
			if status.Code(err) == codes.NotFound && requireMatch {
				return ErrInstallationNotFound
			}
			if status.Code(err) == codes.NotFound {
				return nil
			}
			return err
		}
		var stored installationDocument
		if err := doc.DataTo(&stored); err != nil {
			return fmt.Errorf("decode push installations: %w", err)
		}
		matched := false
		for id := range ids {
			var changed bool
			stored.Installations, changed = retire(stored.Installations, id, disabledAt)
			matched = matched || changed
		}
		if !matched {
			if requireMatch {
				return ErrInstallationNotFound
			}
			return nil
		}
		return tx.Update(ref, installationUpdates(stored.Installations, disabledAt))
	})
}

func (s *Store) DeleteOwnersForUser(ctx context.Context, uid string) error {
	docs, err := s.ownerCollection(ctx).Where(OwnerUIDField, "==", uid).Documents(ctx).GetAll()
	if err != nil {
		return fmt.Errorf("list owned push targets: %w", err)
	}
	var errs []error
	for _, doc := range docs {
		if err := s.deleteOwnerIfOwned(ctx, doc.Ref, uid); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (s *Store) deleteOwnerIfOwned(ctx context.Context, ref *firestore.DocumentRef, uid string) error {
	return s.fs.RunTransaction(ctx, func(ctx context.Context, tx *firestore.Transaction) error {
		doc, err := tx.Get(ref)
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return fmt.Errorf("get owned push target: %w", err)
		}
		var owner ownerDocument
		if err := doc.DataTo(&owner); err != nil {
			return fmt.Errorf("decode owned push target: %w", err)
		}
		if owner.UID != uid {
			return nil
		}
		return tx.Delete(ref)
	})
}

func (s *Store) NewestActiveTarget(ctx context.Context, uid string, now time.Time) (push.Target, error) {
	doc, err := s.userDoc(ctx, uid).Get(ctx)
	if status.Code(err) == codes.NotFound {
		return push.Target{}, ErrUserNotFound
	}
	if err != nil {
		return push.Target{}, fmt.Errorf("get push installation user: %w", err)
	}
	var stored installationDocument
	if err := doc.DataTo(&stored); err != nil {
		return push.Target{}, fmt.Errorf("decode push installations: %w", err)
	}
	id, installation, ok := NewestActiveInstallation(stored.Installations, now)
	if !ok {
		return push.Target{}, ErrInstallationNotFound
	}
	return push.Target{
		ID: id, RecipientID: uid, Type: installation.TargetType, Address: installation.Address(),
	}, nil
}

func NewestActiveInstallation(installations map[string]push.Installation, now time.Time) (string, push.Installation, bool) {
	var newestID string
	var newest push.Installation
	for id, installation := range installations {
		if !installation.ActiveAt(now) {
			continue
		}
		if newestID == "" || installation.LastSeenAt.After(newest.LastSeenAt) ||
			(installation.LastSeenAt.Equal(newest.LastSeenAt) && id < newestID) {
			newestID = id
			newest = installation
		}
	}
	return newestID, newest, newestID != ""
}

func installationUpdates(installations map[string]push.Installation, updatedAt time.Time) []firestore.Update {
	return []firestore.Update{
		{Path: InstallationsField, Value: installations},
		{Path: UpdatedAtField, Value: updatedAt},
	}
}

func retire(installations map[string]push.Installation, id string, disabledAt time.Time) (map[string]push.Installation, bool) {
	installation, ok := installations[id]
	if !ok {
		return installations, false
	}
	installation.Enabled = false
	installation.FID = ""
	installation.Token = ""
	installation.DisabledAt = disabledAt
	installation.UpdatedAt = disabledAt
	installations[id] = installation
	return installations, true
}

func prune(installations map[string]push.Installation, keepID string, now time.Time) (map[string]push.Installation, []string) {
	removed := make([]string, 0)
	for id, installation := range installations {
		if id == keepID {
			continue
		}
		lastSeen := installation.LastSeenAt
		if lastSeen.IsZero() {
			lastSeen = installation.UpdatedAt
		}
		if !lastSeen.IsZero() && now.Sub(lastSeen) > push.InstallationRetention {
			delete(installations, id)
			removed = append(removed, id)
			continue
		}
		if !installation.Enabled && !installation.DisabledAt.IsZero() && now.Sub(installation.DisabledAt) > push.DisabledInstallRetention {
			delete(installations, id)
			removed = append(removed, id)
		}
	}
	if len(installations) <= push.MaxInstallationsPerUser {
		sort.Strings(removed)
		return installations, removed
	}
	type candidate struct {
		id       string
		lastSeen time.Time
	}
	candidates := make([]candidate, 0, len(installations))
	for id, installation := range installations {
		if id == keepID {
			continue
		}
		lastSeen := installation.LastSeenAt
		if lastSeen.IsZero() {
			lastSeen = installation.UpdatedAt
		}
		candidates = append(candidates, candidate{id: id, lastSeen: lastSeen})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].lastSeen.Equal(candidates[j].lastSeen) {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].lastSeen.Before(candidates[j].lastSeen)
	})
	for _, candidate := range candidates {
		if len(installations) <= push.MaxInstallationsPerUser {
			break
		}
		delete(installations, candidate.id)
		removed = append(removed, candidate.id)
	}
	sort.Strings(removed)
	return installations, removed
}
