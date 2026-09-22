# 운영·배포 소스 대조

2026-09-22. PocketBase v0.40.4 `5cec579da984436a258602a46a96302fbd31f77c` 대 현재 PgBase 작업 트리. 기존 operations 22행 + 추가 OPS-26을 읽었다. 기능 유무와 wire compatibility를 구분했다.

## 중요한 차이

- **OPS-07 / P1**: `posts:list`, `*:auth` 같은 PB collection-action limiter 라벨을 저장해도 PgBase의 실제 admission은 경로 라벨만 전달한다. 경로 규칙은 정상 구현되어 있다.
- **OPS-19 / P1**: PB CronMaxKeep은 자동 백업만 정리한다. PgBase는 수동 및 업로드까지 전체 repository 개수를 유지한다. 기존 테스트도 upload가 이전 수동 백업을 지우는 결과를 검증한다. 보존 정책이 같은 것으로 안내하면 안 된다.
- **OPS-05 / P2**: 목록/상세/통계/삭제는 있으나 PB의 일반 filter 및 filtered stats를 지원하지 않는다.
- **OPS-08, OPS-23**: 임의 trusted proxy header 및 SMTP 없는 CLI OTP 기능은 PB와 다르다.
- **OPS-26**: 업데이트 구현/CI 파일이 있다고 여러 OS의 공개 release와 실제 교체 인수가 끝난 것은 아니다.

## 구현 근거가 있는 영역

PostgreSQL 온라인 snapshot 백업, local/S3 객체 복원, managed activation, 암호화 설정, 내장 HTTPS/ACME, durable jobs, 서명된 self-update, CLI 관리, health, 정적 사이트는 실제 실행 경로가 있다. SQLite→PostgreSQL 및 관리자 쿠키 보안 정책에 따른 차이는 `adapted`로 기록했다. 특히 PB 공식 backup은 원격 S3 record 파일을 제외하지만 PgBase는 해당 object snapshot도 수집하므로 단순 하위집합이라고 볼 수 없다.

## 이번 실행 검증

`go test ./internal/api ./internal/config ./internal/backup -run 'TestCORS|TestRequestLogRecordsRecoveredPanicAs500|TestLiveReportsProcessHealth|TestReady|TestLoadEnv.*Encryption|TestManagerSerializesPersistsAndRecoversJobs' -count=1 -v`

세 package PASS. 로그: [operations-tests-output.txt](operations-tests-output.txt). 이것은 PgBase 현재 동작 검사이며 PB와 양쪽 서버를 실행한 conformance 테스트가 아니다. DB/S3 통합 및 전체 Go/UI/SDK 실행은 [이전 작업 검증](../../development-completion-2026-09-22.md)과 구분한다. 공개 CA, 실제 외부 SMTP, native Windows updater, 공개 release를 이번에 실행하지 않았다.

## 행별 대조

각 행의 테스트는 명시한 verification 수준으로만 해석한다.

### OPS-01 serve CLI/runtime — adapted

- PB: serve [domains]는 기본 127.0.0.1:8090과 내장 SQLite로 시작한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/cmd/serve.go#L14)
- PgBase: serve → config → RunManaged; PostgreSQL URL/토큰키와 외부 DB가 필요하다. (internal/cli/run.go:173, internal/config/env.go:37, pgbase/managed.go:1)
- 차이/제한: 같은 시작 경험/플래그가 아니다. SQLite 단일 파일 배포 대신 PostgreSQL 운영이 필요하다.
- 근거: internal/cli/run_test.go; test-exists; none

### OPS-02 통합 runtime settings GET/PATCH/reload — adapted

- PB: Settings는 SMTP/S3/meta/logs/backups/rateLimits 등을 한 모델로 저장·조회한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/settings_model.go#L123)
- PgBase: 암호화 runtime settings와 revision/reload/subscribe 지원. SMTP·storage는 별도 관리 API. (internal/runtimesettings/settings.go:80, internal/runtimesettings/settings.go:180, internal/runtimesettings/settings.go:232)
- 차이/제한: PB settings JSON을 그대로 주고받을 수 없다. 기능별 설정 모델과 reload 계약이 다르다.
- 근거: internal/runtimesettings/settings_integration_test.go; test-exists; none

### OPS-05 request logs list/detail/stats/truncate — partial

- PB: filter/sort/page로 id,created,level,message,data.*를 검색하고 filter를 적용한 통계를 얻는다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/logs.go#L25)
- PgBase: before/statusClass/method/route/from/to 검색, 상세/삭제/통계 구현. (internal/api/operations_console.go:45, internal/api/operations_console.go:83)
- 차이/제한: PB filter 언어 및 arbitrary data 검색 없음. 통계 API는 요청 filter를 전달하지 않는다.
- 근거: internal/api/operations_console_test.go:137; test-exists; P2

