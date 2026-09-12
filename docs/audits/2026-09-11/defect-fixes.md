# 2026-09-11 결함 수정 및 재검증

이 문서는 고정 감사 기준 `0ea07ff91dabf49d0f0e5f4c55aff53ed7ea291b`에서 확인한 제품 결함 20건의 **후속 수정 기록**이다. 원래 판정·증거·우선순위는 [감사 보고서](../../codebase-audit-2026-09-11.md)와 [원본 JSON](findings.json)에 보존하며, 이 문서는 이를 재분류하거나 232행 기능 수용표를 바꾸지 않는다. 수정은 `d05452c293764287f8c548dc892414548a74d11e`를 기준으로 만들었고, 제품·회귀 테스트·generated artifact 74파일은 `275d2e4e01211f8817c17ceaab84959db2843af5`에 기록됐다.

## 현재 검증 상태

| 항목 | 상태 |
| --- | --- |
| 기준 | 감사 기준 `0ea07ff`; 수정 기준 `d05452c2`; 수정 source commit `275d2e4e01211f8817c17ceaab84959db2843af5` |
| DB/외부 의존성 | OAuth·SMTP·S3 실서비스는 사용하지 않고, 격리 PostgreSQL·MinIO·가짜 provider·mailer로 검증 |
| migration | `000064_versioned_extension_objects.sql`, `000065_optional_auth_email_uniqueness.sql`를 추가하고 embedded manifest/checksum을 갱신 |
| 광범위 검증 | `go vet ./...`, `go vet -tags=integration ./...`, `scripts/test-go-unit.sh -count=1`는 exit 0으로 통과했다. disposable PostgreSQL+MinIO full integration은 기능 package가 모두 통과했고, 초기 runner exit 1은 당시 old structural-budget assertion만의 결과였다. Luna의 최종 budget policy unit/integration도 exit 0으로 통과했다. |

## 20개 제품 finding

