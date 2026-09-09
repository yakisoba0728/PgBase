# pgBase 구현 현황 / TODO

## N6–N9 병렬 실행 — N6/N7 인수·N8/N9 구현 병렬 진행 (최종 동기화 2026-09-10 05:31 KST)

실행 기준은 [N6–N9 계획](docs/superpowers/plans/2026-09-10-n6-n9-execution.md)과 [통합 상태](docs/workstreams/n6-n9/integration.md)다. 통합 branch `codex/n6-n9-integration`은 설치 기록 commit `117a1db`에서 시작했고 plan-only `133f644`/`8dadf42`를 통합 commit `7cedc6e`/`ff3dada`로 적용했다. 제품 worker branch의 중복 plan commit은 통합에서 제외한다. Astra는 계획·조율만 맡고 제품 코드·검토·테스트는 Sol/Terra가 수행한다.

| 단계 | 담당·소유 | 현재 상태 | 다음 gate |
| --- | --- | --- | --- |
| G0 기준·계획 | Sol 통합 | 완료 — N1–N5 PR34 CI5/5·merge `2939f15`, PG17.9/schema40 설치와 후속 기록 `117a1db` 확인 | 완료 단계 재실행 없음 |
| G1 N6/N7 계약 | N6 Chandrasekhar/Sol, N7 Einstein/Terra, 공통 seam Sol 통합 | 완료 — 독립 worktree/base/파일 소유와 API·검증 계약 고정 | 안정 제품 commit과 focused evidence |
| G2 N6 구현 | Chandrasekhar/Sol — `internal/jsvm`, typed Go facade, JS options/CLI/hooks/API commons | migration `21b8293`, bounded single file stream/lock fix `bb569af` 통합; focused race/PG green, Nash core Standards/Spec 0 | 외부 JS 앱/browser·cross-flow |
| G2 N7 구현 | Einstein/Terra — `sdk/dart`, 전용 scripts/docs/examples | core acceptance 완료 — data/admin/browser/revoke와 실제 open-SSE logout·cursor 410·cancel upload cleanup green | N6 외부 앱 cross-flow에 동일 SDK 재사용 |
| G3 N6/N7 통합 | Sol 통합 | N7 core, N6 review/migration, CPU/queue/cancel/Close와 within-cap file lock 모두 green; 외부 앱·cross-flow 미실행 | 외부 Go+JS 앱 PG/file/SMTP/job retry와 Dart/Flutter 동일 상태 검증 |
| G4 N8/N9 병렬 착수 | N8 Euclid/Sol `codex/n8-sql-console`, N9 Sagan/Terra `codex/n9-self-update` | clean `91a0bda` 기반 구현; N9 private core `5fea1d4` integrated/race green, N8 private slices active | N8 stable handoff; N9 faults/6 builds; 공통 seam 합성·독립 review |
| G5 최종 인수 | Sol 통합 | 대기 | 전체 unit/race·PG integration·CLI/browser·SDK/native·lint/vet·OpenAPI/generated repro·구조 budget·6 builds |

소유 경계: N6와 N7 worker는 상대 branch와 중앙 문서를 수정하지 않는다. `TODO.md`, `docs/next-work.md`, `docs/project-status.md`, `docs/pocketbase-current-reassessment.md`, 이 통합 상태 문서는 Sol 통합 단독 소유다. 시스템 migration 1–40은 불변이다. **Migration 41은 05:16 KST에 N8 durable redacted execution log와 transactional write receipt 전용으로 예약했다.** N6/N7/N9는 시스템 migration을 사용하지 않는다.

Nash의 N6 core review와 migration snapshot/history-sync/cancel-lock은 완료됐다. G3 green은 외부 Go+JS 앱의 PG/file/SMTP/job retry, 브라우저+Dart/Flutter 공통 record/ETag/exact-number/SSE, focused CPU/queue/cancel/Close 자원 검사가 실제로 통과한 시점이다. N7 revocation/cursor/upload edge도 완료됐다. Astra의 05:14 gate 조정은 독립 N8/N9 제품 착수만 허용하며 G3 완료나 최종 통합을 뜻하지 않는다. 전체 회귀와 6 builds는 G5까지 반복하지 않는다.

격리 환경: N6은 `pgbase-n6-pg-20260910`/PG17.9 `55469`, 외부 앱 `58094`, SMTP `58095`; N7은 `pgbase-n7`/PG `55441`, API `58091`, Flutter `58092`, admin proxy `58093`; N8은 `pgbase-n8-20260910`/PG17 `55470`, PG18 `55471`, API `58096`, UI `58097`; N9는 task-owned temp install과 필요 시 TLS/health `58098`을 소유한다. live PostgreSQL `55440`, MinIO `59000`, HTTP `8090`, 기존 Docker/userdata는 모든 N6–N9 검사 대상에서 제외한다.

- [x] `117a1db`에서 통합 branch 생성 및 두 docs-only plan 적용.
- [x] 중앙 live board·소유·단계 gate와 수분 단위 통합 기록 생성.
- [x] N7 404 red loop 재현 후 generic router 404와 file env 부재로 원인 확정; SDK/서버 동작 변경 없이 fixture narrow fix를 N7에 반환.
- [ ] N6 기반·도메인·첫 migration·durable jobs·CLI/types/watch 안정 commit과 focused/PG gate 완료; hook/migration 확대·외부 JS 앱/browser·stress·review closure 수령 및 통합.
- [x] N7 fixture/live·Flutter browser/SSE restart·failure/admin matrix·독립 review·open-SSE logout·cursor-expired·canceled upload cleanup core acceptance 완료.
- [x] Astra gate 조정에 따라 N6/N7 교차 인수 완료 전 독립 N8 Sol·N9 Terra 제품 stage 착수; G3 완료·통합은 계속 미완료로 분리.
- [ ] N8/N9 구현·독립 review·전체 로컬 최종 인수·문서 동기화·clean final commit.

## N1–N5 원격 병합·기존 설치 반영 — 완료 (2026-09-10 03:28 KST)

새 사용자 승인에 따라 push→PR→필수 CI→정상 merge→기존 PG17/schema38 설치 백업·binary/schema40 적용·실제 인수를 완료했다. 과거 제외 기록은 당시 범위다. Sol이 설치, Terra가 절차·복구를 독립 검토했으며 공용 문서는 통합 담당 단일 소유다.

