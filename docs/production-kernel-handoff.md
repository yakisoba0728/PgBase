# pgBase Production Kernel 최종 핸드오프

이 문서는 `codex/production-kernel` 브랜치에 구현된 Production Kernel의 최종 상태, 계약, 검증 결과, 운영 준비도와 다음 작업을 한곳에 정리한 인수인계 기준 문서다. 전달 기준은 이 문서를 포함한 `origin/codex/production-kernel`의 최신 커밋이며, 특정 로컬 worktree나 과거 중간 커밋은 기준이 아니다.

## 1. 현재 상태

| 항목 | 상태 |
| --- | --- |
| 원격 브랜치 | `origin/codex/production-kernel` |
| 구현 기준점 | 이 문서를 포함한 원격 브랜치 HEAD |
| 구현 계획 | 15개 작업 완료 |
| 코드 리뷰 | 전체 감사 28건과 후속 독립 재감사에서 확인한 제품 정확성 1건까지 수정·재검증 완료 |
| 알려진 미해결 구현 finding | 없음 |
| 릴리스 상태 | pre-v0.1 개발 프리뷰 |
| PR / 병합 | 아직 만들거나 병합하지 않음. 이후 저장소 소유자가 원격 브랜치로 PR 처리 |

현재 결과물은 PostgreSQL 기반 관리형 백엔드의 운영 가능한 핵심이다. 관리자용 컬렉션/레코드 백엔드로는 배포 후보지만, 공개 사용자 인증·접근 규칙·관계·파일·realtime까지 제공하는 PocketBase 기능 대체 완성본은 아니다.

진행률은 서로 다른 완료 기준을 분리해 해석해야 한다.

| 기준 | 추정 진행률 | 의미 |
| --- | ---: | --- |
| 승인된 Production Kernel 스펙 | 100% | 승인된 설계와 15개 구현 작업 완료 |
| 내부/관리자용 PostgreSQL BaaS 배포 | 약 80% | 기능·보안 경계·패키징은 완료, 실제 환경 운영 검증 필요 |
| 외부 실제 서비스 배포 | 약 70% | TLS/프록시/비밀 관리, 관측성, 복구 훈련과 릴리스 운영 필요 |
| 대규모 서비스 검증 | 약 55% | 다중 인스턴스 구조는 구현됐지만 부하·장시간·장애·용량 검증 전 |
| PocketBase 핵심 기능 범위 | 약 40–45% | 현재는 관리자용 데이터/스키마 커널 중심 |

이 수치는 코드 라인 수가 아니라 실제 서비스 투입에 필요한 기능과 운영 증거를 기준으로 한 추정치다.

## 2. 확정된 제품 경계

제품 방향은 PocketBase처럼 한 번에 시작할 수 있는 관리자 UI, 스키마 관리와 애플리케이션 API 경험을 제공하되, SQLite 대신 PostgreSQL을 사용해 다중 인스턴스와 실제 서비스 운영으로 확장하는 것이다. 동일한 화면이나 내부 구현을 복제하는 것이 아니라 이 사용 감각과 배포 단순성을 PostgreSQL 중심으로 재구성한다.

- 하나의 pgBase 프로젝트가 PostgreSQL 데이터베이스 하나를 소유한다.
- 그 데이터베이스 안에서 `pgbase_meta`는 시스템 메타데이터, `app`은 사용자 컬렉션 테이블을 소유한다.
- 같은 프로젝트의 pgBase 인스턴스 여러 개가 하나의 데이터베이스를 공유할 수 있다.
- 같은 프로젝트의 모든 인스턴스는 토큰 비밀, 공개 URL, 신뢰 프록시와 관리자 origin 정책을 일관되게 공유해야 한다.
- 서로 무관한 프로젝트를 한 pgBase 데이터베이스에 넣는 구성은 지원하지 않는다.
- PostgreSQL이 유일한 영속 상태와 동시성 조정의 기준이다. 프로세스 메모리 캐시는 재생성 가능해야 한다.

