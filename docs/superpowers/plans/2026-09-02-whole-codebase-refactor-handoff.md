# PgBase 전면 리팩터링 중단·인수인계

작성일: 2026-09-02

중단 사유: 사용자가 현재까지의 결과를 보존하고 전체 구현을 중단한 뒤, 완료 범위·진행 방식·잔여 작업을 문서로 남기도록 요청했다.

원 계획: [`2026-09-01-whole-codebase-refactor.md`](./2026-09-01-whole-codebase-refactor.md)

## 1. 재개할 때 가장 먼저 할 일

이 작업 트리는 안전한 커밋과 미완성 WIP가 함께 있다. 다음 순서로 재개한다.

1. `git status -sb`와 이 문서를 읽는다.
2. `HEAD`의 커밋된 Release A와 미커밋 doctor/Release B 변경을 섞지 않는다.
3. 기존 변경을 reset, checkout, clean, stash 하지 않는다. 현재 WIP는 의도적으로 보존되어 있다.
4. doctor와 Release B를 각각 독립 재감사한다.
5. `internal/query` runtime 4,850줄 제한을 실제 삭제로 다시 만족시킨 뒤에만 Release B를 커밋한다.
6. 파일 묶음별로 stage하고 아래 검증 명령을 다시 실행한다.

현재 checkout은 detached HEAD다. 아래 hash는 문서 커밋 직전의 마지막 implementation checkpoint다.

```text
worktree: /Users/yakisoba0728/.codex/worktrees/0f44/PgBase
code:     96f96403f866d642644f2c918340c6340e7d5f73
branch:   HEAD (no branch)
```

## 2. 현재 단계 판정

| 단계 | 상태 | 근거 |
|---|---|---|
| R0 | 완료·커밋 | 기준선, 회귀 재현, metrics/test 분리 |
| R1 | 완료·커밋 | numeric/default/auth/doctor/UI/shutdown/E2E/background 결함 수정 |
| R2 | 완료·커밋 | SchemaKernel, compiled catalog, 공통 retry |
| R2b | 완료·커밋 | schema operation ledger/receipt/history |
| R3 | 완료·커밋 | ReadKernel, root planner/assembler, bounded hydration |
| R10a 일부 | 완료·커밋 | candidate digest 기반 release gate 기반 |
| R4 A0/M/A | 완료·커밋 | migration v7, rollout control plane, Release A writer, fence 성능 |
| R4 doctor | 구현 WIP | 코드와 테스트는 작성됐으나 최종 독립 재감사/커밋 전 |
| R4 Release B | 구현 WIP | resolver/query/recordops 연결은 동작하나 LOC hard gate 실패 및 감사 미완료 |
| R4 Release C | 미착수 | WriterC admission, v4 decoder 제거, 최종 rollout gate 필요 |
| R5 | 설계 map만 완료 | 코드는 수정하지 않음 |
| R6~R11 | 미착수 | 원 계획 순서대로 남음 |
| F0~F3 | 미착수 | R11 이후 기능 묶음 |
| R12 | 미착수 | 최종 release-equivalent gate |

대략적인 전체 진척은 40% 전후다. 남은 단계에는 backend뿐 아니라 UI, 운영, restore, release artifact 검증이 포함된다.

## 3. 커밋된 안전 지점

현재 `HEAD`까지는 다음 묶음이 독립 커밋으로 보존되어 있다.

### R0/R1

- `c0e1dc4` metrics 기준선
- `686c87e`, `7d81be3`, `142944b` query/mutation·wire·browser characterization
- `dffd05e` JSON number lexeme
- `592bed8`, `34be549`, `0e79d5c`, `39f4509` shutdown/background/UI/E2E 결함
- `a9e6147`, `40b3543`, `71d47a0`, `ab542b6`, `b54bf5a` doctor/auth/default/hygiene/docs

### R2/R2b

- `4b9bc32`, `853c48c`, `98aff7b`, `78ec383`, `47ecace` retry·SchemaKernel·catalog cap·metrics
- `afd13c0`, `cd41c22`, `0e29133`, `1bea09a`, `fe974c7` exact-target migration/history
- `9278523`, `73677f3`, `bc5c16c`, `85cfca9` operation ledger/receipt/history/metrics

