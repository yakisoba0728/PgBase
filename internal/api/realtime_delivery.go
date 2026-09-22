package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/realtime"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/recordops"
)

const maximumRealtimeDeliveryMessages = 100

type realtimePendingMessage struct {
	subscription   realtimeSubscription
	event          realtime.Event
	relationFilter bool
}

// deliveryMu serializes these operations with subscription/auth replacement.
func (h *realtimeHub) deliverApplicationMessage(w http.ResponseWriter, r *http.Request, control *http.ResponseController, client *realtimeClient, message ApplicationRealtimeMessage) error {
	if !h.revalidate(r.Context(), client) {
		return recordaccess.ErrDenied
	}
	client.mu.RLock()
	subscribed := false
	for _, subscription := range client.subscriptions {
		if subscription.raw == message.Topic {
			subscribed = true
			break
		}
	}
	client.mu.RUnlock()
	// A queued message can outlive an unsubscribe or credential rotation.
	if !subscribed || !h.authorizeApplicationMessage(r.Context(), client.id, message) {
		return nil
	}
	if err := control.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", message.Topic, message.Data); err != nil {
		return err
	}
	return control.Flush()
}

func (h *realtimeHub) deliverRecordMessages(w http.ResponseWriter, r *http.Request, control *http.ResponseController, client *realtimeClient) error {
	if !h.revalidate(r.Context(), client) {
		return recordaccess.ErrDenied
	}
	client.mu.RLock()
	subscriptions := append([]realtimeSubscription(nil), client.subscriptions...)
	principal := client.principal
	client.mu.RUnlock()
	messages, checkpoint, through, err := h.collectRealtimeMessages(r, principal, subscriptions)
	if err != nil {
		writeRealtimeStreamError(w, control, err)
		return err
	}
	if !h.revalidate(r.Context(), client) {
		return recordaccess.ErrDenied
	}
	for index, message := range messages {
		record, include, err := h.realtimeMessageRecord(r, principal, message)
		if err != nil {
			return err
		}
		// Only the last matching topic can acknowledge this sequence. If the
		// connection fails between overlapping topics, replay may repeat the
		// first match but must still deliver the remaining matches.
		nextCheckpoint := checkpoint
		if index+1 == len(messages) || messages[index+1].event.Sequence != message.event.Sequence {
			nextCheckpoint = message.event.Sequence
		}
		if include {
			event := message.event
			lifecycle := &RealtimeLifecycleEvent{UserID: principal.ID, Principal: principal, ClientID: client.id, Collection: event.Collection, Event: &event}
			finish, err := beginRealtime(h.dependencies.RealtimeLifecycle, r.Context(), "message", lifecycle)
			if err != nil {
				finish(err)
				return err
			}
			if !h.revalidate(r.Context(), client) {
				finish(recordaccess.ErrDenied)
				return recordaccess.ErrDenied
			}
			payload, err := json.Marshal(map[string]any{"action": event.Action, "record": record})
			if err == nil {
				err = control.SetWriteDeadline(time.Now().Add(10 * time.Second))
			}
			if err == nil {
				_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", nextCheckpoint, message.subscription.raw, payload)
			}
			finish(err)
			if err != nil {
				return err
			}
		}
		checkpoint = nextCheckpoint
	}
	if err := control.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		return err
	}
	if through != nil {
		if _, err := fmt.Fprintf(w, "id: %d\nevent: cursor\ndata: {}\n\n", *through); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
			return err
		}
	}
	if err := control.Flush(); err != nil {
		return err
	}
	if through != nil {
		client.mu.Lock()
		for index := range client.subscriptions {
			sub := &client.subscriptions[index]
			if !sub.application && (sub.cursor == nil || *sub.cursor < *through) {
				sub.cursor = cloneRealtimeCursor(through)
			}
		}
		client.mu.Unlock()
	}
	return nil
}

