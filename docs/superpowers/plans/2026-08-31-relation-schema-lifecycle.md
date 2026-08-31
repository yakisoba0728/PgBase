# Relation / Schema Lifecycle 구현 계획

기준 설계: [Relation / Schema Lifecycle 설계](../specs/2026-08-31-relation-schema-lifecycle-design.md)

> **구현 상태 (2026-08-31): 완료.** 작업 1~10의 acceptance 범위를 모두 구현했다. PostgreSQL 16.13/17.9/18.3 전체 race, Admin UI typecheck/80 tests/build, Docker E2E 2회 shuffle, relation fixture를 포함한 backup/restore/doctor drill과 독립 재감사를 통과했으며 알려진 미해결 구현 finding은 없다. 실제 서비스의 load/soak, RPO/RTO 훈련과 아래 "다음 묶음"은 후속 제품 범위다.

## 공통 제약

- migration 1–4를 수정하지 않고 v5만 추가한다.
- `schema.Service`만 schema transaction을, `recordops.Service`만 record mutation transaction을 commit한다.
- physical target FK는 항상 `ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED`이고 logical cascade/set-null은 Record Operations 안에서만 실행한다. 이 FK는 commit-time 우회 방어이며 `RESTRICT`처럼 required cycle의 정상 삭제를 막지 않는다.
- relation field/value/config와 collection/record ID는 log나 metric label로 사용하지 않는다.
- PostgreSQL identifier는 catalog validation과 deterministic name helper를 거쳐 quote한다.
- 각 checkpoint는 relation 없는 기존 collection/API/UI의 호환 회귀를 먼저 통과해야 한다.

## 작업 1 — relation domain과 migration 5

- `internal/relation`에 cardinality, delete action, bounds, storage binding, stable validation code를 정의한다.
- `schema.FieldInput`, `PlannedField`, `Field`, `Catalog`가 normalized relation spec과 incoming dependency를 표현하게 한다.
- `000005_relation_schema.sql`에 `field_type=relation`, `pgbase_relation`, `field_relations`, checks/index를 추가한다.
- 기존 checksum과 최대 1,597-field app table 계약을 보존한다.

완료 증거: pure validation tests, empty/populated v4→v5 live migration, repeatability, v1–v4 checksum 회귀.

## 작업 2 — deterministic relation schema planner와 catalog store

- Planner가 pinned full Catalog에서 target/self reference를 해석하게 한다.
- single UUID column/index/FK와 multi junction/constraints/index/deferred trigger를 typed step에서 생성한다.
- semantic definition과 physical binding을 plan/checksum/API preview에 포함한다.
- Store가 relation metadata를 read/write하고 update에서 유지되는 field ID에 정확히 연결한다.
- incoming reference가 있으면 plan/apply collection delete를 stable `collection_referenced`로 거부한다.
- unsupported target/cardinality/bounds/default/set-null/transition/backfill 오류를 field path별로 반환한다.

완료 증거: golden DDL/checksum, quoting/name collision, self/cycle, stale plan, apply rollback, two-instance listener/cache tests.

## 작업 3 — relation-aware doctor와 schema API

- Doctor가 scalar column과 single/multi binding을 구분하고 FK/index/trigger/metadata drift를 검사한다.
- schema plan/catalog DTO에 normalized relation config를 추가하고 strict unknown-field validation을 유지한다.
- relation 오류를 400/409/422로 안정적으로 매핑하며 SQL/object 이름을 숨긴다.

완료 증거: object별 fault injection doctor tests, handler contract/security tests.

## 작업 4 — relation value codec와 CRUD storage Facet

- scalar compiler 앞에서 input을 scalar/single/multi로 분리하고 UUID/null/ordered-array를 losslessly decode한다.
- single은 base statement에, multi는 같은 `pgx.Tx`의 junction write에 적용한다.
- target collection advisory lock과 sorted target `FOR KEY SHARE`로 existence/TOCTOU를 검증한다.
- base read 뒤 multi 값을 field ordinal과 position 순으로 batch hydrate한다.
- required/min/max/duplicate/unique 오류와 PostgreSQL constraint 오류를 stable code로 정규화한다.
- relation-only PATCH도 `updated_at`, revision, validator, ETag를 변경한다.
- schema digest에 relation semantics/value codec version을 추가한다.

완료 증거: unit/fuzz/live tests, target rollback, relation-only ETag, create replay, exact audit/outbox/changed-fields tests.

## 작업 5 — bounded delete graph

- Catalog incoming graph와 batch edge reads로 cascade closure, set-null update, restrict conflict를 plan한다.
- participating collection advisory lock과 `(collection_id, record_id)` row lock 순서를 구현한다.
- self/cycle visited set과 depth/record/edge hard budget을 적용한다.
- surviving set-null source와 cascade delete record마다 revision/audit/outbox를 정확히 하나 생성한다.
- internal edge를 먼저 정리하고 deferred physical NO ACTION FK가 commit 시 Record Operations 우회를 막게 한다.

