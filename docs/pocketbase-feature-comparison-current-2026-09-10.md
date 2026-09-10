# PocketBase와 현재 PgBase — 기능별 1:1 비교 (최종 통합 갱신)

기준선은 `a7b8ac0`; 이 문서의 코드 기준 HEAD는 `c0e5b0da1876` (`codex/n6-n9-merged-status`)이다. 원래 232행은 구현 129·부분 68·미구현 35였다. 이번 갱신은 post-baseline commit과 테스트 파일을 각 행에 연결한다. **부분→구현 판정은 사용자 호출 경로와 회귀 테스트, 최종 통합 검증이 함께 있는 경우에만 바꿨다.**

## 집계

| 상태 | 행 수 | 기준선 대비 |
| --- | ---: | --- |
| 구현 | 197 | 원래 부분 행 중 구현 근거가 확인된 항목을 반영 |
| 부분 | 0 | 남은 부분 구현 행 없음 |
| 미구현 | 35 | 새 구현 근거가 없는 원래 행을 유지 |

## 기능 충족도와 wire 차이

197개 `구현`은 각 행의 좁은 사용자 기능·수용 조건이 호출 가능하고 회귀/통합 근거가 있다는 뜻이다. 이것은 PocketBase SDK·HTTP wire·저장소·ORM을 무수정으로 교체할 수 있다는 뜻이 아니다. 각 행의 `PB wire / native PostgreSQL 차이` 열에서 그 차이를 확인한다. 35개 `미구현`은 새 근거가 없는 기준선 행이다.

## 상세 비교표

### collection

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| SCH-01 · base collection CRUD | 구현 → **구현** / 차이 | plan/apply가 일반 컬렉션의 생성·수정·삭제를 PostgreSQL DDL과 버전 영수증으로 처리한다. | PB collection JSON/직접 PATCH 대신 preview-checksum-apply 프로토콜이다. PB wire adapter의 CRUD 변환과 오류 매핑이 필요하다. | 테스트 존재; `internal/schema/service_integration_test.go:1`; `schema` |
| SCH-02 · auth collection data model | 부분 → **구현** / 차이 | typed auth record/profile/relation/file adapters and compatibility metadata expose auth collection shape; five record rule/authRule/manageRule validation is enforced on login/register and completion origins. 코드 경로: `internal/userauth/auth_record_model.go; internal/userauth/collections.go; internal/userauth/auth_rules.go; internal/api/auth_collections.go`. | PB collection JSON and PgBase realm route/storage remain different. | 통합/단위 테스트 존재; `internal/userauth/auth_record_model_integration_test.go`; `post-a7b8ac0:51ec51d,16e6eca,7a77e69,e970a2b,4b41d09` |
| SCH-03 · catalog list and single lookup | 부분 → **구현** / 차이 | `?compat=pocketbase`에서 목록 envelope, filter/sort/page와 id 또는 이름 단건 조회를 실제 route로 제공한다. 기본 route는 기존 native catalog 응답을 유지한다. 코드 경로: `internal/api/schema_handlers.go`. | PB의 전체 collection wire와 native catalog의 UUID/DDL receipt는 같지 않다. | 통합/단위 테스트 존재; `internal/api/schema_handlers_test.go`; `post-a7b8ac0:f36d11a,de20469,b35eeb5` |
| SCH-04 · name, system and type invariants | 부분 → **구현** / 차이 | PB compatibility metadata로 name/system/type을 저장·검증하고 import·alternate apply에도 보존한다. 코드 경로: `internal/schema/schema_document.go; internal/schema/store.go; internal/schema/catalog.go`. | native PostgreSQL table identity와 PB collection id/이름 범위는 별도이며 compat mode에서만 PB 제약을 강제한다. | 통합/단위 테스트 존재; `internal/schema/compatibility_integration_test.go`; `post-a7b8ac0:619d460,62b9620,993f70b,c8bb322` |
| SCH-05 · collection rename lifecycle | 미구현 → **미구현** / 비호환 | update에서 input.Name 또는 TableName이 현재 이름과 다르면 conflict로 거절한다. | stable collection id 기반 table/index/view/rule/relation 참조 rename 계획과 충돌·rollback 회귀 검증이 필요하다. | 소스; `검색: planUpdate의 name/table mismatch는 ErrConflict이며 rename 실행 경로 없음`; `schema` |
| SCH-06 · dependency-aware collection delete | 구현 → **구현** / 차이 | incoming relation 또는 view dependency가 있으면 계획 단계에서 거절하고, 안전한 경우 view/table을 삭제한다. | 삭제 능력은 대응한다. PB hook/error/wire와 PostgreSQL dependency 정책의 차이를 adapter에서 매핑해야 한다. | 테스트 존재; `internal/schema/sql_view_service_integration_test.go:76`; `schema` |
| SCH-07 · truncate collection records | 미구현 → **미구현** / 비호환 | collection 단위 전체 비우기 API/service가 없다. | 인가된 truncate 명령, record/file/relation hook와 원자성, view 거절, 실패 rollback을 구현해야 한다. | 검색; `검색: collection truncate 테스트 없음`; `schema` |
| SCH-08 · schema scaffolds | 미구현 → **미구현** / 비호환 | 새 collection 입력을 위한 scaffold/meta 기능이 없다. | 세 collection 종류의 기본 문서를 생성하는 meta service/API와 안정된 snapshot 테스트가 필요하다. | 검색; `검색: scaffold 테스트 없음`; `schema` |

### schema

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| SCH-09 · collection record rules | 구현 → **구현** / 차이 | 동일 다섯 action의 typed rule을 별도 GET/PUT/preview API로 저장·컴파일한다. | PB filter DSL과 nil/empty 의미, collection JSON 내 rule 필드를 변환하는 계층이 필요하다. | 테스트 존재; `pgbase/record_rules_integration_test.go:1`; `schema` |
| SCH-10 · common field metadata and system protection | 부분 → **구현** / 차이 | system field metadata와 protected/rename/delete 불변조건, presentation metadata를 schema document와 planner에 반영한다. 코드 경로: `internal/schema/schema_document.go; internal/schema/planner.go`. | PgBase는 physical ColumnName을 별도로 보존한다. | 통합/단위 테스트 존재; `internal/schema/compatibility_integration_test.go`; `post-a7b8ac0:619d460,62b9620` |
| SCH-11 · required blank and zero-value semantics | 부분 → **구현** / 차이 | PocketBase validation mode에서 required의 blank/zero semantics를 decoder와 preflight에 적용한다. 코드 경로: `internal/query/codec.go; internal/schema/field_preflight.go`. | native PostgreSQL mode는 기존 nullable/zero semantics를 유지한다. | 통합/단위 테스트 존재; `internal/query/codec_test.go`; `post-a7b8ac0:62b9620` |
| SCH-12 · field defaults | 구현 → **구현** / 차이 | 사용자 지정 JSON default를 타입별 정규화해 SQL DEFAULT로 저장하고 editor/geo는 고정 zero default, autodate/password/file은 별도 수명주기를 쓴다. | PB의 타입별 암시적 default와 PgBase의 명시적 default 사이 import/export 변환 규칙이 필요하다. | 테스트 존재; `internal/schema/advanced_fields_integration_test.go:13`; `schema` |

### index

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| SCH-13 · single-field index and uniqueness | 구현 → **구현** / 차이 | field의 Indexed/Unique flag로 deterministic PostgreSQL index를 create/update/drop한다. | PB raw index를 단순 single-field flag로 안전하게 해석하고 이름·정렬·collation을 보존할 adapter가 필요하다. | 테스트 존재; `internal/schema/field_contract_integration_test.go:14`; `schema` |
| SCH-14 · compound indexes | 구현 → **구현** / 차이 | 2~8개 scalar field의 named compound/unique index를 검증하고 수명주기를 관리한다. | PB raw 정의 중 단순 복합 index를 PgBase IndexInput으로 변환하고 PB index name/ordering 차이를 매핑해야 한다. | 테스트 존재; `pgbase/schema_transfer_http_integration_test.go:62`; `schema` |
| SCH-15 · expression, partial and raw SQL indexes | 미구현 → **미구현** / 비호환 | IndexInput은 field name 배열과 unique boolean만 받으며 expression/partial/raw SQL을 표현하지 못한다. | 안전한 PostgreSQL index AST, expression allowlist, partial predicate, collation/order, export/import 및 dependency 검증이 필요하다. | 소스; `검색: IndexInput 밖 raw/expression/partial index schema 입력 없음`; `schema` |

### lifecycle

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| SCH-16 · scalar field rename then drop/name reuse | 부분 → **구현** / 차이 | logical rename 뒤 name 재사용·drop에서 ColumnName으로 DDL을 생성해 retained field/value/index를 보존한다. 코드 경로: `internal/schema/planner.go; internal/schema/service.go`. | PB SQLite DDL과 달리 PostgreSQL physical column identity를 유지한다. | 통합/단위 테스트 존재; `internal/schema/field_contract_integration_test.go`; `post-a7b8ac0:31e0bad,4b2e8e4` |
| SCH-17 · renamed multi-relation collection delete | 부분 → **구현** / 차이 | relation storage identity를 사용해 renamed multi-relation의 edge table/trigger/function 삭제 계획을 만든다. 코드 경로: `internal/schema/planner.go; internal/schema/relation_types.go`. | PB relation table naming과 PostgreSQL schema namespace는 다르다. | 통합/단위 테스트 존재; `internal/schema/relation_planner_test.go`; `post-a7b8ac0:31e0bad,619d460` |
| SCH-18 · field add, drop and type change policy | 구현 → **구현** / 차이 | add/drop을 계획하고 integer→bigint, integer/bigint→numeric의 안전한 widening만 허용한다. | PB 호환 모드에서는 stable id 기반 add/drop을 유지하되 type mutation을 PB처럼 거절하거나 migration 정책을 명시해야 한다. SCH-16 결함 수정이 전제다. | 테스트 존재; `internal/schema/planner_test.go:427`; `schema` |

### view

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| SCH-19 · view creation and field inference | 구현 → **구현** / 차이 | declarative source/projection/filter와 inspected SQL 두 방식으로 PostgreSQL view를 만들고 field/dependency를 추론하며 unique non-null UUID id를 검증한다. | PB SQLite viewQuery/문자열 id와 PgBase PostgreSQL SQL/UUID id 사이 변환 또는 명확한 거절 진단이 필요하다. | 테스트 존재; `internal/schema/sql_view_inspect_integration_test.go:14`; `schema` |
| SCH-20 · dry-run view sample | 부분 → **구현** / 차이 | side-effect 없는 view sample/dry-run route가 제한된 sample rows와 error envelope를 반환한다. 코드 경로: `internal/schema/view_sample.go; internal/api/schema_handlers.go`. | PB SQLite query planner가 아니라 PostgreSQL SQL view를 실행한다. | 통합/단위 테스트 존재; `internal/api/schema_operations_live_test.go`; `post-a7b8ac0:62b9620` |
| SCH-21 · view update and dependency guards | 부분 → **구현** / 차이 | create/update DTO의 `viewQuery`를 ViewSQL로 매핑하고 native `view`와 conflicting input을 거절한다. 코드 경로: `internal/api/schema_handlers.go; internal/schema/view.go`. | PgBase view는 PostgreSQL SQL view이며 PB collection view storage와 다르다. | 통합/단위 테스트 존재; `internal/api/schema_operations_live_test.go`; `post-a7b8ac0:2a76863,62b9620` |

### transfer

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| SCH-22 · schema export | 구현 → **구현** / 비호환 | repeatable-read snapshot에서 logical pgbase.schema/v1 문서를 정렬해 관리자 download endpoint로 내보낸다. | PB collection JSON exporter/importable shape, auth options/system metadata/raw indexes를 보존하는 PB format export가 필요하다. | 테스트 존재; `pgbase/schema_transfer_http_integration_test.go:14`; `schema` |
| SCH-23 · atomic schema import and preview | 구현 → **구현** / 비호환 | pgbase.schema/v1을 savepoint에서 DDL/rule까지 preview한 뒤 baseVersion/checksum/idempotency로 원자 적용한다. | PB JSON parser/ID merge/type/options semantics를 추가해야 한다. PgBase의 별도 preview/guarded apply는 유지 가능한 확장이다. | 테스트 존재; `pgbase/schema_transfer_http_integration_test.go:14`; `schema` |
| SCH-24 · import deleteMissing | 미구현 → **미구현** / 비호환 | 문서에 포함된 collection만 create/update하며 현재 catalog의 누락 collection을 삭제하는 옵션이나 change 생성이 없다. | 명시적 deleteMissing flag, dependency-safe 삭제 순서, system/realm 제외 정책, preview diff와 rollback 테스트가 필요하다. | 검색; `검색: rg -n 'deleteMissing|DeleteMissing' internal pgbase 결과 없음`; `schema` |

