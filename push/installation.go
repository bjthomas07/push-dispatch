package push

import (
	"crypto/sha256"
	"fmt"
	"strings"
	"time"
)

const (
	MaxInstallationsPerUser  = 20
	InstallationActiveWindow = 45 * 24 * time.Hour
	InstallationRetention    = 90 * 24 * time.Hour
	DisabledInstallRetention = 30 * 24 * time.Hour
	MaxTargetLength          = 4096
	MaxTimezoneLength        = 128
	MaxLocaleLength          = 64
	MaxAppVersionLength      = 64
)

type Permission string

const (
	PermissionGranted Permission = "granted"
	PermissionDenied  Permission = "denied"
	PermissionUnknown Permission = "unknown"
)

func (p Permission) Valid() bool {
	return p == PermissionGranted || p == PermissionDenied || p == PermissionUnknown
}

type Installation struct {
	Provider   Provider          `firestore:"provider" json:"-"`
	TargetType TargetType        `firestore:"targetType" json:"-"`
	FID        string            `firestore:"fid,omitempty" json:"-"`
	Token      string            `firestore:"token,omitempty" json:"-"`
	Platform   Platform          `firestore:"platform" json:"-"`
	Permission Permission        `firestore:"permission" json:"-"`
	Timezone   string            `firestore:"timezone,omitempty" json:"-"`
	Locale     string            `firestore:"locale,omitempty" json:"-"`
	AppVersion string            `firestore:"appVersion,omitempty" json:"-"`
	Enabled    bool              `firestore:"enabled" json:"-"`
	CreatedAt  time.Time         `firestore:"createdAt" json:"-"`
	UpdatedAt  time.Time         `firestore:"updatedAt" json:"-"`
	LastSeenAt time.Time         `firestore:"lastSeenAt" json:"-"`
	DisabledAt time.Time         `firestore:"disabledAt,omitempty" json:"-"`
	Delivered  map[string]string `firestore:"delivered,omitempty" json:"-"`
}

func (i Installation) Address() string {
	switch i.TargetType {
	case TargetFID:
		return i.FID
	case TargetToken:
		return i.Token
	default:
		return ""
	}
}

func (i Installation) ActiveAt(now time.Time) bool {
	return i.Provider == ProviderFCM && i.TargetType.Valid() && i.Address() != "" &&
		i.Platform.Valid() && i.Enabled && i.Permission == PermissionGranted &&
		!i.LastSeenAt.IsZero() && now.Sub(i.LastSeenAt) <= InstallationActiveWindow
}

func (i Installation) DeliveredFor(key, value string) bool {
	return i.Delivered != nil && i.Delivered[key] == value
}

func PreserveRegistrationState(existing, replacement Installation) Installation {
	if !existing.CreatedAt.IsZero() {
		replacement.CreatedAt = existing.CreatedAt
	}
	if len(existing.Delivered) > 0 {
		replacement.Delivered = make(map[string]string, len(existing.Delivered))
		for key, value := range existing.Delivered {
			replacement.Delivered[key] = value
		}
	}
	return replacement
}

type InstallationRef struct {
	UID            string
	InstallationID string
}

type RegistrationRequest struct {
	Provider   Provider   `json:"provider"`
	TargetType TargetType `json:"targetType"`
	Target     string     `json:"target"`
	Platform   Platform   `json:"platform"`
	Permission Permission `json:"permission,omitempty"`
	Timezone   string     `json:"timezone,omitempty"`
	Locale     string     `json:"locale,omitempty"`
	AppVersion string     `json:"appVersion,omitempty"`
	Enabled    *bool      `json:"enabled,omitempty"`
}

type RegistrationResponse struct {
	InstallationID string `json:"installationId"`
}

type HeartbeatRequest struct {
	Permission *Permission `json:"permission,omitempty"`
	Timezone   *string     `json:"timezone,omitempty"`
	Enabled    *bool       `json:"enabled,omitempty"`
}

type InstallationHeartbeat struct {
	Permission *Permission
	Timezone   *string
	Enabled    *bool
	SeenAt     time.Time
}

func InstallationID(provider Provider, targetType TargetType, target string) string {
	digest := sha256.Sum256([]byte(string(provider) + "\x00" + string(targetType) + "\x00" + target))
	return fmt.Sprintf("%x", digest)
}

func NewInstallation(request RegistrationRequest, now time.Time) (string, Installation, error) {
	if request.Provider != ProviderFCM {
		return "", Installation{}, fmt.Errorf("provider must be %q", ProviderFCM)
	}
	if !request.TargetType.Valid() {
		return "", Installation{}, fmt.Errorf("targetType must be %q or %q", TargetFID, TargetToken)
	}
	target := strings.TrimSpace(request.Target)
	if target == "" || len(target) > MaxTargetLength {
		return "", Installation{}, fmt.Errorf("target is required and must be at most %d characters", MaxTargetLength)
	}
	platform := Platform(strings.ToLower(strings.TrimSpace(string(request.Platform))))
	if !platform.Valid() {
		return "", Installation{}, fmt.Errorf("platform must be %q or %q", PlatformIOS, PlatformAndroid)
	}
	permission := request.Permission
	if permission == "" {
		permission = PermissionUnknown
	}
	if !permission.Valid() {
		return "", Installation{}, fmt.Errorf("invalid permission %q", permission)
	}
	if err := ValidateTimezone(request.Timezone); err != nil {
		return "", Installation{}, err
	}
	locale := strings.TrimSpace(request.Locale)
	if len(locale) > MaxLocaleLength {
		return "", Installation{}, fmt.Errorf("locale must be at most %d characters", MaxLocaleLength)
	}
	appVersion := strings.TrimSpace(request.AppVersion)
	if len(appVersion) > MaxAppVersionLength {
		return "", Installation{}, fmt.Errorf("appVersion must be at most %d characters", MaxAppVersionLength)
	}
	enabled := true
	if request.Enabled != nil {
		enabled = *request.Enabled
	}
	installation := Installation{
		Provider: request.Provider, TargetType: request.TargetType, Platform: platform,
		Permission: permission, Timezone: request.Timezone, Locale: locale,
		AppVersion: appVersion, Enabled: enabled, CreatedAt: now, UpdatedAt: now,
		LastSeenAt: now,
	}
	if !enabled {
		installation.DisabledAt = now
	}
	if request.TargetType == TargetFID {
		installation.FID = target
	} else {
		installation.Token = target
	}
	return InstallationID(request.Provider, request.TargetType, target), installation, nil
}

func ValidateHeartbeat(request HeartbeatRequest) error {
	if request.Permission != nil && !request.Permission.Valid() {
		return fmt.Errorf("invalid permission")
	}
	if request.Timezone != nil {
		return ValidateTimezone(*request.Timezone)
	}
	return nil
}

func ValidateTimezone(timezone string) error {
	if timezone == "" {
		return nil
	}
	if len(timezone) > MaxTimezoneLength {
		return fmt.Errorf("timezone must be at most %d characters", MaxTimezoneLength)
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		return fmt.Errorf("invalid timezone")
	}
	return nil
}
