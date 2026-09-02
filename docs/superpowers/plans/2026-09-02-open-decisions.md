# PgBase 전면 리팩터링 — 결정 대기 항목

작성일: 2026-09-02 (재개 세션)

원 계획: [`2026-09-01-whole-codebase-refactor.md`](./2026-09-01-whole-codebase-refactor.md)
진행 현황: [`2026-09-02-whole-codebase-refactor-handoff.md`](./2026-09-02-whole-codebase-refactor-handoff.md)

브랜치 `refactor/whole-codebase`. 이 문서는 구현을 계속 진행하기 위해 사람의 판단이 필요한 항목만 모은 것이다. 이미 결정해 반영한 사항은 4절에 근거와 함께 적었으니 뒤집을 근거가 있으면 그때 바꾼다.

2026-09-03 최종 재감사에서 이 문서의 구현 판단은 모두 닫혔다. 아래의
“블로커” 표현은 당시 진행 순서를 설명하는 역사 기록이며 현재 상태가
아니다. 외부 CI·registry 실행은 권한/환경 의존 검증이지 설계 판단 대기
항목이 아니다.

---

## 결정 완료 (2026-09-02)

아래가 확정된 답이다. 각 절의 배경 설명은 근거로 남겨두되, 실행 기준은 이 절이다.

### D1 — (a) 재예측 채택

- R11 data-group 예상 **16,100~16,500**, hard limit **16,750**.
- **R5 완료 직후** 실제 gross-new/gross-deleted를 반영해 한 번 고정하고, 이후에는 올리지 않는다.
- 기존 12,750을 맞추려고 검증된 correctness 코드를 줄이지 않는다.
- 전체 runtime hard target도 R11에서 함께 재산정한다.
- **package별 zero-slack ratchet과 gross-new/deleted 회계는 그대로 유지한다.** 숫자만 현실화하는 것이지 강제력을 푸는 것이 아니다.

### D2-1 — 별도 durable schema-writer lease

record writer lease를 재사용하지 않는다. v8에 schema compiler/writer version용 lease와 capability를 별도로 둔다. 외부 drain만으로 안전성을 증명하지 않는다.

### D2-2 — activation surface

DB capability row가 유일한 source of truth다. `internal/schema.Service`가 좁은 activate/rollback transition을 소유한다. operator CLI에서만 호출하고 HTTP, 환경변수, public `pgbase.Options` 어디에도 노출하지 않는다.

### D2-3 — manifest API

`Required()`는 현재 즉시 필수인 core object만 유지한다. `CompanionIndexes()` 같은 별도 immutable accessor를 두고 `IndexPurposeNullableDesc`로 구분한다. ownership은 required와 companion의 합집합으로 계산한다. doctor는 capability가 ready일 때만 companion을 필수 physical contract로 판정한다.

### D2-4 — 향후 schema mutation

새 collection은 v2에서 companion index를 **생성 transaction 안에서** 만든다. 기존 collection에 field/index를 추가하는 경우 companion만 **CIC maintenance**로 만든다. 이때 capability를 pending으로 내리고, maintenance가 current catalog를 검증한 뒤 ready로 복귀시킨다.

### D2-5 — migration 번호

결정 당시 embedded migration은 v1~v7뿐이었으므로 다음은 v8로 확정했다. **이행 완료**: v8 `schema_companion_indexes`가 checksum `e1cf6ca6e847d1b0593c6def69c5c3a65c0916efe1011fbdacedb87f72177cff`로 들어갔고 `embed_test.go`에 고정, `docs/upgrades.md`에 공개됐다. 그 다음 migration은 v9다 — **이행 완료**: v9 `schema_compiler_generation`은 재감사의 pre-P0 writer/maintenance drain, protocol fence, 세대화된 maintenance 상태까지 포함한 checksum `f4d5b404ed1c5df0ecbea6066a7285b27fee62aa47aaf157a6a47cbd9aaf1918`로 고정됐다. 그 다음은 v10이다.