### field

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| FLD-01 · bool field | 구현 → **구현** / 차이 | JSON true/false만 엄격히 받아 PostgreSQL boolean으로 저장한다; NULL required만 공통 검사한다. | PB의 coercion과 required=true에서 false를 거절하는 동작은 SCH-11 호환 모드에서 맞춰야 한다. | 실행; `.local/verification/pocketbase-comparison-2026-09-10/pb-required-probe.log; .local/verification/pocketbase-comparison-2026-09-10/pg-required-probe.log`; `schema` |
| FLD-02 · number field | 구현 → **구현** / 차이 | integer, bigint, arbitrary numeric 타입을 나누고 min/max를 검증하며 bigint/numeric은 정밀도 보존을 위해 JSON string을 받는다. | PB number 입력/coercion과 onlyInt를 세 Pg 타입으로 mapping하고 zero Required는 SCH-11에서 맞춰야 한다. | 테스트 존재; `internal/fieldvalue/options_test.go:11`; `schema` |
| FLD-03 · text validators and autogenerate | 구현 → **구현** / 차이 | text에 rune min/max, RE2 pattern, allowed values와 bounded autogeneratePattern을 write-time·tightening preflight에서 적용한다. | PB 기본 max 5000 및 random regex 생성 범위/coercion을 맞추는 adapter vectors가 필요하다. | 테스트 존재; `internal/fieldvalue/options_test.go:57`; `schema` |
| FLD-04 · custom text primary key | 미구현 → **미구현** / 비호환 | record id는 고정 UUID system column이고 id는 사용자 field 예약어라 text primary key를 선언할 수 없다. | PB 문자열 record id를 그대로 저장·조회·relation 참조할 수 있는 identity abstraction 또는 명시적 변환 계층이 필요하다. | 소스; `검색: FieldInput에 primaryKey 옵션 없음`; `schema` |
| FLD-05 · email field and domain constraints | 구현 → **구현** / 차이 | text Format=email이 mail parser를 사용하고 allowed/denied domains를 정규화해 parent domain의 subdomain까지 matching한다. | PB exact-domain 의미와 PgBase subdomain 의미 중 호환 모드 정책을 정하고 option 이름을 변환해야 한다. | 테스트 존재; `internal/query/field_options_test.go:11`; `schema` |
| FLD-06 · URL field and domain constraints | 구현 → **구현** / 차이 | text Format=url이 host가 있는 http/https URI만 받고 allowed/denied parent/subdomain 규칙을 적용한다. | PB가 허용하는 scheme/port Host 의미와 exact domain 목록을 differential vectors로 고정하고 adapter를 추가해야 한다. | 테스트 존재; `internal/fieldvalue/options_test.go:35`; `schema` |
| FLD-07 · date field min/max | 부분 → **구현** / 차이 | date min/max field contract와 existing-row tightening preflight, PB date wire formatting을 적용한다. 코드 경로: `internal/schema/schema_document.go; internal/schema/field_preflight.go; internal/query/codec.go`. | timestamp 저장은 PostgreSQL timestamptz다. | 통합/단위 테스트 존재; `internal/schema/compatibility_integration_test.go`; `post-a7b8ac0:62b9620` |
| FLD-08 · 단일·다중 select 값 선택 | 구현 → **구현** / 비호환 | text+AllowedValues는 scalar 선택, jsonb+AllowedValues는 다중 string array를 검증하며 관리자 UI도 각각 select/multiple select로 연결된다. | PB 한 select 타입의 MaxSelect 모드와 달리 PgBase는 text/jsonb를 선택한다. 같은 필드의 scalar↔array 모드 전환·빈값 normalization은 동등하지 않다. | 소스·실행: scalar/array의 허용값·거절값 4개 decoder probe; `.local/verification/pocketbase-comparison-2026-09-10/pg_required_probe_test.go:29`; `schema` |
| FLD-09 · JSON field required and maxSize | 부분 → **구현** / 차이 | JSON maxSize와 PB blank predicate를 write decoder, import, preflight에 적용한다. 코드 경로: `internal/schema/schema_document.go; internal/schema/field_preflight.go; internal/query/codec.go`. | JSONB physical representation과 PB JSON wire validation 일부가 다르다. | 통합/단위 테스트 존재; `internal/schema/compatibility_integration_test.go`; `post-a7b8ac0:62b9620` |
| FLD-10 · file count, size, MIME and thumbs | 구현 → **구현** / 차이 | metadata-backed file field가 MaxSelect/MaxFileSize/MimeTypes/Thumbs/Required를 정책에 전달하고 변환 variant를 제한한다. | PB upload mutation 문법과 filename/storage response 및 option 상한 차이를 adapter/test vectors로 맞춰야 한다. | 테스트 존재; `pgbase/file_integration_test.go:23`; `schema` |
| FLD-11 · per-field protected file toggle | 부분 → **구현** / 차이 | field public/protected metadata를 download authorization과 client visibility에 연결한다. 코드 경로: `internal/filestore/service.go; internal/api/file_handlers.go; pgbase/file_services.go`. | PB file token URL grammar 대신 PgBase signed/authorized delivery route를 쓴다. | 통합/단위 테스트 존재; `pgbase/file_integration_test.go`; `post-a7b8ac0:08e13d0,429df00,5b6e0cf,7c0ff15,1294a11` |
| FLD-12 · relation cardinality, bounds and delete action | 구현 → **구현** / 차이 | single inline FK와 multiple edge table, min/max 1000, restrict/cascade/set_null을 검증하고 record delete graph를 transaction 처리한다. | PB CascadeDelete의 방향/의미와 PgBase OnDelete enum을 변환하고 PB string id relation wire를 다뤄야 한다. | 테스트 존재; `internal/recordops/relation_delete_live_test.go:27`; `schema` |
| FLD-13 · password field | 구현 → **구현** / 비호환 | 일반 collection password field를 write-only 처리하고 min/max/pattern 후 Argon2id hash로 저장하며 verify helper를 제공한다. | PB bcrypt hash/cost 및 record getter/setter/wire semantics가 필요하면 adapter 또는 hash migration 전략이 필요하다. | 테스트 존재; `internal/recordops/generated_fields_integration_test.go:146`; `schema` |
| FLD-14 · autodate field | 구현 → **구현** / 차이 | autodate는 read-only이고 onCreate/onUpdate 조건에 statement_timestamp를 기록하며 schema가 최소 한 flag를 요구한다. | PB가 client 값을 no-op하는 방식과 PgBase가 field_read_only로 거절하는 차이를 호환 모드에서 맞춰야 한다. | 테스트 존재; `internal/recordops/generated_fields_integration_test.go:227`; `schema` |
| FLD-15 · editor field | 구현 → **구현** / 대응 | editor string에 동일 기본 5MiB/per-field MaxSize, Required empty 검사와 ConvertURLs metadata/export를 제공한다. | 핵심 사용자 계약은 대응한다. PB wire differential vectors로 byte boundary와 ConvertURLs roundtrip을 추가하면 된다. | 테스트 존재; `internal/schema/advanced_fields_integration_test.go:13`; `schema` |
| FLD-16 · geoPoint field | 구현 → **구현** / 대응 | 동일 두 key 객체, finite/range/Null Island required 검증을 수행하고 filter/sort/cursor도 지원한다. | 핵심 저장·검증 계약은 대응한다. PB/Pg 경계값 differential test만 추가하면 된다. | 테스트 존재; `internal/query/advanced_fields_integration_test.go:15`; `schema` |

### record-crud

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| REC-01 · 레코드 목록 조회 | 구현 → **구현** / 비호환 | 관리자·사용자·서비스·익명 경로에서 목록을 조회하며 cursor/limit 페이지를 반환한다. | 조회 기능은 견고하지만 경로, 페이지 입력과 응답 envelope가 PocketBase 클라이언트와 호환되지 않는다. | 실행; `[Pg service tests](internal/query/service_test.go:1)`; `query` |
| REC-02 · 단일 레코드 조회 | 구현 → **구현** / 비호환 | UUID 레코드 id를 조회하고 expand/fields 및 ETag를 반환한다. | 동작은 갖췄으나 PgBase는 UUID만 허용하고 공개 경로와 JSON 메타데이터가 PocketBase와 다르다. | 실행; `[Pg handler tests](internal/api/record_handlers_test.go:57)`; `query` |
| REC-03 · 레코드 생성 | 구현 → **구현** / 비호환 | 엄격한 JSON 입력, UUIDv7 id, 감사·outbox·revision을 한 트랜잭션으로 생성한다. | 핵심 생성 기능은 구현됐다. 경로, id 형식, 오류와 응답 wire가 다르며 PgBase 전용 Location 결함은 별도 메모에 기록했다. | 실행; `[Pg CRUD handler tests](internal/api/record_handlers_test.go:57)`; `query` |
| REC-04 · 레코드 부분 갱신과 빈 PATCH no-op | 부분 → **구현** / 차이 | empty PATCH를 no-op update로 처리하고 lifecycle/revision pipeline을 보존한다. 코드 경로: `internal/recordops/batch.go; internal/query/compiler.go`. | native mutation receipt와 PB record envelope는 다르다. | 통합/단위 테스트 존재; `internal/recordops/batch_integration_test.go`; `post-a7b8ac0:1dc7e2b` |
| REC-05 · 레코드 삭제 | 구현 → **구현** / 비호환 | DELETE, 선택적 If-Match, 204 응답을 지원한다. | 삭제 능력은 구현됐다. 경로와 조건부 요청 규약은 PocketBase와 다르다. | 실행; `[Pg batch delete result test](pgbase/batch_integration_test.go:235)`; `query` |
| REC-06 · 뷰 컬렉션 레코드 읽기 전용 처리 | 구현 → **구현** / 차이 | 뷰 레코드 조회와 write 차단을 구현한다. | 사용자 능력은 대응하지만 API 경로와 오류 세부 형식은 다르다. | 테스트 존재; `[Pg SQL view integration](pgbase/sql_view_integration_test.go:12)`; `query` |

### serialization

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| REC-07 · 레코드 JSON 메타데이터와 가시성 | 부분 → **구현** / 차이 | compat mode read가 collectionId/collectionName, PB-style time keys와 relation/expand projection을 만든다. 코드 경로: `internal/api/record_handlers.go; internal/query/codec.go; internal/recordops/read.go`. | 기본 native response는 UUID/cursor/PG timestamp contract를 유지한다. | 통합/단위 테스트 존재; `internal/responseprojection/record_test.go`; `post-a7b8ac0:7a522b2` |

### batch

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| REC-08 · 원자적 순서 보장 batch | 구현 → **구현** / 대응 | create/update/delete/upsert를 순서대로 단일 트랜잭션에서 실행하고 파일·감사 이벤트도 롤백한다. | 핵심 사용자 의미론은 대응한다. 요청·응답 형식 차이는 REC-09, upsert 결함은 REC-10으로 분리했다. | 실행; `[Pg atomic batch tests](pgbase/batch_integration_test.go:17)`; `query` |
| REC-09 · batch 요청·응답 wire | 구현 → **구현** / 비호환 | 외부 batch 경로별로 requests 또는 typed operations를 받고 status/headers/body/eventId/record/etag/version을 반환한다. | 기능은 더 풍부하지만 PB 요청 URL 문법, 최상위 경로와 결과 envelope가 달라 PB SDK 요청을 그대로 처리하지 못한다. | 테스트 존재; `[Pg HTTP batch tests](pgbase/batch_integration_test.go:170)`; `query` |
| REC-10 · PUT upsert와 조건부 갱신 | 부분 → **구현** / 차이 | conditional PUT upsert가 absent stale If-Match를 거절하고 legacy base row를 update로 분기한다. 코드 경로: `internal/recordops/batch.go; internal/query/compiler.go`. | PB batch endpoint wire와 PgBase receipt/ETag surface는 다르다. | 통합/단위 테스트 존재; `internal/recordops/batch_integration_test.go`; `post-a7b8ac0:1dc7e2b` |
| REC-11 · multipart batch와 파일 참조 | 구현 → **구현** / 비호환 | multipart 파트를 $file 참조로 batch body에 연결하고 교체·롤백을 지원한다. | 파일 포함 원자성은 구현됐으나 multipart 필드 명명과 개별 요청 wire가 PocketBase와 다르다. | 실행; `[Pg multipart batch integration](pgbase/batch_integration_test.go:17)`; `query` |
| REC-12 · batch 한도·timeout 설정 | 부분 → **구현** / 차이 | persisted runtime batch limits를 transaction, upload path, HTTP runtime settings에 적용한다. 코드 경로: `internal/runtimesettings/settings.go; internal/api/batch_handlers.go; internal/recordops/batch.go`. | 설정 API는 PB 단일 settings document와 다르다. | 통합/단위 테스트 존재; `pgbase/runtime_settings_http_integration_test.go`; `post-a7b8ac0:53eb6a1,63aae01` |

