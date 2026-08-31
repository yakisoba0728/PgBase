# Relation / Schema Lifecycle 설계

상태: 2026-08-31 구현 기준 설계

참조 조사: [PocketBase reference research and pgBase capability roadmap](../../pocketbase-reference-research-2026-08-31.md)

## 1. 목적

관리자가 컬렉션에 단일 또는 다중 관계 필드를 정의하고, 레코드 CRUD에서는 이를 UUID 또는 순서 있는 UUID 배열처럼 사용하며, 조회에서는 bounded `expand`로 안전하게 연결 레코드를 가져오게 한다. PostgreSQL 외래 키와 transaction lock으로 실제 무결성을 보장하되, `cascade`와 `set_null`이 Record Operations를 우회해 revision, ETag, audit, outbox를 누락하지 않게 한다.

이 단계는 이후 public auth/rules, policy-aware realtime, file ownership이 재사용할 relation graph와 policy Seam을 만든다. PostgreSQL이 유일한 durable source of truth이고 여러 pgBase 인스턴스가 같은 database를 공유한다는 기존 경계는 유지한다.

## 2. 설계 비교와 선택

독립적으로 세 Interface 설계를 비교했다.

| 안 | 강점 | 비용 | 판정 |
| --- | --- | --- | --- |
| A: `recordgraph.Kernel`의 `Schema/Mutate/Read` 3개 진입점 | 가장 높은 Depth와 한곳에 모인 Locality | 안정화된 `schema`, `recordops`, `query` Module을 한 번에 재편하고 God Module이 될 위험 | 제외 |
| B: schema/mutation/query/integrity Relation Facet + Ports/Adapters | 기존 transaction 소유권을 유지하고 auth/rules, realtime, storage migration에 높은 Leverage | 범용 storage registry와 query algebra를 미리 만들면 과설계 | 내부 Seam의 기반안 |
| C: 관계를 scalar처럼 다루고 모든 관계를 edge table로 저장 | CRUD Interface가 가장 단순하고 single/multi Implementation이 균일 | single 관계도 join과 table이 필요해 대규모 catalog와 조회 비용 증가 | 호출 표면만 채택 |

최종안은 B와 C의 혼합이다.

- 기존 `schema.Service`, `recordops.Service`, `query.Service` 공개 Interface와 transaction 소유권을 유지한다.
- `internal/relation`은 의미 모델, validation, storage binding, delete graph, expand plan을 제공하는 깊은 Module이다.
- 각 consumer가 필요한 좁은 package-private Facet만 소유한다. 범용 field plugin이나 DB portability registry는 만들지 않는다.
- 호출자는 single을 UUID 또는 `null`, multi를 순서 있는 UUID 배열로 사용하고 `expand` 문자열만 추가한다.
- single은 source table UUID column, multi는 전용 junction table을 사용하는 PostgreSQL hybrid Adapter다.
- policy 조회는 typed predicate/projection Port를 거치며 첫 Adapter는 검증된 administrator만 허용한다.

## 3. 의미 모델과 공개 JSON

```go
const TypeRelation FieldType = "relation"

type RelationCardinality string
const (
    RelationSingle   RelationCardinality = "single"
    RelationMultiple RelationCardinality = "multiple"
)

type RelationDeleteAction string
const (
    RelationRestrict RelationDeleteAction = "restrict"
    RelationCascade  RelationDeleteAction = "cascade"
    RelationSetNull  RelationDeleteAction = "set_null"
)

type RelationInput struct {
    TargetCollection string
    Cardinality      RelationCardinality
    MinSelect        int
    MaxSelect        int
    OnDelete         RelationDeleteAction
}

type RelationSpec struct {
    TargetCollectionID uuid.UUID
    Cardinality        RelationCardinality
    MinSelect          int
    MaxSelect          int
    OnDelete           RelationDeleteAction
    ValueCodecVersion  int
    Storage            RelationStorage
}
```

`FieldInput.Relation`은 `type: "relation"`일 때 필수이고 scalar type에서는 금지한다. API 입력의 target collection name은 plan 시 pinned catalog에서 immutable collection ID로 해석한다. self relation은 source collection을 가리키는 symbolic reference로 plan하고 apply 시 새 collection ID로 확정한다.

