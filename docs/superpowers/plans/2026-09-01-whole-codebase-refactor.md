# PgBase 전면 리팩터링 계획

작성일: 2026-09-01

상태: 전체 소스·테스트·운영·보안 심화 검토 완료, 구현 전 승인안

심화 재검토: 2026-09-01, production/test/workflow/deployment/migration/document 전 범위 재대조

독립 계획 재감사: 누적 blocker 10건 수정 후 blocker 0

## 1. 목표와 우선순위

이 작업의 최우선 목표는 현재 동작을 보존하면서 코드베이스의 interface를 줄이고, 중복된 implementation을 깊은 module 뒤로 모아 전체 런타임 코드를 줄이는 것이다.

우선순위는 다음 순서를 고정한다.

1. 데이터 정합성, 보안, 운영상 모호성 제거
2. module deepening, 중복 제거, exported surface 축소
3. 구현을 설명하는 주석과 죽은 코드 제거
4. 테스트를 내부 구현 중심에서 interface 계약 중심으로 교체
5. 깊어진 seam을 사용하는 기능 추가
6. 전체 성능·보안·배포·복구 검증

LOC 감소는 depth의 정의가 아니라, 새 layer만 얹고 기존 구현을 남기는 실패를 막는 guardrail로 사용한다. 테스트, migration, 생성물, 문서는 런타임 LOC와 별도로 계산한다.

### 설계 원칙

- interface는 caller와 test가 함께 사용하는 유일한 surface로 본다.
- 새 module을 삭제했을 때 복잡성이 여러 caller로 다시 퍼져야 한다. 그렇지 않으면 shallow wrapper이므로 만들지 않는다.
- production과 test처럼 두 adapter가 실제로 필요한 seam만 유지한다.
- PostgreSQL lock, snapshot, SQLSTATE, deferred constraint는 제품 의미이므로 generic persistence interface로 숨기지 않는다.
- 새 interface를 추가한 뒤 old path를 병렬로 유지하지 않는다. 교체하고 삭제한다.
- 파일 분할이나 이름 변경은 locality와 탐색 비용을 줄일 때만 수행한다.

## 2. 범위와 비범위

포함 범위:

- Go runtime, public package, CLI, API, auth, query, record, relation, schema, doctor, observability
- React 관리자 UI와 embedded dist
- PostgreSQL system migration과 동적 collection schema
- Docker, Compose, CI/CD, backup/restore, E2E, 보안 gate
- 현행 버그, 기존 감사 문서의 잘못된 종결 상태, 테스트 공백
- 이 계획에서 지정한 네 개의 기능 묶음

이번 묶음의 비범위:

- public user auth와 전체 RBAC/rules
- file storage
- outbox delivery worker와 realtime delivery
- microservice 분리 또는 PostgreSQL 추상화
- 다른 SQL dialect 지원
- v1~v5 migration 수정

public auth/RBAC, files, realtime은 threat model과 외부 interface를 크게 바꾸므로 이 리팩터링에 섞지 않고 후속 epic으로 둔다.

## 3. 현재 기준선과 정량 목표

현재 Git 추적 기준선:

| 범위 | 파일 | LOC |
|---|---:|---:|
| 전체 추적 파일 | 253 | 62,572 |
| Go runtime | 86 | 20,072 |
| 관리자 UI runtime(CSS 포함, test helper 제외) | 19 | 2,412 |
| 손작성 runtime 합계 | 105 | 22,484 |
| Go tests | 81 | 27,994 |
| Vitest | 12 | 약 2,500 |
| 문서 디렉터리 | 18 | 5,187 |

`62,572`는 tests, migration, workflow/config, 생성물과 문서를 포함한 저장소 전체 수치이고, `22,484`는 실제 제품 runtime 리팩터링 수치다. 둘을 섞어서 감소율을 만들지 않는다. 회귀·동시성·복구 테스트와 append-only migration을 보강하므로 저장소 전체 LOC는 줄지 않을 수 있다.

| 전체 저장소 checkpoint | 추적 LOC 예상 | 판정 방식 |
|---|---:|---|
| 현재 | 62,572 | 확정 기준선 |
| R11 기존 기능 리팩터링 종료 | 62,000~66,000 | forecast only; runtime hard target과 test/migration 증분을 별도 보고 |
| F0~F3 및 최종 gate 종료 | 63,000~68,000 | forecast only; 총량 증가는 실패가 아니며 category별 gross/new/delete로 설명 |

따라서 사용자가 체감하는 제품 코드는 R11에서 884~1,434줄(3.9~6.4%) 줄이는 것이 현실적 목표다. 기능까지 추가한 최종 runtime은 현재보다 0~684줄(0~3.0%) 적다. 저장소 전체는 테스트와 운영 증거 때문에 현재 대비 약 428~5,428줄 늘 수 있으며, R0의 동일 측정 명령으로 매 checkpoint에서 이 forecast를 다시 보정한다.

리팩터링 범위별 runtime 기준선:

| 트랙 | 현재 | 기존 기능 리팩터링 종료 목표 |
|---|---:|---:|
| database/query/recordops/relation/schema | 43파일 / 12,560 LOC | core replacement checkpoint 11,900 이하; 정합성·ledger·pagination·index 포함 예상 12,150~12,650, hard 12,750 |
| auth/API/CLI/config/doctor/migrate/observability + public app/composition | 36파일 / 6,520 LOC + 약 992 LOC | 6,800 이하 |
| 관리자 UI | 19파일 / 2,412 LOC | 2,050 LOC 이하 |
| 전체 기존 기능·확정 결함 수정 runtime | 22,484 LOC | 예상 21,050~21,600, hard 21,600; stretch 20,700 |

심화 검토 전의 20,235줄은 pagination sidecar/backfill, schema operation ledger/reconciler, 인증 복구 정책과 release 검증 runtime까지 넣고도 10%를 줄인 수치라 hard target으로는 과도했다. 기존 기능과 확정 결함 수정까지의 정직한 예상은 21,050~21,600줄이며 20,700줄은 stretch다. 새 기능 F0~F3까지 포함한 최종 runtime은 21,800~22,484줄로 예상하고, 현재 22,484줄을 넘지 않는 것을 hard guardrail로 둔다. 보안·정합성 code를 숨기거나 서로 다른 implementation을 합쳐 수치를 맞추지 않는다.

LOC는 net만 보지 않는다. 각 replacement stack은 `gross new`, `gross deleted`, `net`, 삭제된 old caller 수, interface/export 변화량을 함께 보고한다.

| checkpoint | 예산과 판정 |
|---|---|
| R0 측정/즉시 hygiene | production dead/unused/error-handling 삭제 55~62 LOC를 실제 diff로 확정하고 이후 stack별 gross budget을 잠근다. |
| R2/R3/R6 core replacement | data runtime 11,900 LOC 이하. 현재 확인한 old planner/query/retry/relation caller 삭제 후보 650~850 LOC를 replacement와 같은 stack에서 제거한다. |
| R1~R10 정합성 추가 완료 | data 12,150~12,650, hard 12,750; backend+app hard 6,800; UI 2,050 이하; 합계 hard 21,600. |
| F0~F3 완료 | 전체 예상 21,800~22,484, hard 22,484. 기능 LOC를 기존 module에 숨기지 않는다. |

R2/R2b/R3/R4/R5/R6는 R0에서 각각의 gross-new/gross-delete 예산을 prototype diff로 확정한다. 새 코드가 늘어나는 correctness/migration stack은 허용하되, replacement stack이 old implementation을 남긴 채 net-positive이면 merge하지 않는다.

파일 수는 hard target에서 제외한다. 현재 `recordops/service.go` 1,221줄, `schema/planner.go` 900줄, `schema/service.go` 850줄처럼 locality를 위해 나눠야 할 파일이 있으므로, 파일 수 감소를 강제하면 서로 무관한 implementation을 다시 합치는 역효과가 난다. module 수와 public surface는 줄이되 private 파일 수는 결과로만 보고한다.

심화 재검토에서 측정한 구조 기준선:

| 지표 | 현재 | 기존 기능 리팩터링 종료 목표 |
|---|---:|---:|
| production Go interface 선언 | 19 | 12 이하 |
| production Go exported 선언(type/function/method) | 337 | 285 이하 |
| production `New*` constructor | 24 | 16 이하 |
| runtime comment-only/comment-like 줄 | Go 844 + UI 42 = 886 | 600 이하, stretch 500 |
| 800줄 초과 production 파일 | 3 | 0 |
| page 직접 `useState`/`useEffect` 지점 | 69 | 30 이하 |
| UI runtime export | 83 | 65 이하 |
| unused 6 + unused parameter/result 3 + 무효 대입 2 | 11 | 0 |
| runtime CLI 무시 I/O 오류 | 6 | 0 또는 의도적 무시를 한 곳에서 명시 |

기계적 복제와 의미적 복제를 구분한다.

- production에 기본 `dupl` 임계값을 넘는 동일 코드 조각은 0건이다. 파일을 합치거나 generic helper를 늘릴 근거가 없다.
- 실제 중복은 query/record/schema의 retry loop 3개, schema apply template 3개, scalar/relation order·keyset, root page assembly, CLI DB bootstrap 3개, migration history 판정 2개처럼 정책이 여러 module에 다시 구현된 형태다.
- test에는 동일 조각 3쌍이 있고, table/conformance vector로 교체할 수 있다. test LOC 감소는 동작 추적표가 완성된 뒤에만 계산한다.

현재 검증 기준선:

- `go test ./... -count=1`, `go vet ./...`, Vitest 12파일/90개, TypeScript/Vite production build는 통과했다.
- 로컬 PostgreSQL 환경이 없어 Go 테스트 262개가 skip됐다. 따라서 이 통과는 unit 기준선일 뿐 live 정합성 증거가 아니다. integration suite는 별도 build tag/명령으로 분리하고 환경 미설정 시 즉시 실패하게 한다.
- `govulncheck`의 reachable finding은 0, `npm audit --audit-level=high`는 0이다. Trivy image 결과는 release-equivalent gate에서 별도로 확인한다.
- standalone `staticcheck 2025.1.1`은 로컬 Go 1.27 export data를 읽지 못한다. 코드 실패로 세지 않고, 저장소 toolchain Go 1.26.7과 호환되는 pinned lint toolchain으로 교체한다.

2026-09-01 repository-wide Codex Security scan에서 source-backed finding 두 건을 확정했다.

| severity | finding | root control | 처리 단계 |
|---|---|---|---|
| medium | 5회 실패가 15분 동안 올바른 password까지 pre-verification 차단하는 targeted administrator lockout | `internal/adminauth/service.go:203`, `internal/adminauth/store.go:199` | R0 regression → R1 limiter state-machine 수정 |
| low | 실존 계정만 `FOR UPDATE`를 KDF 동안 유지해 동시 timing으로 account 존재를 구분하고 KDF 대기가 DB resource를 점유 | `internal/adminauth/store.go:91`, `internal/adminauth/service.go:209,279` | R0 concurrency/pool regression → R1 transaction/KDF 재배치 |

SQL/DDL injection, cursor tampering, relation authorization bypass, idempotency cross-principal replay, XSS/static traversal 후보는 현재 administrator-only boundary, typed grammar/parameter, identifier quoting, HMAC/canonical cursor, CSP/path guard와 resource budget 근거로 기각했다. production TLS/proxy/PostgreSQL grants/tag protection은 저장소 밖 실제 배포 설정을 R12 운영 승인 때 별도 확인한다.

추가 구조 지표:

