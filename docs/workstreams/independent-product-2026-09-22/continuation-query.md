# Field-comparison cursor binding

The previously static RHS concern is now reproduced against PostgreSQL and fixed. The public query service omitted the RHS field from a V5 query fingerprint: text field comparisons were encoded as an empty literal, while numeric field comparisons failed literal decoding. Literal-left contains and compatible LIKE semantics also shared the native contains fingerprint.

## Actual red and green

The regression drives anonymous access through the production query service. Its fixture contains two matching records and one excluded record. No mocked signer or query executor is involved.

```text
docker exec -w /workspace pgbase-remaining-runner go test -tags=integration ./internal/query -run '^TestPublicFieldComparisonsBindBothCursorOperands$' -count=1 -p=2
FAIL (0.173s):
  label = expected -> label = alternate: old cursor accepted
  label = expected -> label = expected:lower: old cursor accepted
  correctly signed legacy empty-literal fingerprint: accepted for field comparison
  amount >= minimum: first page returned validation failed
  label ~ 'match' -> 'match' ~ label: old cursor accepted
  native contains -> compatible LIKE mode: old cursor accepted
```

The earlier source investigation had already identified the omitted RHS and flags, so a broad hypothesis/instrumentation phase was unnecessary. The actual failing service test established the boundary before production edits.

```text
docker exec -w /workspace pgbase-remaining-runner go test -race -tags=integration ./internal/query -run 'TestPublicFieldComparisonsBindBothCursorOperands|TestCursorV5.*Fingerprint|TestCursorV5OrdinaryLiteralAndFunctionGoldenUnchanged|TestFieldComparisonPlanningRejectsUnboundOperands|TestPublicFunctionFiltersContinueBoundCursor|TestPublicQueryRejectsHiddenOperands|TestListContinuesSignedV5ScalarPageWithinBoundMembership|TestListKeepsV5BindingAndAbsoluteExpiryAcrossContinuationPages' -count=1 -p=2
PASS (2.285s, final run including the operator validation regression below)
```

The passing test verifies first page `alpha`, continuation `beta`, a terminal empty cursor, and rejection after either operand, modifier or operator changes. It verifies rejection of a signed legacy descriptor for the same field-comparison request. Existing function pagination, hidden operand protection, V5 membership/expiry continuation and literal/function golden hashes also pass.

Independent review also identified incomplete SQL operand validation. The new numeric binding exposed unsupported numeric/boolean contains operators; `:length` could produce integer-vs-text comparisons, and `?`-prefixed field operators were emitted as nonexistent PostgreSQL operators. These were reproduced through the actual service before their fix:

```text
docker exec -w /workspace pgbase-remaining-runner go test -tags=integration ./internal/query -run '^TestPublicFieldComparisonsBindBothCursorOperands$/invalid_field_operators' -count=1 -p=2
FAIL (0.167s): List and Count returned SQLSTATE 42883 for numeric/boolean contains, mismatched length operands and unsupported ?-prefixed field operators.
```

The final passing run checks nine invalid combinations return filter validation errors and nineteen supported expressions execute correctly in both list and count. Positive coverage includes the six ordinary comparison operators for numbers and text, text contains/not-contains, lower, compatible length operands and boolean equality/inequality.

## Contract and implementation

`cursor_v5_fields.go` adds a distinct field-comparison descriptor with both stable field paths, source types, modifiers and external-identity collection IDs, plus the comparison operator. Neither physical column names nor mutable logical names identify operands. Unit regressions cover field replacement/type changes and name changes retaining the same stable IDs.

`cursor_v5_fingerprint.go` adds literal-left and compatible LIKE flags only when they affect literal contains semantics. Ordinary literal descriptors retain their byte representation through omitted false flags. Function descriptors, the keyed signature domain and cursor wire version remain unchanged. New golden tests capture the pre-change native literal and function digests; the existing relation-literal golden remains unchanged.

`field_comparison_plan.go` validates supported root field comparisons before cursor binding, with the same validator used by SQL compilation. It allows the six ordinary operators and text contains/not-contains, checks source types and effective types after modifiers, and restricts booleans to equality/inequality. Unsupported `?`-prefixed field operators are rejected rather than emitted into SQL. Unsupported relation-path/field combinations fail closed rather than dropping the RHS. No new syntax, relation comparison capability or PocketBase parity was introduced. Authorization and hidden-field checks still run before planning.

Production changes in this continuation add 129 runtime lines. The existing relation planner stays at 796 lines after moving RHS planning and validation into its own 75-line helper. Root owns final package/global budget measurements and complete-suite verification.

## Upgrade and invalidation

Existing field-to-field cursors and affected literal-left/compatible-LIKE cursors fail with `invalid_cursor` when resumed against their original query after this update. Clients must discard that cursor and restart the traversal. There is no fallback to the ambiguous legacy field-comparison hash. Ordinary literal and function cursors retain their previous binding.

Deploy the fix across every serving instance and drain old instances before treating the corrected contract as enforced. A mixed fleet can still route requests to an old node that accepts the ambiguous binding. No database migration or new cursor wire version is required.

Old text field-comparison hashes were byte-identical to legitimate empty-literal query hashes, so their provenance cannot be recovered from an old token. Preserving ordinary literal cursors necessarily prevents global identification of every historically ambiguous token. The guarantee is strict matching against the new field-comparison descriptor; invalidating every pre-update cursor would require a separate coordinated signing-key rotation and would also invalidate ordinary cursors.

The existing parser's treatment of literal-left `?`-prefixed operators is a separate syntax-semantics limitation identified during read-only review. This field-comparison fix neither broadens those operators nor claims to correct their literal-side normalization.