### D3-1 — 승인

Compose의 `PGBASE_STATEMENT_TIMEOUT` passthrough 유지.

### D3-2 — E2E 60초 유지 + 기본 5초 별도 검증

E2E의 60초 timeout은 유지하되, 기본 5초는 별도의 가벼운 Compose/config policy test로 검증한다.

### SARIF

gate는 HIGH/CRITICAL로 유지하고, 가능하면 전체 severity SARIF를 별도 artifact로 보존한다.

### D4 — 4.1~4.5 전부 현재 결정 유지

advisory key 미통합, Release C 후순위, exact-v7 강제 유지, sidecar runbook + R8 metric, `internal/query` ratchet 재기준화 모두 그대로.

### D5 — 승인

R10b의 `internal/repositorypolicy/release_workflow_test.go` 수정은 workflow 정책 테스트를 green으로 유지하기 위한 정당한 예외로 승인.

---

## D6 — 전체 정량 지표 현황 (R11 재산정 입력값)

D1에서 "전체 runtime hard target도 R11에서 함께 재산정한다"고 결정했다. R9a 통합 시점의 실측이다. R8, R5 §9.2, R5 §9.3이 아직 착지하지 않았으므로 최종값은 아니다.

```text
지표                        현재      계획 target      차이
handwritten runtime       31,726      22,484        +9,242
  Go runtime              29,205
  UI runtime               2,521       2,050          +471
interface 선언                 14          12            +2
exported 선언                 442         285          +157
New* constructor              30          16           +14
Go runtime 주석 줄         1,268         600          +668
UI page 직접 hook             19          30       ✅ 달성
Playwright              231줄/6파일     최소 5      ✅ 달성
```

Go runtime 상위 기여자:

```text
internal/schema   6,376      internal/relation   2,008
internal/query    4,917      internal/adminauth  1,331
internal/api      2,522      internal/cli          926
internal/doctor   2,410      pgbase                881
internal/recordops 2,157     internal/cursor       719
```

`tests/` 아래 non-test Go는 66줄뿐이라 측정 아티팩트가 아니다. 실제 runtime이다.

### 해석

계획의 22,484는 baseline이자 hard guardrail이었다("현재 22,484줄을 넘지 않는 것을 hard guardrail로 둔다"). 즉 리팩터링 후에도 늘지 않는 것이 전제였는데, 현재 41% 초과다. R2b ledger, R4 membership sidecar/rollout, R7 route registry와 ledger recovery 계약, R9a의 port/location 계층이 전부 계획이 요구한 신규 표면이고 baseline에는 없던 것들이다.

R9a 담당 에이전트의 보고를 그대로 옮기면: "AdminLocation(137), ports 계층(176 + 306 vs 기존 `api/*` 394), opaque value 타입과 `useResourceState`는 R9a 항목 1~2가 *요구하는* 표면이고 2,490 baseline에는 존재하지 않았다. 계획의 2,041 추정치는 이들보다 앞선다." 아무도 import하지 않는 export 7개를 제거하고 선언을 압축해 −42줄/−9 export를 확보했지만, 표면을 줄이지 않으면서 지표만 맞추는 `export type {…}` 묶음은 하지 않았다.

주석 1,268줄은 특히 주의가 필요하다. 계획 §5는 주석을 리팩터링과 함께 **제거**하라고 하지만 동시에 PostgreSQL snapshot/lock/null ordering 근거, 보안 fail-closed 근거, cursor rollout 제약은 **유지**하라고 한다. 이번 세션의 단계들은 후자를 대량으로 추가했다(deadline margin 근거, unwrap 근거, savepoint 근거, advisory key 미통합 근거 등). 600줄 목표는 그 유지 대상을 셈에 넣지 않은 값으로 보인다.

### R11에서 결정할 것