### write-modifier

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| REC-13 · 숫자 증가·감소 modifier | 미구현 → **미구현** / 비호환 | 입력 키를 정확한 스키마 필드명으로만 해석해 modifier 키를 unknown_field로 거부한다. | 원자적인 상대 증감 연산과 PB modifier 키 파싱이 없다. | 검색; `없음(음성 검색; pgSource 참조)`; `query` |
| REC-14 · select·relation·file prepend/append/remove modifier | 미구현 → **미구현** / 비호환 | 정확한 필드명과 전체 대체 입력만 지원하며 modifier 키를 거부한다. | 다중 relation/select/file의 증분 변경을 PB 요청 한 번으로 표현할 수 없다. | 검색; `없음(음성 검색; pgSource 참조)`; `query` |

### filter

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| QRY-01 · 불리언 문법과 괄호 | 구현 → **구현** / 대응 | 동일한 불리언 연산자와 괄호 우선순위를 파싱한다. | 핵심 문법은 대응한다. PgBase는 길이 4096, 노드 100의 명시적 복잡도 한도를 둔다. | 실행; `[Pg compiler tests](internal/query/compiler_test.go:463)`; `query` |
| QRY-02 · 스칼라 비교 연산자 | 구현 → **구현** / 대응 | 동일한 여섯 비교 연산자를 지원한다. | 스칼라 비교 집합은 대응한다. 타입 coercion과 SQL 정규화 세부는 엔진별 차이가 남는다. | 실행; `[Pg filter tests](internal/query/compiler_test.go:396)`; `query` |
| QRY-03 · contains와 not-contains | 부분 → **구현** / 차이 | PB `~`/`!~` LIKE semantics를 `%` wildcard, escaped underscore/backslash, not-contains로 compile한다. 코드 경로: `internal/query/filter.go; internal/query/scalar_predicate.go`. | compat mode에서만 PB expression semantics를 쓰며 native filter contract는 보존한다. | 통합/단위 테스트 존재; `internal/query/pocketbase_compat_live_test.go`; `post-a7b8ac0:c0dbb0f,de20469,9946193` |
| QRY-04 · any-match 비교 연산자 | 미구현 → **미구현** / 비호환 | ? 접두 비교 연산자를 토큰으로 인정하지 않는다. | 다중 relation/select/JSON 값의 PB any-match filter를 표현할 수 없다. | 검색; `없음(음성 검색; pgSource 참조)`; `query` |
| QRY-05 · 피연산자 배치와 field-to-field 비교 | 부분 → **구현** / 차이 | literal/field 양쪽 operand와 field-to-field comparison, symmetric contains를 relation-aware SQL로 compile한다. 코드 경로: `internal/query/filter.go; internal/query/relation_filter_sort.go; internal/query/predicate.go`. | 지원 type/path는 PostgreSQL schema field typing에 제한된다. | 통합/단위 테스트 존재; `internal/query/service_surface_test.go`; `post-a7b8ac0:8cd2f8d,c0dbb0f,9946193` |
| QRY-06 · 문자열·숫자·불리언·null literal | 구현 → **구현** / 차이 | 작은따옴표 문자열, 숫자, true/false, null을 파싱하고 문자열의 겹친 apostrophe escape를 지원한다. | 기본 타입은 갖췄지만 문자열 인용, 날짜 coercion 및 null 비교 정규화의 완전한 wire 동등성은 없다. | 테스트 존재; `[Pg compiler filter tests](internal/query/compiler_test.go:396)`; `query` |
| QRY-08 · JSON 객체 속성·배열 index filter | 미구현 → **미구현** / 비호환 | JSONB 필드 자체를 unsupported_type으로 거부하고 dot path resolver는 relation 또는 geo .lat/.lon만 허용한다. | 임의 JSON 객체 속성과 배열 index를 공개 filter에서 조회할 수 없다. PostgreSQL jsonb path 추출과 literal 타입 비교 규칙이 필요하다. | 소스; `[PB JSON path resolver cases](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/core/record_field_resolver_test.go#L760-L770)`; `query` |
| QRY-10 · 필드 modifier lower·length·each·isset·changed | 부분 → **구현** / 차이 | lower/length/each/isset/changed modifier를 PocketBase scalar parser와 mutation rule evaluation에 적용하고 persisted typed rule AST로 round-trip한다. 코드 경로: `internal/accessrule/pocketbase.go; internal/accessrule/rule.go; internal/query/access_rule.go`. | native rule syntax/storage remains PostgreSQL-specific. | 통합/단위 테스트 존재; `internal/accessrule/pocketbase_test.go`; `post-a7b8ac0:14da805` |
| QRY-11 · geoDistance·strftime 함수 | 미구현 → **미구현** / 비호환 | geo 필드의 .lat/.lon 투영은 있지만 filter 함수 호출 문법은 없다. | 거리 계산과 시간 포맷 기반 PB filter를 이식할 수 없다. | 검색; `없음(음성 검색; pgSource 참조)`; `query` |
| QRY-12 · 시간 macro와 특수 identifier | 미구현 → **미구현** / 비호환 | @ macro 또는 특수 identifier 문법이 없고 단어 literal은 true/false/null만 허용한다. | 상대 시간 및 PB rule/resolver 표현식을 그대로 실행할 수 없다. | 검색; `없음(음성 검색; pgSource 참조)`; `query` |

### relation-query

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| QRY-07 · 정방향 relation filter | 구현 → **구현** / 차이 | 정방향 relation 경로를 EXISTS join으로 컴파일하며 깊이 3, join 8의 예산을 적용한다. | 일반 능력은 구현됐다. 허용 깊이·join 예산과 오류 형식이 PB와 다르다. | 실행; `[Pg relation filter tests](internal/query/relation_filter_sort_test.go:18)`; `query` |
| QRY-09 · 역방향 relation filter | 미구현 → **미구현** / 비호환 | _via_는 expand 계획에서만 해석하며 filter/sort relation resolver는 정방향 schema field만 걷는다. | back relation 조건을 공개 filter로 표현할 수 없다. | 검색; `없음(음성 검색; pgSource 참조)`; `query` |
| QRY-14 · relation sort | 부분 → **구현** / 차이 | forward/multi/reverse `_via_` relation sort와 stable id tie cursor를 relation SQL plan에 구현한다. 코드 경로: `internal/query/relation_filter_sort.go; internal/query/order.go`. | 100,000 root-row safety cap은 PB와 다른 운영 한도다. | 통합/단위 테스트 존재; `internal/query/relation_filter_sort_test.go`; `post-a7b8ac0:84f279e,9946193` |

### sort

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| QRY-13 · 다중 스칼라 sort와 안정 tie-break | 구현 → **구현** / 차이 | +/- comma 목록, 최대 8개, 중복 거부, id 안정 tie-break를 구현한다. | 정렬 능력은 견고하지만 PgBase 일반 컬렉션은 색인된 스칼라만 허용하고 기본 정렬도 PB와 다를 수 있다. | 실행; `[Pg complexity tests](internal/query/compiler_test.go:463)`; `query` |
| QRY-15 · @random·@rowid 특수 sort | 미구현 → **미구현** / 비호환 | sort 필드는 schema/system field로만 해석해 @ 토큰을 거부한다. | PB의 무작위 정렬 요청을 표현할 수 없다. @rowid는 SQLite 특화 내부 의미라 PostgreSQL 대체 의미를 별도 결정해야 한다. | 검색; `없음(음성 검색; pgSource 참조)`; `query` |

### pagination

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| QRY-16 · page 번호 기반 임의 페이지 이동 | 미구현 → **미구현** / 비호환 | cursor/limit keyset만 받아 이전 페이지에서 받은 cursor가 없으면 임의 page N으로 이동할 수 없다. | PB SDK의 page/perPage 요청과 직접 페이지 점프를 처리할 호환 endpoint 또는 offset adapter가 없다. | 소스; `없음(음성 검색; pgSource 참조)`; `query` |
| QRY-17 · totalItems·totalPages와 skipTotal | 미구현 → **미구현** / 비호환 | 목록 응답은 items와 선택적 nextCursor만 가지며 totalItems, totalPages, skipTotal 계약이 없다. | 전체 건수 UI와 PB pagination envelope를 만들 수 없고 skipTotal 요청도 인식하지 않는다. | 소스; `없음(음성 검색; pgSource 참조)`; `query` |

### projection

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| QRY-18 · fields 응답 projection | 부분 → **구현** / 차이 | shared nested projection으로 records/batch/read/auth response의 fields 및 nested selection을 적용한다. 코드 경로: `internal/responseprojection/projection.go; internal/query/projection.go`. | nested wildcard/excerpt and all SDK surfaces are tracked separately under SDK-03. | 통합/단위 테스트 존재; `internal/query/projection_test.go`; `post-a7b8ac0:042e67c,7a522b2` |

### expand

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| QRY-19 · 정방향·중첩 expand | 구현 → **구현** / 차이 | 정방향 relation의 sibling 및 nested expand를 깊이 6과 node/row/query 예산 안에서 구현한다. | 핵심 기능과 깊이 상한은 가깝지만 PgBase의 추가 node/row/query 예산과 오류가 다르다. | 실행; `[Pg nested expand live tests](internal/query/expand_nested_live_test.go:16)`; `query` |
| QRY-20 · 역방향 _via_ expand | 구현 → **구현** / 차이 | sourceCollection_via_field를 해석해 reverse expand하며 별도 reverse-row 예산을 적용한다. | 문법과 능력은 대응하지만 collection 식별 방식, 예산 초과 오류와 결과 세부가 다르다. | 실행; `[Pg reverse expand live tests](internal/query/expand_nested_live_test.go:88)`; `query` |
| QRY-21 · expand 직렬화와 잘못된 경로 처리 | 부분 → **구현** / 차이 | PocketBase compat expand plan이 invalid/missing path를 skip하고 valid path만 expand하며 cursor pagination을 보존한다. 코드 경로: `internal/query/expand_plan.go; internal/query/expand.go`. | native expand는 strict validation을 유지한다. | 통합/단위 테스트 존재; `internal/query/pocketbase_compat_live_test.go`; `post-a7b8ac0:7a522b2,9946193` |

### administrator

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| AUTH-01 · superuser/administrator | 구현 → **구현** / 비호환 | 별도 administrator 계정·세션 | PB superuser record/JWT를 받지 않는다. | 소스/테스트 존재; `internal/adminauth/service_integration_test.go:1`; `auth` |
| AUTH-02 · administrator MFA | 구현 → **구현** / 차이 | administrator TOTP login | PB collection MFA와 별도 admin model이다. | 소스/테스트 존재; `internal/adminauth/mfa_integration_test.go:1`; `auth` |
| AUTH-26 · impersonation | 구현 → **구현** / 비호환 | bounded admin-issued user session | issuer/route/token refresh semantics가 다르다. | 실행/테스트 존재; `pgbase/impersonation_http_integration_test.go:12`; `auth` |

### realm

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| AUTH-03 · named auth collection/realm | 구현 → **구현** / 비호환 | 최대 100 auth realm | PB collection schema/options/record API가 아니라 realm API다. | 소스/테스트 존재; `internal/userauth/collections_integration_test.go:1`; `auth` |
| AUTH-04 · arbitrary auth record fields | 부분 → **구현** / 차이 | auth record model이 typed scalar/relation/file fields, profile schema와 CRUD adapter를 제공한다. 코드 경로: `internal/userauth/auth_record_model.go; internal/userauth/auth_records.go; internal/api/auth_records.go`. | PB generic collection CRUD wire와 PgBase realm route/model은 다르다. | 통합/단위 테스트 존재; `internal/userauth/auth_record_model_integration_test.go`; `post-a7b8ac0:51ec51d,16e6eca,7a77e69` |

