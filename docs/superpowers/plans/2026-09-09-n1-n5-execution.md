# N1–N5 병렬 구현 실행 계획

기준: `238cc37` (P8 완료, 시스템 migration 38). 사용자는 계획 작성 후 N1–N5 구현과 실시간 TODO 갱신을 승인했다. Astra는 범위 조정과 결과 확인을 맡고, 제품 구현·통합·검증은 GPT-5.6 Sol/Terra가 수행한다.

## 실행 순서와 소유권

1. 이 계획과 루트 TODO를 먼저 기록한다.
2. 동일 기준 커밋의 독립 git worktree에서 아래 네 작업을 병렬 구현한다. 각 담당은 코드 변경 전에 상태 문서에 구체적인 설계·채택 범위·검증 항목을 기록한다.
3. Sol 통합 담당이 N4 → N1 → N2/N3 → N5 순서로 통합한다. 준비된 독립 변경을 먼저 통합해도 되지만 N1 snapshot/diff의 N4 옵션 보존은 최종 확인한다.
4. Sol 통합 담당이 충돌 해결·생성물·전체 회귀와 독립 검토를 담당한다.
5. 완료 기준에 대한 증거를 TODO에 연결하고 구현/검증/미완료를 구분한다.

| 담당 | 범위 | 주 소유 파일 | 상태 문서 |
| --- | --- | --- | --- |
| Sol N1 | 자동 migration | internal/usermigrate, CLI user_migration, pgbase/user_migrations, schema snapshot/diff 연결 | docs/workstreams/n1-n5/N1.md |
| Terra N2/N3 | 정적 사이트·관리자 CLI | static handler, CLI/config/options/router, adminauth 관리 기능 | docs/workstreams/n1-n5/N2-N3.md |
| Sol N4 | 필드·조회 | schema/fieldvalue/query/recordops, 관리자 필드 편집, SDK 필드 타입 | docs/workstreams/n1-n5/N4.md |
| Terra N5 | 세부 hooks | lifecycle, API 요청 hooks, pgbase hook adapter, mailer/filestore | docs/workstreams/n1-n5/N5.md |
| Sol 통합 | 통합·전체 검증·실시간 TODO | 루트 TODO, next-work/project-status/현재 비교 문서, 생성물·공통 CI/정책 | docs/workstreams/n1-n5/integration.md |

각 담당은 관련 테스트와 기능 문서도 소유한다. 자기 worktree에서만 수정·커밋하고 공유 파일 변경은 상태 문서에 명시한다. 루트 TODO와 공통 상태 문서는 통합 담당 한 명만 편집한다. 단계 전환·검증 결과·장애 발생 시 담당 상태 문서를 즉시 갱신하며 통합 담당은 진행 중 수분 간격으로 중앙 TODO에 반영한다. 구현과 필요한 검증이 모두 끝나야 완료다.

## N1 — 자동 migration

- [ ] 결정적 schema snapshot/diff 규약. 필드 ID rename, relation/View/index 의존 순서, N4 옵션 보존.
- [ ] 실제 Go migration source 생성, collection snapshot 명령, 소스/이력 대조 및 history-sync. 검증 없이 미적용 SQL을 적용된 것으로 기록하지 않는다.
- [ ] 관리자 변경 자동 기록을 opt-in으로 연결. DB commit과 소스 기록 실패의 복구·중복 방지 구현.
- [ ] 비밀 값 배제, 데이터 손실 변경 및 역변환 불가 Down 명시.
- [ ] 외부 Go 모듈에서 생성·빌드·Up/Down·새 DB 재현·데이터 보존·실패 rollback·동시성 검증.

## N2 — 사용자 정적 사이트·SPA

- [ ] public 디렉터리/SPA fallback을 Go 옵션·환경·CLI에 연결.
- [ ] API/admin/custom route 우선순위, 없는 asset 404와 SPA 탐색 fallback 구분.
- [ ] 경로 정규화·symlink·MIME·캐시·HEAD·없는 디렉터리 동작 구현.
- [ ] 실제 HTTP/브라우저에서 asset·깊은 링크 새로고침·API 404 보존 확인.

## N3 — 관리자 CLI

- [ ] create/reset-password와 공통 서비스로 upsert/delete 구현. update 별칭의 암호 변경 의미 명시.
- [ ] 관리자 OTP 및 IP 제한의 적용·관리 범위를 정하고 CLI와 실제 인증 경로에 연결. PB 고유 의미 중 비지원 범위 명시.
- [ ] 마지막 관리자 보호·입력·종료 코드·세션 무효화·실패 시 무변경을 실제 DB에서 검증.

## N4 — 필드·조회 보완

- [ ] 문자열 자동 생성 패턴의 시점·명시 값 우선순위·충돌·재시도·멱등성 구현.
- [ ] email/URL 도메인 차단 목록, allow/deny 정규화·동시 설정·기존 데이터 preflight 구현.
- [ ] 필드 수정자·:lower·geoPoint 탐색을 항목별 대조해 채택 범위와 유지할 차이를 명시하고 실제 입력·조회·인가·Batch에 연결.
- [ ] schema/API/UI/SDK/import/export/rename 연결. 정확 숫자·UUID·Argon2id 계약 보존.

## N5 — 세부 hooks

- [ ] lifecycle-parity의 partial 영역별 이벤트·payload·시점·수정 가능 필드 정의.
- [ ] CRUD list/view/write, Batch/import 전체 요청 hooks 구현.
- [ ] 목적별 메일 및 파일 token/download/transform hooks 구현.
- [ ] 범용 model 단계의 PgBase 대응과 ORM 비지원 범위 문서화.
- [ ] veto·권한·취소·panic·retry/replay·commit 최종 결과·비밀 비노출 검증.

## 통합 인수

- [ ] 작업별 집중 단위/DB 검증과 독립 검토.
- [ ] migration 1–38 원본/checksum 보존. 새 번호는 통합 담당과 조율.
- [ ] Go unit/race, 격리 PostgreSQL+MinIO integration/race·skip 정책, lint/vet.
- [ ] UI unit/typecheck/build·내장 생성물 재현, SDK unit/live conformance/로컬 pack.
- [ ] 새 기능 실제 HTTP/브라우저 및 필요한 기존 회귀.
- [ ] OpenAPI route manifest·구조 예산·지원 플랫폼 빌드 확인. 무근거 예산 상향 금지.
- [ ] 현재 문서와 과거 TODO의 오래된 미완료 표시 정리.

격리 DB·임시 서버를 사용하고 기존 로컬 설치·사용자 DB·비밀 값·파일을 보존한다. 이번 범위는 N1–N5 로컬 구현·검증·로컬 커밋이다. 원격 PR/병합·설치 교체·공개 배포·N6–N9는 별도다. 기존 P8 검증 기록을 이번 변경의 통과 증거로 사용하지 않는다.
