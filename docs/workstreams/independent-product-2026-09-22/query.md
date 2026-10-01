# Hidden query operand protection

PgBase's hidden-field contract now applies to every caller-supplied filter operand, including the right-hand field, function arguments, JSON roots, and relation paths. This fixes PgBase's own information-disclosure boundary; it does not expand PocketBase syntax compatibility.

Changed: `internal/query/field_visibility.go`, the two list/count call sites in `expand.go`, existing visibility unit tests, and `hidden_operands_integration_test.go`.

## Reproduction and verification

The production `query.Service.List` path reproduced the bug against an isolated PostgreSQL fixture before the fix:

```text
docker exec -w /workspace pgbase-remaining-runner go test -tags=integration ./internal/query -run '^TestPublicQueryRejectsHiddenOperands$/^user$/^native_list$/^right_field$' -count=1 -p=2
FAIL: error = <nil>, want *ValidationError (0.174s)
```

The filter was `title = secret`, with `secret.Hidden=true` and an authenticated user. The test expects `filter/hidden_field`, not merely any failure.

After the fix:

```text
docker exec -w /workspace pgbase-remaining-runner go test -tags=integration ./internal/query -run '^TestPublicQueryRejectsHiddenOperands$' -count=1 -p=2
PASS (0.231s)

go test ./internal/query -run 'TestHidden|TestCompileRelationList|TestCompileRecordProjection|TestPocketBaseSpecialSortSQL|TestQueryServiceSurface' -count=1
PASS (0.728s)

docker exec -w /workspace pgbase-remaining-runner go test -race -tags=integration ./internal/query -run 'TestPublicQueryRejectsHiddenOperands|TestExpandedTargetRulesReceiveRequestContextAndFullCatalog|TestPocketBaseListPageCountFilterAndSnapshot|TestPocketBaseListSkipTotalAvoidsCountAndRandomSortRuns' -count=1 -p=2
PASS (1.905s)
```

The new integration test covers authenticated and anonymous callers through native list/count, compatible list, and offset pages with count enabled or skipped. Eleven filters exercise scalar RHS/modifiers, grouped comparisons, function arguments, JSON paths, and relation paths. These are real query-service and database paths, not mocked guard calls.

Stored administrator-defined authorization rules may still reference hidden fields: a `secret = 'allowed'` rule filters two fixture rows down to one. The caller cannot reuse that hidden field in their own filter. Hidden output remains omitted for non-administrators; supported administrator queries retain hidden access.

## Limits and integration handoff

At the hidden-field fix stage, some function/JSON query shapes failed elsewhere in query planning. The native function cursor defect was then reproduced and fixed as described below; JSON and unsupported relation-function shapes remain outside this change. Administrator positive controls in the hidden-field test cover its original executable query shapes.

The worker's full `go test ./internal/query -count=1` run reported only the structural runtime-line ceiling: 7,586 actual lines versus 7,565 allowed, a net increase of 21. The integration owner owns the measured ceiling update and final full-suite result. Production files remain under 800 lines; `field_visibility.go` is 164 lines. The historical audit matrix, manifest, and observation artifacts were preserved.

## Native function filters and signed cursors

The existing native parser and SQL compiler accepted `geoDistance` and `strftime`, but the V5 query fingerprint assumed every comparison had a scalar field. Function plans had none, so even the first request returned `invalid cursor` before executing its record query.

The new PostgreSQL regression uses an anonymous caller, visible fields, two matching records and one excluded record. Before the fix:

```text
docker exec -w /workspace pgbase-remaining-runner go test -tags=integration ./internal/query -run '^TestPublicFunctionFiltersContinueBoundCursor$' -count=1 -p=2
FAIL: strftime and geoDistance cursorless first page: err=invalid cursor (0.154s)
```

Functions now have an additive fingerprint descriptor containing the function name, ordered typed operands, stable field IDs and geographic properties, result type, operator, RHS value and LIKE mode. Existing scalar descriptor bytes, signature domain and V5 version are unchanged. An existing scalar golden hash and actual scalar cursor continuation tests still pass. Field names and generated SQL are not used as identities. Root numeric fields, root geographic coordinates and the already supported time arguments retain their existing SQL semantics; no relation-function or nested-function support was added.

The regression verifies first page `alpha`, continuation `beta`, terminal cursor absence, and rejection when the caller changes a field, geographic property, operand order/value, time format, operator or RHS. Unit checks cover stable field renaming, replacement field IDs/types and commutative groups. Hidden-field protection remains active before function planning.

Two direct edge cases were also reproduced before their fixes: unsupported function/field comparisons silently discarded the function, and invalid literal dates produced PostgreSQL errors 22007/22008 in list and count. The parser now rejects unsupported function placements; the planner rejects unsupported function operand/comparison modifiers. Only datetime errors from queries containing a literal `strftime` time argument become `filter/type_mismatch`; PostgreSQL retains timestamp interpretation and other errors retain their original classification.

```text
docker exec -w /workspace pgbase-remaining-runner go test -tags=integration ./internal/query -run 'TestFunctionComparisonsRejects?UnsupportedOperandShapes|TestPublicFunctionFiltersContinueBoundCursor/invalid_time_literal' -count=1 -p=2
FAIL: function/field comparisons accepted; invalid date list/count returned raw SQLSTATE 22007/22008 (0.167s)

docker exec -w /workspace pgbase-remaining-runner go test -race -tags=integration ./internal/query -run 'TestPublicFunctionFiltersContinueBoundCursor|TestCursorV5QueryFingerprint|TestCursorV5FunctionFingerprint|TestFunctionPlanRejects|TestFunctionComparisonsReject|TestPublicQueryRejectsHiddenOperands|TestPocketBaseGeoDistanceAndStrftimeCompile|TestListContinuesSignedV5ScalarPageWithinBoundMembership|TestListKeepsV5BindingAndAbsoluteExpiryAcrossContinuationPages' -count=1 -p=2
PASS (1.788s)
```

Function-specific production changes add 160 runtime lines across the new function plan, descriptor and error helpers plus their existing call sites. The integration owner owns final package/global budget measurements and full-suite verification.

Separate follow-up, static observation only: the existing scalar comparison fingerprint appears to omit field-to-field RHS metadata. This was not reproduced as cursor reuse in this work and was deliberately not changed: correcting a descriptor already used to issue cursors requires a separately reviewed invalidation/version policy. The function descriptor adds only a previously unissuable shape.

## Integration closure

After the function fixes, the integration owner reran all Go unit/race packages and the query/userauth PostgreSQL packages with the skip guard: all passed (query integration 90.893s, userauth 25.913s). The query runtime budget is the exact measured 7,746 lines; the full data group is 28,340. Native Windows structural checks include every new file through a temporary Git index. See the [final verification](README.md#최종-검증) for the complete stack and earlier eight-package integration run.
