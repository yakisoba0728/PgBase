# PgBase 전면 리팩터링 중단·인수인계

작성일: 2026-09-02 (최종 갱신: 2026-09-02, 재개 세션)

중단 사유: 사용자가 현재까지의 결과를 보존하고 전체 구현을 중단한 뒤, 완료 범위·진행 방식·잔여 작업을 문서로 남기도록 요청했다. 이후 같은 날 재개해 doctor 묶음을 감사·수정·커밋했고 Release B 감사를 마쳤다.

원 계획: [`2026-09-01-whole-codebase-refactor.md`](./2026-09-01-whole-codebase-refactor.md)

## 0. 남은 작업 TODO

우선순위 순이다. `[ ]`는 미완, `[x]`는 이 세션에서 완료.

### 0.1 Release B를 커밋 가능하게 만들기 (현재 차단 상태)

- [x] **B1 blocker — ready 상태 collection count 등식 제거.** `traversal.go`가 `ready_collection_count == cutover_collection_count`를 traversal 전제조건으로 요구했다. 두 값은 cutover 시점과 promote 시점에 각각 찍히는 receipt이고, `schema.Store.insertMembershipMetadata`는 dual_write/ready 중 collection 생성을 정식 지원한다. backfill 창에서 collection이 하나 생기면 PromoteReady가 성공한 뒤 **모든 collection의 모든 list가 영구히 503**이 되고 어떤 transition으로도 복구되지 않았다. write와 `Get`은 계속 성공해 장애가 비대칭이었다. DB CHECK에는 이 등식이 없다. 수정 후 제품 경로(cutover → collection 생성 → backfill → PromoteReady → resolve) 회귀 테스트로 고정했다.
- [x] **B2 blocker — descriptor 발급 실패를 `invalid_cursor`로 뭉개는 문제.** `root_page.go`의 v5 발급 분기와 `cursor_v5_binding.go`가 `queryErr`/`orderErr`를 전부 `invalidCursor()`로 접었다. v5 발급이 켜진 뒤 **cursor 없는 첫 요청**의 응답이 422 `validation_failed`에서 400 `invalid_cursor`로 바뀌었다(계획 §4 위반). v4/v5 동일 요청을 비교하는 회귀 테스트로 재현한 뒤 수정했다.
  - 수정 중 **감사가 보지 못한 두 번째 층**을 발견했다. 오류 클래스를 전파하도록 고치자 v5가 여전히 v4와 다른 detail code를 냈다(`type_mismatch` vs `unsupported_type`). `compileComparison`은 JSONB를 `unsupported_type`으로 먼저 막는데 fingerprint 경로는 그 guard 없이 `decodeFilterValue`를 직접 호출해 `default:` 분기로 떨어지기 때문이다. status만 고쳤으면 envelope 불일치가 남아 §4 위반이 그대로였다. JSONB 거부를 plan 단계(`planRelationFilter`)로 올려 두 경로가 단일 정책을 공유하게 했다 — 계획 §5가 지목한 "정책이 여러 module에 재구현된" 중복의 실제 사례다.
  - 테스트 자체의 결함도 하나 고쳤다. `EnableV5`는 capability singleton이라 전역인데 루프 안에서 켜면 두 번째 하위 테스트의 "before"가 이미 after다. v4 답변을 전부 먼저 고정한 뒤 한 번만 켜도록 구조를 바꿨다.
- [x] **B3 — `internal/query` LOC 부채.** blocker 2건 수정이 순증해 38 → 67줄이 됐다. 정직한 삭제 경로가 전부 막혀 있음을 확인한 뒤(아래 §0.5) 사용자 승인으로 **선택지 (B)**를 택했다: `service_surface_test.go`의 per-package ratchet을 4,850 → 4,917로 **여유 0으로** 재기준화하고 계획 §11대로 correctness category와 회계를 주석에 명기했다.

  ```text
  internal/query runtime  gross new 93 / gross deleted 26 / net +67  → 4,917
  group(database+query+recordops+relation+schema) 15,768  (hard 12,750, R11 소관)
  ```

  ratchet은 없어진 것이 아니라 재기준화된 것이다. 이 값을 다시 올리려면 그것을 상쇄하는 삭제와 함께여야 한다.