- scalar/relation `ORDER BY` renderer: 2개 이상 → 1개
- scalar/relation keyset renderer: 2개 → 1개
- root page row loop/finalizer: 각각 2~3개 → 각각 1개
- schema apply transaction template: 3개 → 1개
- transaction retry implementation: 3개 → 1개
- CLI DB bootstrap: 3개 → 1개
- migration history evaluator: 2개 → 1개
- view의 HTTP/CSRF/ETag/checksum 지식: 0
- 임시 forwarding adapter: 각 작업 묶음 종료 시 0
- integration suite의 환경 미설정 skip: 0

## 4. 변경 불가 계약

다음 계약은 명시적인 versioning 단계가 없는 한 바꾸지 않는다.

- 공개 `pgbase.Options`, `App.Handler`, `App.Start`, `App.Run`, `App.Close`의 의미
- 기존 HTTP route, method, status, JSON/error envelope, 보안 header
- cookie, exact-origin, CSRF, trusted-proxy, body-size, ETag, idempotency 계약
- lossless bigint/numeric과 duplicate-key 거부
- schema plan의 SQL 순서, checksum, base version 검증
- record mutation의 revision/audit/outbox/idempotency 원자성
- relation lock 순서, deferred constraint, cascade/set-null/restrict 의미
- migration checksum과 v1~v5 SQL
- metric 이름, label, bucket, dashboard/alert query
- embedded UI의 CSP/cache/traversal 방어
- PostgreSQL 16/17/18 지원

DB lock 순서는 하나의 범용 options interface로 평탄화하지 않고 entry point별 executable golden으로 고정한다.

```text
idempotent create:
request advisory lock → first snapshot/schema pin → creation-order sidecar lock
→ relation target locks → record/relation write → revision/audit/outbox/idempotency

update:
schema pin → collection advisory locks(UUID order)
→ row locks(collection ID, record ID order) → relation write → revision/audit/outbox

delete(Read Committed):
schema pin → participant collection advisory locks(UUID order)
→ graph discovery → ordered row locks → relation storage/base delete → revision/audit/outbox

schema apply:
schema advisory lock → schema_state FOR UPDATE → validated catalog
→ DDL/metadata/audit/version/notify

admin login/reset:
network admission short transaction → email failure-signal short read → bounded delay(no resource held)
→ KDF slot → candidate hash read(no row lock)
→ real/dummy KDF outside transaction → administrator FOR UPDATE + hash/active recheck
→ session insert → sorted rate-limit update/cleanup
```

공통 module은 advisory-key 계산, UUID/record 정렬 comparator, retry 판정만 제공한다. 각 deep entry point가 자신의 isolation과 순서를 소유하며 caller에게 mode/options bag을 노출하지 않는다.

## 5. 주석과 코드 삭제 원칙

주석은 단계별 리팩터링과 함께 제거한다. 먼저 주석만 대량 삭제하지 않는다.

삭제 대상:

- 코드가 그대로 말하는 동작 설명
- 함수명이나 타입을 반복하는 문장
- 이미 끝난 구현 계획, 현재 코드와 맞지 않는 TODO 성격의 문장
- boolean state나 호출 순서를 보충 설명하는 주석
- private helper의 자명한 입력·출력 설명

유지 대상:

- PostgreSQL snapshot, lock, deferred trigger, index/null ordering의 이유
- 보안상 fail-closed 선택과 secret 취급 이유
- 호환성, cursor rollout, migration checksum 제약
- 크기·깊이·cardinality·retry budget의 근거
- 운영자가 알아야 하는 위험과 rollback 조건
- 외부 interface의 비자명한 오류·성능 계약

“무엇”은 이름과 타입으로 표현하고, 주석은 “왜”만 설명해야 한다. 역사적 migration 주석은 checksum 보존을 위해 수정하지 않는다.

현재 runtime comment-only/comment-like 886줄에서 hard target 600줄 이하는 최소 286줄(32.3%) 삭제, stretch 500줄은 386줄(43.6%) 삭제다. 주석 감소 목표는 자명한 문장을 먼저 지우는 숫자 놀이로 달성하지 않는다. shallow seam을 삭제할 때 그 seam의 doc comment도 함께 사라지게 하고, 남은 600줄 이하는 rationale/contract audit으로 한 줄씩 분류한다. 특히 advisory lock의 snapshot 시점, nullable DESC index, fail-closed JSON, token redaction, migration checksum 근거는 줄 수와 관계없이 유지한다. generated/minified output과 historical migration은 이 runtime 주석 KPI로 억지로 수정하지 않는다.

심화 검사에서 바로 확인된 zero-behavior 삭제/축소 inventory:

- `internal/api/health.go`: 사용되지 않는 `readinessFunc`와 method
- `internal/query/expand.go`: 사용되지 않는 `expandRecords`; `executeExpandPlan`의 사용되지 않는 collection 인자
- `internal/query/expand_plan.go`: 사용되지 않는 `isExpandBudgetError`
- `internal/query/service.go`: 사용되지 않는 `inTx`, `retryableTransactionError`
- `internal/query/relation_filter_sort.go`: 오류를 그대로 반환하는 `relationPlanningErrorForCursor`와 수제 `strconvItoa`
- `internal/recordops/service.go`: `recordETag`의 사용되지 않는 version 인자
- `internal/doctor/relations.go`: 즉시 버려지는 `metadataMatches` 대입과 항상 nil인 `classifyRelations` error result
- `internal/query/expand_measure.go`: 분기에서 항상 덮어쓰는 `valueSize` 초기화
- `internal/cli/run.go`: response close와 출력 오류 처리 정책이 흩어진 6곳

반대로 다음은 생김새가 같다는 이유만으로 합치지 않는다.

- API와 mutation kernel의 request ID 검증은 서로 다른 trust boundary의 defense-in-depth다. typed request metadata가 두 경계를 대체하지 않는 한 6줄 중복을 위한 새 package를 만들지 않는다.
- schema/query/relation의 clone helper와 enum switch는 소유 데이터와 fail-closed 의미가 다르면 그대로 둔다.
- `config.LoadEnv`, cursor codec, JSON Unicode scanner, catalog hydration처럼 길지만 하나의 protocol/algorithm을 응집해 숨기는 함수는 복잡도 수치만으로 분해하지 않는다.

## 6. 목표 module 지도

### 6.0 Design It Twice 비교와 선택

첫 번째 대안은 sealed `Operation[R]`와 `Do(ctx, kernel, subject, operation)` 한 entry point다. 표면이 가장 작고 새 operation이 interface를 늘리지 않는 장점이 있다. 그러나 schema/read/mutation의 서로 다른 transaction 의미가 중앙 operation algebra로 모여 discoverability와 locality를 잃고, constructor가 tagged options bag으로 퇴화하면 금지한 `Execute(any)`의 generic 버전이 된다. 채택하지 않는다.

두 번째 대안은 `OpenCollection` capability, credential/access port, scalar field behavior registry를 중심으로 미래 RBAC·새 field type·bulk/export 확장성을 최대화한다. 실제 새 scalar 8종은 서로 다른 codec/default/SQL behavior를 가지므로 private conformance registry는 유효할 수 있다. 하지만 public auth/RBAC과 새 field type은 이번 범위 밖이다. 두 번째 production policy나 실제 새 field type 없이 interface부터 만들지 않는다.

세 번째 대안은 common caller를 기준으로 `ReadKernel`, `RecordKernel`, `SchemaKernel` 세 intent module과 vertical HTTP/UI adapter를 둔다. caller는 한두 entry point만 알고 각 module은 자신의 고정 transaction/lock/response 계약을 숨긴다. 현재 코드에서 가장 큰 삭제량과 locality를 동시에 얻으므로 이 안을 기본으로 선택한다.

최종안은 세 번째 대안을 기반으로 두 번째 대안의 다음 요소만 가져오는 hybrid다.

- admin/query/record 사이의 중복 principal translation은 하나의 typed `Subject`로 수렴한다.
- schema operation receipt/ledger는 HTTP가 아니라 schema transaction과 같은 module이 소유한다.
- scalar behavior registry는 R2 spike에서 기존 cross-layer switch를 실제로 줄이고 net LOC가 음수일 때만 private implementation으로 채택한다.
- 현재 relation traversal만 보호하는 sole-production-adapter `ProjectionPolicy`는 미래 RBAC seam으로 보존하지 않는다. root/read/mutation까지 같은 pinned transaction에서 다루는 두 번째 실제 policy가 생길 때 `AccessPlan`을 새로 설계한다.
- 범용 command union, public plugin registry, repository/dialect port는 만들지 않는다.

### 6.1 데이터 커널

#### `ReadKernel`

외부 interface는 `List`와 `Get` 두 entry point로 수렴한다.

```go
List(ctx, ListRequest) (RecordPage, error)
Get(ctx, GetRequest) (Record, error)
```

implementation은 다음을 숨긴다.

- scalar/relation filter와 sort
- cursor decode, anchor reload, privacy
- root membership와 pagination watermark
- limit+1, multi-relation hydration, expand
- 정확한 4MiB envelope와 next cursor
- query plan telemetry

내부 값 `RootPlan`, `OrderSpec`, `PageAssembler`를 두되 외부 interface에는 노출하지 않는다. scalar와 relation은 별도 executor가 아니라 join/predicate가 다른 `RootPlan`이다.

#### `RecordKernel`

Create/Update/Delete의 서로 다른 의도는 유지한다. 하나의 거대한 `Execute(Command)`로 합치지 않는다. 대신 catalog pinning, retry, relation lock/storage, size check, revision/audit/outbox/idempotency commit을 implementation으로 숨긴다.

#### `RelationKernel`

inline FK와 edge table이라는 두 실제 storage adapter만 private seam으로 유지한다. production caller가 `Decode → Lock → Replace → Hydrate` 순서를 직접 조립하지 못하게 한다. delete graph는 깊은 module로 유지하되 storage와 committed artifact orchestration을 `RecordKernel.Delete` 뒤로 감춘다.

#### `SchemaKernel`

```go
Preview(ctx, Change) (Plan, error)
Apply(ctx, ApplyIntent) (ApplyResult, error)
OperationStatus(ctx, OperationRef) (OperationReceipt, error)
ListOperations(ctx, OperationQuery) (OperationPage, error)
Catalog(ctx) (Catalog, error)
```

`Change`는 create/update/delete constructor로만 만들 수 있는 합 타입이다. planner는 production implementation 하나인 private concrete `Planner.Plan(catalog, change)`로 둔다. `PlanBuilder`, `catalogPlanBuilder`, type assertion adapter를 삭제한다.

`ApplyIntent`도 sealed domain 값이다. `LegacyApply(change, expectedPlan, actor)`와 `GuardedApply(change, expectedPlan, actor, clientOperationID, idempotencyKey)` 두 constructor만 제공하고 범용 options bag은 두지 않는다. guarded constructor가 typed input에서 canonical request hash를 내부 계산해 intent에 보관하며 caller-supplied hash를 신뢰하지 않는다. `OperationRef`는 operation ID와 현재 actor scope만 담으며, `OperationStatus`는 owner-scoped committed/failed/running/unknown receipt만 반환한다.

`OperationQuery` 역시 constructor로만 만든 owner scope, `limit`, opaque cursor를 담는다. 이번 UI에는 status filtering 요구가 없으므로 미사용 filter surface를 만들지 않는다. limit은 기본 20, 최대 100이고 정렬은 `(created_at DESC, operation_id DESC)`로 고정한다. `OperationPage`는 redacted summary와 다음 cursor만 노출하며 raw request/hash/SQL/error를 포함하지 않는다. cursor는 owner scope를 HMAC으로 결합하고 만료시킨다. claim/reconcile/cleanup은 `SchemaKernel`의 private/background implementation이고 HTTP에 별도 orchestration interface를 노출하지 않는다.

