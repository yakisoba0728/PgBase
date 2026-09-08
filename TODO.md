# pgBase 구현 현황 / TODO

기준: 2026-09-09, v0.1.0 이후. 현재 작업 브랜치: `codex/pocketbase-m2-m6`. M1과 M2-A는 PR #26/#27, main `09d6b7d`에 병합 완료. 사용자 지시에 따라 M2–M6의 로컬 개발·실행·검증 전체를 진행한다. 외부 인프라·실제 외부 계정 연결·공개 패키지 배포는 대기 범위다.
체크된 항목만 완료다. 구현·검증·병합·릴리스는 별도로 표시한다. 각 담당자의 결과가 들어오거나 검증 단계가 바뀔 때 이 파일과 아래 변경 기록을 갱신한다.

## 현재 실행 보드

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
- [ ] 단계별 PR/최신 CI/main 병합 결과 기록, 소스와 릴리스 구분.

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