여러 데이터베이스 연결 요구는 유지하되 Production Kernel 안에서 서로 무관한 DB를 한 data-plane 프로세스에 섞지는 않았다. 후속 구현은 프로젝트별 독립 DB/풀/비밀/수명주기를 가진 control plane이 여러 pgBase 프로젝트를 등록하고 관리하는 방식이 우선안이다. 이 경계는 한 DB의 장애나 자격정보가 다른 프로젝트로 전파되는 범위를 줄이고 프로젝트별 용량·백업·이전을 독립적으로 운영하게 한다. 현재 branch에는 그 control plane이나 DB registry가 아직 없다.

현재 포함된 범위:

- PostgreSQL 연결 풀, 트랜잭션 경계와 명시적 시스템 마이그레이션
- 최초 관리자 setup, 로그인, 세션, 로그아웃, CLI 관리자 생성과 암호 재설정
- 결정적 컬렉션 plan/apply와 다중 인스턴스 카탈로그 동기화
- 관리자 전용 typed CRUD와 서명된 cursor 페이지네이션
- 임베드된 React 관리자 UI
- liveness/readiness, 진단 CLI, JSON 구조화 로그와 graceful shutdown
- 비root/distroless 컨테이너, 개발용 Compose와 GitHub Actions 검증
- 배포, 백업/복원, 업그레이드와 보안 운영 문서

현재 제외된 범위:

- 공개 사용자 인증, RBAC 또는 레코드 접근 규칙
- 관계 필드, 파일 저장소, realtime 구독
- 관계 조인/expand, jsonb 내부 필터링을 포함한 범용 query 언어 (레코드 목록에는 제한적 `filter`/`sort` 표현식이 제공됨)
- 공식 SDK와 생성된 OpenAPI
- online DDL, down migration, read replica routing, sharding
- 임의의 v0.x 버전 사이 무중단 호환성 보장
- 특정 처리량, 지연 시간 또는 대규모 운영 SLO 보장

## 3. 시스템 구조

```text
Browser / API client
        |
        v
HTTP safety boundary
(request ID, recovery, limits, CORS, origin, session, CSRF)
        |
        +--> embedded React administrator UI
        |
        v
Admin auth / Schema service / Record query compiler
        |
        v
PostgreSQL pool and transactions
        |
        +--> pgbase_meta: system state and catalog
        +--> app: physical collection tables
        +--> advisory locks: serialized migrations/schema apply
        +--> LISTEN/NOTIFY: multi-instance invalidation hint
```

주요 모듈 경계:

| 경로 | 책임 |
| --- | --- |
| `pgbase` | 공개 `Options`, `New`, `App.Run`, `App.Close`, `App.Handler` 수명주기 API |
| `cmd/pgbase`, `internal/cli` | 실행 파일과 운영 명령 |
| `internal/config` | 환경 변수 파싱과 검증 |
| `internal/database` | PostgreSQL 풀, 트랜잭션과 테스트 DB 격리 |
| `internal/migrate` | 임베드된 시스템 마이그레이션과 상태 검사 |
| `internal/adminauth` | 관리자 암호, 세션, setup, rate limit과 청소 작업 |
| `internal/schema` | 컬렉션 검증, plan/apply, 카탈로그 캐시와 listener |
| `internal/query` | 타입 codec, 안전한 SQL 컴파일, CRUD와 cursor |
| `internal/api` | HTTP 라우팅, 인증/CSRF/origin과 안전한 오류 경계 |
| `internal/doctor`, `internal/observability` | 진단과 비밀정보를 제거하는 JSON 로그 |
| `ui/admin` | 빌드 결과를 Go 바이너리에 포함하는 React 관리자 UI |

## 4. PostgreSQL 데이터와 스키마 계약

시스템 마이그레이션은 다음 객체를 만든다.

