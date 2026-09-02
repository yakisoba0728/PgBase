# PgBase 전면 리팩터링 중단·인수인계

작성일: 2026-09-02 (최종 갱신: 2026-09-03, 전체 재감사)

중단 사유: 사용자가 현재까지의 결과를 보존하고 전체 구현을 중단한 뒤, 완료 범위·진행 방식·잔여 작업을 문서로 남기도록 요청했다. 이후 같은 날 재개해 doctor 묶음을 감사·수정·커밋했고 Release B 감사를 마쳤다.

원 계획: [`2026-09-01-whole-codebase-refactor.md`](./2026-09-01-whole-codebase-refactor.md)

결정 기록: [`2026-09-02-open-decisions.md`](./2026-09-02-open-decisions.md) — 구현 판단은 모두 닫혔고,
남은 외부 CI/registry evidence는 설계 결정과 분리했다.

## 2026-09-03 최종 로컬 감사 인수인계 — 현재 기준

기존 “15/17 닫힘” 보고, `2c942ea` 기준 인수인계와 감사 대응의 `0fdc66c → 7d9362e` 정정은 아래에 역사
기록으로 보존한다. 현재 build와 manifest 기준은
`72de20e5e92d12b168782af36432c463d4598e78`이다.

최종 보고 뒤 인증 전이(`da6a728`), channel 단조성(`be01eec`), durable schema recovery(`58b45fe`),
retry 없는 browser gate(`3716e6d`, `d2701c5`), guarded operation identity(`4a82832`), API no-store
(`d97bb109`)를 닫았다. 이어 promotion의 대기 실행을 `queue: max`로 보존했다(`91f52e1`). 이것은 플랫폼 상한
100개의 pending run을 보존하지만 외부 registry writer에 대한 CAS는 아니다.

Playwright의 authenticated project는 하나의 global catalog version을 함께 바꾸므로 worker 1개로
직렬화했다. `retries: 0`은 유지했고 TypeScript source에 들어 있던 raw NUL separator는 명시적 escape로
정상화했다(`13421ce`). 마지막으로 같은 관리자·같은 route의 여러 탭이 하나의 pending slot을 덮어쓰거나
다른 탭의 recovery record를 삭제하던 P2를 operation별 v2 key로 닫았다. reload는 유효 pending을 expiry·ID
순으로 처리하고 failed verdict와 현재 dirty draft를 보존한다. unsafe storage는 오래된 오류보다 복구 지침을
우선 표시한다(`c1b27fa`, `c1d4b6b`, `cc58163`, `86909dd`, `ade7129`).

v2에는 operation ID·종류·expiry만 저장하며 draft와 idempotency key는 저장하지 않는다. **65분** expiry는
server-side schema apply 최대 1시간에 transaction/reconciliation settlement 5분을 더한 것이다. legacy v1과
v2는 exact operation ID만 삭제하고, malformed entry나 storage 실패는 fail-closed다.

### 최종 artifact 입력

| selectable phase | 최종 tip |
|---|---|
| A0 | `9672d695c315dbcf36f993cec78eebe9fa5faac9` |
| A | `451e715938e9d58f50970080d34142776ef256b2` |
| B | `4676b81e3c940d9a65da11837093686379457d0f` |
| C | `07c0eddd5c6dedb99dd2cdfa3e1e85f4583dcbc4` |
| P0 | `f93c2c755848dc4757ad76c469bf8aa24bf9b63d` |
| F | `87a89145958c6716662e03137328fba5e1dc39f3` |

여섯 tip은 main history에 tree-neutrally 보존되고 manifest와 release oracle에 고정됐다. phase별
writer/compiler 상수와 migration bytes는 유지했다. Operator
`f7ec0ff54953417dd0b41331f0b9d02b023195d8`와 Predecessor
`d3fb5eb9e5480ea1fdba30fcd81ff85608e2755b`는 rollout/rollback용 historical audit fixture이며 selectable
phase가 아니다.

최종 bundle은 `/tmp/pgbase-final-storage-safe-artifacts.U8s0mp`, `go1.26.7 darwin/arm64`, mode `0755`다.