- [x] origin/default branch·기존 PR·보호 정책 확인: active ruleset `22511139`, strict required CI 5개, bypass actor 없음.
- [x] [PR #34](https://github.com/yakisoba0728/PgBase/pull/34) push·CI5/5 SUCCESS·02:44 KST 정상 squash merge. 제품 SHA `2939f1543e4889c0ffa967eae398c91d0ecbb6ce`; 제출 `432bfaa`와 merge tree 동일. 보호 우회·force push 없음.
- [x] 별도 fresh DB/files/archives/config/binary 백업·hash inventory, 정확한 fresh dump의 merged-SHA38→40 canary, 독립 physical restore 완료.
- [x] 03:24 KST 최종 installer exit0: 설치 제품 `2939f15`, PostgreSQL17.9/schema40, HTTP8090 실행 중. binary SHA-256 `77788e37c9b3c82fd934a0186992321aa9d0f0bd4a82d258eace20030cdce492`.
- [x] binary provenance/hash·doctor/readiness·기존 관리자 login/logout·schema export·N4 preview·managed backup 생성/다운로드/hash/삭제·N1 진단·데이터/정책/config/files/archives 보존 통과.
- [x] TODO/project-status/next-work/reassessment·설치 기록 마감. `codex/n1-n5-installed-status`의 로컬 문서 후속 커밋이며 병합·설치 제품 SHA와 구분한다.

CI: Go unit5m21s, PG17.9 integration/race·SDK live16m54s, Frontend test1m17s, audit1m3s, Observability25s. 최종 tree의 전체 Go/PG17 gates가 green이며 설치 뒤 전체 검사·제품 수정·설치 round를 추가하지 않았다.

복구점: `/Users/yakisoba0728/Documents/GitHub/PgBase/.local/backups/pre-n1-n5-2939f1543e48-20260910-030338`. 물리 fallback 및 exact hashes/CI/private evidence는 [원격·설치 기록](docs/workstreams/n1-n5/release-install.md)에 있다. 실제 앱0 tables/0 rows·활성 관리자1명·파일 객체0개·기존 archives4개를 보존했다.

실패 이력 정정:03:10 disabled auto-status 성공 기대,03:14 이전 snapshot 증거 overwrite가 installer를 실패시켰다. **두 시도 모두 제품/schema40·core smoke는 정상이었으나 증거 처리 오류로 불필요한 rollback을 수행했다.** 두 복구 모두 P8/schema38·원래 정지 상태·데이터/정책/files/archives 보존에 성공했고 실패 dump/복구 evidence를 보존했다. 최종 installer hash `cd68eba44cb7b1266deee6968650ffa96fdd40f6b759a3ef6a8e48e2c81632c5`로03:23 신호 갱신 후03:24 성공했다. 제품 결함·추가 권한 blocker는 없다.

지원 경계: N1 자동 기록·N2 public/SPA는 기존 설정대로 비활성; N5 standalone 등록 callbacks 없음은 통합 테스트 근거와 구분한다. 공개 release/npm/pub.dev/cloud/provider 설정은 수행하지 않았다.

병렬 후속: N6 Chandrasekhar/Sol·N7 Einstein/Terra는 별도 승인으로 merged `2939f15` 기반 전용 `n6-n9/n6`,`n6-n9/n7` worktree에서 구현 중이다. live 설치·ports·공용 docs 접근 금지, 계획 `133f644`/`8dadf42`와 코드는 이 설치 문서 커밋에 섞지 않는다.

## N1–N5 병렬 실행 — 로컬 통합·인수 완료 (최종 동기화 2026-09-10 02:13 KST)

실행 계획: [N1–N5 계획](docs/superpowers/plans/2026-09-09-n1-n5-execution.md). Astra는 조율, GPT-5.6 Sol/Terra는 구현·통합·검증을 담당한다.

| 항목 | 담당 | 상태 | 증거 |
| --- | --- | --- | --- |
| 계획·작업 분할 | Astra | 완료 | 실행 계획 작성 |
| N1 자동 migration | Chandrasekhar / GPT-5.6 Sol | 구현·독립검토·집중/전체 race·live·N1↔N4 matrix 완료, 로컬 통합 인수 | [N1 상태](docs/workstreams/n1-n5/N1.md), migration 40, product through `c35ea58`, matrix `185227d` |
| N2 정적 사이트·SPA | Kant / GPT-5.6 Terra | 구현·집중/전체 PG18 race·독립검토·실제 browser 완료, 로컬 통합 인수 | [N2/N3 상태](docs/workstreams/n1-n5/N2-N3.md), product through `6d53192` |
| N3 관리자 CLI | Kant / GPT-5.6 Terra | 구현·집중/전체 PG18 race·독립검토·OTP/IP/session browser 완료, 로컬 통합 인수 | [N2/N3 상태](docs/workstreams/n1-n5/N2-N3.md), migration 39, product through `6d53192` |
| N4 필드·조회 | Lagrange / GPT-5.6 Sol | 구현·DB/HTTP/UI/SDK/browser·독립검토·N1 보존 matrix 완료, 로컬 통합 인수 | [N4 상태](docs/workstreams/n1-n5/N4.md), product through `98d848a` |
| N5 세부 hooks | Euler / GPT-5.6 Terra | 구현·수정·독립검토·mutable auth/cancel/panic/replay·전체 race 완료, 로컬 통합 인수 | [N5 상태](docs/workstreams/n1-n5/N5.md), product through `99cce83` |
| 통합·검토·전체 검증 | Sol 통합 | 전체 로컬 인수 완료 | [통합 상태](docs/workstreams/n1-n5/integration.md), product/test `2837802`; final unit/race·PG18+MinIO integration/race·skip policy·UI/SDK/browser/build/policy green |

- [x] 계획과 완료 기준 기록.
- [x] N1–N5 각 구현·집중 검증 완료.
- [x] 통합·독립 검토·전체 회귀 완료.
- [x] 최신 상태 문서와 오래된 체크리스트 정리.

최종 acceptance(02:10 KST):

- [x] Euler / GPT-5.6 Terra — 실제 Chromium 20개 전부 실행: 일반 17건과 gated managed backup, local↔S3 file migration, independent S3 backup 모두 통과. N2/N3/N4 focused browser도 통과; production-like `59000`은 사용하지 않았다.
- [x] Lagrange / GPT-5.6 Sol — reload 500은 임시 harness의 non-waiting `locator.count()` 실패가 browser를 닫아 in-flight list를 취소한 결과. checked-in auto-wait spec과 waiting full flow 통과. 잘못된 product 가설 `830278d`는 `2837802`에서 exact revert; final backup unit/race·PG18+MinIO 3/3·독립 review 통과.
- [x] Chandrasekhar / GPT-5.6 Sol — final N1 5-package unit, 13-test PG18 race, N1→N4 matrix, 외부 생성 모듈 Up/Down·data preservation·irreversible no-mutation, durable recovery/history, fresh 1→40, 38→40 upgrade/doctor, 1–38 byte/checksum 불변 통과. 상태 evidence integration `2f98ccc`.
- [x] Kant / GPT-5.6 Terra — static gates 통과. release direct-constructor 실패는 v6 rewind test가 신규 39/40 table을 남긴 fixture 결함으로 확인; test-only `cc5b271`→integration `845e32d`, targeted release/PG18 race·migration idempotency·1–38 불변 통과.
- [x] Chandrasekhar / GPT-5.6 Sol — doctor 37→38은 N1 v40 이전 history 호환용 고정 `to_regclass(user_migration_schema_versions)` probe 1회의 정확한 증가. catalog 2/20개·off/dual-write/ready 모두 38; ceiling/rationale `4f73d47`→integration `16d5bbd`, full doctor PG18 race 25.683s·unit/vet 통과. Kant 독립 review도 exact PG18 race 3.695s와 no findings로 종료.
- [x] Euler / GPT-5.6 Terra — final all-package unit/race PASS(24.2s), 별도 PG18 `55444`+MinIO `59004` full integration/race PASS(`pgbase` 191.613s, `recordops` 176.678s, `query` 160.119s, `schema` 142.565s, `doctor` 124.423s), explicit skip-policy PASS(1.163s).
- [x] Sol 통합 — false product fix exact revert, 결과 반영, public status/next-work/reassessment 갱신, 최종 local commit. push/PR/remote merge/deploy/install은 수행하지 않음.

격리 fixture 정리(02:11 KST): N1–N5 전용 PostgreSQL 18/MinIO container 4개와 전용 network 2개를 제거했다. 설치된 PostgreSQL 17, MinIO, PgBase server/binary, 사용자 DB·파일·환경은 변경하지 않았다.

최종 문서·tree 독립 확인(02:13 KST): 다섯 공용 상태 문서의 구현/검증/로컬 commit/원격 병합/설치 구분에 finding 없음. `830278d^`↔`2837802` backup diff 없음, migration 1–40 연속·1–38 불변, debug/instrumentation artifact와 격리 fixture 잔여 없음, `git diff --check` 통과.

실행 기록: 계획 작성 및 독립 worktree 준비 완료. 2026-09-09 23:46 KST에 네 worktree의 실제 상태와 담당을 확인하고 중앙 보드에 반영했다. migration 39는 N2/N3에 예약했고 N1의 durable recording에 새 영구 상태가 꼭 필요할 때만 40을 사용한다. N4의 영구 필드 옵션 저장 요구는 번호를 만들기 전에 통합 담당에게 보고한다. N4 `FieldOptions`→N1 canonical snapshot/diff, N1 durable mutation recording→N5 process-local request/post-commit hook 분리, 공통 Options/router/CLI 충돌 경계를 담당자에게 전달했다. 23:50 KST에 전용 PostgreSQL 18(`55442`)·MinIO(`59001`) fixture를 준비하고 system migration/database 및 실제 S3 smoke test를 통과했다. 00:11 KST에 Kant가 같은 격리 PG18에서 N2/N3 집중 `-race` DB 검증을 완료했다. Euler가 보고한 P1 3건은 `f68ad01`에서 수정됐고 00:18 KST read-only 재검토로 모두 닫혔다. N2/N3은 통합 대기이며 전체 회귀 전이다. 기존 로컬 설치 P8/schema v38은 유지하며 이번 검증은 격리 환경만 사용한다.

구조 budget 해석(00:20 KST): production 파일 800줄 상한과 실용적인 모듈별 고정 cap은 유지한다. 과거 전역 runtime/interface/export 기준은 N1–N5의 승인된 기능 증가분을 정확히 측정하고 설계 근거·독립 검토·통과 테스트를 남기는 경우 조정할 수 있다. 최종 변경 수치와 근거는 이 보드에 공개하며, 기존 P8 기능 삭제나 무관한 대규모 refactor로 과거 전역 수치에 맞추지 않는다.

최종 budget 측정·독립 검토 완료(01:21 KST): `238cc37`→최종 제품에서 Go runtime 58,513, UI 6,249, handwritten 64,762(+3,345 net), interface 57→60, export 973→1,053, constructor 46→49, page hook 129→131이며 UI export는 203으로 동일하다. schema data-group은 N1/N4 승인 기능으로 21,747→22,693(+946: schema +875, recordops +76, query -5)이다. schema-private transaction/planner 경계를 깨는 광범위 refactor 대신 정확한 증가와 설계 근거를 반영했고 어떤 production 파일도 제외하지 않았다. 고정 800줄 상한을 유지하며 helper를 동작 변경 없이 옮겨 `planner.go` 763줄, `schema_document.go` 798줄, 800줄 초과 production 파일 0개다. 독립 Standards/Spec review와 unit/race/lint가 통과했다.

문서 시작점: [구현 이력·현재 상태](docs/project-status.md) · [후속 작업 N0–N9와 완료 기준](docs/next-work.md) · [PocketBase 현재 재평가](docs/pocketbase-current-reassessment.md). 이 파일은 개별 체크와 실행 이력을 보존한다. 과거 단계의 제외 범위는 당시 기록이며 최신 상태는 상단 완료 기록과 위 문서를 따른다.

기준: 2026-09-10, v0.1.0 이후. 최신 제품 병합·실제 로컬 설치는 [PR #34](https://github.com/yakisoba0728/PgBase/pull/34) / `2939f15`, N1–N5, PostgreSQL17.9/schema40이다. P1–P5는 PR #29, P6는 #30, P7은 #31, P8은 #32로 구현·로컬 검증·원격 CI·병합·설치를 마쳤다. 외부 인프라·실제 외부 계정 연결·공개 패키지 배포는 대기 범위다.
체크된 항목만 완료다. 구현·검증·병합·릴리스는 별도로 표시한다. 각 담당자의 결과가 들어오거나 검증 단계가 바뀔 때 이 파일과 아래 변경 기록을 갱신한다.

## P7 — 필드 표시·인증 확장·스키마 입출력·Go 라우트 (로컬 적용 완료)

전체 후속 범위를 유지한다. 아래 작업 묶음의 완료는 PocketBase 전체 호환이나 외부 운영 검증 완료를 뜻하지 않는다. 비교 기준은 로컬에 고정한 PocketBase v0.40.3 소스다.

| 작업 | 담당 | 상태 |
| --- | --- | --- |
| Hidden/Presentable/Help 필드 계약·응답·관리 UI | Sol / phase2_files | scalar/관계/파일 및 기존 파일 토큰 차단 PG18 HTTP 검증 통과 |
| 스키마 export/import 검토·원자적 적용·관리 UI | Sol / phase2_sql_planner | 상호 관계/복합 인덱스·View·동시성 PG18 및 SDK 정확 JSON 전체 경로 통과 |
| 이메일 OTP 로그인·정책·메일·SDK | Sol / phase2_oauth_catalog + Root | 실제 SMTP/HTTP 정책·realm·MFA·SDK 및 설정 브라우저 검증 통과 |
| 관리자 impersonation·임시 세션·관리 UI | Sol / phase2_oauth_catalog | PG18 실제 named-realm HTTP·민감 작업 제한·생성자 폐기 검증 통과 |
| Go custom route 등록·인증·관찰·문서 | Root | 구현 및 단위/PG18 실제 HTTP 검증 통과 |

- [x] 기준 main·clean checkout·로컬 readiness 재확인, 새 작업 브랜치 생성.
- [x] 과거 PB-A 후보 목록을 P1–P6의 실제 완료 범위와 대조하여 동기화.
- [x] 필드 표시 metadata의 persistence/validation/rename/export 보존. migration29, schema roundtrip 및 기존 rename 경로 검증.
- [x] Hidden의 사용자·service·anonymous·expand·realtime 응답 계약 검증, 관리자 편집 보존. 공통 principal gate + PG18 관계/파일 HTTP; 파일이 숨겨진 뒤 기존 다운로드 토큰도 차단, 관리자 원본 접근 보존.
- [x] Presentable 관계 표시·Help 편집/입력 안내·SDK 타입 및 UI 검증. UI 460개 통과, 실제 브라우저 표시 설정/도움말 저장 통과.
- [x] 버전 있는 스키마 문서와 export/import round-trip; 상호 관계·복합 인덱스·View·정책의 실제 HTTP/DB 검증 및 지원하지 않는 View metadata 명시적 거절.
- [x] import preview와 stale 계획 거절, 원자적 apply 및 실패 시 기존 데이터 보존. 늦은 실패 롤백·중복 요청 취소·DB backend 종료·권한 최초 삽입 경쟁 PG18 통과.
- [x] 관리자 export/download·import 검토/apply·HTTP/SDK 연결. SDK raw JSON export→preview→apply 숫자 보존 및 immutable 요청 snapshot 검증 통과.
- [x] 이메일 OTP 요청/확인·만료·재발송·시도 제한·동시 사용 방지. PG18 동시 재발송/사용/정책 비활성화 경쟁 검증 통과.
- [x] realm 정책·메일 template/시험·MFA 연계·세션 무효화·SDK 검증. DTO 수정 후 실제 SMTP+HTTP default/named realm 및 정책 비활성화 검증 통과. 기존 사용자만 허용하고 이메일 인증 상태를 자동 변경하지 않음; 외부 SMTP 인증은 대기.
- [x] 실제 관리자 OAuth·SMTP·메일 template 입력의 요청 전용 필드 매핑 검증. path-only/response-only metadata 전송 문제 수정 후 브라우저 저장·재조회·재저장 통과.
- [x] 관리자 impersonation 임시 세션·생성자 추적·기간/폐기·HTTP/SDK/UI 및 실제 DB 검증. named realm HTTP의 민감 작업 제한·생성자 세션 폐기 및 브라우저 발급·폐기 통과.
- [x] Go custom route의 공개 등록 interface·경로 충돌 검증·명시적 인증 정책. `Options.CustomRoutes`/`PrincipalFromContext`, 예제 및 `docs/custom-routes.md`.
- [x] custom route에 기존 요청 제한·오류 처리·인증·로그/metric/hook 연결 및 실제 HTTP 검증. 단위: 충돌/상한/응답 실패 원자성/metric template; PG18: 관리자 CSRF·세션/사용자·service 인증 분리·폐기 후 차단.
- [x] 새 migration 순서·기존 checksum·OpenAPI·구조 예산 검증. 1–28 원본 동일, 신규 29–32 및 현재 사용 DB 복사본 upgrade/doctor 통과. 실측 예산·800줄 제한 및 전체 Go unit/race 통과.
- [x] 전체 Go/UI/SDK 회귀와 새 기능 브라우저 인수. Go unit/race·PostgreSQL18+MinIO 전체 integration/race·skip 정책, lint/vet, UI 466개·전체 브라우저 15개와 추가 응답 유실 복구, SDK·6종 로컬 빌드 통과.
- [x] [PR #31](https://github.com/yakisoba0728/PgBase/pull/31) 생성.
- [x] 필수 원격 CI 5개 통과·[PR #31](https://github.com/yakisoba0728/PgBase/pull/31) 병합(`701256b`). 기존 DB·파일·실행 파일 백업 후 v28→v32 적용. doctor 이상 없음, 관리자 로그인·schema export/preview·PG17 웹 백업/download/hash 검증 통과.

현재 검증 기록: UI 466/466, 전체 브라우저 15/15와 추가 응답 유실 복구 포함 P7 5/5 통과. 전체 Go unit/race는 구조 검토·실측 예산 반영 후 통과. SDK 정확 JSON 검증 통과. 복원 전환 중 503을 처리하도록 테스트 poll 수정 후 5회 통과했으며 최종 PostgreSQL18+MinIO 전체 integration/race와 skip 정책까지 통과. [P7 PR #31](https://github.com/yakisoba0728/PgBase/pull/31)의 필수 원격 CI 5개가 통과했고 병합 완료. P7 설치 당시 기준은 PR #31 `701256b`, schema v32였고 readiness가 정상이었다. 현재 설치는 상단 최신 상태를 따른다.

- [x] 기존 실행 DB의 별도 복사본에 v28→v32 마이그레이션 적용 및 doctor 확인. 원본 DB/서버는 그대로 유지했고 readiness 정상.
- [x] 구버전 field metadata 읽기 호환 수정 및 mixed-fleet CLI 회귀 2개 통과.
- [x] 스키마 import 상호 관계 컬렉션·복합 인덱스의 실제 HTTP 왕복 회귀 수정/통과. 관계 ADD COLUMN 후 record-size 검증으로 순서를 수정.
- [x] import 복구 중 새 preview 차단·저장 실패·export 후 계획 무효화 UI 회귀. 실제 응답·상태 조회 유실 뒤 브라우저 새로고침으로 committed 확인, recovery에 schema 본문 비저장 확인.

P7 당시 후속 후보였던 고급 필드·S3 설정·사용자 migration·추가 hooks의 현재 상태는 아래 P8을 따른다. JSVM·Dart·SQL 콘솔·self-update 및 외부 검증은 후속 목록에 남는다.

## P8 — 고급 필드·S3 설정·사용자 migration (병합·로컬 적용 완료)

최신 확인: Go 전체 unit/race·skip 정책, UI 489/489·typecheck/build, 전체 관리자 브라우저 19/19 및 백업 probe 수정 후 P8 3/3, SDK 단위·pack, lint/vet·OpenAPI·6종 빌드·구조 예산이 통과했다. PostgreSQL 18+MinIO integration/race·skip 정책 및 live SDK conformance도 통과했다. 전체 실행 중 확인한 백업 lifecycle 테스트 동기화 수정은 해당 패키지 전체 재검사로 보완했다. [PR #32](https://github.com/yakisoba0728/PgBase/pull/32)의 필수 CI 5개가 통과했고 `0d28446`로 병합했다. 원본 DB·파일·백업 아카이브·환경·바이너리를 별도 백업한 뒤 로컬 v32→v38 적용, doctor/readiness·관리자 로그인·고급 필드 preview·저장소 연결 시험·PG17 백업/download/hash 확인을 마쳤다.

Sol 에이전트 3명과 Root가 PocketBase v0.40.3 원본 계약을 PgBase의 PostgreSQL·UUID·세션 계약과 대조했다. 개별 구현/검증, 전체 인수, 병합, 실제 설치와 공개 릴리스를 구분한다.

| 작업 | 담당 | 확인한 구현과 증거 |
| --- | --- | --- |
| editor/geoPoint 및 필드 스키마 | Sol / phase2_files + Root | populated rename·View·schema 왕복, 좌표 filter/sort/cursor, 5 MiB HTML HTTP/Batch, 관리자 편집 |
| autodate/password | Sol / phase2_sql_planner + Root | 해시/시간, retry/replay/rollback, 응답 비노출, draft/duplicate 제외, 공개 Go 암호 검증 |
| 파일 S3 설정과 이전 | Sol / phase2_oauth_catalog + Root | 암호화된 revision 설정, DB/MinIO·HTTP·SDK·브라우저, durable 이전·응답 유실 복구·lease 유실 방어 |
| 사용자 migration | Sol / phase2_sql_planner + phase2_files | Go 생성/목록/up/down, checksum/rollback, schema Apply 경쟁·공유 캐시, 외부 모듈 CLI list |
| lifecycle hooks | Sol / phase2_files + Root | app/schema/auth/realtime/settings/import/backup의 veto·commit·실패 결과 연결 |
| 백업 S3 | Sol / phase2_sql_planner + phase2_oauth_catalog | 독립 repository·API/UI/SDK, startup 경합·pending fence·소유 객체 보호·정확한 probe 삭제, S3 파일 snapshot의 독립 로컬 복원 |

개별 로그는 `.local/verification/phase4-*.log`에 있고 재현 가능한 회귀는 소스에 포함했다. 기존 UI 481/486개 결과는 과거 실행이다. 최신 UI는 489개이며 브라우저 전체 19개와 후속 P8 3개는 중복되므로 합산하지 않는다.

### P8-A 고급 필드
- [x] editor: 저장 형식·크기/필수 검증·출력 방식과 관리자 편집기 계약 대조.
- [x] editor: schema preview/apply·기존 데이터 검사·API/SDK·생성/수정 UI 연결.
- [x] geoPoint: 좌표·범위·빈 값·정밀도·필터/정렬 지원 범위 대조 및 입력/응답 계약 구현.
- [x] geoPoint: PostgreSQL 저장·기존 데이터/인덱스·조회 및 관리자 좌표 입력 UI 통합 검증.
- [x] 사용자 autodate: 생성/수정 자동 갱신·사용자 입력 처리·batch/upsert 시점 계약 구현.
- [x] 사용자 autodate: 사용자 필드와 시스템 created/updated 의미를 구분하고 SDK/UI/실제 DB 회귀.
- [x] 일반 password 필드: 사용자 인증 암호와 별개인 해시 저장·조회 비노출·입력 변경 계약 대조.
- [x] 일반 password 필드: 숨김/기본값/필터/인덱스/복제/export 제약·로그 비노출·관리자 입력 UI 검증.
- [x] 새 타입의 populated rename·물리 데이터 보존·schema export/import·View 및 관계 조회·Batch·권한 규칙 회귀. 집중 검증과 최종 PG18/race·관리자 브라우저 인수 통과.
- [x] 구버전 API로 데이터·관계를 만든 DB 복사본의 v32→v38 업그레이드·해시/행 수 보존·doctor (`phase4-populated-upgrade/result.json`).
- [x] 최종 변경 집합의 기존 migration 1–32 checksum 불변, 신규 33–38 embed·upgrade 및 전체 회귀 통과.

### P8-B S3 관리자 설정과 시험
- [x] 현재 환경 기반 S3 구성과 관리자 저장 설정의 우선순위·비밀 값 보존/비반환 계약 결정.
- [x] endpoint/bucket/region/path style/인증 설정 검증·읽기/갱신 API·UI 구현.
- [x] 시험 연결이 기존 객체를 변경하지 않도록 전용 시험 객체 생성/읽기/삭제 및 실패 정리.
- [x] 설정 변경의 기존 파일 위치·다운로드·업로드/변환/백업 일관성을 DB lease/generation·durable ledger·실제 MinIO 실패/복원 회귀로 검증.
- [x] 실제 로컬 MinIO·관리자 브라우저·재시작·잘못된 설정/연결 실패 회귀 검증.
- [x] 기존 파일 이전의 최종 인수. durable ledger/copy/verify/cutover/resume/cancel·다운로드/응답 유실 브라우저·pool/lease·복원 경로의 최신 PG18+MinIO/race 통과.
- [x] 백업 아카이브 자체의 독립 S3 repository·관리 API/UI/SDK 및 실제 S3 snapshot 복원/lease 유실 집중 검증.
- [x] 업로드 파일·백업 저장소·managed 복원을 최신 코드로 통합 인수. 소유 객체/metadata·startup 경합·pending fence·실제 S3 원본 삭제 후 독립 로컬 복원·probe 정리 검증.

### P8-C 사용자 migration과 확장
- [x] 시스템 migration과 사용자 migration을 분리한 기록·순서·checksum 계약 구현. 적용 소스 변경 거절·실패 rollback·동시 runner PG18 회귀 통과.
- [x] 사용자 migration 생성/목록/up/down CLI와 Go 작성 API·예제 구현. 외부 Go 모듈 실행 및 같은 초에 생성된 여러 migration 빌드 검증 통과 (`phase4-user-migrations.log`).
- [x] 사용자 migration 실패 rollback·동시 runner·적용 소스 변경·managed physical/control state 손상 거절 집중 PG18 검증.
- [x] SQL 취소·Up/Down 실패 rollback·동시 runner·checksum/제어 ledger 보호와 실제 schema Apply 경쟁 증거 확인. migration의 잠금을 기다린 stale schema 계획은 충돌로 거절되고 부분 DDL 없이 공유 캐시를 갱신한다. 전체 usermigrate PG18/race 통과.
- [x] 신뢰된 Go callback의 비가역 외부 효과는 sandbox/rollback으로 보장하지 않음을 `docs/user-migrations.md`에 명시. 제공된 pgx.Tx만 사용하는 계약과 외부 모듈 CLI list 검증 범위를 구분.
- [x] PocketBase lifecycle hook 목록과 PgBase 이벤트를 `docs/lifecycle-parity.md`에 대조.
- [x] app boot/serve/terminate, collection create/update/delete, password/OAuth/OTP/verification/reset/email-change/impersonation hooks 구현. 중복 시작·veto·commit/replay·정보 비노출 단위/race/PG18 회귀 통과.
- [x] realtime connect/subscribe/message hooks. API 단위/race 및 실제 PG18 SSE admission·subscribe·message 검증 통과.
- [x] 저장된 file storage settings hooks, schema import의 collection별 commit hook 연결. PG18 HTTP veto 및 다중 컬렉션 atomic rollback/commit/replay 검증 통과.
- [x] backup create/restore/delete hooks와 최종 작업 receipt·activation 결과 전달. manager veto/완료 단위·race 검증 통과; 새 S3 관리자 설정을 포함한 전체 복원 회귀도 통과.
- [x] JSVM·Dart SDK·SQL 콘솔·self-update 후속 작업과 완료 기준을 `docs/next-work.md` N6–N9로 분리. 세부 설계·구현은 아직 미완료.

### PocketBase 재대조에서 구체화한 후속 공백

현재 소스와 Sol 독립 확인 근거는 `docs/pocketbase-current-reassessment.md`에 기록한다. 아래는 P8 구현 완료와 별도로 추적한다.

- [ ] 관리자 스키마 변경의 migration source 자동 diff·collection snapshot·history-sync 계약 및 구현.
- [ ] 사용자 public 정적 디렉터리와 SPA fallback 지원.
- [ ] 관리자 CLI upsert/update/delete·OTP·IP 관리의 대응 범위와 구현.
- [ ] 문자열 자동 생성 패턴 및 email/URL 도메인 차단 목록.
- [ ] 세부 CRUD/batch/import 요청 hooks, 목적별 mail 및 파일 token/download/transform hooks의 대응 범위와 구현.


## P6 — PocketBase 비교의 부분 구현 영역 마무리 (구현·로컬 인수 완료)

기준 main `33816df` / PR #29. 사용자가 ② 부분 구현 영역 전체를 승인했다. 아래는 **구현·연결·검증까지 끝난 항목만** 체크한다. 외부 배포·실제 외부 계정 검증은 수행하지 않으며, ③의 JSVM/Dart/이메일 OTP/impersonation 등은 이번 범위에 포함하지 않는다. SQL View 표현력은 이번 범위이고, 임의 SQL 실행 콘솔은 별도다.

| 영역 | 담당 | 상태 |
| --- | --- | --- |
| 파일 필드·변환·트랜잭션 연결 | Sol / phase2_files | 파일·multipart Batch 통합 검증 통과 |
| OAuth 제공자·아바타·HTML 메일 | Sol / phase2_oauth_catalog | 30개 프리셋·Apple bridge·PG mock 검증 통과 |
| 영구 로그·달력 스케줄러 | Sol / phase2_operations | PG/UI 검증 통과 |
| SQL View backend | Sol / phase2_sql_planner + Root | planner·수정·권한·페이지 조회 통합 통과 |
| 범용 batch | Sol / phase2_files | multipart·멱등성 검증 통과 |
| 웹 백업·Go lifecycle hooks | Root | 백업 PG18/MinIO/HTTP·hook HTTP 검증 통과 |

### P6-A 파일
- [x] 필드별 크기/MIME/최소·최대 개수 정책과 관리자 편집·SDK 타입.
- [x] 기존 파일과 충돌하는 정책 강화의 preview/preflight 거절.
- [x] 업로드·삭제·레코드 쓰기의 필수 파일/개수 제약 및 경쟁 검증. direct create 누락을 최종 transaction 검증으로 해결; storage 미설정도 우회 불가.
- [x] multipart batch를 위한 파일 staging/transaction commit/실패 정리.
- [x] WxH/crop/fit/top/bottom 변환·캐시·상한·인가·실패 정리.
- [x] 로컬/S3 및 실제 DB·HTTP·UI 회귀 검증. 필수 파일 생성·원자적 교체 포함 브라우저 11/11 통과.

- [x] 필수 파일 포함 관리자 생성/교체를 multipart Batch로 연결하고 실패 시 선택 파일·입력 보존.
- [x] 변환 객체 저장 후 metadata 실패·commit 결과 불확실 시 동일 publication 잠금으로 결과를 기다린 뒤 참조 확인·정리. PG18 실패/경쟁 회귀 통과.
- [x] 같은 레코드/파일 필드의 여러 Batch 작업을 요청 순서대로 교체.

### P6-B OAuth·메일
- [x] 제공자 카탈로그·설정 프리셋·관리자 구성 UI.
- [x] provider별 설정과 generic adapter의 런타임 적용·로컬 mock 검증.
- [x] 프로필 이미지 가져오기와 기존 사용자 데이터 보존.
- [x] redirect/교환 흐름의 브라우저 완료·취소·오류 처리.
- [x] HTML 템플릿·공통 레이아웃·plain-text 대체 본문.
- [x] 변수 검증·미리보기·시험 발송·실제 로컬 SMTP 검증.

### P6-C View
- [x] 읽기 전용 SQL View 정의·미리보기·결과 타입 추론.
- [x] JOIN·집계·그룹화와 안정적인 UUID 행 식별자 계약.
- [x] 쓰기/위험 함수/잘못된 타입/중복 ID 거절과 쿼리 실행 상한.
- [x] 의존 컬렉션 변경·삭제 시 보존 또는 원자적 거절.
- [x] 관리자 SQL View 편집·표본 결과·SDK 타입.
- [x] 실제 PostgreSQL/HTTP/브라우저 조회·권한·회귀 검증.

### P6-D Batch
- [x] collection을 가로지르는 경로 기반 JSON 요청 묶음.
- [x] create/update/delete/PUT upsert와 부모 인증·권한 적용.
- [x] multipart 파일 입력과 레코드·첨부 metadata의 단일 DB transaction.
- [x] 실패/취소/응답 손실/retry에서 DB·파일·outbox 정합성.
- [x] 요청별 상태·본문·ETag와 제한 오류의 명확한 응답.
- [x] TypeScript SDK·예제·실제 DB/HTTP 연동 검증.

### P6-E 로그
- [x] 영구 요청 로그 저장·제한된 비동기 큐·종료 시 flush.
- [x] 보관 기간·용량·drop 관측과 성능 상한.
- [x] 검색·상세·통계·삭제 HTTP/UI와 관리자 인가.
- [x] 민감 입력 비저장·재시작 지속성·경쟁·UI 검증.

### P6-F 예약 작업
- [x] 달력 cron 표현식·timezone·DST·다음 실행 계산.
- [x] 기존 @every·lease/fence·재시도·설정 지속성 보존.
- [x] Go handler 런타임 등록·해제 및 미등록 상태 처리.
- [x] 관리자 편집·검증·수동 실행 UI와 실제 실행 회귀.

### P6-G 웹 백업·복원
- [x] 백업 목록·생성·작업 진행/실패 조회·다운로드·삭제.
- [x] 업로드된 recovery set의 구조·크기·해시 검증.
- [x] PostgreSQL snapshot과 immutable 파일/S3 객체의 일관된 온라인 백업.
- [x] 파일 GC와 백업의 배타 처리·중단/실패 정리.
- [x] 새 대상 복원·검증·관리자 활성화 흐름과 기존 대상 보존.
- [x] 재시작/복원 중 관리 화면·작업 상태·인증 재확인.
- [x] 로컬 PostgreSQL+MinIO 실제 복원 및 브라우저 회귀.

### P6-H Go 확장
- [x] 공개 lifecycle hook 등록/해제·정렬·취소·오류 전파 계약.
- [x] 요청·응답·모델·메일·파일 동작에 실제 hook 연결.
- [x] 트랜잭션 내부 재시도와 commit 이후 동작 구분.
- [x] 기존 mutation hooks 호환·문서·Go 예제.
- [x] 순서·실패·동시성·취소·실제 end-to-end 호출 검증.

### P6 검증 기록
- 2026-09-09: PostgreSQL 18·MinIO 온라인 snapshot 복원, 활성화 실패 정리, record/mail/file/request hook, SQL View 집계·수정·독립 권한, multipart Batch 회귀 통과.
- 2026-09-09: 실제 관리자 브라우저 10/10 통과(기존 8개 + SQL View 생성·조회, 백업 생성·다운로드·업로드·삭제). SQL 정의 수정 브라우저와 최종 전체 회귀는 추가 실행 중.
- 2026-09-09: Apple form_post bridge·nonce/JWKS·DB state 소비 로컬 통합 통과. PocketBase 현재 registry는 30개이며 오래된 upstream 테스트의 32개 기대값과 구분함.
- 2026-09-09: 과거 v6로 되감는 fixture 정리 누락과 file maxSelect catalog 검증 충돌 수정. 이후 전체 Go unit/race·PostgreSQL 18 integration/race·lint/vet 통과. 최종 필수파일 생성/변환 실패 정리 수정은 추가 인수 중.

- 2026-09-09: 필수 파일 JSON 생성 우회/스토리지 미설정 우회, 동일 레코드 Batch 파일 교체 순서, 미확정 commit cleanup의 선행 삭제, ready 파일 failUpload 삭제 경로를 수정하고 PG18 실제 경쟁·rollback 검증 완료.
- 2026-09-09: 최종 UI 459개·인증 브라우저 11개 통과. 원본 로컬 PostgreSQL 17 DB·파일·이전 실행 파일 백업 후 v23→v28 업그레이드, doctor 이상 없음. 같은 로컬 서버의 관리자 로그인·온라인 웹 백업·다운로드·아카이브 hash 검증 완료.

### P6-I 통합 인수
- [x] migration/과거 checksum·OpenAPI·측정 기반 구조 예산 갱신. v24–28 추가, 과거 1–23 checksum 유지, production 800줄 초과 0.
- [x] Go unit/integration/race·lint/vet 및 필요한 skip 정책. 최종 전체 PostgreSQL 18+MinIO race suite와 단위 suite 통과, lint 0 issues, vet 통과.
- [x] UI typecheck/unit/build·브라우저·SDK conformance. UI 459/459, 인증 브라우저 11/11, SDK 패키징·HTTP 검증 통과.
- [x] 데이터가 있는 기존 로컬 프로젝트의 백업·upgrade·doctor 검증. PostgreSQL 17 로컬 DB 23→28, doctor ok, 실제 관리자 로그인·웹 백업·다운로드·해시 검증 통과.
- [x] 구현/부분 구현/미구현 상태표와 정확한 제약 갱신. `docs/phase2-local-contracts.md`에 8개 영역 및 유지하는 차이·③ 제외 범위를 명시.
- [x] [PR #30](https://github.com/yakisoba0728/PgBase/pull/30) 생성과 원격 CI·병합 상태 연결. 로컬 최종 확인 완료. CI·병합의 현재 결과는 이 PR의 GitHub 상태를 따른다.

## P1–P5 — PocketBase 소스 감사 후 로컬 제품 완성 (구현·로컬 검증 완료)

승인 범위: 아래 다섯 영역의 구현과 로컬 검증. JSVM·Dart·임의 SQL 콘솔과 외부 배포는 이번 범위에서 제외한다. 기준 소스 감사는 `docs/pocketbase-source-audit-2026-09-09/assessment.md`를 따른다.

| 영역 | 담당 | 현재 상태 |
| --- | --- | --- |
| P1 필드·스키마 계약 | Sol / schema_fields + Root UI/SDK | DB·브라우저 검증 완료 |
| P2 레코드 관리 생산성 | Sol / record_productivity | 전체 로컬 회귀 통과 |
| P3 인증 관리 | Sol / auth_administration + Sol 교차 검토 | 전체 로컬 회귀 통과 |
| P4 운영 콘솔 | Root + Sol / auth_administration | 구현·복원·취소·연결 손실 회귀 통과 |
| P5 컬렉션 API 예제·SDK | Root | 구현·SDK 타입/재시도 테스트 통과 |

### P1 필드·스키마
- [x] 필드 ID를 이용한 이름 변경과 기존 데이터 보존.
- [x] 인덱스·뷰·규칙의 필드 참조를 일관되게 갱신하거나 안전하게 거절.
- [x] Required = SQL NOT NULL 계약을 UI·문서에 명시.
- [x] 텍스트 길이·패턴, email/URL·도메인, select 값·수량 검증.
- [x] metadata·DDL·plan checksum·dry-run·apply·실제 레코드 입력 검증 연결.
- [x] 기존 데이터와 충돌하는 제약 강화의 원자적 거절.
- [x] 관리자 스키마 편집 UI와 TypeScript SDK 타입 연결.
- [x] PostgreSQL 데이터 보존·rollback·검증 통합 테스트 및 UI 테스트.

### P2 레코드 관리
- [x] 현재 목록에서 다중 선택 및 ETag 기반 일괄 삭제·부분 실패 표시.
- [x] 현재 필터·정렬의 페이지네이션 JSON 내보내기와 상한 표시.
- [x] 시스템·파일 필드를 제외한 새 ID 레코드 복제.
- [x] 컬렉션·관리자 단위 열 표시 설정 유지.
- [x] 비밀 입력을 제외한 로컬 초안 복구와 저장·로그아웃 정리.
- [x] 관계 선택기에서 연결 레코드 생성·수정.
- [x] 취소·오류·응답 역전·모바일·접근성 회귀 검증.

### P3 인증 관리
- [x] realm OAuth 설정 저장·실제 런타임 적용 및 비밀 값 비반환.
- [x] 가입·암호·MFA 정책 저장과 런타임 적용.
- [x] 제한된 인증 메일 템플릿 편집·검증·실제 메일 적용.
- [x] 관리자 SMTP 테스트 발송 및 사용자 인증·복구 메일 발송.
- [x] 관리 UI·same-origin API·앱 의존성 연결.
- [x] PostgreSQL·로컬 OAuth/SMTP·UI 통합 검증.

### P4 운영 콘솔
- [x] 관리자가 최근 요청 로그를 제한된 페이지로 조회·필터링.
- [x] 자격 증명·요청 본문·쿼리값을 로그에 저장하지 않는 계약 검증.
- [x] 등록된 작업 목록·실행 상태·간격·활성 상태 조회/변경.
- [x] 수동 실행 요청과 기존 lease/fencing 경계 보존.
- [x] 로컬 DB+파일 백업과 새로운 대상에 복원하는 실행 가능한 흐름.
- [x] 백업·복원 전제와 결과를 관리 UI·문서에서 확인 가능.
- [x] 운영 API 권한·작업 동시성·복원 무결성 검증.

### P5 API 예제·SDK 및 전체 완료
- [x] 컬렉션 필드·접근 방식에 맞춘 CRUD·필터·정렬·페이지 예제.
- [x] ETag·인증·관계·파일의 실제 API 계약 설명.
- [x] SDK에서 새 필드 옵션·ID 및 필요한 관리 API 지원.
- [x] SDK 예제 타입 검사·테스트·로컬 패키징.
- [x] 전체 Go unit/integration·UI·브라우저·빌드 검증.
- [x] OpenAPI·migration manifest·측정 기반 구조 예산과 문서 갱신.
- [x] 리뷰 문제 재현·수정·재검증.
- [x] PR 준비: 구현·검증·문서·로컬 실행 상태 확인.
- [x] [PR #29](https://github.com/yakisoba0728/PgBase/pull/29) 생성. 원격 CI·병합 결과는 해당 PR에서 확인.

### P1–P5 검증 중 발견한 회귀 수정
- [x] 필드 수정 시 file metadata 삭제를 유발하던 전체 field 재생성 제거; stable ID upsert로 보존.
- [x] 기존 v21 DB의 새 필드 옵션 열 부재를 migration preflight에서 처리.
- [x] PostgreSQL 18의 auth collection RESTRICT 오류를 409 충돌로 매핑.
- [x] 이메일 변경 링크의 action 값과 저장된 메일 템플릿 purpose 분리.
- [x] 인증 설정의 빈 provider/template 목록을 배열로 반환.
- [x] DDL 없는 필드 rename의 SQL 목록을 배열로 반환하고 브라우저 적용까지 재검증.
- [x] 백업 중 exclusive gate 연결 손실 시 작업 중단 검증.
- [x] 백업 URL 연결 대상 override 거절 및 애플리케이션 pool 옵션을 포함한 실제 DB 백업·복원 회귀 검증.

### P1–P5 결과 기록
- 2026-09-09: Go unit/race, 전체 PostgreSQL 18 통합/race·skip 정책·구조 예산, lint 0 issues, vet, UI 450/450, 실제 인증 브라우저 8/8 통과. SDK 패키징·HTTP conformance와 6개 OS/아키텍처 빌드 통과.
- 2026-09-09: 기존 로컬 DB·파일 백업 후 v21→v23 오프라인 업그레이드. `http://127.0.0.1:8090` readiness 정상, doctor `ok: true`, migrations current=latest=23.

## 이전 M2–M6 실행 보드

| 작업 | 담당 | 상태 | 완료 기준 |
| --- | --- | --- | --- |
| M2-B 메일 기반 계정 흐름 | GPT 5.6 Sol / public_auth | 로컬 통합 통과 | 로컬 SMTP, 검증·복구·이메일 변경 토큰의 소비·만료·경쟁 검증 |
| M3 TypeScript SDK·예제 | GPT 5.6 Terra / owner_access | 로컬 패키징·Node·브라우저 통과 | auth/CRUD/취소/ETag/재시도, 로컬 npm pack·브라우저 예제 |
| M4 파일·스토리지 | GPT 5.6 Sol / private_release_review | DB·MinIO·브라우저 통과 | 파일 수명주기·로컬/S3·정리·권한·실제 로컬 스토리지 검사 |
| M5 outbox·realtime | Root | DB/HTTP·실패 경로 통과 | commit 순서 재생, fencing worker, 사용자별 SSE·폐기 반영 |
| M2-C 정책/Auth/OAuth/MFA | Root + Sol/Terra | DB·HTTP·관리 UI 통과 | 기능별 실제 API·관계 권한·로컬 mock provider/OTP 검사 |
| M6 확장·운영 로컬 검증 | Root + Sol | 로컬 검증 통과 | View/batch/index/hooks/cron·로컬 부하/복구·플랫폼 빌드 |

## M0 — 기준점과 작업 관리

- [x] PocketBase v0.40.3 및 pgBase v0.1.0 기준 기능 비교.
- [x] 실제 main 커밋 `3677a6b182b440a158e96d10482b1552030c18f5` 확인.
- [x] 구현 전용 `codex/public-identity-owner-access` 브랜치 생성.
- [x] Sol/Terra 담당 파일 경계와 공통 인터페이스 확정.
- [x] 상세 TODO와 현재 실행 보드 생성.
- [x] 비교 보고서와 이번 구현 기록을 [PR #26](https://github.com/yakisoba0728/PgBase/pull/26)에 포함.

## M1 — 일반 사용자 + 소유자 전용 데이터 API (현재 구현)

첫 단위의 범위: 프로젝트 단위 사용자, 이메일/암호 인증, 만료·폐기 가능한 Bearer 세션, 관리자 opt-in 컬렉션의 소유자 전용 레코드. PocketBase Auth 컬렉션 전체와 임의 규칙 언어를 완료했다고 표시하지 않는다. 관계의 모든 경로에 권한이 적용되기 전까지 일반 사용자에게 관계를 가진/참조되는 컬렉션은 차단한다.

### A. 사용자 저장소와 인증 — Sol

- [x] 추가 전용 migration 10에 users 테이블 정의.
- [x] normalized email 고유성 및 active 상태.
- [x] 기존 Argon2id 구현을 통한 암호 저장/검증.
- [x] 이메일·암호 길이 및 입력 정규화 검증.
- [x] 충분한 난수의 불투명 Bearer 토큰 발급.
- [x] 세션 토큰 원문 대신 keyed hash 저장.
- [x] 세션 만료·폐기·비활성 사용자 검사.
- [x] 프로젝트 DB에 공유되는 가입/로그인 rate limit.
- [x] 비싼 암호 연산에 동시 작업 제한.
- [x] 가입 중복 경쟁의 고유 제약 처리.
- [x] 없는 사용자와 잘못된 암호의 일관된 인증 실패 응답.
- [x] 회원가입 서비스 및 새 세션 반환.
- [x] 로그인 서비스 및 새 세션 반환.
- [x] 토큰 인증 및 자기 세션 조회.
- [x] 로그아웃 후 기존 토큰 재사용 거부.
- [x] 인증 입력/오류/세션 단위 테스트.
- [x] 실제 PostgreSQL 사용자·세션·경쟁·폐기 통합 테스트.

### B. 소유자 규칙과 레코드 무결성 — Terra

- [x] identity에 일반 사용자 종류 추가, 관리자 종류와 구분.
- [x] migration 11에 컬렉션 접근 설정 추가, 설정 부재는 차단.
- [x] 레코드 소유자 저장과 외래키·삭제 정리 계약.
- [x] 기존 audit/idempotency actor 종류 제약의 추가 migration.
- [x] 기존 migration 1–9 내용·체크섬 그대로 보존.
- [x] 관리자 전용 컬렉션 접근 설정 조회/변경 서비스.
- [x] 정책 변경과 데이터 변경의 트랜잭션 경계 검증.
- [x] 생성 시 서버가 인증 사용자로 소유자 설정.
- [x] 클라이언트가 소유자를 지정/위조/변경할 수 없음.
- [x] 데이터·revision·audit·outbox·소유자 동시 commit/rollback.
- [x] 목록에 소유자 조건을 SQL LIMIT 이전에 적용.
- [x] 필터·정렬·페이지네이션과 소유권 동시 적용.
- [x] 다른 사용자의 커서 재사용 거부.
- [x] 다른 사용자의 단건 조회에 존재 여부·ETag 노출 방지.
- [x] 다른 사용자의 update/delete 차단.
- [x] 기존 관리자 생성 레코드는 일반 사용자에게 노출하지 않음.
- [x] idempotency replay에도 현재 사용자·정책·소유권 적용.
- [x] 비활성 사용자/정책 차단이 실제 읽기·쓰기 경계에 적용됨.
- [x] 일반 사용자의 outgoing/incoming relation 컬렉션 차단.
- [x] 관계 추가/삭제와 사용자 접근의 동시성 경계 검증 (공유/배타 schema lock 경로 독립 검토).
- [x] 기존 관리자 CRUD·관계 기능 회귀 없음.
- [x] 실제 DB의 A/B 사용자 격리·rollback·재시도 통합 테스트.

### C. HTTP와 실행 설정 — Root

- [x] 명시적 opt-in 회원가입 설정, 기본값 off.
- [x] `POST /api/v1/auth/register`.
- [x] `POST /api/v1/auth/login`.
- [x] `GET /api/v1/auth/session`.
- [x] `POST /api/v1/auth/logout`.
- [x] Authorization Bearer 전용 사용자 인증 경계.
- [x] 기존 관리자 쿠키·CSRF·same-origin 계약 유지.
- [x] `/api/v1/user/collections/{collection}/records` 목록/생성.
- [x] 같은 경로의 `/{id}` 단건 조회/수정/삭제.
- [x] 관리자 전용 컬렉션 사용자 접근 설정 GET/PUT.
- [x] 인증 응답에 cache-control no-store 적용.
- [x] 토큰·암호를 오류/로그에 포함하지 않음.
- [x] CORS에 공개 Bearer 요청 및 필요한 응답 헤더 계약 반영.
- [x] 신뢰 프록시를 따르는 client address로 rate limit 입력.
- [x] 등록 경로 수·body/response/deadline budget 갱신.
- [x] 앱에서 서비스 구성·설정 전달.
- [x] 인증 미구성/가입 off/잘못된 토큰의 HTTP 단위 테스트.
- [x] 실제 HTTP 사용자 두 명의 가입→로그인→CRUD→로그아웃 검증.
- [x] administrator 사용자 API 혼동 및 권한 상승 거부 검증.

### D. 마이그레이션·호환성 — Root + Sol 검토

- [x] migration 10/11 임베드 및 manifest 검사 갱신.
- [x] 새 DB bootstrap에서 모든 새 테이블 생성.
- [x] v0.1.0 DB 9→현재 버전 upgrade: 기존 실제 레코드·revision·audit·outbox·idempotency 보존.
- [x] upgrade 재실행이 데이터/소유권/사용자를 변경하지 않음.
- [x] 과거 migration 체크섬 회귀 검사.
- [x] 현재 바이너리의 필수 migration floor 명시.
- [x] 고정된 과거 release phase artifact의 기존 계약 유지.
- [x] stop/migrate/start 등 실제 지원 upgrade 절차 기록.
- [x] 새 사용자·세션·소유권 데이터의 backup/restore 포함 확인.

### 독립 검토 후 추가된 수정

- [x] DB 오류를 권한 거부로 잘못 변환하지 않도록 오류 분류 수정 및 회귀 테스트 (Terra).
- [x] 네트워크 주소 회전에 대비한 계정 단위 로그인 제한과 실제 DB 테스트 (Sol).
- [x] 정책 비활성화·계정 비활성화와 진행 중 쓰기의 잠금 순서 검증 (Terra).

### E. 검증과 인수인계

- [x] gofmt 및 diff 검사, pinned golangci-lint 0 issues, go vet.
- [x] 관련 Go 단위 테스트.
- [x] 인증·owner 실제 PostgreSQL 통합 테스트.
- [x] 전체 Go unit/race 및 skip 정책.
- [x] 전체 PostgreSQL integration/race 회귀 및 skip 정책 (PG17).
- [x] 기존 UI typecheck/test/build: 24개 파일 402개 테스트, embedded dist 일치.
- [x] 새 공개 사용자 HTTP end-to-end 계약 검증.
- [x] 독립 Sol/Terra 검토 및 실제 문제 수정.
- [x] README의 pre-v0.1 설명을 실제 상태와 맞춤.
- [x] 공개 사용자 quickstart: 가입 설정→컬렉션 허용→두 사용자 CRUD.
- [x] 이번 단위의 제한과 미구현 기능 명시.
- [x] [PR #26 생성](https://github.com/yakisoba0728/PgBase/pull/26).
- 최신 커밋의 CI 결과는 [PR checks](https://github.com/yakisoba0728/PgBase/pull/26/checks)에서 추적한다.
- 병합 여부와 최종 main 커밋은 [PR #26 타임라인](https://github.com/yakisoba0728/PgBase/pull/26)에서 추적한다. 검증 중인 커밋 자체에 병합 완료를 미리 기록하지 않는다.
- [x] 이번 PR은 소스 구현만 반영. 배포 버전은 v0.1.0 유지, 후속 릴리스는 별도 작업이며 기존 태그 변경 금지.

## M2-A — 사용자 관리와 암호 변경 (현재 구현 단위)

기존 project users 모델을 확장한다. 메일 기반 복구와 OAuth, Auth 컬렉션 전체는 이 단위에 포함하지 않는다. 비활성화는 기존 세션을 전부 폐기하고, 재활성화는 새 로그인을 요구한다. 암호 변경은 현재 암호 확인 후 현재 세션을 포함한 모든 세션을 원자적으로 폐기한다.

### 저장소·동시성 — Sol

- [x] 기존 users/user_sessions 스키마 재사용, 불필요한 migration 없음.
- [x] UUID 순서의 bounded keyset 사용자 목록 (최대 100명).
- [x] 사용자 목록에 암호 해시·세션 토큰·내부 제한 상태 미포함.
- [x] 관리자 사용자 활성화/비활성화 서비스.
- [x] 비활성화와 모든 세션 폐기를 같은 transaction에서 처리.
- [x] 재활성화 후 과거 세션이 되살아나지 않음.
- [x] 사용자 전체 세션 폐기 및 실제 폐기 수 반환.
- [x] 현재 암호 확인과 새 암호 입력 검증.
- [x] 암호 변경의 연산량·실패 시도 제한.
- [x] 잠금 전 암호 연산, 잠금 후 기존 hash/active 재검증.
- [x] 암호 변경과 모든 세션 폐기의 원자성.
- [x] 로그인/비활성화/폐기/암호 변경의 경쟁 조건 DB 검사.
- [x] 잘못된 현재 암호가 기존 암호·세션을 변경하지 않음.

### HTTP·UI — Root + Terra

- [x] GET /api/admin/users?after=UUID&limit=50.
- [x] PATCH /api/admin/users/{id} {active:boolean}.
- [x] POST /api/admin/users/{id}/revoke-sessions.
- [x] POST /api/v1/auth/change-password {currentPassword,newPassword}.
- [x] 관리자 전용 읽기·CSRF mutation과 사용자 Bearer 경계.
- [x] 잘못된 UUID·중복/알 수 없는 query·빈 active·과대 body 거부.
- [x] 내부 오류 비노출, 400/401/404/429/503 계약.
- [x] /admin/users 경로와 내비게이션.
- [x] 목록·빈 상태·페이지 이동·조회 실패 재시도.
- [x] 활성화/비활성화·전체 세션 폐기 UI.
- [x] 요청 중 중복 조작 방지, 실패 후 재시도 가능.
- [x] 세션 변경·페이지 이탈 후 늦은 응답 무효화.
- [x] 컬렉션 owner-access 설정 화면과 관계 제한 설명.
- [x] HTTP adapter·메모리 backend 계약과 UI 상호작용 검사.

### 검증·완료 — Root + 독립 Sol

- [x] 실제 HTTP의 관리자 사용자 관리·계정 수명주기 검사.
- [x] 기존 사용자 데이터 소유권과 관리자 기능 회귀 없음.
- [x] 단위/race·PostgreSQL integration 및 skip 정책.
- [x] pinned lint·vet·gofmt·구조 제한의 실제 증가량 기록.
- [x] UI typecheck·test·build 및 embedded dist 갱신/재현.
- [x] 독립 검토, 발견된 실제 문제 수정 후 재검증.
- [x] API·관리 UI·암호 변경/다시 로그인 사용법 작성.
- [x] [PR #27 생성](https://github.com/yakisoba0728/PgBase/pull/27).
- M2-A 최신 CI는 [PR checks](https://github.com/yakisoba0728/PgBase/pull/27/checks), 병합 및 main 커밋은 [PR 타임라인](https://github.com/yakisoba0728/PgBase/pull/27)을 기준으로 추적한다.
- M2-A는 소스 구현이며 새 릴리스는 아직 발행하지 않았다.

## M2 — 사용자 수명주기와 유연한 권한

- [x] Auth 컬렉션 모델과 기존 프로젝트 사용자 업그레이드 설계.
- [x] 관리자 UI에서 사용자 목록·비활성화·세션 폐기 (PR #27 병합·검증 완료).
- [x] 암호 변경과 기존 세션 폐기 정책 (PR #27 병합·검증 완료).
- [x] SMTP/메일 어댑터와 테스트 전송.
- [x] 이메일 검증 토큰·만료·일회성 소비.
- [x] 비밀번호 재설정 요청·완료·재사용 거부.
- [x] 이메일 변경과 계정 재인증.
- [x] OAuth2 provider 계약 및 state/PKCE 테스트.
- [x] OTP와 MFA 흐름.
- [x] 타입 있는 CRUD 규칙 AST 및 검증기.
- [x] list/view/create/update/delete 규칙별 저장·평가.
- [x] 익명/인증 사용자/서비스 계정 구분.
- [x] 관계 ID·expand·역방향·filter·sort의 모든 hop 권한.
- [x] 관계 삭제 전파의 대상별 변경 권한.
- [x] 규칙 변경 후 커서·캐시·구독의 재검증.
- [x] 정책 편집 UI·미리보기·오류 복구.
- [x] 서비스 토큰 scope·회수·감사 기록.

## M3 — API 계약과 TypeScript SDK

- [x] 공개 API 경로/입력/오류/상태 코드 문서.
- [x] 버전 호환성 및 폐기 정책.
- [x] OpenAPI 생성 경로 검토·결정.
- [x] SDK auth store 및 토큰 lifecycle.
- [x] SDK list/get/create/update/delete.
- [x] 커서 순회·요청 취소·ETag·idempotency 지원.
- [x] 브라우저/Node 환경의 conformance 테스트.
- [x] SDK 로컬 버전·타입·exports·실제 npm pack 및 별도 프로젝트 설치.
- [ ] SDK 공개 npm 릴리스 (외부 배포 보류).
- [x] 작은 예제 앱에서 회원가입과 개인 데이터 관리.
- [x] Dart SDK 수요 및 구현 범위 검토.

## M4 — 파일과 스토리지

- [x] file 필드와 blob 메타데이터 모델.
- [x] 업로드 대기/완료/record commit/삭제 상태 머신.
- [x] 로컬·S3-compatible storage 어댑터.
- [x] 크기·유형·파일명·checksum 검증.
- [x] 사용자 규칙과 결합한 업로드/읽기/삭제.
- [x] 보호 파일 URL·만료 토큰.
- [x] 썸네일 작업과 변환 자원 제한.
- [x] 레코드 실패 시 미참조 blob 정리.
- [x] 재시도·장애 중 파일 정합성 테스트.
- [x] 파일 포함 백업/복원 검증과 관리자 UI.

## M5 — Outbox와 realtime

- [x] outbox claim/lease/fencing 및 병렬 worker.
- [x] retry/backoff/dead-letter 및 운영 진단.
- [x] subscriber·checkpoint·replay 계약.
- [x] SSE 구독/해지와 연결 자원 제한.
- [x] 구독·전달 시 사용자 및 record 규칙 검사.
- [x] 정책 변경/로그아웃/비활성화 즉시 반영 계약.
- [x] 여러 인스턴스의 전달·중복·재연결 테스트.
- [x] 연결 수·쓰기 제한·disconnect·DB 실패 주입·재시도 검증.
- [ ] 실제 여러 호스트 간 network partition 검증 (외부 환경 대기).
- [x] SDK subscribe/unsubscribe와 예제 앱.

## M6 — 나머지 제품 경험과 운영

- [x] View 컬렉션과 읽기 전용 query 모델.
- [x] batch CRUD의 원자성·권한·크기 제한.
- [x] 복합 인덱스와 고급 필드·쿼리 요구 검토.
- [x] 공개 Go event hook과 transaction 범위.
- [x] JavaScript 확장 runtime 여부·자원 경계 결정.
- [x] 사용자 정의 cron과 여러 인스턴스 실행 계약.
- [x] 관리자 UI 기존·신규 주요 흐름 브라우저 회귀, label·모바일 overflow 검사 (전체 접근성 인증은 아님).
- [x] 6개 OS/architecture 로컬 사전 빌드와 checksum 생성.
- [ ] 플랫폼별 실제 실행·서명·공개 배포 (외부 배포 보류).
- [ ] 실제 staging TLS/proxy/외부 PostgreSQL 배포.
- [ ] 목표 트래픽 기준 load/soak/spike 및 SLO 측정.
- [x] 로컬 WAL checkpoint PITR·파일 포함 논리 복원 및 관측 시간 기록.
- [ ] 운영 RPO/RTO·외부 스토리지 시점 복구 보증 (운영 환경 대기).
- [x] 기능 비교표를 구현·검증 완료 시점에 갱신.

## 전체 로컬 작업의 완료 기준과 세부 추적

- [x] 사용자 범위 확인: M2–M6 중 로컬에서 개발·검증 가능한 전체 기능.
- [x] 클라우드/도메인 미정 확인. 실제 스테이징·외부 계정 연결·공개 배포는 완료로 표시하지 않고 별도 대기.
- [x] 병합 기준 main `09d6b7d`와 구현 브랜치 생성.
- [x] migration 12–21의 순서·체크섬·이전 DB upgrade 검증.
- [x] feature 설정의 기본 off/구성 검증·readiness·shutdown 연결.
- [x] 각 module의 API·SDK·UI 연결 후 실제 앱 end-to-end 검사.
- [x] docs/API 계약·OpenAPI·예제·README를 실제 구현과 일치시킴.
- [x] 최종 변경 범위의 독립 검토·unit/race·DB·UI·SDK·로컬서비스 통합 검사.
- [x] [M2–M6 로컬 구현 PR #28 생성](https://github.com/yakisoba0728/PgBase/pull/28), 소스와 릴리스 구분.
- 최신 커밋 CI는 [PR checks](https://github.com/yakisoba0728/PgBase/pull/28/checks), 실제 병합 여부·main 커밋은 [PR 타임라인](https://github.com/yakisoba0728/PgBase/pull/28)을 기준으로 추적한다. 검증 전 병합 성공을 미리 기록하지 않는다.

### M2-B 계정 메일 상세

- [x] SMTP sender, timeout, TLS/STARTTLS/auth, 로컬 테스트용 명시적 plaintext 설정.
- [x] local SMTP sink에서 실제 메일 수신·링크·헤더 검사.
- [x] verified 저장과 사용자 DTO·UI 표시.
- [x] 용도별 HMAC 토큰, 원문 미저장, 만료·일회성 소비.
- [x] 사용자 존재를 숨기는 복구 요청 응답·요청 제한.
- [x] 복구 완료의 암호 변경·세션 폐기·기존 credential 비교.
- [x] 이메일 검증 요청·완료 API와 사용자 표시.
- [x] 이메일 변경의 현재 암호·고유성·재인증·확인 링크 처리.
- [x] 메일 전달 실패·DB 실패·동시 확인·토큰 재사용 회귀.
- [x] 로컬 메일 inbox·실제 SMTP 전송 검사·관리 UI verified 표시·공개 예제 복구 화면.

### M2-C 인증·권한 상세

- [x] Auth 컬렉션 설정 모델과 기존 users의 기본 컬렉션 이관.
- [x] OAuth provider 설정/시작/callback, state·PKCE·issuer/sub binding.
- [x] 로컬 mock OAuth provider의 성공·취소·재생·계정 충돌 검사.
- [x] OTP/TOTP 등록·확인·해제·복구 코드·MFA 단계 세션.
- [x] 제한된 규칙 AST parser/type checker/SQL binding과 모호한 규칙 거부.
- [x] action별 규칙 저장·수정 및 schema/policy revision 캐시 무효화.
- [x] relation filter/sort/expand/reverse·변경·삭제 전파 전체 권한 검사.
- [x] 익명·사용자·서비스 계정 scope, service token 발급·폐기·검증.
- [x] 정책 편집기·미리보기와 허용/거부 실제 데이터 행렬 테스트.

### M3 SDK·예제 상세

- [x] ESM·타입 선언·exports·Node/브라우저 fetch 주입. (SDK 로컬 HTTP 테스트 통과; 실제 PgBase 통합 별도 추적)
- [x] 메모리/명시적 브라우저 auth store, session generation fencing. (SDK 로컬 HTTP 테스트 통과; 실제 PgBase 통합 별도 추적)
- [x] auth/CRUD 에러·page iterator·AbortSignal·ETag·idempotency 재시도. (SDK 로컬 HTTP 테스트 통과; 실제 PgBase 통합 별도 추적)
- [x] 메일 action·파일 업로드/다운로드·SSE subscribe 래퍼 연결.
- [x] Node HTTP server·실제 브라우저 conformance와 로컬 pack 설치 검사.
- [x] 로컬 예제: 가입/로그인/암호·이메일/개인 데이터/파일/구독.
- [x] SDK 버전 호환성·OpenAPI 경로 검증·Dart 범위 결정 기록.

### M4 파일 상세

- [x] file 필드 스키마·검증·관리 UI 편집/업로드 연결.
- [x] record/field별 metadata pending→ready→delete 수명주기.
- [x] opaque object key·크기/MIME/파일명/SHA256 검증·streaming.
- [x] 로컬 스토리지 traversal·경합·중단·오류 정리 검사.
- [x] S3-compatible adapter와 로컬 MinIO 실제 업로드/읽기/삭제 검사.
- [x] 현재 읽기/쓰기 권한 검사와 사용자/파일/만료 바인딩 다운로드 토큰.
- [x] JPEG/PNG 썸네일 디코드 크기 제한·원본과 동일한 접근 권한.
- [x] 삭제 queue의 lease/retry·DB rollback·dangling object 정리.
- [x] record 삭제/컬렉션 삭제 후 파일정리·파일 포함 백업/복원.

### M5 realtime 상세

- [x] outbox event의 owner snapshot, 삭제 이벤트 권한 유지.
- [x] durable delivery sequence의 commit 순서와 event 중복 방지.
- [x] worker claim/lease fencing, retry/backoff/dead-letter/운영 상태.
- [x] 구독 요청·매 전달의 사용자 session/active/policy 확인.
- [x] SSE event ID·Last-Event-ID 재생·heartbeat·연결/쓰기 제한.
- [x] 느린 소비자·disconnect·재연결·DB 오류와 다중 인스턴스 테스트.
- [x] worker/subscription shutdown·SDK/예제 subscription 연결.

### M6 로컬 상세

- [x] View 컬렉션의 선언·읽기 전용 query·관리 UI·권한.
- [x] batch operation 입력·크기 제한·단일 transaction·실패 rollback.
- [x] 복합 인덱스 설계·plan/apply·삭제·조회 계획 검사.
- [x] Go hooks의 등록·before/after/commit·실패 처리·명확한 tx 범위.
- [x] JavaScript runtime 포함 여부와 비용/자원 제한 결정.
- [x] cron 등록·스케줄·DB lease·중복 실행/실패 재시도 검사.
- [x] Linux/macOS/Windows·amd64/arm64 로컬 cross-build 및 checksum.
- [x] 로컬 load/soak/spike 측정과 환경·처리량·실패율 보고 (운영 SLO로 주장하지 않음).
- [x] 로컬 WAL archive/PITR marker 복구·파일 포함 논리 복구·측정 한계 기록 (운영 RPO/RTO·S3 시점 복구는 외부 검증 대기).
- [x] 관리자 UI 접근성·페이지 탐색·실제 브라우저 회귀.
- [x] 구현된 기능과 아직 외부 검증되지 않은 부분의 비교표 갱신.


## 변경 기록

- 2026-09-08: M1 범위·인터페이스·담당 확정. Sol 인증, Terra owner 정책, Root HTTP 연결, Sol 독립 호환성 검토 시작. M2–M6는 대기 목록이며 완료로 계산하지 않음.

- 2026-09-08: Sol 사용자 인증 단위/DB 검사, Terra 소유권·재시도·롤백·관계 차단 DB 검사, Sol 독립 HTTP 두 사용자 테스트(race) 통과. Root 인증·CORS·배선 HTTP 단위 검증 및 migration floor 갱신. 독립 검토 두 건과 잠금 동시성 추가 테스트 진행 중.

- 2026-09-08: 검토 수정 두 건 및 정책/계정 잠금 DB 테스트 완료. 공개 사용자·소유권을 포함한 실제 dump/restore가 PostgreSQL 16/17/18에서 통과. 사용자 API 문서와 9→11 유지보수 업그레이드 절차 작성. 전체 회귀 검사 시작.

- 2026-09-08: 전체 Go unit/race 및 skip 정책 통과. UI typecheck, 402개 테스트, build 및 embedded dist 일치. 최종 Sol 문서 검토에서 샘플 idempotency key 길이와 선택적 If-Match 설명을 바로잡음.

- 2026-09-08: 전체 PostgreSQL integration/race·skip 정책 통과. Sol의 populated v9→v11 업그레이드 및 반복 실행 보존 테스트 통과. 미사용 쿼리 wrapper를 제거하고 기존 설명 주석을 복구해 최종 코드 크기 재측정.

- 2026-09-08: 최종 쿼리·레코드·업그레이드·크기 정책 race 재검사와 pinned lint 통과. 실제 Cosign 검증 테스트 통과. 복원 후 원래 Bearer 세션까지 PG16/17/18에서 확인. PR 준비 완료, 배포된 v0.1.0은 유지.

- 2026-09-08: PR #26 생성, Backend/Frontend/Security CI 시작. 위 GitHub 링크가 최신 커밋별 CI·병합 상태의 기준이다. 구현·로컬 검증 완료와 배포 릴리스 완료는 구분한다.

- 2026-09-08: M1 PR #26 병합 및 main 확인 완료. 후속 M2-A 브랜치 생성. Sol 사용자 수명주기, Terra 관리자 UI, Sol 독립 HTTP 테스트, Root API 연결 작업 시작. 세부 TODO 35개 추가.

- 2026-09-08: M2-A 서비스·API·UI 구현 연결. 실제 HTTP 수명주기, 2개 서비스 간 암호 변경 제한, 로그인/비활성화·암호 변경 경쟁 조건 검사 통과. UI 다중 행 pending 잠금, incoming 관계 판정, 정책 조회 재시도 및 늦은 응답 무효화를 검토·보완하고 테스트 추가.

- 2026-09-08: M2-A 전체 Go unit/race·PG17 integration/race 및 skip 정책, pinned lint/vet 통과. UI 25개 파일 416개 테스트·typecheck·build 통과. 실제 브라우저에서 비활성화→재활성화·세션 폐기를 확인하고, 데스크톱 버튼 가시성 및 모바일 페이지 overflow를 검증. 최종 런타임 증가 +656줄은 설계 문서와 제한에 반영.

- 2026-09-08: M2-A PR #27 생성. Backend/Frontend/Security CI 실행 시작. 구현·로컬 검사와 CI·병합·배포 상태를 분리해 추적.

- 2026-09-08: 사용자 지시로 M2–M6 전체를 진행하되 로컬 개발 가능한 범위로 확정. 단계별 세부 TODO와 완료 기준을 확장. Sol 계정 메일, Terra SDK/예제, Sol 파일/스토리지, Root realtime 병렬 착수.

- 2026-09-08: M2-B 메일 서비스·단일 사용/경합 PostgreSQL 검사 통과, HTTP 연결 진행. M3 기본 SDK 및 메일/SSE 래퍼 로컬 HTTP 검사·pack 통과. M5 delivery/owner snapshot/SSE 구현 후 DB·HTTP 검증 진행. Sol OAuth/MFA, Terra cron 후속 착수.

- 2026-09-08: 실제 앱+로컬 SMTP 가입→이메일검증→암호복구→이메일변경→기존세션폐기 HTTP 검사 통과. 실제 SSE owner 격리·삭제이벤트 재연결·로그아웃종료 통과 (`pgbase` 통합 테스트). MFA/OAuth 서비스 PG 검증 통과, Auth collection 후속 구현 중. Batch 원자성 및 파일 API 연결 검증 진행.

- 2026-09-08: M2 typed rule CRUD/페이지커서갱신/쓰기rollback 실제 HTTP 통과; M5 현재rules 기반cross-owner 이벤트 및 삭제snapshot·규칙폐기 SSE 통과. 서비스토큰scope/폐기·익명거부 HTTP 통과. M4 MinIO와HTTPfile검증 완료 및파일UI집중테스트추가. M6 batch성공/rollback/권한·hook검증 및SDKwrapper 통과. 현재 Auth collections UI, relation 모든hop권한, compoundindex/View 작업 중.

- 2026-09-08: 로컬 개발 스크립트로 PG17/PgBase/SMTP inbox 기동 및 readiness 통과. Auth 컬렉션 관리 UI·규칙 미리보기 UI 및 423개 UI 테스트·typecheck/build 통과. 실제 DB publish 실패 10회→backoff→dead-letter→운영 retry→event ID 보존 검사 통과. 파일 포함 dump/restore 및 6개 플랫폼 빌드 진행.

- 2026-09-08: PG17 파일 포함 dump/restore·인증 다운로드 통과. 6개 OS/architecture 빌드와 checksum 생성 통과. 30초 로컬 HTTP baseline/steady/spike/recovery 42,331건 오류0; p95 2.27–8.52ms. 독립 WAL named checkpoint 복구 성공(대상 이전 marker 유실0, 이후marker 제외, 기동관측1초). 운영보증으로 계산하지 않음.

- 2026-09-09: Auth/OAuth/MFA·관계 모든 hop·서비스 토큰·View/복합 인덱스·파일·SSE 독립 검토 및 회귀 완료. SDK 실제 tarball 설치와 PgBase HTTP conformance 통과. 브라우저에서 기본 fetch 바인딩 오류와 토큰 폼 입력 실패 후 잠김을 발견·수정. UI 431개 테스트 통과. 전체 Go/PG 및 PG16/17/18 복원 최종 재검증 중.

- 2026-09-09: 서비스 토큰 scope의 컬렉션 삭제 방해를 실제 브라우저에서 재현해 guarded schema transaction에서 정리하도록 수정. active/revoked 토큰 HTTP 회귀 및 브라우저 4개 통과. UI 431개, 기존 browser 1+7개, Go race·PG17/MinIO 기능 및 skip 정책, PG16/17/18 파일 포함 복원 통과. 최종 구조 제한 재측정·회귀 통과, PR 준비.

- 2026-09-09: PR #28 생성, 최신 HEAD에 Backend/Frontend/Security CI 실행. 로컬 기능과 검증 항목은 완료했으며 외부 인프라·공개 릴리스 항목만 대기로 유지한다. 병합 결과는 PR 타임라인에서 확인한다.

- 2026-09-09: 첫 CI의 gofmt 검사에서 View 파일 포맷 누락 발견·수정. 형식 변경 3줄을 구조 측정에 반영하고 최신 HEAD 검증 재실행.

## PocketBase 전체 소스 대조 감사 (2026-09-09)

- [x] 공식 전체 Git 저장소 다운로드, v0.40.3/commit 고정.
- [x] 추적 파일 923개 전수 inventory와 체크섬·담당 작성.
- [x] GPT 5.6 Sol API/core 병렬 독해 시작.
- [x] API/forms/mails 전체 소스·테스트 90개 독해와 PgBase 대조.
- [x] core/migrations 전체 소스·테스트 독해와 PgBase 대조.
- [x] core/field* 32개 구현·테스트 전체 본문 판독과 필드 계약 비교.
- [x] 관리자 UI 전체 handwritten source 독해와 화면별 대조.
- [x] UI handwritten CSS 35개 전체 판독 및 icon vendor 연계 확인.
- [x] Upstream cron 실패·relation picker 로딩 상태를 원본 함수+stub로 좁게 재현.
- [x] tools 전체 소스·테스트 독해와 공통 동작 대조.
- [x] archive/cron/dbutils 16개 전체 판독 및 PgBase 계약 비교.
- [x] plugins/CLI/build/release/test fixtures 및 생성물 검토 (82 본문 / 22 artifact / 3 역사 참고 문서).
- [x] 파일별 ledger 누락·중복·정확한 읽기 범위 교차 확인.
- [x] 기존 65–70% 추정 재평가, 실제 공백·우선순위·완료 기준 보고.

진행 기록: [전체 소스 감사](docs/pocketbase-source-audit-2026-09-09/README.md). 본문 734개·생성물/자산 186개·역사 참고 3개를 구분해 검토 완료. 미판독 소스 0개이며, inventory 등록만으로 본문 판독 처리하지 않았다.


### 소스 감사에서 도출한 후속 구현 후보 — 현재 구현과 동기화

현재 소스 감사의 완료 표시와 아래 제품 구현 완료를 구분한다. 계약 차이와 상세 완료 기준은 [통합 평가](docs/pocketbase-source-audit-2026-09-09/assessment.md)에 기록한다.

- [x] PB-A01: Required·빈 값·숫자 정밀도·ID·relation 중복에 대한 PgBase 호환 정책과 wire 예제 확정. P1 문서·wire 검증 완료.
- [x] PB-A02: 필드 ID 기반 데이터 보존 rename 계획/API/UI 및 관계·view·index 보존 검증. P1 완료.
- [x] PB-A03: text 길이/pattern, email/URL 형식·도메인, select 선택 수 검증을 스키마/API/UI/SDK에 연결. P1 완료.
- [x] PB-A04: editor·geoPoint·사용자 autodate 지원 범위와 전용 입력/조회 UI는 P8에서 완료. N4의 `:lower` filter/rule과 geoPoint `.lon`/`.lat` 조회 보완도 로컬 통합·인수 완료.
- [x] PB-A05: Hidden/Presentable/Help 등 필드 메타데이터의 API 응답·relation 표시 계약 구현.
- [x] PB-A06: 레코드 bulk 선택·삭제·export·duplicate와 부분 실패/동시 갱신 표시. P2 완료.
- [x] PB-A07: relation picker의 즉석 생성·수정 및 column preference·draft 복구. P2 완료.
- [x] PB-A08: auth realm별 OAuth provider/정책/메일 템플릿 설정과 로컬 inbox 시험. P3/P6-B 완료.
- [x] PB-A09: 이메일 OTP·impersonation 필요 여부 및 세션/토큰 무효화 계약 확정 후 구현.
- [x] PB-A10: 파일 필드별 MIME·크기·개수·Required 설정과 기존 첨부 처리 정책. P6-A 완료.
- [x] PB-A11: 여러 썸네일 crop/fit·크기 설정 및 권한·캐시·저장 비용 검증. P6-A 완료.
- [x] PB-A12: 관리자 로그 검색/통계와 cron 목록·실행 상태·수동 실행 API/UI. P6-E/F 완료.
- [x] PB-A13: 로컬 PostgreSQL+파일 백업 작업 추적·다운로드·복원 UI와 일관성 검증. P6-G 완료.
- [x] PB-A14: SMTP/S3 설정 시험 및 schema import/export 검토·적용 UI. SMTP는 P3/P6-B, schema 입출력은 P7, 파일·백업 S3 설정/API/UI/SDK와 실제 MinIO 시험은 P8 로컬 인수 완료.
- [x] PB-A15: 실제 지원 SDK/OpenAPI 기반 collection별 API preview와 실행 가능한 예제 검증. P5 완료.
- [x] PB-A16: Go custom routes·compiled migration authoring은 P7/P8, 세부 request/file/mail hooks와 자동 snapshot/diff/history-sync는 N5/N1 로컬 통합·인수까지 완료했다.
- [ ] PB-A16 후속: JSVM·Dart SDK는 N6/N7로 분리하며 아직 구현하지 않았다.

- 2026-09-09 P8: file migration 시작 응답 유실을 client operation UUID로 복구하고 동일 입력 재시도/다른 입력 충돌을 검증. 실제 파일 local→S3→local 왕복 다운로드 및 유실 응답 브라우저 통과. File storage 전체 PG18+MinIO integration/race와 SDK tests 통과, skip 없음 (`phase4-storage-final.log`). 사용자 인증 옵션과 독립된 관리자 저장소 routes 회귀 추가.

- 2026-09-09 P8 최종 인수: UI 전체 489개, 실제 관리자 브라우저 19개, SDK main client·관리자 7개 및 npm pack 통과. 정적 분석 8항목을 수정해 lint 0 issues 및 vet 통과. 독립 Spec 리뷰에서 concurrent startup의 pending 백업 정리 경쟁을 재현·보강하고 메타데이터/소유 객체 검증도 보강 중. 전체 integration에서 발견한 이전 크기 상한·migration rewind fixture를 현재 계약에 맞춰 수정·재검증한 뒤 최종 전체 회귀·PR·머지를 진행한다.

- 2026-09-09 P8 검토 마무리: startup recovery의 pending 정리 경쟁, S3 소유 metadata 검증과 probe 잔여 객체 문제를 수정했다. 사용자 migration/schema Apply 경쟁 회귀 추가 및 전체 usermigrate PG18/race 통과. 백업 After 수신과 worker 종료를 혼동하던 테스트를 동기화하고 race 100회 통과. 변경 집합의 100개 문서 링크와 구조 예산/OpenAPI·migration checksum도 검증했다.

- 2026-09-09 P8 최종 로컬 인수 완료: Go unit/race, PG18+MinIO integration/race와 백업 테스트 수정 후 패키지 재검사·skip 정책, UI 489개·typecheck/build, 관리자 브라우저 19개 및 최신 P8 3개, SDK live conformance/pack, lint/vet, 6종 빌드와 migration/OpenAPI/구조 예산 통과. PR·원격 CI·병합·백업 후 설치는 별도로 기록한다.

- [x] P8 [PR #32](https://github.com/yakisoba0728/PgBase/pull/32) 필수 CI 5개 통과 후 squash 병합 (`0d28446`).
- [x] 원본 DB·파일·백업·환경·바이너리 보존 후 로컬 schema v32→v38 적용. doctor/readiness, 관리자 세션, 4종 고급 필드 schema preview, local 파일/백업 저장소 시험, 실제 PG17 managed backup/download 및 manifest/파일 해시 검증 통과.
- 2026-09-09 P8 종료: 구현·검증·PR·병합·로컬 설치를 마쳤다. 설치 제품 커밋은 `0d28446b4003679465103c5fe2d4f1c65364cc79`이며 `.local/verification/phase4-local-*`와 `phase4-live-smoke.json`에 증거를 보존한다. 설치 완료 문서 수정은 제품 코드와 별도의 후속 PR로 반영한다. N1–N9는 미완료 작업으로 유지한다.
