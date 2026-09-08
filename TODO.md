# pgBase 구현 현황 / TODO

기준: 2026-09-08, v0.1.0 이후. 작업 브랜치: `codex/public-identity-owner-access`.
체크된 항목만 완료다. 구현·검증·병합·릴리스는 별도로 표시한다. 각 담당자의 결과가 들어오거나 검증 단계가 바뀔 때 이 파일과 아래 변경 기록을 갱신한다.

## 현재 실행 보드

| 작업 | 담당 | 상태 | 완료 기준 |
| --- | --- | --- | --- |
| M1-A 사용자 인증 | GPT 5.6 Sol / public_auth | 구현·단위·DB·검토 보완 완료 | 가입·로그인·Bearer 검증·로그아웃, 저장소/검증 테스트 |
| M1-B 소유권 기반 데이터 접근 | GPT 5.6 Terra / owner_access | 구현·DB·잠금 회귀 완료 | A/B 사용자 분리, 기본 차단, 원자적 소유권 기록, SQL 페이지 제한 전 필터 |
| M1-C HTTP·설정·앱 연결 | Root | 구현·실제 HTTP/race 검증 완료 | 실제 HTTP로 두 사용자의 독립 CRUD 가능 |
| M1-D 마이그레이션·독립 검토 | GPT 5.6 Sol / private_release_review + Root | 업그레이드·반복 실행 검증 완료 | 기존 이력 체크섬 유지, 업그레이드·역사 버전 검증 경계 확인 |
| M1-E 통합 검증·문서·PR | Root + 검토 에이전트 | 전체 회귀 검사 진행 | 전체 관련 검사 통과, PR/CI/병합 결과 기록 |

## M0 — 기준점과 작업 관리

- [x] PocketBase v0.40.3 및 pgBase v0.1.0 기준 기능 비교.
- [x] 실제 main 커밋 `3677a6b182b440a158e96d10482b1552030c18f5` 확인.
- [x] 구현 전용 `codex/public-identity-owner-access` 브랜치 생성.
- [x] Sol/Terra 담당 파일 경계와 공통 인터페이스 확정.
- [x] 상세 TODO와 현재 실행 보드 생성.
- [ ] 비교 보고서와 이번 구현 기록을 PR에 포함.

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
- [ ] PR 생성·최신 커밋 CI 확인.
- [ ] PR 병합 및 최종 main 확인.
- [ ] 릴리스 여부·버전·검증 결과를 별도 기록 (기존 v0.1.0 태그 변경 금지).

## M2 — 사용자 수명주기와 유연한 권한

- [ ] Auth 컬렉션 모델과 기존 프로젝트 사용자 업그레이드 설계.
- [ ] 관리자 UI에서 사용자 목록·비활성화·세션 폐기.
- [ ] 암호 변경과 기존 세션 폐기 정책.
- [ ] SMTP/메일 어댑터와 테스트 전송.
- [ ] 이메일 검증 토큰·만료·일회성 소비.
- [ ] 비밀번호 재설정 요청·완료·재사용 거부.
- [ ] 이메일 변경과 계정 재인증.
- [ ] OAuth2 provider 계약 및 state/PKCE 테스트.
- [ ] OTP와 MFA 흐름.
- [ ] 타입 있는 CRUD 규칙 AST 및 검증기.
- [ ] list/view/create/update/delete 규칙별 저장·평가.
- [ ] 익명/인증 사용자/서비스 계정 구분.
- [ ] 관계 ID·expand·역방향·filter·sort의 모든 hop 권한.
- [ ] 관계 삭제 전파의 대상별 변경 권한.
- [ ] 규칙 변경 후 커서·캐시·구독의 재검증.
- [ ] 정책 편집 UI·미리보기·오류 복구.
- [ ] 서비스 토큰 scope·회수·감사 기록.

## M3 — API 계약과 TypeScript SDK

- [ ] 공개 API 경로/입력/오류/상태 코드 문서.
- [ ] 버전 호환성 및 폐기 정책.
- [ ] OpenAPI 생성 경로 검토·결정.
- [ ] SDK auth store 및 토큰 lifecycle.
- [ ] SDK list/get/create/update/delete.
- [ ] 커서 순회·요청 취소·ETag·idempotency 지원.
- [ ] 브라우저/Node 환경의 conformance 테스트.
- [ ] SDK 배포 패키지·버전·릴리스.
- [ ] 작은 예제 앱에서 회원가입과 개인 데이터 관리.
- [ ] Dart SDK 수요 및 구현 범위 검토.

