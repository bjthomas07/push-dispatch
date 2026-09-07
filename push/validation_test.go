package push

import (
	"strings"
	"testing"
	"time"
)

func TestValidateMessageProtectsProviderEnvelope(t *testing.T) {
	if err := ValidateMessage(Message{Title: "Hello", Body: "World", Data: map[string]string{"deep_link": "example://today"}}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []Message{
		{},
		{Title: "hi", TTL: -time.Second},
		{Title: "hi", TTL: 29 * 24 * time.Hour},
		{Title: "hi", Data: map[string]string{"google.foo": "reserved"}},
		{Title: "hi", Data: map[string]string{"notification_id": "spoofed"}},
		{Title: strings.Repeat("x", 3073)},
		{Title: "hi", Apple: AppleConfig{CollapseID: strings.Repeat("x", 65)}},
	} {
		if ValidateMessage(m) == nil {
			t.Fatal("invalid message accepted")
		}
	}
}