입력 규칙은 다음과 같다.

- `single`: `MaxSelect=1`, `MinSelect`는 0 또는 1이다.
- `multiple`: `2 <= MaxSelect <= 1000`, `0 <= MinSelect <= MaxSelect`다. 무제한 배열은 허용하지 않는다.
- `Field.Required`는 `MinSelect > 0`과 일치해야 한다. 생략된 `MinSelect`는 optional이면 0, required이면 1로 정규화한다.
- `set_null`은 `MinSelect=0`일 때만 허용한다.
- relation default는 허용하지 않는다.
- 모든 relation은 reverse lookup을 위해 자동 index된다. `Unique`는 single에만 허용하며 one-to-one 제약이다.
- target/cardinality 변경은 첫 릴리스에서 in-place 변환하지 않고 `relation_storage_migration_required`로 거부한다. 명시적으로 필드를 제거하고 다시 추가한다.
- 기존 row가 있는 collection에 required relation을 추가할 수 없다. optional 추가, 값 채우기, required 전환의 staged migration을 사용한다.

레코드 JSON은 다음과 같다.

- optional single: UUID 문자열 또는 `null`
- required single: UUID 문자열
- multiple: 순서를 보존하는 중복 없는 UUID 문자열 배열; empty는 `[]`
- PATCH에서 생략은 unchanged, single `null`과 multi `[]`는 clear다.
- 중복 multi id는 조용히 제거하지 않고 `duplicate_relation_value`로 거부한다.

원래 관계 값은 record root에 남고 expanded target은 별도 `expand` object에 둔다. expand 결과는 root ETag의 재료가 아니다.

## 4. Catalog와 system migration 5

기존 migration 1–4 checksum은 수정하지 않는다. `000005_relation_schema.sql`은 다음을 추가한다.

- `pgbase_meta.fields.field_type` check에 `relation` 추가
- relation 전용 물리 객체를 위한 `pgbase_relation` schema
- `pgbase_meta.field_relations`

개념적 metadata shape는 다음과 같다.

```sql
CREATE TABLE pgbase_meta.field_relations (
    field_id uuid PRIMARY KEY
        REFERENCES pgbase_meta.fields(id) ON DELETE CASCADE,
    source_collection_id uuid NOT NULL
        REFERENCES pgbase_meta.collections(id) ON DELETE CASCADE,
    target_collection_id uuid NOT NULL
        REFERENCES pgbase_meta.collections(id) ON DELETE RESTRICT,
    cardinality text NOT NULL,
    min_select integer NOT NULL,
    max_select integer NOT NULL,
    on_delete text NOT NULL,
    value_codec_version integer NOT NULL,
    storage_kind text NOT NULL,
    storage_name text NOT NULL,
    created_at timestamptz NOT NULL
);
```

check constraints는 cardinality/bounds/action/storage 조합을 방어하고 `(source_collection_id, storage_name)`을 unique로 유지한다. single column 이름은 collection-local이므로 서로 다른 collection에서 같은 field 이름을 사용할 수 있다. `(target_collection_id, field_id)` index는 incoming dependency와 delete graph를 지원한다. Catalog는 field별 `RelationSpec`과 target collection ID별 정렬된 incoming relation 목록을 한 번 계산한다.

`field_relations.target_collection_id ... ON DELETE RESTRICT`는 metadata 우회를 막는 최종 방어다. collection plan/delete는 physical DDL 전에 incoming relation을 찾아 `collection_referenced`로 거부한다. relation field를 먼저 제거해야 target collection을 삭제할 수 있다.

## 5. PostgreSQL storage Adapter

### Single

single relation은 source table의 UUID column이다.

- optional이면 nullable, required이면 `NOT NULL`
- target record FK는 `ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED`
- reverse lookup index는 항상 생성
- `Unique=true`이면 unique index 사용

### Multiple

multi relation은 `pgbase_relation.r_<stable_hash>` table에 저장한다.

```sql
source_id uuid NOT NULL,
target_id uuid NOT NULL,
position integer NOT NULL CHECK (position >= 0),
PRIMARY KEY (source_id, target_id),
UNIQUE (source_id, position)
```