완료 증거: restrict/cascade/set-null 조합, self/cycle/diamond graph, closure 내부 restrict, budget rollback, separate-pool phantom/opposite-order concurrency tests.

## 작업 6 — forward expand와 policy Seam

- `GetRequest`/`ListRequest`와 HTTP handler에 bounded `expand`를 추가한다.
- 문자열을 typed path tree로 parse하고 one-hop forward path부터 지원한다.
- base page를 먼저 확정한 뒤 path별 batch loader로 single/multi target을 hydrate한다.
- batch-only `ProjectionPolicy` Port와 `AdminAllowAll` Adapter를 추가한다.
- depth/node/row/byte/query-step budget과 no-N+1 query-count instrumentation을 적용한다.
- root ETag/cursor는 expand 결과 변경으로 무효화되지 않게 한다.

완료 증거: single/multi one-hop response, hidden-target stub, page/cursor, cycle/budget, constant-query-count live tests.

## 작업 7 — nested/reverse expand와 relation filter/sort

- forward dot path를 depth 6까지 breadth-first batch 실행한다.
- Catalog incoming metadata에서 `collection_via_field` virtual reverse path를 만든다.
- reverse node 1,000과 전체 10,000-row budget을 적용한다.
- single ID equality, multi membership, bounded path `EXISTS` filter를 typed AST에 추가한다.
- single path의 indexed scalar sort만 허용하고 multi sort는 stable error로 거부한다.
- policy-aware relation sort는 root 100,000-row 실행 budget을 넘기기 전에 bounded primary-key probe로 거부한다.
- 각 hop/subquery에 policy predicate와 projection을 적용하고 existence oracle을 막는다.

완료 증거: nested/self/cycle/reverse, policy filtering, filter depth/join budget, sort/cursor determinism, response-byte tests.

## 작업 8 — Admin UI와 target lookup

- collection create/edit form에 target, cardinality, min/max, delete action, single unique controls를 추가한다.
- self target, staged required 전환, collection dependency 오류를 명확히 표시한다.
- record form에 single selector와 ordered unique multi selector를 추가한다.
- bounded target search endpoint와 UUID 직접 입력 fallback을 제공한다.
- raw relation IDs와 `expand` response를 record table/detail에서 안전하게 표시한다.

완료 증거: form validation, plan SQL review, relation CRUD/stale ETag/error UX, typecheck/unit/build.

## 작업 9 — telemetry, E2E, 운영 문서

- bounded relation schema/write/delete/expand operation/outcome/duration event를 추가한다.
- Docker E2E에서 schema create, relation CRUD, restart, two-instance cache, delete action, expand를 검증한다.
- deployment/upgrades/backup/handoff/tradeoff/README의 relation 계약과 direct-SQL 비지원을 갱신한다.
- migration rollback, backup/PITR restore와 relation object 복구 evidence를 추가한다.

완료 증거: dashboard/rule/cardinality 정책 검사, Docker E2E 반복/셔플, 문서 링크/예제 검사.

## 작업 10 — 독립 리뷰와 전체 검증

- schema integrity, transaction/concurrency, query cost/policy, API/UI 네 관점으로 독립 리뷰한다.
- 발견된 finding을 재현 test로 고정하고 수정 뒤 교차 검증한다.
- PostgreSQL 지원 버전별 full Go, race, vet, build와 frontend typecheck/test/build를 실행한다.
- migration immutability, Docker E2E, govulncheck/npm audit, promtool, diff/artifact 검사를 실행한다.

완료 증거: unresolved finding 0, 반복 가능한 검증 명령/결과, 최신 handoff matrix.

## 병렬 실행 경계

1. 작업 1 완료 뒤 작업 2의 planner와 migration/store test를 병렬화한다.
2. Catalog/DTO shape가 고정되면 작업 3, 작업 4의 codec, 작업 8의 schema form을 병렬화한다.
3. CRUD storage가 안정화되면 작업 5 delete graph와 작업 6 forward expand를 병렬화한다.
4. forward planner가 고정되면 작업 7, 작업 8 record selector, 작업 9 운영 artifact를 병렬화한다.
5. 모든 묶음 뒤 작업 10을 별도 reviewer가 수행한다.

## 다음 묶음

Relation 완료 후 public identity/auth collection과 typed collection rules를 같은 principal, query policy, mutation transaction Seam에 연결한다. 그 다음 outbox delivery lease/fencing, policy-aware realtime, file/object-storage lifecycle을 구현한다.
