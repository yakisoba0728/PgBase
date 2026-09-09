# 다음 작업과 완료 기준

기준: 2026-09-10. N1–N5는 구현·독립 검토·전체 로컬 인수 및 [PR #34](https://github.com/yakisoba0728/PgBase/pull/34) 필수 CI5/5·merge `2939f15`를 완료했고 기존 PG17.9/schema38→40 설치·인수도03:24 KST 마쳤다. N6/N7/N9 로컬 구현·핵심 인수는 끝났고 N8과 전체 G5는 일시정지 checkpoint 이후 사용자 요청으로 재개했다. [완료 이력과 현재 상태](project-status.md), [실행 체크리스트](../TODO.md), [PocketBase 재평가](pocketbase-current-reassessment.md)를 함께 사용한다. 아래 ID는 실행 순서이며 기존 M/P 단계 번호를 바꾸지 않는다. 기간·진행률은 추정하지 않는다.

## 우선순위와 의존성

| 순서 | ID | 작업 | 시작 조건 |
| --- | --- | --- | --- |
| 완료 | N0 | P8 최종 인수·PR·설치 | PR #32 및 로컬 v38 인수 완료 |
| 완료 | N1 | 사용자 migration 자동화 | 구현·N4 option matrix·fresh/upgrade/doctor·전체 race 통과. PR34 병합·schema40 설치 인수 완료 |
| 완료 | N2 | public 정적 사이트·SPA | 구현·실제 browser route/asset/deep-link·전체 race 통과. PR34 병합·schema40 설치 인수 완료 |
| 완료 | N3 | 관리자 CLI 확장 | 구현·PG18 race·OTP/IP/session browser·전체 race 통과. PR34 병합·schema40 설치 인수 완료 |
| 완료 | N4 | 필드 세부 옵션·조회 차이 | 구현·DB/HTTP/UI/SDK/browser·전체 race 통과. PR34 병합·schema40 설치 인수 완료 |
| 완료 | N5 | 세부 lifecycle/request hooks | 구현·독립 review·집중/전체 race/HTTP 통과. PR34 병합·schema40 설치 인수 완료 |
| 로컬 완료·G5 대기 | N6 | JSVM | external Go+JS·PG/file/SMTP/job·same-origin browser·review zero 완료 |
| 로컬 완료·G5 대기 | N7 | Dart SDK | Dart/Flutter external consumer·cross-flow·browser·review zero 완료 |
| **ACTIVE** | N8 | SQL 콘솔 | integration `6bb6dbb`; unintegrated WIP `codex/n8-sql-console@4d773b5`; acceptance open |
| 로컬 완료·G5 대기 | N9 | self-update | private+shared review closure, Darwin fixture·six-build/Windows compile 근거 완료 |

통합 Sol은 `117a1db` 기반 `codex/n6-n9-integration`에서 계획 `133f644`/`8dadf42`를 적용했고 중앙 문서와 공통 seam을 단독 소유한다. N6/N7/N9은 로컬 범위에서 완료됐으나 전체 G5는 실행하지 않았다. N8의 integrated product/UI checkpoint는 pre-pause `6bb6dbb`까지이며 owner WIP `4d773b5`는 clean branch에만 보존하고 통합하지 않았다. 불완전 review checkpoint `9ac351a`는 docs-only로 통합했으며 full PASS가 아니다. 재개 시 R12의 수정된 assertion을 먼저 재실행하고 R1–R4/R8–R10/R12를 독립 review로 닫은 뒤 PG17/18·race·six-build·실제 browser와 G5를 진행한다. live 설치·ports는 검증 대상이 아니다.

N7 인계 갱신: 통합 lead가 03:38 KST 전용 `58091/55441` fixture에서 `tool/live_conformance.dart`의 upload 404를 재현했다. 무인증 file route도 인증 오류가 아닌 generic router 404였고 컨테이너에 `PGBASE_FILES_DRIVER`/`PGBASE_FILES_LOCAL_DIRECTORY`가 없었다. `withFileServices`가 file routes를 등록하지 않은 fixture 설정 누락으로 확정했다. N7 `52e23ce`가 task-owned local storage를 추가한 뒤 실제 user upload/list/token/byte-exact download/delete와 admin upload가 통과했다. SDK·Go server 계약 변경 사유는 없었고 해당 404 검사는 종료했다.

## N0 — 지금 작성한 P8 마무리

- [x] 실제 S3 업로드 파일 백업에서 원본 S3 객체를 삭제한 뒤 새 DB/로컬 파일로 복원하여 정확한 바이트·local/no-secret 설정·원본 객체 미재생성을 확인했다. `TestS3FileSnapshotRestoreNewThenStorageResetPreservesIndependentLocalBytes`, PG18+MinIO/race 통과.
- [x] 복원 대상에서 상속된 file migration·source cleanup·pending upload/deletion 및 backup publication 기록을 제거하고 candidate 시작 전 연결했다. helper와 managed 전환 집중 검증 통과.
- [x] 백업 publish 연결 유실, 중복 ID, 부분 실패, foreign object, 삭제 재시도·retention 및 설정 전환의 최종 조합 검증을 완료한다. 최종 PG18+MinIO/race와 수정된 backup 패키지 재검사가 통과했다.
- [x] 사용자 migration 취소·동시 실행·checksum 변경·Down 실패·제어 ledger 보호를 확인했고 실제 schema Apply 경쟁/캐시 갱신 회귀를 추가했다. 전체 usermigrate PG18/race 통과. 비가역 외부 효과는 신뢰된 Go callback의 제한으로 문서화했다.
- [x] migration 1–32 원본 불변과 신규 33–38 embed/checksum·upgrade 검증을 확정한다. populated v32→v38·doctor 증거는 이미 통과했으므로 관련 변경이 있을 때만 다시 실행한다.
- [x] 최종 변경 집합으로 Go unit/race, PostgreSQL+MinIO integration/race 및 skip 정책, UI unit/typecheck/build, SDK conformance/pack, 실제 브라우저 흐름을 실행한다.
- [x] OpenAPI·lint/vet·생성물 재현·지원 OS/architecture 빌드와 구조 실측을 확인한다. production 파일 800줄 제한을 유지하고 근거 있는 구조 예산·순증가량을 문서화한다.
- [x] 독립 Standards/Spec 검토를 완료했다. 백업 startup recovery 경합, 소유 metadata/객체 검증과 probe 삭제 문제를 수정하고 PG18+MinIO/race 및 최신 P8 브라우저 3/3을 통과했다.
- [x] PR에 구현 범위·검증·제약을 작성하고 required CI 통과 후 병합한다.
- [x] 원본 로컬 DB·파일·실행 파일을 백업하고 새 바이너리/DB를 적용한다. doctor/readiness·관리자 로그인·고급 필드 preview·local 파일/백업 저장소 probe·PG17 백업/download/hash가 통과했고 설치 커밋을 기록했다. 실제 S3 왕복은 격리된 MinIO/브라우저로 검증했다.

완료 기준: 최신 변경 전체의 인수 증거, 병합 커밋, 원본 백업 위치, 실제 설치 버전이 모두 기록되어야 한다. P8 완료가 아래 N1–N9 완료를 의미하지 않는다.

완료 근거: [PR #32](https://github.com/yakisoba0728/PgBase/pull/32), 제품 커밋 `0d28446`, 로컬 PostgreSQL 17/schema v38. 외부 배포·공개 릴리스는 별도다.

## N1 — 자동 migration

- [x] schema 변경을 결정적인 Go migration source로 변환하는 snapshot/diff 규약을 정의했다.
- [x] 필드 ID rename, 관계·View·index 의존 순서와 역방향 변환 가능성을 반영했다.
- [x] 관리자 변경 성공과 소스/이력 기록 실패의 처리·재시도·중복 방지 경계를 정의했다.
- [x] collection snapshot 명령, 적용 history와 source 대조 및 history-sync의 허용 범위를 구현했다.
- [x] 생성 소스에서 자격 증명을 제외하고, 불가능한 Down과 데이터 손실 가능 변경을 명시했다.
- [x] 별도 Go 프로젝트에서 생성·빌드·Up·Down 및 실제 데이터 보존/실패 rollback을 검증했다.

완료 기준: 관리자 변경 또는 snapshot으로 만든 소스가 새 DB에서 재현되고 지원하는 Down이 검증된다. 빈 Go 템플릿이나 schema JSON export만으로 완료 처리하지 않는다.

## N2 — 사용자 정적 사이트와 SPA

- [x] public 디렉터리 옵션과 index fallback을 CLI/환경/Go 설정에 연결했다.
- [x] API·관리자·사용자 route 우선순위, 없는 asset과 SPA 경로의 차이를 정의했다.
- [x] 경로 정규화·symlink·MIME·캐시·HEAD·없는 디렉터리 동작을 구현했다.
- [x] 실제 프런트엔드 빌드의 asset 제공, 깊은 링크 새로고침, API 404 보존을 브라우저로 검증했다.

완료 기준: 별도 정적 서버 없이 사용자 앱을 제공하며 API/관리자 경로와 충돌하지 않는다.

## N3 — 관리자 CLI

- [x] create/reset-password와 공통 서비스를 사용해 upsert/delete를 구현했다. PB update의 암호 변경 의미는 reset-password가 담당한다.
- [x] OTP 및 IP 제한 관리의 PgBase 대응 계약을 확정하고 CLI에 연결했다.
- [x] 사용자 식별, 입력 오류·종료 코드, 암호 입력 방식, 마지막 관리자 처리, 세션 무효화를 일관되게 적용했다.
- [x] 실제 DB에서 idempotent upsert·삭제/갱신·재인증·실패 시 무변경을 검증하고 help/예제를 갱신했다.

## N4 — 필드와 조회의 세부 차이

- [x] 문자열 자동 생성 패턴의 생성 시점·명시 값 우선순위·재시도·충돌 계약을 구현했다.
- [x] email/URL 도메인 차단 목록, allow/deny 동시 설정·정규화·기존 데이터 preflight를 구현했다.
- [x] PB 필드 수정자, `:lower` 필터, geoPoint 탐색의 현재 차이를 분류하고 구현 대상과 유지할 차이를 명시했다.
- [x] 새 옵션을 schema/API/SDK/UI/import/export/rename에 함께 연결했다.
- [x] Required·정밀도·UUID·Argon2id처럼 유지할 PgBase 계약을 호환 예제와 함께 검증했다.

완료 기준: 채택한 세부 기능은 실제 입력/조회/권한/Batch에서 동작하고 미채택 계약은 명시된다. 타입 이름의 존재만으로 PB 필드 완성을 주장하지 않는다.

## N5 — 세부 hooks

- [x] [hook 대조표](lifecycle-parity.md)의 partial 항목마다 이벤트·payload·시점·수정 가능 범위를 정했다.
- [x] CRUD list/view/write 및 Batch/import 전체 요청 hooks를 구현했다.
- [x] 목적별 메일과 파일 token/download/transform hooks를 구현했다.
- [x] PB 범용 model validate/execute/success/error 단계의 PgBase 대응과 지원하지 않을 ORM 범위를 명시했다.
- [x] veto·권한·취소·panic·retry/replay·commit/activation 결과 및 비밀 비노출을 실제 요청에서 검증했다.

완료 기준: 등록 가능한 이름뿐 아니라 실제 API 호출 경로와 최종 결과가 연결된다. 외부 효과의 durable delivery는 outbox 계약과 분리한다.

## N6 — JSVM (로컬 완료, G5 대기)

- [x] 엔진·지원 문법·모듈 로딩·타입 정의·개발 reload와 PB API 호환 범위를 정했다.
- [x] Go API 위에 route/hook/migration/job 및 필요한 DB/mail/file 바인딩을 구현했다.
- [x] 실행 취소·timeout·동시 VM·공유 상태·shutdown·오류 전파·자원 한도를 검증했다.
- [x] 실제 JS 앱으로 CRUD 확장·메일·작업·migration 및 실패/재시도 시나리오를 검증했다.

완료 기준: 실행 파일에 script를 넣어 읽는 수준을 넘어 문서화된 바인딩과 실패 계약을 갖춘다. PB JS API 전체 호환 여부는 별도로 표시한다.

## N7 — Dart SDK (로컬 완료, G5 대기)

- [x] 패키지 구조, auth store, 정확 숫자·UUID·에러·취소 계약을 정의했다.
- [x] auth/CRUD/필터·페이지/ETag·idempotency/Batch/file/SSE 및 필요한 관리 API를 구현했다.
- [x] 실제 PgBase HTTP conformance, reconnect·로그아웃·에러·파일 전송을 검증했다.
- [x] 로컬 Dart/Flutter 예제와 설치 문서를 제공했다. pub.dev 공개 발행은 대기로 둔다.

## N8 — SQL 콘솔 (ACTIVE, 미완료)

통합된 UI/shared/backend 기반은 `6bb6dbb`까지다. 추가 backend candidate는 `codex/n8-sql-console@4d773b5`에 clean WIP로만 보존한다. R1–R4/R8–R12, current PG/race/build, 실제 browser와 final review가 열려 있으므로 아래 항목은 완료 처리하지 않는다. API/UI fixture와 credential file은 생성되지 않았고 N8 PG 컨테이너는 data를 보존한 정지 상태다.

- [ ] 허용 statement·읽기/쓰기·transaction·PgBase 소유 schema 변경 경계를 정한다.
- [ ] 관리자 인가, timeout/cancel·행/본문 상한·실행 로그 및 오류 표시를 구현한다.
- [ ] UI에서 쿼리 실행·결과 탐색·취소·정확 숫자를 지원한다.
- [ ] rollback·lock contention·연결 종료와 schema/record/audit/outbox 불변식 보존을 검증한다.

완료 기준: 현재 SQL View 편집과 독립된 기능으로 구현하고, 직접 SQL이 우회할 수 있는 제품 불변식의 처리 방식을 문서화한다.

## N9 — self-update (로컬 완료, G5 대기)

- [x] 버전/플랫폼 artifact 선택, checksum·서명 검증, 현재 파일 보존과 원자적 교체 계약을 정했다.
- [x] 다운로드 중단·검증 실패·권한 부족·실행 실패 시 복구를 로컬 fixture로 검증했다.
- [x] 실행 파일 업데이트와 DB migration의 순서·재시작·호환 범위를 문서화했다.
- [x] CLI 도움말·진행/오류·로컬 배포 예제를 제공했다.

완료 기준: 로컬 artifact로 업데이트와 실패 복원이 재현된다. 공개 release 채널 배포나 모든 플랫폼 실제 실행을 자동 완료로 계산하지 않는다.

## 외부 환경이 정해진 뒤 할 일

| 대기 항목 | 필요한 조건 | 완료 증거 |
| --- | --- | --- |
| staging TLS/proxy/외부 PostgreSQL | 서버·도메인·접근 설정 | 실제 배포·upgrade/rollback·복구 |
| OAuth·SMTP·실제 S3 | 사용자가 정한 provider 계정 | 실제 callback·메일·객체 작업 |
| 운영 SLO·RPO/RTO | 목표 트래픽·데이터량·복구 목표 | load/soak/spike·장애·시점 복원 측정 |
| 여러 호스트 partition | 독립 호스트·네트워크 | 분리/재연결·중복 처리·복구 증거 |
| 공개 npm/pub.dev/binary 배포 | 배포 대상·자격·승인 범위 | 게시 artifact·서명·설치·플랫폼 실행 |

## 장기 제품 선택

다중 프로젝트 control plane, read replica routing, sharding, online DDL, cross-project relation은 현재 로컬 PocketBase 보완과 분리한다. 도입 시 실제 수요·운영 목표와 별도 설계를 먼저 기록한다. PB wire/SDK 완전 호환이 필요하다면 별도 호환 계층·conformance 범위를 계획해야 한다.

## 작업을 완료 처리하는 규칙

각 ID마다 구현·개별 검증·전체 인수·병합·설치·공개 배포를 구분한다. 변경 파일/커밋·테스트 결과·알려진 제약을 TODO에 기록하고 이 문서와 현재 상태 표를 함께 갱신한다. 예전 통과 기록은 유지하되 뒤늦게 변경된 코드의 통과 증거로 사용하지 않는다. 관련 변경 없는 테스트를 반복 실행해 숫자를 늘리지 않는다.