| binary | SHA-256 |
|---|---|
| A0 | `ff4418865a2d2aff76b7cff8051d2d650a27223fe455fb5279c943952faedf84` |
| A | `e048929edab0794eb47252e451203949b4dcb0faa6ce1936e826e8e5ca737683` |
| B | `c7ad49086aae87a64cc3bc9b140f17851e23db3b6c75336fa98be81f227c903f` |
| C | `48f2e4636c7ada341ddd46914a72653d3fd8f244d277f3595d286b12ad02fb17` |
| Operator | `427b58adf5c64452a9b261ede5ea22977934709f9a85d36f245decb8e09b2653` |
| Predecessor | `5399706fe99985472cc09842b5d61a37d2f2f91c3e337afdc4227de1f3d5ae5b` |
| P0 | `4ae9998580e4521b1b277d6c27949a8b6fdd2165292e9e4ee1bf591f0dd39cea` |
| F | `24701d9999434bb63980616180efa504fb9d7ec8b8e9aefb206a03064d2e9002` |

### migration v9를 실행하기 전 필수 경고

pre-P0 server, one-shot CLI, 예약 `schema-index` job의 **새 시작을 먼저 동결**한다. v8에서 P0를 full fleet에
배포해 readiness를 확인하고, 이미 시작된 pre-P0 process와 schema transaction을 모두 drain한 뒤에만 지정
P0 job으로 `pgbase migrate up --through 9`를 실행한다. maintenance advisory lock을 아직 잡지 않은 채
precheck 뒤에서 멈춘 구 process는 migration lock으로 차단되지 않으므로 process drain을 생략할 수 없다.

v9 checksum은 `19baaeab06d5b9c23333ae5c14cf0c6c806360e1dabc958c35c24263225d35c6`이다. v9가 기존
`building`/`ready`를 `pending`으로 내리므로 F full-fleet와 generation 3 lease 뒤 activation/build를 다시
수행한다. F는 v8 DB를 서비스하지 않으며 M 뒤에는 pre-P0가 아니라 검증된 P0로만 rollback한다.

### 로컬 검증 결과와 소요 시간

- PG16/17/18 integration·race exact backend matrix: 순차 통과, real `357.19s` / `333.30s` / `332.18s`
- release artifact process oracle `-race`: 통과, test `37.844s`, real `38.87s`
- `go test -race ./... -count=1`: 통과, real `17.06s`; vet/lint/format/repository policy 통과, lint `0 issues`
- UI full/typecheck/build/dist: `381/381`, typecheck·build 통과, dist 동기
- Docker E2E `-count=2 -shuffle=on`: exact `72de20e` 재실행 통과, package `59.231s`, real `60.07s`
- restore drill `-count=1`: exact `72de20e` 재실행 통과, package `16.493s`, real `17.32s`
- final `72de20e` image Playwright, 새 onboarding/auth Compose volume: `retries: 0`, `8/8`, test `5.6s`, real `6.05s`
- `govulncheck`: 호출 가능한 취약점 0; `npm audit`: 취약점 0

검증 시간이 길어진 이유는 버전별 PostgreSQL matrix를 안정된 부하에서 순차 실행하고, phase 변경 뒤 8개
과거 source를 다시 빌드해 process oracle로 검증했으며 Docker E2E 2회 shuffle, restore, mutation 재현,
fresh browser stack을 함께 확인했기 때문이다. 기존 결과를 새 pin에 소급하거나 retry로 실패를 숨기지 않았다.

data group zero-slack ratchet은 `16,747`줄로 D1 ceiling `16,750`까지 수치상 3줄만 남았다. 게이트 자체는
현재값에 고정됐으므로 다음 증가는 같은 stack의 삭제로 지불해야 한다. UI runtime은 `3,522/3,527`줄이다.

### 남은 작업

로컬 구현·감사·release-equivalent process 검증은 위 snapshot에서 닫혔다. 다음은 이 worktree가 만들 수 없는
외부 evidence이며, 완료되기 전에는 배포 승인이 아니다.

- exact commit push 뒤 `macos-15` terminal CI 실행
- GHCR exact candidate push와 provenance/SBOM/custom attestation 게시·`gh attestation verify`
- 실제 `trivy-action` allowlist와 SARIF upload
- exact digest의 모든 release gate와 사람의 명시적 promotion
- moving/exact tag의 only-writer 보장 또는 registry tag 보호

PG17의 고부하 5초 statement-timeout 원인은 특정하지 못했다. 조용한 환경에서 최대 연결 46/100, lock wait
29개 sample 전부 0으로 통과했지만 이를 해결로 보지 않는다. 상세 근거는
[`2026-09-02-audit-response.md`](./2026-09-02-audit-response.md)의 마지막 절을 따른다.

