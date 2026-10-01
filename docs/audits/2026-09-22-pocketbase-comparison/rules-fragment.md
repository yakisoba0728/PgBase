# PocketBase rules comparison — 2026-09-22

Scope: seven preexisting `category=rules` rows plus six omissions. This is a source/test-assertion audit, not a claim that the complete rule language is conformant.

- PocketBase: local checkout `.local/pocketbase-comparison/pocketbase`, verified HEAD `5cec579da984436a258602a46a96302fbd31f77c` (v0.40.4).
- PgBase: `remaining-development/PgBase`, verified HEAD `6993744a8f36db48ddfeaa8dae017656752d24c7`, including the current worktree state.
- No production edits, tests, database operations, or Docker actions were performed. Existing recently run schema/authpolicy race tests were not reused as evidence for these contracts.
- `verification=test-exists` means the indicated test assertions were read; it does not mean tests were executed or that the gap has a dedicated regression test. Newly identified incompatibilities are mostly static source findings. A separate read-only agent inspected the pinned PB contracts while this agent inspected PgBase.

## Result

| ID | Status | Main conclusion |
| --- | --- | --- |
| RULE-01 | adapted | five CRUD actions |
| RULE-02 | partial | owner/authenticated/comparison |
| RULE-03 | partial | PocketBase filter syntax/variables |
| RULE-04 | partial | rule null/empty semantics |
| RULE-05 | partial | authRule |
| RULE-06 | partial | manageRule |
| RULE-07 | partial | rule administration/preview |
| RULE-08 | partial | rule denial HTTP status and result contract (omission) |
| RULE-09 | partial | actual request environment and auth projection (omission) |
| RULE-10 | adapted | privileged subject rule bypass (omission) |
| RULE-11 | partial | hidden fields in stored API rules (omission) |
| RULE-12 | partial | collection aliases and shared-row correlation (omission) |
| RULE-13 | partial | predicate NULL/empty and explicit LIKE wildcard semantics (omission) |

There are 2 adapted and 11 partial rows. No row is declared fully equivalent. The JSON retains the original seven identifiers and adds RULE-08 through RULE-13; these rows intentionally separate API surface, predicate meaning, request wiring, and HTTP results.

## Highest-value findings

