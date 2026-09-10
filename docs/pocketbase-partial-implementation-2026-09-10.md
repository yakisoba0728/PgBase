# 부분 구현 68개 수용 조건 ledger — 2026-09-10 갱신

PocketBase 대조 원본은 [공식 v0.40.3 pinned commit](https://github.com/pocketbase/pocketbase/tree/5684ee24f1e88f71a2ede73d20cf547fbd509e16) `5684ee24f1e88f71a2ede73d20cf547fbd509e16`이다.

기준선 `a7b8ac0`; 현재 코드 기준 `c0e5b0da1876`. `done`은 구현 commit과 회귀 테스트 파일이 표에 기록된 경우다. 이번 최종 ledger에는 `pending_verification` 행이 없다.

| ID | 기능 | 상태 | 구현 commit | 테스트 근거 | 남은 PB wire/native PostgreSQL 차이 |
| --- | --- | --- | --- | --- | --- |
| SCH-02 | auth collection data model | done | post-a7b8ac0:51ec51d,16e6eca,7a77e69,e970a2b,4b41d09 | internal/userauth/auth_record_model_integration_test.go | PB collection JSON and PgBase realm route/storage remain different. |
| SCH-03 | catalog list and single lookup | done | post-a7b8ac0:f36d11a,de20469,b35eeb5 | internal/api/schema_handlers_test.go | PB의 전체 collection wire와 native catalog의 UUID/DDL receipt는 같지 않다. |
| SCH-04 | name, system and type invariants | done | post-a7b8ac0:619d460,62b9620,993f70b,c8bb322 | internal/schema/compatibility_integration_test.go | native PostgreSQL table identity와 PB collection id/이름 범위는 별도이며 compat mode에서만 PB 제약을 강제한다. |
| SCH-10 | common field metadata and system protection | done | post-a7b8ac0:619d460,62b9620 | internal/schema/compatibility_integration_test.go | PgBase는 physical ColumnName을 별도로 보존한다. |
| SCH-11 | required blank and zero-value semantics | done | post-a7b8ac0:62b9620 | internal/query/codec_test.go | native PostgreSQL mode는 기존 nullable/zero semantics를 유지한다. |
| SCH-16 | scalar field rename then drop/name reuse | done | post-a7b8ac0:31e0bad,4b2e8e4 | internal/schema/field_contract_integration_test.go | PB SQLite DDL과 달리 PostgreSQL physical column identity를 유지한다. |
| SCH-17 | renamed multi-relation collection delete | done | post-a7b8ac0:31e0bad,619d460 | internal/schema/relation_planner_test.go | PB relation table naming과 PostgreSQL schema namespace는 다르다. |
| SCH-20 | dry-run view sample | done | post-a7b8ac0:62b9620 | internal/api/schema_operations_live_test.go | PB SQLite query planner가 아니라 PostgreSQL SQL view를 실행한다. |
| SCH-21 | view update and dependency guards | done | post-a7b8ac0:2a76863,62b9620 | internal/api/schema_operations_live_test.go | PgBase view는 PostgreSQL SQL view이며 PB collection view storage와 다르다. |
| FLD-07 | date field min/max | done | post-a7b8ac0:62b9620 | internal/schema/compatibility_integration_test.go | timestamp 저장은 PostgreSQL timestamptz다. |
| FLD-09 | JSON field required and maxSize | done | post-a7b8ac0:62b9620 | internal/schema/compatibility_integration_test.go | JSONB physical representation과 PB JSON wire validation 일부가 다르다. |
| FLD-11 | per-field protected file toggle | done | post-a7b8ac0:08e13d0,429df00,5b6e0cf,7c0ff15,1294a11 | pgbase/file_integration_test.go | PB file token URL grammar 대신 PgBase signed/authorized delivery route를 쓴다. |
| REC-04 | 레코드 부분 갱신과 빈 PATCH no-op | done | post-a7b8ac0:1dc7e2b | internal/recordops/batch_integration_test.go | native mutation receipt와 PB record envelope는 다르다. |
| REC-07 | 레코드 JSON 메타데이터와 가시성 | done | post-a7b8ac0:7a522b2 | internal/responseprojection/record_test.go | 기본 native response는 UUID/cursor/PG timestamp contract를 유지한다. |
| REC-10 | PUT upsert와 조건부 갱신 | done | post-a7b8ac0:1dc7e2b | internal/recordops/batch_integration_test.go | PB batch endpoint wire와 PgBase receipt/ETag surface는 다르다. |
| REC-12 | batch 한도·timeout 설정 | done | post-a7b8ac0:53eb6a1,63aae01 | pgbase/runtime_settings_http_integration_test.go | 설정 API는 PB 단일 settings document와 다르다. |
| QRY-03 | contains와 not-contains | done | post-a7b8ac0:c0dbb0f,de20469,9946193 | internal/query/pocketbase_compat_live_test.go | compat mode에서만 PB expression semantics를 쓰며 native filter contract는 보존한다. |
| QRY-05 | 피연산자 배치와 field-to-field 비교 | done | post-a7b8ac0:8cd2f8d,c0dbb0f,9946193 | internal/query/service_surface_test.go | 지원 type/path는 PostgreSQL schema field typing에 제한된다. |
| QRY-10 | 필드 modifier lower·length·each·isset·changed | done | post-a7b8ac0:14da805 | internal/accessrule/pocketbase_test.go | native rule syntax/storage remains PostgreSQL-specific. |
| QRY-14 | relation sort | done | post-a7b8ac0:84f279e,9946193 | internal/query/relation_filter_sort_test.go | 100,000 root-row safety cap은 PB와 다른 운영 한도다. |
| QRY-18 | fields 응답 projection | done | post-a7b8ac0:042e67c,7a522b2 | internal/query/projection_test.go | nested wildcard/excerpt and all SDK surfaces are tracked separately under SDK-03. |
| QRY-21 | expand 직렬화와 잘못된 경로 처리 | done | post-a7b8ac0:7a522b2,9946193 | internal/query/pocketbase_compat_live_test.go | native expand는 strict validation을 유지한다. |
| AUTH-04 | arbitrary auth record fields | done | post-a7b8ac0:51ec51d,16e6eca,7a77e69 | internal/userauth/auth_record_model_integration_test.go | PB generic collection CRUD wire와 PgBase realm route/model은 다르다. |
| AUTH-06 | password identity fields | done | post-a7b8ac0:17a6c25,e970a2b,ddd9e38 | internal/userauth/auth_record_model_integration_test.go | PB identity field option JSON은 compatibility projection을 통해서만 노출된다. |
| AUTH-11 | auth methods | done | post-a7b8ac0:b107b04 | internal/userauth/auth_methods_integration_test.go | browser helper acceptance is tracked separately under SDK-05. |
| AUTH-19 | MFA conditional policy | done | post-a7b8ac0:4aa55f4,dcfc384 | internal/userauth/auth_admin_integration_test.go | PB mfa document field names와 PgBase realm policy storage가 다르다. |
| AUTH-27 | OAuth provider inventory helper | done | post-a7b8ac0:b107b04 | internal/userauth/auth_methods_integration_test.go | PB helper response의 모든 provider-specific option은 보장하지 않는다. |
| AUTH-29 | PB auth auxiliary system record handling | done | post-a7b8ac0:51ec51d,5feb0f2,c46f9fd | internal/userauth/auth_record_model_integration_test.go | PB system collection ids/rules와 PgBase realm auxiliary storage는 다르다. |
| AUTH-30 | 사용자 암호의 최대 길이·pattern·hash cost 옵션 | done | post-a7b8ac0:ddd9e38,17a6c25 | internal/userauth/auth_admin_integration_test.go | algorithm/config names are PgBase policy fields, not PB bcrypt knobs. |
| AUTH-31 | 목적별 token secret·duration 설정 | done | post-a7b8ac0:ccac6e5,52fb238,cf13aab,12dfd76,685b510,536bca8,dfe4d0d,87b7c33,57132b9 | internal/userauth/email_actions_integration_test.go | PB TokenConfig JSON and signing format differ, but purpose-level policy is callable. |
| AUTH-33 | OIDC userInfo 없는 id_token 기반 로그인 | done | post-a7b8ac0:92efa86,6b52542 | internal/userauth/oauth_http_test.go | provider extra options and PB callback wire are not identical. |
| RULE-04 | rule null/empty semantics | done | post-a7b8ac0:14da805,4b41d09 | internal/accessrule/pocketbase_test.go | rule storage and native ownership defaults are PgBase-specific. |
| MAIL-02 | auth mail templates | done | post-a7b8ac0:4b41d09,a7d4a14,6df249a | internal/userauth/auth_rules_test.go | PB category/template wire remains different while active notification policy is enforced. |
| FIL-05 | 보호 파일 field | done | post-a7b8ac0:429df00,08e13d0,43d70dd,1294a11 | pgbase/file_integration_test.go | URL/token grammar differs from PocketBase. |
| FIL-09 | 이미지 transform format 범위 | done | post-a7b8ac0:a9cb65d,3c7b7e6 | internal/filestore/variants_test.go | transform engine/option surface is not byte-identical to PB. |
| RT-02 | subscribe/unsubscribe API | done | post-a7b8ac0:6b5b5a7,5ee234c,385d7b4,cc2d3b3 | internal/api/realtime_sessions_test.go | transport remains PgBase durable SSE semantics rather than PB realtime wire. |
| RT-04 | guest/user/superuser/service SSE availability | done | post-a7b8ac0:57f863f,b7a0714 | pgbase/realtime_integration_test.go | SSE event envelope and service token semantics differ. |
| RT-05 | subscription filter/expand와 access 재판정 | done | post-a7b8ac0:57f863f,b7a0714 | internal/realtime/snapshot_test.go | PB expand serialization is approximated through PgBase projection. |
| EXT-03 | 라우트 그룹·미들웨어·스트리밍 응답 | done | post-a7b8ac0:c10c934 | internal/api/custom_routes_test.go | PB router API and Go handler types differ. |
| EXT-06 | 임의 Model CRUD 단계 훅 | done | post-a7b8ac0:2c2f18e,d5dc521 | pgbase/model_lifecycle_test.go | 이는 automatic PocketBase Model ORM interception이 아니다. persistence, transaction, nesting은 callback이 소유하고 직접 검증은 happy path만 다루며 rollback/failure 증거는 약하다. |
| EXT-08 | auth·mailer·file·realtime·backup 훅 범위 | done | post-a7b8ac0:a7d4a14,76b17d2,d5dc521 | pgbase/custom_routes_integration_test.go | event names/payloads are PgBase-specific. |
| EXT-10 | raw DB builder와 일반 transaction helper | done | post-a7b8ac0:d5dc521,730056e | pgbase/javascript_database_objects_integration_test.go | not a drop-in PocketBase dbx builder; only trusted extension API is exposed. |
| EXT-11 | mailer·file·template helper | done | post-a7b8ac0:d5dc521,2c7cb80 | pgbase/javascript_database_objects_integration_test.go | not an arbitrary PocketBase filesystem/template helper API. |
| EXT-12 | 스크립트 발견·등록 모델과 TypeScript 입력 | done | post-a7b8ac0:5efaf64 | internal/jsvm/runtime_test.go | PB global .pb.ts execution model remains incompatible. |
| EXT-18 | 개발 reload·타입 선언·custom VM binding·template | done | post-a7b8ac0:5efaf64 | internal/jsvm/runtime_test.go | arbitrary VM binding/template registry is not PB-compatible. |
| MIG-03 | down rollback 개수와 irreversible 표시 | done | post-a7b8ac0:c10c934 | internal/cli/user_migration_test.go | CLI command/options differ from PocketBase. |
| SDK-03 | fields projection | done | post-a7b8ac0:042e67c,0e3e569,59b0c28,f6cdbe8,758bc88,a3ca3ba | sdk/typescript/test/sdk.test.mjs | nested wildcard/excerpt syntax remains a PgBase projection grammar difference, not an untested route gap. |
| SDK-05 | OAuth browser/realtime helper | done | post-a7b8ac0:abbad2d,7d7deb7,cc2d3b3,a3ca3ba | sdk/typescript/test/sdk.test.mjs | host adapter가 platform popup/deeplink I/O를 제공한다. mocked adapter test는 orchestration을 검증하며 external OAuth-provider popup E2E는 주장하지 않는다. |
| SDK-06 | AuthStore·지속 저장·cookie import/export | done | post-a7b8ac0:c667c69,b02d1c9,cf6a3f3 | sdk/dart/test/sdk_test.dart | SDK API names and storage hooks differ from PB. |
| SDK-07 | 요청 취소 | done | post-a7b8ac0:168d2c2 | sdk/dart/test/sdk_test.dart | cancellation API differs from PB auto-cancellation names. |
| SDK-10 | realtime subscribe/unsubscribe와 reconnect | done | post-a7b8ac0:385d7b4,598969d,cc2d3b3 | pgbase/sdk_realtime_conformance_integration_test.go | PgBase event transport remains distinct. |
| SDK-14 | 관리자 세션 저장·동시 로그인 일관성 | done | post-a7b8ac0:cf6a3f3 | sdk/dart/test/sdk_test.dart | PB AdminAuthWithPassword API differs. |
| UI-01 | 최초 설치·첫 관리자·백업 초기화 | done | post-a7b8ac0:607fbeb,c288d1e,e2807a9,962217c | ui/admin/src/pages/setup-page.test.tsx | UI workflow differs from PB installer. |
| UI-02 | 관리자 로그인·MFA·로그아웃 | done | post-a7b8ac0:bf996e9,75276ef,794e48c | ui/admin/src/pages/login-page.test.tsx | PB modal/layout and credential wire differ. |
| UI-04 | theme·responsive·a11y | done | post-a7b8ac0:607fbeb,eb20e04,254c3aa | ui/admin/src/pages/settings-page.test.tsx | PB branding/theme setting surface is not identical. |
| UI-08 | type·view SQL dry-run | done | post-a7b8ac0:607fbeb,62b9620 | internal/api/schema_operations_live_test.go | PostgreSQL view syntax/results differ. |
| UI-14 | collection API preview | done | post-a7b8ac0:607fbeb | ui/admin/src/pages/collection-api-examples.tsx | not every PB SDK/curl/realtime example is generated. |
| UI-21 | file mapping/thumbnail/download | done | post-a7b8ac0:c388778,7c0ff15 | ui/admin/src/pages/file-field.test.tsx | rich PB file manager behavior differs. |
| UI-22 | auth users lifecycle actions | done | post-a7b8ac0:bf996e9,c46f9fd,da96c1b | internal/userauth/auth_admin_integration_test.go | PB modal composition differs. |
| UI-25 | service tokens/admin application settings | done | post-a7b8ac0:607fbeb,eb20e04,8ac0451 | ui/admin/src/pages/settings-page.test.tsx | PB settings document/wire differs. |
| UI-26 | logs search/detail/statistics/truncate | done | post-a7b8ac0:607fbeb,e1ac79b,8ac0451 | internal/observability/request_log_test.go | PB charts/export/retention setting breadth differs. |
| UI-28 | backup lifecycle | done | post-a7b8ac0:607fbeb,962217c,2c7cb80,7965ef5 | internal/backup/manager_lifecycle_test.go | automatic schedule form layout differs from PocketBase. |
| UI-30 | SQL console/history/result/CSV | done | post-a7b8ac0:607fbeb,53eb6a1 | migrations/system/sql_console_test.go | CSV export/column sort does not match PB fully. |
| UI-35 | 날짜·시간 picker와 local timezone 표시 | done | post-a7b8ac0:607fbeb,62b9620 | internal/query/codec_test.go | dedicated local-time picker UX differs. |
| OPS-02 | 통합 runtime settings GET/PATCH/reload | done | post-a7b8ac0:53eb6a1,e0c331c,e1d25ce,8ac0451 | internal/runtimesettings/settings_integration_test.go | PB single settings document/reload wire differs. |
| OPS-16 | panic 복구 후 request-log 기록 | done | post-a7b8ac0:e1ac79b | internal/api/telemetry_test.go | log schema/statistics differ from PB. |
| OPS-17 | persisted settings encryption key environment | done | post-a7b8ac0:2780aa3,9fc262d | internal/config/env_test.go | PB `--encryptionEnv` CLI spelling/document format differs. |
| OPS-23 | 관리자 CLI OTP·IP 정책 | done | post-a7b8ac0:794e48c,8ac0451 | internal/cli/admin_integration_test.go | PB CLI flags/output differ. |

## 최종 통합 결과

모든 68개 원래 부분 구현 행이 `done`이며, 아래 표의 차이는 기능 미완료가 아니라 명시된 wire·저장소·API 설계 차이다.