## [역사 기록 · superseded] 2026-09-03 최종 인수인계 (`2c942ea` 기준)

기준 main은 `refactor/whole-codebase`의 `2c942ea`이며, 배포 artifact 고정은
`codex/record-membership-carry-forward-integration`에 준비했다. 원 계획 R0~R12와 F0~F3, 최초 감사
10건, 재감사 7건을 다시 대조했다. “15/17 닫힘” 보고 이후에도 네 경계를 더 발견했다: 실제 schema
writer/listener 교착(P1), 구 maintenance의 post-F 승격 경로(P1), v8/v9 catalog-shape fail-open(P1), M 전에
물리 lock을 잡은 구 CIC session의 post-M 잔류(P2). 코드 회귀 테스트와 역변이에 더해, lock 전에
멈춘 pre-P0 process는 full-fleet P0 배포와 launch freeze/process drain 계약으로 닫는다. 상세 원인과 증거는
[`2026-09-02-audit-response.md`](./2026-09-02-audit-response.md)의 마지막 절이 현재 권위 있는 기록이다.

현재 artifact 고정점은 다음과 같다.

- schema P0 `cdcd2f1409575f6d0bd4c57eba193a6e1b532e45`
- schema F `3b1042daf41c8aeccc45f6edc92c9198cd740006`
- migration 9 `19baaeab06d5b9c23333ae5c14cf0c6c806360e1dabc958c35c24263225d35c6`
- membership A0/A/B/C `181a115`/`03c6ebc`/`bafc95a`/`36fea6e`

Git history의 실제 A0/A/B/C/predecessor/P0/F source를 각각 빌드하는 builder, process rollout/rollback oracle,
digest-pinned phase image workflow를 추가했다. 최종 8-binary bundle로 전체 `releaseartifacts` race process가
30.186초에 통과했다. 구현 방식은 characterization/RED → 최소 수정 → 실제 PostgreSQL lock/catalog 관측 → 역변이 →
PG16/17/18 및 release-equivalent matrix → Standards/Spec 독립 감사 순서다.

남은 작업은 이 로컬 트리에서 수행할 수 없는 외부 evidence뿐이다.

- branch push 후 macOS terminal CI 실제 실행
- GHCR phase candidate push와 provenance/SBOM/custom source attestation 게시·조회
- 배포 대상 final candidate의 release gate 및 사람의 명시적 promotion

이 외부 기록 전에는 배포 승인을 내리지 않는다. branch push, publish, deploy, registry/tag 변경은 하지 않았다.
아래의 “남은 작업 TODO”, 과거 WIP와 진행률 표는 중단 당시의 역사 기록이며 현재 상태가 아니다.

## 0. 진행 현황 (재개 세션)

브랜치 `refactor/whole-codebase`. 이 세션에서 30건 넘게 커밋했고 작업 트리는 깨끗하다.

| 단계 | 상태 |
|---|---|
| R4 Release B | 완료·커밋 (감사 blocker 2건 수정 포함) |
| R4 App WriterB | 완료·커밋 — Release B가 배포 가능한 산출물이 됨 |
| doctor membership | 완료·커밋 (감사 blocker D1 포함 3건 + 후속 6건) |
| R6 RecordKernel/RelationKernel | 완료·통합 — cascade N+1 제거 (6N+6 → 상수 12) |
| R7 | 완료 — 잔여였던 AtomicJSON 런타임 강제(`api/route_registry.go`, `applyResponseMode`)와 ledger HTTP recovery 계약(`api/schema_operations.go`)이 모두 착지했다 |
| R10b | 완료·통합 — 완료 기준 12개 전부 충족 |
| R8 / R9a / R5(§9.2) | 완료·통합 |
| R4 Release C / R5(§9.3) / F0~F3 | 완료·통합 |
| R11 | 완료 — compatibility path 삭제와 정량 게이트 확정 (§0.0) |
| R12 | 완료 — 로컬에서 실행 가능한 gate 전부 재실행, 나머지는 CI 전용으로 명시 (§0.1) |

## 0.0 R11 — compatibility path 삭제와 정량 마감 (완료)

R0~R10, F0~F3, membership Release A/B/C가 모두 착지한 뒤의 최종 consolidation이다. 시작 커밋은 `fb9bb3a`.

### 삭제한 것과 근거