- 전체 runtime hard target의 새 값
- exported 선언, New* constructor, 주석 줄 목표의 새 값 — 또는 이들을 hard KPI에서 빼고 방향성 지표로 강등할지
- UI 2,050의 새 값
- 어느 지표를 zero-slack ratchet으로 강제하고 어느 것을 보고 전용으로 둘지

### R11 재산정 결과 (확정)

R11이 위 네 항목에 답했고, 답한 숫자는 전부 `internal/repositorypolicy/loc_budget_test.go`의 실행 가능한 게이트로 고정했다. 각 임계값은 1 낮춰서 실패하는 것을 확인한 뒤 되돌리는 mutation check를 거쳤다.

| 지표 | R11 시작 | R11 종료 | 계획 target | 판정 |
|---|---:|---:|---:|---|
| data group | 16,734 | **16,653** | 12,750 (D1 상한 16,750) | hard gate |
| handwritten runtime | 34,956 | **34,812** | 22,484 | hard gate |
| ‌  Go runtime | 31,483 | 31,339 | — | (구성) |
| ‌  UI runtime | 3,473 | 3,473 | 2,050 | hard gate |
| interface 선언 | 17 | 17 | 12 | hard gate |
| exported 선언 | 481 | **467** | 285 | hard gate |
| New* constructor | 30 | **25** | 16 | hard gate |
| Go runtime 주석 줄 | 1,832 | **1,739** | 600 | 보고 전용 지표 |
| UI page 직접 hook | 19 | 19 | 30 | hard gate (달성) |
| UI runtime export | 119 | 119 | 65 | hard gate |
| 800줄 초과 production 파일 | 3 | **0** | 0 | hard gate (달성) |

R11 stack 회계 (`go run ./tools/repostats --compare fb9bb3a`): runtime gross-new 1,036 / gross-deleted 1,179 / net **−143**, test +199, support −1, generated·docs·config 0. 파일 분할은 이동한 줄을 삭제 1 + 추가 1로 세므로 gross 양쪽이 그만큼 부풀어 있다.

**전체 runtime hard target = 34,812.** 계획의 22,484는 baseline guardrail이었고 현재는 55% 초과다. 초과분은 계획 본문이 뒤에 요구한 표면이다 — R2b operation ledger, R4 membership sidecar/rollout/lease, R5 companion index capability, R7 route registry와 ledger recovery 계약, R9a port/location 계층. 계획 §11이 correctness·migration stack의 LOC 양수를 명시적으로 허용하므로 이들을 줄여 22,484로 되돌리는 것은 §12가 금지하는 방향이다. 또한 이 숫자에는 repostats 정의상 `internal/testutil` 247, `tests/` 66, `migrations/system` 95가 포함된다. 측정 정의를 바꿔 숫자를 낮추지 않았다.

**data group = 16,653.** D1이 준 예상 대역 16,100~16,500보다 153줄 높고 상한 16,750보다 97줄 낮다. R11이 이 그룹에서 실제로 삭제한 것은 planner 진입점 6개와 catalogAware 분기, incoming fallback, 세 개의 forwarding constructor, 죽은 accessor다. 그 이상은 검증된 correctness 코드를 줄이는 일이라 하지 않았다. 파일 분할이 +40을 되돌려 놓은 것도 이 숫자에 들어 있다.

**exported 선언 467 / New* constructor 25 / interface 17 — hard gate로 유지.** 285·16·12에는 못 미치지만 zero-slack ratchet은 유지한다. export를 하나 늘리려면 그것을 상쇄하는 삭제와 같은 stack이어야 한다는 강제력은 숫자와 무관하게 작동한다. interface 17은 줄이지 않았다: 남은 것 중 `api.SchemaService`/`RecordService`/`AdminAuth`는 HTTP 계층이 도메인 패키지를 import하지 않게 하는 경계이고, `database.DBTX`·`api.Readiness`·`pgbase.backgroundRunner`·`cli.Terminal`은 production 구현이 둘 이상이다.

