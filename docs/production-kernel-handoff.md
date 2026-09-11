# pgBase Production Kernel 최종 핸드오프

## Status applicability

This is a historical handoff. Baseline SHA: `03a0fc1`; verification SHA: `53a474143e117251d9b322441508ac85d776ef2a`. Its claims apply to that handoff snapshot; use [`pocketbase-status-index.json`](pocketbase-status-index.json) to distinguish it from current status.

> 기록 범위 안내: 이 문서는 해당 단계의 계약·검증 이력을 보존합니다. 현재 전체 상태는 [구현 이력](project-status.md), 남은 작업은 [후속 작업](next-work.md), PocketBase 비교는 [현재 재평가](pocketbase-current-reassessment.md)를 따릅니다. 본문의 당시 미구현/제외 항목은 후속 단계에서 달라졌을 수 있습니다.

이 문서는 `main`에 병합된 Production Kernel과 이후 데이터 커널·운영 계측·Record Operations·relation/schema lifecycle 확장의 상태, 계약, 검증 결과, 운영 준비도와 다음 작업을 한곳에 정리한 인수인계 기준 문서다. PR #8의 병합 기준점은 `6dd81f9`이며, 그 이후 변경은 리뷰를 거쳐 `main`에 반영된 커밋만 원격 전달 기준이 된다. 커밋 전 로컬 worktree 변경은 별도로 보존·검토해야 한다.

## 1. 현재 상태