### password

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| AUTH-05 · self registration | 구현 → **구현** / 차이 | registrationEnabled register | PB createRule/data contract가 아니다. | 소스/테스트 존재; `internal/userauth/collections_integration_test.go:1`; `auth` |
| AUTH-06 · password identity fields | 부분 → **구현** / 차이 | username/phone/custom identity와 credential-backed system fields를 collection policy와 login projection에 연결한다. 코드 경로: `internal/userauth/collections.go; internal/userauth/auth_record_model.go; internal/api/auth_collections.go`. | PB identity field option JSON은 compatibility projection을 통해서만 노출된다. | 통합/단위 테스트 존재; `internal/userauth/auth_record_model_integration_test.go`; `post-a7b8ac0:17a6c25,e970a2b,ddd9e38` |
| AUTH-07 · 비밀번호 최소 길이 정책 | 구현 → **구현** / 차이 | realm minimumPasswordBytes | PgBase가 노출하는 것은 realm 최소 길이뿐이다. PB password field의 min/max/pattern/cost(bcrypt hash cost) option, auth collection별 token TTL/secret knobs, auth record emailVisibility option은 없다. | 소스/테스트 존재; `internal/userauth/auth_admin_integration_test.go:1`; `auth` |
| AUTH-08 · password login | 구현 → **구현** / 비호환 | POST realm login | path/body/response/JWT가 다르다. | 실행/테스트 존재; `internal/api/auth_collections_methods_test.go:1`; `auth` |

### session

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| AUTH-09 · session refresh | 구현 → **구현** / 비호환 | opaque rotate/sliding TTL | PB JWT semantics가 없고 impersonation refresh가 없다. | 실행/테스트 존재; `internal/userauth/service_integration_test.go:1`; `auth` |
| AUTH-10 · logout and revoke | 구현 → **구현** / 차이 | logout/admin revoke-sessions | PB token persistence model이 아니다. | 실행/테스트 존재; `pgbase/user_management_integration_test.go:1`; `auth` |

### discovery

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| AUTH-11 · auth methods | 부분 → **구현** / 차이 | callable auth-method descriptor가 OAuth provider start endpoint와 PKCE URL/state/verifier를 live flow로 연결한다. 코드 경로: `internal/userauth/auth_methods.go; internal/userauth/oauth.go; internal/api/auth_collections.go`. | browser helper acceptance is tracked separately under SDK-05. | 통합/단위 테스트 존재; `internal/userauth/auth_methods_integration_test.go`; `post-a7b8ac0:b107b04` |

### oauth

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| AUTH-12 · provider catalog/config | 구현 → **구현** / 차이 | realm HTTP OAuth provider rows | PB catalog/option JSON/PKCE switch는 동일하지 않다. | 소스/테스트 존재; `internal/userauth/oauth_http_test.go:1`; `auth` |
| AUTH-13 · realm routing | 구현 → **구현** / 차이 | state binds auth_collection_id | PB one-call auth-with-oauth2와 start/complete wire가 다르다. | 실행/테스트 존재; `pgbase/auth_factors_integration_test.go:1`; `auth` |
| AUTH-14 · signup and link | 구현 → **구현** / 차이 | registration switch/password-bound link | PB email fallback/createData behavior가 다르다. | 실행/테스트 존재; `internal/userauth/oauth_profiles_test.go:1`; `auth` |
| AUTH-15 · OAuth unlink | 미구현 → **미구현** / 비호환 | user OAuth identity unlink 없음 | identity-specific unlink API/UI가 필요하다. | 검색/dispatch 확인: rg unlink/Delete OAuth/user_oauth_identities와 factor routes 검사; `없음`; `auth` |
| AUTH-16 · profile mapping/avatar | 구현 → **구현** / 차이 | provider fields/avatar import | PB named mappedFields/auth-record assignment와 다르다. | 실행/테스트 존재; `internal/userauth/oauth_avatar_test.go:1`; `auth` |
| AUTH-27 · OAuth provider inventory helper | 부분 → **구현** / 차이 | auth methods/provider inventory helper를 callable route/descriptors로 제공한다. 코드 경로: `internal/userauth/auth_methods.go; internal/api/auth_collections.go`. | PB helper response의 모든 provider-specific option은 보장하지 않는다. | 통합/단위 테스트 존재; `internal/userauth/auth_methods_integration_test.go`; `post-a7b8ac0:b107b04` |
| AUTH-28 · Apple client-secret helper | 미구현 → **미구현** / 비호환 | 없음 | settings helper/API와 key input handling이 필요하다. | 검색/dispatch 확인: generate-client-secret/Apple secret generator 전역 검색 0건; OAuth routes는 callback만 있음; `없음`; `auth` |

### otp

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| AUTH-17 · email OTP login | 구현 → **구현** / 비호환 | realm otp request/complete | route/응답/config/token wire가 다르다. | 실행/테스트 존재; `pgbase/email_otp_http_integration_test.go:15`; `auth` |

### mfa

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| AUTH-18 · MFA challenge completion | 구현 → **구현** / 차이 | TOTP factor plus 10 recovery codes | PB native MFA는 TOTP가 아니라 password/OAuth2/email OTP의 method composition이다. PgBase의 TOTP/recovery는 추가 기능이지만 PB method selection/mfaId wire는 없다. | 실행/테스트 존재; `internal/userauth/mfa_integration_test.go:1`; `auth` |
| AUTH-19 · MFA conditional policy | 부분 → **구현** / 차이 | realm MFA duration과 conditional MFA rule을 login/OTP/OAuth challenge에 적용한다. 코드 경로: `internal/userauth/mfa.go; internal/userauth/auth_admin.go; internal/userauth/oauth.go`. | PB mfa document field names와 PgBase realm policy storage가 다르다. | 통합/단위 테스트 존재; `internal/userauth/auth_admin_integration_test.go`; `post-a7b8ac0:4aa55f4,dcfc384` |

### email-action

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| AUTH-20 · password reset request | 구현 → **구현** / 비호환 | public default/realm request | collection path/mail URL/token contract가 다르다. | 실행/테스트 존재; `pgbase/email_actions_integration_test.go:1`; `auth` |
| AUTH-21 · password reset confirmation | 구현 → **구현** / 비호환 | token/newPassword confirm | PB token/endpoint wire와 다르다. | 실행/테스트 존재; `internal/userauth/email_actions_integration_test.go:1`; `auth` |
| AUTH-22 · verification request/confirm | 구현 → **구현** / 비호환 | session request/public confirm | PB public email-address request와 collection path가 없다. | 실행/테스트 존재; `pgbase/email_actions_integration_test.go:1`; `auth` |
| AUTH-23 · email change request/confirm | 구현 → **구현** / 비호환 | session/currentPassword confirm | request body/URL/template wire가 다르다. | 실행/테스트 존재; `pgbase/email_actions_integration_test.go:1`; `auth` |

### administration

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| AUTH-24 · verified/active account management | 구현 → **구현** / 차이 | admin verified/active/revoke routes | PB record CRUD/delegated manageRule가 아니다. | 실행/테스트 존재; `pgbase/user_management_integration_test.go:1`; `auth` |

### alerts

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| AUTH-25 · new-device auth alert/auth origins | 미구현 → **미구현** / 비호환 | 없음 | origin persistence, fingerprint, alert template/delivery API가 필요하다. | 검색/dispatch 확인: AuthAlert/auth_origin/fingerprint login 0건; mail purpose routes 검사; `없음`; `auth` |

### auxiliary-records

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| AUTH-29 · PB auth auxiliary system record handling | 부분 → **구현** / 차이 | auxiliary auth system records와 OAuth identities를 typed views/management routes로 투영한다. 코드 경로: `internal/userauth/auth_auxiliary.go; internal/api/auth_records.go; internal/api/auth_admin.go`. | PB system collection ids/rules와 PgBase realm auxiliary storage는 다르다. | 통합/단위 테스트 존재; `internal/userauth/auth_record_model_integration_test.go`; `post-a7b8ac0:51ec51d,5feb0f2,c46f9fd` |

### auth

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| AUTH-30 · 사용자 암호의 최대 길이·pattern·hash cost 옵션 | 부분 → **구현** / 차이 | collection identity/password policy에 maximum length, pattern and custom password hashing policy를 적용한다. 코드 경로: `internal/userauth/collections.go; internal/userauth/auth_record_model.go`. | algorithm/config names are PgBase policy fields, not PB bcrypt knobs. | 통합/단위 테스트 존재; `internal/userauth/auth_admin_integration_test.go`; `post-a7b8ac0:ddd9e38,17a6c25` |
| AUTH-31 · 목적별 token secret·duration 설정 | 부분 → **구현** / 차이 | email verify/reset/change, session, OTP, impersonation token purpose마다 key/TTL/key-version rotation과 legacy acceptance를 적용한다. 코드 경로: `internal/userauth/token_policy.go; internal/userauth/email_actions.go; internal/userauth/session_refresh.go; internal/userauth/email_otp.go; internal/userauth/impersonation.go`. | PB TokenConfig JSON and signing format differ, but purpose-level policy is callable. | 통합/단위 테스트 존재; `internal/userauth/email_actions_integration_test.go`; `post-a7b8ac0:ccac6e5,52fb238,cf13aab,12dfd76,685b510,536bca8,dfe4d0d,87b7c33,57132b9` |
| AUTH-32 · 인증 레코드 emailVisibility 공개 제어 | 미구현 → **미구현** / 비호환 | 별도 publicUser 응답; 해당 필드·auth record 공개 정책 없음 | auth record 공개 조회와 이메일 공개 여부를 분리하는 대응 계약이 필요. | 소스·검색; `없음`; `root-additions` |
| AUTH-33 · OIDC userInfo 없는 id_token 기반 로그인 | 부분 → **구현** / 차이 | OIDC userInfo failure 시 verified id_token claims fallback으로 sign-in하고 invalid token은 거절한다. 코드 경로: `internal/userauth/oauth_oidc.go; internal/userauth/oauth_http.go`. | provider extra options and PB callback wire are not identical. | 통합/단위 테스트 존재; `internal/userauth/oauth_http_test.go`; `post-a7b8ac0:92efa86,6b52542` |
| UI-23 · realm/options/OAuth/OTP/MFA | 구현 → **구현** / 차이 | realm profile/policy/OAuth redirect | identity/token knobs 범위 차이 | 소스·테스트 존재; `ui/admin/src/pages/auth-collections-page.test.tsx:8`; `ui` |
| UI-24 · mail template/SMTP test | 구현 → **구현** / 차이 | 4 template preview/save+realm SMTP/test | global vs realm boundary | 소스·테스트 존재; `ui/admin/src/pages/auth-collections-page.test.tsx:57`; `ui` |
| UI-25 · service tokens/admin application settings | 부분 → **구현** / 차이 | service token/admin application/rate/profile controls are mounted and persisted. 코드 경로: `ui/admin/src/pages/settings-page.tsx; internal/api/runtime_settings.go`. | PB settings document/wire differs. | 통합/단위 테스트 존재; `ui/admin/src/pages/settings-page.test.tsx`; `post-a7b8ac0:607fbeb,eb20e04,8ac0451` |

### record-authorization

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| RULE-01 · five CRUD actions | 구현 → **구현** / 차이 | same five JSON AST rules | serialization/meaning이 다르다. | 실행/테스트 존재; `pgbase/record_rules_integration_test.go:1`; `auth` |
| RULE-02 · owner/authenticated/comparison | 구현 → **구현** / 차이 | owner/authenticated/all/any/comparison AST | op/depth/type/auth value가 제한된다. | 소스/테스트 존재; `internal/accessrule/rule_test.go:1`; `auth` |
| RULE-03 · PocketBase filter syntax/variables | 미구현 → **미구현** / 비호환 | PB DSL parser 없음 | parser/resolver/relation join/adapter가 필요하다. | 검색/dispatch 확인: @request/@collection/FilterData/RecordFieldResolver 0건; JSON Rules만 Parse; `internal/accessrule/rule_test.go:1`; `auth` |
| RULE-04 · rule null/empty semantics | 부분 → **구현** / 차이 | PB scalar parser가 null/empty literals, relation sort and nullable comparisons를 compile하고 global/auth record rule activation에 적용한다. 코드 경로: `internal/accessrule/pocketbase.go; internal/api/rules.go; internal/userauth/auth_rules.go`. | rule storage and native ownership defaults are PgBase-specific. | 통합/단위 테스트 존재; `internal/accessrule/pocketbase_test.go`; `post-a7b8ac0:14da805,4b41d09` |
| RULE-07 · rule administration/preview | 구현 → **구현** / 비호환 | admin JSON AST preview | 저장·preview workflow 자체는 있다. PB filter string을 붙여넣거나 @request/@collection/relation join을 preview하는 workflow는 없다. | 실행/테스트 존재; `internal/api/record_handlers_test.go:1`; `auth` |