| 삭제 | 근거 |
|---|---|
| `Planner.PlanCreate/PlanUpdate/PlanDelete`와 `*WithCatalog` 6개 | production caller 0. `Plan(catalog, Change)`만 `Service.Preview`, `preflightLegacyApply`, durable apply에서 쓰인다. |
| `catalogAware bool` 파라미터와 3개 분기 | 남은 caller가 전부 `true`. `PlanX(v, in)` ≡ `PlanXWithCatalog(v, Catalog{}, in)`이 이미 동치라 catalog-blind 경로는 scalar collection만 계획할 수 있었다. |
| `collectionHasIncomingRelation`의 fallback scan | `validateCatalogIntegrity`가 incoming index와 collections의 불일치를 compile 단계에서 거부한다. planner에 도달하는 모든 catalog는 compile을 통과한 것이므로 index가 유일한 근거다. PG16 integration으로 실증. |
| `query.NewService` | 트리 전체에 caller 0 (테스트 포함). |
| `schema.NewService` → `NewServiceWithRecorder` forwarder | recorder를 nil로 기본값 처리. CLI schema-index만 recorder 없는 쪽을 쓰고 있었다. |
| `schema.NewListener` → `NewListenerWithRecorder` forwarder | production caller 0. |
| `schema.preflightCatalogWithLimit` 중간 hop | hook을 nil로 기본값 처리하는 3단 chain의 가운데. |
| `recordops.NewService` / `NewServiceWithRecorder` | runtime을 nil로 기본값 처리. application은 `NewServiceWithMembershipRuntime`만 쓴다. 세 개가 하나가 됐다. |
| `schema.Planner` 타입 자체 | 필드 없는 빈 struct에 method 1개. 모든 생성 지점이 `Planner{}` 리터럴을 썼고 `NewService`가 바꿀 수 없는 파라미터로 받았다. 계획 §12의 "production adapter 하나만 있는 가상 seam". `CompilePlan` package function이 됐다. |
| `schema.OperationRecoveryError.Ref` | production caller 0. in-package 테스트 2곳이 감싼 필드를 직접 읽는다. |
| `recordops.IsCode` → test helper 이동 | production caller 0. API 계층은 자체 switch로 recordops 오류를 매핑한다. 삭제가 아니라 category 정정(runtime −4 / test +4). |
| 주석 81줄 | 선언 이름을 그대로 반복하는 문장. §5 유지 대상은 건드리지 않았다. |

### 유지하기로 한 것과 근거

- **`membershipWriterProvider`의 lazy registration.** production은 항상 runtime을 주입하지만, 이것은 교체된 구현의 옛 절반이 아니라 지원되는 생성 모드다. `TestDirectConstructorRegistersTheSameWriterReleaseAsTheApplication`이 직접 생성한 service도 application과 같은 durable lease generation을 주장한다는 것을 고정한다. 지우면 중복이 아니라 동작이 사라진다.
- **`membership`의 rollout transition 6개.** production caller 0이고 `DisableV5`는 테스트 caller도 0이지만 `docs/upgrades.md`가 기술한 운영 rollout 절차 자체다. `DisableV5`는 `EnableV5`의 롤백 경로이므로, 진행 중인 rollout의 롤백을 지우는 것은 운영 능력의 제거다.
- **`schema.LegacyApply`와 `operationModeLegacy`.** HTTP `POST /api/admin/collections/apply`가 도달하며 `PgBase-Compatibility: missing-idempotency-key`를 내보내는 공개 wire 계약이다. R11 완료 기준이 "승인된 공개 wire compatibility path는 유지"라고 명시한다.
- **`recordartifact.ETag`의 legacy 분기와 `IndexSpec.CompatibleNames`.** 각각 이전 빌드가 쓴 데이터와 이전 이름의 index를 drift로 오판하지 않게 하는 호환 경로다.
- **`planUpdate`의 `input.Name != current.Name` guard.** compiled catalog는 key == Name을 강제하므로 production에서는 상시 false지만, apply가 신뢰하는 전제에 대한 fail-closed 방어다. mis-keyed catalog를 만드는 테스트로 커버리지를 유지했다.
- **중복 helper 다수** (`validation(field, code)` 3곳, `*OperationOutcome` 5곳, recorder nil-guard 4곳, `jsonString` 3곳 등). 계획 §3이 "production에 기본 `dupl` 임계값을 넘는 동일 코드 조각은 0건이다. 파일을 합치거나 generic helper를 늘릴 근거가 없다"고 명시하고, §5가 6줄 중복을 위해 새 package를 만들지 말라고 한다. 목록은 이 문서 아래 "R11이 확인한 잔여 중복"에 남긴다.