- [ ] **B4 — Release B 커밋.** resolver와 query/recordops는 **한 커밋이어야 한다.** membership WIP가 free function `ValidateTraversal`을 제거하고 method `Traversal.ValidateV5`로 대체하는데 HEAD의 `internal/query/cursor_v5_binding.go:22`가 그것을 쓴다. 격리 worktree에서 확인함: resolver만 얹으면 `undefined: membership.ValidateTraversal`로 빌드 실패. 계획 §11이 같은 stack 안의 compatibility wrapper를 권하지 않으므로 shim을 두지 말고 한 커밋으로 묶는다.
- [ ] **B5 — 커밋 전 최소 live gate.** 아래 §11의 PG16 race + PG17/PG18 integration 3종을 다시 실행하고, candidate commit을 임시 detached worktree에서 exact snapshot으로 재검증한다.

### 0.2 Release B 감사가 남긴 결정 사항 (merge 전 판단 필요)

- [x] **read path가 정확한 v7 설치를 무조건 요구하는 것 — 유지하기로 결정.** resolver SQL의 `limit $4 = len(versions)+1`은 의도적 설계다. 바이너리가 아는 것보다 migration이 하나 더 있으면 `historyCount`가 초과하고 `historyMatches`도 false가 되어 두 신호가 동시에 뜬다. 즉 "DB가 나보다 앞서면 fail-closed"가 명시적 의도이며, 계획 §11이 요구한 fail-closed와 일치한다. 완화하면 임베드하지 않은 migration의 checksum을 검증할 수 없는 상태에서 membership 객체를 신뢰하게 된다. 대응책은 계획의 `P0 → M → F` 순서 자체다 — migration 적용 전에 그것을 인식하는 아티팩트를 전 fleet에 먼저 배포한다. `docs/upgrades.md`의 "Record membership v7 feature release"에 명시했다.
- [x] **sidecar 없는 base row의 노출 방식 — runbook으로 확정, metric은 R8로 이관.** 읽기 경로 counter는 구현 불가로 판단했다. 제외된 row를 세려면 "반환되지 않은 base row"를 알아야 하고, 그것은 doctor가 수행하는 것과 같은 O(전체 레코드) anti-join이라 list 경로에 올릴 수 없다. doctor의 `record_membership_sidecar_missing`이 유일한 탐지 수단이며, 이를 background sampler로 metric화하는 것은 R8 소관으로 옮겼다. 수동 대응 절차는 `docs/upgrades.md`에 적었다.
- [ ] **rollback 상태에서 v5 cursor가 user error로 표면화되는 것.** control-plane rollback 상태에서 v5 cursor가 `invalid_cursor`가 된다. 명세("현재 durable generation/counter에 대해 검증")와는 일치하나 의식적 승인이 필요하다.

### 0.3 Release B 감사 minor 항목

- [ ] `compiler.go` / `relation_filter_sort.go`의 `if after != nil && len(after.Values) != 0`. 새 value-less first-page boundary에 필요한 guard지만, 향후 zero-value boundary가 생기면 hard error가 아니라 **1페이지로 조용히 재시작**한다. 현재 도달 경로는 없다(제거된 안전망).
- [ ] `cursor_v5_binding.go`의 `newCursorV5Descriptor`가 `IssueVersion`만 보고 unexported `traversal.ready`를 확인하지 않는다. `validTraversalStatus` 덕분에 현재만 안전하다. `bindCursorV5`는 `ValidateV5`로 확인하므로 발급 경로도 맞추는 것이 좋다.
- [ ] `TestV5KeysetTraversalExcludesRecordsCommittedAfterFirstPage`가 continuation 1회만 검사하고 high-water 실제 값을 단언하지 않는다. 3페이지 이상 carry 케이스 추가.
- [ ] `expand_live_test.go`가 `IssueVersion: cursor.Version4`로 stub해서 그 고정 시간 경로는 v5를 전혀 타지 않는다.