- `pgbase_meta.schema_state`: 현재 컬렉션 스키마 버전
- `pgbase_meta.admins`: 관리자 계정과 Argon2id 암호 해시
- `pgbase_meta.admin_sessions`: 원문이 아닌 해시된 세션 토큰, 만료/폐기 상태
- `pgbase_meta.admin_rate_limits`: 해시된 subject 기반 로그인 제한 상태
- `pgbase_meta.collections`, `fields`, `collection_indexes`: 논리/물리 카탈로그 매핑
- `pgbase_meta.schema_migrations`: 적용 작업, checksum, 계획, SQL과 실행 감사 정보
- `app.<generated_table>`: 컬렉션별 물리 레코드 테이블

지원 필드 타입은 `boolean`, `integer`, `bigint`, `numeric`, `text`, `timestamptz`, `uuid`, `jsonb`다. 기본값도 필드 타입에 맞게 검증한다. `bigint`와 `numeric`은 JSON 숫자의 정밀도 손실을 피하도록 문자열 표현을 지원하고, 레코드 ID는 UUID다.

컬렉션 변경은 두 단계다.

1. `plan`이 정규화된 정의, 기준 카탈로그 버전, checksum과 검토 가능한 SQL을 만든다. 본문의 `operation`은 `create`, `update`, `delete` 중 하나여야 한다(감사 레코드에는 내부 라벨 `collection_create`/`collection_update`/`collection_delete`가 기록된다). update는 `fields` 키가 반드시 필요하며 생략하면 요청이 거부된다.
2. `apply`가 기준 버전과 checksum을 다시 검증한 뒤 PostgreSQL advisory lock 아래 하나의 트랜잭션으로 DDL, 메타데이터, 감사 기록과 버전 증가를 반영한다.

update는 이름 기준 필드 diff로 추가(`ADD COLUMN`), 삭제(`DROP COLUMN`), required/default/unique/indexed 플래그 변경과 화이트리스트된 확대 타입 변경(integer→bigint, integer→numeric, bigint→numeric)만 허용하고 나머지 타입 변경은 `unsupported_schema_change`로 거부한다. 기존 데이터가 변경을 위반하면(예: NULL이 남아 있는 컬럼의 SET NOT NULL) 적용이 422로 롤백된다. delete는 `DROP TABLE` 하나로 물리 테이블과 의존 인덱스를 제거한다.

식별자는 검증된 카탈로그 매핑에서만 생성하고 값은 parameter binding한다. 다른 인스턴스는 PostgreSQL `LISTEN/NOTIFY`를 invalidation 힌트로 사용하며, 알림 유실을 가정해 영속 카탈로그 버전을 다시 읽는다. listener가 동기화되지 않으면 readiness가 실패한다.

## 5. HTTP API 계약

현재 REST API는 관리자 세션용이다. mutation은 별도 표시가 없으면 유효한 관리자 쿠키, 정확한 `Origin`, 세션에서 발급한 `X-PgBase-CSRF` 헤더가 필요하다.