| ID | 구체 수정 | 재현·검증 근거 | 상태와 한계 |
| --- | --- | --- | --- |
| SFM-001 | snapshot에 compatibility/type/system/validation/field system 메타데이터를 presence-aware로 보존 | PostgreSQL `TestMigrationSnapshotReplayPreservesMetadataAndAdvancedIndexes`, `TestLegacyMigrationSnapshotReplayInheritsAbsentMetadata` | 수정·실행 검증. 구 legacy snapshot은 없는 값만 기존 값으로 상속 |
| SFM-002 | advanced index의 keys/where/raw SQL을 논리 정의 그대로 export/replay | 위 PostgreSQL replay와 `TestMigrationSnapshotIndexPreservesLogicalDefinition` | 수정·실행 검증 |
| SFM-003 | native/compatibility 이름의 case-fold 공통 namespace와 ambiguous lookup fail-closed | `TestCollectionLookupNamesShareOneCaseFoldedNamespace`, `TestResolveCollectionIdentifierRejectsAmbiguousCatalogDeterministically` | 수정·실행 검증. DB cross-column union constraint는 없음 |
| RQR-001 | PocketBase relation rule 저장에 catalog resolver/compiler 사용 | `TestRuleServiceSetAcceptsPocketBaseRelationRule` (격리 PostgreSQL) | 수정·실행 검증 |
| RQR-002 | reverse single/multiple relation correlation 분기 | `TestCompileRelationListReverseRelationsUseIncomingCorrelation`, `TestListRelationAwareFiltersAndSortsWithPrivateStableCursor` | 수정·실행 검증 |
| RQR-003 | truncate relation edge count schema를 `pgbase_relation`로 고정 | `TestTruncateCountsMultipleRelationsInRelationSchema` (격리 PostgreSQL) | 수정·실행 검증 |
| AUTHMAIL-001 | 빈 email은 partial unique index 대상에서 제외하고 realm/provider/subject identity uniqueness 유지 | `TestOAuthDistinctSubjectsWithoutEmailRemainIndependent`; nonempty 등록·email-change conflict 회귀 | 수정·실행 검증. no-email 계정은 email recovery/OTP 불가 |
| AUTHMAIL-002 | policy 저장과 collection schema update 모두에서 visible text identity field 검증 | `TestAuthPolicyIdentityFieldsRequireVisibleDeclaredTextFields`, `TestAuthCollectionUpdatePreservesPublishedIdentityFieldContract` | 수정·실행 검증. custom identity의 물리 unique index는 추가하지 않음 |
| EXT-AUD-001 | branded ApiError만 status/data를 HTTP 경계로 전달 | `TestPocketBaseAPIErrorExecutionCarriesOnlyBrandedPublicErrors`, `TestJavaScriptRoutePreservesPublicApiErrors` | 수정·독립 재검증 통과 |
| EXT-AUD-002 | supplied OTP id/password·alert info를 템플릿/메일 hook metadata로 전달하고 OTP 재발급 제거 | `TestAuthMailDispatcherChecksRealmAndDispatchesEveryKind`, `TestAdminRecordMailRendersSuppliedPayloadWithoutMintingOTP` | 수정·실행 검증. PocketBase `sentTo` column은 PgBase에 없음 |
| EXT-AUD-003 | transaction-bound app facade와 nested reuse | `TestPocketBaseRunInTransactionPassesAppFacadeAndNests`, `TestPocketBaseRunInTransactionUsesInvocationBoundPostgresTransaction` | 수정·독립 PostgreSQL 재검증 통과; model+SQL commit/rollback 함께 확인 |
| EXT-AUD-004 | final transaction outcome용 optional MutationErrorHook와 record model error phase | `TestRecordLifecyclePublishesFailedTransactionOutcome`, `TestRecordHooksFollowExternalTransactionOutcomeExactlyOnce` | 수정·실행 검증 |
| EXT-AUD-005 | immutable object physical version, DB metadata/deletion queue 원자화, rollback cleanup | `TestExtensionObjectReplacementPublicationFailurePreservesCommittedBytes`, `TestExtensionObjectExternalTransactionCommitAndRollbackFinalizePhysicalVersions` | 수정·race/integration 검증. ambiguous transaction outcome은 보수적으로 retention |
| EXT-AUD-006 | 모든 public object write를 단일 `object.write` hook boundary로 통합 | `TestExtensionObjectWriteEntryPointsRunOneVetoHook` | 수정·실행 검증 |
| EXT-AUD-007 | prefix `LIKE`를 literal left comparison으로 교체 | `TestExtensionObjectPrefixIsLiteralAcrossPaginationAndDeletePrefix` | 수정·실행 검증 |
| EXT-AUD-008 | JS declaration에 설치 surface, byte/API/mail/transaction 타입과 51 hook alias/base global을 보강하고 generated output 동기화 | `TestSupportedHookGlobalsAreDeclared`, `TestJavaScriptTypeDeclarationsCoverInstalledAppInventory`, artifact sync, strict TypeScript compile | 수정·독립 재검증 통과. 실제 payload의 requestID/recordID/eventID/actorID 및 numeric version shape도 확인 |
| OPS-F-001 | runtime setting snapshot의 rate-limit slices를 모든 publication 경계에서 deep copy | `TestUpdatePublicationOwnsRateLimitSlicesAcrossInputsSnapshotsAndListeners`, `TestGetRateLimitSnapshotsAreSafeForConcurrentCallerMutation` with `-race` | 수정·실행 검증 |
| OPS-F-002 | RestoreNew 직후 failed activation cleanup 소유권 등록 | `TestManagedRestoreDiscardsProvisionedTargetWhenOpeningFails` | 수정·실행 검증 |
| SDK-AUDIT-001 | TS realtime terminal/EOF retry budget, unsubscribe readiness, stale client ID 처리 | local fake SSE `realtime-terminal.test.mjs`, `cd sdk/typescript && npm test` | 수정·실행 검증 |
| SDK-AUDIT-003 | Dart realm browser OAuth start를 named realm route로 변경 | Dart `oauthStartAndOpen`/`oauthWithBrowser` path·callback-close tests, `dart test`, `dart analyze` | 수정·실행 검증. analyze의 기존 informational diagnostic 1건 유지 |