1. **Request rules are not validated with a viable request environment.** `recordaccess.ValidateRules` supplies nil request for list/view/delete, while the PB compiler rejects request operands with nil request. Create/update have empty maps, so auth literals are unavailable. For example `@request.method = 'GET'` cannot pass the ordinary list-rule save path. The broad conformance fixture writes `collection_rules` directly, bypassing this validation. See [rules.go:86](../../../internal/recordaccess/rules.go), [pocketbase_resolver.go:434](../../../internal/accessrule/pocketbase_resolver.go), and [pocketbase_conformance_integration_test.go:84](../../../internal/recordaccess/pocketbase_conformance_integration_test.go). PB accepts request fields in its [resolver contract](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver.go#L97).

2. **AuthRule evaluates a different caller.** PB evaluates the authenticating record with the original HTTP request, then changes `requestInfo.Auth` only for response enrichment ([PB helper:53](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L53), [PB helper:94](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L94)). PgBase `authorizeSessionTx` always injects the authenticating target as the User principal and supplies no original HTTP request metadata (`internal/userauth/auth_rule_resolver.go:188`). Thus `@request.auth.id != ''` during unauthenticated password login has a different meaning. Existing PgBase tests carefully verify target-field denial, credential reuse, and rollback across password/OAuth/OTP/MFA/refresh, but not this caller-context contract.

3. **The scalar request context is incompatible.** PB uses `@request.context` values such as default/password/oauth2/realtime/protectedFile ([PB constants](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/event_request.go#L152)). PgBase implements `@request.context.<key>` with a map containing requestId, and real record HTTP populates only id/email/verified/created in Auth (`internal/api/record_handlers.go:37`). PB custom auth fields and relation references cannot be inferred from manually supplied integration-test maps.

4. **ManageRule is intentionally narrower.** PgBase explicitly requires the actor to be in the target auth collection; its test at `internal/userauth/auth_record_model_integration_test.go:239` expects cross-realm rejection. PB's [manager check](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L732) requires authentication and a nonempty matching rule but adds no same-auth-collection condition. PB also allows a permitted ordinary password update with oldPassword ([form:151](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/record_upsert.go#L151)); PgBase authorized record PATCH does not accept oldPassword and always requires manageRule for a password mutation (`internal/api/auth_records.go:245`, `internal/userauth/auth_records.go:144`). Separate password-change endpoints do not establish record PATCH parity.

5. **UI save and preview contracts do not match their backend.** The backend preview now supports supplied PB AST relations and request values; old claims that this is absent are stale. However, `RecordRulesEditor.preview` sends the complete five-action JSON draft as a single rule (`ui/admin/src/pages/record-rules-editor.tsx:47`), while `RulePreview.Rule` is one `accessrule.Rule`. Save sends `{rules, revision}` (`ui/admin/src/backend/http-admin-ports.ts:518`), while the native PUT body accepts only rules and uses a decoder with `DisallowUnknownFields` (`internal/api/rules.go:51`, `internal/api/admin_handlers.go:384`). UI tests use mocks and do not assert real endpoint body compatibility. These findings were not browser-tested.

6. **A valid PB stored rule can be rejected by the hidden-field restriction.** PB stored-rule resolvers allow hidden fields and only restrict client search filters afterward ([PB list resolver](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L64)); [PB regression test](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud_test.go#L262) hides title, uses it in listRule, and expects four matches. PgBase's PB catalog resolver rejects hidden non-relation fields unconditionally (`internal/accessrule/catalog_resolver.go:48`). Native AST scalar access is a separate path and does not repair PB rule import.

7. **Predicate meaning needs differential tests, not a syntax checklist.** PB normalizes ordinary non-JSON NULL/empty-string equality and uses NULL-aware inequality ([PB equality](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/filter.go#L334)); PgBase compiles strict IS NULL or COALESCE comparisons. PB preserves explicit percent wildcard placement ([PB wrapLikeParams](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/filter.go#L492), [PB fixture](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/filter_test.go#L314)); PgBase always wraps both sides in percent. A PB prefix rule such as `title ~ 'private%'` becomes substring matching in PgBase. Existing PgBase tests establish its own null/contains behavior, not equivalence to these fixtures.

8. **Collection aliases are missing; correlation is not established.** PB splits collection name/ID from alias and shares the corresponding join ([PB runner](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver_runner.go#L157), [PB join replacement](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver.go#L423), [PB alias test](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver_test.go#L353)). PgBase resolves an exact catalog name and emits per-comparison subqueries. Existing tests have a single @collection comparison. Explicit alias import failure is source-supported; the possible permissions effect of cross-row correlation requires a focused executable counterexample.

## Wire semantics and language bounds

PB distinguishes locked rules from false predicates: 403 for locked actions, 200 empty for a nonmatching list, 400 for create predicate failure, and 404 for failed view/update/delete predicates ([PB CRUD](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L52), [PB create](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L308)). PgBase lowers base null rules to deny AST, returns 403 for failed create predicate, and returns 401 for AuthRule failure. Its existing `pgbase/record_rules_integration_test.go` explicitly asserts create 403, view/update masking, and empty deny-list behavior.

The PB AST compiler implements substantial relation, JSON, any/each, function and date-macro functionality. Its limits and adaptations should not be described as full PB grammar: direct auth-ID literal equality is rejected by `internal/query/access_rule.go:146`; that parser uses `parseFilterRequest` without the double-quoted-literal adapter used by PB record queries; selected date macros omit PB numeric time components. PB accepts arbitrary auth-ID literals through its normal [request resolver](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver_runner.go#L199) and [generic token comparison](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/filter.go#L305).

## Files read

PgBase implementation:
`internal/accessrule/{rule.go,pocketbase.go,pocketbase_resolver.go,catalog_resolver.go}`;
`internal/query/{access_rule.go,filter.go,service.go}`;
`internal/recordaccess/{rules.go,rule_compile.go,rule_check.go,subject.go,access.go,preview.go}`;
`internal/recordops/access_context.go`;
`internal/userauth/{auth_rules.go,auth_rule_resolver.go,managed_authorization.go,auth_records.go,session_refresh.go,mfa.go,oauth.go,service.go}`;
`internal/api/{rules.go,record_handlers.go,user_handlers.go,auth_records.go,admin_handlers.go}`;
`ui/admin/src/pages/record-rules-editor.tsx`;
`ui/admin/src/backend/http-admin-ports.ts`.

PgBase tests read:
`internal/accessrule/{pocketbase_test.go,pocketbase_resolver_test.go}`;
`internal/query/{access_rule_test.go,pocketbase_rule_conformance_test.go}`;
`internal/recordaccess/{rules_validation_test.go,pocketbase_rule_integration_test.go,pocketbase_conformance_integration_test.go,preview_regression_integration_test.go}`;
`internal/userauth/{nullable_rules_integration_test.go,auth_rule_enforcement_integration_test.go,auth_record_model_integration_test.go}`;
`pgbase/record_rules_integration_test.go`;
`ui/admin/src/pages/record-rules-editor.test.tsx`.

Pinned PocketBase implementation/tests inspected locally by this agent or its read-only PB-contract subagent:
`apis/{record_crud.go,record_crud_test.go,record_helpers.go,record_helpers_test.go,record_auth_refresh.go,file.go}`;
`core/{collection_model_auth_options.go,collection_validate.go,event_request.go,record_query.go,record_field_resolver.go,record_field_resolver_runner.go,record_field_resolver_test.go}`;
`forms/record_upsert.go`;
`tools/search/{filter.go,filter_test.go,token_functions.go,identifier_macros.go,provider.go}`.
