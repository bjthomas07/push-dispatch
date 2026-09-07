package fcm

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strings"
	"testing"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"
	"google.golang.org/api/option"

	"github.com/bjthomas07/push-dispatch/push"
)

type fakeMessagingClient struct {
	message *messaging.MulticastMessage
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func (f *fakeMessagingClient) SendEachForMulticast(_ context.Context, message *messaging.MulticastMessage) (*messaging.BatchResponse, error) {
	f.message = message
	count := len(message.Tokens) + len(message.Fids)
	responses := make([]*messaging.SendResponse, count)
	for index := range responses {
		responses[index] = &messaging.SendResponse{Success: true, MessageID: fmt.Sprintf("message-%d", index)}
	}
	return &messaging.BatchResponse{SuccessCount: count, Responses: responses}, nil
}

func TestSenderMapsTokensAndFIDsToAdminSDKFields(t *testing.T) {
	client := &fakeMessagingClient{}
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	sender := newWithClientAndClock(client, func() time.Time { return now })
	badge := 1
	targets := []push.Target{
		{ID: "fid-one", Type: push.TargetFID, Address: "fid-value-one"},
		{ID: "token-one", Type: push.TargetToken, Address: "token-value"},
		{ID: "fid-two", Type: push.TargetFID, Address: "fid-value-two"},
	}
	message := push.Message{
		Title: "Title",
		Body:  "Body",
		Data:  map[string]string{"deepLink": "exampleapp://daily"},
		Analytics: push.NotificationAnalytics{
			NotificationID: "pl_morning_2026-08-19_1200",
			App:            "exampleapp",
			Kind:           "morning",
			Label:          "pl_morning_2026-08-19",
		},
		TTL:   time.Hour,
		Apple: push.AppleConfig{Sound: "default", Badge: &badge, CollapseID: "daily"},
		Android: push.AndroidConfig{
			Sound: "default", ChannelID: "reminders", CollapseKey: "daily",
		},
	}

	result, err := sender.SendBatch(context.Background(), message, targets)
	if err != nil {
		t.Fatal(err)
	}
	if got := client.message.Tokens; len(got) != 1 || got[0] != "token-value" {
		t.Fatalf("Tokens = %v, want [token-value]", got)
	}
	if got := client.message.Fids; len(got) != 2 || got[0] != "fid-value-one" || got[1] != "fid-value-two" {
		t.Fatalf("Fids = %v, want both FIDs", got)
	}
	if len(client.message.Tokens)+len(client.message.Fids) > push.MaxBatchTargets {
		t.Fatal("mixed multicast exceeded provider limit")
	}
	if result.SuccessCount != 3 || len(result.Results) != 3 {
		t.Fatalf("result = %+v, want three successes", result)
	}
	// The Admin SDK reports token results first, then FID results. The adapter must
	// retain the corresponding installation identity after that reorder.
	if result.Results[0].Target.ID != "token-one" || result.Results[1].Target.ID != "fid-one" || result.Results[2].Target.ID != "fid-two" {
		t.Fatalf("result target order = %q/%q/%q", result.Results[0].Target.ID, result.Results[1].Target.ID, result.Results[2].Target.ID)
	}
	if client.message.APNS == nil || client.message.APNS.Payload.Aps.Badge == nil || *client.message.APNS.Payload.Aps.Badge != 1 {
		t.Fatal("APNS badge mapping missing")
	}
	if got := client.message.APNS.Headers["apns-expiration"]; got != "1787144400" {
		t.Fatalf("APNS expiration = %q, want TTL deadline 1787144400", got)
	}
	if got := client.message.APNS.Headers["apns-collapse-id"]; got != "daily" {
		t.Fatalf("APNS collapse id = %q, want daily", got)
	}
	if client.message.Android == nil || client.message.Android.Notification == nil || client.message.Android.Notification.ChannelID != "reminders" {
		t.Fatal("Android channel mapping missing")
	}
	if client.message.FCMOptions == nil || client.message.FCMOptions.AnalyticsLabel != "pl_morning_2026-08-19" {
		t.Fatalf("FCM analytics options = %+v", client.message.FCMOptions)
	}
	if client.message.Data[push.NotificationDataKeySchemaVersion] != push.NotificationAnalyticsSchemaVersion ||
		client.message.Data[push.NotificationDataKeyNotificationID] != "pl_morning_2026-08-19_1200" ||
		client.message.Data[push.NotificationDataKeyApp] != "exampleapp" ||
		client.message.Data[push.NotificationDataKeyKind] != "morning" ||
		client.message.Data[push.NotificationDataKeyAnalyticsLabel] != "pl_morning_2026-08-19" {
		t.Fatalf("FCM analytics data = %+v", client.message.Data)
	}
	if len(message.Data) != 1 || message.Data["deepLink"] != "exampleapp://daily" {
		t.Fatalf("caller data mutated = %+v", message.Data)
	}
}

func TestSenderRejectsInvalidAnalyticsBeforeCallingClient(t *testing.T) {
	tests := []struct {
		name    string
		message push.Message
	}{
		{
			name: "invalid label",
			message: push.Message{Analytics: push.NotificationAnalytics{
				NotificationID: "notification-1", App: "exampleapp", Kind: "morning", Label: "spaces are invalid",
			}},
		},
		{
			name: "label longer than fifty characters",
			message: push.Message{Analytics: push.NotificationAnalytics{
				NotificationID: "notification-1", App: "exampleapp", Kind: "morning", Label: strings.Repeat("a", 51),
			}},
		},
		{
			name: "blank metadata value",
			message: push.Message{Analytics: push.NotificationAnalytics{
				NotificationID: "notification-1", App: "  ", Kind: "morning", Label: "pl_morning_2026-08-19",
			}},
		},
		{
			name: "metadata value with surrounding whitespace",
			message: push.Message{Analytics: push.NotificationAnalytics{
				NotificationID: "notification-1", App: "exampleapp ", Kind: "morning", Label: "pl_morning_2026-08-19",
			}},
		},
		{
			name: "metadata value longer than Analytics limit",
			message: push.Message{Analytics: push.NotificationAnalytics{
				NotificationID: strings.Repeat("a", push.NotificationAnalyticsMaxValueLength+1),
				App:            "exampleapp", Kind: "morning", Label: "pl_morning_2026-08-19",
			}},
		},
		{
			name: "conflicting reserved key",
			message: push.Message{
				Data: map[string]string{push.NotificationDataKeyKind: "evening"},
				Analytics: push.NotificationAnalytics{
					NotificationID: "notification-1", App: "exampleapp", Kind: "morning", Label: "pl_morning_2026-08-19",
				},
			},
		},
		{
			name:    "reserved key without metadata",
			message: push.Message{Data: map[string]string{push.NotificationDataKeySchemaVersion: "1"}},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &fakeMessagingClient{}
			originalData := maps.Clone(test.message.Data)
			_, err := newWithClient(client).SendBatch(context.Background(), test.message, []push.Target{{
				Type: push.TargetToken, Address: "token",
			}})
			if err == nil {
				t.Fatal("SendBatch error = nil")
			}
			if client.message != nil {
				t.Fatal("invalid analytics reached messaging client")
			}
			if !maps.Equal(test.message.Data, originalData) {
				t.Fatalf("caller data mutated on validation failure: got %+v, want %+v", test.message.Data, originalData)
			}
		})
	}
}

