# Whole-codebase refactor 감사 결과

감사일: 2026-09-02 (Asia/Seoul)

역사적 실행 로그는 당시 `/tmp` 작업 디렉터리에 기록됐으며 현재 checkout에는 보존되지 않는다. 아래 표의 해당 결과는 기록된 historical claim이고 fresh checkout에서 재현 가능한 증거로 취급하지 않는다. 소스 인용은 이 checkout의 상대 경로 또는 고정 커밋 기준으로 유지한다.

## 결론

**기존 자동 검증은 통과하지만, 전체 구현 완료 또는 운영 배포 승인으로 판정할 수 없다.**

Standards 3건, Spec 3건, 추가 실행·정합성 검사 4건을 확인했다. 총 10건이며 P1 3건, P2 6건, P3 1건이다. 이는 발견한 사항의 수이지 저장소에 남은 모든 결함의 수를 보장하지 않는다.

특히 기존 데이터가 있는 DB의 업그레이드 경로, 정상 장시간 DDL과 membership heartbeat의 충돌, schema-writer fleet 증명의 중복 ID 허용은 배포 전에 해결해야 한다. 테스트 통과는 이 조건들이 안전하다는 증거가 아니었다. 별도 회귀 테스트에서 실제 실패를 확인했다.

## 범위와 방법

- 사용자가 승인한 기준: `0fdc66c2fe930951b1934ad4f7ab20288109bf22` → `d3fb5eb9e5480ea1fdba30fcd81ff85608e2755b`.
- 비교: `git diff 0fdc66c2fe930951b1934ad4f7ab20288109bf22...HEAD`; 446 files, +65,933 / -17,331.
- 원 계획 전체, open decisions, 인수인계 기록, upgrade 문서와 실제 구현을 대조했다.
- code-review 스킬에 따라 Standards와 Spec을 독립 병렬 검토했다. 별도로 DB 정합성, 인증·HTTP·lifecycle을 교차 검토하고 실제 테스트를 실행했다.
- 승인된 D1/D6 정량 재예측, advisory-key 유지 등의 명시적 예외는 위반으로 세지 않았다. 실제 요구 누락, 사용자 데이터 유실 조건, 실행 실패를 구분했다.
- 제품 코드·커밋·브랜치는 변경하지 않았다. 저장소에는 이 감사 문서만 추가했다. 재현 테스트는 임시 파일/Go overlay를 사용했다. 임시 DB와 테스트가 소유한 Compose 리소스는 정리했다.

## Standards

### STD-1 · P2 · 저장 중 추가 편집한 draft가 조용히 유실됨

위치: [collection-designer.ts:493](../../../ui/admin/src/designer/collection-designer.ts:493), [record-workspace.ts:249](../../../ui/admin/src/workspace/record-workspace.ts:249).

Collection apply가 진행되는 동안 필드 입력은 활성화되어 있다. A를 제출한 뒤 사용자가 B로 추가 편집해도, A의 성공 응답이 현재 draft의 revision을 확인하지 않고 `savedDraft()`와 `navigate()`를 실행한다. 저장되지 않은 B까지 저장된 것으로 처리되어 이탈 확인 없이 사라진다. Record editor도 동일한 제출/응답 처리 구조이며 일반 입력이 pending 중 활성화되어 있다.

재현: preview A → 응답을 보류한 apply → 필드명을 `new_unsaved_title`로 편집 → A 성공 응답. 최신 draft가 남아 있어야 하지만 records 화면으로 navigation이 발생했다. Collection 흐름은 scratch Vitest로 재현했고 record 흐름은 코드로 확인했다.

기준: [F0 완료 조건, 계획:890](../../../docs/superpowers/plans/2026-09-01-whole-codebase-refactor.md:890)의 저장 완료·명시적 discard 외 dirty draft 유실 금지.

수정 완료 조건: 제출 revision과 현재 revision을 비교해 이후 편집을 보존하거나 pending 중 편집을 차단한다. Collection/record 양쪽에 deferred-response 회귀 테스트를 둔다.