### 파일 분할

800줄 초과 3개를 locality 기준으로 나눴다. 선언 이동만 있고 함수 사이로 옮긴 로직은 없다.

- `schema/planner.go` 900 → 597 + `relation_ddl.go` 311
- `schema/physical_manifest.go` 831 → 443 + `physical_manifest_builder.go` 398
- `recordops/service.go` 889 → 654 + `relation_telemetry.go` 163 + `idempotency.go` 81

600~800줄 파일 6개의 module depth 근거는 open-decisions의 "R11 재산정 결과"에 적었다.

### 게이트

`internal/repositorypolicy/loc_budget_test.go`가 data group을 16,653으로, 구조 지표 8개를 실측값으로 고정한다. 구조 지표는 `tools/repostats`를 실행해 읽으므로 정의가 보고서와 어긋날 수 없다. 임계값 9개 전부 1 낮춰 실패를 확인한 뒤 되돌리는 mutation check를 거쳤다. `internal/query`의 per-package ratchet은 4,894 → 4,884로 조였다.

Go runtime 주석 줄만 게이트에서 뺐다. 근거는 open-decisions에 있다.

### R11이 확인한 잔여 중복 (합치지 않기로 한 것)

다음 단계가 다시 발견하고 다시 판단하지 않도록 남긴다.

- `validation(field, code string) error` — `query/types.go`, `relation/types.go`, `schema/names.go`. 세 `ValidationError`가 모두 `internal/validation`의 같은 타입 alias라 기술적으로는 합칠 수 있다. 3줄짜리 body 3개다.
- 오류 → outcome 분류기 5개 — `migrate/runner.go`, `schema/listener.go`, `schema/service.go`, `query/read_telemetry.go`, `recordops/relation_telemetry.go`. 앞 둘은 byte 단위로 같고 나머지 셋은 자기 패키지의 sentinel arm이 추가된다.
- recorder nil-guard `record(ctx, event)` 4개.
- `jsonString`/`parseJSONString` 3개, `quotedTable`/`quotedRecordCollection` 2개, `qualified`/`relationQualified` 2개.
- word-byte 분류기 3개 — `query/filter.go`, `query/read_telemetry.go`, `recordops/relation_telemetry.go`.
- constant-time 비교 2개 — `adminauth/service.go`, `observability/metrics.go`. 한쪽은 expected가 이미 해시돼 있다.
- `api`의 401 preamble 4곳 — `admin_handlers.go` 2곳, `schema_operations.go` 2곳. helper 하나가 정당해 보이는 유일한 항목이다.
- advisory key 파생 2개는 D4-4.1이 이미 통합하지 않기로 결정했다(lock key 바이트가 바뀌면 rolling deploy 중 상호배제를 잃는다).

### 이 세션에서 발견해 고친 실제 결함

계획 문서를 근거로 감사하지 않았으면 나오지 않았을 것들이다.

- **doctor D1** — `pg_get_expr`이 `IntervalStyle`에 따라 다르게 역파싱되어, `ALTER DATABASE ... SET intervalstyle` 한 줄이면 정상 DB가 "설치 불완전"으로 오진되고 이후 membership 검사가 전부 건너뛰어졌다. 트랜잭션 레벨에서 `intervalstyle`/`datestyle`을 고정해 기존 `relations.go`의 `pg_get_expr` 2곳까지 함께 보호했다.
- **Release B B1** — `ready_collection_count == cutover_collection_count`를 traversal 전제조건으로 요구했다. 두 값은 서로 다른 시점의 receipt이고 DB CHECK에도 없으며, backfill 창에서 collection이 하나 생기면 PromoteReady 성공 후 **모든 collection의 모든 list가 영구 503**이 되고 복구 경로가 없었다. write와 `Get`은 계속 성공해 장애가 비대칭이었다.
- **Release B B2** — v5 발급을 켜는 순간 cursor 없는 요청의 응답이 422 `validation_failed`에서 400 `invalid_cursor`로 바뀌었다. 수정 중 오류 클래스만 복원하면 v5가 v4와 다른 detail code를 낸다는 두 번째 층을 발견해, JSONB 거부를 plan 단계로 올려 단일 정책으로 수렴시켰다.
- **App이 Release A로 lease를 잡고 있었음** — 바이너리가 Release B 읽기 경로를 갖고도 WriterA로 등록해, `EnableV5`가 자기 fleet proof를 거부했다. 운영자가 자기 바이너리를 활성화할 수 없는 상태였다.
- **ResponseWriter 래퍼가 unwrap 불가** — `recoveryMiddleware`가 모든 응답을 버퍼링하는데 그 writer가 실제 연결을 참조하지 않아, `http.ResponseController`가 체인 어디에서도 write deadline을 찾지 못했다. 테스트뿐 아니라 프로덕션에서도 그랬다.
- **종료 순서가 계획과 반대** — `Close`가 HTTP drain 이전에 background를 취소해, 처리 중이던 요청이 의존하던 cleaner/listener/lease를 잃을 수 있었다.
- **restore drill flake** — `pg_isready`가 postgres entrypoint의 socket-only 초기화 중 ready를 보고하는데 TCP는 EOF를 반환해, PG17/18에서 약 50% 확률로 실패하고 있었다.

