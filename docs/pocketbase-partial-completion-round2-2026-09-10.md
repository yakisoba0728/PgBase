# PocketBase 부분 구현 2차 완료 ledger — 2026-09-10

기준선은 `1c0f967`의 232개 고유 행(구현 199, 부분 8, 미구현 25)이며 SHA-256 `0f98b4b68beb9a48172124462855bd8c74d6cfb42d2986fe4d350d56ac52cc94`로 고정했다. PocketBase 기준은 v0.40.3 pinned `5684ee24f1e88f71a2ede73d20cf547fbd509e16`이다. 최종 Go 검증 HEAD는 `cd6684d79bbec793a6e03ea34812223261a845be`; SDK/UI/브라우저 검증 HEAD는 `20b0ea66d3a1ab5f58f1ca7fb2df271e2d01eb3a`다.

완료는 설정·wrapper·단위 테스트의 존재가 아니라 실제 HTTP/SDK/extension 진입점, 허용과 거절, 실패/rollback 증거로 판정했다. PostgreSQL 저장 방식과 PB wire 차이는 사용자 capability가 유지될 때만 의도적 차이로 기록했다.

## 원래 부분 8행 최종 수용 결과

| ID | 결과 | production 경로·commit | 허용·거절·rollback 증거 | 남은 의도적 차이 |
|---|---|---|---|---|
| SCH-02 | 구현 | `internal/userauth/collections.go`, `internal/schema/auth_schema_document.go`; `529d4a2`, `bd99e3e` | `auth_collection_document_integration_test.go`가 typed field/rule/policy import-export, `nullable_rules_integration_test.go`가 null deny/empty allow와 전 session 경로를 실제 PG에서 검증한다. malformed/unknown rule은 원자 저장 전에 거절된다. | auth aggregate는 여러 PostgreSQL meta table에 저장되고 compatibility document에서 합성된다. |
| AUTH-15 | 구현 | `internal/userauth/oauth.go`, `auth_auxiliary.go`; `da96c1b` | `auth_rule_enforcement_integration_test.go`가 owner/admin binding, 마지막 sign-in 수단 거절, concurrent unlink 한 건만 성공, OTP 추가 후 허용과 거절 시 identity 보존을 검증한다. | PB system collection 대신 realm auxiliary API를 쓴다. |
| RULE-03 | 구현 | `internal/accessrule/pocketbase.go`, `internal/recordaccess/rule_check.go`; `5187490`, `7f8bf78`, `21d7ab7`, final restore `cd6684d` | `pocketbase_conformance_integration_test.go`가 JSON/index/any/null, forward/back relation, `@collection`, geoDistance/strftime와 request variables를 실제 PG allow/deny로 검증한다. invalid function/modifier/type·injection-like input은 거절된다. | catalog-aware PB compiler는 opt-in이고 기존 native Compile 계약은 유지한다. |
| RULE-05 | 구현 | `internal/userauth/auth_rule_resolver.go`, `service.go`; `347ead7`, `e5c2320`, `529d4a2` | `auth_rule_enforcement_integration_test.go`가 password/OAuth/OTP/MFA/refresh deny 시 state·challenge·credential 미소비와 allow를 검증한다. nullable test가 null/empty를 전 경로에서 구분한다. | token/session wire는 PgBase 형식이다. |
| RULE-06 | 구현 | `internal/userauth/auth_records.go`; `e5c2320`, `66352c9` | `auth_record_model_integration_test.go`가 actor/target 분리, delegated allow/deny, credential 변경·session revoke, cross-realm/hidden-field 거절을 검증한다. | pinned PB v0.40.3의 삭제는 manageRule이 아니라 **deleteRule**을 쓴다. auxiliary identity는 self/admin scope다. 이전 수용 문구를 이 의미로 교정했다. |
| FIL-13 | 구현 | `internal/jsvm/pocketbase_filesystem_globals.go`, `pgbase/extension_objects.go`; `8bd6ea5`, `ff6e84d`, `632daf7`, `134f960` | JS와 native integration이 startup+manifest deny, namespace traversal, list/copy/stream/delete/stat, partial/cancel cleanup, local/S3와 admitted serve/thumbnail을 검증한다. | bounded stream와 namespaced object key를 쓰며 임의 host filesystem은 노출하지 않는다. |
| EXT-14 | 구현 | `internal/jsvm/pocketbase_globals.go`, `pgbase/extension_bridge.go`와 전용 adapters | `docs/compatibility/pocketbase-app-methods.md`가 pinned `core/app.go` 192개를 exact lower-camel wrapper→host→native effect/hook으로 192/192 대조한다. JS→PG transaction rollback, HTTP timeout/origin, filesystem traversal, process allowlist와 capability escalation denial이 integration에서 검증됐다. | PostgreSQL 하나가 DB/AuxDB authority를 담당한다. unsafeWithoutHooks/raw DB/process는 app-control+administrator opt-in이다. arbitrary Go pointer는 노출하지 않는다. |
| OPS-07 | 구현 | `internal/apiratelimit`, `internal/api/middleware.go`; `b8f46ef`, `fc69f53`, `6ca243a` | API integration이 429/Retry-After, trusted proxy/IP, disabled/reload와 guest/user/service/admin audience 우선순위를 실제 HTTP에서 검증한다. | PB settings/SQLite limiter 대신 persisted settings와 PostgreSQL atomic counter를 쓴다. |

