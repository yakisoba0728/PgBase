# 구조와 API 호환 경계

비교 대상은 PocketBase v0.40.4와 PgBase `6993744` + 현재 미커밋 구현이다. 기능별 표에서 `adapted`는 유용한 대응 구현이 있지만 계약이 달라 이식 작업이 필요하다는 뜻이다.

## 핵심 구조

| 항목 | PocketBase | 현재 PgBase | 해석 |
|---|---|---|---|
| 데이터베이스 | 내장 SQLite main/aux DB | 별도 PostgreSQL, typed app tables + metadata + relation edges | 의도한 기반 설계 차이. SQLite가 없다고 미구현으로 세지 않는다. |
| 실행·설치 | 실행파일과 pb_data 중심 | Go 실행파일·embedded UI 외 PostgreSQL 및 필수 secret 설정 | 단일 실행파일만으로 같은 설치 경험을 제공하지 않는다. |
| 관리자 인증 | _superusers auth records와 bearer token | 별도 admins, same-origin cookie session와 CSRF | 단순 URL 치환으로 공식 SDK의 superuser flow를 이식할 수 없다. |
| 사용자 인증 | collection record와 JWT 기반 응답 | auth realm·credential/profile 저장, DB opaque token | 토큰 decode·만료 추론·갱신 모델 차이. |
| 스키마 변경 | collection API와 SQLite DDL 동기화 | plan/preview/checksum/apply/operation receipt | 안전한 변경 절차를 별도 계약으로 구현. |
| 레코드 동시성 | PB record mutation 계약 | revision/ETag/If-Match, 생성 idempotency key | 호출자는 추가 입력과 conflict 처리를 구현해야 한다. |
| 조회 | page/perPage 및 count 중심 | native signed cursor + PB 형태 page/count adapter | compat query 일부가 있어도 전체 PB parser·serializer 일치가 아니다. |
| 백업 | ZIP + local pb_data; 원격 S3 파일 제외 | PG exported snapshot + local/S3 object + manifest tar.gz | 별도 복구 체계이며 PB archive를 직접 복원할 수 없다. |
| 확장 | PB App/Model/dbx/hooks API와 JS binding | PgBase Go API + trusted synchronous Goja facade | 일부 이름이 같아도 call signature·event timing까지 일치하지 않는다. |

PB 근거: [공식 README](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/README.md), [serve](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/cmd/serve.go#L14), [CRUD](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L23), [backup](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/backup_create.go#L28). PgBase 근거는 아래 route table 및 [schema/fields](schema-fields.md), [auth/rules/mail](auth-rules-mail.md), [records/query](records-query.md), [operations](operations.md)의 코드·테스트 링크를 따른다.

## 대표 endpoint 대조

| 목적 | PocketBase | PgBase |
|---|---|---|
| 일반 레코드 | /api/collections/{collection}/records | /api/v1/collections/{collection}/records (관리자), /api/v1/user/collections/{collection}/records (사용자), 별도 public/service 경로 |
| 컬렉션 관리 | /api/collections | /api/admin/collections 및 /plan, /apply |
| password login | /api/collections/{collection}/auth-with-password | /api/v1/auth/{authCollection}/login |
| auth refresh | /api/collections/{collection}/auth-refresh | /api/v1/auth/{authCollection}/refresh |
| 관리자 UI | /_/ | /admin/ |
| 파일 공개 URL | /api/files/{collection}/{record}/{filename} | /api/files/{fileId} |
| 배치 | /api/batch | /api/v1/collections/batch 및 user/service 경로 |
| realtime | GET/POST /api/realtime | GET/POST /api/realtime 존재. 인증·topic options·재연결과 수명 차이는 RT 행 참조 |
| health | /api/health | /health/live, /health/ready |
| logs | /api/logs 및 /stats | /api/admin/logs 및 /statistics |

PB: [record route](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L23), [auth route](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth.go#L23), [file route](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file.go#L31), [health](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/health.go#L13), [logs](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/logs.go#L13).
PgBase: [router](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/router.go:94), [auth routes](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/auth_collections.go:55), [file routes](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:45), [realtime routes](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:109), [operations routes](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/operations_console.go:45).

**결과:** 공식 PocketBase JS/Dart SDK, 기존 pb_hooks, PB schema JSON·DB/backup를 그대로 바꾸어 끼울 수 있는 대체 제품은 아니다. PgBase 자체 API/SDK로 개발하는 데 필요한 주요 기능은 존재한다. 앞으로 “PgBase 계약을 완성할 것인지”와 “PB wire/API까지 추가로 호환할 것인지”를 각 backlog 항목의 완료 기준에 명시해야 한다. 이 문서는 전자의 장점을 후자의 완료 증거로 계산하지 않는다.
