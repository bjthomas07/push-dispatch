package push

import (
	"testing"
	"time"
)

func TestNewInstallationStoresMetadataWithoutChangingStableID(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	request := RegistrationRequest{
		Provider: ProviderFCM, TargetType: TargetFID, Target: "fid", Platform: PlatformIOS,
		Permission: PermissionGranted, Timezone: "America/New_York", Locale: "en-US", AppVersion: "2.1.0",
	}
	id, installation, err := NewInstallation(request, now)
	if err != nil {
		t.Fatal(err)
	}
	request.Locale = "fr-FR"
	request.AppVersion = "2.2.0"
	updatedID, _, err := NewInstallation(request, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 64 || updatedID != id {
		t.Fatalf("installation ids = %q/%q", id, updatedID)
	}
	if installation.FID != "fid" || installation.Locale != "en-US" || installation.AppVersion != "2.1.0" || !installation.ActiveAt(now) {
		t.Fatalf("installation = %+v", installation)
	}
}

func TestNewInstallationRejectsInvalidInput(t *testing.T) {
	valid := RegistrationRequest{
		Provider: ProviderFCM, TargetType: TargetToken, Target: "token", Platform: PlatformAndroid,
		Permission: PermissionGranted, Timezone: "UTC",
	}
	tests := []func(*RegistrationRequest){
		func(r *RegistrationRequest) { r.Provider = "other" },
		func(r *RegistrationRequest) { r.TargetType = "other" },
		func(r *RegistrationRequest) { r.Target = "" },
		func(r *RegistrationRequest) { r.Platform = "other" },
		func(r *RegistrationRequest) { r.Permission = "other" },
		func(r *RegistrationRequest) { r.Timezone = "not/a-zone" },
	}
	for index, mutate := range tests {
		request := valid
		mutate(&request)
		if _, _, err := NewInstallation(request, time.Now()); err == nil {
			t.Errorf("case %d accepted invalid request: %+v", index, request)
		}
	}
}

func TestPreserveRegistrationStateKeepsDeliveryRetryMarkers(t *testing.T) {
	created := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	existing := Installation{CreatedAt: created, Delivered: map[string]string{"series_day": "2026-08-19_1200"}}
	replacement := PreserveRegistrationState(existing, Installation{CreatedAt: created.Add(time.Hour), AppVersion: "2.2.0"})
	if !replacement.CreatedAt.Equal(created) || replacement.Delivered["series_day"] != "2026-08-19_1200" {
		t.Fatalf("replacement = %+v", replacement)
	}
	replacement.Delivered["series_day"] = "changed"
	if existing.Delivered["series_day"] != "2026-08-19_1200" {
		t.Fatal("delivery marker map was aliased")
	}
}
