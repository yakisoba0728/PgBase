# PgBase 구현 이력과 현재 상태

기록일: 2026-09-11. 이 문서는 지금까지의 작업을 통합한 현재 상태 문서다. 세부 작업 기록은 [TODO](../TODO.md), 다음 실행 순서와 완료 기준은 [후속 작업](next-work.md), PocketBase와의 차이는 [현재 기능 비교](pocketbase-feature-comparison-current-2026-09-10.md)를 따른다. 과거 감사·마일스톤 문서는 당시 증거를 보존하는 기록이다.

## 1. 현재 기준점

| 항목 | 확인된 상태 |
| --- | --- |
| 현재 저장소 | [yakisoba0728/PgBase](https://github.com/yakisoba0728/PgBase) |
| 최신 제품 병합 기준 | [PR #34](https://github.com/yakisoba0728/PgBase/pull/34), `2939f1543e4889c0ffa967eae398c91d0ecbb6ce` |
| N1–N5 개발 기준 | 구현·독립 검토·전체 로컬 인수 및 필수 원격 CI5/5·정상 병합 완료. [기존 설치·인수](workstreams/n1-n5/release-install.md) 완료 |
| N6–N9 소스 기준 | **구현·독립 검토·G5 로컬 인수 및 원격 병합 완료**. 최종 제품/UI `b7674f704e14b07104ebf6f1c9ee5e53092f367e`, 후속 수정은 [PR #35](https://github.com/yakisoba0728/PgBase/pull/35)로 main `53a474143e117251d9b322441508ac85d776ef2a`에 병합. [인수 증거](workstreams/n6-n9/acceptance.md) |
| PocketBase Round 3 검증 소스 | `codex/round3-integration`의 `bd76b1d1aaf28839072fa71e6b05cf39219053e9`. 고정 232행은 구현 232·부분 0·미구현 0. [21행 완료 ledger](pocketbase-unimplemented-completion-round3-2026-09-11.md) |
| 설치 제품 커밋 | `2939f1543e4889c0ffa967eae398c91d0ecbb6ce`. 설치 기록 문서의 후속 커밋과 구분 |
| 실제 로컬 설치 | N1–N5, PostgreSQL17.9, schema40, `http://127.0.0.1:8090/admin` |
| P8 개발 DB | migration 33–38, 임시 PostgreSQL 18/MinIO 및 populated 복사본 인수 완료 |
| 공개 릴리스 | README에 기록된 최초 v0.1.0과 현재 개발 소스를 구분. P8/N1–N5 추가 공개 릴리스는 없음 |
| 비교 대상 | 로컬 PocketBase v0.40.3, `5684ee24f1e88f71a2ede73d20cf547fbd509e16` |
| 승인된 범위 | 로컬에서 구현·검증 가능한 제품 기능. 외부 서버·도메인·실제 provider 계정·공개 패키지 배포는 대기 |

N1–N5 구현·로컬 통합 인수·필수 원격 CI 5개·PR 병합·기존 로컬 설치까지 완료했다. 구현, 개별 검증, 통합 인수, 병합, 설치, 공개 배포를 서로 다른 상태로 기록한다. 저장소 이전과 협업 요청은 있었지만 과거 저장소 상태·현재 collaborator 권한은 이 문서 작성에서 재조회하지 않았으므로 완료 상태를 새로 단정하지 않는다.

## 2. 작업 이력

| 단계 | 완료한 주요 내용 | 근거 / 인수 상태 |
| --- | --- | --- |
| 초기 Production Kernel | 관리자 setup·세션, PostgreSQL schema plan/apply, typed CRUD, cursor·ETag·멱등성, audit/outbox, 내장 UI, doctor·계측·컨테이너 | [초기 handoff](production-kernel-handoff.md), 후속 커밋에서 확장 |
| 데이터 커널·코드 감사 | Record Operations 원자성, 관계 저장·삭제·확장, schema lifecycle, 동시성·실패 처리 및 구조 개선. Membership A0/A/B/C 단계별 rollout artifact, lease/fence, mixed-fleet 호환·doctor·UI recovery 보강 | [8/28](codebase-audit-2026-08-28.md), [8/31](codebase-audit-2026-08-31.md), [9/1](codebase-audit-2026-09-01.md), [9/8](codebase-audit-2026-09-08.md) 감사 기록 및 git 이력. 과거 finding 상태는 후속 수정과 함께 판단 |
| 배포 검증 기반 | 릴리스 검증·서명/attestation 작업 흐름, private release 대응 | PR #25 `3677a6b`. 실제 외부 배포 완료와는 별개 |
| M1 | 공개 가입·로그인·폐기 가능한 Bearer 세션, opt-in 소유자 레코드 API | [PR #26](https://github.com/yakisoba0728/PgBase/pull/26), `52af13d` 병합 |
| M2-A | 관리자 사용자 목록·비활성화·세션 폐기, 암호 변경과 재로그인 정책 | [PR #27](https://github.com/yakisoba0728/PgBase/pull/27), `09d6b7d` 병합 |
| M2–M6 | 인증·권한 확장, SDK, 파일, SSE, View·Batch·작업 및 로컬 개발 환경 | [PR #28](https://github.com/yakisoba0728/PgBase/pull/28), `35af64c` 병합 |
| PocketBase 소스 감사 | 파일별 판독·계약 대조·관리자 UI와 API 공백 조사 | 본문 734개, 생성물/자산 186개, 역사 참고 3개. [판독 목록과 범위](pocketbase-source-audit-2026-09-09/README.md). 현재 재평가는 전체 재판독 아님 |
| P1–P5 | 필드 계약·데이터 보존 rename, 레코드 작업, 인증 설정, 운영 콘솔, API 예제·SDK | [PR #29](https://github.com/yakisoba0728/PgBase/pull/29), `33816df` 병합, 로컬 v21→v23 |
| P6 | 파일 정책/변환, OAuth·HTML 메일, SQL View, multipart Batch, 영구 로그·달력 cron, 웹 백업·Go hooks | [PR #30](https://github.com/yakisoba0728/PgBase/pull/30), `b942391` 병합, 로컬 v23→v28 |
| P7 | hidden/presentable/help, schema import/export, 이메일 OTP, impersonation, Go custom routes | [PR #31](https://github.com/yakisoba0728/PgBase/pull/31), `701256b` 병합, 필수 CI 5개 및 로컬 v28→v32 인수 완료 |
| P8 | 고급 필드, 저장된 S3 설정·파일 이전·백업 저장소, 사용자 Go migration, 추가 hooks | [PR #32](https://github.com/yakisoba0728/PgBase/pull/32), `0d28446` 병합, 필수 CI 5개·로컬 v32→v38 인수 완료 |
| N1–N5 | 자동 migration, public 정적 사이트/SPA, 관리자 CLI, 필드·조회 세부 계약, request/file/mail hooks | 전체 로컬 인수·독립 검토와 [PR #34](https://github.com/yakisoba0728/PgBase/pull/34) 필수 CI5/5·merge `2939f15` 완료. 03:24 KST 기존 PG17/schema38→40 설치·인수 완료 |
| PocketBase Round 2/3 | source audit의 부분·미구현 행을 actual route/service/SDK/UI 효과와 거절·rollback·동시성 근거로 닫음 | Round 2 `211/0/21`, Round 3 최종 `bd76b1d1`에서 `232/0/0`. [current comparison](pocketbase-feature-comparison-current-2026-09-10.md)과 [Round 3 ledger](pocketbase-unimplemented-completion-round3-2026-09-11.md) 참조 |

M/P 번호는 시기별 작업 묶음이다. 이전 단계에서 제외했던 항목을 후속 단계가 구현한 경우 후속 기록을 우선한다. 예를 들어 과거 M6의 'JS runtime 범위 결정 완료'는 JSVM 구현 완료가 아니다.

## 3. 구현·로컬 적용을 마친 기능 — P7까지

| 영역 | 제공하는 기능 | 상세 계약 |
| --- | --- | --- |
| 데이터·스키마 | 필드 ID 기반 rename, 정확 숫자·JSONB·단일/다중 관계, 복합 인덱스, 제약 preflight, 원자적 plan/apply, 논리 schema export/import | [schema transfer](schema-import-export.md), [P7](phase3-local-contracts.md) |
| 레코드 API | CRUD·ETag·멱등성·서명 커서, 관계 expand/filter/sort, 컬렉션을 가로지르는 JSON/multipart Batch, PUT upsert, 실패 rollback | [공개 API](public-api.md), [P6](phase2-local-contracts.md) |
| 인증·권한 | auth realm, password/OAuth PKCE, TOTP·복구 코드·이메일 OTP, 검증/재설정/이메일 변경, impersonation, 익명/사용자/service 규칙과 관계 경로 인가 | [사용자 관리](user-management.md), [P7](phase3-local-contracts.md), [TODO](../TODO.md) |
| 관리자 데이터 작업 | 필드 옵션·표시/숨김, 필터·정렬, 다중 삭제·부분 실패, JSON export·복제, 열 설정·초안 복구, relation picker 즉석 생성/수정 | [제품 작업 기록](local-product-completion.md), [P7](phase3-local-contracts.md) |
| 인증 설정·메일 | realm별 정책·OAuth 프리셋 30개·프로필 이미지, Apple callback bridge, SMTP 시험, HTML/plain-text 템플릿·미리보기 | [P6](phase2-local-contracts.md). 실제 provider 계정 검증과 구분 |
| 파일 | local/S3 adapter, pending/ready/deleted 수명주기, 필드별 크기·MIME·개수·Required, 보호 토큰, crop/fit/top/bottom 썸네일, Batch 파일 commit·실패 정리 | [P6](phase2-local-contracts.md), [백업](backup-restore.md) |
| Realtime | durable outbox, claim/lease/fence, retry/dead-letter, SSE cursor replay, 매 전달 권한 재확인, 느린 소비자·종료 처리 | [Realtime](realtime.md) |
| 운영 UI | 영구 로그 검색/상세/통계/정리, 달력 cron·timezone/DST·수동 실행, 웹 백업 생성/업로드/다운로드/삭제·새 대상 복원/활성화 | [작업](local-jobs.md), [백업 콘솔](local-backup-console.md) |
| 개발자 도구 | TypeScript SDK와 로컬 pack/설치, auth/CRUD/파일/SSE/관리 API, 컬렉션 API 예제, Go custom route·기본 hooks | [SDK](../sdk/typescript/README.md), [Go routes](custom-routes.md), [확장 범위](extensions-and-client-scope.md) |
| 개발·검증 기반 | 로컬 PostgreSQL/SMTP/MinIO, DB 격리·race·브라우저·SDK 검증, OpenAPI route manifest, 6종 cross-build, 로컬 load/PITR 실험 | [로컬 개발](local-development.md), [검증 기록](local-verification.md) |

여기의 '완료'는 정의한 PgBase 계약의 로컬 인수를 뜻한다. 예를 들어 Required는 기본 scalar에서 SQL NULL 기준이며 파일은 최소 1개 기준이다. PB의 모든 빈 값·필드 수정자·요청 payload와 같다는 뜻이 아니다. 파일의 별도 minSelect는 지원하지 않는다.

## 4. 구현·로컬 인수·설치를 마친 P8

| 작업 | 구현 / 확인한 증거 | 남은 경계 |
| --- | --- | --- |
| editor·geoPoint | schema/API/UI, rich text·HTML source·오프라인 좌표 입력, 좌표 filter/sort, 5 MiB HTML HTTP 왕복 | 로컬 인수 완료. PB 필터/JSON 탐색 및 editor 정규화와 차이 유지 |
| autodate·password | DB 자동 시간, Argon2id, 비노출·초안/복제 제외, retry/replay/rollback, 공개 Go 암호 검증 helper | 로컬 인수 완료. PB bcrypt 설정과 다른 계약 |
| 파일 S3 설정 | 암호화된 revision 설정, secret 보존/clear, API/UI/SDK, Put/Open/Delete 연결 시험 | 로컬 인수 완료 |
| 기존 파일 이전 | durable ledger, copy/hash 검증, cutover, resume/cancel, namespace 재사용 보호, 동일 operation UUID 응답 유실 복구 | 로컬 race·브라우저 인수 완료 |
| 파일 작업 잠금 | 요청 DB 풀과 분리한 coordination 연결, 세대 확인, lease 유실 시 업로드 확정 거절 | 백업·복원 결합 회귀 통과 |
| 백업 S3 | 독립 repository/설정/UI/SDK, 소유 archive 보호·삭제 재시도, pending publication ledger(migration38). 실제 S3 파일 snapshot 복원·원본 객체 삭제 후 로컬 바이트 보존 및 managed 전환 집중 race 통과 | 로컬 전체 회귀 통과 |
| 사용자 Go migration | create/list/up/down, source checksum, tx rollback, schema 잠금/캐시 및 migration 제어 상태 보호 | P8 당시 로컬 인수 완료. 자동 diff/snapshot/history-sync는 후속 N1에서 구현·설치 완료 |
| lifecycle hooks | app/collection/auth/realtime/file-storage settings/import/backup의 before/after·veto·결과 전달 | 로컬 인수 완료. [hook 목록](lifecycle-parity.md)의 partial 항목은 별도 후속 |

상세 계약: [고급 필드](advanced-fields.md), [파일 저장소](storage-settings.md), [백업 S3](backup-s3.md), [사용자 migration](user-migrations.md), [P8](phase4-local-contracts.md).

## 5. N1–N5 통합·원격 병합·설치 완료

| 작업 | 구현한 범위 | 현재 상태와 경계 |
| --- | --- | --- |
| N1 자동 migration | 결정적 canonical snapshot/diff, stable-ID field rename, 의존 순서, 생성 Go source, 지원 가능한 Up/Down, typed irreversible Down, history compare/sync, schema transaction과 함께 기록되는 durable auto-recorder/recovery | migration 40 사용. 외부 Go module·fresh 1→40·v38→v40·doctor·N1→N4 option matrix 통과. collection rename은 기존 domain에 update가 없어 delete/create·irreversible |
| N2 정적 사이트·SPA | `PublicDir`/SPA fallback, CLI·환경·Go options, API/admin/custom route 우선순위, asset 404와 navigation fallback 구분, descriptor-root 경로 격리 | 실제 built asset, deep-link refresh, missing asset/API JSON 404, admin 우선순위 Chromium 통과 |
| N3 관리자 CLI | admin upsert/delete, TOTP 설정·해제, IP allow/deny/list, stable-ID reactivation, session invalidation, last-admin 보호 | migration 39 사용. 실제 PG18 race와 browser TOTP/IP/session/last-admin 통과 |
| N4 필드·조회 | bounded text autogeneration, email/URL denied domains, canonical options, `:lower` filter/rule, geoPoint `.lon`/`.lat`, schema/API/SDK/UI 연결 | explicit input 우선·create-only 생성, collision/replay/rollback, domain preflight, HTTP/Batch/browser 및 N1 snapshot/Up/Down 보존 통과. 미채택 PB modifiers는 문서화된 차이 |
| N5 세부 hooks | record list/view/write, Batch/schema import, 목적별 mail, file token/download/transform의 typed request/result hooks | mutable input 재검증, immutable identity/snapshot, veto·panic·cancel·retry/replay·stream outcome 검증. process-local이며 durable delivery/outbox가 아님 |

N1–N5는 [PR #34](https://github.com/yakisoba0728/PgBase/pull/34)의 필수 CI5/5 통과 후 `2939f15`로 병합됐다. 제출 `432bfaa`와 merge tree는 동일하며 최종 코드 전체 Go unit/race와 PG17+MinIO integration/race·SDK live 근거가 추가됐다. 03:24 KST 같은 제품 SHA의 실제 PG17.9/schema40 설치·인수를 완료했다. 백업/hash와 두 installer 증거 오류로 정상 제품을 rollback한 뒤 복구·최종 성공한 이력은 [설치 기록](workstreams/n1-n5/release-install.md)을 따른다. 공개 release/package/cloud 배포는 이 승인에 포함되지 않는다.

## 6. N6–N9 로컬 통합 — 완료

N6 JSVM과 N7 Dart SDK는 실제 external Go+JS/Dart/Flutter consumer, PG·HTTP·file·SMTP·SSE, same-origin PublicDir와 동일 record/ETag/exact-number 교차 계약을 완료했다. N9는 private/shared composition review, native Darwin update/recovery, 최종6종 CGO0 build와 Windows helper compile을 완료했다. Windows native 실행은 수행하지 않았다.

N8 SQL 콘솔은 parser/role guard, 정확한 결과·cancel, 영구 write identity/30일 heavy-history 정리, API/session-scoped UI와 실제 browser13 시나리오를 완료했다. 마지막 terminal 성공의 durable 저장 전 노출 문제는 `b7674f7`에서 수정했으며 결정적 row-lock red/green과 영향 범위5개 PG race3.439s로 닫았다. Gauss 최종 `0b7fcb65`는 Standards PASS / Spec PASS / open0다.

G5는 전체 Go/PG18+MinIO baseline 실행과 정확한 실패 범위 수정·재검증을 결합한다. 최초 전체 PG 명령의43 package PASS와 두 test fixture 실패, 수정 뒤 성공, 최종 lint0/vet/SDK live+pack/UI build·repro/구조 실측/6build/native metadata를 [최종 인수](workstreams/n6-n9/acceptance.md)에 분리 기록했다. 기존 green N6/N7/N9 전체 인수는 반복하지 않았다.

08:45 KST에 작업 전용 N7/N8/G5 컨테이너6개를 정지했고 data/evidence를 보존했다. 모든 worker/API/UI/browser도 종료했다. live PG17.9 `55440`, HTTP `8090`, MinIO `59000`, 설치 binary와 기존 userdata는 변경하지 않았다. N6–N9는 이후 [PR #35](https://github.com/yakisoba0728/PgBase/pull/35)로 정상 병합됐고, 공개 release/npm/pub.dev/cloud 배포·live 교체는 수행하지 않았다.

## 7. 검증과 실제 수정한 문제

| 시점 / 범위 | 증거 | 해석 |
| --- | --- | --- |
| P1–P5 | UI 450개, 브라우저 8개, Go/PG18 race·SDK·lint/vet·6종 빌드 | 당시 최종 코드 인수 |
| P6 | UI 459개, 브라우저 11개, Go/PG18+MinIO race·SDK·lint/vet | 당시 최종 코드 인수 및 v28 설치 |
| P7 | UI 466개, 전체 브라우저 15개와 후속 P7 5개, Go/PG18+MinIO race, CI 5개 | 병합·v32 설치까지 완료. 테스트 수를 합산하지 않음 |
| P8 병합·설치 | [PR #32](https://github.com/yakisoba0728/PgBase/pull/32) 필수 CI 5개 통과, 로컬 PG17 v32→v38, doctor/readiness·관리자 세션·4종 필드 preview·두 저장소 probe·managed 백업 다운로드/hash | DB·파일·기존 백업·환경·바이너리 사전 보존. S3 실제 왕복은 격리된 MinIO 환경에서 인수 |
| P8 전체 로컬 검사 | Go unit/race·PG18+MinIO integration/race·skip 정책, UI 489/489·typecheck/build, SDK live conformance/pack, lint/vet·6종 빌드·migration/OpenAPI·구조 예산 | 최종 백업 테스트 수정은 패키지 전체 재검사 및 race 100회로 보완 |
| P8 브라우저 | 고급 필드, 실제 파일 local→S3→local, 독립 S3 백업 설정/생성/다운로드/삭제 3개 통과 | 전체 관리자 브라우저 19/19 통과 후 최신 probe 수정에서도 P8 3/3 재검증. 중복 시나리오를 합산하지 않음 |
| P8 DB 업그레이드 | 구버전 실제 CLI/API로 text·integer·JSONB·relation 4레코드 생성 → v32→v38, 해시·행 수 보존, doctor OK | 인수 당시 원본 v32 유지, 복사본/임시 서버 정리. 실제 설치 상태는 상단을 따름 |
| P8 집중 검증 | 고급 필드 rename/View/schema 왕복·권한/Batch, 사용자 migration, 파일 lease 유실, 백업 pending fence | 최종 PG18+MinIO/race 통과. 백업 테스트 종료 동기화는 패키지 전체 재검사로 보완 |
| P8 백업 최종 집중 묶음 | PG18+MinIO race: `internal/backup` 3.761초, `pgbase` 3.493초. 실제 S3 원본 삭제 후 복원 바이트·local/no-secret 설정, 소유 객체 보호·pending fence·managed 전환 | `phase4-backup-s3.log`. 전체 저장소/Go/UI/SDK 인수와는 구분 |
| N1–N5 전체 로컬 인수 | final Go unit/race 24.2초, PG18+MinIO integration/race(`pgbase` 191.613초 등), skip policy 1.163초, UI 492개·typecheck/build/repro, SDK 7/7·live/pack, OpenAPI 115 paths/144 operations, 6종 build, 실제 Chromium 20개와 focused N2/N3/N4 | 독립 Standards/Spec review와 구조 실측 완료. 잘못된 backup product 가설은 exact revert 후 affected unit/race·PG18+MinIO·auto-wait browser로 최종 확인 |
| PocketBase Round 3 최종 검증 | `bd76b1d1`: Go unit/vet, 전체 integration `-p 2`, recordidentity/recordops/query/userauth/realtime/pgbase race, TS build/types/tests 8, Dart analyze+tests 30, SDK text-ID/auth HTTP count 2, UI 44 files/533 tests+build | Chromium auth realm scaffold/duplicate/register/login, base/view/truncate/UI extension 증거는 앞선 schema+UI 통합 head에서 수행. final head는 UI unit/build 재실행. 구조 측정 data 27,498, handwritten 97,941, UI 7,101, production 800줄 초과 0 |

수정과 검증의 대표 사례:

- schema field 재생성으로 파일 metadata를 잃던 경로를 stable ID 갱신으로 변경했다.
- 필수 파일 생성 우회, 같은 Batch의 파일 교체 순서, 불확실한 commit의 선행 object 삭제를 수정했다.
- 구버전 catalog 열 존재 검사와 schema import 관계 DDL 순서를 보강했다.
- 관리자 인증 설정의 요청 전용/응답 전용 필드 매핑 오류와 응답 유실 후 import 복구를 수정했다.
- 5 MiB HTML Batch 응답에 남아 있던 4 MiB 제한을 수정했다.
- S3 이전의 미리보기 ID 오인, 시작 응답 유실의 중복 작업, 상태 조회 실패/복원 후 오래된 ID로 UI가 잠기는 문제를 수정했다.
- 잠금용 DB 풀 고갈, lease 연결 유실 후 구세대 파일 metadata 확정 문제를 보강했다.
- 백업 publication의 영구 pending 기록으로 연결 유실 중 저장소 변경을 차단하는 집중 검증이 통과했다.
- 기존 schema-transfer UI 테스트가 비동기 digest 완료 전에 단언하던 타이밍을 수정했다. 이후 최신 UI 전체 489/489와 typecheck/build가 통과했다.
- 다른 backup publisher가 실행 중일 때 startup recovery가 pending 기록을 정리하던 경쟁을 수정했다. 소유 metadata와 외부 객체를 구분하고 성공한 연결 시험의 삭제 실패/잔여 객체도 검증했다.
- 사용자 migration과 schema Apply의 잠금 대기·stale 계획 거절·공유 캐시 갱신을 직접 검증했다.
- 확대된 8 MiB 레코드/48 MiB 응답 계약에 맞춰 과거 페이지 경계 fixture를 갱신하고 canonical JSON 크기 계산에서 HTML escaping 차이를 바로잡았다.

로컬 재현 로그는 `.local/verification/phase4-*.log`, 업그레이드 결과는 `.local/verification/phase4-populated-upgrade/result.json`에 있다. `.local`은 다른 clone에 포함되지 않을 수 있다. 재현 가능한 테스트 소스는 `internal/{schema,query,recordops,filestore,backup,usermigrate}`, `pgbase`, `ui/admin/e2e/authenticated/phase4.spec.ts`에 보존한다. 자격 증명·실제 환경 파일은 문서에 복사하지 않는다.

## 8. 남은 범위와 문서 사용법

N6–N9 구현·독립 검토·전체 G5 로컬 인수는 `b7674f7`에서 완료했고, 후속 수정까지 main `53a4741`에 병합됐다. PocketBase Round 3 기능 수용은 별도 검증 branch `bd76b1d1`에서 완료했으며 원격 병합·설치·공개 배포와 구분한다. 설치 제품은 여전히 N1–N5/schema40이다. 232/0/0은 고정 비교표의 기능 수용 결과이고 무수정 PocketBase wire/SDK/ORM 호환을 뜻하지 않는다.

클라우드 배포·운영 SLO/RPO/RTO·외부 provider 계정·실제 다중 호스트 partition·공개 패키지 배포는 별도 대기다. 로컬 테스트와 cross-build가 이 증거를 대신하지 않는다. read replica·sharding·다중 프로젝트 control plane·online DDL 같은 장기 확장은 PB 기능 공백 및 현재 로컬 완료 조건과 분리한다.

1. 전체 진행 상황은 이 문서를 읽는다.
2. 다음 구현은 [후속 작업](next-work.md)의 ID·완료 기준을 따른다.
3. 실행 결과는 [TODO](../TODO.md)에 기록하고 이 문서의 상태와 함께 갱신한다.
4. PB 세부 차이는 [현재 기능 비교](pocketbase-feature-comparison-current-2026-09-10.md)와 [Round 3 완료 ledger](pocketbase-unimplemented-completion-round3-2026-09-11.md), 동작 규약은 각 기능 문서를 따른다.
5. 과거 감사의 미구현/미해결 표시는 해당 커밋의 기록으로 읽고 현재 코드와 대조한다.
