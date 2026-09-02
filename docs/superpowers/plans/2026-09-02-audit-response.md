# 2026-09-02 감사 대응 기록

[감사 문서](2026-09-02-whole-codebase-audit.md)가 제기한 10건에 대한 대응 기록이다. 감사가 명시한 대로,
정량 target 승인이나 일반 test PASS만으로 항목을 닫지 않는다. 각 항목은 **그 발견을 재현하던 조건에서
실패하는 회귀 테스트**와, 수정을 되돌렸을 때 그 테스트가 실제로 깨진다는 변이 증거를 함께 요구한다.

변이 증거를 요구하는 이유는 통과하는 테스트가 무엇을 증명하는지 알 수 없기 때문이다. 수정을 되돌렸을 때
깨지지 않는 테스트는 그 수정에 대해 아무것도 말해주지 않는다.

## 상태 요약

| 항목 | 등급 | 상태 | 커밋 |
|---|---|---|---|
| STD-1 draft 유실 | P2 | 닫힘 | `4fc6891`…`eed8837` |
| STD-2 DELETE graph 비용 | P2 | 닫힘 | `52eb31d` (지불: `03f07ec`) |
| STD-3 forwarding wrapper | P3 | 닫힘 | `e5a7b5b`…`3594daa` |
| SPEC-1 운영 진입점 부재 | **P1** | 닫힘 | `2e87742`, `672391d` |
| SPEC-2 compiler v2 미배선 | P2 | 닫힘 | `4737dda`…`1fabe31` |
| SPEC-3 macOS terminal CI | P2 | 닫힘 | `00423d1` |
| RUN-1 장시간 DDL이 앱을 종료 | **P1** | 닫힘 | `514df60` (+ 잔여 `560a5cc`) |
| RUN-2 witness 중복 우회 | **P1** | 닫힘 | `7c51094` |
| RUN-3 opclass 오판정 | P2 | 닫힘 | `be3ccd8`, `8269ed3` |
| RUN-4 SIGTERM 순서 | P2 | 닫힘 | `294db6c` |

## 닫힌 항목

### RUN-4 · SIGTERM이 drain보다 먼저 background를 취소

**이 항목은 이전에 "고쳤다"고 잘못 보고된 적이 있다.** `closeResources`의 순서만 바꾸고
`Start(context.Background())` + 직접 `Close` 경로로만 테스트했기 때문에, background context가 여전히
`Run`의 부모 context의 직계 자식이라는 사실이 드러나지 않았다. 실제 SIGTERM에서는 그대로 깨졌다.

수정은 `pgbase/app.go:405`에서 `a.activate(context.WithoutCancel(ctx), ...)`. 부모 신호는 이제 runner에
전혀 닿지 않고 `<-ctx.Done()`만 선택해 `Close`를 부르며, `Close`가 readiness-false → HTTP drain →
`backgroundCancel()` → wait → pool close 순서를 이미 보장한다. `Start`의 계약은 그대로다.

변이 검증(통합 담당자가 에이전트 보고와 독립적으로 재실행):

```
activate(ctx, ...)  →  background runner was cancelled while HTTP request was still in flight
```

`cmd/pgbase/main.go:13`의 `signal.NotifyContext`가 `internal/cli/run.go:165`의 `app.Run(ctx)`로 들어가므로
이 경로가 실제 SIGTERM 경로다.

### RUN-1 · 정상적인 장시간 DDL이 heartbeat를 죽이고 앱을 내림

heartbeat가 `schema_state`를 `FOR SHARE`로 핀했는데, schema apply는 같은 행을 `FOR UPDATE`로 잡고
`statement_timeout = 0`으로 DDL 전체를 수행한다. lease는 어떤 collection DDL도 건드리지 않는 meta 상태이므로
핀할 이유가 없었고, 그 핀이 모든 갱신을 큰 ALTER 뒤에 줄 세웠다.

lease TTL 60s 대 `SchemaApplyTimeout` 기본 10분이므로 transient 재시도만으로는 닫히지 않는다.

변이 검증: 핀을 복원하면 **5.22s에 SQLSTATE 57014**로 실패한다 — 감사가 관측한 ~5.18s와 같은 지점이다.

fence는 손대지 않았다. `Writer.Begin`/`LockCreate`가 스스로 핀하고, 쓰기 트랜잭션 안에서
`lease_expires_at > clock_timestamp()`와 `minimum_writer_version`을 다시 읽는다. 공유 락 제거는 대기 그래프에서
간선을 없애는 것이라 순환을 만들 수 없다.

**잔여분도 닫았다.** tolerance는 벽시계 약속인데 창은 시도가 반환된 뒤에만 재검사됐고, 갱신은
`Options.StatementTimeout`만큼 블록된다. `Normalized`가 2147483647ms까지 허용하므로 이를 올린 운영자는
lease가 이미 만료된 뒤에 루프가 멈출 수 있었다. 시도 context를 tolerance에서 파생시켜 구조적으로 막았다.
쓰기 안전성 구멍은 아니었고 — fence가 거부한다 — 자기 마감을 넘겨 남아 있는 프로세스였다.

변이 검증: statement timeout 30s 대 tolerance 1s로 두 숫자를 분리했다. 수정 시 1.3s에 정지, 되돌리면
블록된 시도가 루프를 statement timeout에 묶어 8s에 실패한다.