- source FK는 `app.<source>(id) ON DELETE CASCADE`다. source와 함께 사라지는 내부 edge만 정리하며 생존 user record를 변경하지 않는다.
- target FK는 항상 `app.<target>(id) ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED`다. `NO ACTION`은 commit까지 검사를 미뤄 required cycle 전체 삭제를 허용하면서 kernel 우회는 최종적으로 거부한다. PostgreSQL의 `RESTRICT` 검사는 deferrable 선언과 무관하게 즉시 실행되므로 사용하지 않는다.
- `(target_id, source_id)` index는 reverse lookup/delete discovery를 지원한다.
- `position`은 `0..n-1` 연속이어야 한다.
- source insert와 edge insert/update/delete에 설치된 deferred constraint trigger가 commit 시 `MinSelect`, `MaxSelect`, 연속 position을 검사한다.

물리 이름은 validated source table/field name과 domain-separated SHA-256에서 결정적으로 파생하고 63-byte identifier 제한 안에 둔다. plan checksum에는 compiler version, semantic definition, storage binding, 정렬된 typed DDL step이 포함된다. ETag용 schema digest에는 target collection ID, cardinality, bounds, delete action, uniqueness, value codec version만 포함하고 물리 table/constraint 이름은 제외한다.

논리 `cascade`나 `set_null`을 physical FK action으로 만들지 않는다. DB action이 surviving record를 바꾸면 revision, ETag, audit, outbox, policy를 우회하기 때문이다.

## 6. Schema lifecycle

기존 `PlanCreate/Update/Delete`와 `ApplyCreate/Update/Delete` Interface는 유지하되 Planner는 full Catalog를 받는 relation Facet을 사용한다.

Plan 순서:

1. field와 symbolic target을 validation한다.
2. target을 stable collection ID로 해석하고 incoming dependency graph를 계산한다.
3. base table/column DDL을 만든다.
4. relation table/index/FK/trigger DDL을 normalized field order로 만든다.
5. semantic definition, binding, DDL로 checksum을 만든다.

Apply 순서:

1. global schema advisory transaction lock
2. `schema_state FOR UPDATE`
3. current catalog/base version/checksum 재검증
4. qualified physical object name 순서로 DDL
5. collection/field/relation metadata
6. schema migration audit와 version 증가
7. transaction-local `NOTIFY`
8. commit 후 cache publish

DDL, relation metadata, schema audit, version, notification은 모두 commit되거나 모두 rollback된다. self relation은 지원한다. 서로 다른 두 새 collection을 한 plan에서 동시에 만드는 batch API는 첫 릴리스의 비범위지만, A 생성 → B가 A 참조 → A가 B를 참조하는 update로 cycle을 구성할 수 있다.

## 7. Relation mutation Facet

외부 CRUD request type은 유지하고 relation field도 기존 `Input map[string]json.RawMessage`에 들어간다. `recordops`가 transaction과 artifacts를 계속 소유하며 relation Implementation은 같은 `pgx.Tx`에서 validation/write/delete effect만 제공한다.

```go
type MutationEffects struct {
    Root     RecordChange
    Derived  []RecordChange
}

type RecordChange struct {
    Ref           RecordRef
    Operation     string
    Before        map[string]any
    After         map[string]any
    ChangedFields []string
}
```

Create/update 순서:

1. 기존 principal/request/idempotency/body shape를 bounded validation한다.
2. idempotency lock이 있으면 기존 순서를 유지한다.
3. `schema_state FOR SHARE`로 catalog를 pin한다.
4. relation JSON type/cardinality/duplicate를 검증하고 필요한 collection advisory lock을 UUID 순으로 얻는다.
5. source row와 target row를 `(collection_id, record_id)` 순으로 각각 `FOR UPDATE`, `FOR KEY SHARE` 잠근다.
6. base row와 relation edges를 쓴다. relation-only PATCH도 `updated_at`을 변경한다.
7. committed representation을 다시 읽고 source revision/validator/ETag를 변경한다.
8. revision, audit, outbox, create idempotency result를 쓴다.

target이 없거나 principal이 link할 수 없는 경우 public caller에는 동일한 `relation_target_invalid`를 반환해 existence oracle을 막는다. physical FK는 validation과 lock 사이의 TOCTOU를 방어한다.