| 항목 | 상태 |
| --- | --- |
| 원격 브랜치 | `origin/main` (PR #10은 `03a0fc1`로 병합 완료) |
| 구현 기준점 | `main` @ `03a0fc1`와 이 문서를 포함한 후속 변경 집합 |
| 구현 계획 | Production Kernel 15개 작업 + 데이터/Record Operations/relation 확장 구현 |
| 코드 리뷰 | 2026-08-28 감사 29건(최초 28건 + 후속 1건), 2026-08-31 감사 27건, [2026-09-01 4·5라운드 감사](codebase-audit-2026-09-01.md) 37건을 재현·수정·교차 검증 완료 |
| 알려진 미해결 구현 finding | 3건 — keyset 커서의 커밋 가시성 레이스, nullable 컬럼 내림차순의 인덱스 미매칭, legacy 초과 기본값을 가진 컬렉션의 `PlanUpdate` 거부. [2026-09-01 감사 §7](codebase-audit-2026-09-01.md#7-수정-종결과-최종-검증)에 사유와 함께 기록했다 |
| 릴리스 상태 | pre-v0.1 개발 프리뷰 |

현재 결과물은 PostgreSQL 기반 관리자용 컬렉션/레코드 백엔드의 배포 후보이며 single/multiple relation과 bounded traversal을 포함한다. 공개 사용자 인증·접근 규칙·파일·realtime까지 제공하는 PocketBase 기능 대체 완성본은 아니고, 실제 트래픽의 용량·복구·장애 증거가 쌓이기 전에는 대규모 운영 준비가 끝났다고 보지 않는다.

진행률은 서로 다른 완료 기준을 분리해 해석해야 한다.

| 기준 | 현재 증거 | 아직 필요한 증거 |
| --- | --- | --- |
| 승인된 Production Kernel | 설계, 구현, 계층별 자동 검증 | 릴리스별 회귀 재실행 |
| 관리자용 PostgreSQL data plane | 스키마/레코드/relation API, 다중 인스턴스 동기화, 컨테이너 | 실제 배포 환경의 rollout/rollback과 복구 훈련 |
| relation slice | schema/CRUD/delete action/expand/filter/sort/doctor의 단위·live·Docker 계약 | 운영 데이터 분포에서의 load/soak/경합 측정 |
| 공개 애플리케이션 백엔드 | 관리자 principal용 policy seam만 존재 | 공개 사용자 인증, record rules, service account |
| 대규모 운영 | 수치 보장 없음 | 목표 부하, 장시간, 장애/partition, RPO/RTO와 SLO 증거 |

## 2. 확정된 제품 경계

제품 방향은 PocketBase처럼 한 번에 시작할 수 있는 관리자 UI, 스키마 관리와 애플리케이션 API 경험을 제공하되, SQLite 대신 PostgreSQL을 사용해 다중 인스턴스와 실제 서비스 운영으로 확장하는 것이다. 동일한 화면이나 내부 구현을 복제하는 것이 아니라 이 사용 감각과 배포 단순성을 PostgreSQL 중심으로 재구성한다.

- 하나의 pgBase 프로젝트가 PostgreSQL 데이터베이스 하나를 소유한다.
- 그 데이터베이스 안에서 `pgbase_meta`는 시스템/관계 메타데이터와 record sidecar를, `app`은 사용자 컬렉션 테이블과 single relation column을, `pgbase_relation`은 ordered multiple relation edge와 제약 함수를 소유한다.
- 같은 프로젝트의 pgBase 인스턴스 여러 개가 하나의 데이터베이스를 공유할 수 있다.
- 같은 프로젝트의 모든 인스턴스는 토큰 비밀, 공개 URL, 신뢰 프록시와 관리자 origin 정책을 일관되게 공유해야 한다.
- 서로 무관한 프로젝트를 한 pgBase 데이터베이스에 넣는 구성은 지원하지 않는다.
- PostgreSQL이 유일한 영속 상태와 동시성 조정의 기준이다. 프로세스 메모리 캐시는 재생성 가능해야 한다.

여러 데이터베이스 연결 요구는 유지하되 Production Kernel 안에서 서로 무관한 DB를 한 data-plane 프로세스에 섞지는 않았다. 후속 구현은 프로젝트별 독립 DB/풀/비밀/수명주기를 가진 control plane이 여러 pgBase 프로젝트를 등록하고 관리하는 방식이 우선안이다. 이 경계는 한 DB의 장애나 자격정보가 다른 프로젝트로 전파되는 범위를 줄이고 프로젝트별 용량·백업·이전을 독립적으로 운영하게 한다. 현재 branch에는 그 control plane이나 DB registry가 아직 없다.

현재 포함된 범위:

- PostgreSQL 연결 풀, 트랜잭션 경계와 명시적 시스템 마이그레이션
- 최초 관리자 setup, 로그인, 세션, 로그아웃, CLI 관리자 생성과 암호 재설정
- 결정적 컬렉션 plan/apply와 다중 인스턴스 카탈로그 동기화
- 관리자 전용 typed CRUD, 서명된 cursor 페이지네이션, create idempotency와 strong ETag concurrency
- single/multiple relation schema와 ordered raw UUID value, `restrict`/`cascade`/`set_null` delete action
- bounded forward/nested/reverse expand, relation path filter와 single-path indexed scalar sort
- relation metadata/physical object drift를 포함한 doctor 진단
- 같은 record transaction에 기록되는 committed audit와 transactional outbox writer
- 임베드된 React 관리자 UI
- liveness/readiness, 진단 CLI, JSON 구조화 로그와 graceful shutdown
- 비root/distroless 컨테이너, 개발용 Compose와 GitHub Actions 검증
- 배포, 백업/복원, 업그레이드와 보안 운영 문서

현재 제외된 범위:

- 공개 사용자 인증, RBAC 또는 레코드 접근 규칙
- cross-project relation, relation edge attribute, arbitrary SQL join, 자동 single/multiple storage conversion
- 파일 저장소, outbox delivery worker와 realtime 구독
- jsonb 내부 필터링을 포함한 범용 query 언어 (레코드 목록에는 bounded relation-aware `expand`/`filter`/`sort`만 제공됨)
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
Admin auth / Schema service / Record Operations + Relation kernel
        |
        v
PostgreSQL pool and transactions
        |
        +--> pgbase_meta: system state and catalog
        +--> app: physical collection tables and single relation FKs
        +--> pgbase_relation: ordered multi edges and constraint functions
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
| `internal/query` | 타입 codec, 안전한 동적 SQL 컴파일과 cursor |
| `internal/relation` | relation value/storage binding, bounded hydration과 delete graph |
| `internal/recordops` | principal-aware record transaction, idempotency, ETag/revision, audit/outbox와 retention cleaner |
| `internal/api` | HTTP 라우팅, 인증/CSRF/origin과 안전한 오류 경계 |
| `internal/doctor` | 시작 전 데이터베이스·설정 진단 |
| `internal/observability` | 앱별 private Prometheus registry, bounded event/HTTP/pool 계측과 보호된 exposition |
| `ui/admin` | 빌드 결과를 Go 바이너리에 포함하는 React 관리자 UI |

## 4. PostgreSQL 데이터와 스키마 계약

시스템 마이그레이션은 다음 객체를 만든다.

- `pgbase_meta.schema_state`: 현재 컬렉션 스키마 버전
- `pgbase_meta.admins`: 관리자 계정과 Argon2id 암호 해시
- `pgbase_meta.admin_sessions`: 원문이 아닌 해시된 세션 토큰, 만료/폐기 상태
- `pgbase_meta.admin_rate_limits`: 해시된 subject 기반 로그인 제한 상태
- `pgbase_meta.collections`, `fields`, `collection_indexes`: 논리/물리 카탈로그 매핑
- `pgbase_meta.field_relations`: source/target, cardinality, min/max, delete action, codec와 deterministic storage binding
- `pgbase_meta.schema_migrations`: 적용 작업, checksum, 계획, SQL과 실행 감사 정보
- `pgbase_meta.record_revisions`: 동적 table 열을 늘리지 않는 record별 optimistic-concurrency sidecar
- `pgbase_meta.record_audit_events`: 값/credential을 저장하지 않는 append-only committed mutation 증거
- `pgbase_meta.record_outbox`: record mutation과 원자적으로 생성되는 versioned delivery 원본
- `pgbase_meta.record_idempotency`: 24시간 create replay 결과; raw key 대신 hash 저장
- `app.<generated_table>`: 컬렉션별 물리 레코드 테이블과 single relation UUID column/reverse index/deferred target FK
- `pgbase_relation.<generated_edge_table>`: `(source_id, target_id, position)` ordered multi relation, source/target FK, reverse index, position/cardinality constraint trigger

지원 필드 타입은 `boolean`, `integer`, `bigint`, `numeric`, `text`, `timestamptz`, `uuid`, `jsonb`, `relation`이다. 기본값도 필드 타입에 맞게 검증하며 relation default는 지원하지 않는다. Single relation은 UUID 또는 `null`, multiple relation은 configured `minSelect`/`maxSelect` 안의 ordered unique UUID array다. `bigint`와 `numeric`은 JSON 숫자의 정밀도 손실을 피하도록 문자열 표현을 지원하고, 레코드 ID는 UUID다. PostgreSQL btree 한계를 요청 내용에 따라 500으로 노출하지 않도록 `indexed` 또는 `unique`인 `text` 레코드 값은 UTF-8 기준 2,048바이트 이하로 제한한다.

컬렉션 변경은 두 단계다.

1. `plan`이 정규화된 정의, 기준 카탈로그 버전, checksum과 검토 가능한 SQL을 만든다. 본문의 `operation`은 `create`, `update`, `delete` 중 하나여야 한다(감사 레코드에는 내부 라벨 `collection_create`/`collection_update`/`collection_delete`가 기록된다). update는 `fields` 키가 반드시 필요하며 생략하면 요청이 거부된다.
2. `apply`가 기준 버전과 checksum을 다시 검증한 뒤 PostgreSQL advisory lock 아래 하나의 트랜잭션으로 DDL, 메타데이터, 감사 기록과 버전 증가를 반영한다.

update는 이름 기준 필드 diff로 추가(`ADD COLUMN`), 삭제(`DROP COLUMN`), required/default/unique/indexed 플래그 변경과 화이트리스트된 확대 타입 변경(integer→bigint, integer→numeric, bigint→numeric)만 허용하고 나머지 타입 변경은 `unsupported_schema_change`로 거부한다. Relation의 target/cardinality/storage 전환은 자동 migration하지 않으며, required relation 전환에는 staged backfill이 필요하다. 기존 데이터가 변경을 위반하면(예: NULL이 남아 있는 컬럼의 SET NOT NULL) 적용이 422로 롤백된다. Incoming relation이 있는 collection delete는 `collection_referenced`로 거부되고, 관계를 먼저 제거한 뒤 source table과 owned edge objects를 하나의 schema transaction에서 삭제한다.

Physical target FK는 `ON DELETE NO ACTION DEFERRABLE INITIALLY DEFERRED`인 commit-time backstop이다. `restrict`, `cascade`, `set_null`은 Record Operations의 bounded delete graph가 같은 transaction에서 수행한다. Cascade closure 내부의 restrict는 삭제를 막지 않고 closure 밖 incoming restrict만 전체 요청을 거부한다. 기본 delete budget은 depth 32, affected record 10,000, examined edge 100,000이며 초과하면 전체가 rollback된다. Delete transaction은 Read Committed에서 schema share lock → collection advisory lock의 UUID 순서 → affected row lock 순서를 사용한다. Direct SQL mutation은 revision/ETag/audit/outbox와 logical delete action을 우회하므로 지원하지 않는다.

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
| `POST /api/admin/collections/plan` | 컬렉션 create/update/delete 계획 (`operation` 필수) | 관리자 세션 + mutation 보호 |
| `POST /api/admin/collections/apply` | 검토한 계획 적용 (`operation` 필수) | 관리자 세션 + mutation 보호 |
| `GET /api/admin/system` | 버전, DB, migration, catalog/listener 상태 | 관리자 인증 |
| `GET /api/admin/system/health` | 배경 doctor 실행의 redacted finding code와 healthy/drift/unavailable 상태 | 관리자 인증 + same-origin read |
| `GET /api/v1/collections/{collection}/records` | 레코드 목록, bounded scalar/relation-path `filter`, root/single-relation indexed scalar `sort`, `expand`, cursor 페이지네이션 | 관리자 세션 |
| `POST /api/v1/collections/{collection}/records` | typed 레코드 생성 | 관리자 세션 + mutation 보호 |
| `GET /api/v1/collections/{collection}/records/{id}` | raw relation ID와 optional bounded `expand`를 포함한 단일 레코드 조회 | 관리자 세션 |
| `PATCH /api/v1/collections/{collection}/records/{id}` | 부분 갱신 | 관리자 세션 + mutation 보호 |
| `DELETE /api/v1/collections/{collection}/records/{id}` | 삭제 | 관리자 세션 + mutation 보호 |

`/api/v1` 표기는 pre-v0.1 동안 안정성 약속이 아니다. 오류는 내부 PostgreSQL/드라이버 오류를 노출하지 않고 다음 공통 envelope를 사용한다.

단일 record GET/POST/PATCH는 strong `ETag`를 반환한다. POST가 `Idempotency-Key`를 보내면 같은 관리자·컬렉션·key·body의 재시도는 원래 201 결과를 replay하고 다른 body 재사용은 409다. PATCH/DELETE의 `If-Match`는 stale write를 412로 막는다. 번들 UI는 항상 이 두 guard를 사용한다. Header가 없는 기존 `/api/v1` mutation은 pre-v0.1 compatibility로 허용하지만 `PgBase-Compatibility`만 반환한다. RFC 9745 `Deprecation`은 API resource 자체를 날짜와 정책 링크로 폐기할 때만 사용한다.

Relation write도 같은 Record Operations transaction과 guard를 사용한다. Target delete의 `restrict`는 전체를 거부한다. `set_null` survivor update는 record마다 새 revision/ETag와 audit/outbox event를 하나씩 남기고, root/cascade delete는 record마다 독립 tombstone audit/outbox event를 남긴 뒤 revision sidecar를 제거한다. Root caller의 `If-Match`만 검사하며 graph 전체는 결정적 lock 순서와 하나의 transaction에서 성공하거나 rollback된다.

Expand는 4 KiB expression, depth 6, unique node 20, 전체 fetched row 10,000, reverse node당 1,000, query step 64, encoded response/page 4 MiB로 제한한다. Filter는 4 KiB/100 syntax node이며 relation traversal은 depth 3/join node 8, sort는 최대 8 key와 relation traversal depth 2다. Relation sort는 single path가 indexed/unique scalar에 도달할 때만 허용하고, policy-aware `NULLS LAST` join/sort 비용을 제한하기 위해 root collection이 100,000행을 넘으면 target join 전에 `relation_sort_budget_exceeded`로 거부한다. Multi relation sort, arbitrary join, unbounded traversal은 stable validation error로 거부한다. Expand target은 root의 ETag와 cursor material을 바꾸지 않는다.

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

- setup token은 유효한 UTF-8 32자 이상, 1,024바이트 이하인지 검증해 HTTP 설정 요청 한도 안에 들어가게 하며, token secret은 디코딩 후 32바이트 이상이어야 한다.
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

핵심 환경 변수는 `PGBASE_DATABASE_URL`, `PGBASE_TOKEN_SECRET`, `PGBASE_SETUP_TOKEN`, `PGBASE_METRICS_TOKEN`, `PGBASE_HTTP_ADDRESS`, `PGBASE_PUBLIC_URL`, `PGBASE_AUTO_MIGRATE`, `PGBASE_TRUSTED_PROXIES`, `PGBASE_CORS_ALLOWED_ORIGINS`, `PGBASE_ADMIN_SESSION_TTL`, `PGBASE_STATEMENT_TIMEOUT`, `PGBASE_SHUTDOWN_TIMEOUT`, `PGBASE_STOP_GRACE_PERIOD`다. 정확한 요구사항과 기본값은 [Deployment and configuration](deployment.md#configuration)을 기준으로 한다.

Go에 임베드할 때는 `pgbase.Options`를 구성해 `pgbase.New(ctx, options)`를 호출한다. 내장 HTTP 서버는 `Run`을 사용하고, 호출자가 서버를 소유할 때는 반드시 `Start`를 성공시킨 뒤 `Handler`를 제공하고 마지막에 `Close`한다. `Start`된 앱을 다시 `Run`할 수는 없다. `New`는 pgBase worker나 HTTP listener를 시작하지 않지만, 이 단계에서 생성하는 `pgxpool`은 내부 연결 유지 goroutine을 실행할 수 있다.

## 8. 관리자 UI

`/admin`은 React SPA를 Go 바이너리에 임베드해 제공한다. 구현된 화면은 다음과 같다.

- 최초 관리자 setup과 로그인/로그아웃
- 컬렉션 목록과 relation target/cardinality/min/max/delete action을 포함한 컬렉션 생성 plan/apply
- 컬렉션 편집(필드 추가/삭제/플래그/relation 변경 plan/apply), staged required relation 안내와 2단계 삭제 확인
- 레코드 목록(raw ordered relation ID 표시, filter 표현식, 정렬 가능 컬럼 헤더, cursor 페이지네이션), relation UUID 편집, idempotent 생성, ETag 기반 수정/삭제와 stale reload UX
- 애플리케이션/DB/migration/catalog/listener 시스템 상태

프론트엔드 소스와 커밋된 `ui/admin/dist`는 CI에서 재빌드 후 diff가 없어야 한다.

## 9. 수명주기, 상태 확인과 패키징

- `/health/live`는 HTTP 경로가 살아 있으면 DB 장애와 무관하게 200을 반환한다.
- `/health/ready`는 HTTP가 요청을 받고 있고 DB가 연결되며 시스템 migration이 최신이고 schema listener가 동기화될 때만 200이다.
- `PGBASE_METRICS_TOKEN`을 설정한 인스턴스만 bearer-protected `/metrics`를 제공한다. HTTP route template, pool, retry, migration/schema/listener/auth outcome과 readiness는 무제한 사용자 값을 label로 사용하지 않으며 dashboard와 초기 alert rule이 함께 제공된다.
- SIGTERM 또는 context 취소 시 readiness를 먼저 내리고 새 요청을 차단한 뒤 HTTP, schema listener, admin cleaner와 record-idempotency cleaner를 `PGBASE_SHUTDOWN_TIMEOUT` 안에 drain하고 풀을 닫는다.
- 컨테이너는 Go 1.26.8로 정적 빌드하며 distroless `nonroot` 런타임을 사용한다. shell을 포함하지 않는다.
- 개발 Compose는 PostgreSQL 16.13을 사용하고 loopback port, read-only pgBase filesystem, 제한된 tmpfs, all-capability drop과 no-new-privileges를 적용한다.
- 개발 Compose만 `PGBASE_AUTO_MIGRATE=true`다. 운영에서는 false 또는 unset 상태에서 별도 migration job을 사용한다.
- tag release는 digest로 고정한 BuildKit `docker-container` builder를 사용하고 tag 원문을 binary `VERSION`에 주입한다. `/api/admin/system`에서 실행 중인 이미지 버전을 확인할 수 있다.

마지막 항목의 builder digest 고정은 build 도구의 공급망 고정이지 publish candidate 동일성 증거가 아니다. 현재 tag workflow에서 E2E는 Compose로 pgBase 이미지를 만들고, security는 별도의 audit 이미지를 만들며, backup/restore는 source-built Go binary를 사용한다. 이 reusable job들이 끝난 뒤 release job이 publish 이미지를 별도로 Buildx build/push한다. 따라서 현재 gate는 같은 source revision을 검증하지만 E2E·security·restore가 최종 publish digest를 실행했다는 것을 증명하지 않는다. release SBOM과 provenance는 publish digest에 연결되지만, 단일 candidate build와 검증된 digest의 promotion은 아직 구현되지 않았다.

## 10. 검증 gate

최종 전달 후보는 마지막 변경 뒤 다음 gate를 모두 다시 통과해야 한다. 이 표는 실제 트래픽의 처리량이나 SLO 증거가 아니라 release acceptance 범위다. 각 전달 보고에는 실행한 PostgreSQL 버전, 명령, 결과와 미실행 항목을 별도로 남긴다.

| 영역 | 검증 |
| --- | --- |
| Go unit | live PostgreSQL 없이 `scripts/test-go-unit.sh -count=1` |
| Go integration | live PostgreSQL을 사용한 `scripts/test-go-integration.sh -count=1` |
| 정적 검사 | `go vet ./...` |
| 동시성 | Linux / Go 1.26.8에서 `scripts/test-go-integration.sh -race -count=1` |
| Frontend | integrity-bound `npm ci`, typecheck, unit/contract test, production build |
| Embedded UI | 재빌드한 `ui/admin/dist`와 커밋 결과가 동일 |
| Black-box E2E | source-built pgBase Compose 이미지와 digest-pinned PostgreSQL 16.13에서 `-count=2 -shuffle=on` 실행 |
| E2E 범위 | 실제 Chromium 온보딩, bootstrap/CRUD/restart, record idempotency·ETag guard, two-instance concurrent stale-write 수렴, DB outage/recovery, two-instance schema sync, graceful drain, 이미지 버전 주입 |
| Relation | migration/schema planner/doctor, single/multi ordered CRUD, restart/two-instance catalog read, bounded expand/filter/sort, restrict/cascade/set-null graph와 rollback/concurrency를 포함한 focused/full gate |
| Backup/restore | source-built Go binary와 실제 API로 single/multiple relation과 ordered edge를 seed하고 빈 DB에 전체 dump를 복원한 뒤 metadata, owner join, edge 순서와 relation-aware doctor 검증 |
| Telemetry | opt-in/off와 bearer 실패/성공 Docker scrape, raw path/query sentinel 부재, private registry/race, dashboard JSON과 고정 digest `promtool` rule 검증, disabled/enabled benchmark artifact |
| CI PostgreSQL matrix | 16.13, 17.9, 18.3 |
| Container | E2E가 별도로 build한 이미지의 nonroot/shell 없음/hardened Compose runtime 검사, release builder digest 고정 |
| Release artifact | publish digest에 SBOM/provenance 연결. 같은 digest에 대한 E2E/security/restore와 rebuild 없는 digest promotion은 아직 없음 |
| Review | 4개 병렬 영역 재감사, 교차 검증과 잔여 finding 수정 완료 |

2026-08-31 최종 워킹트리는 PostgreSQL 16.13, 17.9, 18.3 각각에서 `go test -race ./... -count=1`을 skip 없이 통과했다. Admin UI는 12개 파일 80개 테스트와 typecheck/build를 통과했고, Docker black-box E2E는 relation 시나리오를 포함해 `-count=2 -shuffle=on`으로 통과했다. gofmt/vet/build/diff check, reachable Go vulnerability 0, high npm vulnerability 0, dashboard JSON과 8개 Prometheus alert rule 검증도 통과했다. 상세 finding별 종결 근거는 [2026-08-31 codebase audit](codebase-audit-2026-08-31.md#7-수정-종결과-최종-재검증)에 남겼다.

GitHub Actions는 Go formatting/vet/race와 benchmark artifact, 프론트엔드 typecheck/test/build/dist 재현성, Docker black-box E2E와 backup/restore drill을 각각 gate한다. 태그 이미지 발행은 이 reusable gate가 모두 성공한 뒤에만 실행된다. 다만 각 image-dependent gate와 publish job은 독립 build이므로 이 순서는 source revision gate이지 same-digest artifact gate가 아니다. 이 검증은 구현 정확성의 근거지만 최종 publish artifact 동일성이나 실제 트래픽의 용량·지연·장시간 안정성 증거를 대신하지 않는다.

## 11. 운영 배포 계약

운영 배포 순서는 다음과 같다.

1. `pgbase_meta`, `app`, `pgbase_relation`을 한 시점에 함께 백업한다.
2. 새 바이너리로 `pgbase migrate status`를 실행한다.
3. 지명한 migration job 하나에서 `pgbase migrate up`을 실행한다.
4. 다시 status를 확인해 `current == latest`, `dirty=false`를 요구한다.
5. `pgbase serve` 인스턴스를 rollout한다.
6. 각 인스턴스의 `/health/ready`가 200일 때만 load balancer에 넣는다.

원격 PostgreSQL은 검증된 TLS를 사용하고, pgBase의 HTTP TLS는 reverse proxy/load balancer에서 종료한다. orchestrator termination grace는 shutdown timeout보다 길어야 한다. 상세 절차는 [Deployment](deployment.md), [Backup and restore](backup-restore.md), [Upgrades](upgrades.md)를 따른다.

현재 무중단 upgrade는 보장하지 않는다. collection DDL은 PostgreSQL lock을 획득할 수 있고 system migration은 maintenance window가 필요할 수 있다. 릴리스별 호환성이 증명되지 않았다면 stop-migrate-start를 우선한다.

## 12. 남은 위험과 다음 우선순위

실제 서비스와 대규모 운영 완료를 주장하려면 코드 finding 상태와 무관하게 다음 증거와 기능이 필요하다.

### P0: 첫 실제 서비스 배포 전

1. staging에 실제 TLS proxy/load balancer, 비밀 관리, 외부 PostgreSQL을 구성하고 rollout/rollback을 훈련한다.
2. 제공된 요청률/지연/error, DB pool, migration/schema/listener/readiness/auth/retry 지표와 초기 경보를 staging에서 운영하고, 실제 트래픽으로 threshold와 SLO를 교정한다.
3. 목표 트래픽 모델로 load, soak, spike, DB 장애와 network partition 테스트를 실행하고 용량/SLO 증거를 보존한다.
4. 운영 방식의 backup/PITR와 별도 환경 restore drill을 실행하고 RPO/RTO를 확정한다.
5. 실제 registry tag release, 서명 검증 정책, release note와 migration 호환성 승인을 운영 절차로 확정한다. Publish image·builder digest와 provenance/SBOM 생성 경로는 구현되어 있지만, E2E/security/restore가 같은 candidate digest를 검증하고 그 digest를 rebuild 없이 승격하는 경로는 아직 없다.
6. 외부 또는 독립 보안 리뷰, 의존성/이미지 scan과 incident runbook을 완료한다.

### P1: PocketBase형 공개 애플리케이션 백엔드

1. public user collection/authentication과 이메일 검증·암호 재설정 정책
2. collection/record 단위 RBAC 또는 규칙 엔진과 감사 가능 정책 평가
3. API token/service account와 관리자 역할 분리
4. 공식 OpenAPI 계약, 호환성 정책과 최소 SDK

### P2: 기능 폭과 확장성

1. relation edge attribute와 명시적 join collection UX, cross-project relation 요구 검토 (현재 bounded expand/filter/sort는 구현됨)
2. object storage 기반 file field와 upload/download authorization
3. 현재 transactional outbox writer 위의 lease/fencing delivery worker, PostgreSQL 기반 realtime change delivery와 재연결/유실 계약
4. 복합 인덱스 등 인덱스 모델 확장과 고급 filter/sort 표현식 (제한적 filter/sort는 2026-08-29 데이터 커널 확장으로 제공됨)
5. online schema change 전략, read replica, tenant/project control plane 검토

## 13. 브랜치 인수인계와 재개 방법

리뷰·병합된 원격 `main`이 전달 기준이며 로컬 worktree나 로컬 merge 상태는 인수인계 계약이 아니다.

새 clone 또는 브랜치가 없는 checkout:

```console
git fetch origin
git switch --track origin/main
```

이미 로컬 브랜치가 있을 때:

```console
git switch main
git pull --ff-only
```

새 환경은 Go 1.26.8, Node 24, npm 11, Docker Engine/Compose/Buildx, 그리고 데이터베이스를 만들 수 있는 superuser 계정을 가진 PostgreSQL 16 이상을 준비한다. Go unit suite는 live PostgreSQL 파일을 build하지 않으며, PostgreSQL integration suite는 `PGBASE_TEST_ADMIN_URL`이 없으면 `go test`를 시작하기 전에 실패한다. 두 명령 모두 JSON test event를 skip allowlist와 대조한다. CI는 같은 integration suite를 PostgreSQL 16.13, 17.9, 18.3에서 각각 실행한다. clone 직후 다음 순서로 전달 상태를 확인한다.

```console
scripts/test-go-unit.sh -count=1
go vet ./...
export PGBASE_TEST_ADMIN_URL=postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable
scripts/test-go-integration.sh -count=1
cd ui/admin
npm ci --ignore-scripts
npm run typecheck
npm test -- --run
npm run build
cd ../..
go test -tags=e2e ./tests/e2e -v -count=2 -shuffle=on
```

E2E는 임시 Compose 프로젝트, 포트와 비밀을 자체 생성하고 종료 시 제거한다. 로컬 서비스 시작은 [10-minute quickstart](quickstart.md), 운영 배포는 [Deployment](deployment.md)를 따른다.

향후 PR 전에는 target branch를 fetch한 다음 전체 branch diff와 CI를 다시 검토한다. 이 handoff 절차의 작업 범위에는 target branch로의 로컬 merge와 worktree 삭제가 포함되지 않는다.

## 14. 문서와 소스 지도

| 문서/경로 | 역할 |
| --- | --- |
| [README](../README.md) | 프로젝트 진입점과 현재 범위 |
| [Tradeoffs](production-kernel-tradeoffs.md) | 확정된 결정의 이점·비용·재검토 조건과 의도적으로 남긴 위험 |
| [Codebase audit](codebase-audit-2026-08-28.md) | 최초 finding, 수정 증거와 후속 재감사 결과 |
| [Round 4-5 audit](codebase-audit-2026-09-01.md) | 전수 정독과 적대적 검증으로 찾은 37건, 수정 증거와 미수정 항목 |
| [10-minute quickstart](quickstart.md) | 로컬 첫 실행과 첫 CRUD |
| [Deployment and configuration](deployment.md) | 운영 topology, 설정, TLS/proxy, rollout, secret rotation |
| [Backup and restore](backup-restore.md) | 세 schema와 relation artifact의 일관된 backup/PITR/restore drill |
| [Upgrades](upgrades.md) | pre-v0.1 upgrade/rollback 절차와 제한 |
| [Security policy](../SECURITY.md) | 취약점 신고와 지원 정책 |
| [Approved design](superpowers/specs/2026-08-28-production-kernel-design.md) | 승인된 제품/아키텍처 스펙 |
| [Implementation plan](superpowers/plans/2026-08-28-production-kernel.md) | 15개 작업의 구현 기록과 acceptance 기준 |
| [Relation design](superpowers/specs/2026-08-31-relation-schema-lifecycle-design.md) | relation schema/value/delete/query/운영 계약 |
| [Relation implementation plan](superpowers/plans/2026-08-31-relation-schema-lifecycle.md) | relation 작업 순서와 acceptance 기준 |
| [Original pgBase v0.1 Notion page](https://app.notion.com/p/pgBase-v0-1-3c9d9bc767f081ce8934e9cd22bdc85b?source=copy_link) | 초기 제품 메모와 기획 출처 |
| `migrations/system` | 실제 PostgreSQL system schema의 최종 기준 |
| `internal/api/router.go` | 실제 HTTP route의 최종 기준 |
| `internal/config`, `pgbase/options.go` | 설정과 공개 Go option의 최종 기준 |
| `.github/workflows`, `deployments` | CI와 컨테이너/Compose 실행 계약 |

상태 요약은 이 handoff를 기준으로 하고, 구체적인 동작이 문서와 다를 경우 현재 branch의 코드와 테스트를 최종 실행 계약으로 본다. 승인 당시의 의도와 결정 배경은 design, 구현 순서와 검증 내역은 implementation plan을 참고한다.