### SPEC-1 · populated DB를 운영으로 올릴 진입점이 없음

`Cutover/Backfill/PromoteReady/EnableV5/DisableV5/RetireV4`의 production caller가 0개였다. 신규 설치는
bootstrap이 건너뛰지만, 레코드가 이미 있는 DB는 readiness 200을 받으면서 모든 list가
503 `record_unavailable`로 떨어진다.

`pgbase membership {status,cutover,backfill,promote-ready,enable-v5,disable-v5,retire-v4}`를 추가했다.
D2-2대로 HTTP route도, 환경 변수도, `pgbase.Options` 노출도 없다.

**어떤 전제조건도 약화하지 않았다.** 명령은 인자를 넘기고 수령증을 출력할 뿐이고, 모든 guard는 전이 안에 남아
있다. witness는 ID가 아니라 `ID:VERSION`인데, `verifyFleetProof`가 instance와 release를 원소 단위로 대조하기
때문이다 — 전이가 곧 다시 읽을 행에서 CLI가 조회한 버전을 넘기면 아무것도 증명하지 못한다.

readiness는 **200을 유지**한다. 롤아웃 중인 인스턴스는 실제로 서비스한다: 쓰기, 단건 읽기, admin, schema
경로가 모두 동작하고 list만 막힌다. 함대 전체가 같은 상태이므로 not-ready는 우회로를 만들지 못하고 배포
전체를 내린다. 더 나쁜 것은 교착이다 — `cutover`/`enable-v5`/`retire-v4`가 살아있는 writer lease를 요구하므로,
ready가 된 적 없는 인스턴스를 받지 않는 orchestrator는 그걸 고칠 배포 자체를 멈춘다.

대신 관측 가능성으로 해결한다: `pgbase membership status --json`, 그리고 **ready 함대가 list를 못 하면
미완 롤아웃**이라는 문서화된 규칙. `doctor`는 이걸 잡지 못한다는 점을 문서에 명시했다 — doctor는 membership
*drift*와 무효 설치를 보고하지만, 롤아웃이 아예 돌지 않은 DB는 doctor 기준으로 깨끗하다.

블랙박스 테스트는 감사의 재현을 그대로 따른다: v1~v5 prefix → collection + **300** 레코드(배치 2회 초과) →
`migrate up` → 실제 앱 기동 → readiness 200 **및** 인증된 list 503 → 7단계 전부 `Execute`로 구동 →
인증된 HTTP list 300/300.

변이 6건이 실패하고 1건이 통과했다. 통과한 것은 `RetireV4`에서 `ErrCursorDrainPending`을 제거한 경우인데,
`migrations/system/000007_record_membership.sql:95`의 `v4_retired_at >= v4_decode_until` CHECK가 DB에서
거부하기 때문이다. **이중으로 강제되고 있으며, 테스트는 유지된다는 사실은 잡지만 어느 층이 잡았는지는
구분하지 못한다.** 이 한계는 숨기지 않고 기록한다.

### SPEC-3 · macOS terminal CI 완료 조건 없음

darwin 전용 PTY cancellation/echo-restoration 테스트는 존재하지만 Ubuntu 러너에서는 컴파일조차 되지 않았다.
로컬에서 확인했다: 이 맥에서 `TestReadTerminalPasswordContextCancellationRestoresPTY`는 통과하고,
`GOOS=linux go vet ./internal/cli/`에서는 빠진다.

`macos-15` job을 추가하되 **패키지 전체가 아니라 테스트 이름을 명시**했고, `internal/cli`의 모든
`//go:build darwin` 테스트 함수명이 workflow에 나타나는지 검사하는 정책 테스트를 걸었다.

변이 2건: macOS job 제거 → "no macOS runner, so the darwin terminal tests never run in CI";
`-run` 필터의 이름만 변경 → "does not run the darwin-only test …". 두 번째가 build tag 표류로 커버리지가
조용히 사라지는 경우를 막는다.

### STD-2 · DELETE마다 delete graph를 다시 바인딩

`BindDeleteGraph`가 collection마다 카탈로그 전체를 도는데 이를 DELETE마다 수행했다. 감사 측정으로
500 collection에서 호출당 158.6ms / 53,909,426 bytes / 755,078 allocs.

컴파일된 스냅샷이 graph를 들고 있게 했다. `compileCatalogWithLimit`만 슬롯을 설치하므로 `EnsureCurrent`가
핀한 값은 그 버전의 모든 요청이 공유하고, `Load`가 준 호출자 소유 사본은 스스로 바인딩한다 — 변형될 수 있는
사본이 오래된 graph를 내놓을 수 없다. `sync.Once`로 새 버전에 동시 도착한 DELETE들이 여러 번 경쟁하지 않고
한 번의 빌드를 기다린다.

`schema`가 `relation`을 import할 수 없으므로(순환) 값은 `any`로 경계를 넘는다.
`internal/recordops/delete.go`는 **변경이 필요 없었다**.

측정(호출당, 컴파일된 카탈로그):

| collections | allocs 이전 | allocs 이후 |
| ---: | ---: | ---: |
| 10 | 407 | 0 |
| 100 | 31,008 | 0 |
| 500 | 755,064 | 0 |

