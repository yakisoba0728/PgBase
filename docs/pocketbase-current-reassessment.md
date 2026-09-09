# PocketBase 대비 현재 재평가

기준: 로컬 PocketBase v0.40.3 (`5684ee24f1e88f71a2ede73d20cf547fbd509e16`)와 PgBase `codex/pocketbase-parity-phase4` 작업 트리. 2026-09-09 확인. 과거 전체 소스 감사는 이전 커밋의 기록이며 현재 미구현 목록으로 그대로 사용하지 않는다. 이번 확인은 현재 코드의 주요 기능·세부 계약 재대조이며 전체 소스 재감사나 새 전체 테스트 실행을 뜻하지 않는다.

실행 중인 설치는 P7 / schema v32다. P8 고급 필드·S3 설정·사용자 migration·추가 hooks는 개발 코드이며 아직 병합·설치 전이다. 개별 테스트 통과를 전체 인수 완료로 계산하지 않는다.

| 관점 | 현재 판단 |
| --- | --- |
| 앱 백엔드 기본 기능 | 컬렉션·CRUD·관계·규칙·인증·파일·Batch·Realtime·TypeScript 클라이언트가 있어 로컬 앱 개발에 사용할 수 있다. |
| 관리자 주요 작업 | 필드 편집·레코드 일괄 작업·스키마 입출력·인증 설정·로그·cron·로컬 백업/복원까지 구현·적용했다. S3 관리 확장은 P8 로컬 인수를 마쳤으며 병합·설치가 남아 있다. |
| PocketBase 세부 동작 | 필드별 옵션·수정자·필터·요청 hook·migration 자동 생성 등 차이가 남는다. 큰 기능 이름이 대응한다고 세부 계약 완료로 표시하지 않는다. |
| 개발자 확장과 도구 | Go route와 migration 및 여러 hooks가 있지만 JSVM, Dart SDK, SQL 콘솔, self-update는 없다. |
| 호환성과 운영 | PostgreSQL, UUID, 정확 숫자, 세션, 커서, 파일 URL 등의 규약이 다르다. PocketBase SDK/스키마/확장 코드의 무수정 사용이나 SQLite 단일 파일 운영 경험을 제공하지 않는다. |

## 큰 기능 표에서 빠지기 쉬운 미완료 항목

- 문자열 자동 생성 패턴: PocketBase `core/field_text.go`의 `AutogeneratePattern`에 대응하는 공개 PgBase FieldOptions는 없다.
- email/URL 도메인 차단 목록: PocketBase `ExceptDomains`에 비해 PgBase는 현재 `AllowedDomains`만 노출한다.
- 일반 password의 암호화 알고리즘/설정: PgBase는 Argon2id를 사용하며 PB bcrypt cost 설정과 호환되지 않는다. 이는 알고리즘 부재가 아닌 설계 차이다.
- editor의 `:lower` 필터 및 geoPoint의 일반 JSON-property 탐색은 지원하지 않는다. PgBase 좌표 조회는 lon/lat로 제한한다.
- 사용자 migration은 Go create/list/up/down과 빈 Up/Down 템플릿을 제공한다. 관리자 변경을 이식 가능한 migration 소스로 자동 기록하는 snapshot/diff 및 history-sync는 제공하지 않는다. 스키마 JSON export와는 다른 기능이다.
- hooks는 per-endpoint CRUD 요청 payload, 목적별 메일 발송 이벤트, 파일 토큰/다운로드/변환 읽기 이벤트가 부족하다. 공통 request/mail/file hook 존재와 구분한다.
- JSVM·Dart SDK·SQL 콘솔·self-update가 미구현이다.
- 사용자 정적 파일 디렉터리와 SPA fallback: PgBase 기본 router는 내장 `/admin` UI만 제공한다. PB 기본 실행 파일의 publicDir 기반 사이트 제공과 차이가 있다.
- 관리자 CLI: PgBase는 create/reset-password를 제공하지만 PB superuser의 upsert/update/delete/OTP/IP 목록 관리와는 범위가 다르다. 일부 관리자 HTTP/UI 기능이 있어도 CLI 완료로 계산하지 않는다.

근거: [현재 필드 설정](../internal/schema/types.go), [고급 필드 계약](advanced-fields.md), [사용자 migration CLI](../internal/cli/user_migration.go), [생성기](../internal/usermigrate/generate.go), [hook 대조](lifecycle-parity.md), [확장 및 SDK 범위](extensions-and-client-scope.md).

Sol 에이전트가 현재 [router](../internal/api/router.go), [관리자 CLI](../internal/cli/run.go), migration 및 hook 소스를 독립 확인했다. 자동 migration·정적 사이트·CLI·세부 hook 차이를 재확인했으며 제품 코드를 변경하지 않았다.

## 검증 상태

P8 브라우저 3개 시나리오, 실제 파일 local→S3→local 왕복, populated v32→v38 업그레이드의 데이터 해시·행 수 보존과 doctor 확인은 통과했다. 백업 연결 유실 및 실제 S3 원본 객체 삭제 후 로컬 복원 바이트·원본 저장소 독립성 집중 PG18+MinIO/race도 통과했다. 최종 Go/DB/UI/SDK 로컬 회귀도 통과했다. PR·원격 CI·병합·로컬 설치는 별도 단계다. 전체 이력은 [현재 상태](project-status.md), 후속 구현은 [N0–N9](next-work.md)에 정리한다.

기능별 중요도와 세부 계약 수를 고정한 전체 분모가 없으므로 단일 진행률 백분율을 산출하지 않는다. 현재 평가는 '주요 BaaS와 관리자 작업은 제공, 세부 기능과 확장 생태계는 미완성'이다. 최신 작업 결과는 [TODO](../TODO.md)를 따른다.