### STD-2 · P2 · DELETE마다 catalog 전체의 delete graph를 O(C²)로 다시 생성

위치: [recordops/delete.go:68](../../../internal/recordops/delete.go:68) → [relation/delete.go:125](../../../internal/relation/delete.go:125) → [binding.go:44](../../../internal/relation/binding.go:44).

각 DELETE attempt에서 `BindDeleteGraph(catalog)`를 호출한다. 이 함수는 collection C개 각각에 대해 `Bind`를 호출하고, 각 `Bind`는 전체 C개 collection을 다시 순회하고 map을 할당한다. [compiledCatalog:23](../../../internal/schema/catalog.go:23)에는 재사용할 relation binding/delete graph가 없다.

순수 benchmark 결과(Apple M5, DB 호출 없음, 각 3회):

| 관계 없는 빈 collection 수 | 시간 / 호출 | 할당 bytes / 호출 | allocations / 호출 |
| --- | ---: | ---: | ---: |
| 10 | 41µs | 22,712 | 407 |
| 100 | 6.26ms | 1,493,872 | 31,009 |
| 500 | 158.6ms | 53,909,426 | 755,078 |

이는 전체 DELETE 지연이나 production SLO 측정값이 아니라 graph 생성만의 비용이다. SQL query-count gate는 이 CPU/메모리 비용을 탐지하지 못한다.

기준: [계획:307](../../../docs/superpowers/plans/2026-09-01-whole-codebase-refactor.md:307)의 catalog version마다 relation binding/delete graph를 정확히 한 번 compile.

수정 완료 조건: 검증된 version별 immutable graph를 재사용하고 collection 수에 따른 CPU/할당량 회귀 검사를 추가한다. 기존 코드보다 느려졌다는 주장이 아니라, 약속한 구조적 개선이 빠졌다는 지적이다.

### STD-3 · P3 · SchemaKernel의 내부 compatibility forwarding surface가 남음

위치: [service.go:98](../../../internal/schema/service.go:98).

`PlanCreate/PlanUpdate/PlanDelete/ApplyCreate/ApplyUpdate/ApplyDelete` 6개가 새 `Preview/Apply`에 전달하는 wrapper로 남았고 API 의존 interface에도 기존/신규 surface가 병렬로 유지된다. Legacy HTTP wire 보존은 필요하지만, 그것이 내부 중복 surface의 존속을 요구하지는 않는다.

기준: 계획 §§6.1·11·12의 단일 의도 surface 및 같은 stack의 wrapper 제거 요구. Fowler 관점의 **possible Middle Man**은 판단성 보조 근거이며, 그 자체를 기계적 위반으로 취급하지 않았다.

수정 완료 조건: HTTP adapter가 `Preview`/`Apply(LegacyApply(...))`로 직접 연결되도록 하되 기존 wire golden을 유지한다. 구조 항목으로서 즉시 장애와는 구분한다.

## Spec

### SPEC-1 · P1 · 기존 populated DB의 membership rollout을 실행할 운영 진입점이 없음

위치: [CLI 명령 목록:47](../../../internal/cli/run.go:47), [bootstrap.go:195](../../../internal/membership/bootstrap.go:195), [traversal.go:146](../../../internal/membership/traversal.go:146).

현재 CLI는 `serve/migrate/admin/doctor/schema-index/healthcheck`뿐이다. `Cutover/Backfill/PromoteReady/EnableV5/DisableV5/RetireV4`의 production caller는 0개다. Fresh bootstrap은 기존 레코드가 있으면 건너뛰지만, 최종 Release C List는 ready·v5 issue·v4 retirement를 모두 요구한다. 문서에 전이 순서가 있어도 제공 바이너리로 실행할 방법이 없다.

재현: 기준 커밋과 동일한 v1~v5 migration prefix → 기존 collection/레코드 1개 SQL seed → HEAD v6/v7/v8 migration → HEAD `New/Start` 성공 → readiness **200** → 인증된 기존 레코드 List **503 `record_unavailable`**. Capability는 `off`, cursor issue version은 4에 남았다.