## 호환성 영향

| 영역 | 수정된 계약 |
| --- | --- |
| migrations | v64는 extension object immutable physical version을, v65는 nonempty auth email partial uniqueness를 도입한다. 기존 migration checksum은 변경하지 않았다. |
| transaction/retry | record error lifecycle은 실제 commit/rollback 후 한 번만 관찰한다. extension object와 record 작업은 외부 transaction outcome을 따른다. |
| API/extension/types | branded ApiError HTTP response, `$app.runInTransaction` facade, OTP/auth alert metadata, JS declaration과 generated TypeScript output이 변경된다. |
| identity | OAuth no-email subject를 같은 realm에서 여러 개 지원하고 nonempty email uniqueness·provider subject uniqueness를 유지한다. |

## 추적 가능한 source·test 근거

| 범위 | 구현 source | 회귀 test |
| --- | --- | --- |
| schema | [migration snapshot](../../../internal/schema/migration_snapshot.go), [schema service](../../../internal/schema/service.go) | [snapshot regression](../../../internal/schema/migration_snapshot_regression_integration_test.go) |
| records/rules | [rule persistence](../../../internal/recordaccess/rules.go), [truncate](../../../internal/recordops/truncate.go) | [PocketBase rule test](../../../internal/recordaccess/pocketbase_rule_integration_test.go), [truncate test](../../../internal/recordops/truncate_integration_test.go) |
| auth/mail | [OAuth](../../../internal/userauth/oauth.go), [auth policy](../../../internal/userauth/auth_admin.go), [mail bridge](../../../pgbase/extension_bridge.go) | [OAuth no-email](../../../internal/userauth/oauth_empty_email_integration_test.go), [identity policy](../../../internal/userauth/identity_policy_schema_integration_test.go), [mail action](../../../internal/userauth/email_actions_integration_test.go) |
| extensions/runtime | [object service](../../../pgbase/extension_objects.go), [runtime](../../../internal/jsvm/runtime.go), [declarations](../../../pgbase/javascript_types.go) | [object integration](../../../pgbase/extension_objects_integration_test.go), [runtime declarations](../../../pgbase/javascript_types_parity_test.go), [hook inventory](../../../internal/jsvm/hook_declarations_test.go) |
| operations/SDK | [runtime settings](../../../internal/runtimesettings/settings.go), [managed restore](../../../pgbase/managed.go), [TypeScript SDK](../../../sdk/typescript/src/index.ts), [Dart SDK](../../../sdk/dart/lib/src/client.dart) | [settings](../../../internal/runtimesettings/settings_test.go), [managed restore](../../../pgbase/managed_restore_test.go), [TS realtime](../../../sdk/typescript/test/realtime-terminal.test.mjs), [Dart tests](../../../sdk/dart/test/sdk_test.dart) |

## 원본 감사와의 관계

`AUTHMAIL-003`, `SDK-AUDIT-002`는 원본에서 intentional difference였고, `DOC-001`, `DOC-RT-001`은 문서 finding이며 이 20개 제품 수정 ledger에 넣지 않았다. UI-001은 RQR-001의 impact alias로 중복 집계하지 않는다. 상세 소유자 기록은 tracked source/test 경로와 이 표의 test 이름을 우선 근거로 하며, 비추적 `.local` 로그는 출처 링크로 사용하지 않는다.

외부 OAuth·SMTP·production, browser E2E, release image는 이 bounded backend 검증 범위 밖이다. Dart는 30개 test를 통과했고 기존 `avoid_dynamic_calls` informational diagnostic 1건은 유지된다. TypeScript 전체 test는 통과했다.