Schema planner가 `ExpectedPhysicalSchema` manifest를 생성하고 doctor도 같은 manifest를 소비한다. schema 생성 지식과 drift 진단 지식을 두 번 작성하지 않는다. manifest는 모든 PostgreSQL 객체를 독점한다고 가정하지 않고 `Required` 객체와 PgBase-owned namespace/name prefix를 구분한다. 사용자 추가 index는 허용하면서 PgBase-owned orphan constraint/index/trigger/function은 탐지해야 한다.

validated catalog는 version마다 정확히 한 번 integrity validation, relation binding, incoming index, delete graph, physical manifest를 compile한 immutable capability다. request pin은 전체 clone/revalidation이 아니라 O(1) version reference를 얻고 transaction의 schema share lock이 수명을 보장한다.

schema operation receipt는 `SchemaKernel`의 implementation이다. bounded client-known operation ID를 DDL 전에 claim하고, committed receipt는 DDL/metadata/audit/version과 같은 transaction에서 기록하며, rollback/commit-ACK loss/process death는 ledger가 reconcile한다. HTTP는 이 결과의 deadline과 wire adapter일 뿐 durable state를 소유하지 않는다.

#### PostgreSQL retry primitive와 transaction ownership

`CollectionTxRunner`라는 새 상위 module/interface는 만들지 않는다. query/record/schema의 동일한 SQLSTATE 판정, capped jitter/backoff, cancellation, retry observation만 PostgreSQL 전용 private primitive로 공유한다. read, create, update, delete, schema apply는 각 deep entry point가 isolation, pre-snapshot lock, catalog pin과 lock 순서를 직접 소유한다. retry primitive가 `TxOptions`, lock mode, catalog callback을 받는 options bag으로 커지면 즉시 중단한다.

### 6.2 백엔드 표면

#### Auth module

```text
adminauth.New(pool, Config{TokenSecret, SetupToken, SessionTTL, Logger})
```

Argon2id, clock, KDF semaphore, Postgres store, token hashing, maintenance cleaner를 implementation으로 내린다. issued/authenticated session 결과가 CSRF를 제공하게 해 HTTP adapter가 token secret을 알지 못하게 한다.

#### HTTP transport module

```text
NewRouter(Modules, HTTPConfig) (http.Handler, error)
```

private route registry가 `Public`, `SameOrigin`, `AdminRead`, `AdminMutation` access policy와 `AtomicJSON`, `Passthrough`, 장기 작업 response mode를 소유한다. 모든 route는 정확히 하나의 정책을 가져야 한다.

#### CLI module

```text
Execute(ctx, args, envLookup, IO{In, Out, Err}) int
```

`CommandSet`, mutable stdin/terminal globals, 세 번의 DB bootstrap을 삭제한다. private command table과 production/test terminal adapter만 남긴다.

#### Doctor module

```text
Schema manifest ─┐
Catalog metadata ┼─> ExpectedPhysicalSchema
Postgres catalog ┘─> PhysicalSnapshot
Expected + Snapshot -> deterministic Report
```

catalog query 수를 collection/field 수와 무관한 고정 상한으로 만들고, 비교는 pure implementation으로 분리한다.

#### Observability module

caller는 operation만 제공하고 component는 operation에서 파생한다. operation/outcome 허용 조합은 한 implementation에서 검증한다. metric 이름과 label은 변경하지 않는다.

### 6.3 관리자 UI와 운영

#### `AdminLocation`

typed route parse/format, history, popstate, canonicalization, resource identity와 outlet remount를 숨긴다. browser history와 memory history의 두 adapter를 사용한다. 현재 7개 route를 위해 React Router는 도입하지 않는다.

#### `AdminBackend`

`SessionPort`, `SchemaPort`, `RecordPort`, `SystemPort`와 production HTTP adapter, scripted in-memory test adapter를 둔다. view에서 endpoint, headers, CSRF, ETag, idempotency key, checksum, raw HTTP error를 제거한다. in-memory adapter는 SQL/filter/schema engine이나 HTTP status/header를 재구현하지 않고 workflow 결과만 script한다.

`PlannedChange`, `VersionedRecord`, `CreateAttempt`는 opaque 값으로 만들어 다른 collection name과 checksum을 조합하거나 ETag lifecycle을 잘못 다룰 수 없게 한다.

#### `CollectionDesigner`

create/edit/delete/default/relation 계획 상태를 discriminated union으로 통합한다. boolean state 묶음, stale preview, destructive confirmation, 늦게 도착한 요청 결과를 implementation이 소유한다.

#### `RecordWorkspace`

record list/editor, tri-state draft, lossless codec, partial PATCH, stale recovery, 현행 next-cursor 상태와 relation picker를 숨긴다. Previous를 위한 cursor back-stack은 F0에서 이 interface를 확장한다. 범용 `useAsync`나 `useForm` 같은 shallow helper는 만들지 않는다.

#### `RestoreDrill`

backup workflow의 긴 inline seed/dump/assert shell을 test/support code인 `tests/restore`의 실행 가능한 검증 module로 옮긴다. 이는 runtime LOC에 포함하지 않는다. release mode는 immutable `image@sha256:...`를 필수 input으로 받고 tag나 source-built local binary를 거부한다. registry permission과 publish side effect는 workflow에 명시적으로 남긴다.

## 7. 작업 의존성

```text
R0 계약 고정 → R1 작은 확정 결함

R1 → R2 SchemaKernel + compiled catalog + private retry primitive
R2 → R2b schema operation ledger/receipt
R1 → R7 Auth/App, R9a AdminLocation, R10a candidate/gate infrastructure

R2 → R3 ReadKernel → R4 pagination/cursor v5 implementation
                    └→ R5 relation sort/index
R2 → R6 RecordKernel + RelationKernel
R2 → R8 Doctor physical manifest
R2b → R7 HTTP schema receipt/deadline adapter

R7 → R8 CLI + Migration + Doctor + Observability
R7 → R9a AdminBackend + UI modules
R4 implementation + R10a gate infrastructure
→ R4 release A0/M/A/B/C rollout

R2~R10 완료 + R4 rollout 완료
→ R11 compatibility/shallow module 삭제와 정량 마감
→ F0~F3 기능 추가
→ R12 release-equivalent 최종 실행

R4의 release A0/A/B/C는 R10a가 먼저 구현한 같은 gate definition을 각각 실행한 뒤 별도 hold point에서 fleet/capability/TTL 조건을 확인한다. R12는 그 definition을 뒤늦게 만드는 단계가 아니라 최종 feature candidate에 다시 실행해 전체 결과를 마감하는 단계다.
```

R2 이후 데이터 트랙, R7 백엔드 트랙, R9a 프런트 트랙은 interface 충돌이 없는 범위에서 병렬 진행할 수 있다. HTTP의 schema adapter는 `SchemaKernel` interface가 확정된 뒤 연결한다.

### 주요 시작 파일

| 묶음 | 현재 implementation 시작점 |
|---|---|
| R1 numeric | `internal/jsonvalue/json.go`, `internal/jsonvalue/json_test.go` |
| R1 legacy default | `internal/schema/defaults.go`, `internal/schema/planner.go`, 관련 schema tests |
| R1 auth timing/lockout | `internal/adminauth/service.go`, `internal/adminauth/store.go`, `internal/adminauth/service_test.go`, `internal/api/admin_handlers.go` |
| R1 doctor drift | `internal/doctor/checks.go`, `internal/doctor/relations.go`, 관련 live tests |
| R1 UI identity | `ui/admin/src/app.tsx`, collection/records pages와 tests |
| R2 schema/transaction | `internal/schema/types.go`, `planner.go`, `service.go`, `listener.go`, `cache.go`; query/record/schema retry loops |
| R3 read/query | `internal/query/service.go`, `root_page.go`, `relation_list.go`, `relation_filter_sort.go`, `compiler.go`, `expand.go` |
| R4 cursor/membership | `internal/query/cursor.go`, `root_page.go`, `internal/recordops/service.go`, schema planner/store/doctor, 새 system migration과 resumable backfill |
| R5 sort/index | `internal/query/compiler.go`, `relation_filter_sort.go`, `internal/schema/planner.go`, doctor index checks |
| R6 mutation/relation | `internal/recordops/service.go`, `delete.go`, `relation.go`; `internal/relation/{locks,storage,delete,delete_execute}.go` |
| R7 auth/HTTP/app | `internal/adminauth/**`, `internal/api/router.go`, middleware/handlers, `pgbase/app.go` |
| R8 운영 backend | `internal/migrate/**`, `internal/doctor/**`, `internal/cli/**`, `internal/observability/**` |
| R9a 관리자 UI | `ui/admin/src/app.tsx`, `api/**`, `auth/session.tsx`, collection/record pages |
| R10 Ops | `deployments/**`, `.github/workflows/**`, `tests/e2e/**`, `ui/admin/e2e/**` |

## 8. 단계별 실행 계획

### R0 — Characterization, 기준선, 실패 재현

변경 내용:

- unit와 PostgreSQL integration suite를 명시적으로 분리한다.
- integration job에서 `PGBASE_TEST_ADMIN_URL` 미설정은 skip이 아니라 실패하게 한다.
- 현재 로컬 `go test -json`의 262 skipped tests를 package/test 목록으로 저장하고, unit command에는 live test가 섞이지 않으며 integration command는 환경 없이 시작조차 못 하게 한다.
- PostgreSQL 16/17/18에서 전체 live test와 race 기준선을 잡는다.
- HTTP wire, CLI 출력, metric exposition, schema plan/checksum, cursor v4 golden을 고정한다.
- golden은 route/status/header multiplicity/cookie flags/error shape를 semantic하게 비교한다. request ID, 시간, expiry, duration만 명시적 placeholder로 canonicalize하고 normalizer 자체를 테스트한다.
- auth는 malformed/unknown/real candidate의 network/global admission, KDF slot, short hash read, real/dummy KDF, administrator hash/active recheck, session insert, limiter cleanup 순서를 executable golden으로 고정한다.
- query/record/relation benchmark와 fixed large fixture EXPLAIN 기준을 만든다.
- 현재 결함의 failing regression을 먼저 추가한다.
- 추적 파일만 계산하는 runtime/test/generated LOC 측정 명령을 저장소에 둔다.
- Go 1.26.7에 맞춘 lint toolchain과 최소 blocking set `unused`, `unparam`, `ineffassign`/`wastedassign`, production `errcheck`를 pin한다. broad style/exhaustive/gosec 결과는 PostgreSQL bit-cast, jitter, cookie policy 같은 의도적 항목을 검증한 allowlist로 관리한다.
- interface/export/constructor/comment/page-state/long-file 지표를 machine-readable baseline으로 남긴다.

필수 failing regression:

- 늦게 commit된 create와 keyset traversal
- legacy oversized default carry-forward update
- bare relation sort의 불필요한 join/100k budget
- relation filter + root DESC index path
- 실존/비실존 로그인 동시 요청
- 동일 관리자에 5회 실패 뒤 올바른 credential도 차단되는 account-lockout DoS
- KDF slot 대기 중 DB transaction/connection과 실존 계정 row lock이 유지되는 문제
- doctor timestamp/index/function drift
- DDL 30초 초과 후 결과 재확인
- collection/records popstate state leak
- Compose grace가 shutdown timeout보다 짧은 override
- 현재 발견된 E2E wall-clock poll 7개와 Compose `TestMain` startup이 parent context 취소 뒤 남는 문제
- background runner가 error 없이 예상 밖 종료했는데 `App.Run`이 정상 종료로 보고하는 문제
- plain multi-relation List/Get이 최종 4MiB trim 전에 모든 root를 무제한 hydrate하는 문제
- cascade delete가 최대 10,000 record의 base row/revision을 N+1로 읽는 문제
- cursor anchor-only 유효 payload fuzz seed와 relation DESC/null-boundary/EXPLAIN 공백