### OPS-06 login/OTP rate limits — adapted

- PB: 기본 auth 경로 규칙을 전역 limiter와 IP 기준으로 적용한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/middlewares_rate_limit.go#L32)
- PgBase: 계정·네트워크 로그인 실패, 등록 quota를 DB로 저장하고 재시도 제한한다. (internal/runtimesettings/settings.go:51, internal/adminauth/service.go:275)
- 차이/제한: 보호 기능은 있으나 집계 단위/해제 시점과 설정 모델이 PB와 다르다.
- 근거: internal/adminauth/service_integration_test.go:262; test-exists; none

### OPS-07 PB-style configurable global API rate rules — partial

- PB: collectionName:action, *:action, 경로/접두사, audience 및 superuser 제외를 지원한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/middlewares_rate_limit.go#L103)
- PgBase: DB quota, 경로/HTTP메서드/접두사·audience·IP제외 지원. (internal/apiratelimit/service.go:40, internal/api/middleware.go:23)
- 차이/제한: Admit는 method+path, path, / 라벨만 조회하여 PB collection/action 태그가 적용되지 않는다. 관리자도 @auth quota 대상이고 PB superuser bypass와 다르다.
- 근거: internal/api/api_rate_limit_integration_test.go:19, pgbase/api_rate_limit_service_actor_integration_test.go:14; test-exists; P1

### OPS-08 trusted proxy/real client IP — partial

- PB: 설정된 헤더 목록과 UseLeftmostIP로 실제 IP를 결정한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/event_request.go#L40)
- PgBase: 신뢰 CIDR에서 온 요청의 X-Forwarded-For만 오른쪽부터 검증한다. (internal/api/client_address.go:24, internal/runtimesettings/settings.go:37)
- 차이/제한: PB의 custom header/leftmost 옵션 없음. 안전한 CIDR 모델은 설계 차이지만 CF-Connecting-IP 등 기존 설정은 이식되지 않는다.
- 근거: internal/api/client_address_test.go; test-exists; P2

### OPS-09 built-in HTTPS/ACME serve — adapted

- PB: autocert, HTTP challenge/redirect, www 별칭을 제공한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/serve.go#L149)
- PgBase: 정적 인증서 또는 명시적 호스트 allowlist ACME, 캐시, challenge/redirect와 managed restore 유지. (pgbase/tls.go:17, pgbase/app_construct.go:423)
- 차이/제한: 구성 플래그/도메인 자동 추론이 다르다. 실제 공개 CA 발급·갱신은 이번 감사에서 실행하지 않았다.
- 근거: pgbase/tls_integration_test.go:31, pgbase/acme_mock_integration_test.go:32, pgbase/managed_tls_integration_test.go:86; test-exists; none

### OPS-10 backup CRUD/transfer queue — adapted

- PB: ZIP 백업을 생성/목록/업로드/다운로드/삭제/복원한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/backup.go#L17)
- PgBase: tar.gz+manifest, 비동기 job receipt, SHA 검증, upload/download/delete/restore 구현. (internal/api/operations_console.go:173, internal/backup/manager.go:269)
- 차이/제한: PB ZIP/파일이름·응답·라우트와 호환되지 않는다. 백업 교차 복원은 지원하지 않는다.
- 근거: internal/backup/archive_test.go:95, pgbase/managed_integration_test.go:24; executed-now; none

### OPS-11 DB와 파일의 일관된 백업·복원 — adapted

- PB: SQLite VACUUM INTO와 local pb_data 파일을 백업; 원격 S3 파일은 별도 관리한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/backup_create.go#L28)
- PgBase: exported PG snapshot+GC gate에서 ready files/variants/extension_objects 포함, 새 DB/스토리지 대상 복원·managed activation. (internal/backup/online.go:1, pgbase/backup_options.go:73, internal/backup/extension_restore_integration_test.go:1)
- 차이/제한: PG 백업 포맷과 복구 절차다. local/S3 source 삭제 후 확장 객체 바이트 복원을 이전 작업에서 확인. restore는 RunManaged 또는 Activate가 필요하다.
- 근거: internal/backup/online_integration_test.go, internal/backup/extension_restore_integration_test.go, pgbase/managed_integration_test.go; prior-run; none

### OPS-12 backup S3 repository — adapted