이 재현은 구버전 바이너리를 직접 실행한 것이 아니라 바이트 동일한 migration prefix를 구성한 것이다. 또한 문서가 금지한 C 직행을 권장하는 것이 아니다. 안전한 A0/M/A/B/C 경로를 실제로 실행할 지원 surface가 없다는 점이 문제다. 외부 registry에 과거 A/B artifact가 있는지는 조사하지 않았다.

기준: [R4 rollout, 계획:607](../../../docs/superpowers/plans/2026-09-01-whole-codebase-refactor.md:607)의 drain/cutover/resumable backfill/ready/enable/drain/retire 및 독립 deployable artifact 요구. [open-decisions:153](../../../docs/superpowers/plans/2026-09-02-open-decisions.md:153)도 CLI 미배선을 기록하고 있어 새 발견으로 가장하지 않았다. 완료 판정에서 남아 있는 blocker다.

수정 완료 조건: operator-only 전이 명령과 정확한 artifact/runbook을 제공하고, populated 이전 버전 DB를 지원 순서로 업그레이드하여 List 및 rollback/holdpoint를 black-box 검증한다. Capability를 SQL로 강제 ready 처리하는 임시 우회는 해결로 보지 않는다.

### SPEC-2 · P2 · v2 plan compiler와 신규 collection의 transactional companion 생성이 미배선

위치: [planner.go:560](../../../internal/schema/planner.go:560), [create DDL:523](../../../internal/schema/planner.go:523), [operation_apply.go:255](../../../internal/schema/operation_apply.go:255).

실제 checksum compiler는 `schema-plan-v1` 상수이며 production v2 issuance/dual validation 경로가 없다. Create DDL은 core/relation index만 만들고 companion은 만들지 않는다. 모든 schema mutation은 capability를 pending으로 내리므로 ready 이후 새 collection을 만들어도 수동 activation/build 경로가 다시 필요하다.

기준: [계획:646](../../../docs/superpowers/plans/2026-09-01-whole-codebase-refactor.md:646)의 mixed-fleet dual validation, [승인 D2-4:40](../../../docs/superpowers/plans/2026-09-02-open-decisions.md:40)의 “새 collection은 v2에서 companion index를 생성 transaction 안에서 만든다.”

수정 완료 조건: capability와 compiler format을 연결하고, v1/v2 검증·downgrade 동작을 고정한다. Ready 상태의 신규 collection 생성은 같은 transaction에서 companion을 갖춰야 한다. 기존 collection의 field/index 추가만 승인된 CIC maintenance 경로로 보낸다.

### SPEC-3 · P2 · macOS terminal CI 완료 조건이 없음

위치: [backend.yml:45](../../../.github/workflows/backend.yml:45).

현재 workflow의 runner는 Ubuntu뿐이다. Darwin 전용 PTY cancellation/echo-restoration 테스트는 존재하지만 Linux 빌드에서 제외되므로 release CI가 이 동작의 회귀를 막지 못한다.

기준: [R8 완료 조건, 계획:770](../../../docs/superpowers/plans/2026-09-01-whole-codebase-refactor.md:770)의 macOS terminal CI 실행.

이번 Mac 로컬 Go suite는 통과했다. 지적은 macOS 구현이 현재 실패한다는 뜻이 아니라, 명시된 지속 검증 gate가 없다는 것이다. 수정 완료 조건은 해당 PTY 테스트를 실행하는 macOS CI job과 성공 실행 증거다.

## 추가 실행·정합성 검사

### RUN-1 · P1 · 정상 장시간 schema DDL이 heartbeat를 실패시켜 앱 종료로 전파됨

위치: [membership/service.go:146](../../../internal/membership/service.go:146), [runtime.go:129](../../../internal/membership/runtime.go:129), [app.go:523](../../../pgbase/app.go:523).