**UI 3,473 — hard gate로 유지, 2,050은 폐기.** R9a 담당 에이전트가 이미 정직한 감축을 소진했고(아무도 import하지 않는 export 7개 제거, −42줄), `AdminLocation`·ports 계층·`useResourceState`는 R9a 항목 1~2가 요구한 표면이라 2,050 baseline에 존재하지 않았다. UI export 119도 같은 이유로 65 대신 현재값에 고정한다.

**주석 600 — 도달 불가. 보고 전용 지표로 강등.** 전수 감사 결과 확신 있는 삭제 대상은 89줄이었고 R11이 그중 81줄을 지웠다(나머지는 seam 삭제로 함께 사라짐). 정직한 바닥은 **약 1,670~1,730**이다. 1,739에서 600까지의 1,139줄은 advisory lock 순서와 snapshot 시점, `DESC NULLS LAST` companion index, Read Committed/RepeatableRead 선택, fail-closed redaction, 응답·hydration·cascade budget, rollout/rollback 조건 — 계획 §5가 명시적으로 **유지**를 요구한 항목이다. 이 지표만 ratchet을 걸면 §12의 "주석 수치를 맞추기 위한 보안·동시성 rationale 삭제" 쪽으로 압력이 생긴다. 다른 모든 지표의 압력은 코드 삭제 방향이라 ratchet이 안전하다. 따라서 주석만 게이트에서 뺐고, repostats가 매 실행 보고한다.

**600줄 초과 파일의 module depth 근거** (계획 §13 요구). 800줄 초과는 0이 됐고, 남은 여섯은 각각 하나의 개념이다.

- `internal/schema/companion_index.go` 779 — nullable-DESC companion index rollout 전체(capability 전이, schema-writer lease, CIC maintenance, 상태 조회). D2-1~D2-4가 이 넷을 한 상태 기계로 결정했으므로 나누면 전이 규칙이 파일 경계를 가로지른다.
- `pgbase/app.go` 686 — 프로세스 조립과 종료 순서. 종료 순서 자체가 계약이라(HTTP drain이 background 취소보다 먼저) 조립과 분리하면 그 순서를 읽을 곳이 없어진다.
- `internal/schema/operation_store.go` 679 — schema operation ledger의 durable 표현 하나. row 스캔·claim·완료·조회가 같은 열 집합을 공유한다.
- `internal/recordops/service.go` 654 — List/Get/Create/Update/Delete 다섯 진입점과 그 트랜잭션 정책. relation telemetry와 idempotency는 R11에서 분리했다.
- `internal/schema/service.go` 641 — catalog 캐시 pin, plan preview, apply 진입점, catalog 무결성 검증. 무결성 규칙은 apply가 신뢰하는 전제라서 함께 읽혀야 한다.
- `internal/observability/metrics.go` 611 — metric 이름·label 집합 하나. 계획 §12가 metric rename/label 의미 변경을 금지하므로 이 파일은 그 계약의 단일 지점이다.

**R11이 닫지 못한 것.** `internal/membership`의 rollout transition(`Cutover`/`Backfill`/`PromoteReady`/`EnableV5`/`DisableV5`/`RetireV4`)은 production caller가 0이다. 삭제하지 않았다 — 이들은 `docs/upgrades.md`가 기술한 운영 rollout 절차 자체이고, `DisableV5`는 `EnableV5`의 롤백 경로다. 진행 중인 rollout의 롤백을 지우는 것은 삭제가 아니라 운영 능력의 제거다. D2-2가 결정한 "operator CLI에서만 호출"이 아직 배선되지 않은 상태이며, 그 배선은 R11의 sweep 범위가 아니다.

