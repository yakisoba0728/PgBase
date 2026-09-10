package pgbase

import "github.com/yakisoba0728/PgBase/internal/recordops"

// Mutation describes a record transition, including its stable event identity.
type Mutation = recordops.Mutation

// MutationHook validates transitions before transaction commit and observes
// them after commit. Before may run again on transaction retry and must avoid
// external side effects. After is best effort; use realtime/outbox for durable
// delivery. Hooks must honor their context and must not reenter record writes.
type MutationHook = recordops.MutationHook
