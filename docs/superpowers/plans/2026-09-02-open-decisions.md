# PgBase 전면 리팩터링 — 결정 대기 항목

작성일: 2026-09-02 (재개 세션)

원 계획: [`2026-09-01-whole-codebase-refactor.md`](./2026-09-01-whole-codebase-refactor.md)
진행 현황: [`2026-09-02-whole-codebase-refactor-handoff.md`](./2026-09-02-whole-codebase-refactor-handoff.md)

브랜치 `refactor/whole-codebase`. 이 문서는 구현을 계속 진행하기 위해 사람의 판단이 필요한 항목만 모은 것이다. 이미 결정해 반영한 사항은 4절에 근거와 함께 적었으니 뒤집을 근거가 있으면 그때 바꾼다.

블로커는 D1과 D2 둘이다. D1은 R11 착수 시점에, D2는 R5 §9.3 착수 시점에 필요하다. 그 앞 단계(R8, R9a, R5 §9.2, R7 잔여)는 영향받지 않는다.

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

R10b에서 이 환경으로는 검증 불가한 것들이다. 실제 CI에서만 확인된다.

- `gh attestation verify` — 실제 게시된 digest와 attestation, `GH_TOKEN` + registry 인증 경로가 필요하다.
- `trivy-action`의 `trivyignores` 입력이 allowlist YAML을 실제로 소비하는지, SARIF 아티팩트 업로드가 되는지. 스캐너 결과와 allowlist 스키마는 검증했고 action 배선은 못 했다.
- drill과 E2E의 release mode **happy path**(실제 `image@sha256` 대상). **거부 경로만** 증명됐다.
- `--platform linux/amd64`가 `--provenance`/`--sbom` index 출력과 promotion script의 `--prefer-index=false` retag와 상호작용하는 방식. R10a부터 있던 기존 동작이고, script의 digest read-back이 불일치를 잡는 지점이다.