- PB: 백업 저장소용 별도 S3 설정을 사용한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/backup_create.go#L109)
- PgBase: 독립 S3 저장소, archive/metadata 검증 및 checksum 제공. (internal/backup/s3_repository.go:1, pgbase/backup_options.go:107)
- 차이/제한: PgBase의 archive/metadata 쌍으로 저장하며 PB ZIP repository와 호환되지 않는다.
- 근거: internal/backup/s3_repository_test.go:43, internal/backup/backup_integration_test.go; test-exists; none

### OPS-13 durable scheduled jobs/cron — adapted

- PB: 메모리 cron 등록, 관리자 목록 및 즉시 실행 지원. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/cron.go#L15)
- PgBase: DB durable jobs, timezone, 활성화/스케줄 수정, 실행 요청/claim 지원. (internal/jobs/service.go:1, internal/api/operations_console.go:113, pgbase/jobs_options.go:13)
- 차이/제한: in-process PB cron과 실행·중복·재시도 모델이 다르다. 동적 JS cron API 공백은 EXT 행에서 다룬다.
- 근거: internal/jobs/management_integration_test.go; test-exists; none

### OPS-15 self-update — adapted

- PB: GitHub release 아카이브로 실행 파일을 업데이트한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/ghupdate/ghupdate.go#L35)
- PgBase: 서명된 manifest, artifact checksum, schema bounds, phase journal, probe/rollback, Windows helper 분기. (internal/selfupdate/install.go:61, internal/selfupdate/install.go:181)
- 차이/제한: PB release feed는 사용할 수 없다. updater 구현 존재와 실제 배포 feed/Windows 전체 교체 인수는 별개다.
- 근거: internal/selfupdate/selfupdate_test.go:162, internal/selfupdate/selfupdate_test.go:236; test-exists; none

### OPS-16 panic 복구 후 request-log 기록 — equivalent

- PB: API middleware는 panic을 복구하여 서버 오류/로그로 처리한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/middlewares.go#L253)
- PgBase: recovery 아래의 응답 상태를 request telemetry가 500으로 관측한다. (internal/api/middleware.go:127, internal/api/router.go:305)
- 차이/제한: 이 좁은 오류 처리 계약에 한해 일치. 전체 로그 wire 포맷 일치를 뜻하지 않는다.
- 근거: internal/api/telemetry_test.go:250, operations-tests-output.txt; executed-now; none

### OPS-17 persisted settings encryption key environment — adapted

- PB: 지정한 encryption env 키로 저장된 settings를 암호화/복호화한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/settings_query.go#L54)
- PgBase: 환경변수명 선택/CLI override와 base64 키 검증, AES-GCM 설정 암호화·rotate 제공. (internal/config/env.go:18, internal/runtimesettings/settings.go:275, internal/runtimesettings/settings.go:315)
- 차이/제한: 환경변수명/키 인코딩/암호문이 다르므로 기존 PB 암호화 settings를 읽는 기능은 아니다.
- 근거: internal/config/env_test.go:41, internal/config/env_test.go:57; executed-now; none

### OPS-18 내장 자동 백업 스케줄 — adapted

- PB: backups.cron 변경을 bootstrap/settings reload에 반영한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/backup.go#L33)
- PgBase: managed-backup durable job에 schedule/timezone/enabled를 반영한다. (pgbase/backup_schedule.go:17)
- 차이/제한: 명시적 enabled와 durable job 모델이다. 스케줄문법·저장형식은 PB와 동일하지 않다.
- 근거: internal/runtimesettings/settings_test.go, pgbase/managed_integration_test.go; test-exists; none

### OPS-19 백업 보관 개수 제한 — partial

- PB: CronMaxKeep은 @auto_pb_backup_ 접두사의 자동 백업만 정리한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/backup.go#L69)
- PgBase: publish/upload 후 repository 전체를 Keep 개수로 정리한다. (internal/backup/manager.go:372, internal/backup/manager.go:378)
- 차이/제한: 수동·업로드 백업도 보관 한도에 포함돼 제거될 수 있다. PB 자동백업 보존 정책과 다른 데이터 보존 동작이다.
- 근거: internal/backup/archive_test.go:95, operations-tests-output.txt; executed-now; P1

### OPS-20 자동 백업 실패 관리자 메일 알림 — adapted

- PB: 자동 백업 실패를 전체 superuser에게 알린다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/backup.go#L48)
- PgBase: 최종 실패 managed job receipt 후 active admins에 SMTP 재시도/최소간격을 적용해 알린다. (pgbase/backup_alert.go:29, pgbase/backup_alert.go:40, internal/backup/manager.go:269)
- 차이/제한: 시작 시 options.SMTP로 만든 발신자를 사용한다. SMTP설정 reload 일치는 별도 확인 필요; 실제 외부 메일 전달은 미실행.
- 근거: internal/backup/managed_failure_test.go, pgbase/backup_alert_test.go:21, pgbase/tls_integration_test.go:64; test-exists; none