Heartbeat는 `schema_state FOR SHARE`를 얻는데 schema apply의 `FOR UPDATE`와 충돌한다. DDL에는 별도 장시간 budget과 `statement_timeout=0`이 적용되지만 heartbeat는 기본 5초 statement timeout을 사용한다. 이 오류에서 membership runtime은 종료하며 App은 다른 background를 취소하고 실행 오류로 전파한다.

재현: 등록된 runtime이 있고 60초 lease가 유효한 상태에서 schema apply와 같은 exclusive pin을 6초 유지하려 하자 heartbeat worker가 약 **5.18초**, SQLSTATE **57014**로 종료했다. 실제 대형 ALTER 대신 동일 lock 경합을 만드는 집중 테스트다. 전체 App 종료 전파는 호출 경로로 확인했다.

수정 완료 조건: 정상 DDL contention과 lease 만료를 구분한다. Schema pin과 heartbeat 경로를 분리하거나 lease 안전 범위의 bounded transient 처리를 제공하되, 실제 expiry는 fail-closed해야 한다. 실제 장시간 DDL과 live App을 함께 실행하는 회귀 테스트도 추가한다.

### RUN-2 · P1 · 중복 witness ID로 mixed-compiler fleet 차단을 우회

위치: [companion_index.go:436](../../../internal/schema/companion_index.go:436), [CLI witness 파싱:27](../../../internal/cli/schema_index.go:27).

검증은 live map 크기와 proof slice 길이를 비교한 뒤 proof 각 항목만 조회한다. 고유성이나 UUID 집합의 정확한 일치를 검사하지 않는다. Live fleet이 `[newID, oldID]`여도 proof `[newID, newID]`의 길이는 2이고 모든 조회가 new compiler를 가리켜 통과한다. CLI도 중복 witness를 허용한다.

재현: compiler 2/1의 live lease를 만들었을 때 정직한 두 ID proof는 거부됐지만, compiler 2 ID를 두 번 준 proof는 `state=building, generation=1, err=nil`로 activation에 성공했다.

이것은 인증 우회나 외부 공격자 원격 침입 주장이 아니다. Operator-only 전이의 안전 증명이 잘못되어 혼합 버전 rollout을 차단하지 못하는 결함이다.

수정 완료 조건: 중복을 거부하고 proof/live UUID 집합을 정확히 비교하며 모든 live compiler version을 검사한다. `[new,new]`, 누락, 추가, mixed-version 조합의 회귀 테스트를 둔다.

### RUN-3 · P2 · 잘못된 index opclass를 정상 companion으로 인정

위치: [companion_index.go:599](../../../internal/schema/companion_index.go:599), [shape comparison:640](../../../internal/schema/companion_index.go:640).

Physical observation은 key 열과 DESC/NULL 옵션 등만 읽고 `indclass`/`indcollation`은 읽지 않는다. 이름과 열 순서가 같은 `text_pattern_ops` index도 일반 text ORDER BY용 companion으로 인정하여 maintenance가 ready로 승격하고 diagnostics가 정상으로 보고한다.

재현: 20,000행과 동일 deterministic name의 `nickname text_pattern_ops DESC NULLS LAST, id DESC NULLS FIRST` index → activation/build 성공 → `ready`, Expected 1 / Valid 1 / Mismatched 0. 실제 EXPLAIN은 **Seq Scan 20,000행 → Sort**였다.

수정 완료 조건: 실제 ORDER BY 의미에 필요한 opclass/collation을 비교하고 mismatch를 표시·복구한다. 단순 valid flag/이름 일치 외에 잘못된 물리 정의를 넣는 drift 테스트를 추가한다.

### RUN-4 · P2 · SIGTERM에서 background가 HTTP drain보다 먼저 취소됨

위치: [app.go:478](../../../pgbase/app.go:478), [closeResources:585](../../../pgbase/app.go:585).

새 `Close` ordering은 HTTP drain 후 background를 취소하지만, background context는 여전히 `Run` 부모 context의 직접 자식이다. SIGTERM/Run cancel에서는 부모 취소가 먼저 전파되므로 drain 중인 요청이 있어도 listener·membership heartbeat·reconciler가 먼저 멈춘다.