| Method / path | 목적 | 보호 |
| --- | --- | --- |
| `GET /health/live` | 프로세스 HTTP liveness | 공개 |
| `GET /health/ready` | HTTP, DB, migration, schema listener readiness | 공개 |
| `GET /api/admin/status` | setup 필요 여부 확인 | 공개 (setupRequired만 반환) |
| `POST /api/admin/setup` | 최초 관리자 한 번 생성 | setup token + same-origin |
| `POST /api/admin/auth/login` | 관리자 로그인 | same-origin + rate limit |
| `POST /api/admin/login` | 로그인 호환 alias | same-origin + rate limit |
| `GET /api/admin/session` | 현재 관리자와 CSRF token 조회 | 관리자 세션 |
| `POST /api/admin/auth/logout` | 현재 세션 폐기 | 관리자 세션 + mutation 보호 |
| `DELETE /api/admin/session` | 로그아웃 호환 alias | 관리자 세션 + mutation 보호 |
| `GET /api/admin/collections` | 현재 카탈로그 조회 | 관리자 인증 |
| `POST /api/admin/collections/plan` | 컬렉션 create/update/delete 계획 (`operation` 필수) | 관리자 인증 |
| `POST /api/admin/collections/apply` | 검토한 계획 적용 (`operation` 필수) | 관리자 세션 + mutation 보호 |
| `GET /api/admin/system` | 버전, DB, migration, catalog/listener 상태 | 관리자 인증 |
| `GET /api/v1/collections/{collection}/records` | 레코드 목록, `filter`/`sort` 표현식과 cursor 페이지네이션 | 관리자 세션 |
| `POST /api/v1/collections/{collection}/records` | typed 레코드 생성 | 관리자 세션 + mutation 보호 |
| `GET /api/v1/collections/{collection}/records/{id}` | 단일 레코드 조회 | 관리자 세션 |
| `PATCH /api/v1/collections/{collection}/records/{id}` | 부분 갱신 | 관리자 세션 + mutation 보호 |
| `DELETE /api/v1/collections/{collection}/records/{id}` | 삭제 | 관리자 세션 + mutation 보호 |

`/api/v1` 표기는 pre-v0.1 동안 안정성 약속이 아니다. 오류는 내부 PostgreSQL/드라이버 오류를 노출하지 않고 다음 공통 envelope를 사용한다.

```json
{
  "error": {
    "code": "stable_code",
    "message": "Safe client-facing message",
    "details": {},
    "requestId": "request-correlation-id"
  }
}
```

## 6. 관리자 보안 계약

- setup token과 token secret은 최소 길이를 검증하며, token secret은 디코딩 후 32바이트 이상이어야 한다.
- 관리자 암호는 Argon2id로 해시한다. 로그인 실패 제한 상태는 PostgreSQL에 있어 다중 인스턴스가 공유한다.
- 세션 원문은 쿠키에만 전달하고 DB에는 HMAC 해시만 저장한다. 쿠키는 `HttpOnly`, `SameSite=Lax`이며 외부 origin이 HTTPS이면 `Secure`다.
- 브라우저 mutation은 정확한 origin 검증과 세션 결합 CSRF 검증을 통과해야 한다.
- forwarded scheme/client IP는 `PGBASE_TRUSTED_PROXIES`에 포함된 실제 peer에서만 신뢰한다.
- CORS는 정확한 origin allowlist만 허용하고 wildcard를 거부한다.
- JSON body 크기와 unknown/trailing JSON을 제한하며, panic recovery와 안전한 오류 변환을 적용한다.
- 로그는 request ID와 구조화 필드를 제공하고 알려진 자격정보를 제거한다. DSN query/keyword 키는 인코딩을 해제해 분류하며 공백·escape를 포함한 값 전체를 제거한다. 비밀을 로그에 넣지 않는 운영 원칙은 별도로 유지해야 한다.
- token secret 교체는 관리자 세션, CSRF, signed cursor와 rate-limit hash namespace를 무효화한다.
- 관리자 암호 재설정은 해당 관리자의 기존 세션을 모두 폐기한다.

보안 취약점 보고와 지원 범위는 [Security policy](../SECURITY.md)를 따른다.

## 7. CLI와 설정

| 명령 | 용도 |
| --- | --- |
| `pgbase serve` | HTTP 서버와 백그라운드 구성요소 실행 |
| `pgbase migrate status` | 시스템 migration의 current/latest/dirty 확인 |
| `pgbase migrate up` | 임베드된 시스템 migration 적용 |
| `pgbase admin create --email EMAIL [--password-stdin]` | 운영자 관리자 생성 |
| `pgbase admin reset-password --email EMAIL [--password-stdin]` | 암호 변경과 세션 폐기 |
| `pgbase doctor [--json]` | DB와 migration 진단 |
| `pgbase healthcheck --url URL [--timeout DURATION]` | shell/curl 없는 컨테이너 health probe |