allocation이 안정적 신호다(다른 에이전트들이 같은 머신에서 스위트를 돌리고 있어 시간은 잡음이 크다).
`uncompiled` 벤치마크 변형을 저장소에 남겨 "이전" 숫자가 사료가 아니라 재측정 가능하게 했다.

**남은 것을 명시한다.** 버전당 1회 컴파일은 여전히 O(C²)다. 요청당이 아니라 버전당으로 옮긴 것이 STD-2의 완료
조건이고, 평탄화는 별도 변경이다. `catalogCollectionsByID`는 delete마다 O(C) 맵을 다시 만든다.
읽기 경로의 `relation.Bind`는 여전히 요청당이다 — 다른 호출 지점이며 STD-2가 아니다.

### SPEC-2 · v2 plan compiler와 신규 collection의 transactional companion 생성

실제 checksum compiler는 `schema-plan-v1` 상수였고 production v2 issuance도 dual validation 경로도 없었다.
create DDL은 core/relation index만 만들고 companion은 만들지 않았다. 그리고 **모든** schema mutation이
capability를 pending으로 내렸으므로, ready 이후 collection을 하나 만들면 수동 activation/build가 다시 필요했다.

**issuance는 capability row에서만 온다**(D2-2). `schemaPlanCompilerV1/V2`는 unexported 상수 쌍이고
`compilePlan`이 label을 한 번만 찍으므로 한 형식으로 만들고 다른 형식으로 이름 붙이는 것이 불가능하다.
`ready`만 v2를 발급한다 — migration 8의 주석이 이미 약속하던 것이다. exported `CompilePlan`은 v1에 고정돼 있어
wire golden이 하나도 움직이지 않았다.

**demote를 좁힌 것이 핵심이다.** 판정은 `companions && kind != changeUpdate`다. v2 create는 자기 companion을
같은 트랜잭션에서 만들고, delete는 자기 테이블을 그 위의 모든 index와 함께 떨어뜨린다 — 둘 다 빈틈을 남기지
않는다. **update만** gate를 닫고 승인된 CIC maintenance 경로가 다시 연다.

capability를 이어가려면 `catalog_version`뿐 아니라 `ready_index_count`도 옮겨야 한다. doctor가 그 수를
catalog와 대조하기 때문이다. 그리고 `updated_at`은 `transaction_timestamp()`가 아니라 `clock_timestamp()`다 —
이 트랜잭션이 schema advisory lock을 기다리는 동안 커밋된 promotion은 이 트랜잭션 시작보다 나중의 `ready_at`을
쓰고, v8의 timeline CHECK가 그것을 거부한다. **담당자가 "테스트가 순차 실행이라 어떤 테스트도 이걸 잡지
못했을 것"이라고 스스로 기록했다.**

**dual validation.** `compileMixedFleetPlan`이 두 세대를 모두 컴파일하고 Apply가 어느 쪽 checksum도 받는다.
바이너리 하나만 있어도 가설이 아니다 — preview와 apply 사이에 capability가 움직일 수 있다. 다만 바이너리
사이에서는 **단방향**이다: 새 바이너리는 둘 다 받지만 수정 전 바이너리는 v1만 받는다. 이것은 본질적이고,
lease 증명이 경계를 만든다 — v1 writer가 만료되지 않은 lease를 들고 있는 동안 activation이 거부된다.
잔여: 90s TTL을 넘겨 유휴 상태인 v1 writer는 v2 checksum을 `ErrConflict`로 거부하고(클라이언트가 재-preview,
손상 없음) 다음 쓰기에서 demote한다.

**commitment point는 collection 단위**이며 apply 트랜잭션 커밋 시점이다 — v2로 만들어진 collection은
companion을 영구히 갖고, v1으로 만들어진 것은 CIC가 필요하다. *capability* 자체에는 돌아올 수 없는 지점이
없다: rollback은 언제든 issuance를 v1으로 되돌리고, 모든 index는 유지되며, 재-activation은 fleet을 다시 증명한다.

변이 6건 중 통합 담당자가 직접 재실행한 두 건(양방향):

| 변이 | 관측 |
|---|---|
| 무조건 demote(수정 전) | create·delete 테스트가 `State:"pending"`, api 테스트가 `creating a collection dropped the capability` |
| 지나치게 좁힘(update도 통과) | update 테스트가 `capability after adding an indexed nullable field = ... State:"ready"` |

담당자가 정직하게 남긴 두 가지: **T2의 의미 있는 변이는 revert가 아니라 over-narrowing(M3)이다** — revert는
update 경로를 그대로 두므로 T2가 설계상 통과한다. 그리고 T4의 v1-lease activation 거부는 **기존 동작**이라
자기 변이 중 어느 것에도 실패하지 않으며, commitment point가 거기 기대기 때문에 단언할 뿐 자기 수정의
증거가 아니다.

**D2-4를 넘어선 부분.** D2-4는 create와 update만 명명한다. delete가 capability를 유지한다는 것은 감사 문구를
따른 구현 판단이며, 결정 문서에 쓰여 있지 않다. `open-decisions.md`에 "확인 필요"로 분리해 두었다 —
되돌리려면 `&& kind != changeDelete` 한 줄이고 다른 것은 바뀌지 않는다.

### STD-3 · SchemaKernel의 compatibility forwarding surface