relation 변경은 source의 changed field, revision, ETag, audit, outbox에 반드시 나타난다. create replay body에는 normalized relation 값이 포함된다.

## 8. Delete graph

record delete의 logical action은 incoming relation별로 결정한다.

1. root를 delete set에 넣는다.
2. incoming `cascade` source를 fixed point까지 delete set에 추가한다.
3. closure 밖 source의 `set_null` edge/column을 update set에 추가한다.
4. closure 밖 source의 `restrict` edge가 하나라도 있으면 전체를 거부한다.
5. self/cycle은 `(collection_id, record_id)` visited set으로 한 번만 처리한다.
6. 내부 edge를 먼저 제거하고 surviving source update, record delete를 결정적 순서로 실행한다.

closure 안에서 서로를 참조하는 `restrict` edge는 둘 다 삭제되므로 거부 원인이 아니다. 모든 derived update/delete는 자체 event ID, revision transition, audit, outbox를 하나씩 가지며 root principal/request와 같은 transaction에 기록된다. root 요청만 caller의 `If-Match`를 검사하고 derived effect는 root operation을 cause로 취급한다.

초기 안전한 동시성 전략은 coarse relation collection advisory lock이다.

- pinned catalog에서 root와 incoming relation graph에 참여하는 collection ID를 계산한다.
- 모든 relation write/delete가 같은 domain의 collection lock을 UUID 순으로 얻는다.
- 그 뒤 affected record row를 `(collection_id, record_id)` 순으로 잠근다.
- schema version row는 항상 collection/record lock보다 먼저 잠근다.

이 전략은 phantom edge가 discovery 뒤 추가되는 것을 막고 multi-instance에서 결정적이다. contention evidence가 필요해지면 `LockStrategy` Implementation만 target stripe로 교체한다.

기본 hard budget은 cascade depth 32, affected record 10,000, examined edge 100,000이다. 초과하면 `relation_delete_budget_exceeded`로 부분 결과 없이 rollback한다.

## 9. Read, expand, filter, sort

`GetRequest`와 `ListRequest`에 bounded `Expand` shape를 추가한다. 기본 record는 raw relation id를 반환하고, 요청한 path만 `expand` object에 넣는다.

Expand compiler는 문자열을 pinned catalog 기준 typed tree로 바꾼다. 기본 budget은 다음과 같다.

- 최대 depth 6
- 최대 expand node 20
- 요청 전체 fetched relation row 10,000
- reverse relation node당 1,000
- 전체 response 4 MiB
- query step 수는 unique path node 수에 비례

base page를 먼저 확정한 뒤 breadth-first batch hydration한다. 각 hop은 현재 level의 source IDs를 모아 single column 또는 junction을 batch 조회하고 target을 `id = ANY($1::uuid[])`로 가져온다. record 수에 비례한 query를 호출하는 Interface는 만들지 않는다.

```go
type ProjectionPolicy interface {
    ReadScope(context.Context, Principal, schema.Collection, ReadAction) (Predicate, FieldSet, error)
}
```

policy Port는 raw SQL이 아니라 typed predicate/projection을 반환한다. 현재 `AdminAllowAll` Adapter만 설치하고 후속 public rule compiler가 같은 Seam을 구현한다. 각 expand/filter/sort hop에 target policy를 적용하며 보이지 않는 target은 expand에서 누락한다.

첫 vertical slice는 one-hop forward expand를 제공하고 같은 planner를 nested/reverse traversal로 깊게 만든다. 최종 relation slice는 다음을 지원한다.

- dot-path forward expand
- `collection_via_field` virtual reverse expand
- single relation ID equality와 multi membership
- bounded relation path filter의 `EXISTS` 의미
- single path의 indexed scalar sort

multi relation sort는 aggregate 의미가 명시되지 않아 거부한다. filter traversal 최대 depth 3/join node 8, sort traversal 최대 depth 2로 expand보다 낮은 별도 budget을 둔다. Exact policy-aware `NULLS LAST` relation sort는 target index가 있어도 root-wide join/sort가 될 수 있으므로 같은 snapshot에서 root 100,000행까지 허용하고, 100,001개 primary-key entry를 bounded probe한 시점에 target join 없이 거부한다. cursor는 기존 root ordering과 final `id` tiebreaker를 유지한다.