func TestSenderAllowsExactlyFiveHundredMixedTargets(t *testing.T) {
	client := &fakeMessagingClient{}
	sender := newWithClient(client)
	targets := make([]push.Target, 0, push.MaxBatchTargets)
	for index := 0; index < push.MaxBatchTargets; index++ {
		targetType := push.TargetFID
		if index%2 == 0 {
			targetType = push.TargetToken
		}
		targets = append(targets, push.Target{Type: targetType, Address: fmt.Sprintf("target-%d", index)})
	}
	if _, err := sender.SendBatch(context.Background(), push.Message{Title: "test"}, targets); err != nil {
		t.Fatal(err)
	}
	if got := len(client.message.Tokens) + len(client.message.Fids); got != push.MaxBatchTargets {
		t.Fatalf("mixed target count = %d, want %d", got, push.MaxBatchTargets)
	}
}

func TestSenderRejectsMoreThanFiveHundredMixedTargets(t *testing.T) {
	targets := make([]push.Target, push.MaxBatchTargets+1)
	for index := range targets {
		targets[index] = push.Target{Type: push.TargetFID, Address: fmt.Sprintf("fid-%d", index)}
	}
	if _, err := newWithClient(&fakeMessagingClient{}).SendBatch(context.Background(), push.Message{}, targets); err == nil {
		t.Fatal("oversized FCM batch error = nil")
	}
}

func TestSenderClassifiesThirdPartyAuthAsUnknown(t *testing.T) {
	ctx := context.Background()
	httpClient := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		body := `{"error":{"status":"INVALID_ARGUMENT","message":"APNS auth failed","details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"THIRD_PARTY_AUTH_ERROR"}]}}`
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(body)),
		}, nil
	})}
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: "test-project"}, option.WithHTTPClient(httpClient))
	if err != nil {
		t.Fatal(err)
	}
	client, err := app.Messaging(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := newWithClient(client).SendBatch(ctx, push.Message{Title: "Title"}, []push.Target{{
		ID: "ios", Type: push.TargetFID, Address: "fid-value",
	}})
	if err != nil {
		t.Fatalf("SendBatch error = %v, want per-target failure", err)
	}
	if result.FailureCount != 1 || len(result.Results) != 1 {
		t.Fatalf("result = %+v, want one failure", result)
	}
	targetResult := result.Results[0]
	if !messaging.IsThirdPartyAuthError(targetResult.Err) || targetResult.Code != push.ErrorUnknown {
		t.Fatalf("target result = %+v, want third-party auth classified unknown", targetResult)
	}
}