R12 정정: "`DisableV5`는 테스트 caller도 0"은 더 이상 사실이 아니다. Release C가 `query/cursor_v5_binding_integration_test.go`, `membership/traversal_integration_test.go`, `membership/service_integration_test.go`에서 호출한다. production caller 0과 CLI 미배선은 그대로다 — `internal/cli`의 명령 집합은 `serve`/`migrate`/`admin`/`doctor`/`schema-index`/`healthcheck`뿐이다.

---

## D1 — 그룹 LOC 예산 3,100줄 초과 (블로커, R11)

### 측정값

```text
database+query+recordops+relation+schema = 15,854
계획 §3:  checkpoint 11,900 / 예상 12,150~12,650 / hard 12,750

  database     396
  query      4,917
  recordops  2,157
  relation   2,008
  schema     6,376
```

### 왜 R11이 이걸 갚을 수 없나

R6는 계획의 핵심 목표를 달성했다. cascade delete의 read/write N+1이 사라졌고 tracer 실측으로 `6N+6`에서 **레코드 수와 무관한 상수 12**가 됐다(N=8일 때 54→12, N=64일 때 390→12). 그런데 그 배치 처리가 **순 +86줄**이다(gross-new 959 / gross-deleted 873). 담당 에이전트는 두 패키지 목표 3,650을 4,165로 미달했다고 보고하면서, 정직한 삭제를 소진한 뒤 추가로 찾을 수 있는 건 60~80줄 정도이며 완전히 테스트된 delete 경로를 불안정하게 만들 가치가 없다고 판단했다. rationale 주석을 잘라 숫자를 맞추지는 않았다.

계획 §11은 correctness stack이 LOC 양수여도 된다고 명시한다. R6는 그 정의에 정확히 해당한다.

`internal/schema`가 6,376줄로 단일 최대 기여자인데 **전용 deepening 단계가 없다.** R2/R2b가 SchemaKernel과 ledger를 만들었지만 그건 추가였고, R11은 계획 본문이 "각 R2~R10 묶음은 자기 replacement와 old caller/test 삭제를 완료 조건으로 갖고, R11은 저장소 전체에 남은 누락을 잡는 최종 consolidation"이라고 정의한 sweep이다. 혼자 3,100줄을 찾아낼 수 있는 단계가 아니다.

계획 §3 자체가 이미 한 번 재예측한 전례가 있다 — "심화 검토 전의 20,235줄은 pagination sidecar/backfill, schema operation ledger/reconciler, 인증 복구 정책과 release 검증 runtime까지 넣고도 10%를 줄인 수치라 hard target으로는 과도했다".

### 선택지

- **(a) 재예측.** R2b/R4/R5/R6의 실제 비용을 반영해 그룹 예산을 다시 잡는다. §3이 한 번 했던 것과 같은 조정. 게이트의 강제력은 유지하되 숫자를 현실에 맞춘다.
- **(b) `internal/schema` 전용 단계 신설.** 최대 기여자를 실제로 깊게 만든다. 새 단계이므로 일정이 늘어난다.
- **(c) 게이트 유지 + R11 확장.** R11을 sweep이 아니라 실질 deepening 단계로 재정의한다. 계획의 R11 정의를 바꾸는 일이다.

### 결정에 필요한 것

세 선택지 중 하나. (a)를 고르면 새 목표 숫자도 함께.

---

## D2 — R5 §9.3 companion index rollout 착수 전 결정 5건 (블로커, R5 후반)

nullable-DESC companion index는 새 system migration이 필요해서 착수 전에 답이 있어야 한다. 원 핸드오프 §9.3에 있던 항목을 그대로 옮긴다.