### 0.4 doctor 후속 항목 (커밋됨, blocker 아님)

- [x] **D1 blocker — `IntervalStyle` 의존 CHECK 해시.** 아래 §5 참고. 수정·커밋 완료.
- [x] **D2 — O(전체 레코드) scan에 대한 §5 rationale 주석.**
- [x] **D5/D6 — 누락 픽스처 2건.** 변이 검증까지 완료.
- [ ] **D3 — 동적 identifier + schema pin 부재.** doctor는 `pinSchemaShared`를 호출하지 않아 DDL이 동시에 돈다. `readMembershipCollections`와 collection별 probe 사이에 `app."order"`가 drop되면 raw `42P01`이 뜬다. doctor가 identifier를 SQL에 보간하는 첫 사례다. fail-closed이고 stderr 진단은 프로젝트 설계이므로 오탐 경보지 redaction 결함은 아니다.
- [ ] **D4 — 다섯 membership table에 trigger/rule/RLS 검사 없음.** `relations.go`는 relation table의 trigger와 function body까지 고정하는데 membership은 column/제약/index만 본다. `BEFORE INSERT ... RETURN NULL` trigger나 RLS가 coverage query와 sidecar scan을 조용히 무력화할 수 있다. 해시 고정의 목적("같은 이름의 약화된 대체물은 통과하면 안 된다")과 위협 모델이 불일치한다.
- [ ] **D7 — `cutover` 상태 테스트 없음.** 코드는 맞다(`transitions.go`가 `finishCutover` 전까지 counter를 0으로 두고 `writer.go`가 모든 write에 `ErrUnavailable`). 하지만 `cutover` 분기를 `dual_write` 규칙으로 뒤집어도 아무 테스트도 깨지지 않는다.
- [ ] **D8 — `assertBoundedMembershipReport`는 거의 tautology.** 모든 finding message가 문자열 상수라 byte 경계가 구조적으로 깨질 수 없다. report byte는 bound하지만 **scan 작업량은 bound하지 않는다**(D2와 별개).
- [ ] **D10 — `appendFindingOnce`가 `relations.go`의 `appendRelationFinding`과 byte 단위 중복.** §12 위반은 아니고 중복 지표 항목. 변이 검증 중 확인한 사실: membership 경로에서 이 dedup은 **도달 불가능**하다. 루프 뒤 단일 `if orphanedFound` 분기가 이미 1회를 보장한다.
- [ ] **D11 — `cli/run.go`가 `len(report.Findings) == 0`일 때만 `diagnostic_failed`를 붙인다.** finding을 붙인 뒤 오류가 나면 운영자는 진단이 잘렸다는 신호 없이 부분 report를 본다. 기존 문제지만 D3이 도달 빈도를 높인다.
- [ ] **doctor 전체 query-count hard assertion.** 아직 없음(확인함: `internal/doctor`·`internal/testutil` 어디에도 `pgx.QueryTracer`를 쓰는 코드가 없다). `internal/query`의 `readKernelQueryTracer` 패턴을 쓰면 된다. **주의: 현재 코드는 이 assertion을 통과하지 못한다** — membership 경로가 ready에서 `6 + 2N`(N = collection 수)이고, 기준선의 `checkCollections`도 이미 table/field당 query를 낸다. 즉 doctor 전체 query-count gate는 이 WIP가 만든 문제가 아니라 **기존 실패**이며, 이 WIP는 여기에 데이터 비례 축을 하나 더 얹었다. R8에서 정리한다.

### 0.5 `internal/query` 38줄 LOC 부채 — 조사 결과