### auth-authorization

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| RULE-05 · authRule | 미구현 → **미구현** / 비호환 | realm authRule 없음 | realm authRule store/evaluation을 모든 auth method에 적용해야 한다. | 검색/dispatch 확인: AuthRule/authRule 0건; LoginInCollection은 policy/password/active 검사; `없음`; `auth` |
| RULE-06 · manageRule | 미구현 → **미구현** / 비호환 | user delegation rule 없음 | auth-record CRUD와 managed record filter evaluation이 필요하다. | 검색/dispatch 확인: ManageRule/manageRule 0건; user management는 accessAdminSession; `없음`; `auth` |

### mail

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| MAIL-01 · SMTP configuration | 구현 → **구현** / 차이 | per-realm SMTP | PB global settings JSON/API와 다르다. | 실행/테스트 존재; `pgbase/email_actions_integration_test.go:144`; `auth` |
| MAIL-02 · auth mail templates | 부분 → **구현** / 차이 | typed auth collection rules propagate login/register/MFA/OAuth/OTP completion origin into active auth mail lifecycle; attachment/new-origin alert flow is retained. 코드 경로: `internal/userauth/auth_rules.go; internal/userauth/email_actions.go; internal/mailer/mailer.go`. | PB category/template wire remains different while active notification policy is enforced. | 통합/단위 테스트 존재; `internal/userauth/auth_rules_test.go`; `post-a7b8ac0:4b41d09,a7d4a14,6df249a` |
| MAIL-03 · placeholders/action links | 구현 → **구현** / 비호환 | action_url/otp/otp_id action page | placeholder 이름/action route/token encoding이 다르다. | 소스/테스트 존재; `pgbase/email_actions_integration_test.go:1`; `auth` |
| MAIL-04 · template preview/SMTP test | 구현 → **구현** / 차이 | realm preview/smtp test | PB settings form/API/scope와 다르다. | 실행/테스트 존재; `pgbase/email_actions_integration_test.go:144`; `auth` |
| MAIL-05 · auth mail hooks | 구현 → **구현** / 차이 | purpose lifecycle hooks | PB event names/payload와 다르다. | 실행/테스트 존재; `pgbase/auth_lifecycle_hooks_test.go:1`; `auth` |

### files

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| FIL-01 · record multipart의 여러 attachment part | 구현 → **구현** / 차이 | batch multipart가 named file parts를 stage하고 `$file` references를 record attachment change로 원자 commit한다. | PB record field-name multipart wire와 PgBase requests/$file binding 형식은 다르다; 전용 file endpoint의 단일 part 제약은 별도 route 제약이다. | 테스트 존재; `pgbase/batch_integration_test.go:17`; `operations` |
| FIL-02 · file field 최대 선택 수/cardinality | 구현 → **구현** / 차이 | FieldPolicy.MaximumCount와 required를 조회·업로드·commit에 적용한다. | PB JSON 배열과 UUID file-resource API의 형식이 다르다. | 테스트 존재; `internal/filestore/service_integration_test.go:22`; `operations` |
| FIL-03 · 파일 크기와 MIME 정책 | 구현 → **구현** / 차이 | 전역 상한에 field policy 상한·MIME allowlist를 겹쳐 적용한다. | PB 입력/오류 JSON과 MIME 탐지 경로가 동일하다고 검증하지 않았다. | 테스트 존재; `internal/filestore/service_integration_test.go:22`; `operations` |
| FIL-04 · record 변경과 파일 attach/remove | 구현 → **구현** / 차이 | staging/Commit AttachmentChange로 record transaction과 파일 metadata를 연결한다. | PB field payload 조작 문법은 받지 않는다. | 테스트 존재; `pgbase/file_integration_test.go:1`; `operations` |
| FIL-05 · 보호 파일 field | 부분 → **구현** / 차이 | public/protected field metadata controls explicit public route and authorized private delivery, including revocation. 코드 경로: `internal/filestore/service.go; internal/api/file_handlers.go`. | URL/token grammar differs from PocketBase. | 통합/단위 테스트 존재; `pgbase/file_integration_test.go`; `post-a7b8ac0:429df00,08e13d0,43d70dd,1294a11` |
| FIL-06 · 원본 download URL·filename 응답 | 구현 → **구현** / 비호환 | GET /api/v1/user\|admin/files/{fileId}가 File.Filename의 Content-Disposition attachment와 no-store로 stream한다. | filename 응답은 확인했지만 PB collection/record/filename URL, 공개 access, cache semantics는 없다. | 소스; `pgbase/file_integration_test.go:1`; `operations` |
| FIL-07 · 단기 download token | 구현 → **구현** / 비호환 | 파일·variant·principal에 묶인 token을 발급하고 query token을 검증한다. | PB token issuer/claim과 endpoint가 다르다. | 테스트 존재; `internal/filestore/service_integration_test.go:270`; `operations` |
| FIL-08 · thumb WxH/t/b/f 문법 | 구현 → **구현** / 차이 | variant 정규식이 WxH와 t/b/f를 수용한다. | PB query 이름은 thumb, PgBase는 variant이다. | 테스트 존재; `internal/filestore/variants_test.go:1`; `operations` |
| FIL-09 · 이미지 transform format 범위 | 부분 → **구현** / 차이 | GIF/WebP input and WebP variant rendering are supported. 코드 경로: `internal/filestore/image.go; internal/filestore/variants.go`. | transform engine/option surface is not byte-identical to PB. | 통합/단위 테스트 존재; `internal/filestore/variants_test.go`; `post-a7b8ac0:a9cb65d,3c7b7e6` |
| FIL-10 · thumbnail cache/concurrent generation | 구현 → **구현** / 차이 | DB variant receipt와 advisory lock으로 rendition 생성을 직렬화한다. | PB wait/fallback/cache behavior를 그대로 재현하지 않는다. | 테스트 존재; `internal/filestore/variants_integration_test.go:61`; `operations` |
| FIL-11 · 로컬 object store | 구현 → **구현** / 차이 | 검증된 key의 local ObjectStore와 file metadata lifecycle을 제공한다. | 저장 경로와 blob facade API가 다르다. | 테스트 존재; `internal/filestore/local_test.go:1`; `operations` |
| FIL-12 · S3 backend와 runtime 설정 | 구현 → **구현** / 차이 | S3 backend와 암호화된 설정, revisioned live switching을 제공한다. | PB settings JSON/환경 변수와 다르다. | 테스트 존재; `internal/filestore/storage_settings_integration_test.go:21`; `operations` |
| FIL-13 · extension용 generic filesystem facade | 미구현 → **미구현** / 비호환 | attachment FileService만 확인되었다. | internal/filestore·pgbase에서 PB System Copy/List/NewWriter 동등 공개 facade를 찾지 못했다. | 검색; `없음`; `operations` |

### realtime

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| RT-01 · SSE connection endpoint/headers | 구현 → **구현** / 비호환 | collection별 user SSE route가 record/cursor frame을 전송한다. | PB_CONNECT/clientId handshake가 없다. | 테스트 존재; `pgbase/realtime_integration_test.go:43`; `operations` |
| RT-02 · subscribe/unsubscribe API | 부분 → **구현** / 차이 | clientId multiplex subscribe/unsubscribe and replace-subscriptions sessions are exposed with replacement isolation. 코드 경로: `internal/api/realtime_sessions.go; internal/realtime/service.go`. | transport remains PgBase durable SSE semantics rather than PB realtime wire. | 통합/단위 테스트 존재; `internal/api/realtime_sessions_test.go`; `post-a7b8ac0:6b5b5a7,5ee234c,385d7b4,cc2d3b3` |
| RT-03 · arbitrary subscription topics | 미구현 → **미구현** / 비호환 | collection record-event만 route registry에서 노출한다. | api/realtime.go·internal/realtime·SDK에서 arbitrary topic publish/subscribe를 찾지 못했다. | 검색; `internal/api/realtime_test.go:1`; `operations` |
| RT-04 · guest/user/superuser/service SSE availability | 부분 → **구현** / 차이 | guest/user/admin/service principals are authorized at session admission and on live delivery. 코드 경로: `internal/api/realtime_sessions.go; internal/realtime/service.go`. | SSE event envelope and service token semantics differ. | 통합/단위 테스트 존재; `pgbase/realtime_integration_test.go`; `post-a7b8ac0:57f863f,b7a0714` |
| RT-05 · subscription filter/expand와 access 재판정 | 부분 → **구현** / 차이 | subscription filters/expands re-evaluate current record, deletion and revocation against access rules. 코드 경로: `internal/api/realtime_sessions.go; internal/realtime/service.go`. | PB expand serialization is approximated through PgBase projection. | 통합/단위 테스트 존재; `internal/realtime/snapshot_test.go`; `post-a7b8ac0:57f863f,b7a0714` |
| RT-06 · event envelope | 구현 → **구현** / 비호환 | event: record JSON(eventId, delivery cursor, collection, recordId, operation, version)를 쓴다. | PB event name/data schema를 emit하지 않는다. | 소스; `pgbase/realtime_integration_test.go:43`; `operations` |
| RT-07 · connection lifetime과 reconnect recovery | 구현 → **구현** / 차이 | connection은 30분 context와 1초 poll을 사용한다; PB protocol recovery가 아니라 durable cursor replay는 별도 PgBase 확장이다. | PB idle timeout 및 clientId re-subscribe contract는 없다. | 소스; `pgbase/realtime_integration_test.go:43`; `operations` |