1. **schema-writer fleet proof를 새 durable lease로 둘지, 외부 drain holdpoint로 둘지.** 계획 §11은 "schema writer를 단일 version으로 고정하지 못하는 rollout은 시작하지 않는다"고 못박는다. R4가 record writer에 durable lease를 도입했으므로 같은 방식을 schema writer에 복제할지, 아니면 운영 절차상의 drain으로 대체할지.
2. **narrow activation surface의 위치.** v2 plan issuance를 켜는 스위치가 어디에 사는가.
3. **pending companion을 `ExpectedPhysicalSchema.Required()`와 구분하는 API 형태.** doctor가 pending/building/ready 전이 상태를 구분해야 하는데, manifest의 필수 객체 집합과 아직 만들어지는 중인 객체를 어떻게 나눌지.
4. **향후 schema mutation의 companion을 transaction DDL에서 만들지, CIC로 만들지.** 기존 collection index는 `CREATE INDEX CONCURRENTLY`로 가는 것이 계획에 명시돼 있는데, 앞으로 새로 생기는 collection의 companion도 같은 경로인지 아니면 생성 트랜잭션 안에서 만드는지.
5. **다음 migration 번호가 v8인지 재확인.** 현재 HEAD 기준으로는 v8이지만 merge 순서가 바뀌면 번호와 checksum을 다시 확인해야 한다.

### 참고: §9.2는 진행 중

R5의 query correctness 절반(§9.2)은 `internal/schema`를 건드리지 않으므로 이 결정 없이 진행 중이다. §9.3만 대기 상태다.

---

## D3 — R10b가 남긴 검토 요청 2건 (비블로커)

담당 에이전트가 명시적으로 검토를 요청한 항목이다.

1. **`docker-compose.yml`에 `PGBASE_STATEMENT_TIMEOUT` passthrough 추가** (기본값 5s 유지). drain 테스트에서 PostgreSQL이 차단된 쿼리를 종료 창이 닫히기 전에 취소해버려서 필요했다. 문서화된 런타임 변수인데 번들 profile에서 설정할 수 없던 상태였다. 미설정 사용자에게는 동작 변화가 없다.
2. **E2E Compose 프로젝트가 60s statement timeout으로 전환.** 그 결과 E2E가 더 이상 5s 기본값을 검증하지 않는다. 의존하던 테스트는 없었지만 커버리지 축소다. 유지할지, 5s를 검증하는 별도 케이스를 되살릴지.

부수적으로 SARIF를 HIGH/CRITICAL로 필터링했다(게이트와 일치). 필터 없이 남기면 기록은 더 풍부해진다 — 선호가 있으면 알려달라.

---

## D4 — 이미 결정해 반영한 사항 (확인만, 뒤집을 근거 있으면 알려달라)

### 4.1 advisory key 파생 2개를 통합하지 않음

`relation.collectionAdvisoryKey`(raw sha256)와 `recordops.advisoryLockKey`(`recordartifact.Digest`, length-prefixed)가 남아 있다. 계획은 advisory-key primitive를 1개로 요구하지만, 해시를 통일하면 **lock key 바이트가 바뀐다.** rolling deploy 중 구 바이너리와 신 바이너리가 같은 collection에 서로 다른 키를 계산해 **상호배제를 잃는다.** 어떤 wire 테스트도 잡지 못하는 데이터 정합성 회귀다.

ordering comparator는 실제로 1개로 통일됐고(`relation.recordRefLess`, recordops 사본 삭제), caller options bag은 0이다. 남은 것은 key 파생 2개뿐이며 각각 고정된 단일 도메인 파생이고 자기 entry point가 소유한다.

### 4.2 R4 Release C를 마지막으로 미룸

계획 순서상 Release C는 R5보다 앞이지만, v4 decoder 제거는 "모든 instance가 release B 이상임을 확인하고 15분 cursor TTL을 drain"한 뒤에만 안전하다. Release B가 이번 세션에서 방금 배포 가능해진 참이라, 코드에서 v4 지원을 먼저 없애면 되돌릴 수 없는 방향으로 앞서간다. R5 이후로 미뤘다.

### 4.3 read path의 exact-v7 강제를 유지 (코드 변경 없음)