완료 기준:

- production diff 0
- `go test -json`의 모든 `Action=skip`을 명시적 allowlist와 대조하고 integration DB skip은 0
- 현행 wire/checksum/metric fixture 승인
- 모든 현재 결함이 재현되거나 재현 불가 이유가 문서화됨
- Playwright는 onboarding 전용 격리 backend와 authenticated worker fixture를 분리하고, unique resource cleanup 또는 명시적 serial 정책을 가짐

### R1 — 작고 독립적인 확정 결함 수정

각 finding을 독립 commit으로 처리한다.

첫 commit은 동작을 바꾸지 않는 hygiene stack이다. 위 deletion inventory의 unused wrapper/symbol/parameter/result/assignment를 제거하고, CLI I/O 처리와 `cmd/pgbase`의 실행되지 않는 `defer`를 명시적 종료 순서로 바꾼다. replacement 없이 새 helper/package는 만들지 않는다.

1. `ValidPostgresNumber`가 실제 JSON number lexeme만 허용하도록 수정한다.
2. unchanged legacy default만 stored normalization으로 carry-forward하고, 신규·변경 default는 strict 제한을 유지한다.
3. 선행 network admission transaction과 email failure-signal read가 connection을 모두 반환한 뒤 KDF semaphore를 획득한다. semaphore를 얻기 전에는 candidate-hash DB borrow를 시작하지 않는다. 그 다음 candidate hash를 short read로 얻고 connection을 반환한 뒤 real/dummy Argon2를 transaction 밖에서 정확히 한 번 수행한다. 성공한 경우에만 short transaction에서 administrator row를 잠그고 active/hash가 바뀌지 않았음을 재확인한 뒤 session을 발급한다. hash가 바뀌면 bounded fail/retry로 끝낸다.
4. login limiter 상태 머신을 하나로 확정한다. network bucket은 현재처럼 요청 전에 short transaction으로 attempt를 예약하고 `20 requests / 15분`을 넘으면 KDF 전에 hard 429를 반환한다. email bucket은 어느 IP에서든 실패한 검증 뒤에만 증가하며 account 존재 여부와 독립적인 normalized candidate key를 사용한다. 이 bucket은 절대 pre-verification rejection을 만들지 않고, 직전 15분 실패 수가 0~2면 0, 3이면 250ms, 4이면 500ms, 5 이상이면 최대 1초의 context-aware delay와 alert signal만 만든다. email counter는 동일한 short read로 읽고 connection을 반환한 뒤 delay하며, delay 동안 DB connection/transaction/row lock/KDF slot은 0이다. 이후 모든 admitted 요청은 real/dummy KDF를 정확히 한 번 거치므로 다른 network에서 온 올바른 credential은 공격자가 email bucket을 계속 갱신해도 최대 1초의 configured delay 뒤 검증된다. 성공은 email bucket을 삭제하고 자신의 network reservation 하나만 되돌리며, 실패는 network reservation을 유지한다. 같은 abusive network가 20회 한도에 걸린 경우의 429는 명시적으로 유지한다. setup limiter 변경은 이 finding에 섞지 않는다.
5. doctor가 base timestamp `NOT NULL`, btree method, direction, null option, opclass drift를 탐지하게 한다.
6. 현 router의 keyed outlet과 resource change reset으로 route state leak을 최소 수정한다. `AdminLocation` module 교체는 R9에서 한 번만 수행한다.
7. Compose 기본값과 override 모두 `stop grace >= shutdown + 5s`를 만족하도록 설정 interface와 policy test를 추가한다.
8. 현재 확인한 7개 E2E wall-clock poll(`e2e_test.go` 3, `relation_test.go` 1, `shutdown_test.go` 3)을 하나의 context-aware test-support poll로 교체하고 Compose `TestMain` startup에도 bounded context를 둔다. `rg` guard로 새 raw poll 유입을 막는다.
9. 감사 문서의 잘못된 종결 상태와 release gate 문서 drift를 정정한다.
10. background runner의 예상 밖 nil 반환을 operational error로 취급한다. 정상 종료는 app shutdown context 취소와 일치하는 경우뿐이다.

완료 기준:

- malformed number corpus와 fuzz property 통과
- unchanged legacy plan SQL/checksum byte equality
- malformed/unknown/real candidate가 요청당 KDF 1회와 같은 global semaphore wave를 사용함. network/email short DB 단계는 KDF wait 전에 종료되고, KDF slot 대기 중 acquired DB connection/transaction/row lock과 candidate-hash lookup은 0
- 동시 known/unknown 5-request fixture가 account-existence별 직렬화 차이를 만들지 않음
- password reset과 login race에서 old hash로 검증한 session이 살아남지 않고, sorted rate-limit cleanup, raw token 비로그·비영속 계약 유지
- target account에 5회 실패 후 다른 network의 올바른 credential은 추가 지연 1초 이내에 검증되고, 같은 abusive network는 20회/15분 hard limit을 유지함; email history가 같은 real/unknown candidate의 public 결과와 delay class는 동일하고 multi-instance에서도 동일
- BRIN/hash/mixed direction/null/opclass drift가 모두 finding
- collection A→B, records A→B, 같은 collection의 record A→B, new↔edit 전환에서 이전 상태가 렌더·전송되지 않음
- server checksum 409는 UI 성공 조건으로 세지 않음
- default/override/malformed Compose profile에서 5초 이상 grace margin 유지
- parent context 취소 후 모든 E2E poll/startup이 즉시 종료
- background runner의 nil/error 조기 종료가 sibling cancel과 non-nil `Run` error로 이어짐
- selected blocking lint finding 0

### R2 — `SchemaKernel`, validated catalog, transaction foundation

변경 내용:

- create/update/delete를 `Change`와 단일 planner로 통합한다.
- apply의 lock/timeout/state/catalog/replan/DDL/audit/version/notify/cache 흐름을 한 transaction template로 만든다.
- SQLSTATE/capped jitter/cancellation/observation만 소유하는 private retry primitive를 만든다. schema apply는 자신의 transaction을 직접 소유하고, query/record의 old retry loop는 R3/R6 caller 이동과 함께 삭제한다.
- listener reload, request-held `FOR SHARE`, post-apply exclusive load는 고정된 entry point를 유지하되 동일한 catalog validation implementation을 사용한다.
- catalog를 version당 한 번 validate/compile하고 immutable capability만 cache에 publish한다. request pin은 O(1)이고 전체 clone/revalidation을 반복하지 않는다.
- validated catalog와 authenticated wire snapshot을 version당 한 번 canonical pre-serialize한다. 정확한 16 MiB cap을 두고, schema preview/apply가 다음 catalog를 cap 위로 만들면 stable `422 catalog_limit_exceeded`로 거부한다. 기존 DB가 이미 cap을 넘으면 migration/doctor preflight가 rollout을 막고 CLI maintenance로 축소한 뒤 재개한다. 이는 unbounded atomic response를 없애기 위한 명시적 compatibility tightening이다.
- `ExpectedPhysicalSchema` manifest를 생성한다.
- scalar kind spike가 8개 field type에 흩어진 codec/default/filter/order/physical switch와 LOC를 실제로 줄이는지 측정한다. public registry/interface나 net-positive wrapper이면 채택하지 않는다.
- public HTTP shape와 plan checksum은 그대로 유지하되, 승인된 catalog cap 초과만 새 stable `422 catalog_limit_exceeded` contract로 versioning한다.

완료 기준:

- retry 판정/backoff primitive 1개, schema apply의 별도 raw retry loop 0
- catalog validation implementation 1개; query/record의 얕은 `CatalogEnsurer` interface는 R3/R6 caller 이동과 함께 삭제
- planner interface/type assertion 0, concrete planner 1개
- catalog version당 compile 1회, request catalog pin/lookup O(1), invalid catalog publish 0
- admitted catalog의 canonical wire snapshot 16 MiB 이하, version당 serialize 1회, cap을 넘는 future apply 0
- legacy oversized catalog는 배포 전에 doctor/migration preflight로 식별되고 축소 절차 없이 새 binary가 ready가 되지 않음
- create/update/delete apply lock 순서 동일
- invalid catalog가 readiness green이나 cache publish로 이어지지 않음
- manifest가 required object와 PgBase-owned namespace/prefix를 구분하고 사용자 추가 index false positive 0
- schema apply와 record pin의 PostgreSQL 16/17/18 concurrency gate 통과
- R2 replacement stack의 new+caller migration+old deletion net delta와 interface/export delta를 보고

### R2b — schema operation ledger와 recovery contract

durable receipt는 schema transaction과 같은 locality에서 먼저 완성하고, 아직 HTTP에 노출하지 않는다.

- append-only system migration으로 bounded operation claim/receipt를 추가한다.
- client-known operation ID와 idempotency key, actor/resource scope, canonical request hash, plan checksum/base version을 별도 short durable claim transaction에서 기록한다. claim은 lease와 fencing generation을 가진다.
- DDL transaction은 claim row를 `FOR UPDATE`하고 owner/fence/request hash를 검증한 뒤 schema advisory lock과 `schema_state` lock을 같은 고정 순서로 획득한다.
- committed receipt는 schema DDL/metadata/audit/version과 같은 DDL transaction에서 기록한다.
- rollback은 conditional follow-up transaction에서 failed로 기록하고, commit ACK 단절/process death는 running/unknown으로 남겨 audit/version과 재조정한다. reconciler도 claim row → schema lock 순서를 그대로 사용하며 stale owner는 fencing generation으로 쓰지 못한다.
- same identity+same request는 같은 receipt, same identity+different request는 conflict다.
- retention, payload 크기, multi-instance `SKIP LOCKED` cleanup/reconcile budget을 고정한다.
- owner-scoped history를 `(created_at DESC, operation_id DESC)` keyset으로 읽는 `ListOperations`를 ledger와 같은 module에 둔다. 기본 20/최대 100개, signed owner-scoped cursor, redacted summary, 256 KiB serialized page cap을 고정한다.
- R7 전에 `GET /api/admin/schema-operations/{id}`와 `GET /api/admin/schema-operations?limit=&cursor=`의 method/header/body/status/error/ownership contract를 승인한다. 새 UI는 응답 전부터 아는 operation ID를 보내야 하며, legacy headerless apply의 기존 status/body는 유지한다.

완료 기준:

- crash, cancel, rollback, commit-ACK loss, duplicate/conflicting claim, restart reconcile live tests
- claim/DDL transaction 경계, owner/fence/hash mismatch, expired lease takeover, reconciler-vs-apply deadlock/liveness live tests
- raw SQL/input/secret 미저장, actor가 다른 operation 조회 불가
- migration은 11절의 `P0 → M → feature release` 절차로만 적용하고, 적용 뒤 rollback target은 P0뿐이다.
- 다른 actor의 단건/목록 조회가 동일한 not-found shape이고, limit 0/1/100/101, cursor 변조·owner-scope 재사용·만료, 같은 timestamp tie-break와 256 KiB cap을 검증
- R7 HTTP adapter 없이도 `SchemaKernel`에서 receipt 단건/목록 조회와 reconciliation 검증 가능

### R3 — `ReadKernel`, `RootPlan`, `OrderSpec`, `PageAssembler`

첫 structural commit은 byte-for-byte behavior-preserving refactor만 수행한다. bounded incremental hydration은 public behavior가 같은지 별도 correctness commit에서 검증한다.

변경 내용:

- scalar/relation/expand 진입을 `ListRequest` 한 경로로 통합한다.
- `ListRelationAwareUsage` 같은 내부 plan 분류 누출을 삭제한다.
- cursor decode/anchor reload, row loop, hydration 측정, envelope finalization을 각각 한 implementation으로 만든다.
- `OrderSpec`이 SQL expression, cursor type, direction, null placement, anchor/privacy, index hint를 소유한다.
- root 경계를 먼저 고정하고 expand는 public representation만 바꾸게 한다.
- 현재 plain List/Get이 최종 4MiB trim 전에 page 후보 전체의 multiple relation을 materialize하는 경로를 없앤다. bounded batch로 base row를 읽고 후보를 순차 hydrate하며, 메모리에는 방출 root와 pagination 판단에 필요한 한 개의 lookahead만 유지한다. oversized 후보 때문에 실제 hydrate 총량은 더 클 수 있지만 retained heap은 bounded다.
- plain List/Get에는 새 budget error를 만들지 않는다. 기존 4 MiB page/cursor/wire 의미를 유지하면서 다음 방출 가능 row를 찾을 때까지 bounded batch를 계속 처리한다. 기존 expand budget만 기존 안정 오류 계약을 유지한다.
- validation detail과 principal/subject 변환을 공통 domain 값으로 수렴해 query 전용 error facade와 중복 principal struct를 삭제한다.

완료 기준:

- root row loop 1개
- cursor decode/anchor 경로 1개
- `ORDER BY`와 keyset renderer 각각 1개
- page finalizer 1개
- scalar/relation/expand가 같은 root request에서 동일 IDs/cursor 생성
- 정확한 4MiB 및 oversized record 계약 유지
- root limit을 키워도 retained heap이 `emitted roots + 1 lookahead` representation budget을 넘지 않음; 총 순차 hydration 수와 retained hydration 수를 별도 측정
- single/multiple relation field 수와 expand step을 늘린 fixed fixture에서 `pgx` tracer 기반 query-count 상한 통과
- query의 raw retry loop와 얕은 `CatalogEnsurer` interface 삭제
- `internal/query` runtime 4,850 LOC 이하, stretch 4,650 LOC; 파일 수는 locality 결과로만 보고

### R4 — pagination membership와 cursor v5

silent omission을 없애기 위해 collection별 transactional creation ordinal을 도입한다.

설계:

- `pgbase_meta`에 collection counter와 `(collection_id, record_id, ordinal)` sidecar를 둔다.
- base table에는 column을 추가하지 않는다. 현재 1,597 user fields + 3 system columns인 PostgreSQL 1,600-column 계약을 그대로 보존한다.
- release A의 모든 create는 guarded/legacy header 여부와 capability 상태에 관계없이 먼저 collection creation-fence/counter row를 잠그고, 그 뒤 capability를 읽으며 lock을 commit까지 유지한다. ready이면 `UPDATE ... RETURNING`으로 ordinal을 예약한다.
- base insert와 sidecar insert를 같은 transaction에서 수행한다. delete/cascade/backfill은 base row → sidecar의 동일한 lock 순서와 conditional insert/delete를 사용해 delete-before/after 어느 interleaving에서도 orphan이나 resurrected sidecar를 만들지 않는다.
- root query는 sidecar를 join하고 첫 page가 본 counter high-water로 traversal membership을 고정한다.
- 후속 page는 `ordinal <= high_water`를 적용한다.
- cursor v5는 collection ID, catalog version, stable field IDs, canonical filter/root-query fingerprint, total `OrderSpec`과 null placement, anchor, high-water, principal/policy scope digest, expiry를 서명한다.
- membership은 고정하지만 기존 record의 sort value 변경까지 snapshot으로 고정하지는 않는다고 interface에 명시한다.

호환 rollout:

1. release A0(P0): 정확한 pre/post migration 두 history만 인식하고 feature를 off로 유지하는 preparatory binary를 전 fleet에 배포한다. v4/v5 decode와 v4 issue는 가능하지만 sidecar를 요구하지 않는다.
2. migration M: 한 designated job이 A0 artifact의 `migrate up --through <version>`으로 checksum이 고정된 additive sidecar/counter/capability migration 하나만 적용하고 history/checksum을 read-back한다. application instance의 auto-migrate는 끈다.
3. release A: post-migration DB만 ready로 인정하며, 모든 create가 guarded/legacy header 및 capability 상태와 무관하게 creation fence를 잡는다. v4/v5 decode, v4 issue, capability off를 유지한다.
4. durable lease 확인만으로 진행하지 않는다. A0 instance를 완전히 drain하고 이미 시작한 pre-A transaction도 0임을 확인한 뒤, collection creation fence 아래 cutover snapshot/count와 counter를 초기화하고 dual-write capability를 켠다.
5. system migration transaction과 분리된 resumable backfill이 cutover snapshot의 기존 record를 `(created_at,id)` 순서로 채운다. 이후 create는 reserved counter 다음 ordinal을 사용한다.
6. backfill은 collection별 checkpoint와 validation을 기록하고 중단·재시작 가능해야 한다.
7. 최대-width 1,600-column collection을 포함해 sidecar completeness가 확인되면 durable DB capability를 ready로 전환한다.
8. release B: capability가 ready인 DB에서만 feature flag로 v5 issue와 high-water read를 활성화한다.
9. 모든 instance가 release B 이상임을 확인하고 15분 cursor TTL을 drain한다.
10. release C: v4 decode를 제거한다. migration 뒤 cutover 전 rollback target은 A0, cutover 뒤는 feature-off release A, C 이후는 dual-decoder release B로 고정한다.

schema planner/store/doctor는 새 collection의 sidecar maintenance와 capability 상태를 함께 이해해야 한다. old record/schema writer는 durable writer-version lease/capability gate로 차단한다. system migration runner가 모든 dynamic row backfill을 한 transaction에서 수행하게 만들지 않는다.

release A0, A, B, C는 각각 독립 deployable artifact이며 매번 R10a에서 구현한 동일-digest release-equivalent gate를 통과한다. A→B는 dual-write/backfill/capability ready hold point, B→C는 전 fleet B 이상 확인과 cursor TTL drain hold point를 통과하기 전 진행하지 않는다.

완료 기준:

- delayed commit test에서 traversal membership이 첫 page high-water와 정확히 일치
- 늦은 commit은 같은 traversal에 섞이지 않고 새 traversal에서 나타남
- scalar/relation/expand가 같은 high-water 사용
- cursor가 collection/catalog field identity/sort/null/filter/high-water/principal scope에 결합되고 다른 scope에서 재사용 불가
- v4/v5 mixed fleet와 TTL drain 검증
- valid anchor-only cursor fuzz seed를 추가하고 sort value가 없는 유효 anchor payload를 거부하지 않음
- populated 1,600-column collection migration과 목록/EXPLAIN 통과
- backfill kill/restart, partial capability, dual-write rollback 검증
- A0 이전 instance 또는 pre-A in-flight transaction drain을 생략한 cutover가 fail-closed하고, release A의 legacy/guarded create가 모두 fence를 획득함을 executable trace로 검증
- create/delete/backfill의 base-row→sidecar lock 순서와 conditional write가 delete-before/after race에서 orphan 0을 보장
- sidecar orphan/missing row를 doctor가 bounded finding으로 보고
- hot collection fixed-concurrency 기준에서 creation fence 도입 전 대비 throughput regression 15% 이하, p99 fence wait < configured statement timeout. R0 benchmark가 더 엄격한 기존 SLO를 발견하면 그 값을 사용한다.

### R5 — relation sort와 nullable DESC index

변경 내용:

- bare single relation은 root inline UUID `OrderSpec`을 사용한다.
- multiple bare relation sort는 안정된 unsupported error를 유지한다.
- dotted relation path만 target join/private cursor/policy 경로를 사용한다.
- relation filter + root `-created_at`도 NOT NULL `NULLS FIRST`를 사용한다.
- nullable DESC의 공개 `NULLS LAST` 계약은 유지하고 companion index를 추가한다.
- companion index의 목적은 별도 mutable catalog row가 아니라 `ExpectedPhysicalSchema.IndexSpec`에서 결정적으로 파생한다.
- existing collection index는 transaction migration 안에서 만들지 않고 resumable `CREATE INDEX CONCURRENTLY` maintenance로 구축한다.
- writer capability gate가 ready이기 전에는 새 compiler가 companion index를 필수 contract로 가정하거나 새 plan format을 issue하지 않는다.
- plan compiler version은 mixed fleet에서 dual validation하고, downgrade 시 extra index는 유지한다.
- doctor는 pending/building/ready transitional state를 구분하고 invalid concurrent index를 복구 대상으로 보고한다.

완료 기준:

- `sort=owner` SQL에 target join 없음
- 100,001-row collection에서 bare relation sort budget 거부 없음
- relation filter + `-created_at`이 `(created_at,id)` backward scan 사용
- nullable DESC가 fixed large fixture에서 companion index 사용
- dotted nullable relation sort의 ASC/DESC, null↔non-null boundary, duplicate value와 anchor deletion property/live test 통과
- scalar/relation order와 keyset property test가 같은 결과
- telemetry가 bare relation sort를 relation-path 작업으로 세지 않음
- concurrent index build kill/restart와 partial fleet checksum/preview/apply 검증
- rollback binary가 extra companion index를 orphan이나 conflict로 오인하지 않음

### R6 — `RecordKernel`과 `RelationKernel` deepening

변경 내용:

- Create/Update/Delete 외부 intent는 유지한다.
- caller가 relation lock/storage/hydration 순서를 조립하는 interface를 제거한다.
- common mutation commit path가 revision/audit/outbox/response artifact를 기록한다.
- delete closure, survivor snapshot/touch, set-null, cascade base delete와 artifact를 한 deep implementation으로 모은다.
- cascade closure의 base row와 revision을 collection별 batch로 읽고, survivor/base mutation과 revision/audit/outbox artifact도 collection/operation별 set-based batch로 기록해 최대 10,000-record path의 read/write N+1을 모두 제거한다. event sequence, affected-row 검증, retry-stable ID는 batch input에서 명시적으로 보존한다.
- retry-stable event/validator identity는 retry loop 밖에 유지한다.
- inline FK와 edge table adapter는 relation implementation 안에만 둔다.
- `DeletePlan.Clears`처럼 test만 사용하는 공개 facade와 operation별로 복제된 validation/principal error 타입을 최종 caller 이동과 함께 삭제한다.

완료 기준:

- production caller의 `LockCollections`, `LockRows`, `ReplaceMulti` 직접 조립 0
- advisory-key/ordering primitive는 1개이고 Create/Update/Delete entry point가 각자의 고정 순서를 소유함; caller options bag 0
- opposite collection order, phantom commit, cascade cycle, retry closure change live test 통과
- retry 뒤 event ID와 validator 안정성 유지
- cascade fixture의 `pgx` tracer 전체 read/write query count가 record 수에 선형 증가하지 않고 collection/edge/artifact batch 상한 안에 머묾; batch `RowsAffected`와 event 순서는 기존 per-record 계약과 동일
- query/record/schema에 별도 복제된 retry loop 0; operation별 고정 entry point만 남음
- query/record의 `CatalogEnsurer` interface 0
- `recordops+relation` runtime 3,650 LOC 이하, stretch 3,350 LOC; 파일 수는 locality 결과로만 보고

### R7 — Auth, HTTP transport, App lifecycle

변경 내용:

- auth constructor를 pool+config로 축소하고 store/password/clock/token/cleaner를 private implementation으로 내린다.
- auth 결과가 CSRF를 제공해 API에서 token secret을 제거한다.
- metrics module이 bearer token을 받아 hash만 보유하게 해 router dependency에서 metrics secret도 제거한다.
- 현재 22개 route 선언을 단일 private registry로 옮기고 access/deadline/response policy를 data로 검증한다. 특정 `mux.Handle` 호출 모양을 세는 repository AST test는 registry uniqueness/cardinality/completeness test로 교체한다.
- required module 검증을 request-time nil check가 아니라 router construction error로 바꾼다.
- `AccessPolicy`, `ResponseMode(AtomicJSON(maxBytes)/Passthrough)`, `DeadlinePolicy(Default/SchemaApply)`를 서로 다른 축으로 둔다. 모든 atomic route는 cap을 명시하며 catalog는 R2의 canonical 16 MiB snapshot만 반환하고 record page는 기존 4 MiB contract를 재사용한다. cap 없는 `AtomicJSON`은 construction error다.
- bounded `AtomicJSON` buffer와 passthrough response를 분리한다. cap 초과는 header/body commit 전 stable error로 끝내고 catalog처럼 큰 응답이 buffer를 무제한 점유하지 못하게 한다.
- `pgbase.App`는 composition/lifecycle만 소유하고 start/run/close/background coordination 중복을 private lifecycle implementation으로 모은다.

DDL timeout과 결과 모호성 수정:

- `DeadlinePolicy.SchemaApply`는 handler 진입에서 `SchemaApplyTimeout` domain context를 만들고 그 context를 `SchemaKernel`과 pgx transaction까지 전파한다. response buffering 전에 writer deadline을 domain deadline + 고정 5초 response margin으로 `http.ResponseController.SetWriteDeadline`에 적용한다. wrapper가 앞에 있다면 `Unwrap()`으로 실제 writer를 노출하며, deadline 설정 실패는 어떤 header/body/domain 호출보다 먼저 안전한 503으로 끝낸다.
- `Options`와 env에 `SchemaApplyTimeout`을 추가한다. 기본 10분, 허용 30초~1시간으로 두고 built-in `Run`과 caller-owned `Handler` server에서 모두 검증한다.
- R2b ledger의 HTTP recovery contract를 붙인다. guarded client는 `PgBase-Operation-ID` UUID와 bounded `Idempotency-Key`를 apply 전에 생성하고, response가 끊겨도 `GET /api/admin/schema-operations/{id}`로 상태를 조회한다. 최근 이력은 `GET /api/admin/schema-operations?limit=&cursor=`로만 제공하고 기본 20/최대 100개와 256 KiB `AtomicJSON` cap을 registry에 고정한다.
- context timeout/cancel과 commit ACK가 경쟁하면 transport 추측으로 failed를 반환하지 않는다. ledger의 committed/failed/running/unknown을 조회해 응답하고, 아직 판정할 수 없으면 recovery status URL을 가진 stable unknown 응답으로 끝낸다.
- status/history route는 현재 관리자와 operation owner를 모두 검증하고 bounded/redacted receipt만 반환한다. CORS allow/expose, duplicate header, malformed ID/key, same/different request conflict, limit/cursor, not-found와 unauthorized shape를 wire golden으로 고정한다.
- headerless legacy apply의 기존 body/status는 그대로 유지하고 compatibility header만 명시한다. guarded UI/새 client만 recovery 보장을 받으며 기존 공개 client를 조용히 깨지 않는다.
- App만 private named-task registry를 소유하고 module constructor/handler는 hidden goroutine을 시작하지 않는다. `Start`가 auth cleaner, record idempotency cleaner, schema receipt reconcile/cleaner, schema listener, system sampler를 정확히 한 번 등록·시작한다.
- `Run` parent context 취소는 task context를 즉시 취소하지 않고 shutdown을 시작한다. readiness false → 새 request 차단/HTTP drain → task cancel/wait → pool close 순서를 `ShutdownTimeout` 안에서 실행한다. 강제 종료된 operation은 다음 시작 때 R2b ledger로 재조정한다.

완료 기준:

- API dependency에 raw auth/metrics secret 0
- 모든 route가 정확히 하나의 access policy를 가짐
- route path+method 중복 0, 현재 public route cardinality와 method 집합 보존, 장기 schema route만 명시적 deadline policy 사용
- mutation ordering origin→auth→CSRF→handler 고정
- rejected request가 domain module을 호출하지 않음
- cap 초과 atomic response가 partial header/body를 내보내지 않음
- 모든 atomic route가 명시적 cap을 가지며 admitted catalog 응답의 peak buffer는 canonical snapshot 16 MiB + 고정 overhead 이내
- wire/ETag/Location/Retry-After/cache 계약 동일
- 30초를 넘지만 `SchemaApplyTimeout` 안에서 끝난 DDL의 응답이 정상 전달됨
- handler/domain deadline이 pgx transaction까지 전달되고 writer deadline은 정확히 5초 margin을 가지며, timeout-vs-commit race는 ledger 상태와 모순되는 failed 응답을 만들지 않음
- cancel/timeout/commit-ACK 단절 각각에서 receipt가 failed/committed/unknown 중 실제 DB 상태와 일치하거나 재조정 가능
- operation status/history는 다른 관리자/조작된 ID·cursor에서 존재 여부를 누출하지 않고, history 순서/limit/tie-break가 `SchemaKernel.ListOperations`와 동일하며 legacy headerless apply의 golden은 byte/status 호환
- built-in `Run`과 caller-owned `Handler`의 30초 outer `WriteTimeout` 환경 모두 deadline policy 통과
- `Handler`는 caller-owned server만 제공하고 background runner는 `Start`가 정확히 한 번 시작한다는 현 계약 유지
- `Start`/`Run` 상호 배제와 concurrent `Close` idempotency 유지
- auth cleaner, idempotency cleaner, schema receipt reconciler/cleaner, schema listener, system sampler가 named registry에서 정확히 한 번 시작
- module 내부 hidden goroutine 0; 한 background runner의 error 또는 shutdown 전 예상 밖 nil 반환이 나머지를 취소하고 이름이 포함된 오류를 반환
- readiness false → HTTP drain → background wait → pool close 순서 유지

### R8 — Migration, Doctor, CLI, Observability

Migration:

- `Up`과 `Status`가 하나의 `inspectHistory`를 사용한다.
- unknown version, checksum mismatch, history gap을 typed issue로 구분한다.
- designated rollout job을 위한 exact-target `migrate up --through <version>`을 제공한다. target은 embedded history의 실제 version이어야 하고 그 뒤 pending migration이 있으면 적용하지 않으며, 전후 version/checksum/dirty 상태를 machine-readable receipt로 남긴다.

Doctor:

- `ExpectedPhysicalSchema`와 bulk `PhysicalSnapshot` 비교로 교체한다.
- function body substring 대신 canonical expected definition/hash를 비교한다.
- catalog query 수를 고정 상한으로 만든다.

CLI:

- `Execute(ctx,args,env,IO)`로 외부 interface를 축소한다.
- private command table, DB runtime, production/test terminal adapter를 둔다.
- mutable stdin/terminal global과 `CommandSet`을 삭제한다.

Observability:

- operation에서 component를 파생한다.
- exact operation/outcome matrix를 한 implementation에서 검증한다.

System health:

- 현재 `SystemStatusProvider.Status`가 ping/status 오류를 삼키는 경로를 error-carrying snapshot으로 교체한다.
- bounded background sampler가 database/migration/catalog/listener를 수집하고 last-success/last-attempt/staleness를 cache한다. HTTP request가 직접 무제한 DB 진단을 실행하지 않는다.
- unavailable은 healthy zero value와 구분하고 raw database error는 안정된 code/redacted component로만 변환한다.

완료 기준:

- migration dirty reason별 exact issue/finding
- exact-target migration이 승인한 version 하나를 넘지 않고 checksum/history read-back 불일치 시 feature release를 차단
- function comment spoof와 base/index drift 탐지
- clean schema PostgreSQL 16/17/18 false positive 0
- doctor query count가 catalog 크기와 무관한 고정 상한
- CLI DB bootstrap 1곳, mutable IO globals 0
- CLI exit/stdout/stderr/redaction golden 동일
- macOS terminal CI 실행
- metric exposition byte-equivalent, caller component 인자 0
- status probe 실패가 healthy로 표시되지 않고 last-success가 보존되며, 느린 DB에서 HTTP status latency가 sampler budget을 넘지 않음

### R9a — 관리자 UI behavior-preserving deepening

1. `AdminLocation`과 route identity/late-result guard를 완성한다.
2. `AdminBackend`의 `SessionPort`/`SchemaPort`/`RecordPort`/`SystemPort`, HTTP/scripted-memory adapter와 opaque plan/version/attempt 타입을 도입한다.
3. `CollectionDesigner`로 create/edit/delete/default/relation 상태를 통합한다.
4. `RecordWorkspace`로 list/editor/codec/filter/sort/cursor/stale 상태를 통합한다.
5. 기존 test name/behavior를 새 interface/adapter/DOM/E2E surface에 매핑한 추적표를 만든 뒤 page 테스트를 교체한다.
6. schema/record/session/system의 semantic conformance vector를 HTTP와 scripted-memory adapter 양쪽에 실행한다. HTTP status/header/duplicate-key/lossless-body는 HTTP adapter 전용 wire test로 남긴다.

완료 기준:

- page에서 `fetch`, `APIClientError`, CSRF, checksum, ETag import 0
- view의 endpoint literal과 raw `APIClient.request` 호출 0
- session context가 raw HTTP client를 노출하지 않고 auth snapshot/action만 제공하며 System page도 `SystemPort`만 사용
- collection change 시 definition/page/filter/sort/cursor/error 원자 초기화
- collection A delete-plan 상태가 같은 component instance의 B로, records A filter/sort/cursor가 B로 이동하지 않음
- stale response가 다른 resource snapshot에 합쳐지지 않음
- UI runtime 2,050 LOC 이하(현재 surface 교체 추정 약 2,041 LOC); private 파일 수는 module locality 결과로만 보고
- page 직접 `useState`/`useEffect` 지점 30 이하, UI runtime export 65 이하
- Vitest 약 1,900 LOC 이하를 보조 목표로 하되 behavior 추적표가 대체 surface를 가리키지 못하는 테스트는 삭제 금지
- Playwright 실제 backend workflow 1개 → 최소 5개
- onboarding 전용 backend와 authenticated worker fixture가 격리되고 retry/parallel 실행 시 resource 충돌 0

필수 브라우저 흐름:

- onboarding/reload, logout/login
- collection create/edit/delete와 direct history jump
- scalar/relation record create/edit/filter/sort
- two-tab stale ETag recovery
- System redaction/diagnostic

### R10 — Ops, CI, restore, release

R10은 rollout 선행 checkpoint인 R10a와 나머지 운영 보강 R10b로 나눈다. R10a에서 tag/version preflight, 단일 candidate build, digest-pinned reusable gate, promotion primitive를 먼저 완성해야 R2b/R4/R5 migration이나 R4 fleet rollout을 시작할 수 있다. R10b의 Compose/restore/alert 정리는 R11 전까지 끝낸다.

변경 내용:

- release workflow는 tag/version preflight를 가장 먼저 실행하고, 통과한 경우에만 candidate image를 한 번 build/push해 digest를 `candidate_image`, version을 `expected_version` reusable-workflow input으로 내보낸다. backend/frontend 외 image-independent gate를 제외한 Docker E2E/security/restore는 이 digest만 pull/실행하며 release mode의 local build를 금지한다. promotion은 검증된 digest에 tag만 추가하고 rebuild하지 않는다.
- `PGBASE_SHUTDOWN_TIMEOUT`과 `PGBASE_STOP_GRACE_PERIOD`를 함께 설정 가능한 contract로 만든다.
- direct SIGTERM뿐 아니라 실제 `docker compose stop` drain을 검증한다.
- backup/restore inline shell을 test/support code인 `tests/restore`의 `RestoreDrill`로 옮긴다. local mode는 `go test -tags=restore ./tests/restore`, release mode는 `PGBASE_RESTORE_CANDIDATE_IMAGE=registry/name@sha256:... go test -tags=restore ./tests/restore`를 interface로 사용한다.
- Docker Go E2E도 `PGBASE_E2E_CANDIDATE_IMAGE=image@sha256:...`를 release input으로 받고, 설정 시 `compose up --build`와 임시 tag 재작성 없이 그 digest만 실행한다. local mode만 source build를 허용한다.
- revision/audit/outbox/idempotency와 schema operation receipt/running-unknown reconciliation sentinel을 restore 후 직접 확인한다.
- frontend dist gate가 untracked output도 잡게 한다.
- stable SemVer release만 `latest`를 이동한다. exact version tag는 immutable이고, prerelease는 immutable exact version tag와 명시적 moving channel tag만 다루며 `latest`를 건드리지 않는다. tag matrix를 executable workflow policy test로 고정한다.

| release input | preflight | immutable exact tag | 이동 가능한 channel tag | 삭제 또는 no-op |
|---|---|---|---|---|
| `vMAJOR.MINOR.PATCH` | canonical stable SemVer와 `expected_version` 일치 | absent면 create, same digest면 idempotent no-op, different digest면 전체 promotion reject | `latest`만 새 digest로 이동 | 삭제 0; `alpha`/`beta`/`rc` no-op |
| `vMAJOR.MINOR.PATCH-alpha.N` | canonical prerelease와 version 일치 | absent/create, same/no-op, different/reject | `alpha`만 이동 | 삭제 0; `latest`/`beta`/`rc` no-op |
| `vMAJOR.MINOR.PATCH-beta.N` | canonical prerelease와 version 일치 | absent/create, same/no-op, different/reject | `beta`만 이동 | 삭제 0; `latest`/`alpha`/`rc` no-op |
| `vMAJOR.MINOR.PATCH-rc.N` | canonical prerelease와 version 일치 | absent/create, same/no-op, different/reject | `rc`만 이동 | 삭제 0; `latest`/`alpha`/`beta` no-op |
| malformed, build metadata, 지원하지 않는 channel | reject | build/push/promotion 0 | 모두 no-op | 모든 tag no-op |

exact version tag의 registry state를 channel mutation보다 먼저 read-back한다. different digest면 channel도 건드리지 않고 실패하며 exact tag는 절대 force-retag하지 않는다. moving channel과 새 exact version은 모두 검증한 동일 digest를 가리켜야 한다. 기존 tag 삭제는 이 workflow의 권한 밖이고, channel 이동 전후 digest를 audit artifact에 남긴다.
- provenance/SBOM에 대해 실제 `gh attestation verify` smoke 절차를 둔다.
- `npm ci --ignore-scripts` 가능 여부를 검증하고 가능하면 실제 Docker build, frontend CI, E2E install 모두에 적용한다. Playwright browser/system dependency 설치는 별도 명시적 command로 분리한다. 불가능하면 필요한 install script를 allowlist한다.
- Trivy finding은 SARIF/artifact로 남긴다. `ignore-unfixed: true` blanket은 제거하고 예외마다 advisory/CVE, owner, 근거, 만료일, 재평가 조건을 가진 allowlist를 사용한다.
- alert rule의 `promtool` 검증에 더해 dashboard JSON에서 PromQL을 추출해 parser/metric-contract 검증을 실행한다.

완료 기준:

- default/override/malformed rendered Compose profile에서 grace >= shutdown + 5s
- timeout 직전 inflight request를 둔 Compose stop에서 exit 0, 비-137
- 다섯 release gate 유지
- workflow path filter가 `tests/restore/**` 변경을 포함
- release candidate와 scheduled drill 모두 PostgreSQL 16/17/18을 포함하며 각 version에서 같은 candidate digest를 restore 후 실행
- release drill이 digest-pinned candidate image 없이는 시작하지 않고 source-built binary fallback을 거부
- release Docker E2E도 digest-pinned candidate 없이는 시작하지 않고 local build/tag fallback을 거부
- restore가 세 schema, migration, admin, relation order, sidecar, schema receipt/reconcile, doctor와 복원 후 HTTP idempotency replay/ETag를 검증
- candidate가 보고한 version이 `expected_version`과 정확히 같고, prerelease tag matrix에서 `latest` mutation 0
- 위 다섯 tag matrix 행과 exact tag의 absent/same/different-digest 3-way를 모두 실행함. different digest는 channel mutation 0회, malformed/unsupported input은 candidate build·push·promotion 0회
- committed dist와 clean build가 파일 집합까지 일치
- support platform 또는 multi-arch 정책이 workflow와 문서에서 동일

### R11 — compatibility/shallow module 삭제와 정량 마감

R11은 old path를 처음 삭제하는 단계가 아니다. 각 R2~R10 묶음은 자기 replacement와 old caller/test 삭제를 완료 조건으로 갖고, R11은 저장소 전체에 남은 누락을 잡는 최종 consolidation이다.

변경 내용:

- forwarding constructor, type assertion adapter, old cursor issuer, old planner/list executor, shallow wrapper를 전수 검색한다.
- exported symbol과 constructor arity를 기준선과 대조한다.
- behavior 추적표가 대체 surface를 가리키는지 확인한 뒤 남은 implementation-coupled tests를 삭제한다.
- 단계별로 이름과 타입이 대체한 설명 주석을 제거하고 rationale/contract 주석만 남긴다.
- runtime/test/support/generated LOC를 서로 다른 category로 최종 계산한다.

완료 기준:

- 내부 forwarding implementation adapter 0; headerless `Idempotency-Key`/`If-Match` 등 승인된 공개 wire compatibility path는 유지
- old implementation을 호출하는 caller 0
- query/order/page/schema/retry/CLI bootstrap 중복 지표 달성
- 기존 기능·확정 결함 수정 runtime 예상 21,050~21,600, hard 21,600, stretch 20,700
- data는 core replacement checkpoint 11,900 이하, R4/R5/ledger 포함 예상 12,150~12,650(hard 12,750); backend+app hard 6,800, UI 2,050 이하
- R2/R2b/R3/R4/R5/R6 각각 gross-new/gross-delete/net과 old caller 삭제 수가 기록되고 R0에서 승인한 stack budget을 통과
- production interface 12 이하, exported 선언 285 이하, `New*` constructor 16 이하
- runtime comment-only/comment-like 600줄 이하(stretch 500), 800줄 초과 production 파일 0
- test/support code를 runtime 감소로 잘못 계산하지 않음
- 전체 추적 LOC는 R11 62,000~66,000 forecast와 category별 증감으로 보고하되 hard 감소 KPI로 사용하지 않음
- F0~F3가 최종 deep interface만 사용 가능

## 9. 기능 추가 묶음

기능은 refactor와 같은 commit에 섞지 않는다. R11까지 old path 삭제와 정량 마감을 끝낸 뒤 독립 작업으로 추가한다.

### F0 — 관리자 UI 안전·탐색 개선

- nav button을 semantic link로 바꿔 새 탭, 주소 복사, browser history를 정상 지원한다.
- collection 삭제에 typed-name confirmation과 고정된 target identity를 표시한다.
- collection/record unsaved draft가 있을 때 route 이탈을 확인한다.
- record cursor back-stack과 Previous page를 추가한다.

완료 기준:

- 새 탭/수정키/뒤로·앞으로 이동이 typed route round-trip과 일치
- stale plan이나 다른 collection identity로 delete 전송 불가
- 저장 완료·명시적 discard 외에는 dirty draft가 조용히 사라지지 않음
- Previous/Next가 같은 query fingerprint와 cursor chain을 사용

### F1 — schema operation recovery/history UX

- 기존 plan/apply endpoint와 checksum 계약을 유지한다.
- R2b ledger와 R7 HTTP receipt/status adapter를 관리자 UI에서 사용한다.
- 연결 종료나 reload 뒤에도 operation ID로 committed/failed/unknown 상태를 복구한다.
- `SchemaKernel.ListOperations`와 `GET /api/admin/schema-operations?limit=&cursor=`를 통해 최근 schema operation을 20개씩 bounded/redacted history로 표시한다. UI가 ledger table을 직접 읽거나 단건 status를 반복 fan-out하지 않는다.
- 별도 background DDL worker와 queue는 이번 묶음에서 만들지 않는다.

완료 기준:

- 30초를 넘긴 DDL이 commit되면 UI가 최종 성공을 복구
- reload와 재로그인 뒤 operation status 복구
- unknown 상태를 성공이나 실패로 임의 단정하지 않음
- history의 next-page가 `(created_at DESC, operation_id DESC)` 순서를 유지하고 다른 owner가 cursor를 재사용하지 못함
- raw SQL/secret/request hash가 operation status/history에 노출되지 않고 페이지가 100개·256 KiB를 넘지 않음

### F2 — searchable relation picker

- indexed/unique target field와 UUID exact search를 `ReadKernel`로 재사용한다.
- 별도 무제한 search endpoint를 만들지 않는다.
- first-50 이후 cursor pagination, 선택된 ID의 label hydration, keyboard 접근성을 추가한다.
- self relation, 현행 relation visibility/budget, 2,048-byte indexed text 한계를 유지한다. 미래 RBAC용 `AccessPlan`을 이 기능만을 위해 미리 만들지 않는다.

완료 기준:

- single/multiple relation 선택과 ordered unique UUID 보존
- cursor 기반 추가 로드
- unindexed broad scan 불가
- selected target 삭제/권한 변화의 안정된 표시

### F3 — 관리자 System schema health

- refactored Doctor의 stable finding code와 redacted summary를 authenticated read-only route로 노출한다.
- raw database error, SQL, secret, identifier 폭증을 응답하지 않는다.
- System 화면에 마지막 성공 시각, retry, migration/catalog/listener 상태를 표시한다.

완료 기준:

- healthy/drift/unavailable 상태 구분
- finding 수와 payload 크기 제한
- same-origin/admin read 정책
- CLI doctor와 HTTP doctor가 같은 comparator 결과 사용

F0~F3 종료 후 관리자 UI는 초기 2,412 LOC 이하, 전체 runtime은 초기 22,484 LOC 이하를 hard guardrail로 둔다. 예상 범위는 UI 약 2,150~2,350 LOC, 전체 runtime 21,800~22,484 LOC이며 기능 LOC를 억지로 기존 module에 숨기지 않는다.

### R12 — R10a release-equivalent gate의 최종 실행과 검증

- R10a에서 이미 구현·사용한 build-candidate와 reusable gate definition을 최종 feature candidate에 다시 실행한다. publish candidate image를 한 번 build하고 `candidate_image=image@sha256:...`, `expected_version`을 required input으로 전달하며 E2E/security/restore가 검증한 동일 digest만 registry tag로 승격한다.
- E2E image와 release buildx image가 별도 source build가 되지 않게 한다.
- embedded dist의 hash와 file set, binary version, nonroot user, healthcheck, supported platform을 candidate digest에서 검증한다.
- 실제 Docker build가 승인된 `npm ci --ignore-scripts` 또는 명시적 allowlist를 사용함을 확인한다.
- SBOM/provenance attestation을 같은 digest에 연결하고 `gh attestation verify`를 실행한다.
- PostgreSQL 16/17/18, race, EXPLAIN, Docker Go E2E, Playwright, Compose stop, backup/restore를 모두 실행한다.
- Docker Go E2E에 동일 candidate `PGBASE_E2E_CANDIDATE_IMAGE=image@sha256:...`를 전달하고 release mode의 `--build`를 금지한다.
- `RestoreDrill`에 동일 candidate `image@sha256`와 expected version을 전달하고 PostgreSQL 16/17/18 각각에서 source-built fallback 없이 receipt/reconcile까지 확인한다.
- release security mode도 candidate digest만 scan하고 별도 `docker build`를 금지한다. promotion은 digest retag만 허용하며 R10의 stable/alpha/beta/rc/reject tag matrix 전체를 검사한다.
- `go test -json` skip allowlist, generated dist, git clean, LOC/interface 지표를 다시 판정한다.
- 최신 감사 문서와 현재 코드를 독립 관점으로 다시 대조한다.