핵심 환경 변수는 `PGBASE_DATABASE_URL`, `PGBASE_TOKEN_SECRET`, `PGBASE_SETUP_TOKEN`, `PGBASE_HTTP_ADDRESS`, `PGBASE_PUBLIC_URL`, `PGBASE_AUTO_MIGRATE`, `PGBASE_TRUSTED_PROXIES`, `PGBASE_CORS_ALLOWED_ORIGINS`, `PGBASE_ADMIN_SESSION_TTL`, `PGBASE_STATEMENT_TIMEOUT`, `PGBASE_SHUTDOWN_TIMEOUT`다. 정확한 요구사항과 기본값은 [Deployment and configuration](deployment.md#configuration)을 기준으로 한다.

Go에 임베드할 때는 `pgbase.Options`를 구성해 `pgbase.New(ctx, options)`를 호출한다. 내장 HTTP 서버는 `Run`을 사용하고, 호출자가 서버를 소유할 때는 반드시 `Start`를 성공시킨 뒤 `Handler`를 제공하고 마지막에 `Close`한다. `Start`된 앱을 다시 `Run`할 수는 없다. `New`는 pgBase worker나 HTTP listener를 시작하지 않지만, 이 단계에서 생성하는 `pgxpool`은 내부 연결 유지 goroutine을 실행할 수 있다.

## 8. 관리자 UI

`/admin`은 React SPA를 Go 바이너리에 임베드해 제공한다. 구현된 화면은 다음과 같다.

- 최초 관리자 setup과 로그인/로그아웃
- 컬렉션 목록과 컬렉션 생성 plan/apply
- 컬렉션 편집(필드 추가/삭제/플래그 변경 plan/apply)과 2단계 삭제 확인
- 레코드 목록(filter 표현식, 정렬 가능 컬럼 헤더, cursor 페이지네이션), 생성, 수정, 삭제
- 애플리케이션/DB/migration/catalog/listener 시스템 상태

프론트엔드 소스와 커밋된 `ui/admin/dist`는 CI에서 재빌드 후 diff가 없어야 한다.

## 9. 수명주기, 상태 확인과 패키징

- `/health/live`는 HTTP 경로가 살아 있으면 DB 장애와 무관하게 200을 반환한다.
- `/health/ready`는 HTTP가 요청을 받고 있고 DB가 연결되며 시스템 migration이 최신이고 schema listener가 동기화될 때만 200이다.
- SIGTERM 또는 context 취소 시 readiness를 먼저 내리고 새 요청을 차단한 뒤 HTTP와 백그라운드 작업을 `PGBASE_SHUTDOWN_TIMEOUT` 안에 drain하고 풀을 닫는다.
- 컨테이너는 Go 1.26.7로 정적 빌드하며 distroless `nonroot` 런타임을 사용한다. shell을 포함하지 않는다.
- 개발 Compose는 PostgreSQL 16.13을 사용하고 loopback port, read-only pgBase filesystem, 제한된 tmpfs, all-capability drop과 no-new-privileges를 적용한다.
- 개발 Compose만 `PGBASE_AUTO_MIGRATE=true`다. 운영에서는 false 또는 unset 상태에서 별도 migration job을 사용한다.
- tag release는 digest로 고정한 BuildKit `docker-container` builder를 사용하고 tag 원문을 binary `VERSION`에 주입한다. `/api/admin/system`에서 실행 중인 이미지 버전을 확인할 수 있다.

## 10. 검증 증거

전체 수정과 후속 재감사를 반영한 전달 브랜치에서 다음 검증을 통과했다.

| 영역 | 검증 |
| --- | --- |
| Go | live PostgreSQL을 사용한 `go test ./... -count=1` |
| 정적 검사 | `go vet ./...` |
| 동시성 | Linux / Go 1.26.7에서 `go test -race ./... -count=1` |
| Frontend | integrity-bound `npm ci`, typecheck, unit/contract test, production build |
| Embedded UI | 재빌드한 `ui/admin/dist`와 커밋 결과가 동일 |
| Black-box E2E | digest-pinned PostgreSQL 16.13 Compose에서 `-count=2 -shuffle=on` 실행 |
| E2E 범위 | 실제 Chromium 온보딩, bootstrap/CRUD/restart, DB outage/recovery, two-instance schema sync, graceful drain, 이미지 버전 주입 |
| CI PostgreSQL matrix | 16.13, 17.9, 18.3 |
| Container | nonroot, shell 없음, hardened Compose runtime 검사, digest-pinned BuildKit bootstrap |
| Review | 4개 병렬 영역 재감사, 교차 검증과 잔여 finding 수정 완료 |

GitHub Actions는 Go formatting/vet/race와 benchmark artifact, 프론트엔드 typecheck/test/build/dist 재현성, Docker black-box E2E를 각각 gate한다. 이 검증은 구현 정확성의 근거지만 실제 트래픽의 용량·지연·장시간 안정성 증거를 대신하지 않는다.

## 11. 운영 배포 계약

운영 배포 순서는 다음과 같다.

1. `pgbase_meta`와 `app`을 함께 백업한다.
2. 새 바이너리로 `pgbase migrate status`를 실행한다.
3. 지명한 migration job 하나에서 `pgbase migrate up`을 실행한다.
4. 다시 status를 확인해 `current == latest`, `dirty=false`를 요구한다.
5. `pgbase serve` 인스턴스를 rollout한다.
6. 각 인스턴스의 `/health/ready`가 200일 때만 load balancer에 넣는다.

원격 PostgreSQL은 검증된 TLS를 사용하고, pgBase의 HTTP TLS는 reverse proxy/load balancer에서 종료한다. orchestrator termination grace는 shutdown timeout보다 길어야 한다. 상세 절차는 [Deployment](deployment.md), [Backup and restore](backup-restore.md), [Upgrades](upgrades.md)를 따른다.

현재 무중단 upgrade는 보장하지 않는다. collection DDL은 PostgreSQL lock을 획득할 수 있고 system migration은 maintenance window가 필요할 수 있다. 릴리스별 호환성이 증명되지 않았다면 stop-migrate-start를 우선한다.

## 12. 남은 위험과 다음 우선순위

구현 기준점에 알려진 미해결 코드 finding은 없지만, 실제 서비스와 대규모 운영 완료를 주장하려면 다음 증거와 기능이 필요하다.

### P0: 첫 실제 서비스 배포 전

1. staging에 실제 TLS proxy/load balancer, 비밀 관리, 외부 PostgreSQL을 구성하고 rollout/rollback을 훈련한다.
2. 요청률/지연/error, DB pool, migration/listener/readiness, 세션/rate-limit 지표와 경보/대시보드를 추가한다.
3. 목표 트래픽 모델로 load, soak, spike, DB 장애와 network partition 테스트를 실행하고 용량/SLO를 기록한다.
4. 운영 방식의 backup/PITR와 별도 환경 restore drill을 실행하고 RPO/RTO를 확정한다.
5. 실제 registry tag release, 서명 검증 정책, release note와 migration 호환성 승인을 운영 절차로 확정한다. 이미지·builder digest, provenance와 SBOM 생성 경로는 구현되어 있다.
6. 외부 또는 독립 보안 리뷰, 의존성/이미지 scan과 incident runbook을 완료한다.

### P1: PocketBase형 공개 애플리케이션 백엔드

1. public user collection/authentication과 이메일 검증·암호 재설정 정책
2. collection/record 단위 RBAC 또는 규칙 엔진과 감사 가능 정책 평가
3. API token/service account와 관리자 역할 분리
4. 공식 OpenAPI 계약, 호환성 정책과 최소 SDK

### P2: 기능 폭과 확장성

1. relationship와 안전한 join/populate 모델
2. object storage 기반 file field와 upload/download authorization
3. PostgreSQL 기반 realtime change delivery와 재연결/유실 계약
4. 제한적 filter/sort/index 모델
5. online schema change 전략, read replica, tenant/project control plane 검토

## 13. 브랜치 인수인계와 재개 방법

원격 브랜치가 전달 기준이며 로컬 worktree나 로컬 merge 상태는 인수인계 계약이 아니다.

새 clone 또는 브랜치가 없는 checkout:

```console
git fetch origin
git switch --track origin/codex/production-kernel
```

이미 로컬 브랜치가 있을 때:

```console
git switch codex/production-kernel
git pull --ff-only
```

새 환경은 Go 1.26.7, Node 24, npm 11, Docker Engine/Compose/Buildx를 준비한다. clone 직후 다음 순서로 전달 상태를 확인한다.

```console
go test ./... -count=1
go vet ./...
cd ui/admin
npm ci --ignore-scripts
npm run typecheck
npm test -- --run
npm run build
cd ../..
go test -tags=e2e ./tests/e2e -v -count=2 -shuffle=on
```

E2E는 임시 Compose 프로젝트, 포트와 비밀을 자체 생성하고 종료 시 제거한다. 로컬 서비스 시작은 [10-minute quickstart](quickstart.md), 운영 배포는 [Deployment](deployment.md)를 따른다.

향후 PR 전에는 target branch를 fetch한 다음 전체 branch diff와 CI를 다시 검토한다. 이 handoff에서는 PR 생성, target branch로의 로컬 merge, worktree 삭제를 하지 않는다.

## 14. 문서와 소스 지도

| 문서/경로 | 역할 |
| --- | --- |
| [README](../README.md) | 프로젝트 진입점과 현재 범위 |
| [Tradeoffs](production-kernel-tradeoffs.md) | 확정된 결정의 이점·비용·재검토 조건과 의도적으로 남긴 위험 |
| [Codebase audit](codebase-audit-2026-08-28.md) | 최초 finding, 수정 증거와 후속 재감사 결과 |
| [10-minute quickstart](quickstart.md) | 로컬 첫 실행과 첫 CRUD |
| [Deployment and configuration](deployment.md) | 운영 topology, 설정, TLS/proxy, rollout, secret rotation |
| [Backup and restore](backup-restore.md) | 두 schema의 일관된 backup/restore와 drill |
| [Upgrades](upgrades.md) | pre-v0.1 upgrade/rollback 절차와 제한 |
| [Security policy](../SECURITY.md) | 취약점 신고와 지원 정책 |
| [Approved design](superpowers/specs/2026-08-28-production-kernel-design.md) | 승인된 제품/아키텍처 스펙 |
| [Implementation plan](superpowers/plans/2026-08-28-production-kernel.md) | 15개 작업의 구현 기록과 acceptance 기준 |
| [Original pgBase v0.1 Notion page](https://app.notion.com/p/pgBase-v0-1-3c9d9bc767f081ce8934e9cd22bdc85b?source=copy_link) | 초기 제품 메모와 기획 출처 |
| `migrations/system` | 실제 PostgreSQL system schema의 최종 기준 |
| `internal/api/router.go` | 실제 HTTP route의 최종 기준 |
| `internal/config`, `pgbase/options.go` | 설정과 공개 Go option의 최종 기준 |
| `.github/workflows`, `deployments` | CI와 컨테이너/Compose 실행 계약 |

상태 요약은 이 handoff를 기준으로 하고, 구체적인 동작이 문서와 다를 경우 현재 branch의 코드와 테스트를 최종 실행 계약으로 본다. 승인 당시의 의도와 결정 배경은 design, 구현 순서와 검증 내역은 implementation plan을 참고한다.