## 10. Stable errors와 security

Schema error code:

- `relation_target_collection_not_found`
- `relation_invalid_cardinality`
- `relation_invalid_bounds`
- `relation_default_unsupported`
- `relation_set_null_requires_optional`
- `relation_storage_migration_required`
- `relation_required_backfill_needed`
- `collection_referenced`

Record/query error code:

- `relation_invalid_value`
- `duplicate_relation_value`
- `relation_min_select`
- `relation_max_select`
- `relation_target_invalid`
- `relation_delete_restricted`
- `relation_delete_budget_exceeded`
- `expand_invalid_path`
- `expand_budget_exceeded`
- `relation_filter_unsupported`
- `relation_sort_unsupported`
- `relation_sort_budget_exceeded`

validation detail은 field ordinal/path, relation ID, record key 순으로 정렬한다. SQL, physical object/constraint 이름, hidden target 존재 여부는 response/log/metric label에 노출하지 않는다. `23503`, `23505`, `23514`, `40001`, `40P01`은 relation context에서 stable validation/conflict 또는 bounded retry로 정규화한다.

## 11. Doctor와 telemetry

Doctor는 scalar field의 column check와 relation binding check를 구분한다.

- single column type/nullability/index/FK target/action/deferrability
- multi table columns/PK/unique/check/source·target FK/index
- cardinality/position deferred trigger
- metadata source/target/field 존재와 incoming cache 일관성
- unmanaged or missing relation object

Metric은 collection, field, relation ID를 label로 사용하지 않는다. 허용된 bounded operation/outcome label만 사용해 plan/apply/write/delete/expand의 성공, validation, conflict, budget, unavailable 결과와 duration을 기록한다.

## 12. Admin API와 UI

schema DTO에 nested `relation`을 추가하고 plan response는 normalized relation definition과 생성될 SQL을 표시한다. UI는 target collection selector, single/multiple, min/max, restrict/cascade/set-null, single unique를 제공한다. self target을 선택할 수 있고 destructive action과 staged required 전환을 설명한다.

record editor는 single searchable selector와 ordered unique multi selector를 사용한다. selector의 target search도 bounded/policy-aware endpoint를 사용하며 전체 collection을 브라우저로 내려받지 않는다. API fallback은 UUID 직접 입력을 지원한다.

record GET/list/create/update는 `?expand=`를 받을 수 있고 response shape는 root relation ids와 별도 `expand`다. 기존 relation 없는 요청/응답과 cursor는 호환된다.

## 13. 완료 조건

- 빈 DB와 populated v4 DB의 v5 migration, 반복 실행, v1–v4 checksum 불변
- deterministic single/multi/self/cyclic plan과 stale checksum rejection
- relation metadata, physical DDL, version, audit, `NOTIFY` 원자 rollback
- single/multi FK, uniqueness, ordering, min/max, target 존재 PostgreSQL 검증
- relation-only create/update의 revision, ETag, audit, outbox, idempotency 원자성
- separate pool의 concurrent relation write/delete와 opposite lock order convergence
- restrict/cascade/set-null, self/cycle, budget rollback의 exact derived artifact 검증
- collection referenced guard와 relation 제거 후 정상 삭제
- no-N+1 forward/nested/reverse expand query-count test와 depth/path/row/byte/policy budget
- relation filter/sort/cursor security와 hidden-target oracle 회귀 테스트
- doctor drift, bounded telemetry, admin API/UI, Docker E2E
- full Go/PostgreSQL/race/vet/build, frontend typecheck/test/build, migration/security/artifact 검사

## 14. 비범위와 다음 단계

첫 relation slice는 cross-database/project relation, arbitrary SQL join, unbounded recursion, edge attributes, automatic online single/multi conversion, user-defined DB cascade/hook을 제공하지 않는다. edge attributes가 필요하면 명시적인 join collection을 사용한다.

relation 완료 뒤 public auth/rules가 `ProjectionPolicy`와 Record Operations principal Seam을 구현한다. 그 다음 outbox delivery lease/fencing과 policy-aware realtime projection, file/object-storage lifecycle을 추가한다.