완료 기준:

- E2E/security/restore의 input digest와 promotion/publish digest가 동일하고 candidate version이 expected version과 동일
- 다섯 release gate와 attestation 검증 green
- integration DB skip 0, 허용되지 않은 전체 skip 0
- worktree clean, committed dist byte/file-set reproducible
- 보안·정합성·성능 regression 0

이 gate는 R12에서 처음 만드는 것이 아니다. R4 release A0/A/B/C와 최종 F0~F3 release가 모두 R10a의 같은 gate definition을 재사용하고, R12는 최종 결과와 artifact를 한 번에 마감한다.

## 10. 테스트 교체 전략

새 테스트를 기존 테스트 위에 계속 쌓지 않는다. 새 deep module의 interface 테스트가 같은 동작을 고정하면 private helper와 shallow wrapper 테스트를 삭제한다.

유지할 테스트:

- parser, default, codec, cursor와 같은 pure contract
- HTTP 전체 router 보안/wire contract
- auth setup/login/reset/session/rate-limit
- PostgreSQL lock/deferred constraint/migration/live behavior
- schema plan/checksum golden
- record revision/audit/outbox/idempotency 원자성
- CSP/static UI handler와 repository policy

교체할 테스트:

- scalar/relation compiler별 동일 SQL fragment 테스트 → `OrderSpec` property/interface 테스트
- 두 root executor의 row-loop 테스트 → `ReadKernel.List` 계약 테스트
- fake planner/type-assertion 테스트 → pure private `Planner.Plan(catalog,change)` property/golden 테스트와 실제 PostgreSQL `SchemaKernel.Preview` integration 테스트
- auth store 직접 SQL 테스트 → auth interface integration 테스트. malformed/unknown/real KDF wave, 5-failure 뒤 valid recovery, KDF queue의 pool occupancy, reset-vs-login hash recheck는 multi-instance PostgreSQL concurrency vector로 고정한다.
- route별 반복 origin/auth/CSRF 테스트 → route-policy exhaustiveness + 대표 wire 테스트
- fake `CommandSet` dispatch 테스트 → 실제 `Execute` 계약 테스트
- page별 fetch 순서/hook state 테스트 → UI module interface + 얇은 DOM smoke
- workflow inline shell 테스트 → `RestoreDrill` 실행 테스트

테스트 삭제 전에는 기존 test name/observable contract → 새 pure/interface/live/DOM/E2E test를 연결한 추적표가 필수다. owned adapter는 같은 conformance vector를 production과 in-memory adapter 양쪽에서 통과해야 한다.

adapter conformance는 semantic input/output, cancellation, typed error만 공유한다. HTTP status/header/CORS/duplicate-key/lossless JSON body는 HTTP wire test, SQL/lock/query count는 live PostgreSQL test에 남겨 scripted-memory adapter가 transport나 PostgreSQL을 재구현하지 않게 한다. DB round-trip 회귀는 fake row counter가 아니라 `pgx` tracer로 측정한다.

필수 검증 층:

1. pure unit/property/fuzz
2. deep module interface tests
3. PostgreSQL 16/17/18 live/concurrency
4. fixed large fixture `EXPLAIN (FORMAT JSON, ANALYZE, BUFFERS)`
5. Go race/vet/format/build
6. frontend typecheck/Vitest/build/dist diff
7. Docker Go E2E와 Playwright
8. Compose shutdown, backup/restore, replica/outage
9. govulncheck/npm audit/Trivy/provenance/SBOM

EXPLAIN test는 elapsed time이 아니라 plan node, index name, row/buffer 상한을 검증한다.

unit command는 live test를 포함하지 않는다. integration command는 PostgreSQL 16/17/18 fixture가 없으면 skip하지 않고 시작 전에 실패하며, `go test -json`의 package/test별 skip allowlist에 없는 skip 하나도 release를 막는다.

## 11. PR, rollback, migration 규칙

R2b/R4/R5처럼 새 system migration이 필요한 묶음은 코드 배포와 DB 변경을 다음 상태 머신으로 분리한다.

1. **P0 준비:** 기존 schema와 정확히 다음 migration checksum이 적용된 schema 두 상태만 지원하고 새 feature는 off인 preparatory artifact를 만든다. 평소의 unknown/checksum-mismatch/history-gap fail-closed는 유지하며, 이 한 개의 declared optional migration만 startup compatibility range로 허용한다. R10a gate를 통과한 P0를 전 fleet에 배포하고 이전 binary와 그 in-flight transaction을 drain한다.
2. **M 적용:** application auto-migrate를 끈 상태에서 designated single job이 P0 artifact의 `migrate up --through <version>`으로 승인된 migration 하나까지만 적용한다. 시작 전 current version/checksum, 종료 후 target version/checksum과 dirty=false를 read-back하고, 다른 pending migration이 있으면 시작하지 않는다.
3. **F 활성화:** post-migration schema를 required minimum으로 두는 feature release를 전 fleet에 배포한 뒤 capability/flag를 켠다. mixed fleet write fence와 각 묶음의 drain hold point를 모두 통과하기 전에는 활성화하지 않는다.

첫 system migration 전에 exact-target `--through`와 checksum read-back을 작은 독립 prerequisite stack으로 추가한다. migration 전에는 이전 binary로 code revert가 가능하다. M commit 뒤에는 DB를 down-migrate하거나 pre-P0 binary를 배포하지 않고, P0로 deploy rollback한 뒤 feature off 상태에서 forward fix한다. feature-specific cutover 뒤에는 해당 절에서 정한 더 최신 compatibility release만 rollback target으로 쓴다. 코드 revert, deploy rollback, DB migration rollback을 운영 runbook에서 서로 다른 동작으로 표기한다.

- 작업 묶음 하나를 독립 PR/commit stack으로 유지한다. migration M 전에는 독립 revert 가능해야 하고, M 뒤의 Git revert는 결과 artifact가 해당 migration을 인식하면서 feature-off를 유지하는 경우에만 허용한다.
- 새 module 도입과 old caller/implementation 삭제를 같은 stack으로 묶는다. system migration이 없는 stack은 독립 revert 가능하고, migration stack은 M 뒤에 pre-P0 source 상태로 되돌리지 않는다.
- compatibility wrapper는 같은 stack 밖이나 다음 R 단계까지 존속시키지 않는다.
- LOC 감소는 개별 commit/PR이 아니라 replacement+deletion stack 전체에서 측정한다. characterization, correctness, security, migration, doctor 강화, 기능 stack은 양수일 수 있으며 category를 별도 표기한다.
- public wire나 metric diff가 생기면 명시적 승인 없이 진행하지 않는다.
- migration은 append-only이며 위 `P0 → M → F` expand/contract 절차로 배포한다.
- R2b, R4, R5의 새 system migration 번호는 실제 merge 순서로 직렬 할당하고 병렬 branch의 번호/checksum 충돌을 merge 전에 재조정한다.
- cursor와 plan format은 mixed fleet dual decode/dual validation 기간을 둔다.
- schema writer를 단일 version으로 고정하지 못하는 rollout은 시작하지 않는다.
- DB downgrade 시 새 물리 객체는 즉시 drop하지 않는다. 다만 migration 적용 뒤의 rollback target은 그 migration/version을 인식하고 feature를 off로 유지할 수 있는 preparatory compatibility release로만 고정한다. unknown migration에서 fail-closed하는 pre-migration binary는 일반 rollback 대상이 아니다.
- 성능 regression, deadlock, lock-wait 급증, false-positive doctor finding이 발생하면 M 전에는 해당 묶음만 revert한다. M 뒤에는 capability/feature를 fail-closed off로 전환하고 해당 절의 compatibility artifact로 deploy rollback한 뒤 forward-fix한다. DB migration과 pre-P0 code는 revert하지 않는다.
- generated dist는 source 변경과 같은 commit에서 재생성하고 clean-status를 확인한다.

## 12. 금지 사항

- generic repository, SQL dialect abstraction, in-memory PostgreSQL mock
- production adapter 하나만 있는 가상 seam
- 단순 re-export 또는 30 LOC 미만 pass-through wrapper
- 범용 `Execute(any)` command union
- 범용 React `useAsync`/`useForm` framework
- 파일 수만 줄이기 위한 무관한 implementation 병합
- test를 통과시키기 위한 public error/detail 약화
- metric rename이나 label 의미 변경
- v1~v5 migration 편집
- 주석 수치를 맞추기 위한 보안·동시성 rationale 삭제
- failing test를 skip으로 바꾸기

## 13. 최종 완료 기준

기능적 완료:

- 현재 확인된 정합성·정렬·legacy default·auth timing·doctor·UI state·Compose 문제 해결
- F0~F3 기능이 기존 deep module을 재사용해 구현됨
- 기존 wire, checksum, mutation artifact, metric 계약 보존

구조적 완료:

- 전체 추적 LOC는 현재 62,572, 최종 forecast 63,000~68,000으로 별도 보고하고 runtime 감소와 혼동하지 않음
- 기존 기능·확정 결함 수정 runtime 예상 21,050~21,600, hard 21,600, stretch 20,700
- 새 기능 포함 최종 runtime 22,484 LOC 이하 hard guardrail; 예상 21,800~22,484 LOC
- 정량 중복 지표 달성
- old shallow module, forwarding adapter, dead code 0
- production interface 12 이하, exported 선언 285 이하, constructor 16 이하
- runtime 주석 600줄 이하(stretch 500)이며 남은 주석이 모두 rationale/contract 성격
- page 직접 state/effect 지점 30 이하, UI runtime export 65 이하
- production 파일 800 LOC 초과 0을 목표로 하고, 600 LOC 초과 파일은 module depth 근거를 문서화

검증 완료:

- PostgreSQL 16/17/18 live/race skip 0
- pagination delayed commit, relation concurrency, schema apply/pin gate 통과
- query EXPLAIN index gate 통과
- Go/React/Docker/Playwright/restore/security/release-equivalent gate 통과
- committed dist 재현 및 작업 트리 clean
- 최신 감사 문서를 현재 코드와 다시 대조한 독립 리뷰 완료

## 14. 첫 실행 묶음

승인 후 바로 시작할 첫 묶음은 R0과 R1이다.

1. live PostgreSQL 환경과 skip-free 기준선을 만든다.
2. 현재 결함과 unbounded hydration/N+1/background-runner 문제를 실패 테스트와 tracer 기준으로 고정한다.
3. behavior 변화 없는 dead symbol/parameter/assignment와 CLI I/O hygiene를 첫 독립 commit에서 제거한다.
4. numeric, legacy default, auth timing, UI resource identity, Compose invariant를 독립 commit으로 수정한다.
5. doctor의 작은 false-negative를 보완한다.
6. 전체 gate와 LOC/interface/export/constructor/comment report를 다시 저장한다.

R0/R1이 green이 되기 전에는 대규모 interface 이동, cursor migration, 새 기능 구현을 시작하지 않는다.
