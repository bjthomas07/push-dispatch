package push

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ValidateMessage rejects invalid TTLs and oversized payloads before dispatch.
// The conservative 3 KiB envelope leaves room for FCM/APNs protocol overhead.
func ValidateMessage(m Message) error {
	if m.Title == "" && m.Body == "" {
		return fmt.Errorf("title or body is required")
	}
	if m.TTL < 0 || m.TTL > 28*24*time.Hour {
		return fmt.Errorf("TTL must be between zero and 28 days")
	}
	if len(m.Apple.CollapseID) > 64 {
		return fmt.Errorf("Apple collapse ID exceeds 64 bytes")
	}
	if m.Apple.Badge != nil && *m.Apple.Badge < 0 {
		return fmt.Errorf("badge must be nonnegative")
	}
	for k := range m.Data {
		if k == "" || k == "from" || k == "message_type" || strings.HasPrefix(k, "google.") || strings.HasPrefix(k, "gcm.") {
			return fmt.Errorf("reserved data key %q", k)
		}
	}
	if _, err := EnrichNotificationData(m.Data, m.Analytics); err != nil {
		return err
	}
	encoded, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(encoded) > 3072 {
		return fmt.Errorf("message exceeds 3072-byte library payload budget")
	}
	return nil
}