## 원래 미구현 행 중 추가 완료

| ID | 결과 | 실제 근거 |
|---|---|---|
| QRY-04 | 구현 | `internal/query/pocketbase_filter_completion_test.go`와 recordaccess PG conformance가 `?=` 계열 any-match, empty multi-value와 parameter binding을 검증한다. |
| QRY-08 | 구현 | 같은 compiler/test가 JSON object path·array index·relation terminal·null을 typed jsonb extraction으로 검증한다. |
| QRY-11 | 구현 | geoDistance/strftime arity/type compiler와 실제 PG 결과, invalid 호출 거절을 검증한다. |
| QRY-12 | 구현 | execution-time UTC macros와 `@request`/`@collection` identifier를 공통 AST로 처리하며 PB의 end boundary 999ms를 oracle test로 고정한다. |

나머지 미구현 21개는 `SCH-05`, `SCH-07`, `SCH-08`, `SCH-15`, `SCH-24`, `FLD-04`, `REC-13`, `REC-14`, `QRY-15`, `QRY-16`, `QRY-17`, `AUTH-28`, `AUTH-32`, `RT-03`, `EXT-04`, `MIG-06`, `SDK-09`, `UI-05`, `UI-07`, `OPS-09`, `OPS-20`이다. `$dbx`는 SDK-09의 TS/Dart placeholder helper가 아니고, broker publish는 RT-03 arbitrary public topic subscribe가 아니며, duplicate만으로 UI-07의 truncate/scaffold까지 닫히지 않는다.

## 검증과 최종 집계

- `cd6684d`: `go test ./...`, `go vet ./...`, `GOTOOLCHAIN=go1.26.8 go test -tags=integration -p 2 ./...`, race `./internal/realtime ./internal/userauth ./internal/jsvm ./pgbase`, 구조 gate와 production 800 LOC 초과 0 모두 PASS. 로그는 `.local/partial-completion-round2/logs/go-unit-vet-final.log`, `go-integration-final-rerun.log`, `go-race-final.log`이다.
- `20b0ea6`: TypeScript build/type/runtime/admin 7/7, Dart 3.13.3 analyze 및 27/27, TS+Dart 실제 HTTP record/realtime conformance, UI 44 files/524 tests와 build PASS.
- authenticated Chromium 22개 고유 테스트 PASS. restore/archive/profile처럼 backend 전제를 바꾸는 흐름은 task-only PG/port/admin/local files+backup/MinIO의 fresh fixture로 분리해 검증했다.

최종 232행 집계는 **구현 211, 부분 0, 미구현 21**이다. 이는 이 비교표의 행별 capability 결과이며 PocketBase 전체 또는 wire 100% 호환 주장이 아니다.