`PlanCreate/PlanUpdate/PlanDelete/ApplyCreate/ApplyUpdate/ApplyDelete` 6개가 `Preview`/`Apply`로 전달하는
wrapper로 남아 있었고, `internal/api`가 의존하는 interface도 기존·신규 표면을 나란히 들고 있었다.

HTTP adapter를 `Preview` / `Apply(LegacyApply(...))`에 직접 연결하고, interface와 `*Service` 양쪽에서 6개를
삭제했다. 호출 지점 약 250곳은 대부분 테스트였고, `internal/schema` 안에서는 실제 호출을 그대로 보여주는
unexported helper로, 바깥 패키지에서는 인라인으로 바꿨다. **테스트가 이제 production이 쓰는 표면을 읽는다.**

**wire golden은 손대지 않았다.** `internal/api/testdata/schema_operations_wire.golden.json`은 byte 동일하고,
`git diff -- internal/api/testdata/`가 비어 있음을 통합 시 확인했다. golden test 파일이 편집되긴 했지만
fixture field 이름(`applied:`/`guardedApplyResult:` → `applyResult:`)뿐이고 기대값 줄은 0개다.

**동작 차이는 없었다.** 6개 모두 문자 그대로의 forward였다. 유일하게 버리던 것 — `ApplyResult.Receipt`와
`.Replayed` — 은 handler도 이미 버리고 있었다. 다만 legacy claim 충돌 시 `Replayed: true`에 zero `Collection`이
돌아가는 기존 경계가 있으며(`operation_apply.go:replayedApplyResult`), 이번에 도입된 것이 아니라 원래
있던 것임을 기록해 둔다.

리팩터링이라 증거는 새 테스트가 아니라 **변하지 않은 기존 스위트**다. 테스트를 1개 삭제했는데
(`TestPreviewMatchesCompatibilityEntryPoints`), wrapper가 사라지자 비교의 양쪽이 같은 `Preview` 호출이 되어
무조건 통과하는 동어반복이 됐기 때문이다. 같은 파일 바로 아래의
`TestApplyIntentUsesOneKernelForCreateUpdateAndDelete`가 실제 DB에 대해 create/update/delete를
`Preview` + `Apply`로 구동하며 더 많은 것을 확인한다. **테스트 삭제는 발견 자체가 허가한 일이 아니므로
근거와 함께 남긴다.**

과거 문서 두 곳이 이 6개를 현재 설계로 서술하고 있었다.
`docs/superpowers/specs/2026-08-31-relation-schema-lifecycle-design.md` §6에는 갱신 주석을 달았다.
`docs/superpowers/plans/2026-08-28-production-kernel.md:1093` 은 날짜가 박힌 계획 기록이고 거기 적힌 signature는
당시에도 배포본과 달랐으므로(baseVersion 없음) 기록으로 남겨 두고 고치지 않았다.

### STD-1 · 저장 중 편집한 draft가 유실됨

성공 핸들러가 현재 draft의 개정을 확인하지 않고 `savedDraft()` + `navigate()`를 호출해, 전송한 draft를
저장된 것으로 놓고 화면의 새 draft를 버렸다.

제출한 draft가 저장 기준선이 되고, 응답이 도착하면 화면의 draft를 그 기준선과 비교한다. 같으면 이전과 같이
해제하고 이동한다. 다르면 아무것도 해제하지 않고 이동하지 않으며, 새 draft는 새 기준선에 대해 dirty이므로
이탈 가드가 유지된다.

레코드 저장은 수락된 쓰기가 만든 버전을 `loaded`로 유지해, 이어지는 저장이 오래된 경로로 412를 내지 않는다.

**에이전트가 자기 수정의 부작용을 스스로 찾아냈다.** 페이지에 머무르면 `state.definition`이 한 카탈로그
버전만큼 낡는다. `removedFields`가 영구 데이터 손실 체크박스를 좌우하므로, 방금 만들어진 컬럼을 draft가
지우면 경고 없이 사라질 수 있었다.

백엔드 변경은 필요 없었다. `applyGuarded`(`schema_handlers.go:290`)는 create와 update 모두에
`collectionDTO(result.Collection)`을 쓰고 있었고, 어댑터(`http-backend.ts:114`)가 `data.status`만 읽으며
그 본문을 버리고 있었다. `ApplyOutcome`의 applied 갈래가 `definition`을 실어 나르고,
`Array.isArray(body.fields)`로 collection 본문과 수령증·삭제 응답을 구분한다.

replay는 명시적으로 (1)을 선택했다: 수령증만 오면 아무것도 해제하지 않고 이동해 shell이 draft를 묻는다.
replay는 전송이 끊긴 첫 시도와 편집 없는 재전송을 모두 요구하는데, 편집은 Apply 버튼이 요구하는 preview를
무효화하므로 replay 경로는 이 수정이 다루는 in-flight 편집 경로와 서로소다.

**남은 것:** 관리자가 그 확인을 거부하면 `definition`이 낡은 페이지에 남는다. draft는 살아남지만 그 페이지의
파괴적 경고는 새로고침 전까지 이전만큼 신뢰할 수 없다. (1)의 비용이다.

변이 검증(통합 담당자가 독립 재실행). `definition:` 병합 한 줄 삭제:

```
warns about a field the draft removes from the collection the applied change had just added
  AssertionError: expected [] to deeply equal [ 'extra' ]      ← 조용한 컬럼 삭제
stops warning about a field the applied change had already dropped
  AssertionError: expected [ 'extra' ] to deeply equal []      ← 거짓 경보
```

## 통합 담당자가 직접 재실행한 변이 검증

에이전트 보고를 그대로 옮기지 않고, 아래 네 건은 통합 후 브랜치에서 직접 변이를 넣어 다시 확인했다.
P1 세 건과, 이전에 잘못 보고했던 항목을 골랐다.

| 항목 | 넣은 변이 | 관측 |
|---|---|---|
| RUN-1 (P1) | `refreshWriter`에 `pinSchemaShared` 복원 | `5.23s`에 `pin schema for record membership: ... (SQLSTATE 57014)` — 감사의 ~5.18s와 같은 지점. 앱 레벨에서는 `readiness during the schema DDL = 503, components:["record membership writer"]` |
| RUN-2 (P1) | lease를 map으로 색인하고 **proof를 순회**하도록(감사가 서술한 그 모양) | `repeated_new_witness` 하위 테스트가 `activation error = <nil>, want ErrCompanionIndexFleet` — 우회가 그대로 재현 |
| RUN-4 (P2) | `activate(context.WithoutCancel(ctx), ...)` → `activate(ctx, ...)` | `background runner was cancelled while HTTP request was still in flight` |
| STD-1 (P2) | `definition:` 병합 한 줄 삭제 | `expected [] to deeply equal [ 'extra' ]`(조용한 컬럼 삭제)와 `expected [ 'extra' ] to deeply equal []`(거짓 경보) |

RUN-2에서 처음 넣은 변이는 **실패하지 않았다.** lease를 순회하되 witness를 map으로 찾는 모양이었는데, 그것은
길이 검사와 합쳐지면 여전히 중복을 잡는다. 감사가 지적한 것은 방향이 반대인 경우 — proof를 순회하는 것 —
이며, 그 모양으로 다시 넣었을 때 비로소 우회가 재현됐다. **변이가 통과했다는 것은 수정이 불필요하다는 뜻이
아니라 변이를 잘못 골랐다는 뜻일 수 있다**는 사례로 남긴다.

SPEC-1, RUN-3, STD-2, STD-3, SPEC-3의 변이 증거는 각 담당 보고에 있으며 여기에는 요약만 옮겼다.

## 정량 예산

D1의 회계를 유지한다: package별 zero-slack ratchet과 gross-new/gross-deleted를 계속 기록한다.

- data group **16,653 → 16,645**. STD-2가 지불했다. 내림은 항상 허용된다 — zero-slack이 작업이 간 방향으로
  따라간다는 뜻이다. D1이 고정한 16,750 천장은 건드리지 않았다.
- handwritten runtime **34,812 → 35,150**, UI runtime **3,473 → 3,517**. 올린 세 번 모두 코드와 같은 커밋에서,
  파일별 gross-new/gross-deleted를 테스트 주석에 적었다.

RUN-1/RUN-4는 스스로 지불했고(−2), SPEC-1(+297)과 STD-1(+44)은 지불하지 못했다. **그 사실을 위장하지 않고
기록한다.** `.golangci.yml`의 `unused`가 두 패키지에서 0건을 보고하므로 지울 죽은 코드가 없고, 계획 §11은
정확성·마이그레이션 스택이 LOC-positive인 것을 허용한다. §5가 요구하는 근거 주석을 지워 숫자를 맞추는 것은
§12가 금지하는 방향이므로 하지 않았다.

## 이 세션이 수행할 수 없는 것

감사의 "미검증 경계"가 요구한 마무리 중 두 가지는 외부 변경이라 수행하지 않는다.

- **실제 candidate digest에 대한 외부 release gate.** attestation/upload와 registry candidate promotion은
  외부 mutation이다. 이번 Docker E2E/restore는 local source mode이며, same-digest release 검증을 독립
  재수행했다고 주장하지 않는다.
- **macOS CI의 실제 실행.** job과 그것을 고정하는 정책 테스트는 들어갔지만, 실행은 GitHub Actions에서
  일어나며 push를 요구한다.

두 가지 모두 브랜치가 병합된 뒤 운영자가 실행해야 한다.

## 통합 tip에서 재실행한 gate

감사가 요구한 순서 — 발견별 회귀 테스트가 먼저 green, 그다음 기존 matrix 재실행 — 를 따랐다. 아래는 8건이
들어간 `ed165cf`에서 다시 돌린 결과다.

| gate | 결과 |
|---|---|
| `gofmt -l` / `go vet ./...` / `go vet -tags=integration ./...` | clean |
| `./scripts/lint-go.sh` | 0 issues |
| `go test ./... -count=1` | 전체 통과 |
| integration PG16 (57626) / PG17 (57674) / PG18 (57627) | 각각 통과 |
| `-race` integration PG16 | 통과 |
| UI vitest | 24 files / **354** tests 통과 |
| `npm run typecheck`, `npm run build`, dist drift | 통과, dist 동기 |
| Docker E2E `-count=2 -shuffle=on` | 14개 시나리오 통과 (52.98s) |
| 복원 훈련 `-tags=restore` | `TestRestoreDrill` 포함 3건 통과 (12.17s) |
| Playwright (onboarding + authenticated) | **8/8 통과** |