resolver SQL이 `applied_migrations`를 `limit len(versions)+1`로 읽는 것은 **의도적 설계**다. 바이너리가 아는 것보다 migration이 하나 더 있으면 `historyCount`가 초과하고 `historyMatches`도 false가 되어 두 신호가 동시에 뜬다. 즉 "DB가 나보다 앞서면 fail-closed".

완화하면 임베드하지 않은 migration의 checksum을 검증할 수 없는 상태에서 membership 객체를 신뢰하게 된다. 계획 §11의 `P0 → M → F` 순서가 정식 대응책이다 — migration 적용 전에 그것을 인식하는 아티팩트를 전 fleet에 먼저 배포하므로, 올바른 롤아웃에서는 Release B 인스턴스가 자기보다 앞선 DB를 만나지 않는다. `docs/upgrades.md`의 "Record membership v7 feature release"에 명시했다.

### 4.4 sidecar 없는 base row는 runbook + R8 metric으로 처리

읽기 경로 counter는 구현 불가로 판단했다. 제외된 row를 세려면 "반환되지 않은 base row"를 알아야 하고, 그것은 doctor가 수행하는 것과 같은 O(전체 레코드) anti-join이라 list 경로에 올릴 수 없다. doctor의 `record_membership_sidecar_missing`이 유일한 탐지 수단이며, 이를 background sampler로 metric화하는 것은 R8 소관으로 옮겼다. 수동 대응 절차는 `docs/upgrades.md`에 있다.

### 4.5 `internal/query` ratchet 재기준화

4,850 → 4,917로 여유 0으로 재기준화하고 계획 §11대로 correctness category와 회계를 테스트 주석에 명기했다(gross-new 93 / gross-deleted 26 / net +67). 정직한 삭제 경로가 전부 막혀 있음을 확인한 뒤의 조치다 — lint에 `unused`/`unparam`/`ineffassign`/`wastedassign`이 켜져 있고 0 issues, 계획 §5의 query 삭제 인벤토리는 R3에서 소진, 주석은 대부분 §5 "유지" 대상, R5 §9.2 항목 1은 오히려 +9줄이었다.

---

## D5 — 범위 위반 신고 1건 (기록용)

R10b 에이전트가 선언한 범위 밖 파일 하나를 수정했다: `internal/repositorypolicy/release_workflow_test.go`. 그 테스트가 계획 §10이 `RestoreDrill`로 교체하라고 한 backup/restore 인라인 셸을 정확히 고정하고 있어서, 두면 트리가 red였다. drill 커버리지 단언을 워크플로 파일에서 `tests/restore/*.go`로 돌리고 나머지는 그대로 뒀다고 보고했다. 해당 파일의 소유권을 따로 두고 싶으면 알려달라.

---

## 검증할 수 없어 통과로 주장하지 않은 항목 (기록용, 결정 불필요)

R10b에서 이 환경으로는 검증 불가한 것들이다. R12가 127.0.0.1의 일회용 `registry:2`로 셋을 닫았고, 남은 것은 실제 CI에서만 확인된다.

- `gh attestation verify` — **여전히 미검증.** 실제 게시된 digest와 attestation, `GH_TOKEN` + registry 인증 경로가 필요하다. buildx가 index에 넣는 in-toto attestation manifest는 `actions/attest-build-provenance`/`attest-sbom`이 GitHub attestation store에 올리는 것과 다른 물건이라, 로컬 build가 이 경로에 준 커버리지는 0이다.
- `trivy-action`의 `trivyignores` 입력이 allowlist YAML을 실제로 소비하는지, SARIF 아티팩트 업로드가 되는지 — **여전히 미검증.** R12가 trivy 0.74.0에 `--ignorefile deployments/security/trivy-allowlist.yaml`을 직접 넘겨 파일이 파싱되고 candidate digest 스캔이 HIGH/CRITICAL 0으로 통과하는 것까지는 확인했다. action이 그 입력을 `--ignorefile`로 넘기는지는 확인하지 못했다.
- drill과 E2E의 release mode **happy path** — **닫힘.** 실제 `image@sha256`를 대상으로 PG 16/17/18 drill과 Docker Go E2E가 통과했고, 두 gate 모두 candidate가 보고한 version이 `expected_version`과 같은지 확인한다.
- `--platform linux/amd64` × `--provenance`/`--sbom` index 출력 × `--prefer-index=false` retag — **닫힘.** candidate digest는 index digest이고 index는 platform manifest 1 + attestation manifest 1을 담는다. `imagetools create --prefer-index=false`로 exact tag와 channel tag를 만든 뒤 read-back한 digest가 candidate index digest와 같았다. absent/same/different 3-way와 stable/alpha/beta/rc 승격, malformed 입력의 registry 접근 0회도 같은 registry에서 실행했다.


