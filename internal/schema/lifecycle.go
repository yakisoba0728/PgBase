package schema

import (
	"context"

	"github.com/google/uuid"
)

// CollectionMutationEvent describes one claimed schema mutation. Before hooks
// may veto it but must not perform external side effects: a caller retry uses a
// new operation claim. Result is populated only after the commit outcome is
// known; replays of an already completed operation don't emit another event.
type CollectionMutationEvent struct {
	Operation string
	Name      string
	ActorID   uuid.UUID
	Result    ApplyResult
}

// CollectionMutationLifecycle pairs one immutable hook snapshot with the
// mutation's final commit outcome.
type CollectionMutationLifecycle interface {
	BeginCollectionMutation(context.Context, *CollectionMutationEvent) (finish func(error), err error)
}

func (s *Service) SetCollectionMutationLifecycle(hooks CollectionMutationLifecycle) {
	if s != nil {
		s.collectionLifecycle = hooks
	}
}

func collectionMutationEvent(intent ApplyIntent) *CollectionMutationEvent {
	event := &CollectionMutationEvent{ActorID: intent.actor}
	switch intent.change.kind {
	case changeCreate:
		event.Operation, event.Name = "create", intent.change.create.Name
	case changeUpdate:
		event.Operation, event.Name = "update", intent.change.update.Name
	case changeDelete:
		event.Operation, event.Name = "delete", intent.change.deleteName
	}
	return event
}