10건이 모두 들어간 최종 tip에서 위 전부를 한 번 더 실행했고 결과는 동일하다 — lint 0 issues, UI 354,
E2E 2회 셔플 통과(57.4s), 복원 훈련 통과(14.3s), PG16/17/18 통합과 PG16 race 모두 clean, `dist` 동기.

Playwright는 CI와 같은 구성으로 돌렸다 — 이 worktree에서 빌드한 이미지, 격리된 Compose project 2개
(18090/15432, 18091/15433). onboarding spec은 관리자가 없는 인스턴스를 요구하므로 authenticated와 backend를
공유할 수 없고, 그래서 두 project가 필요하다. 두 스택 모두 볼륨과 함께 정리했다.

세 PostgreSQL 컨테이너(57626/57674/57627)는 전 과정에서 건드리지 않았고, 실행 전후로 확인했다. E2E와 복원
훈련은 자기 포트를 동적으로 잡고 자기 볼륨과 함께 정리한다.

감사가 지적한 대로 이 Playwright 결과는 **1회 실행**이며 flake가 영구 제거됐다는 보장이 아니다. Docker E2E는
CI 조건대로 `-count=2 -shuffle=on`으로 2회 셔플 실행했다.

## 감사 범위 밖에서 새로 확인한 것

### `App.Start`의 context 계약이 exported godoc에 없다

RUN-4를 고치면서 `Run`은 background를 `context.WithoutCancel`로 떼어냈지만, `Start`는 caller의 context를
그대로 runner에 넘긴다. 이것은 실수가 아니라 결정이며 `app.go`의 주석에 "Start keeps its caller-owned
contract"로 적혀 있다.

문제는 **그 계약이 exported godoc에는 없다는 것**이다. `Start`의 godoc은 "caller-owned HTTP server를 위해
background를 시작한다. caller의 server가 멈추면 Close를 호출하라"까지만 말한다. context가 background를
직접 취소한다는 말이 없다.

그래서 라이브러리 임베더가 가장 자연스러운 것 — `signal.NotifyContext`를 `Start`에 넘기고, SIGTERM에서
자기 server를 `Shutdown`한 다음 `Close`를 부르는 것 — 을 하면 **RUN-4와 같은 결과**가 된다: 자기 drain이
끝나기 전에 listener와 membership heartbeat가 먼저 죽는다. `Run` 경로는 고쳐졌지만 `Start` 경로는 임베더가
계약을 알 수 없어 같은 실수를 반복하게 된다.

이것은 P1이 아니다. 임베더가 계약을 알면 피할 수 있고, 우리 바이너리는 `Run`만 쓴다. 다만 **감사가 지적한
RUN-4의 대칭 사례**이며, 신규 설치만 고치고 populated DB를 놓쳤던 SPEC-1과 같은 종류의 누락이다.
exported godoc에 계약을 명시하는 것으로 닫는다 — 동작 변경이 아니다.

동작 자체를 `Run`과 맞출지(즉 `Start`도 `WithoutCancel`로 떼고 background 수명을 `Close`에만 묶을지)는
공개 API 의미 변경이라 이 세션에서 임의로 하지 않는다. 판단이 필요하면 별도 결정 항목이다.

### 권고 (구현하지 않음): doctor가 "돌지 않은 rollout"을 탐지하게 할지

SPEC-1은 **실행 수단**을 만들었고 완료 조건도 그것이다. 남은 것은 **알아채는 수단**이다. 지금은
`pgbase membership status`를 *부러* 확인해야 하고, 문서화된 규칙("ready 함대가 list를 못 하면 미완 롤아웃")을
기억해야 한다. doctor는 원래 그걸 기억하지 않아도 되라고 있는 도구다.

`internal/doctor/membership.go:55`가 정확히 그 지점이다:

```go
switch state {
case "off", "cutover":
    return nil
```

`off` + 레코드 존재는 모호하지 않다. `bootstrapFreshInstall`은 기존 레코드가 있으면 건너뛰므로, 그 조합은
"업그레이드된 DB에서 rollout이 시작되지도 않았다"는 뜻이고 모든 list가 503이다. 반대로 `cutover`/`dual_write`는
진행 중이지 결함이 아니므로 여기서 경고하면 안 된다 — 이 구분이 이 check의 전제다.

**구현하지 않은 이유** 두 가지다.

1. SPEC-1 담당이 readiness 대신 관측 가능성을 택한 것은 근거 있는 판단이었고, 감사의 완료 조건도 충족한다.
   그 판단을 통합 담당자가 조용히 뒤집는 것은 옳지 않다.
2. 비용이 공짜가 아니다. doctor의 statement 수는 `TestSnapshotQueryCountIsIndependentOfCatalogSize`가 31로
   고정하고 있고, "레코드가 하나라도 있는가"는 statement가 하나 더 필요하다. catalog 크기와 무관하게 유지하려면
   sidecar scan처럼 collection을 한 문장으로 접어야 한다. 실링을 또 올리는 변경이므로 승인이 필요하다.

판단이 필요한 항목으로 남긴다.

# 재감사 (`d3fb5eb → ed52e71`) 대응

