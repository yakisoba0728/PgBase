# PocketBase와 PgBase 로컬 소스 기능 비교

> 기록 범위 안내: 이 문서는 해당 단계의 계약·검증 이력을 보존합니다. 현재 전체 상태는 [구현 이력](project-status.md), 남은 작업은 [후속 작업](next-work.md), PocketBase 비교는 [현재 재평가](pocketbase-current-reassessment.md)를 따릅니다. 본문의 당시 미구현/제외 항목은 후속 단계에서 달라졌을 수 있습니다.

확인일: 2026-09-09 (Asia/Seoul).

이 표는 PgBase의 현재 작업 트리를 로컬에서 빌드하고 검증한 결과를 [고정된 PocketBase v0.40.3 기준 조사](pocketbase-upstream-capabilities-2026-09-08.md)와 비교한다. PgBase v0.1.0 릴리스 이미지의 기능표가 아니며, PocketBase API나 SDK와의 호환성을 뜻하지 않는다.

| 기능 영역 | PocketBase 기준 기능 | 현재 PgBase 로컬 소스 | 남은 차이와 검증 경계 |
| --- | --- | --- | --- |
| 컬렉션과 스키마 | Base, auth, view 컬렉션과 여러 필드·인덱스 | PostgreSQL 테이블 컬렉션, named auth collection, typed projection view, 관계, 복합 인덱스와 plan/apply 수명주기 | view는 단일 source의 projection/static filter이며 aggregation, nested view, relation target을 지원하지 않는다. PocketBase schema/API 호환은 목표가 아니다. |
| 레코드 API와 관계 | CRUD, filter/sort/expand, relation, batch | 관리자·사용자·service·anonymous CRUD, 원자적 50-operation batch, typed filter/sort, 정·역방향 및 중첩 expand, 관계 삭제 정책 | 모든 PocketBase query 문법이나 SDK 동작을 복제하지 않는다. 로컬 PostgreSQL 통합 테스트 범위만 확인했다. |
| 사용자 인증 | Auth collection, password, OAuth2, OTP/MFA, verification/reset | 기본·named realm 가입/로그인/세션, 이메일 검증·변경·재설정, OAuth2 PKCE, TOTP와 recovery code, 사용자 활성화·세션 폐기 | 외부 OAuth 실제 계정과 외부 SMTP 전달은 검증하지 않았다. 로컬 OAuth provider와 SMTP sink만 사용했다. |
| 접근 규칙 | collection별 list/view/create/update/delete rules | collection opt-in, typed action-rule AST, owner/auth 조건, relation hop·raw ID·cascade 권한, anonymous principal | 규칙 언어와 오류 계약은 PgBase 고유 형식이다. 운영 데이터 규모의 정책 비용은 별도 검증이 필요하다. |
| Service access | 관리자·사용자 token 중심 API | hash-only service bearer token, TTL/revoke, collection/action scope와 전용 route | server-to-server 로컬 경로를 검증했다. 공개 credential management 서비스나 cloud secret integration은 없다. |
| 파일 | protected file upload/download, thumbnails, storage adapters | record-bound streaming upload/list/delete, 서명된 단기 download token, local/S3 adapter, thumbnail variant | 로컬 filesystem과 로컬 S3 fixture를 검증했다. 실제 cloud object store 장애·수명주기·대규모 전송은 검증하지 않았다. |
| Realtime | record subscription과 reconnect | PostgreSQL outbox, lease/fence delivery, permission 재검사, SSE cursor/replay, TypeScript SDK의 SSE parser와 제한된 횟수의 재연결·resume | metadata event만 전송한다. 24시간 기본 retention과 짧은 로컬 부하 검증은 production delivery SLO가 아니다. |
| Hooks와 jobs | Go/JavaScript hooks, cron-like jobs와 dashboard | trusted Go mutation hook API와 Go callback으로 등록하는 leased scheduled-job 실행 수명주기 | job 관리 HTTP route나 dashboard는 없다. JavaScript extension runtime과 PocketBase hook API 호환도 없으며 사용자 callback은 cooperative cancellation을 지켜야 한다. |
| 관리자 UI | collection/auth/file/job 관리 화면 | 기존 schema/record UI에 사용자, auth collection, rules, service token 관리와 file field 편집 화면 추가 | job·realtime 운영 관리 화면은 없다. 브라우저 로컬 검증 범위이며 전체 PocketBase dashboard UX 또는 접근성·브라우저 행렬 동등성을 주장하지 않는다. |
| Client SDK | JavaScript와 Dart SDK | 로컬 TypeScript SDK: auth realm, CRUD/batch, ETag, files, pagination, SSE | `0.1.0-local` private package이며 npm에 공개하지 않았다. Dart SDK와 PocketBase SDK drop-in compatibility는 없다. |
| 배포와 운영 | 단일 실행 파일 중심 배포 | PostgreSQL 기반 app, migrations, doctor/metrics, backup/restore 및 로컬 cross-build 절차 | 이번 소스 기능에 대한 외부 cloud 배포, 공개 artifact publication, multi-hour soak, production SLO와 실제 재해복구 운영은 완료하지 않았다. |

현재 소스는 과거 v0.1.0 비교에서 비어 있던 공개 인증, 규칙, 파일, realtime, SDK 영역에 실행 가능한 로컬 경로를 추가했다. 이를 백분율 하나로 환산하지 않는다. 제품별 API 의미와 배포 모델이 다르고, 로컬 기능 존재 여부는 운영 성숙도나 성능 동등성을 증명하지 않기 때문이다.

검증 명령과 짧은 workload 범위는 [로컬 검증 기록](local-verification.md), HTTP 계약은 [공개 API 추가](public-api.md), SDK 범위는 [확장 및 클라이언트 범위](extensions-and-client-scope.md)에 기록한다. 외부 서비스와 공개 배포 검증이 끝나기 전까지 이 표의 PgBase 열은 “로컬 소스 구현”으로만 해석한다.