### OPS-21 HTTP health 확인 — adapted

- PB: /api/health가 code/message/data와 관리자 부가정보를 반환한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/health.go#L13)
- PgBase: /health/live,/health/ready, 관리 system health 제공; ready는 의존성 장애503. (internal/api/health.go:14, internal/api/router.go:95)
- 차이/제한: 라우트와 JSON 다름. PB SDK health.check를 그대로 연결할 수 없다.
- 근거: internal/api/health_test.go:16, internal/api/health_test.go:34; executed-now; none

### OPS-22 관리자 CLI create/update/upsert/delete — adapted

- PB: superuser 하위명령으로 관리자를 생성/수정/삭제한다. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/cmd/superuser.go#L16)
- PgBase: admin 명령과 stdin/terminal 비밀값으로 생성·upsert·암호변경·삭제 제공. (internal/cli/run.go:255)
- 차이/제한: PB _superusers 레코드가 아니라 별도 admins 저장소와 플래그 계약이다.
- 근거: internal/cli/admin_integration_test.go:130; test-exists; none

### OPS-23 관리자 CLI OTP·IP 정책 — partial

- PB: superuser otp는 SMTP 없이 OTP를 만들고 출력; ips는 전역 allowlist 설정. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/cmd/superuser.go#L171)
- PgBase: admin otp email은 SMTP 발송 후 receipt를 출력, TOTP enable/disable도 지원. IP는 관리자별. (internal/cli/run.go:334)
- 차이/제한: 메일 서버가 없는 상태의 PB 긴급 CLI OTP 발급과 일치하지 않는다. 전역 IP 정책 API도 다르다.
- 근거: internal/cli/admin_integration_test.go:185, pgbase/n2_n3_integration_test.go:72; test-exists; P2

### OPS-24 CORS와 preflight — adapted

- PB: 기본 wildcard origin, HEAD 등 메서드 및 설정 가능한 headers/credentials. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/middlewares_cors.go#L1)
- PgBase: 명시적 origin allowlist, 제한된 헤더/메서드, 관리자 same-origin 권한 유지. (internal/api/middleware.go:361)
- 차이/제한: wildcard는 의도적으로 거절; HEAD/임의 header preflight는 미지원. PB 기본 CORS 설정을 그대로 사용할 수 없다.
- 근거: internal/api/middleware_test.go:201, internal/api/middleware_test.go:231; executed-now; none

### OPS-25 사용자 정적 사이트·SPA fallback — adapted

- PB: pb_public/커스텀 정적 filesystem 및 index fallback 제공. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/base.go#L103)
- PgBase: 명시적 public directory, SPA fallback, 예약 API/admin 경로 우선순위 보존. (pgbase/app_construct.go:219, internal/api/router.go:324)
- 차이/제한: 기본 디렉터리 탐색과 경로 설정은 PgBase 계약이다.
- 근거: pgbase/n2_n3_integration_test.go:24; test-exists; none

### OPS-26 배포 패키징·다중 플랫폼 release 경로 — partial

- PB: 공식 release workflow와 GoReleaser로 다중 OS/아키텍처 실행파일 배포. [공식 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/.github/workflows/release.yaml#L7)
- PgBase: CI backend/frontend/E2E/security/backup gate와 signed container workflow 존재. (.github/workflows/release-gate.yml:1, .github/workflows/release-image.yml:75)
- 차이/제한: 현재 공개 image workflow는 linux/amd64다. 동일 소스의 Windows/macOS/arm64 공개 릴리스 및 업데이트 feed 인수 증거는 이번 조사에 없다.
- 근거: scripts/support/validate-release-candidate.sh; static-only; P2


## 읽은 소스 범위

PB cmd/serve.go,cmd/superuser.go,apis/serve.go,apis/settings.go,apis/logs.go,apis/cron.go,apis/health.go,apis/middlewares_rate_limit.go,apis/middlewares_cors.go,core/settings_model.go,core/settings_query.go,core/event_request.go,core/backup.go,core/backup_create.go,core/backup_restore.go,plugins/ghupdate/ghupdate.go,.github/workflows/release.yaml. PgBase의 행별 pgSource 및 테스트, 운영 route 테이블과 release workflow를 확인했다. PB migration/log DB deadlock 수정은 SQLite main/aux 연결 구현에 관한 것이므로 PostgreSQL 코드가 동일 버그를 가진다고 추정하지 않았다.