### Go 확장

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| EXT-01 · 애플리케이션 임베딩과 시작 수명주기 | 구현 → **구현** / 차이 | 공개 pgbase.Options로 라우트·훅·작업·JavaScript를 주입하고 App.Start/Run/Close 및 app.boot/app.serve/app.terminate 훅을 제공한다. | PocketBase core.App 플러그인을 그대로 컴파일할 수 없고 PgBase Options/HookRegistry로 다시 연결해야 한다. | 소스: 양쪽 모두 공개 Go 임베딩 표면과 수명주기 훅이 있다.; `pgbase/app_lifecycle_hooks_test.go:13`; `extensions` |
| EXT-02 · 사용자 정의 HTTP 라우트 | 구현 → **구현** / 차이 | Options.CustomRoutes에 Go http.Handler, 메서드, 패턴, public/user/administrator/service 접근 수준을 시작 시 등록한다. | 등록 시점과 라우터 타입이 다르며 최대 64개, 예약 namespace, 요청·응답 크기 제한이 있다. | 테스트 존재: 세션 폐기, 관리자 CSRF, 서비스 토큰과 principal 투영을 실제 HTTP로 검사한다.; `pgbase/custom_routes_integration_test.go:16`; `extensions` |
| EXT-03 · 라우트 그룹·미들웨어·스트리밍 응답 | 부분 → **구현** / 차이 | extension route groups support middleware registration and streaming responses. 코드 경로: `internal/api/custom_routes.go; pgbase/custom_routes.go`. | PB router API and Go handler types differ. | 통합/단위 테스트 존재; `internal/api/custom_routes_test.go`; `post-a7b8ac0:c10c934` |
| EXT-05 · 우선순위·등록/해제 가능한 훅 체인 | 구현 → **구현** / 차이 | HookRegistry.On이 priority와 등록 순서를 보장하고 실행 중 등록·제거를 허용하며 Before/After 완료 콜백을 쓴다. | PocketBase의 e.Next() middleware형 hook와 tag 선택자는 동일 API가 아니다. | 테스트 존재: 등록 제거와 우선순위, in-flight snapshot을 단위 검사한다.; `pgbase/lifecycle_hooks_test.go:13`; `extensions` |
| EXT-06 · 임의 Model CRUD 단계 훅 | 부분 → **구현** / 차이 | `RunModelMutation`은 임의 custom repository callback을 감싸는 명시적 opt-in lifecycle wrapper이며 validate, around, execute, success/error 단계를 등록한다. 코드 경로: `pgbase/model_lifecycle.go; pgbase/extension_bridge.go`. | 이는 automatic PocketBase Model ORM interception이 아니다. persistence, transaction, nesting은 callback이 소유하고 직접 검증은 happy path만 다루며 rollback/failure 증거는 약하다. | 통합/단위 테스트 존재; `pgbase/model_lifecycle_test.go`; `post-a7b8ac0:2c2f18e,d5dc521` |
| EXT-07 · record·collection 요청/변경 훅 | 구현 → **구현** / 차이 | record before/after commit, list/view/create/update/delete/batch 요청, collection create/update/delete와 schema import preview/apply 훅을 제공한다. | PgBase에는 record enrich와 PocketBase의 validate/execute/success/error 세부 단계 구분이 없다. | 테스트 존재: 실제 HTTP record/batch와 schema import에서 hook 변경·거절·commit 이후 관찰을 검사한다.; `pgbase/lifecycle_hooks_integration_test.go:122`; `extensions` |
| EXT-08 · auth·mailer·file·realtime·backup 훅 범위 | 부분 → **구현** / 차이 | auth/mailer/file/realtime/backup lifecycle events and JS bridge coverage are provided. 코드 경로: `pgbase/lifecycle_hooks.go; pgbase/extension_bridge.go; pgbase/javascript_hooks.go`. | event names/payloads are PgBase-specific. | 통합/단위 테스트 존재; `pgbase/custom_routes_integration_test.go`; `post-a7b8ac0:a7d4a14,76b17d2,d5dc521` |
| EXT-09 · 권한을 상속하는 record CRUD/list/batch helper | 구현 → **구현** / 차이 | ExtensionsForRequest가 이미 승인된 principal을 묶어 Records.List/Get/Create/Update/Delete/Batch facade를 제공한다. | PgBase facade는 request context와 JSON/ETag/idempotency 계약을 요구하며 PocketBase Record 모델 객체가 아니다. | 테스트 존재: Go facade와 JS facade가 동일한 record 권한·hook를 통과하는 실제 HTTP/DB 검사가 있다.; `pgbase/javascript_records_integration_test.go:16`; `extensions` |
| EXT-10 · raw DB builder와 일반 transaction helper | 부분 → **구현** / 차이 | JS trusted `database.transaction` enforces startup+manifest permission, atomic rollback and bounded statements/args. 코드 경로: `pgbase/extension_database.go; pgbase/trusted_database.go`. | not a drop-in PocketBase dbx builder; only trusted extension API is exposed. | 통합/단위 테스트 존재; `pgbase/javascript_database_objects_integration_test.go`; `post-a7b8ac0:d5dc521,730056e` |
| EXT-11 · mailer·file·template helper | 부분 → **구현** / 차이 | Go/JS logical objects read/write/delete through hashed module namespaces and participate in lifecycle, migration and backup inventory. 코드 경로: `pgbase/extension_objects.go; migrations/system/000057_extension_objects.sql`. | not an arbitrary PocketBase filesystem/template helper API. | 통합/단위 테스트 존재; `pgbase/javascript_database_objects_integration_test.go`; `post-a7b8ac0:d5dc521,2c7cb80` |

### 관리자 UI 확장

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| EXT-04 · 프로그래밍 방식 UIExtension 등록 | 미구현 → **미구현** / 비호환 | 공개 Go/JS 등록부와 관리자 UI 라우트에 UIExtension 또는 extensions.js 조립 표면이 없다. | 관리자 화면에 extension 자산과 main.js를 주입하는 제품 기능이 없다. | 검색: rg 'UIExtension|extensions\.js|/_/extensions' pgbase internal cmd sdk 결과 0건; 일반 /extensions 사용자 라우트 fixture는 UIExtension이 아니다.; `-`; `extensions` |

### JSVM

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| EXT-12 · 스크립트 발견·등록 모델과 TypeScript 입력 | 부분 → **구현** / 차이 | TypeScript scripts are bundled/discovered with manifest/CommonJS registration and shared templates. 코드 경로: `internal/jsvm/bundle.go; pgbase/javascript_runtime.go; pgbase/templates.go`. | PB global .pb.ts execution model remains incompatible. | 통합/단위 테스트 존재; `internal/jsvm/runtime_test.go`; `post-a7b8ac0:5efaf64` |
| EXT-13 · CommonJS 로컬 module 지원 | 구현 → **구현** / 차이 | bundle 내부 정적 상대 require를 해석하고 native require("pgbase")를 등록하며 migration dependency closure를 고정한다. | PgBase는 immutable bundle 안의 정적 module graph만 허용해 임의 node package/동적 경로 로딩과 호환되지 않는다. | 테스트 존재: module path, 동적 require 거부, dependency closure와 handler export 검증이 있다.; `internal/jsvm/bundle_test.go:127`; `extensions` |
| EXT-14 · $app/$dbx/$security/$os/$http/$filesystem/$apis globals | 미구현 → **미구현** / 비호환 | 해당 globals가 없고 require("pgbase")의 제한된 capability method만 있다. | 임의 SQL·프로세스·외부 HTTP·crypto·filesystem을 쓰는 PB hook는 동작하지 않는다. | 검색: '$app|$dbx|$security|$os|$http|$filesystem|$apis'를 pgbase/internal/cmd/sdk에서 찾은 결과 0건; native module 등록은 pgbase 하나다.; `internal/jsvm/runtime_test.go:239`; `extensions` |
| EXT-15 · JS route·hook 등록 | 구현 → **구현** / 차이 | manifest routes와 before/after hook exports를 검증해 고정 CustomRoute/HookRegistry에 설치한다. | 동적 routerUse middleware와 임의 onXxx 호출은 없고 manifest가 허용한 event/priority 조합만 쓴다. | 테스트 존재: JS route exact-number 왕복, hook patch/state, 지원 이벤트 검증이 있다.; `pgbase/javascript_hooks_test.go:16`; `extensions` |
| EXT-16 · JS scheduled job/cron | 구현 → **구현** / 차이 | manifest job에 name/schedule/timezone/handler/capability를 선언하고 operator가 provision한 service subject로 durable scheduled job을 실행한다. | 실행 중 cronAdd/Remove가 없으며 등록, identity, 실행 지속성 의미가 다르다. | 테스트 존재: job 등록 검증과 fenced durable 실행 통합 검사가 있다.; `pgbase/javascript_jobs_integration_test.go:16`; `extensions` |
| EXT-17 · namespaced record·mail·file·log capability | 구현 → **구현** / 차이 | require("pgbase")가 records, mail, files, log namespace를 노출하고 route/hook/job마다 capability allowlist를 검사한다. | 사용자 기능은 대응하지만 PB API 이름과 권한 모델이 다르고 임의 app helper는 노출되지 않는다. | 테스트 존재: Go/JS record 권한 일치, file stream handle 제한, mail/log 호출을 실제 서비스로 검사한다.; `pgbase/javascript_file_mail_integration_test.go:22`; `extensions` |
| EXT-18 · 개발 reload·타입 선언·custom VM binding·template | 부분 → **구현** / 차이 | development reload/type declarations and shared templates are available; custom VM bindings stay intentionally bounded. 코드 경로: `internal/jsvm/runtime.go; pgbase/templates.go; sdk/javascript/pgbase.d.ts`. | arbitrary VM binding/template registry is not PB-compatible. | 통합/단위 테스트 존재; `internal/jsvm/runtime_test.go`; `post-a7b8ac0:5efaf64` |

### 마이그레이션

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| MIG-01 · Go·JavaScript 수동 migration 등록 | 구현 → **구현** / 차이 | RegisterUserMigration의 Go callback과 manifest JavaScript migration export를 한 정렬된 manifest로 합친다. | PB 파일명 기반 등록을 PgBase ID+source checksum+manifest 형식으로 바꿔야 한다. | 테스트 존재: Go와 JS migration 혼합 정렬·중복 ID 거부를 통합 검사한다.; `pgbase/javascript_migration_acceptance_integration_test.go:21`; `extensions` |
| MIG-02 · up 실행과 원자적 rollback | 구현 → **구현** / 차이 | migration별 PostgreSQL transaction, advisory lock, schema state lock 안에서 Up과 ledger 기록을 실행한다. | PB는 전체 run을 중첩 transaction으로 묶지만 PgBase는 migration별 commit이며 PostgreSQL lock 의미가 추가된다. | 테스트 존재: 취소된 JS migration의 DDL과 history가 rollback되는 통합 검사가 있다.; `pgbase/javascript_migration_acceptance_integration_test.go:163`; `extensions` |
| MIG-03 · down rollback 개수와 irreversible 표시 | 부분 → **구현** / 차이 | user migration runner supports multi-down migration and irreversibility signaling. 코드 경로: `internal/usermigrate/runner.go; internal/cli/user_migration.go`. | CLI command/options differ from PocketBase. | 통합/단위 테스트 존재; `internal/cli/user_migration_test.go`; `post-a7b8ac0:c10c934` |
| MIG-04 · 동시 runner fencing | 구현 → **구현** / 차이 | 두 advisory transaction lock과 schema_state row lock으로 여러 process의 같은 migration 중복 적용을 막는다. | 사용자 결과는 대응하지만 DB 엔진별 동시성 계약은 독립적이다. | 테스트 존재: 두 concurrent runner 중 합계 한 번만 적용됨을 PostgreSQL 통합 검사한다.; `pgbase/javascript_migration_acceptance_integration_test.go:192`; `extensions` |
| MIG-05 · history와 source checksum 상태 | 구현 → **구현** / 차이 | pgbase_meta.user_migrations에 ID·SHA-256 revision·적용시각·실행시간을 저장하고 applied/pending/checksum_mismatch/history_only/source_gap을 보고한다. | PgBase는 이미 적용된 source byte 변경을 오류로 취급하므로 PB의 filename-only history보다 엄격하다. | 테스트 존재: source 편집, gap, unknown history를 차단하는 runner 검사가 있다.; `internal/usermigrate/runner_test.go:86`; `extensions` |
| MIG-06 · source에서 제거된 migration 이력 정리 | 미구현 → **미구현** / 비호환 | 지정한 다음 pending generated migration의 after snapshot이 현재 DB와 정확히 같을 때만 history row를 채택한다. | PB history-sync는 사라진 source 이력을 삭제하지만 PgBase의 같은 이름 명령은 pending 이력을 채택한다. source 없는 이력을 정리하는 대응 내장 동작은 없다. | 테스트 존재: snapshot 일치 시 schema version 변경 없이 sync하고 불일치는 거부한다.; `pgbase/javascript_migration_acceptance_integration_test.go:134`; `extensions` |
| MIG-07 · 관리자 schema 변경 자동 migration 기록 | 구현 → **구현** / 차이 | UserMigrationAutoRecordDirectory를 켜면 관리자 schema mutation의 before/after canonical snapshot으로 Go source를 내구 기록하고 pending write를 auto-status/recover로 복구한다. | PgBase 자동 기록은 Go source만 생성하고 비동기 materialization 복구 모델을 쓴다; PB JS auto file과 동일하지 않다. | 테스트 존재: write 실패 pending 상태, recover, 동일 operation 중복과 schema import 기록을 통합 검사한다.; `internal/usermigrate/recorder_integration_test.go:24`; `extensions` |
| MIG-08 · collection snapshot·diff 생성과 data-loss 분석 | 구현 → **구현** / 차이 | migration snapshot/diff가 canonical before/after 문서를 만들고 rename·relation·view·index·field options를 전이하며 data-loss와 irreversible reason을 출력한다. | snapshot 문서와 생성 코드 형식은 독립적이며 PgBase는 위험 분석과 exact-target metadata를 추가한다. | 테스트 존재: rename 데이터 보존, relation/view/index/options, up/down snapshot 동등성을 PostgreSQL에서 검사한다.; `pgbase/javascript_migration_acceptance_integration_test.go:21`; `extensions` |