### 확정된 최대 리스크: 그룹 예산 초과

```
database+query+recordops+relation+schema = 15,854
계획 §3:  checkpoint 11,900 / 예상 12,150~12,650 / hard 12,750
  database 396 / query 4,917 / recordops 2,157 / relation 2,008 / schema 6,376
```

약 3,100줄 초과이고 R6는 이를 줄이지 못했다(N+1 제거 배치가 순 +86). `internal/schema` 6,376줄이 단일 최대 기여자인데 전용 단계가 없다. R11은 다른 단계가 놓친 것을 쓸어담는 단계이지 혼자 3,100줄을 찾는 단계가 아니다. 아래 R11 항목에 선택지를 기록했다.

이 절의 숫자는 R11 착지 시점 스냅샷이다. R5 §9.3의 companion index가 `internal/schema`를 6,376 → 7,223으로 올렸고, 확정된 그룹 게이트는 `internal/repositorypolicy/loc_budget_test.go`의 16,653이다.

## 0.1 R12 — 최종 release-equivalent gate (완료)

R12는 게이트를 새로 만드는 단계가 아니라 R10a/R10b가 만든 정의를 최종 candidate에 다시 실행하고 결과를 마감하는 단계다. 이 저장소에는 registry credential도, 게시된 image도, Actions runner도 없으므로 **외부 registry 쓰기·tag 이동·publish는 하지 않았다.** 대신 127.0.0.1의 일회용 `registry:2` 컨테이너에 workflow와 같은 명령으로 candidate를 한 번 build/push해 실제 `image@sha256`을 만들고, 그 digest를 gate에 그대로 흘렸다.

로컬에서 증명한 것:

- `--platform linux/amd64 --provenance=mode=max --sbom=true --push`가 만드는 candidate digest는 **index digest**이고 그 index는 platform manifest 하나 + attestation manifest 하나를 담는다. `promote-release-image.sh`의 `imagetools create --prefer-index=false` retag는 그 index digest를 **보존한다**. R10b가 검증 불가로 남긴 항목이다.
- release mode의 `RestoreDrill`(PG 16/17/18)과 Docker Go E2E가 같은 digest로 통과한다. 두 gate 모두 candidate가 `/api/admin/system`에서 보고하는 version이 `expected_version`과 같은지 확인하며, drill은 dump 전과 restore 후 두 번 확인한다. `PGBASE_VERSION`은 Compose의 build arg일 뿐 runtime 환경변수가 아니므로 `--no-build` release mode에서 이 비교는 image에 박힌 값을 읽는다. R10b는 거부 경로만 증명했었다.
- stable/alpha/beta/rc 승격과 exact tag의 absent/same/different 3-way, malformed·unsupported 입력의 registry 접근 0회를 실제 registry API로 전부 실행했다.
- candidate image의 admin-ui 단계가 만든 `ui/admin/dist`가 커밋된 dist와 파일 집합·sha256까지 동일하다.

CI에서만 확인할 수 있는 것은 open-decisions의 같은 목록에 남겼다. 특히 `gh attestation verify`는 로컬 커버리지가 **0**이다 — buildx가 index에 넣는 in-toto attestation manifest는 `actions/attest-*`가 GitHub에 올리는 attestation과 다른 물건이다.

닫히지 않았고 R12가 고치지 않기로 한 것:

- **Playwright browser suite가 `retries: 1`에 기대고 있다.** 같은 코드로 3회 실행해 1회에서 flaky pass가 났다. `schema-operations.spec.ts:38`은 `ol.operation-list > li`의 `.first()`에서 status 문자열을 읽고 reload/재로그인 뒤 그 값이 그대로일 것을 요구하는데, worker가 병렬이라 그 첫 행은 다른 worker가 방금 낸 `running` receipt일 수 있다(`commitOperation`이 DDL과 같은 transaction이므로 자기 apply일 수는 없다). `system.spec.ts:24`는 5분 주기 `schemaHealthSampler`의 첫 sample을 5초 안에 기다린다. 각각 "decided 상태인 행만 고른다", "sample이 실제로 도착할 때까지 기다린다"로 고칠 수 있지만, 최종 gate 단계에서 결정적 재현 없이 gate test를 바꾸는 대신 소견으로 남긴다.
- **`tests/r0-postgres-skip-baseline.tsv`(27 KB)를 참조하는 코드·workflow·문서가 없다.** R0 characterization 산출물이 그대로 남았다.
- **`tests/README.md`의 "R0 point-read compatibility decision"이 낡았다.** 4,682,047바이트 fixture는 저장소에 더 이상 없고, 그 절이 지키라고 한 "`Get`은 4 MiB 초과 레코드를 허용한다"는 계약은 MEDIUM-5 수정(`recordops.maximumRecordBytes = 2 << 20`)이 새 write에 대해 도달 불가로 만들었다.

R12가 찾아 고친 것:

- **Go E2E suite가 Compose를 `up -d`로 띄우고 health를 기다리지 않았다.** `TestGracefulShutdownRejectsNewRequestsAndDrainsInflight`는 `PGBASE_AUTO_MIGRATE` 없이 두 번째 instance를 띄우므로, 이 테스트가 먼저 도는 순서에서는 compose 서비스의 자동 migration보다 앞서 붙어 `system migrations are not current`로 fail-closed하고 90초 뒤 실패한다. `-shuffle=1788335022190278000 -count=2`로 재현했고, CI가 도는 형태가 정확히 그것이다. `e2e.yml`이 `go test` 전에 실행하는 `up -d --wait`는 **다른 project**(`pgbase-ci-…`, 포트 18090)를 띄우고 suite는 `--project-name pgbasee2e<pid><nonce>`로 자기 stack을 새로 만들기 때문에 이 race를 막아주지 않는다. CI가 초록이었던 것은 native amd64에서 migration이 수십 ms에 끝나 대개 먼저 이겼기 때문이고, 이 환경의 emulation이 그 창을 벌렸을 뿐이다. suite 자신이 `--wait`하도록 고쳤다. (커밋 `d94df82`의 메시지 마지막 문장은 이 정정 이전에 쓰여 workflow가 결함을 가렸다고 적고 있다.)
- **release mode에서 gate가 자기 image를 build하지 못하게 하는 실행 가능한 정책이 없었다.** `security.yml`의 `docker build`와 `e2e.yml`의 `docker compose build`는 `release == 'false'` 조건을 달고 있지만 그 조건을 지키는 테스트가 없어, 지워도 트리가 초록이었다. `tests/release.TestImageGatesNeverBuildTheirOwnArtifactInReleaseMode`로 고정했다(조건 제거 변이로 실패 확인).

## 0. 남은 작업 TODO

### 0.0 2026-09-02 독립 감사 대응 (최우선)

독립 감사가 10건(P1 3, P2 6, P3 1)을 제기했고 **"아직 완료·배포 승인 판정은 어렵습니다"**로 결론냈다.
발견 내용은 [감사 문서](2026-09-02-whole-codebase-audit.md), 대응·증거·잔여는
[대응 기록](2026-09-02-audit-response.md)에 있다. 아래 목록은 상태만 든다.

- [x] **RUN-1 (P1)** 정상적인 장시간 DDL이 heartbeat를 죽이고 앱을 내림 — heartbeat의 schema 핀 제거 + lease
  tolerance. 잔여였던 "블록된 시도가 tolerance를 넘김"도 닫았다.
