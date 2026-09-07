package push

import (
	"fmt"
	"maps"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	NotificationAnalyticsSchemaVersion  = "1"
	NotificationAnalyticsMaxValueLength = 100

	NotificationDataKeySchemaVersion  = "schema_version"
	NotificationDataKeyNotificationID = "notification_id"
	NotificationDataKeyApp            = "app"
	NotificationDataKeyKind           = "kind"
	NotificationDataKeyAnalyticsLabel = "analytics_label"
)

var analyticsLabelPattern = regexp.MustCompile(`^[a-zA-Z0-9-_.~%]{1,50}$`)

// NotificationAnalytics is the single portable analytics contract for a push.
// Provider adapters derive both their provider-specific label and the client data
// envelope from this metadata, so those two representations cannot drift.
type NotificationAnalytics struct {
	NotificationID string `json:"notificationId,omitempty" firestore:"notificationId,omitempty"`
	App            string `json:"app,omitempty" firestore:"app,omitempty"`
	Kind           string `json:"kind,omitempty" firestore:"kind,omitempty"`
	Label          string `json:"label,omitempty" firestore:"label,omitempty"`
}

var notificationAnalyticsDataKeys = [...]string{
	NotificationDataKeySchemaVersion,
	NotificationDataKeyNotificationID,
	NotificationDataKeyApp,
	NotificationDataKeyKind,
	NotificationDataKeyAnalyticsLabel,
}

func (a NotificationAnalytics) validate() error {
	for _, parameter := range [...]struct {
		name  string
		value string
	}{
		{name: "id", value: a.NotificationID},
		{name: "app", value: a.App},
		{name: "kind", value: a.Kind},
	} {
		trimmed := strings.TrimSpace(parameter.value)
		if trimmed == "" {
			return fmt.Errorf("notification analytics %s is required", parameter.name)
		}
		if trimmed != parameter.value {
			return fmt.Errorf("notification analytics %s must not have surrounding whitespace", parameter.name)
		}
		if utf8.RuneCountInString(parameter.value) > NotificationAnalyticsMaxValueLength {
			return fmt.Errorf("notification analytics %s exceeds %d characters", parameter.name, NotificationAnalyticsMaxValueLength)
		}
	}
	if !analyticsLabelPattern.MatchString(a.Label) {
		return fmt.Errorf("notification analytics label %q must match %s", a.Label, analyticsLabelPattern)
	}
	return nil
}

// EnrichNotificationData returns a cloned, enriched payload. Reserved analytics
// keys are owned by NotificationAnalytics: matching values are harmless, but a
// caller cannot silently override the canonical envelope. The caller's map is
// never mutated because dispatchers may share one Message across concurrent batches.
func EnrichNotificationData(data map[string]string, analytics NotificationAnalytics) (map[string]string, error) {
	if analytics == (NotificationAnalytics{}) {
		for _, key := range notificationAnalyticsDataKeys {
			if _, exists := data[key]; exists {
				return nil, fmt.Errorf("notification data key %q requires NotificationAnalytics", key)
			}
		}
		return maps.Clone(data), nil
	}
	if err := analytics.validate(); err != nil {
		return nil, err
	}

	enriched := maps.Clone(data)
	if enriched == nil {
		enriched = make(map[string]string, len(notificationAnalyticsDataKeys))
	}
	expected := [...]struct {
		key   string
		value string
	}{
		{key: NotificationDataKeySchemaVersion, value: NotificationAnalyticsSchemaVersion},
		{key: NotificationDataKeyNotificationID, value: analytics.NotificationID},
		{key: NotificationDataKeyApp, value: analytics.App},
		{key: NotificationDataKeyKind, value: analytics.Kind},
		{key: NotificationDataKeyAnalyticsLabel, value: analytics.Label},
	}
	for _, field := range expected {
		if existing, exists := enriched[field.key]; exists && existing != field.value {
			return nil, fmt.Errorf("notification data key %q conflicts with NotificationAnalytics", field.key)
		}
		enriched[field.key] = field.value
	}
	return enriched, nil
}