첫 대응 뒤 독립 재감사가 7건을 제기했고 **완료·release 승인 불가**로 판정했다. 기존 10건 기준으로는
완전 해결 6건(STD-2, STD-3, RUN-1~4), 부분 해결 3건(STD-1, SPEC-1, SPEC-2), 실행 증거 없음 1건(SPEC-3)이었다.

## 상태

| 항목 | 등급 | 상태 | 커밋 |
|---|---|---|---|
| 복구 경로에서 draft guard 해제 | P2 | 닫힘 | `b83fb3c`…`73725b5` |
| 우발 파일 `&1` | P3 | 닫힘 | `6f7d853` |
| 구/신 compiler를 fleet proof가 구별 못 함 | **P1** | 닫힘 | `477e487` (migration v9) |
| 승인 checksum과 다른 DDL 실행 | **P1** | 닫힘 | `477e487` |
| 모든 update가 capability를 pending으로 | P2 | 닫힘 | `477e487` |
| SPEC-1 rollout 검증이 완료 조건 미충족 | **P1** | 부분 — 아래 | `d88163e`…`d949d3b` |
| SPEC-3 미검증 | P2 | **도달 불가** | — |

## 내가 만든 것: `&1`

통합 명령에 `2>\&1`로 앰퍼샌드를 이스케이프해서 셸이 `&1`이라는 파일로 리다이렉트했고, `git add -A`가
`ed52e71`에 실어 넣었다. 지적받기 전까지 알아채지 못했다.

## P1 — 승인한 checksum과 다른 DDL을 실행

`operation_apply.go`가 두 세대의 checksum 중 어느 쪽이든 받아들인 뒤, **capability가 고른 plan을 실행**했다.
호출자가 v1 statement 목록에 동의했는데 v2 DDL이 도는 것이다. 게다가 migration row에는 호출자의 checksum과
실행된 plan JSON이 **함께** 기록되므로 둘이 영구히 어긋난 채 남는다.

`compileMixedFleetPlan`의 "either admits nothing new"라는 주석은 *definition*에 대해서만 참이고
*statement*에 대해서는 거짓이었다 — 그게 v2 세대의 존재 이유인데도.

수정: 호출자가 승인한 checksum이 가리키는 plan을 실행한다. `plan` 자체를 재할당하므로 DDL, 기록되는 plan JSON,
companion 생성 여부가 모두 자동으로 따라간다.

변이 검증(통합 담당자 재실행): 재할당을 제거하면 `TestApplyAcceptsAPlanChecksumFromEitherCompilerGeneration` 실패.

## P1 — compiler generation이 승격된 적이 없음

`d3fb5eb`(v1만 발급)과 HEAD(v2 발급)가 **둘 다 `schemaCompilerVersion = 2`**를 선언한다. 따라서
`lease.CompilerVersion < schemaCompilerVersion` 검사가 두 세대를 구별하지 못하고, 진짜 혼합 fleet이 activation
증명을 통과한다. 기존 테스트가 통과한 것은 lease 행에 `1`을 손으로 써넣었기 때문이며 **그 값은 어떤 배포본도
만든 적이 없다.** SPEC-2가 기대던 mixed-fleet 안전성 논거 전체가 무효였다.

첫 대응에서 내가 "lease 증명이 경계를 만든다"고 적었는데, 그 경계는 존재하지 않았다.

수정: 세대를 `3`으로 올리고, migration v9가 capability에 증명한 세대를 기록한다. **v9는 기존 `ready`와
`building`을 모두 pending으로 내린다** — 이전 배포본이 자신을 구별할 수 없었으니 그 증명은 아무것도 뒷받침하지
않는다. v8의 `rollout_state_check`는 건드리지 않고 별도 제약으로 추가해, v8이 배포한 것을 v8이 계속 검사하게 했다.

**운영 비용:** 이미 `ready`에 도달한 fleet은 업그레이드 후 `schema-index activate`를 다시 돌려야 한다.
그리고 모든 capability 읽기가 새 컬럼을 참조하므로 **이 바이너리는 v8 데이터베이스를 서비스할 수 없다.**

변이 검증(재실행): 세대를 `2`로 되돌리면
`activation over a generation 2 writer = <nil>, want ErrCompanionIndexFleet`.

## P1 — SPEC-1 rollout fence: 부분 해결

기존 `TestMembershipUpgradesAPopulatedDatabaseThroughTheDocumentedRollout`은 전이 일곱 단계를 **WriterC 하나**로
수행했다. rollout의 안전 논거 전체가 release별 fence인데, 단일 인스턴스에서는 아무도 낡지 않으므로 fence가
전혀 검사되지 않는다.

이것을 변이로 증명했다. `refreshWriter`의 `writerVersion < minimum` 검사를 삭제하면:

```
기존 테스트   PASS  0.53s     ← fence를 검사하지 않고 있었다
새 테스트 2건 FAIL  61s, 91s
  registering Release A under the Release B floor = <nil>, want ... no longer accepted
  the passed Release A0 writer kept renewing after the cutover raised the floor
```

새 테스트는 A0/A/B/C 런타임을 실제로 등록하고, TTL 만료를 단축 없이 기다리며, `Execute`를 통해 구동한다.