## 확인 필요 (통합 담당자 판단, 승인 전)

### D2-4의 delete 취급 — **해소됨 (R11)**

확인이 필요했던 이유는 판정이 change의 *kind*를 키로 삼았기 때문이다. kind로 나누면 create/update/delete
각각을 정책으로 하나씩 지명해야 하고, D2-4가 명명하지 않은 delete는 정책 확장이 된다.

R11은 판정을 kind가 아니라 **companion manifest의 delta**로 바꿨다. capability가 지키는 불변식은
"커밋되는 catalog가 아무도 만들지 않은 companion을 요구하지 않는다" 하나뿐이므로, 그것만 게이트를 닫는다.
이 규칙 아래에서 delete는 manifest 항목을 제거하기만 하므로 빈틈을 만들 수 없고, 따라서 **정책 확장이 아니라
규칙의 결과로** capability를 유지한다. 같은 규칙이 D2-4가 명명한 두 경우도 그대로 재현한다: companion을
새로 부르는 update는 닫고, v2 create는 자기 테이블의 companion을 같은 트랜잭션에서 만들므로 유지한다.

부수적으로 no-op update(재적용, unindexed 컬럼 추가)도 유지하게 됐다. kind 판정에서는 아무 index도 건드리지
않는 update가 capability를 pending으로 떨어뜨려 전체 재롤아웃을 요구했다(P2-C).

따라서 이 항목은 더 이상 통합 담당자의 승인을 기다리지 않는다.

## 2026-09-03 최종 재감사에서 닫은 추가 판단

### `App.Start` context 의미 — 현재 계약 유지

`Start`의 context는 라이브러리 임베더가 소유한 background lifetime이다.
호출자는 자기 HTTP server를 먼저 종료한 뒤 `App.Close`를 호출해야 하며,
context 취소는 background 작업을 직접 취소한다. `Run`은 HTTP server까지
소유하므로 signal context를 drain 뒤까지 분리하는 별도 계약이다.
`Start`까지 `context.WithoutCancel`로 바꾸면 기존 공개 의미를 뒤집으므로
동작은 유지하고 exported godoc과 lifecycle/race 테스트로 고정했다.

### doctor의 rollout 미시작 탐지 — 구현

`off` + 기존 base record는 더 이상 정상 보고가 아니다. 기존의 한 번짜리
folded sidecar scan을 재사용해
`record_membership_rollout_not_started`를 내며, statement ceiling은 올리지
않았다. 빈 `off` 데이터베이스는 정상이고, 불완전한 설치는 더 강한
`record_membership_installation_invalid`로 남으며, 정상 진행 상태인
`cutover`/`dual_write`는 경고하지 않는다.

### retained Release B — stabilized v7/v8 artifact

“Release B는 exact v7만 안다”는 최초 B milestone에는 맞지만 최종 rollback
artifact에는 맞지 않는다. manifest의 retained B는 C 직전의 안정화
커밋으로 고정해 v7과 v8을 모두 검증한다. 실제 B 바이너리가 v7→v8을
적용한 뒤 C 배포와 B rollback을 수행하는 process oracle로 고정했다.
