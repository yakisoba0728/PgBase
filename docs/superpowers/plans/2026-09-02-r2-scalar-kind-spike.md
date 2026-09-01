# R2 scalar-kind spike decision

Status: rejected after a read-only production-code inventory.

The eight scalar field kinds currently participate in eight `FieldType`-sensitive switches spanning 277 production lines:

| Responsibility | Switches | Lines |
| --- | ---: | ---: |
| Schema default normalization | 1 | 60 |
| Schema widening and supported-kind checks | 2 | 14 |
| Query null and input decoding | 2 | 90 |
| Query scan destinations | 1 | 26 |
| Query filter decoding | 1 | 80 |
| Expand type support | 1 | 7 |
| Total | 8 | 277 |

The substantial branches are not duplicate dispatch. Numeric values preserve scale through text projection, JSONB treats JSON `null` as data and rejects filter/order operations, booleans restrict comparison operators, and timestamps have distinct normalization and offset rules. Scan destinations must also remain a Go destination-type switch.

The smallest encapsulated replacement needs two private registries: one in `schema` for defaults and widening, and one in `query` for decoding, scan factories, filter decoding, and capability flags. A shared registry would export query-private codec concepts across the package boundary. The two-registry design retains the existing behavior bodies and adds keys, callbacks, lookups, relation fallbacks, and error handling. Its best-case production estimate is approximately 305 lines, a net increase of 28 lines before comments and tests; fourteen semantic kind checks would still remain.

Physical DDL does not justify a registry: scalar `FieldType` constants already use PostgreSQL type spellings, with only the PostgreSQL catalog alias for `timestamptz` requiring translation. Ordering and keyset rendering are already largely kind-independent.

Decision: retain the local switches. The proposed descriptor fails the plan's net-negative replacement and deletion tests and would add indirection without deepening either module. Reconsider only when another scalar kind or a second production policy creates reusable behavior.