재현: 실제 TCP HTTP 요청을 barrier 안에 유지한 채 Run context 취소 → 요청 완료 전에 background stopped channel이 닫힘. `-race` scratch 테스트가 `background runner was cancelled while HTTP request was still in flight`로 실패했다. 기존 ordering 테스트의 `Start(context.Background())` 후 직접 Close 경로와 다르다.

기준: [계획:707](../../../docs/superpowers/plans/2026-09-01-whole-codebase-refactor.md:707)의 Run parent 취소와 task 취소 분리.

수정 완료 조건: Run 종료 신호와 background context를 분리하여 readiness false → HTTP drain → task cancel/wait → pool close를 실제 signal 경로에서도 보장한다. Caller-owned Start 계약은 별도로 유지한다.

## 직접 실행한 검증

Go는 `GOTOOLCHAIN=go1.26.7`로 고정했다. UI는 설치된 Node 24.19.0을 사용했다(CI 선언은 24.18.0). 따라서 UI 검증은 CI와 Node patch까지 동일한 실행은 아니다.

| 검증 | 결과 | 증거 |
| --- | --- | --- |
| Go unit, `-race -count=1`, skip policy | PASS | [unit-race.log](/tmp/pgbase-audit-akjyj9/unit-race.log) |
| PostgreSQL 16.13 integration, race | PASS | [pg16-race.log](/tmp/pgbase-audit-akjyj9/pg16-race.log) |
| PostgreSQL 17.9 integration, race | PASS | [pg17-race.log](/tmp/pgbase-audit-akjyj9/pg17-race.log) |
| PostgreSQL 18.3 integration, race | PASS | [pg18-race.log](/tmp/pgbase-audit-akjyj9/pg18-race.log) |
| go vet unit/integration + pinned lint | PASS, 0 issues | [static.log](/tmp/pgbase-audit-akjyj9/static.log) |
| UI typecheck + Vitest | PASS, 24 files / 344 tests | [vitest.log](/tmp/pgbase-audit-akjyj9/vitest.log) |
| 별도 빈 output으로 Vite build 후 committed dist 비교 | 파일 내용/집합 동일 | [dist.log](/tmp/pgbase-audit-akjyj9/dist.log) |
| Playwright, 별도 onboarding/auth backend, workers 2, retries 0 | 8/8 PASS, 5.4초 | [browser.log](/tmp/pgbase-audit-akjyj9/browser.log) |
| Docker Go E2E, local source, count 1, shuffle | 14 top-level tests PASS, skip 0, 44.859초 | [e2e.log](/tmp/pgbase-audit-docker-e2e.9MxLu7/e2e.log) |
| Local-source restore drill PG16/17/18 | 전부 PASS, 25.53초 | [restore log](/tmp/pgbase-correctness-audit.rApUw4/restore-local-pg16-17-18.log) |
| npm audit | 0 vulnerabilities | [npm-audit.log](/tmp/pgbase-audit-akjyj9/npm-audit.log) |
| govulncheck | 호출 경로에 영향 0; 미사용 `x/crypto/openpgp`의 module 수준 GO-2026-5932 1건은 별도 존재 | [상세 로그](/tmp/pgbase-audit-akjyj9/govulncheck-verbose.log) |
| 전체 diff whitespace | PASS | `git diff <base>...HEAD --check` |

위 PASS와 다음 FAIL은 모순이 아니다. 다음은 이번 감사에서 기존 suite 밖의 조건을 추가한 회귀 테스트다. 정상 동작을 기대하는 assertion이 현재 구현에서 실패한다.

