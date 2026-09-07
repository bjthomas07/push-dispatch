// Package fcm adapts Firebase Admin Cloud Messaging to the provider-neutral
// push package.
package fcm

import (
	"context"
	"fmt"
	"strconv"
	"time"

	firebase "firebase.google.com/go/v4"
	"firebase.google.com/go/v4/messaging"

	"github.com/bjthomas07/push-dispatch/push"
)

type messagingClient interface {
	SendEachForMulticast(context.Context, *messaging.MulticastMessage) (*messaging.BatchResponse, error)
}

type Sender struct {
	client messagingClient
	now    func() time.Time
}

func New(ctx context.Context, projectID string) (*Sender, error) {
	if projectID == "" {
		return nil, fmt.Errorf("firebase project id is required")
	}
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: projectID})
	if err != nil {
		return nil, fmt.Errorf("initialize firebase app: %w", err)
	}
	client, err := app.Messaging(ctx)
	if err != nil {
		return nil, fmt.Errorf("initialize firebase messaging: %w", err)
	}
	return &Sender{client: client, now: time.Now}, nil
}

func newWithClient(client messagingClient) *Sender { return newWithClientAndClock(client, time.Now) }

func newWithClientAndClock(client messagingClient, now func() time.Time) *Sender {
	return &Sender{client: client, now: now}
}

func (s *Sender) SendBatch(ctx context.Context, message push.Message, targets []push.Target) (push.BatchResult, error) {
	if len(targets) == 0 {
		return push.BatchResult{}, nil
	}
	if len(targets) > push.MaxBatchTargets {
		return push.BatchResult{}, fmt.Errorf("FCM batch has %d targets, max %d", len(targets), push.MaxBatchTargets)
	}

	if err := push.ValidateMessage(message); err != nil {
		return push.BatchResult{}, err
	}
	multicast, err := toMulticastMessage(message, s.now())
	if err != nil {
		return push.BatchResult{}, fmt.Errorf("build FCM message: %w", err)
	}
	orderedTargets := make([]push.Target, 0, len(targets))
	for _, target := range targets {
		if target.Type == push.TargetToken {
			multicast.Tokens = append(multicast.Tokens, target.Address)
			orderedTargets = append(orderedTargets, target)
		}
	}
	for _, target := range targets {
		if target.Type == push.TargetFID {
			multicast.Fids = append(multicast.Fids, target.Address)
			orderedTargets = append(orderedTargets, target)
		}
	}
	if len(orderedTargets) != len(targets) {
		return push.BatchResult{}, fmt.Errorf("FCM batch contains an unsupported target type")
	}

	response, err := s.client.SendEachForMulticast(ctx, multicast)
	if err != nil {
		return push.BatchResult{}, push.NewSendError(classifyError(err), fmt.Errorf("send FCM multicast: %w", err))
	}
	if len(response.Responses) != len(orderedTargets) {
		return push.BatchResult{}, fmt.Errorf("FCM returned %d target results for %d targets", len(response.Responses), len(orderedTargets))
	}

	result := push.BatchResult{
		Results:      make([]push.TargetResult, 0, len(orderedTargets)),
		SuccessCount: response.SuccessCount,
		FailureCount: response.FailureCount,
	}
	for index, sendResponse := range response.Responses {
		targetResult := push.TargetResult{Target: orderedTargets[index]}
		if sendResponse.Success {
			targetResult.MessageID = sendResponse.MessageID
		} else {
			targetResult.Err = sendResponse.Error
			targetResult.Code = classifyError(sendResponse.Error)
		}
		result.Results = append(result.Results, targetResult)
	}
	return result, nil
}

func toMulticastMessage(message push.Message, now time.Time) (*messaging.MulticastMessage, error) {
	data, err := push.EnrichNotificationData(message.Data, message.Analytics)
	if err != nil {
		return nil, err
	}
	multicast := &messaging.MulticastMessage{
		Notification: &messaging.Notification{Title: message.Title, Body: message.Body},
		Data:         data,
	}
	if message.Analytics.Label != "" {
		multicast.FCMOptions = &messaging.FCMOptions{AnalyticsLabel: message.Analytics.Label}
	}
	if message.TTL > 0 || message.Android.Sound != "" || message.Android.ChannelID != "" ||
		message.Android.Icon != "" || message.Android.Color != "" || message.Android.CollapseKey != "" {
		android := &messaging.AndroidConfig{
			CollapseKey: message.Android.CollapseKey,
			Notification: &messaging.AndroidNotification{
				Sound:     message.Android.Sound,
				ChannelID: message.Android.ChannelID,
				Icon:      message.Android.Icon,
				Color:     message.Android.Color,
			},
		}
		if message.TTL > 0 {
			ttl := message.TTL
			android.TTL = &ttl
		}
		multicast.Android = android
	}
	if message.TTL > 0 || message.Apple.Sound != "" || message.Apple.Badge != nil || message.Apple.CollapseID != "" {
		multicast.APNS = &messaging.APNSConfig{
			Headers: map[string]string{},
			Payload: &messaging.APNSPayload{Aps: &messaging.Aps{
				Sound: message.Apple.Sound,
				Badge: message.Apple.Badge,
			}},
		}
		if message.TTL > 0 {
			multicast.APNS.Headers["apns-expiration"] = strconv.FormatInt(now.Add(message.TTL).Unix(), 10)
		}
		if message.Apple.CollapseID != "" {
			multicast.APNS.Headers["apns-collapse-id"] = message.Apple.CollapseID
		}
	}
	return multicast, nil
}

func classifyError(err error) push.ErrorCode {
	if err == nil {
		return push.ErrorNone
	}
	if messaging.IsUnregistered(err) {
		return push.ErrorUnregistered
	}
	if messaging.IsQuotaExceeded(err) {
		return push.ErrorRateLimited
	}
	if messaging.IsInternal(err) || messaging.IsUnavailable(err) {
		return push.ErrorTransient
	}
	if messaging.IsThirdPartyAuthError(err) {
		return push.ErrorUnknown
	}
	if messaging.IsInvalidArgument(err) || messaging.IsSenderIDMismatch(err) {
		return push.ErrorPermanent
	}
	return push.ErrorUnknown
}
