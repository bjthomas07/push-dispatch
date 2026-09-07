package pushstore

import (
	"fmt"
	"testing"
	"time"

	"github.com/bjthomas07/push-dispatch/push"
)

func TestPruneCapsMapAndKeepsCurrent(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	installations := make(map[string]push.Installation)
	for index := 0; index < push.MaxInstallationsPerUser+5; index++ {
		installations[fmt.Sprintf("installation-%02d", index)] = push.Installation{
			Enabled: true, LastSeenAt: now.Add(-time.Duration(index) * time.Hour),
		}
	}
	keepID := "installation-24"
	got, removed := prune(installations, keepID, now)
	if len(got) != push.MaxInstallationsPerUser {
		t.Fatalf("installations = %d, want %d", len(got), push.MaxInstallationsPerUser)
	}
	if _, ok := got[keepID]; !ok {
		t.Fatalf("current installation %q was pruned", keepID)
	}
	if len(removed) != 5 {
		t.Fatalf("removed installation IDs = %v, want 5", removed)
	}
}

func TestPruneReportsAgeAndDisabledRemovalsForOwnerCleanup(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	installations := map[string]push.Installation{
		"keep":     {Enabled: true, LastSeenAt: now},
		"stale":    {Enabled: true, LastSeenAt: now.Add(-push.InstallationRetention - time.Hour)},
		"disabled": {DisabledAt: now.Add(-push.DisabledInstallRetention - time.Hour), LastSeenAt: now},
	}
	got, removed := prune(installations, "keep", now)
	if len(got) != 1 || removed[0] != "disabled" || removed[1] != "stale" {
		t.Fatalf("installations/removed = %+v/%v", got, removed)
	}
}

func TestRetireClearsAddressAndKeepsHistory(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	installations := map[string]push.Installation{
		"device": {Enabled: true, FID: "fid", Token: "token"},
	}
	got, changed := retire(installations, "device", now)
	retired := got["device"]
	if !changed || retired.Enabled || retired.FID != "" || retired.Token != "" || !retired.DisabledAt.Equal(now) {
		t.Fatalf("retired installation = %+v, changed=%v", retired, changed)
	}
}

func TestNewestActiveInstallationIsDeterministic(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	installation := func(address string, seen time.Time) push.Installation {
		return push.Installation{
			Provider: push.ProviderFCM, TargetType: push.TargetToken, Token: address,
			Platform: push.PlatformAndroid, Permission: push.PermissionGranted,
			Enabled: true, LastSeenAt: seen,
		}
	}
	id, got, ok := NewestActiveInstallation(map[string]push.Installation{
		"b": installation("b", now), "a": installation("a", now),
	}, now)
	if !ok || id != "a" || got.Token != "a" {
		t.Fatalf("newest = %q/%+v/%v", id, got, ok)
	}
}

func TestDeleteStatusBlockedFailsClosed(t *testing.T) {
	configured := map[string]bool{"processing": true, "completed": false}
	if !deleteStatusBlocked("processing", true, configured) {
		t.Fatal("processing deletion was not fenced")
	}
	if deleteStatusBlocked("completed", true, configured) {
		t.Fatal("explicitly unblocked completed status was fenced")
	}
	if !deleteStatusBlocked("unknown", true, configured) || !deleteStatusBlocked("", false, configured) {
		t.Fatal("unknown or invalid deletion status did not fail closed")
	}
}

func TestPrunedOwnerCleanupIsUIDConditional(t *testing.T) {
	owner := ownerDocument{UID: "new-owner"}
	if ownerBelongsTo(owner, "old-owner") {
		t.Fatal("pruning an old user's installation would delete its transferred owner claim")
	}
	if !ownerBelongsTo(owner, "new-owner") {
		t.Fatal("current owner claim was not eligible for pruning cleanup")
	}
}