- [x] **RUN-2 (P1)** 중복 witness가 activation fleet 증명을 우회 — 길이 비교를 집합 동등으로.
- [x] **SPEC-1 (P1)** populated DB를 올릴 운영 진입점 부재 — `pgbase membership` 7개 명령 + runbook.
- [x] **STD-1 (P2)** 저장 중 편집한 draft 유실 — 여기에 딸린 stale definition 위험까지 닫음.
- [x] **STD-2 (P2)** DELETE마다 delete graph 재바인딩 — 500 collection에서 755,064 → 0 allocs.
- [x] **SPEC-3 (P2)** macOS terminal CI 부재 — macos-15 job + 이름 표류를 잡는 정책 테스트.
- [x] **RUN-3 (P2)** 잘못된 opclass를 정상으로 판정 — EXPLAIN으로 Seq Scan → Index Only Scan 확인.
- [x] **RUN-4 (P2)** SIGTERM이 drain보다 먼저 background를 취소 — **이전에 "고쳤다"고 잘못 보고했던 항목**.
- [x] **STD-3 (P3)** compatibility forwarding surface 제거 — 순감 65줄, wire golden 무변경.
- [x] **SPEC-2 (P2)** v2 plan compiler와 신규 collection의 transactional companion 생성 미배선 — issuance를
  capability row에 묶고, ready create는 같은 트랜잭션에서 companion을 만들며, update만 gate를 닫는다.
- [ ] **판단 필요:** SPEC-2가 D2-4에 없는 delete 취급(capability 유지)을 더했다. 근거는
  [open-decisions](2026-09-02-open-decisions.md) 말미에 분리해 두었다.

감사 밖에서 추가로 확인한 것:

- [x] `App.Start`의 context 계약이 exported godoc에 없어, 임베더가 `signal.NotifyContext`를 넘기면 RUN-4를
  자기 서버에 대해 재현하게 된다. godoc에 명시했다(동작 무변경).
- [ ] **판단 필요:** `Start`도 background를 `WithoutCancel`로 떼고 수명을 `Close`에만 묶을지. 공개 API 의미
  변경이라 임의로 하지 않았다.
- [ ] **판단 필요:** doctor가 "돌지 않은 rollout"(`off` + 레코드 존재)을 탐지하게 할지. 비용과 근거는 대응
  기록에 적었다.

이 세션이 수행할 수 없는 마감 두 가지 — 실제 candidate digest에 대한 외부 release gate와 macOS CI의 실제
실행 — 는 외부 변경이므로 병합 후 운영자 작업이다.

### 0.1 이전 세션의 TODO

우선순위 순이다. `[ ]`는 미완, `[x]`는 이 세션에서 완료.

#### Release B를 커밋 가능하게 만들기 (당시 차단 상태)

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

R12의 독립 재대조 결과 네 항목 모두 이후 단계에서 닫혔다.

- [x] `if after != nil && len(after.Values) != 0` → `!after.membershipOnly` 플래그로 바뀌었다. 짧은 value list는 이제 `orderSpec.keyset`에 도달해 `invalid_cursor`가 되고, 1페이지 재시작은 v5 first page만의 명시적 경로다 (`query/types.go`, `query/order.go`).
- [x] `newCursorV5Descriptor`가 bind 경로와 같은 `Traversal.ValidateV5`를 호출한다. `ValidateV5`가 unexported `ready`를 먼저 본다 (`query/cursor_v5_binding.go`, `membership/traversal.go`).
- [x] `TestV5KeysetTraversalExcludesRecordsCommittedAfterFirstPage`가 high-water 값을 직접 단언하고 3페이지를 걸으며 continuation binding의 byte 동일성을 확인한다. 중간 cursor가 하나뿐이라 carry 동일성은 1회만 검사된다.
- [x] `expand_live_test.go`가 clock만 stub하고 traversal은 실제 resolver를 쓴다. `cursor.Version4`는 Release C가 삭제해 존재하지 않는다.

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

**이 표는 중단 시점(2026-09-02 이른 세션)의 스냅샷이고 더 이상 현재 상태가 아니다.** 이후 R5~R12와 F0~F3가 전부 착지했다. 현재 판정은 §0의 표와 §0.1을 본다. 아래는 그때의 판단을 남긴 기록이다.

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

**이 절도 중단 시점의 스냅샷이다.** 확정된 값과 게이트는 open-decisions의 "R11 재산정 결과"에 있고, R12가 그 표를 `go run ./tools/repostats`로 다시 대조해 전 항목 일치를 확인했다(handwritten 34,812 / Go 31,339 / UI 3,473 / interface 17 / export 467 / `New*` 25 / 주석 1,739 / page hook 19 / UI export 119 / 800줄 초과 0).

`tools/repostats`는 Git 추적 파일만 계산한다. 당시에는 새 doctor 파일 두 개가 untracked이라 그대로 실행한 수치가 WIP runtime을 과소 계산했다.

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