// Every reader returns the last sequence it completely inspected. The minimum
// of those watermarks is the only safe shared resume cursor. Pages above it
// remain unread on the next poll; sorting one page per topic alone is unsafe.
func (h *realtimeHub) collectRealtimeMessages(r *http.Request, principal identity.Subject, subscriptions []realtimeSubscription) ([]realtimePendingMessage, int64, *int64, error) {
	reader, ok := h.dependencies.Realtime.(realtimeV2)
	if !ok {
		return nil, 0, nil, errors.New("realtime reader unavailable")
	}
	var messages []realtimePendingMessage
	var through, checkpoint *int64
	for _, sub := range subscriptions {
		if sub.application {
			continue
		}
		events, next, err := reader.ReadFor(r.Context(), realtime.ReadRequest{Principal: principal, Collection: sub.collection, PublicID: sub.recordID, Filter: sub.filter, Fields: sub.fields, After: sub.cursor, RuleContext: ruleRequestContext(r)})
		relationFilter := errors.Is(err, realtime.ErrRelationFilterRequiresCurrentRecord)
		if relationFilter {
			events, next, err = reader.ReadFor(r.Context(), realtime.ReadRequest{Principal: principal, Collection: sub.collection, PublicID: sub.recordID, After: sub.cursor, RuleContext: ruleRequestContext(r)})
		}
		if err != nil {
			return nil, 0, nil, err
		}
		if through == nil || next < *through {
			through = cloneRealtimeCursor(&next)
		}
		start := next
		if sub.cursor != nil {
			start = *sub.cursor
		}
		if checkpoint == nil || start < *checkpoint {
			checkpoint = cloneRealtimeCursor(&start)
		}
		for _, event := range events {
			if event.Sequence <= *through {
				messages = append(messages, realtimePendingMessage{sub, event, relationFilter})
			}
		}
		sort.SliceStable(messages, func(i, j int) bool { return messages[i].event.Sequence < messages[j].event.Sequence })
		// Retain a bounded number of messages plus all topics matching the last
		// selected sequence (at most the 1000 admitted subscriptions). Trimming
		// a sequence's matches would acknowledge only part of its fan-out.
		end := min(len(messages), maximumRealtimeDeliveryMessages)
		for end < len(messages) && messages[end].event.Sequence == messages[end-1].event.Sequence {
			end++
		}
		if end < len(messages) {
			last := messages[end-1].event.Sequence
			if last < *through {
				*through = last
			}
			clear(messages[end:])
			messages = messages[:end]
		}
	}
	if through == nil {
		return nil, 0, nil, nil
	}
	end := len(messages)
	for end > 0 && messages[end-1].event.Sequence > *through {
		end--
	}
	clear(messages[end:])
	return messages[:end], *checkpoint, through, nil
}

func (h *realtimeHub) realtimeMessageRecord(r *http.Request, principal identity.Subject, message realtimePendingMessage) (map[string]any, bool, error) {
	sub, event := message.subscription, message.event
	if message.relationFilter {
		if event.Operation == "delete" || h.dependencies.Records == nil {
			return nil, false, nil
		}
		page, err := h.dependencies.Records.List(r.Context(), recordops.ListRequest{Principal: principal, RequestID: requestID(r.Context()), Collection: event.Collection, Limit: 1, Filter: "(" + sub.filter + ") && id = '" + event.PublicID + "'", RuleContext: ruleRequestContext(r)})
		if err != nil || len(page.Items) == 0 {
			return nil, false, nil
		}
	}
	record := make(map[string]any, len(event.Record)+1)
	for key, value := range event.Record {
		record[key] = value
	}
	if sub.expand != "" && event.Operation != "delete" && h.dependencies.Records != nil {
		current, err := h.dependencies.Records.Get(r.Context(), recordops.GetRequest{Principal: principal, RequestID: requestID(r.Context()), Collection: event.Collection, PublicID: event.PublicID, Expand: sub.expand, Fields: sub.fields})
		if err == nil {
			if expanded, ok := current.Data["expand"]; ok {
				record["expand"] = expanded
			}
		}
	}
	if sub.fields != "" {
		projection, err := query.CompileProjection(sub.fields)
		if err != nil {
			return nil, false, err
		}
		projected, ok := projection.Apply(record).(map[string]any)
		if !ok {
			return nil, false, errors.New("invalid realtime projection")
		}
		record = projected
	}
	return record, true, nil
}

func writeRealtimeStreamError(w http.ResponseWriter, control *http.ResponseController, err error) {
	code := "subscription_failed"
	if errors.Is(err, realtime.ErrCursorExpired) {
		code = "cursor_expired"
	}
	if errors.Is(err, realtime.ErrCursorInvalid) {
		code = "invalid_cursor"
	}
	if control.SetWriteDeadline(time.Now().Add(10*time.Second)) != nil {
		return
	}
	payload, _ := json.Marshal(map[string]string{"code": code})
	_, _ = fmt.Fprintf(w, "event: PB_ERROR\ndata: %s\n\n", payload)
	_ = control.Flush()
}