**닫지 못한 부분:** 독립 release artifact는 만들 수 없다. A0/A/B는 태그된 커밋이 아니라 한 코드베이스의
`WriterVersion` 상수이므로 체크아웃해 빌드할 과거 바이너리가 존재하지 않는다. 단일 프로세스가 각 버전의
런타임을 들고 있는 것이 이 환경의 한계다.

## 부수적으로 드러난 것

- **`heartbeatFailsClosed(ErrWriterTooOld)`의 루프 내 분기는 production 경로로 도달 불가능하다.** 모든 전이가
  살아있는 lease에 fence를 걸고, tolerance(50s) < TTL(60s)이므로 통과당한 채 살아있는 writer가 존재할 수 없다.
  순수한 심층 방어다. 단, tolerance 논거는 host와 DB 시계가 ~10s 안에서 일치한다고 가정한다.
- **`RetireV4`의 버전 floor 검사는 dead code다.** floor B에서만 실행될 수 있고 그때 모든 lease는 이미 B 이상이다.
- **`RegisterWriter`의 주석이 낡았다.** "Release A0 is allowed to start on the exact v6 history"는 거짓이다 —
  배포 호출자 둘 다 WriterC이고, 선언 이력이 v9인 지금 v6 DB는 그 전에 거부된다. 주석을 사실에 맞게 고쳤다.
- **읽기 경로는 cursor issue version이 아니라 `v4_retired_at`에 fence를 건다.** rollback이
  `docs/upgrades.md`가 경고하는 truncated-page 창을 열지 않는다는 뜻이다. 담당자가 처음엔 200을 기대했다가
  실패하자 **코드와 문서에 맞춰 테스트를 고쳤다** — 반대가 아니라.

## 미해결로 남기는 것: 부하 의존 PG17 실패

`go test -tags=integration ./...`가 PG17에서 실패하는 것을 3회 연속 관측했다. `internal/schema`의 서로 다른
테스트들이 매번 5.0~5.3초에 실패했다 — pool의 statement timeout 값이다.

**나는 이것을 fence 테스트가 일으킨 회귀라고 단정했고, 그것은 틀렸다.** 근거로 삼은 비교는
"fence 이전 커밋 2/2 통과 / HEAD 3/3 실패"였는데, 앞의 것은 기계가 조용할 때, 뒤의 것은 다른 에이전트 작업이
정리되던 때 잰 것이다. 교란변수를 통제하지 않았다.

조용한 기계에서 HEAD를 다시 재면 **4/4 통과한다.** 계측 결과 최대 연결 46/100, 락 대기는 29개 샘플 전부 0이다.
연결 고갈도 락 경합도 아니고, 부하가 높을 때 정상 쿼리가 5초를 넘기는 것이다. `-p 1`(패키지 직렬)로는 통과한다.

fence 테스트가 60~90초짜리 동시 작업 창을 늘려 확률을 높이는 것은 사실이지만 결정적 원인은 아니다.
**원인을 짚지 못했으므로 고쳤다고 하지 않는다.** 재현 조건은 위와 같다.

## 이 환경에서 도달 불가능한 것

- **SPEC-3의 실제 CI 실행.** macOS job과 그것을 고정하는 정책 테스트는 브랜치에 있지만 실행은 push를 요구한다.
- **SPEC-1의 독립 release artifact.** 위와 같은 이유로 존재하지 않는다.
- **실제 candidate digest에 대한 외부 release gate.** attestation/upload와 registry promotion은 외부 변경이다.

이 셋이 남아 있는 한 완료·배포 승인 판정은 내리지 않는다.

## 재감사 수정 후 마감 매트릭스

| gate | 결과 |
|---|---|
| `gofmt -l` / `go vet ./...` / `go vet -tags=integration ./...` | clean |
| `./scripts/lint-go.sh` | 0 issues |
| `go test ./... -count=1` | 전체 통과 |
| `-race` (touched 7개 패키지) | 통과 |
| integration PG16 / PG17 / PG18 | 각각 통과 |
| `-race` integration PG16 | 통과 |
| UI vitest | 24 files / **359** tests |
| typecheck / build / dist drift | 통과, dist 동기 |
| Docker E2E `-count=2 -shuffle=on` | 통과 (48.3s) |
| 복원 훈련 | 통과 (12.7s) |
| Playwright | **8/8** |

세 PostgreSQL 컨테이너는 전 과정에서 건드리지 않았고 실행 전후로 확인했다. 위 PG17 통과는 조용한 기계에서
잰 것이며, 바로 위에 기록한 부하 의존 실패를 무효화하지 않는다.

## 정량 예산 (재감사 수정 반영)

```
data group            16,747 / 16,750   ← 여유 3줄
handwritten runtime   35,271            UI runtime 3,527
exported 462   interfaces 17   constructors 25   files over 800 LOC 0
```

**data group이 D1 천장 3줄 아래다.** D1은 이 숫자를 고정했고 절대 올리지 않으므로, 이 다섯 패키지의 다음
변경은 같은 스택의 삭제로 스스로 값을 치러야 한다. 반올림 오차가 아니라 실제 제약이다.

## 판단 대기 (변동 없음)

- `App.Start`도 background를 `Close`에만 묶을지 — 공개 API 의미 변경이라 godoc 명시까지만 했다.
- doctor가 "돌지 않은 rollout"(`off` + 레코드 존재)을 탐지할지 — doctor statement 실링을 올려야 한다.