hard gate는 `service_surface_test.go`의 `internal/query` runtime <= 4,850이다. `ac90b4f` 시점 값이 **정확히 4,850**이고 Release B WIP가 **정확히 +38**(62 삽입 / 24 삭제)이라 여유가 0이다.

정직한 삭제 경로를 전부 조사했고 **모두 막혀 있다**:

- **죽은 코드 없음.** `.golangci.yml`에 `unused`, `unparam`, `ineffassign`, `wastedassign`이 켜져 있고 `scripts/lint-go.sh`가 0 issues다.
- **계획 §5의 query 삭제 인벤토리 소진.** `expandRecords`, `isExpandBudgetError`, `retryableTransactionError`, `strconvItoa`, `relationPlanningErrorForCursor`는 전부 R3에서 이미 제거됐다(grep으로 부재 확인).
- **주석은 대부분 §5 "유지" 대상.** `internal/query` runtime 주석 108줄 중 `filter.go`의 index/null ordering 근거, `codec.go`의 btree 한계·보안 근거 등이 §5가 명시적으로 보존을 요구한 항목이다. 정직한 "what" 주석은 1~6줄뿐이다.
- **R5 §9.2 항목 1은 부채를 갚지 못한다 — 오히려 +9줄이다.** 격리 worktree에서 실제 구현해 측정했다(4,850 → 4,859). dotted path가 `renderRelationSortJoins`/budget/`terminalIsRelation`을 계속 쓰므로 삭제되는 코드가 없다. 핸드오프 이전 판의 "38줄을 갚을 가능성이 높다"는 추정은 **틀렸다**.
- **`cursor_v5_binding.go`의 +34줄은 중복이 아니다.** Release B 감사가 확인: `cursor_v5_fingerprint.go`를 중복하지 않고 호출한다.
- **`root_page.go`의 두 대형 함수는 통합 대상이 아니다.** 배치 순차 하이드레이션과 단발 읽기로 실제 다른 알고리즘이다.

계획 §3의 구속력 있는 목표는 per-package가 아니라 **그룹 단위**다.

```text
database/query/recordops/relation/schema  →  checkpoint 11,900 이하 / hard 12,750
실측(2026-09-02): 15,739
  database 396 / query 4,888 / recordops 2,074 / relation 2,005 / schema 6,376
```

즉 그룹이 약 3,000줄 초과 상태이고, 그 삭제는 **R6(RecordKernel/RelationKernel deepening)과 R11(old adapter/path 삭제)**이 갚기로 되어 있다. 4,850은 계획이 정한 값이 아니라 구현자가 R3 완료 시점 값으로 건 per-package ratchet이다.

계획 §11은 "LOC 감소는 개별 commit이 아니라 stack 전체에서 측정한다. characterization, **correctness**, security, migration, doctor 강화, 기능 stack은 **양수일 수 있으며** category를 별도 표기한다"고 한다. Release B는 pagination silent omission을 고치는 correctness stack이다.

결정 대기 중인 선택지:

- (A) R6/R11의 삭제를 앞당겨 38줄을 먼저 확보한다. 계획의 단계 순서를 뒤집고 correctness 수정을 지연시킨다.
- (B) ratchet을 Release B stack의 일부로 재기준화하고 §11대로 correctness category로 명기한다. 그룹 12,750을 실질 가드로 두고 R11을 강제 지점으로 삼는다. **권고안.**
- (C) `internal/query`에서 추가 통합을 계속 찾는다. 위 조사 기준으로 남은 여지는 크지 않다.

### 0.6 이후 원 계획 단계