### 클라이언트 SDK

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| SDK-02 · record CRUD·페이지 조회·filter/sort/expand | 구현 → **구현** / 차이 | TS/Dart Records가 cursor/limit/filter/sort/expand list, get, create, ETag update/delete, iterator를 제공한다. | PB page-number pagination과 PgBase cursor, 선택적 PB write와 PgBase idempotency/strong ETag 필수 계약이 다르다. | 실행: TS SDK build/type/runtime suite 통과; Go SDK HTTP conformance 통과. Dart runtime은 설치되지 않아 소스·테스트만 확인했다.; `sdk/typescript/test/sdk.test.mjs:37; sdk/dart/test/sdk_test.dart:38`; `extensions` |
| SDK-03 · fields projection | 부분 → **구현** / 차이 | shared server projection and TypeScript/Dart auth response `fields` forwarding are implemented across records, auth, batch and realtime HTTP conformance paths. 코드 경로: `sdk/typescript/src/index.ts; sdk/dart/lib/src/client.dart; internal/responseprojection/projection.go`. | nested wildcard/excerpt syntax remains a PgBase projection grammar difference, not an untested route gap. | 통합/단위 테스트 존재; `sdk/typescript/test/sdk.test.mjs`; `post-a7b8ac0:042e67c,0e3e569,59b0c28,f6cdbe8,758bc88,a3ca3ba` |
| SDK-04 · auth collection, password·refresh·OTP·MFA | 구현 → **구현** / 차이 | realm(name)의 methods/register/login/refresh/session/logout와 password/reset/verification/email-change/OTP/MFA/TOTP API를 TS/Dart에 제공한다. | 메서드 이름과 서버 flow가 다르며 PB RecordAuthResponse 대신 PgBase UserSession/MfaChallengeError를 쓴다. | 실행: TS auth realm/MFA tests와 서버 conformance 통과; Dart source/test는 존재하나 runtime 미실행이다.; `sdk/typescript/test/sdk.test.mjs:118; sdk/dart/test/sdk_test.dart:38`; `extensions` |
| SDK-05 · OAuth browser/realtime helper | 부분 → **구현** / 차이 | TS/Dart `oauthWithBrowser`가 start, open, callback wait, origin/OAuth error/state/code validation, complete, store, always-close orchestration을 수행한다. 코드 경로: `sdk/typescript/src/index.ts; sdk/dart/lib/src/client.dart; sdk/dart/lib/src/realtime.dart`. | host adapter가 platform popup/deeplink I/O를 제공한다. mocked adapter test는 orchestration을 검증하며 external OAuth-provider popup E2E는 주장하지 않는다. | 통합/단위 테스트 존재; `sdk/typescript/test/sdk.test.mjs`; `post-a7b8ac0:abbad2d,7d7deb7,cc2d3b3,a3ca3ba` |
| SDK-06 · AuthStore·지속 저장·cookie import/export | 부분 → **구현** / 차이 | AuthStore persistence, SSR cookie import/export and concurrent admin session pairing are implemented. 코드 경로: `sdk/dart/lib/src/auth.dart; sdk/dart/lib/src/admin.dart; sdk/typescript/src/index.ts`. | SDK API names and storage hooks differ from PB. | 통합/단위 테스트 존재; `sdk/dart/test/sdk_test.dart`; `post-a7b8ac0:c667c69,b02d1c9,cf6a3f3` |
| SDK-07 · 요청 취소 | 부분 → **구현** / 차이 | request cancellation and state updates use safe controllers/registries. 코드 경로: `sdk/dart/lib/src/client.dart; sdk/typescript/src/index.ts`. | cancellation API differs from PB auto-cancellation names. | 통합/단위 테스트 존재; `sdk/dart/test/sdk_test.dart`; `post-a7b8ac0:168d2c2` |
| SDK-08 · 오류 normalization | 구현 → **구현** / 차이 | PgBaseError가 status/code/details/ETag/requestId를 보존하고 cancellation/network/protocol/MFA를 별도 타입으로 구분한다. | catch할 class와 property가 달라 PB 오류 처리 코드는 수정해야 한다. | 실행: TS suite와 Dart test source에 structured error, ETag, cancellation/network 구분 검사가 있다; Dart runtime은 미실행이다.; `sdk/typescript/test/sdk.test.mjs:55; sdk/dart/test/sdk_test.dart:179`; `extensions` |
| SDK-09 · 안전한 filter placeholder helper | 미구현 → **미구현** / 비호환 | Query.filter 문자열 전달은 있지만 TS/Dart client에 placeholder 값을 escape하는 filter helper가 없다. | 사용자가 동적 filter를 직접 안전하게 조립해야 하므로 공식 SDK 코드를 그대로 이식할 수 없다. | 검색: sdk/typescript와 sdk/dart 공개 client에서 filter( 메서드 0건; Query.filter field만 존재한다.; `-`; `extensions` |
| SDK-10 · realtime subscribe/unsubscribe와 reconnect | 부분 → **구현** / 차이 | realtime topic/listener multiplex, unsubscribe and reconnect lifecycle are implemented and live-conformance tested. 코드 경로: `sdk/dart/lib/src/realtime.dart; sdk/typescript/src/index.ts`. | PgBase event transport remains distinct. | 통합/단위 테스트 존재; `pgbase/sdk_realtime_conformance_integration_test.go`; `post-a7b8ac0:385d7b4,598969d,cc2d3b3` |
| SDK-11 · transactional batch·multipart·upsert | 구현 → **구현** / 차이 | compact BatchOperation, HTTP-shaped batch, typed PUT upsert와 multipart file mapping을 TS/Dart 모두 제공한다. | PB fluent builder가 없고 endpoint·request shape·ETag/idempotency 계약이 다르다. | 실행: TS SDK와 Go conformance batch tests 통과; Dart source tests에 compact/generic upsert가 있으나 runtime 미실행이다.; `sdk/typescript/test/sdk.test.mjs:218; sdk/dart/test/sdk_test.dart:99`; `extensions` |
| SDK-12 · record file URL/upload 처리 | 구현 → **구현** / 차이 | record별 Files list/upload/delete/token/download URL·download를 제공하고 TS/Dart upload에 size/hash 계약을 구현한다. | PB filename 기반 공개 URL과 달리 PgBase는 file UUID와 mint된 token/variant를 사용한다. | 실행: Go SDK conformance에서 file round-trip 통과; TS/Dart unit source에 URL과 multipart shape 검사가 있다.; `pgbase/sdk_integration_test.go:18`; `extensions` |

### Dart SDK

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| SDK-14 · 관리자 세션 저장·동시 로그인 일관성 | 부분 → **구현** / 차이 | admin auth state serializes cookie/CSRF updates so concurrent login cannot cross-pair sessions. 코드 경로: `sdk/dart/lib/src/admin.dart`. | PB AdminAuthWithPassword API differs. | 통합/단위 테스트 존재; `sdk/dart/test/sdk_test.dart`; `post-a7b8ac0:cf6a3f3` |

### 시작

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| UI-01 · 최초 설치·첫 관리자·백업 초기화 | 부분 → **구현** / 차이 | setup flow supports first administrator and setup-authorized backup recovery while retaining the setup gate through restore. 코드 경로: `ui/admin/src/pages/setup-page.tsx; internal/api/setup_backups.go`. | UI workflow differs from PB installer. | 통합/단위 테스트 존재; `ui/admin/src/pages/setup-page.test.tsx`; `post-a7b8ac0:607fbeb,c288d1e,e2807a9,962217c` |

### 인증

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| UI-02 · 관리자 로그인·MFA·로그아웃 | 부분 → **구현** / 차이 | admin login/MFA/logout and email-OTP/password-recovery flows are available. 코드 경로: `ui/admin/src/pages/login-page.tsx; internal/api/admin_handlers.go`. | PB modal/layout and credential wire differ. | 통합/단위 테스트 존재; `ui/admin/src/pages/login-page.test.tsx`; `post-a7b8ac0:bf996e9,75276ef,794e48c` |

### 셸

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| UI-03 · 라우팅·미저장 편집 이탈 보호 | 구현 → **구현** / 차이 | history routes와 draft leave guard | hash route와 다름 | 소스·테스트 존재; `ui/admin/src/routing/unsaved-draft.test.tsx:1`; `ui` |
| UI-04 · theme·responsive·a11y | 부분 → **구현** / 차이 | theme/responsive/a11y styling and admin profile/runtime policy screens are present. 코드 경로: `ui/admin/src/styles.css; ui/admin/src/pages/settings-page.tsx`. | PB branding/theme setting surface is not identical. | 통합/단위 테스트 존재; `ui/admin/src/pages/settings-page.test.tsx`; `post-a7b8ac0:607fbeb,eb20e04,254c3aa` |

### 확장

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| UI-05 · admin UI extension 등록·화면 | 미구현 → **미구현** / 비호환 | UI loader/registry/route 검색 결과 없음 | extension UI 기여 불가 | PB 소스·Pg 검색; `없음`; `ui` |

### 컬렉션

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| UI-06 · create/edit/delete | 구현 → **구현** / 차이 | preview/apply/receipt recovery/delete confirm | K3 rename 데이터 보존 위험은 별도 backend 이슈 | 소스·테스트 존재; `ui/admin/src/pages/collection-designer-page.test.tsx:10`; `ui` |
| UI-07 · duplicate/truncate/scaffold | 미구현 → **미구현** / 비호환 | draft 기본필드만 제공 | PB clone/truncate/template control 없음 | 소스·테스트 존재; `ui/admin/src/designer/collection-designer.test.tsx:1`; `ui` |

### 스키마

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| UI-08 · type·view SQL dry-run | 부분 → **구현** / 차이 | view SQL dry-run/sample preview is exposed in designer workflow. 코드 경로: `ui/admin/src/pages/view-sample-preview.tsx; internal/schema/view_sample.go`. | PostgreSQL view syntax/results differ. | 통합/단위 테스트 존재; `internal/api/schema_operations_live_test.go`; `post-a7b8ac0:607fbeb,62b9620` |
| UI-09 · field type·validation editor | 구현 → **구현** / 차이 | PG-native type/options editor | 모든 PB per-field affordance 동등 검증 안 됨 | 소스·테스트 존재; `ui/admin/src/pages/field-options-editor.test.tsx:1`; `ui` |
| UI-10 · relation 제약·index | 구현 → **구현** / 차이 | relation cardinality와 compound index plan | index SQL 표현 차이 | 소스·테스트 존재; `ui/admin/src/designer/collection-designer.test.tsx:1`; `ui` |

### 규칙

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| UI-11 · CRUD 권한 규칙 편집·미리보기 | 구현 → **구현** / 차이 | action rule editor+preview | PB autocomplete depth 미확인 | 소스·테스트 존재; `ui/admin/src/pages/record-rules-editor.test.tsx:1`; `ui` |

### 스키마 이식

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| UI-12 · schema export/import/diff | 구현 → **구현** / 차이 | export/editor/preview/apply | PB merge/ID switches와 다름 | 소스·테스트 존재; `ui/admin/src/pages/schema-transfer-page.test.tsx:21`; `ui` |

### API

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| UI-14 · collection API preview | 부분 → **구현** / 차이 | collection API preview provides route examples and compatible read guidance. 코드 경로: `ui/admin/src/pages/collection-api-examples.tsx`. | not every PB SDK/curl/realtime example is generated. | 통합/단위 테스트 존재; `ui/admin/src/pages/collection-api-examples.tsx`; `post-a7b8ac0:607fbeb` |

### 레코드

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| UI-15 · list/filter/sort/cursor paging | 구현 → **구현** / 차이 | validated filter/sort+next/previous cursor chain | page-number 아닌 cursor | 소스·테스트 존재; `ui/admin/src/pages/records-page.test.tsx:27`; `ui` |
| UI-16 · persisted columns | 구현 → **구현** / 대응 | admin+collection localStorage | browser persistence 미실행 | 소스·테스트 존재; `ui/admin/src/workspace/record-productivity.test.ts:1`; `ui` |
| UI-17 · bulk delete·JSON export | 구현 → **구현** / 차이 | shift select/precondition delete/JSON export | format와 100-page cap 차이 | 소스·테스트 존재; `ui/admin/src/workspace/record-workspace.test.tsx:1`; `ui` |
| UI-19 · CRUD/duplicate/draft | 구현 → **구현** / 차이 | CRUD files atomic/confirm delete/duplicate/redacted draft | auth special action은 별도 | 소스·테스트 존재; `ui/admin/src/pages/record-edit-page.test.tsx:23`; `ui` |
| UI-20 · relation picker/inline target | 구현 → **구현** / 차이 | keyboard picker+inline create/edit | modal UX와 다름 | 소스·테스트 존재; `ui/admin/src/pages/record-edit-page.test.tsx:117`; `ui` |
| UI-21 · file mapping/thumbnail/download | 부분 → **구현** / 차이 | file picker/preview/download reflects protected/public field metadata. 코드 경로: `ui/admin/src/pages/record-edit-page.tsx; ui/admin/src/pages/file-field.test.tsx`. | rich PB file manager behavior differs. | 통합/단위 테스트 존재; `ui/admin/src/pages/file-field.test.tsx`; `post-a7b8ac0:c388778,7c0ff15` |

### 사용자

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| UI-22 · auth users lifecycle actions | 부분 → **구현** / 차이 | auth user lifecycle includes provider unlink and recovery/OAuth flows. 코드 경로: `ui/admin/src/pages/users-page.tsx; internal/api/auth_admin.go`. | PB modal composition differs. | 통합/단위 테스트 존재; `internal/userauth/auth_admin_integration_test.go`; `post-a7b8ac0:bf996e9,c46f9fd,da96c1b` |

### 운영

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| UI-26 · logs search/detail/statistics/truncate | 부분 → **구현** / 차이 | operations console provides logs search/detail/statistics/truncate controls. 코드 경로: `ui/admin/src/pages/operations-console.tsx; internal/observability/request_log.go`. | PB charts/export/retention setting breadth differs. | 통합/단위 테스트 존재; `internal/observability/request_log_test.go`; `post-a7b8ac0:607fbeb,e1ac79b,8ac0451` |
| UI-27 · scheduled jobs/cron configure/run | 구현 → **구현** / 차이 | System OperationsConsole configures schedule/timezone/enable and queues run | PB program-registered cron model과 다름 | 소스·테스트 존재; `ui/admin/src/pages/operations-console.test.tsx:19`; `ui` |
| UI-28 · backup lifecycle | 부분 → **구현** / 차이 | backup lifecycle UI supports managed restore and is covered by fresh browser fixtures for backup/restore, S3 backup and local-to-S3 migration. 코드 경로: `ui/admin/src/pages/operations-console.tsx; internal/backup/manager.go; internal/api/setup_backups.go`. | automatic schedule form layout differs from PocketBase. | 통합/단위 테스트 존재; `internal/backup/manager_lifecycle_test.go`; `post-a7b8ac0:607fbeb,962217c,2c7cb80,7965ef5` |
| UI-29 · file/backup storage settings/test | 구현 → **구현** / 차이 | two mounted routes, secret redaction/test/migration | 시각 QA 미실행 | 소스·테스트 존재; `ui/admin/src/pages/storage-settings-page.test.tsx:28`; `ui` |
| UI-30 · SQL console/history/result/CSV | 부분 → **구현** / 차이 | SQL console/history/result view is implemented. 코드 경로: `ui/admin/src/pages/sql-console-page.tsx; internal/api/runtime_settings.go`. | CSV export/column sort does not match PB fully. | 통합/단위 테스트 존재; `migrations/system/sql_console_test.go`; `post-a7b8ac0:607fbeb,53eb6a1` |

### 접근성

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| UI-32 · keyboard relation selection | 구현 → **구현** / 대응 | arrow/home/end/escape/enter ARIA listbox | 전체 accessibility audit 없음 | 소스·테스트 존재; screen-reader/browser 미실행; `ui/admin/src/pages/record-edit-page.test.tsx:149`; `ui` |

### ui

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| UI-33 · 리치 텍스트·HTML source 편집 | 구현 → **구현** / 차이 | Tiptap toolbar·표·링크·이미지·HTML source와 portable URL 변환 | 편집 엔진과 서식 지원 범위는 다르지만 시각/소스 편집이 모두 있다. 임의 HTML을 시각 편집으로 왕복하면 지원 모델로 정규화될 수 있음. | 소스·테스트 존재; `ui/admin/src/pages/advanced-field-inputs.test.tsx:7`; `root-additions` |
| UI-34 · geoPoint 좌표와 지도에서 위치 선택 | 구현 → **구현** / 차이 | 좌표 입력·클릭·키보드·zoom 가능한 offline Natural Earth 지도 | 도로 타일·지명 지도의 세밀함과 편집 UX는 다르다. 좌표 선택 자체는 구현. | 소스·테스트 존재; `ui/admin/src/pages/advanced-field-inputs.test.tsx:26`; `root-additions` |
| UI-35 · 날짜·시간 picker와 local timezone 표시 | 부분 → **구현** / 차이 | date/time input and display use schema field contracts and admin editor. 코드 경로: `ui/admin/src/pages/record-edit-page.tsx; internal/query/codec.go`. | dedicated local-time picker UX differs. | 통합/단위 테스트 존재; `internal/query/codec_test.go`; `post-a7b8ac0:607fbeb,62b9620` |

### operations

| ID·기능 | 기준선 상태 → 현재 | 현재 구현 동작·코드 경로 | PB wire / native PostgreSQL 차이 | 실제 근거 |
| --- | --- | --- | --- | --- |
| OPS-01 · serve CLI/runtime | 구현 → **구현** / 차이 | pgbase CLI/App runtime을 제공한다. | flag/env/default port가 PB와 다르다. | 테스트 존재; `internal/cli/run_test.go:1`; `operations` |
| OPS-02 · 통합 runtime settings GET/PATCH/reload | 부분 → **구현** / 차이 | runtime settings GET/PATCH/reload persist, publish lifecycle events, and update live services. 코드 경로: `internal/runtimesettings/settings.go; internal/api/runtime_settings.go; pgbase/runtime_settings_hooks.go`. | PB single settings document/reload wire differs. | 통합/단위 테스트 존재; `internal/runtimesettings/settings_integration_test.go`; `post-a7b8ac0:53eb6a1,e0c331c,e1d25ce,8ac0451` |
| OPS-05 · request logs list/detail/stats/truncate | 구현 → **구현** / 차이 | request route/status/duration retained log의 search/detail/statistics/truncate를 제공한다. | PB log model/UI and route가 다르다. | 테스트 존재; `internal/api/operations_console_test.go:137`; `operations` |
| OPS-06 · login/OTP rate limits | 구현 → **구현** / 차이 | admin auth가 account/network durable buckets를 advisory-lock으로 제한한다. | PB label/rule configuration surface와 전역 endpoint coverage가 다르다. | 테스트 존재; `internal/adminauth/service_integration_test.go:262`; `operations` |
| OPS-07 · PB-style configurable global API rate rules | 미구현 → **미구현** / 비호환 | 인증 및 realtime admission 외 일반 API label-rate-rule config를 확인하지 못했다. | internal/api, pgbase.Options에서 PB Settings.RateLimits 동등 설정/미들웨어를 찾지 못했다. | 검색; `internal/api/middleware_test.go:1`; `operations` |
| OPS-08 · trusted proxy/real client IP | 구현 → **구현** / 차이 | 명시 prefix가 trusted일 때만 X-Forwarded-For를 해석하고 IPv6 /64 rate key를 만든다. | PB trustedProxy/settings field wire와 다르다. | 테스트 존재; `internal/api/client_address_test.go:1`; `operations` |
| OPS-09 · built-in HTTPS/ACME serve | 미구현 → **미구현** / 비호환 | 내장 HTTP server만 확인되었고 TLS listener/ACME config를 찾지 못했다. | pgbase, internal/cli, cmd에서 ListenAndServeTLS/ACME equivalent를 찾지 못했다. | 검색; `pgbase/app_integration_test.go:106`; `operations` |
| OPS-10 · backup CRUD/transfer queue | 구현 → **구현** / 차이 | managed backup list/create/upload/open/delete/restore/job interface와 console routes를 제공한다. | PB ZIP/pb_data artifact와 route payload가 다르다. | 테스트 존재; `internal/backup/backup_integration_test.go:1`; `operations` |
| OPS-11 · DB와 파일의 일관된 백업·복원 | 구현 → **구현** / 차이 | pg_dump/pg_restore 논리 백업과 파일 inventory를 새 대상에 복원·검증한 뒤 활성화한다. | 이는 external PostgreSQL 설계 차이이며 PB artifact 호환은 없다. | 테스트 존재; `internal/backup/online_integration_test.go:1`; `operations` |
| OPS-12 · backup S3 repository | 구현 → **구현** / 차이 | backup receipt/archive를 S3에 publish/get/delete/list하고 ownership marker를 검증한다. | 양쪽 S3 backup은 존재하지만 PB ZIP/pb_data와 Pg PostgreSQL archive/receipt format 및 settings wire가 다르다. | 테스트 존재; `internal/backup/s3_repository_test.go:101`; `operations` |
| OPS-13 · durable scheduled jobs/cron | 구현 → **구현** / 차이 | DB-backed schedule, lease/fence, operator configure/request-run을 제공한다. | PB cron expression API와 handler lifecycle이 동일하지 않다. | 테스트 존재; `internal/jobs/management_integration_test.go:1`; `operations` |
| OPS-15 · self-update | 구현 → **구현** / 차이 | signed manifest selection, verified download, journal/recovery, OS replacement를 구현한다. | PB plugin install/channel UI와 다르며 Windows/실제 channel은 이번에 실행하지 않았다. | 테스트 존재; `internal/selfupdate/selfupdate_test.go:1`; `operations` |
| OPS-16 · panic 복구 후 request-log 기록 | 부분 → **구현** / 차이 | panic recovery records request-log data before responding 500. 코드 경로: `internal/api/router.go`. | log schema/statistics differ from PB. | 통합/단위 테스트 존재; `internal/api/telemetry_test.go`; `post-a7b8ac0:e1ac79b` |
| OPS-17 · persisted settings encryption key environment | 부분 → **구현** / 차이 | persisted runtime settings encryption accepts startup environment key and supports rotation/CAS. 코드 경로: `internal/config/env.go; internal/runtimesettings/settings.go; pgbase/runtime_settings_encryption.go`. | PB `--encryptionEnv` CLI spelling/document format differs. | 통합/단위 테스트 존재; `internal/config/env_test.go`; `post-a7b8ac0:2780aa3,9fc262d` |
| OPS-18 · 내장 자동 백업 스케줄 | 미구현 → **미구현** / 비호환 | 수동 managed backup과 일반 사용자 job은 있으나 내장 backup schedule 연결 없음 | BackupOptions·manager·jobs 등록에 자동 백업 스케줄이 없다. 일반 cron에 사용자가 직접 코드를 쓰는 것은 내장 완료로 세지 않음. | 소스·검색; `없음`; `root-additions` |
| OPS-19 · 백업 보관 개수 제한 | 구현 → **구현** / 차이 | local/S3 저장소 retention 및 오래된 항목 삭제 | PB 자동 생성 백업만의 retention과 적용 범위가 다르다. | 소스·테스트 존재; `internal/backup/archive_test.go:99`; `root-additions` |
| OPS-20 · 자동 백업 실패 관리자 메일 알림 | 미구현 → **미구현** / 비호환 | 오류 로그와 job 상태; 기본 관리자 알림 메일 연결 없음 | backup LogError와 mailer 사이의 기본 알림 경로·수신자 정책 필요. | 소스·검색; `없음`; `root-additions` |
| OPS-21 · HTTP health 확인 | 구현 → **구현** / 차이 | /health/live와 /health/ready; system/schema health 별도 | endpoint/JSON과 진단 범위가 다르다. | 소스·테스트 존재; `internal/api/health_test.go:12`; `root-additions` |
| OPS-22 · 관리자 CLI create/update/upsert/delete | 구현 → **구현** / 차이 | admin create/reset-password/update/upsert/delete | PB 명령명·식별·출력은 다르나 계정 관리 목적 대응. | 소스·테스트 존재; `internal/cli/admin_integration_test.go:127`; `root-additions` |
| OPS-23 · 관리자 CLI OTP·IP 정책 | 부분 → **구현** / 차이 | administrator CLI email OTP/recovery and IP/profile policy are provided. 코드 경로: `internal/cli/run.go; internal/adminauth/email_otp.go; internal/api/admin_profile.go`. | PB CLI flags/output differ. | 통합/단위 테스트 존재; `internal/cli/admin_integration_test.go`; `post-a7b8ac0:794e48c,8ac0451` |
| OPS-24 · CORS와 preflight | 구현 → **구현** / 차이 | 명시적 origin allowlist와 경로별 preflight; 관리자 cookie 경계 별도 | 교차 origin API 호출은 지원. PB의 wildcard·custom predicate·설정 세부는 같지 않다. | 소스·테스트 존재; `internal/api/middleware_test.go:231`; `root-additions` |
| OPS-25 · 사용자 정적 사이트·SPA fallback | 구현 → **구현** / 차이 | PublicDir/PublicSPAFallback, API·admin·custom 경로 우선 | 디렉터리 설정과 asset/navigation 404 정책이 다르다. | 소스·테스트 존재; `pgbase/n2_n3_integration_test.go:24`; `root-additions` |
