# Literal-left any-match operator normalization

The remaining query grammar defect is fixed at the operand-normalization boundary. When a literal appears to the left of a `?`-prefixed comparison, the parser now preserves the `?` modifier while reversing the base ordering operator. For example, `2 ?< large_count` is planned as `large_count ?> 2`. Literal-left `?~` and `?!~` now retain the literal-left containment direction. Equality and inequality remain symmetric.

Scalar predicates used by metadata lists normalize the `?` modifier after parsing because both operands are single values there. This keeps the corrected literal-left direction while giving `?=`, `?!=`, `?~`, `?!~`, `?<`, `?<=`, `?>` and `?>=` the existing scalar comparison semantics. Literal-left scalar contains now also evaluates the literal as the haystack, matching the SQL path.

## Red and green evidence

The focused parser/compiler regression was added before the production change and failed deterministically:

```text
go test ./internal/query -run '^TestPocketBaseLiteralLeftAnyMatchOperatorsPreserveOperandSemantics$' -count=1
FAIL: ?<, ?<=, ?> and ?>= were not reversed; ?~ and ?!~ lost literalLeft.
```

After the fix, the same test passes and asserts exact parameterized SQL for all eight `?` operators. The PostgreSQL service regression executes both List and Count for all eight literal-left forms against three real records and checks the returned cardinalities:

```text
docker exec -w /workspace pgbase-remaining-runner go test -tags=integration -race ./internal/query -run '^TestPublicFieldComparisonsBindBothCursorOperands$/literal_left_any_match_operators' -count=1 -p=2
ok github.com/yakisoba0728/PgBase/internal/query 1.213s
```

An independent review found that preserving `?` exposed the shared scalar evaluator, whose switches only accept base operators. The scalar regression failed before its fix for field-left, field-to-field and literal-left inputs, including contains, and now passes:

```text
go test ./internal/query -run 'TestScalarAnyMatchOperatorsNormalizeForSingleValueOperands|TestCompileScalarPredicateUsesSharedBoundedGrammar|TestScalarTimeComparisonsPreserveNanosecondPrecision|TestPocketBaseLiteralLeftAnyMatchOperatorsPreserveOperandSemantics' -count=1
ok github.com/yakisoba0728/PgBase/internal/query 0.674s
```

The focused compatibility and cursor-preservation selection also passes:

```text
docker exec -w /workspace pgbase-remaining-runner go test -tags=integration -race ./internal/query -run 'TestPocketBaseLiteralLeftAnyMatchOperatorsPreserveOperandSemantics|TestPocketBaseAnyMatchOperatorsAreBoundedAndParameterized|TestPublicFieldComparisonsBindBothCursorOperands|TestCursorV5OrdinaryLiteralAndFunctionGoldenUnchanged|TestPublicFunctionFiltersContinueBoundCursor|TestPublicQueryRejectsHiddenOperands' -count=1 -p=2
ok github.com/yakisoba0728/PgBase/internal/query 1.758s
```

## Contract and limits

The change only applies when the parser swaps a literal-left comparison into the field-left internal form. Field-to-field comparisons do not use this path, so their binding, validation and cursor descriptor remain unchanged. The SQL compiler still strips `?` only after parsing has normalized direction; scalar operands retain the existing bounded any-match behavior. Ordinary literal and function cursor golden fingerprints remain unchanged, and hidden-field policy still runs through the existing service planner.

No grammar syntax, relation traversal, wildcard rule, cursor wire version or independent-product policy changed. Existing cursors for affected literal-left `?` filters are rejected after deployment because the corrected normalized operator or `literalLeft` flag changes the query fingerprint; clients must restart those traversals. Ordinary literal, function and field-to-field cursor bindings remain stable. The production change is five net runtime lines (`internal/query/filter.go`: 11 additions, 6 deletions). Root owns the final package and repository budget measurements.