| 추가 재현 | 결과 | 증거 |
| --- | --- | --- |
| 기존 populated v5 → HEAD migrate/start/list | 예상 List 성공 assertion FAIL, 실제 503 | [populated-upgrade.log](/tmp/pgbase-correctness-audit.rApUw4/populated-upgrade.log) |
| DDL pin 경합 중 heartbeat 지속 | FAIL, 57014 | [regression-data.log](/tmp/pgbase-audit-akjyj9/regression-data.log) |
| 중복 fleet witness 거부 | FAIL, activation 성공 | [regression-data.log](/tmp/pgbase-audit-akjyj9/regression-data.log) |
| wrong opclass companion 거부 | FAIL, ready 오판정 | [regression-data.log](/tmp/pgbase-audit-akjyj9/regression-data.log) |
| Run cancel 시 drain 우선 | FAIL | [regression-lifecycle.log](/tmp/pgbase-audit-akjyj9/regression-lifecycle.log) |
| Collection apply 중 새 draft 보존 | FAIL, navigation 발생 | [재현 소스/설명](/tmp/pgbase-standards-audit.aYnhmE/REPRODUCTION.md) |

재현 자산은 `/tmp`에 있으므로 영구 보존이 필요하면 별도 보관해야 한다. 이 감사 문서는 저장소에 남는다. 제품에 회귀 테스트를 편입하거나 수정 커밋을 만드는 작업은 이번 감사에서 하지 않았다.

## 재현 명령

저장소 root에서, 테스트 전용 PostgreSQL 17 컨테이너가 기존 `127.0.0.1:57674` 포트로 실행 중일 때:

```sh
GOTOOLCHAIN=go1.26.7 \
PGBASE_TEST_ADMIN_URL='postgres://postgres:postgres@127.0.0.1:57674/postgres?sslmode=disable' \
go test -overlay=/tmp/pgbase-correctness-audit.rApUw4/overlay.json \
  -tags=integration ./internal/schema ./internal/membership ./pgbase \
  -run '^TestAudit' -count=1 -v

GOTOOLCHAIN=go1.26.7 go test -race \
  -overlay=/tmp/pgbase-auth-http-audit.XWbw1l/overlay.json \
  ./pgbase -run '^TestAuditRunCancellationDrainsBeforeBackgroundCancel$' -count=1 -v
```

위 접속 정보는 감사에 사용한 로컬 disposable test PostgreSQL용이며 운영 DB를 대상으로 실행하면 안 된다. 테스트 helper는 고유 임시 DB를 만들고 제거한다. UI/graph 재현의 설치 위치와 명령은 [별도 설명](/tmp/pgbase-standards-audit.aYnhmE/REPRODUCTION.md)에 있다.

## 미검증 경계와 남은 일

- 실제 GitHub Actions attestation/upload 및 외부 registry candidate promotion을 재실행하지 않았다. 이번 Docker E2E/restore는 **local source mode**다. 이전 완료 기록의 same-digest release 검증을 이번 감사에서 독립 재수행했다고 주장하지 않는다.
- Playwright 8개 통과는 현재 제공된 시나리오 1회 결과다. 장시간 DDL의 commit-ACK 단절과 reload/re-login을 결합한 모든 브라우저 경로, flake가 영구 제거됐다는 보장은 아니다.
- 대규모 부하/장시간 soak, 모든 fuzz target의 장시간 캠페인, exhaustive security scan, 이미지 Trivy/SBOM 전체 재생성은 이번 범위에서 수행하지 않았다. Auth/origin/CSRF/KDF/reset 경로는 소스와 기존 테스트로 검토했으며 별도 확정 finding은 없었다.
- 수정 후에는 발견사항별 회귀 테스트가 먼저 green이어야 한다. 그다음 기존 Go/UI/PG matrix/E2E/restore를 다시 실행하고, 실제 candidate digest의 외부 release gate와 macOS CI를 완료해야 한다.
- 완료 문서에는 남은 operator rollout surface와 compiler v2 조건을 해결 상태에 맞게 반영해야 한다. 정량 target 승인이나 일반 test PASS만으로 이 항목을 닫으면 안 된다.

요약: **Standards 3건(최고 P2: draft 유실·DELETE graph 비용), Spec 3건(최고 P1: populated DB 운영 업그레이드 경로), 추가 실행·정합성 4건(최고 P1: heartbeat 종료·fleet 증명 우회).**