- [ ] **R4 Release C** — WriterC admission, v4 decoder 제거, 최종 rollout gate.
- [ ] **R5** — §9.2 query correctness(항목 1은 격리 worktree에 구현·검증됨, 아래 참고), §9.3 nullable-DESC companion index rollout.
- [ ] **R6** — RecordKernel/RelationKernel deepening, cascade read/write N+1 제거.
- [ ] **R7** — auth constructor, HTTP route registry, deadline/atomic response, app lifecycle.
- [ ] **R8** — migration/doctor/CLI/observability/system sampler 완성. doctor query-count gate와 old doctor path 삭제를 여기서 끝낸다.
- [ ] **R9a** — AdminBackend, CollectionDesigner, RecordWorkspace, UI state/export/hook 감축.
- [ ] **R10b** — Compose stop, restore drill, release tag/security/dashboard gate.
- [ ] **R11** — old adapter/path 삭제와 LOC/interface/export/comment hard guardrail.
- [ ] **F0~F3** — UI navigation/delete/draft/cursor back-stack, schema operation recovery/history UX, searchable relation picker, System schema health.
- [ ] **R12** — 동일 digest의 final release-equivalent PG16/17/18/E2E/security/restore gate.

### 0.7 R5 §9.2 항목 1 — 이미 검증된 설계

격리 worktree에서 구현하고 동작을 확인했다. 커밋하지 않았고 스크래치패드에만 있다.

정확성 근거:

- `internal/schema/planner.go`가 single relation에 `FOREIGN KEY (col) REFERENCES target(id) ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED`를 건다. 커밋된 스냅샷에서는 항상 성립하므로 `LEFT JOIN target ON target.id = root.col`의 `target.id`는 `root.col`과 정확히 같다(둘 다 NULL이거나 둘 다 같은 값).
- 같은 파일이 `SingleReverseIndex`를 root 컬럼에 항상 만든다. inline 정렬이 root 인덱스를 탄다.
- `internal/schema/relation_types.go`가 relation 필드를 무조건 `Indexed = true`로 정규화하므로 `indexedOrderHint(field, orderIndexRoot)`가 성립한다.

변경: `planRelationSort`에서 dot 없는 `RelationSingle` 필드를 relationPath term이 아니라 **root orderTerm**으로 낸다. cursorField는 `ID`/`ColumnName`은 보존하고 `Type`만 `TypeUUID`로 합성한다(`cursorV5Path`가 field id를 인코딩하고, v5 descriptor가 `relation`이 아니라 `uuid`로 인코딩되며, `renderRelation`이 `qualified(rootAlias, cursorField.ColumnName)`으로 렌더링하기 때문). multi-cardinality는 `relation_sort_unsupported`를 유지한다.

동작 확인 결과: `sort=owner`는 private/join 해제, `sort=tags`(multi)는 에러 유지, `sort=owner.name`(dotted)은 join+private 유지.

뒤집어야 할 R0 characterization 테스트 3건:

- `internal/query/relation_filter_sort_test.go` `TestBareRelationSortCurrentlyJoinsTargetAndRequiresPrivateCursor`
- `internal/query/relation_filter_sort_live_test.go` `TestRelationSortCurrentBudgetIncludesBareRelationSortAt100001`
- `internal/query/relation_explain_integration_test.go` `TestCurrentRelationFilterRootDescendingSortUsesFullSortInsteadOfPageIndex`

## 1. 재개할 때 가장 먼저 할 일

1. `git status -sb`와 이 문서를 읽는다.
2. 기존 변경을 reset, checkout, clean, stash 하지 않는다. 현재 WIP는 의도적으로 보존되어 있다.
3. 위 §0.1 순서대로 Release B를 닫는다.
4. 파일 묶음별로 stage하고 §11 검증 명령을 다시 실행한다.

이 작업은 더 이상 detached HEAD가 아니다. 재개 세션에서 43개 커밋을 브랜치로 고정했다.

```text
worktree: /Users/yakisoba0728/.codex/worktrees/0f44/PgBase
branch:   refactor/whole-codebase
code:     a50a2c0  (doctor membership validation, 재개 세션에서 커밋)
base:     ac90b4f  (이전 세션의 문서 checkpoint)
```

`refactor/whole-codebase`는 아직 push하지 않았다.

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