## M4 — 파일과 스토리지

- [ ] file 필드와 blob 메타데이터 모델.
- [ ] 업로드 대기/완료/record commit/삭제 상태 머신.
- [ ] 로컬·S3-compatible storage 어댑터.
- [ ] 크기·유형·파일명·checksum 검증.
- [ ] 사용자 규칙과 결합한 업로드/읽기/삭제.
- [ ] 보호 파일 URL·만료 토큰.
- [ ] 썸네일 작업과 변환 자원 제한.
- [ ] 레코드 실패 시 미참조 blob 정리.
- [ ] 재시도·장애 중 파일 정합성 테스트.
- [ ] 파일 포함 백업/복원 검증과 관리자 UI.

## M5 — Outbox와 realtime

- [ ] outbox claim/lease/fencing 및 병렬 worker.
- [ ] retry/backoff/dead-letter 및 운영 진단.
- [ ] subscriber·checkpoint·replay 계약.
- [ ] SSE 구독/해지와 연결 자원 제한.
- [ ] 구독·전달 시 사용자 및 record 규칙 검사.
- [ ] 정책 변경/로그아웃/비활성화 즉시 반영 계약.
- [ ] 여러 인스턴스의 전달·중복·재연결 테스트.
- [ ] slow consumer·DB 장애·network partition 검증.
- [ ] SDK subscribe/unsubscribe와 예제 앱.

## M6 — 나머지 제품 경험과 운영

- [ ] View 컬렉션과 읽기 전용 query 모델.
- [ ] batch CRUD의 원자성·권한·크기 제한.
- [ ] 복합 인덱스와 고급 필드·쿼리 요구 검토.
- [ ] 공개 Go event hook과 transaction 범위.
- [ ] JavaScript 확장 runtime 여부·자원 경계 결정.
- [ ] 사용자 정의 cron과 여러 인스턴스 실행 계약.
- [ ] 관리자 UI 기능 전체 탐색·접근성 회귀.
- [ ] 여러 플랫폼의 사전 빌드 배포.
- [ ] 실제 staging TLS/proxy/외부 PostgreSQL 배포.
- [ ] 목표 트래픽 기준 load/soak/spike 및 SLO 측정.
- [ ] PITR·장애 복구·RPO/RTO 검증.
- [ ] 기능 비교표를 구현·검증 완료 시점에 갱신.

## 변경 기록

- 2026-09-08: M1 범위·인터페이스·담당 확정. Sol 인증, Terra owner 정책, Root HTTP 연결, Sol 독립 호환성 검토 시작. M2–M6는 대기 목록이며 완료로 계산하지 않음.

- 2026-09-08: Sol 사용자 인증 단위/DB 검사, Terra 소유권·재시도·롤백·관계 차단 DB 검사, Sol 독립 HTTP 두 사용자 테스트(race) 통과. Root 인증·CORS·배선 HTTP 단위 검증 및 migration floor 갱신. 독립 검토 두 건과 잠금 동시성 추가 테스트 진행 중.

- 2026-09-08: 검토 수정 두 건 및 정책/계정 잠금 DB 테스트 완료. 공개 사용자·소유권을 포함한 실제 dump/restore가 PostgreSQL 16/17/18에서 통과. 사용자 API 문서와 9→11 유지보수 업그레이드 절차 작성. 전체 회귀 검사 시작.

- 2026-09-08: 전체 Go unit/race 및 skip 정책 통과. UI typecheck, 402개 테스트, build 및 embedded dist 일치. 최종 Sol 문서 검토에서 샘플 idempotency key 길이와 선택적 If-Match 설명을 바로잡음.

- 2026-09-08: 전체 PostgreSQL integration/race·skip 정책 통과. Sol의 populated v9→v11 업그레이드 및 반복 실행 보존 테스트 통과. 미사용 쿼리 wrapper를 제거하고 기존 설명 주석을 복구해 최종 코드 크기 재측정.

- 2026-09-08: 최종 쿼리·레코드·업그레이드·크기 정책 race 재검사와 pinned lint 통과. 실제 Cosign 검증 테스트 통과. 복원 후 원래 Bearer 세션까지 PG16/17/18에서 확인. PR 준비 완료, 배포된 v0.1.0은 유지.