### R3

- `3a8f74c` unified read planning
- `1b67d82` bounded relation hydration retention
- `97fbf4b` ReadKernel completion

### R10a 선행 기반

- `5d62daa` release candidate digest gate

### R4 Release A까지

- `abd089e` additive record-membership migration v7
- `deb5e0f` rollout control plane hardening
- `6bfd77a` signed cursor v5 continuation binding
- `84162f4` hot-create benchmark baseline
- `da700e3` writer validation coalescing
- `06d4dbf` PostgreSQL transaction time 기반 cursor clock
- `69ed27b` creation fence 단일 CTE/lock 최적화
- `96f9640` 모든 record create/update/delete에 Release A writer 통합

Release A의 고정 동작:

- create는 replay/reserve보다 먼저 collection creation fence를 잡는다.
- base row와 sidecar는 같은 transaction에서 기록한다.
- delete/cascade는 base delete 뒤 sidecar batch delete 순서를 지킨다.
- 외부 오류는 `record_unavailable`로 정규화되고 rollout/DB 세부가 노출되지 않는다.
- app는 정확한 migration v7에서만 Release A writer를 등록한다.
- migration v1~v7은 append-only이며 기존 checksum을 변경하지 않았다.

Release A hot-collection PG16 benchmark의 동일 조건 평균:

```text
baseline: ns/op 679,691, p99 6,070 us
feature:  ns/op 604,322, p99 3,637 us
result:   ns/op 약 11.1% 감소, throughput 약 12.5% 개선
```

## 4. 사용한 진행 방식

각 묶음은 다음 루프로 진행했다.

1. 기존 계약과 실패를 먼저 characterization test로 고정했다.
2. 최소 production 변경으로 실패를 해결했다.
3. PostgreSQL lock/snapshot/SQLSTATE를 추상화로 감추지 않고 executable trace로 검증했다.
4. unit, race, integration, vet, pinned lint를 위험도에 맞게 실행했다.
5. PostgreSQL 16/17/18의 결과를 비교했다.
6. 주요 묶음은 구현자와 별도의 sub-agent가 spec/standards 관점으로 감사했다.
7. 검증할 커밋은 임시 detached worktree에서 exact snapshot으로 다시 실행했다.
8. 성능 변경은 동일 fixture·동시성·시간으로 전후를 비교했다.
9. migration, deploy tag, publish, registry promotion은 실제로 실행하지 않았다.

이 과정에서 실제로 발견해 수정한 주요 위험은 다음과 같다.

- fresh singleton table 통계 때문에 membership CTE cost가 약 2,088,748로 폭증하고 JIT 57개 함수가 생성되던 문제
- v5 expiry가 application clock에 의존할 수 있던 문제
- create replay/counter cutover 사이의 lock 순서와 fence 범위
- doctor가 nullable/type/constraint/index drift와 준비 row 누락을 놓치던 문제
- control-plane 실패와 signed cursor mismatch가 같은 invalid-cursor 오류로 뭉개질 수 있던 문제

## 5. 미커밋 doctor WIP

다음 파일은 커밋되지 않았다.

```text
M  internal/doctor/checks.go
M  internal/doctor/checks_integration_test.go
M  internal/doctor/checks_test.go
?? internal/doctor/membership.go
?? internal/doctor/membership_catalog.go
?? internal/doctor/membership_integration_test.go
```

구현된 내용:

- v6는 membership 기능이 없는 정상 상태로 유지한다.
- 정확한 v7에서는 다섯 membership table의 column type/nullability/default, PK/unique/FK/CHECK/index를 bulk catalog snapshot으로 비교한다.
- capability singleton과 collection별 counter/backfill generation/state/high-water coverage를 한 고정 query로 검증한다.
- partial/malformed installation은 content anti-join 전에 하나의 redacted `record_membership_installation_invalid`로 닫는다.
- ready에서 missing sidecar, dual/ready에서 orphan sidecar를 bounded finding으로 보고한다.
- app base `id`가 없거나 잘못된 경우 기존 catalog finding을 유지하고 unsafe membership scan을 생략한다.
- finding ordering과 identifier redaction을 고정한다.

완료된 검증:

```text
PG16: go test -tags=integration ./internal/doctor -count=1
PG17: go test -tags=integration ./internal/doctor -run 'Membership|BaseID' -count=1
PG18: go test -tags=integration ./internal/doctor -run 'Membership|BaseID' -count=1
go test ./internal/doctor
go test -race ./internal/doctor
go vet ./...
go vet -tags=integration ./...
scripts/lint-go.sh  # 0 issues
```

남은 검증:

- 수정 후 독립 spec 재감사
- focused integration race 재실행
- catalog 크기와 무관한 doctor 전체 query-count hard assertion 확인
- R8 최종 구조에서 old doctor path를 실제로 교체·삭제해 additive stack을 끝낼 것

## 6. 미커밋 Release B WIP

### Membership resolver

```text
M  internal/membership/traversal.go
M  internal/membership/reviewer_integration_test.go
?? internal/membership/traversal_test.go
?? internal/membership/traversal_integration_test.go
```

구현된 계약:

- `ResolveTraversal(ctx, tx, collectionID)`가 schema SHARE pin, transaction time, migration history, 다섯 object, capability, collection counter를 한 SQL statement로 읽는다.
- capability/counter row lock은 잡지 않는다.
- off/dual/ready-v4는 v4 issue, ready-v5는 v5 issue, cutover는 unavailable이다.
- `Traversal.ValidateV5`는 signed generation/high-water mismatch만 `ErrTraversalInvalid`로 분류한다.
- `DisableV5` 뒤에도 기존 v5 continuation은 같은 generation/high-water로 유효하다.
- DB/control 오류 문자열은 `record membership is unavailable`로 redaction하면서 context/SQLSTATE cause는 unwrap 가능하게 유지한다.
- fresh schema EXPLAIN cost는 약 10.15로 PostgreSQL 기본 JIT threshold 아래다.

구현자가 PG16/17/18 membership integration, unit-race, vet, lint를 통과시켰다. 별도 Release B 감사는 사용자의 중단 요청으로 끝까지 실행되지 않았다.

### Query/recordops 연결

```text
M  internal/query/compiler.go
M  internal/query/cursor_v5_binding.go
M  internal/query/cursor_v5_binding_integration_test.go
M  internal/query/expand.go
M  internal/query/expand_live_test.go
M  internal/query/read_kernel_query_count_integration_test.go
M  internal/query/read_telemetry.go
M  internal/query/relation_filter_sort.go
M  internal/query/root_page.go
M  internal/query/service.go
?? internal/query/read_telemetry_test.go
M  internal/recordops/membership_availability_integration_test.go
M  internal/recordops/pagination_membership_integration_test.go
M  internal/recordops/service.go
```

구현된 내용:

- list transaction마다 기존 별도 `transaction_timestamp()` query 대신 resolver를 정확히 한 번 호출한다.
- cursor가 없는 첫 page는 ready+issue5에서만 v5 descriptor와 high-water membership boundary를 만든다.
- v4 continuation은 v4로 유지한다.
- v5 continuation은 signed descriptor와 현재 durable generation/counter를 검증한다.
- retired capability에서는 v4를 거부한다.
- scalar/relation/expand가 같은 descriptor/high-water를 사용한다.
- first-page membership-only boundary는 keyset value 없이도 SQL에 적용된다.
- recordops는 control-plane failure를 stable `record_unavailable`로 바꾸고 cursor binding mismatch는 `invalid_cursor`로 유지한다.
- relation telemetry는 unavailable과 canceled를 분리한다.
- 원래 omission을 재현하던 delayed-commit test는 실제 v5 rollout을 수행하고 first-page 이후 commit된 두 record를 continuation에서 제외하도록 바뀌었다.

확인된 focused 결과:

```text
PG16 focused query v5/rollback/availability/exact-size tests: PASS
PG16 TestReadKernelQueryCountIsBoundedByPlanShapeInsteadOfRootCount: PASS
PG16 TestListIssuesTheSameV5MembershipForRelationAndExpandedRoots: PASS
PG16 TestV5KeysetTraversalExcludesRecordsCommittedAfterFirstPage: PASS
git diff --check: PASS
gofmt -d on touched packages: clean
```

현재 blocker:

```text
TestReadKernelServiceSurfaceIsListAndGet
internal/query runtime lines = 4,888
hard limit = 4,850
required net deletion = at least 38 lines
```

따라서 Release B WIP는 아직 커밋하면 안 된다. hard limit을 올리거나 test를 약화하지 말고, R5 첫 query simplification 또는 동등한 실제 중복/주석/old-path 삭제로 38줄 이상을 상쇄해야 한다.

또한 `pgbase/app.go`는 아직 `membership.WriterA`를 등록하고 `pgbase/app_integration_test.go`도 Release A를 기대한다. 현재 코드는 library-level Release B WIP일 뿐 deployable Release B artifact가 아니다.

## 7. 현재 검증 상태를 해석하는 법

현재 worktree에서 실행한 unit 묶음 결과:

```text
go test ./internal/doctor ./internal/membership ./internal/query ./internal/recordops

doctor:     PASS
membership: PASS
recordops:  PASS
query:      FAIL — runtime 4,888 > 4,850 한 건
```

독립 Release B 감사는 다음만 확인하고 중단됐다.

- static inspection: resolver SQL, first/continuation binding, retirement/rollback branch, telemetry, recordops normalization
- `git diff --check`: clean
- `gofmt -d`: clean
- 발견된 finding: query LOC hard gate 한 건

다음 항목은 독립 감사 기준으로 아직 완료됐다고 주장하지 않는다.

- PG16/17/18 전체 Release B matrix
- forced-JIT
- 전체 query-count suite
- concurrency/lock audit
- end-to-end redaction/API wire
- full integration race

## 8. 정량 상태

`tools/repostats`는 Git 추적 파일만 계산한다. 현재 새 doctor 파일 두 개가 untracked이므로 그대로 실행한 수치만으로 WIP runtime을 과소 계산한다.

현재 tracked-only working-tree 측정:

```text
handwritten runtime: 29,930 LOC
Go runtime:           27,440 LOC
UI runtime:            2,490 LOC
interfaces:               15
exported declarations:   440
New* constructors:        30
Go/UI comments:       943 / 30
UI direct hooks:           72
UI exports:                83
```

untracked production doctor 파일 692줄을 포함한 실제 handwritten WIP runtime은 약 30,622줄이다. 원 계획의 최종 hard guardrail 22,484줄, interface 12 이하, export 285 이하, constructor 16 이하, UI 2,050줄 이하를 아직 크게 초과한다. R6~R11에서 old path와 shallow surface를 실제로 삭제하지 않으면 계획은 완료가 아니다.

## 9. 다음 구현 순서

### 9.1 현재 WIP 닫기

1. doctor WIP를 독립 재감사하고 수정한다.
2. Release B resolver/query/recordops를 다시 독립 감사한다.
3. `internal/query`에서 최소 38 runtime 줄을 실제로 삭제한다.
4. PG16/17/18 unit/integration/race/JIT/query-count/delayed-commit matrix를 실행한다.
5. 다음과 같이 scope별 commit으로 분리한다.
   - doctor membership validation
   - one-statement traversal resolver
   - query/recordops Release B integration
   - app WriterB/operator surface/runbook
6. `pgbase.App`를 WriterB로 올리고 exact-v7 startup/fleet proof/gate를 고정한다.
7. Release C에서 WriterC admission과 v4 decoder 제거를 별도 commit/gate로 처리한다.

### 9.2 R5 query correctness

R5 read-only map에서 확인된 최소 경로:

- bare single relation sort는 root inline UUID term을 사용한다.
- multiple bare relation sort는 기존 `relation_sort_unsupported`를 유지한다.
- dotted relation path만 target join/private cursor/100k budget을 사용한다.
- relation filter + `-created_at`는 root의 `DESC NULLS FIRST` page index backward scan을 사용한다.
- v5 order descriptor에서 inline single relation type은 `relation`이 아니라 `uuid`로 encode한다.
- telemetry는 bare relation filter와 dotted sort만 relation-path 작업으로 센다.

이 query simplification은 Release B의 38줄 LOC debt를 갚을 가능성이 높지만, R5 동작 변경은 별도 commit으로 남긴다.

### 9.3 R5 nullable-DESC companion index rollout

예상 commit 순서:

1. `ExpectedPhysicalSchema`에 purpose-tagged nullable-DESC companion spec과 private plan compiler v1/v2를 추가한다.
2. 다음 append-only system migration에 pending/building/ready capability만 추가한다. dynamic collection index나 CIC를 migration transaction에 넣지 않는다.
3. `schema.Service` 내부에 resumable `CREATE INDEX CONCURRENTLY` maintenance를 둔다.
4. PostgreSQL catalog를 crash checkpoint로 사용하고 invalid residue를 drop/rebuild한다.
5. doctor가 pending/building/ready와 exact physical shape를 bulk snapshot으로 비교한다.
6. fleet holdpoint 뒤 v2 issuance를 활성화하고 rollback은 v1 issuance 전환 후 P0 deploy 순서로 한다.

R5 시작 전에 결정할 사항:

- schema-writer fleet proof를 새 durable lease로 둘지 외부 drain holdpoint로 둘지
- narrow activation surface의 위치
- pending companion을 `ExpectedPhysicalSchema.Required()`와 구분하는 API
- future schema mutation의 companion을 transaction DDL과 CIC 중 어디에서 만들지
- 현재 HEAD 기준 다음 migration은 v8이지만 merge 순서가 바뀌면 번호/checksum을 다시 확인할 것

### 9.4 이후 원 계획

- R6: RecordKernel/RelationKernel deepening, cascade read/write N+1 제거
- R7: auth constructor, HTTP route registry, deadline/atomic response, app lifecycle
- R8: migration/doctor/CLI/observability/system sampler 완성
- R9a: AdminBackend, CollectionDesigner, RecordWorkspace, UI state/export/hook 감축
- R10b: Compose stop, restore drill, release tag/security/dashboard gate
- R11: old adapter/path 삭제와 LOC/interface/export/comment hard guardrail
- F0: UI navigation/delete/draft/cursor back-stack
- F1: schema operation recovery/history UX
- F2: searchable relation picker
- F3: System schema health
- R12: 동일 digest의 final release-equivalent PG16/17/18/E2E/security/restore gate

## 10. 운영 환경과 금지된 변경

로컬 PostgreSQL:

```text
PG16  postgres:16.13-bookworm  127.0.0.1:57626
PG17  postgres:17.9-bookworm   127.0.0.1:57674
PG18  postgres:18.3-bookworm   127.0.0.1:57627
```

PG18은 새 named volume `pgbase-r0-pg18-data`로 재생성됐다. 이전 disposable anonymous test volume은 사라졌지만 repository 파일이나 제품 데이터는 영향을 받지 않았다.

작업 중 실행하지 않은 외부 mutation:

- deploy
- image/tag promotion
- publish
- registry mutation
- git history rewrite
- migration v1~v7 수정

## 11. 재개 검증 명령

먼저 format/diff와 unit hard gate를 확인한다.

```bash
git status -sb
git diff --check
gofmt -d internal/doctor internal/membership internal/query internal/recordops
go test ./internal/doctor ./internal/membership ./internal/query ./internal/recordops
go vet ./...
scripts/lint-go.sh
```

PostgreSQL별 URL:

```bash
PG16='postgres://postgres:postgres@127.0.0.1:57626/postgres?sslmode=disable'
PG17='postgres://postgres:postgres@127.0.0.1:57674/postgres?sslmode=disable'
PG18='postgres://postgres:postgres@127.0.0.1:57627/postgres?sslmode=disable'
```

Release B를 커밋하기 전 최소 live gate:

```bash
PGBASE_TEST_ADMIN_URL="$PG16" go test -race -tags=integration ./internal/membership ./internal/query ./internal/recordops ./internal/api ./pgbase -count=1
PGBASE_TEST_ADMIN_URL="$PG17" go test -tags=integration ./internal/membership ./internal/query ./internal/recordops ./internal/api ./pgbase -count=1
PGBASE_TEST_ADMIN_URL="$PG18" go test -tags=integration ./internal/membership ./internal/query ./internal/recordops ./internal/api ./pgbase -count=1
```

각 candidate commit은 임시 detached worktree에서 exact snapshot으로 한 번 더 검증한다. 현재 dirty worktree의 다른 WIP가 통과 결과에 섞이지 않게 하는 것이 목적이다.
