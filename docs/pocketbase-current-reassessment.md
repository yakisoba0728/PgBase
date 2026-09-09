# PocketBase 대비 현재 재평가

기준: 로컬 PocketBase v0.40.3 (`5684ee24f1e88f71a2ede73d20cf547fbd509e16`), 설치된 PgBase N1–N5 / `2939f1543e4889c0ffa967eae398c91d0ecbb6ce`, PR #34 병합 기준. 2026-09-10 확인. 과거 전체 소스 감사는 이전 커밋의 기록이며 현재 미구현 목록으로 그대로 사용하지 않는다. 이번 확인은 현재 코드의 주요 기능·세부 계약 재대조다.

실행 중인 로컬 설치는 N1–N5 / PostgreSQL17.9 / schema40다. 구현·독립 검토·전체 로컬 인수, PR #34 필수 CI5/5·정상 병합·기존 설치 백업/적용/인수를 완료했다. 공개 release/package/cloud 배포는 수행하지 않았다. 각 기능의 완료는 문서화된 PgBase 계약 범위다.

| 관점 | 현재 판단 |
| --- | --- |
| 앱 백엔드 기본 기능 | 컬렉션·CRUD·관계·규칙·인증·파일·Batch·Realtime·TypeScript 클라이언트가 있어 로컬 앱 개발에 사용할 수 있다. |
| 관리자 주요 작업 | 필드 편집·레코드 일괄 작업·스키마 입출력·인증 설정·로그·cron·로컬 백업/복원까지 구현·적용했다. S3 관리 확장도 P8에서 로컬 인수·병합·설치를 마쳤다. |
| PocketBase 세부 동작 | N1–N5에서 bounded text 생성, domain deny, `:lower`, geoPoint 좌표 조회, 세부 request/file/mail hooks와 자동 migration을 보강했다. 미채택 write modifier와 ORM callback 등은 문서화된 차이로 남는다. |
| 개발자 확장과 도구 | Go route, 수동/자동 migration, typed hooks와 TypeScript SDK가 있다. 현재 병합·설치 제품에 JSVM, Dart SDK, SQL 콘솔, self-update는 없다. N6/JSVM·N7/Dart는 별도 worktree에서 구현 중이다. |
| 호환성과 운영 | PostgreSQL, UUID, 정확 숫자, 세션, 커서, 파일 URL 등의 규약이 다르다. PocketBase SDK/스키마/확장 코드의 무수정 사용이나 SQLite 단일 파일 운영 경험을 제공하지 않는다. |

## N1–N5에서 닫은 차이

- bounded RE2 subset의 문자열 자동 생성 패턴을 create-only absent-key 계약, unique retry/replay/rollback과 함께 schema/API/SDK/UI에 연결했다.
- email/URL allowed/denied domain을 canonicalize하고 exact/subdomain·deny-wins·기존 데이터 preflight를 검증했다.
- canonical schema snapshot/diff, 생성 Go migration source, 지원 가능한 Up/Down, history compare/sync와 durable mutation recorder를 구현했다.
- public 정적 디렉터리와 SPA fallback을 asset/navigation 404 및 API/admin/custom route 우선순위와 함께 구현했다.
- 관리자 upsert/delete, TOTP와 IP 정책 CLI를 stable administrator ID, session invalidation, last-admin 보호와 함께 구현했다.
- CRUD/Batch/schema import, 목적별 mail, file token/download/transform의 typed hook 경계를 실제 요청과 veto·panic·cancel·replay 결과로 검증했다.

## 큰 기능 표에서 빠지기 쉬운 남은 차이

- 일반 password의 암호화 알고리즘/설정: PgBase는 Argon2id를 사용하며 PB bcrypt cost 설정과 호환되지 않는다. 이는 알고리즘 부재가 아닌 설계 차이다.
- editor의 `:lower`는 지원하지만 geoPoint 일반 JSON-property 탐색은 지원하지 않고 `.lon`/`.lat`로 제한한다. numeric/select/relation/file write modifier와 text-key `:autogenerate`는 PgBase의 정확 숫자·순서·인가·파일 lifecycle 계약 때문에 채택하지 않았다.
- 자동 migration은 stable-ID field rename을 지원한다. 기존 schema domain에 collection rename update가 없어 collection name 교체는 delete/create이며 irreversible이다.
- request hooks는 typed service/API boundary다. PocketBase의 임의 ORM model validate/execute/success/error callback을 제공하지 않으며 process-local hook을 durable outbox로 취급하지 않는다.
- JSVM·Dart SDK·SQL 콘솔·self-update는 현재 병합·설치 제품에 포함되지 않는다. N6/N7 병렬 구현은 후속 단계이며 완료로 계산하지 않는다.

근거: [현재 필드 설정](../internal/schema/types.go), [고급 필드 계약](advanced-fields.md), [사용자 migration CLI](../internal/cli/user_migration.go), [생성기](../internal/usermigrate/generate.go), [hook 대조](lifecycle-parity.md), [확장 및 SDK 범위](extensions-and-client-scope.md).

Sol/Terra 작업자가 [실행 계획](superpowers/plans/2026-09-09-n1-n5-execution.md)에 따라 각 구현을 작성·집중 검증하고 다른 workstream을 Standards/Spec 관점에서 독립 검토했다. 통합 기록과 최종 gate는 [N1–N5 integration](workstreams/n1-n5/integration.md)을 따른다.

## 검증 상태

P8의 원격/설치 증거는 유지된다. N1–N5는 별도 disposable PostgreSQL 18/MinIO에서 각 stream race·HTTP, N1 external module/fresh/upgrade/doctor, final whole Go unit/race와 PG18+MinIO integration/race, combined UI 492개/typecheck/build/repro, SDK unit/live/pack, OpenAPI, 6종 build와 실제 Chromium 20개를 통과했다. 추가로 exact 최종 tree의 required CI5/5(전체 Go/PG17 gates 포함)와 실제 PG17/schema38→40 설치 인수를 통과했다. 백업·hash·두 installer 증거 오류와 복구/최종 성공은 [설치 기록](workstreams/n1-n5/release-install.md)을 따른다. 전체 이력은 [현재 상태](project-status.md), exact 결과는 [통합 기록](workstreams/n1-n5/integration.md), 후속 구현은 [N0–N9](next-work.md)에 정리한다.

기능별 중요도와 세부 계약 수를 고정한 전체 분모가 없으므로 단일 진행률 백분율을 산출하지 않는다. 현재 평가는 '주요 BaaS와 관리자 작업 및 N1–N5 세부 보완을 구현·검증·병합·로컬 설치, 확장 생태계와 일부 PB 계약은 미완성'이다. 최신 작업 결과는 [TODO](../TODO.md)를 따른다.
