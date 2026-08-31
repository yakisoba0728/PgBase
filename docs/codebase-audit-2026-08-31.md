# PgBase 전체 코드베이스 감사 보고서 (2026-08-31)

> **종결 상태 (2026-08-31):** 아래 2절의 확정 finding 27건은 모두 재현 테스트로 고정한 뒤 수정했고, relation 확장 후 독립 재감사에서 확인한 후속 finding도 모두 해결했다. 0~6절은 수정 전 감사 증거를 보존하며, 현재 워킹트리의 해결 상태와 최종 검증 결과는 7절을 기준으로 한다.

## 0. 이 문서의 상태

진행 중인 감사의 **작업 기록**이다. 확인되는 즉시 추가하며, 검증 방법과 한계를 함께 남긴다.

- 기준 커밋: `main` @ `6dd81f9` (Merge pull request #8 from webisnoteasy/codex/data-kernel)
- **소스 코드는 수정하지 않았다.** 이 문서와 감사 과정에서 만든 임시 probe test(모두 삭제됨) 외에 tracked 파일 변경은 없다.
- 검증 환경: PostgreSQL 16.13 / 17.9 / 18.3 (CI가 pin한 digest와 동일한 컨테이너), Go 1.27.0 darwin/arm64, Node 24.19.0
- 이전 감사: [docs/codebase-audit-2026-08-28.md](codebase-audit-2026-08-28.md) (3라운드 완료, "알려진 미해결 구현 finding 없음"으로 종결)

이번 감사에서 발견한 항목은 모두 **이전 3라운드를 통과한 것들**이다. 마지막 절에 그 공통 원인을 정리했다.

감사 규모: 2개 워크플로우로 26개 에이전트를 운용했다. 1라운드는 8개 서브시스템 정독 + 8개 적대적 verifier + 종합(26건 지적, **반박 0건**), 2라운드는 8개 영역의 경험적 probe + 종합(20건). 오케스트레이터가 모든 항목을 라이브로 재현·정정했다. 2라운드에서 auth-boundary probe는 provider 오류(`upstream stream error`)로, resource-limits probe는 구조화 출력 실패로 각각 중단됐다 — 해당 영역은 커버리지가 불완전하며 4절에 명시했다.

## 1. 검증 베이스라인 (모두 통과)

라이브 PostgreSQL을 연결한 상태에서 실행했다. `PGBASE_TEST_ADMIN_URL` 없이 실행하면 **496건 중 96건이 조용히 skip**되면서도 모든 패키지가 `ok`로 보고되므로, 이 변수 없는 실행 결과는 신뢰할 수 없다.

| 항목 | 결과 |
| --- | --- |
| `gofmt -l .` | clean |
| `go build ./...` | clean |
| `go vet ./...` | clean |
| `go test -count=1 ./...` | 16/16 패키지 통과, 497 테스트, **skip 0건** |
| `go test -race ./...` | clean |
| `go test -count=2 -shuffle=on -tags=e2e ./tests/e2e` | 통과 (CI와 동일 조건) |
| `go test -race -tags=e2e ./tests/e2e` | 통과 |
| Playwright 브라우저 온보딩 | 통과 (빈 DB → 관리자 생성 → 세션 유지) |
| 퍼징 3개 타겟 | 약 1,100만 exec, crash 0 |
| 커버리지 | api 88.8%, query 86.3%, schema 82.8%, observability 100%, 그 외 74~86% |
| `govulncheck ./...` | 도달 가능 취약점 0건 (`x/crypto` GO-2026-5932는 미사용 `openpgp` 경로) |
| `npm audit --audit-level=high` | 0건 |
| `npm run typecheck` / `npm test` | 통과 / 41 테스트 통과 |
| `ui/admin/dist` 재빌드 | 바이트 동일 (임베드 번들이 소스와 동기화됨) |
| 프로덕션 이미지 | 22MB distroless, `nonroot`, 셸 없음 |
| Compose quickstart | 문서대로 동작, read-only rootfs / `cap_drop: ALL` / `no-new-privileges` 확인 |
| 백업/복원 드릴 | dump→빈 DB 복원 후 schema·catalog·데이터 보존, 복원된 DB가 `doctor` 통과 |
| 벤치마크 (10,000행) | get 0.42ms, list-50 0.88ms, create 0.49ms |
| **PostgreSQL 매트릭스** | **16.13 / 17.9 / 18.3 전부 497 테스트 통과, skip 0건.** 18.3에서 e2e도 통과. CI 매트릭스 3개 버전을 로컬에서 재확인했다. |

AUD31-H03(statement_timeout)과 AUD31-H05(jsonvalue 숫자 divergence)는 **16.13·17.9·18.3에서 동일하게 재현된다.** 특정 PostgreSQL 버전의 특성이 아니라 버전 무관한 결함이다.

## 2. 확인된 문제

각 항목은 **라이브 재현**을 거쳤고, 독립 verifier 에이전트의 적대적 검증에서 살아남았다. 심각도는 실제 영향 기준이다.

### AUD31-H01 (high) — 관리자 로그인 전면 차단 DoS (global rate-limit bucket)

- 위치: `internal/adminauth/service.go:290` (bucket 정의), `service.go:338-349` (차단 판정)
- 원인: 세 번째 bucket이 상수 subject `"v1"`에 `globalFailureLimit=1000`, `clearOnSuccess:false`로 걸려 있다. `rateLimitBlocked`는 **어느 하나라도** 차단되면 거부하므로, 이 bucket이 포화되면 모든 관리자 계정의 로그인이 막힌다.
- 재현: 공유 bucket 행만 `blocked_until`을 설정한 뒤, 실패 이력이 전혀 없는 관리자가 **정확한 비밀번호로** 로그인 → `429 rate_limited`.
- 공격 비용: network axis가 IP당 20으로 제한되므로 IP 50개 필요. 15분 창에 1000회 = **총 1.11 req/s, IP당 0.022 req/s**. 이메일은 존재하지 않아도 된다(조회 전에 실패를 계산함).
- 해제: 차단 중에는 성공이 불가능하고 `clearOnSuccess:false`이므로 **로그인으로는 해제할 수 없다.** 전역 버킷이라 AUD31-H02의 "새 이메일로 관리자 생성" 우회도 통하지 않는다(버킷 subject가 상수 `"v1"`이므로 모든 계정이 같은 행을 공유). 복구는 `pgbase_meta.admin_rate_limits` 직접 삭제, `PGBASE_TOKEN_SECRET` 교체, 또는 공격이 멈춘 뒤 15분 대기뿐이다. 이 점에서 H01이 H02보다 실질적으로 더 심각하다.

### AUD31-H02 (high) — 계정 단위 lockout + 복구 경로 미문서화

- 위치: `internal/adminauth/service.go:201`, `store.go:212-216`
- 재현: 미인증 공격자가 5회 실패 → 정당한 관리자가 정확한 비밀번호로 `429`. 차단 15분이며 `greatest(...)`로 연장되므로 창당 5회만 보내면 무기한 유지된다.
- 복구 경로 (실측으로 정정): 세 가지를 각각 라이브로 확인했다.
  - **CLI `admin reset-password`는 차단을 해제하지 않는다.** 비밀번호를 바꿔도 그 계정은 계속 429다(버킷이 이메일 기준이므로 당연한 결과).
  - **`pgbase admin create --email <다른 주소>`는 동작한다.** 새 주소는 빈 버킷이므로 즉시 로그인된다(라이브 확인: victim은 429, rescue는 200). 즉 호스트 셸 접근이 있으면 제품 내 복구 수단이 존재한다 — 최초 서베이가 "복구 불가"라고 본 것은 과장이었고, verifier의 정정이 맞다.
  - 차단된 주소 **그 자체**를 되살리려면 여전히 `pgbase_meta.admin_rate_limits` 행을 직접 지워야 한다. 또는 15분을 기다린다(공격이 멈춘 경우). `PGBASE_TOKEN_SECRET` 교체도 해시 namespace를 바꿔 해제되지만 전체 세션·CSRF·cursor가 함께 무효화된다.
  - `docs/`에는 이 어느 절차도 기록돼 있지 않다(`grep` 확인). 운영자가 락아웃 상황에서 참고할 문서가 없다는 것이 실질 문제다.

### AUD31-H03 (high) — `PGBASE_STATEMENT_TIMEOUT >= 1m`이면 모든 프로세스가 기동 실패

- 위치: `internal/database/pool.go:31`
- 원인: `statementTimeout.String()`이 Go 문법(`"1m0s"`)을 만들고 PostgreSQL이 거부한다(SQLSTATE 22023, 유효 단위 `us/ms/s/min/h/d`). `AfterConnect`에서 실패하므로 **모든 pool 연결**이 생성되지 못한다.
- 재현: `1m`, `90s`, `1h`, `100us` → `serve`/`migrate`/`doctor` 전부 실패. `5s`, `30s`는 우연히 동작.
- 문서 모순: `docs/deployment.md:49`는 "Go duration"이라고 안내한다. 기본값 5s와 compose 경로는 안전하므로, 영향은 분 단위를 설정한 운영자로 한정된다.
- 검증한 수정 방향: `set_config('statement_timeout','60000')` → `1min`으로 정상 동작 확인(정수는 ms로 해석됨).

### AUD31-H04 (high) — 잘못된 percent-escape가 `filter`/`sort`/`cursor`를 조용히 무시

- 위치: `internal/api/record_handlers.go:45`
- 원인: `r.URL.Query()`가 `ParseQuery` 에러를 버리고, unescape 실패한 키는 `Get`이 `""`을 반환한다. `internal/query/service.go`의 `parseFilterRequest`는 `""`을 "필터 없음"으로 처리한다.
- 재현(라이브):
  - `filter=n=1` → 15행. `filter=n%3D1%`(끝에 bare `%`) → **HTTP 200에 75행 전체**. 필터가 사라진 채 성공 응답.
  - `sort=-n%` → 정렬이 무시되고 기본 순서로 반환.
  - `cursor=abc%` → **1페이지가 반환됨**. 페이지네이션 중 cursor가 한 번 깨지면 클라이언트가 처음부터 다시 돌며 같은 레코드를 재처리한다.
- 이것이 이번 감사에서 가장 실무 위험이 큰 항목이다. 클라이언트가 퍼센트 문자를 인코딩하지 않고 조립하는 것만으로 발생한다.

### AUD31-H05 (high) — `jsonvalue`가 PostgreSQL이 거부하는 숫자를 통과시켜 HTTP 500 유발

- 위치: `internal/jsonvalue/json.go:143-146`
- 원인: fraction digit을 세기 **전에 trailing zero를 제거**하는데, PostgreSQL은 표기된 자릿수대로 센다.
- 재현: jsonb 필드에 `{"n":1.0e-16383}` → **500 internal_error**. `1.00e-16383`도 동일. `1e-16383`은 정상(201), `1e-16384`는 올바르게 422.
- 직접 확인: `select '{"n":1.0e-16383}'::jsonb` → `ERROR: value overflows numeric format`. 즉 검증기가 PostgreSQL과 갈라진다. 클라이언트 입력이 서버 오류가 되는 것은 이 패키지가 막으려던 바로 그 문제다.
- 상류 가드 없음: `internal/query/codec.go:118-122`가 jsonb를 `jsonvalue.Valid`만으로 판정한다.

### AUD31-H06 (high) — 릴리스 이미지가 테스트 게이트 없이 발행되며, 유일한 게이트는 96건을 조용히 skip한다

- 위치: `.github/workflows/release-image.yml` (전체), `deployments/Dockerfile:24`
- 확인 1: `release-image.yml`에는 `needs:`도 테스트 스텝도 없다(`grep`으로 확인). 태그를 push하면 Backend·Frontend·E2E·Security 워크플로우 결과와 **무관하게** 서명된 이미지가 ghcr.io로 발행된다. provenance와 SBOM attestation은 붙지만, 그것은 "이 바이너리가 이 소스에서 나왔다"를 증명할 뿐 "이 소스가 테스트를 통과했다"를 증명하지 않는다.
- 확인 2: 실질적 유일한 게이트는 `Dockerfile:24`의 `RUN go test ./...`인데, 빌드 환경에 `PGBASE_TEST_ADMIN_URL`이 없다(`grep` 결과 0건). 따라서 **96건이 조용히 skip되면서 모든 패키지가 `ok`로 보고된다.**
- 측정한 커버리지 차이(빌드 환경 vs 라이브 PostgreSQL):

  | 패키지 | 이미지 빌드 시 | 라이브 PG |
  | --- | ---: | ---: |
  | `internal/migrate` | **0.0%** | 81.0% |
  | `internal/adminauth` | 18.2% | 77.6% |
  | `internal/schema` | 40.2% | 82.8% |
  | `internal/query` | 70.1% | 86.3% |

- 영향: 운영 스키마를 변경하는 migration runner가 릴리스 시점에 **완전히 검증되지 않는다.** 관리자 인증도 18.2%만 실행된다. 게다가 `ok` 출력 때문에 게이트가 통과한 것처럼 보인다. 이 구조에서는 `internal/migrate`를 깨뜨리는 커밋이 태그되어도 이미지가 정상 발행된다.
- 확인 3: 저장소의 6개 워크플로우 중 **cross-workflow 게이팅이 하나도 없다**(`needs:` 0건). branch protection으로 required check를 걸 수도 있지만 (a) 이 저장소는 private + 현재 플랜에서 rulesets/branch-protection API가 403이라 확인·설정이 불가하고, (b) **태그 push는 PR 보호를 우회**하는데 `release-image.yml`이 정확히 태그로 트리거된다. 즉 PR 보호가 있어도 이 경로는 막히지 않는다.
- 수정 방향: (a) `release-image.yml`에 Backend/E2E 워크플로우 성공을 요구하는 게이트를 추가하고, (b) `Dockerfile`의 `go test`는 빌드 시 DB가 없으므로 게이트로 신뢰하지 않거나 skip을 실패로 처리한다(`-run` 필터나 `go test -count=1 ./... 2>&1 | grep -q SKIP && exit 1` 류의 명시적 방어). 최소한 skip이 0건임을 요구하는 검사가 필요하다.

### AUD31-H07 (high) — `/api/admin/system`이 migration dirty 상태를 숨겨 관리자 UI가 장애 중에 "정상"을 표시

- 위치: `internal/api/system_handler.go:9-16` (`SystemStatus` 구조체), `pgbase/app.go:86-87`
- 원인: `migrate.Status`는 `Dirty` 필드를 갖고 있고 checksum 불일치를 정확히 감지한다(`internal/migrate/status.go:47-51`). `pgbase/app.go:164`는 readiness 판정에 그 `Dirty`를 **실제로 사용한다.** 그런데 `app.go:86-87`은 `Current`와 `Latest`만 `SystemStatus`로 복사하고 `Dirty`를 버린다. `SystemStatus`에는 애초에 그 필드가 없다.
- 재현(라이브): migration 1의 checksum을 손상시킨 뒤 두 엔드포인트를 동시에 조회.

  ```
  /health/ready     -> 503 {"code":"not_ready","details":{"components":["migrations"]}}
  /api/admin/system -> 200 {"migrationCurrent":3,"migrationLatest":3,"listenerReady":true,...}
  ```

- 영향: 제품 내 유일한 운영 화면(`ui/admin/src/pages/system-page.tsx`가 `Migrations 3 / 3`으로 렌더)이 로드밸런서의 판단과 정면으로 모순된다. LB는 인스턴스를 rotation에서 빼는데 관리자 UI는 "정상"이라고 말한다. 503을 디버깅하는 운영자가 System 페이지를 먼저 확인하면 잘못된 결론에 도달한다.
- 수정 방향: `SystemStatus`에 `migrationDirty bool`을 추가하고 `app.go:86-87`에서 함께 복사한 뒤 UI에 표시한다. 데이터는 이미 존재하며 API 경계에서만 유실된다.

### AUD31-H08 (high) — `statement_timeout`이 모든 DDL을 캡하고, AUD31-H03과 겹쳐 59초 상한이 생긴다

- 위치: `internal/database/pool.go:31` (모든 연결에 `statement_timeout` 설정), `internal/schema/service.go:82,147,215` (DDL이 그 연결에서 실행)
- 원인: `statement_timeout`은 pool의 **모든** 연결에 걸리므로 DDL도 예외가 아니다. 큰 테이블의 인덱스 생성은 기본 5s를 쉽게 넘긴다.
- 재현(라이브, 실제 데이터):

  | 테이블 | 인덱스 생성 시간 | 5s timeout 적용 시 |
  | --- | ---: | --- |
  | 3,000,000행 (1,057 MB) | 4,862 ms | 4,431 ms 성공 (간신히) |
  | 6,000,000행 | — | **5,147 ms에서 취소** — `ERROR: canceling statement due to statement timeout` (SQLSTATE 57014) |

- 분류 문제: 57014는 `internal/database/errors.go:50`에서 `KindUnavailable` → **503(재시도 가능)**으로 매핑된다. 그러나 재시도하면 같은 인덱스 빌드를 다시 시도해 항상 같은 지점에서 실패한다. 즉 **영구 실패를 재시도 가능 상태로 보고**한다.
- **AUD31-H03과의 복합 효과**: 유일한 조정 수단이 `PGBASE_STATEMENT_TIMEOUT`인데, Go의 `Duration.String()` 때문에 60초 이상은 서버가 기동조차 하지 않는다. 직접 확인한 경계:

  ```
  59s -> set_config('statement_timeout','59s')  정상
  60s -> set_config('statement_timeout','1m0s') ERROR: invalid value for parameter
  ```

  결과적으로 **모든 DDL 작업에 59초라는 하드 상한**이 생긴다. 그보다 오래 걸리는 스키마 변경은 이 제품으로 적용할 방법이 없다.
- 참고: README와 handoff가 "online DDL 미지원"과 "throughput 보장 없음"을 명시하므로 제품 경계 자체는 정직하다. 다만 59초 상한은 문서화돼 있지 않고, 실패 시 응답이 503이라 운영자가 원인을 오해한다.
- 수정 방향: AUD31-H03을 고치면(밀리초 전송) 상한이 사라진다. 추가로 DDL 트랜잭션에서 `set local statement_timeout`을 별도 값으로 올리거나 0으로 두는 선택지가 있고, 57014를 apply 경로에서 비-재시도 코드로 분류해야 한다.

### AUD31-H09 (high) — 인덱스/unique text 필드에 압축 불가한 값 2704자 이상 → 진단 없는 HTTP 500

- 위치: `internal/database/errors.go:43-53` (`Classify`가 SQLSTATE 54000을 처리하지 않음)
- 원인: PostgreSQL btree 인덱스 엔트리 상한은 2704바이트다. 초과 시 SQLSTATE **54000**(`program_limit_exceeded`)이 발생하는데 `Classify`의 어느 분기에도 없어 `KindUnknown` → 500이 된다.
- 재현(라이브): `unique: true` text 필드에 **압축 불가한 무작위 ASCII** 삽입.

  | 길이 | 결과 |
  | ---: | --- |
  | 2000자 | 201 created |
  | **2704자** | **500 internal_error** |
  | 3000자 / 5000자 | 500 internal_error |

  PostgreSQL 원문: `ERROR: index row size 3016 exceeds btree version 4 maximum 2704 for index "rawbt_v_uniq"`.
- **중요한 함정**: `'A'*3000`처럼 압축 가능한 문자열은 TOAST 압축으로 상한 아래로 줄어들어 **201로 통과한다.** 처음 이 방식으로 시도했을 때 재현되지 않았고, 무작위 문자열로 바꾼 뒤에야 재현됐다. 즉 같은 길이라도 데이터 내용에 따라 성공/실패가 갈린다.
- 진단 부재: 이 500은 서버 로그에 `level:INFO`의 access log 한 줄로만 남고 **ERROR 라인이 0건**이다(`grep -c ERROR` 확인). 클라이언트 입력이 500을 만드는데 운영자는 원인을 알 방법이 없다.
- 영향: 정상 범위의 사용자 입력(긴 slug, 해시, 토큰, URL, base64 문자열)이 500을 유발한다. 422여야 하며, 길이 제한을 명시해야 한다.
- 수정 방향: `Classify`에 54000을 `KindValidation`으로 추가하고, 인덱스/unique text 필드에 대해 입력 단계에서 바이트 길이를 검사한다. class 54 전체(54001 statement_too_complex, 54011 too_many_columns)도 현재 500이다.

### AUD31-M10 (medium) — 타입 widening 후 각 pooled 연결이 한 번씩 500 (SQLSTATE 0A000)

- 위치: `internal/database/errors.go:43-53`
- 원인: `integer → bigint → numeric` widening 후 PostgreSQL이 캐시된 prepared plan을 무효화하며 SQLSTATE **0A000**(`cached plan must not change result type`)을 낸다. 이것도 `Classify`에 없어 500이 된다.
- 영향: 스키마를 정상 절차로 변경한 직후, pool의 각 연결이 첫 요청에서 한 번씩 500을 반환한다. 재시도하면 성공하므로 일시적이지만, 클라이언트에는 서버 결함으로 보이고 503(재시도 가능)도 아니다.
- 수정 방향: 0A000을 재시도 가능(`KindUnavailable`)으로 분류하거나, widening 적용 후 pool 연결을 순환시킨다.

### AUD31-M11 (medium) — `numeric` 값이 0일 때 저장된 scale이 소실되고, read-modify-write로 영구 파괴된다

- 위치: `internal/query/codec.go:213`
- 재현(라이브): 보낸 값 / API 응답 / DB 실제 저장값을 3중으로 비교했다.

  | 보낸 값 | API 응답 | DB 저장값 |
  | --- | --- | --- |
  | `"0.0"` | **`"0"`** | `0.0` |
  | `"0.00"` | **`"0"`** | `0.00` |
  | `"0.000000"` | **`"0"`** | `0.000000` |
  | `"-0.0"` | **`"0"`** | `0.0` |
  | `"1.50"` | `"1.50"` | `1.50` |
  | `"0.10"` | `"0.10"` | `0.10` |
  | `"10.00"` | `"10.00"` | `10.00` |

- 정확한 범위: **DB는 scale을 올바르게 보존한다.** 손실은 API 응답 직렬화에서만 발생하고, 가수가 0인 경우에만 해당한다. `0.10`처럼 0이 포함되기만 한 값은 정상이다.
- 영향: 통화·측정값처럼 유효자릿수가 의미를 갖는 데이터에서 0만 특별 취급된다. 실질 위험은 클라이언트가 읽어서 그대로 다시 쓰는(read-modify-write) 패턴이다 — API가 `"0"`을 주고 클라이언트가 그것을 다시 PATCH하면 DB에 남아 있던 `0.000000`이 `0`으로 덮어써진다. 즉 DB 손상은 왕복을 거쳐야 발생한다.

### AUD31-H10 (high) — 같은 레코드 동시 수정 시 절반이 503 (RepeatableRead + 재시도 부재)

- 위치: `internal/query/service.go:186` (`pgx.TxOptions{IsoLevel: pgx.RepeatableRead}`)
- 재현(라이브, 같은 행에 N개 워커 × 25회 PATCH):

  | 동시 writer | 총 요청 | 200 | 503 | 성공률 |
  | ---: | ---: | ---: | ---: | ---: |
  | 1 | 25 | 25 | 0 | 100.0% |
  | 2 | 50 | 26 | 24 | **52.0%** |
  | 4 | 100 | 49 | 51 | **49.0%** |
  | 8 | 200 | 130 | 70 | 65.0% |
  | 16 | 400 | 271 | 129 | 67.8% |

- 원인: 레코드 경로가 `RepeatableRead`를 쓰므로 같은 행을 동시에 갱신하면 SQLSTATE 40001(serialization_failure)이 발생한다. 40001은 `errors.go:51`에서 `KindUnavailable`로 올바르게 분류되어 `503 + Retry-After: 1`이 나간다.
- **핵심 결함은 격리 수준이 아니라 재시도 부재다.** 직접 확인: `internal/query/`와 `internal/database/tx.go` 어디에도 retry/backoff 코드가 없고(`grep` 0건), `ui/admin/src/api/`도 503을 재시도하지 않는다(0건). 즉 서버가 `Retry-After: 1`을 광고하면서 서버도 번들 UI도 그것을 이행하지 않는다.
- **데이터 무결성은 지켜진다**: 동시 실행 후 행 값이 단일 일관 값(`w14-24`)으로 남았다. torn write도 lost update도 없다. 이것은 가용성·사용성 결함이며 정합성 결함이 아니다.
- 관측 불가: 503이 274건 발생한 로그에 **ERROR 라인이 0건**이다. 운영자는 `level:INFO`의 access log만 본다.
- 영향: 관리자 UI 탭 두 개, 동기화하는 클라이언트, 재시도 없는 SDK — 같은 레코드를 동시에 만지는 흔한 상황에서 쓰기의 절반이 거부된다. 재시도 책임이 전적으로 모든 API 소비자에게 전가된다.
- 수정 방향: (a) `Pool.InTx` 주위에 40001/40P01 한정 유한 재시도를 넣거나, (b) 단일 행 UPDATE/DELETE-by-id에 대해서는 `ReadCommitted`로 낮춘다(카탈로그 일관성은 스키마 version 검사로 이미 보장됨). 어느 쪽이든 40001을 SQLSTATE와 함께 WARN으로 로깅해야 진단이 가능하다.

### AUD31-H11 (high) — 문서의 `pg_restore` 절차가 클라이언트가 서버보다 새로우면 실패하고 대상 DB를 비운다

- 위치: `docs/backup-restore.md:88` 부근(복원 레시피)
- 재현(라이브): 로컬 `pg_restore` 18.6 → 서버 16.13. 문서의 명령을 그대로 실행.

  ```
  pg_restore: error: could not execute query: ERROR:  unrecognized configuration parameter "transaction_timeout"
  Command was: SET transaction_timeout = 0;
  복원 후 스키마: NONE
  ```

  `pg_dump`는 성공해 유효한 아카이브(64KB)를 만들었고, **복원 단계만** 실패했다. `--single-transaction` 덕분에 부분 복원이 아니라 전부 롤백되어 안전하게 실패한다.
- 원인: `transaction_timeout`은 PostgreSQL 17에서 도입된 파라미터다. 18 계열 `pg_restore`가 이를 아카이브 재생 중 `SET`하는데 16.13 서버가 모른다.
- 영향: 이 복원 드릴은 문서가 규정한 **유일한 백업 검증 수단이자 재해 복구 경로**다. Homebrew가 최신 PostgreSQL을 추적하므로 최신 워크스테이션에서는 이 조합이 오히려 일반적이다. 재해 복구 시점에 하드 실패와 빈 데이터베이스를 만난다.
- 문서 상태: `docs/backup-restore.md:9`에 "Use client tools compatible with the server"라는 산문 한 줄이 있지만, 그 뒤에 복사·붙여넣기용 레시피를 제공하면서 버전 확인 단계를 넣지 않았다.
- 수정 방향: 복원 드릴에서 클라이언트를 서버 major에 고정한다(배포 문서가 이미 다른 단계에서 쓰는 `postgres:16.13` 이미지 활용). 또는 `pg_restore --version` vs `show server_version` 사전 점검 단계를 추가하고 "클라이언트 major ≤ 서버 major" 제약을 명시한다.

### AUD31-H12 (high) — 반쪽 복원된 DB에서 `doctor`가 ok:true를 반환해 문서의 모든 복구 검증 관문을 통과한다

- 위치: `internal/doctor/checks.go:68-78` (`checkCollections`)
- 원인: 검증이 **카탈로그 → 테이블 한 방향만** 수행된다. 쿼리가 `from pgbase_meta.collections`로 시작하므로, 카탈로그 행이 없는 `app` 테이블은 구조적으로 조회 대상에 들어오지 않는다. 반대 방향(테이블 → 카탈로그) 검사가 존재하지 않는다.
- 재현(라이브) — `docs/backup-restore.md:3`이 경고하는 바로 그 "반쪽 백업" 시나리오:

  ```
  pg_dump --schema=app            (pgbase_meta 제외)
  pg_restore → app 테이블 10개 복원, pgbase_meta 스키마 0개
  pgbase migrate status  → current=0 latest=3   (운영자의 자연스러운 다음 행동은 migrate up)
  pgbase migrate up
  pgbase doctor          → exit 0 (정상)
  ```

  최종 상태: `pgbase_meta.collections` = **0행**, `app` 테이블 = **10개 고아**. 그런데 `doctor`는 정상이라고 보고한다.
- 최소 재현: 정상 DB에 `create table app.stray (...)`만 추가해도 `doctor` exit 0. 카탈로그에 없는 테이블은 아예 보이지 않는다.
- 영향: `docs/backup-restore.md`가 규정한 수용 관문(`migrate status` + `doctor --json`)을 **복구 불가능한 데이터베이스가 통과한다.** 운영자는 재해 복구가 성공했다고 판단하고 서비스를 올리는데, 모든 컬렉션 정의가 사라진 상태다. 데이터는 디스크에 있지만 제품이 접근할 방법이 없다.
- 수정 방향: `doctor`에 역방향 검사를 추가한다 — `app` 스키마의 모든 테이블이 카탈로그에 대응 행을 갖는지 확인하고, 고아 테이블을 `collection_table_orphaned` 류의 finding으로 보고한다. 나아가 `app` 테이블이 존재하는데 카탈로그가 비어 있으면 반쪽 복원으로 간주해 명시적으로 실패해야 한다.

### AUD31-M01 (medium) — PostgreSQL 시스템 컬럼명이 plan을 통과한 뒤 apply에서 매핑되지 않은 500

- 위치: `internal/schema/names.go:10` (이름 규칙), `internal/schema/service.go:413-425` (`classifyApplyError`)
- 원인: `validateName`은 `^[a-z][a-z0-9_]{0,47}$`와 예약어 3개(`id`/`created_at`/`updated_at`)만 검사하므로 PostgreSQL 시스템 컬럼명이 통과한다. `CREATE TABLE`은 SQLSTATE **42701**(duplicate_column)로 실패하는데 `classifyApplyError`의 switch에 42701이 없어 `ErrConflict`도 `ErrDataConstraint`도 아닌 채 500이 된다.
- 재현(라이브): 각 이름으로 컬렉션 생성 시도.

  | 필드명 | plan | apply |
  | --- | --- | --- |
  | `xmin`, `ctid`, `tableoid`, `xmax`, `cmin`, `cmax` | 200 (checksum 발급) | **500 internal_error** |
  | `oid` | 200 | 201 정상 (PG 12+ 부터 일반 컬럼 가능) |

- 영향: plan이 성공적인 SQL 미리보기(`CREATE TABLE ... "xmin" text`)를 보여주며 checksum까지 발급한 뒤, 관리자가 승인하면 500이 난다. 미리보기의 신뢰성이 깨지고, 500이라 재시도 가능 여부도 알 수 없다.
- 수정 방향: `reservedNames`에 PostgreSQL 시스템 컬럼(`xmin`, `xmax`, `cmin`, `cmax`, `ctid`, `tableoid`)을 추가해 plan 단계에서 422로 거부한다. 보조적으로 `classifyApplyError`에 42701을 추가한다.

### AUD31-M02 (medium) — 기존 데이터 위반이 제약 종류에 따라 422와 409로 갈림

- 위치: `internal/schema/service.go:414-424` (`classifyApplyError`)
- 원인: `database.Classify(err) == KindConflict`가 switch보다 먼저 평가되어 23505(unique_violation)를 `ErrConflict`(409)로 보낸다. 반면 23502(not_null_violation)와 23514(check_violation)는 `ErrDataConstraint`(422)가 된다.
- 재현(라이브), 동일한 "기존 데이터가 새 제약을 위반" 상황:

  | 시나리오 | 응답 |
  | --- | --- |
  | `slug='same'` 2행 존재 → `unique: true` 적용 | **409 schema_conflict** |
  | `a`가 NULL인 행 존재 → `required: true` 적용 | **422 validation_failed** |

- 영향: 클라이언트가 "기존 데이터 때문에 실패"를 단일 코드로 처리할 수 없다. 게다가 409 `schema_conflict`는 optimistic-concurrency 충돌(다른 관리자가 먼저 변경)에도 쓰이는 코드라, 데이터 문제와 경쟁 문제가 같은 코드로 합쳐진다. 전자는 데이터를 고쳐야 하고 후자는 재-plan 후 재시도해야 하는데 구분이 불가능하다.
- 수정 방향: 23505를 `ErrDataConstraint`로 분류해 422로 통일하거나, 최소한 `details`에 원인을 구분해 담는다.

### AUD31-M03 (medium) — 인덱스 없는 컬럼 정렬이 전체 테이블 정렬을 강제 (비대칭 비용)

- 위치: `internal/query/compiler.go:155-167` (`resolveSortColumns`), `internal/schema/types.go:29` (`Indexed` 기본값 false)
- 원인: `resolveSortColumns`는 존재하지 않는 필드와 jsonb만 거부하고 **`field.Indexed`를 요구하지 않는다.** 인덱스는 컬렉션 생성 시 `indexed: true`로 opt-in해야 생기고 기본값은 false다. 즉 인덱스 없는 컬럼도 정렬 가능하다.
- 측정(200,000행, PostgreSQL 16.13, 동일 세션에서 `\timing`):

  | 정렬 대상 | 시간 | 실행 계획 |
  | --- | ---: | --- |
  | 인덱스 없는 컬럼 | **12.286 ms** | Parallel Seq Scan 200k행 + top-N heapsort |
  | 인덱스 있는 컬럼 | 0.279 ms | Incremental Sort + Index Scan (52행만 읽음) |
  | 기본 정렬 `created_at,id` | 0.284 ms | 전용 인덱스 사용 |

- 비대칭 배율 **약 44배**이며, 인덱스 정렬은 행 수와 무관하게 평탄한 반면 인덱스 없는 정렬은 테이블 크기에 선형으로 증가한다. 요청 크기는 동일하다(쿼리 문자열 몇 바이트).
- 영향: 인증된 관리자 한 명이 `sort=<unindexed>`를 반복하면 서버가 매 요청마다 전체 테이블을 정렬한다. `statement_timeout` 5s가 상한을 걸어주지만, 그 상한까지 CPU와 pool 커넥션을 점유한다. admin UI가 jsonb 아닌 모든 컬럼을 정렬 가능하게 노출하므로 의도치 않게도 발생한다.
- 참고: 관리자 인증이 필요하므로 외부 공격 표면은 아니다. 다만 "관리자만 접근"이 성능 보호로 이어지지는 않는다.
- 수정 방향: 인덱스 없는 컬럼 정렬을 거부하거나(명시적 422), 정렬 가능 컬럼에 인덱스를 자동 생성하거나, 최소한 문서와 UI에서 정렬 가능 컬럼을 인덱스 있는 것으로 제한한다.

### AUD31-M04 (medium) — 중복 JSON 키 방어가 overflow 숫자로 무력화

- 위치: `internal/api/admin_handlers.go:230-233`
- 원인: `json.Decoder.Token()`이 에러를 내면 `return nil`(=중복 없음)로 끝난다. `1e400`이 토크나이저를 중단시키면 **그 뒤의 모든 키는 검사되지 않고**, `encoding/json`의 last-key-wins가 적용된다.
- 재현(라이브): `{"payload":1e400,"label":"safe","label":"evil"}` → **201, `label="evil"` 저장**. 방어막 없는 같은 중복은 400. PATCH도 동일(200, `label='patched-evil'`).
- 도달 경로: 레코드 POST/PATCH(`map[string]json.RawMessage`), 스키마 `plan`(`fields[].default`가 RawMessage).
- **`apply`는 안전**: 방어막이 중복보다 앞서야 하는데 `fields`가 `name` 뒤에 오므로 400/`applyCalls=0`. login/setup도 all-string struct라 안전.
- 심각도 한정: `id`/`created_at`/미지의 컬럼은 하위 검증에서 422로 차단되어 권한 상승은 없다. UI는 객체를 직렬화하므로 수동 요청이 필요하다. 영향은 "중복된 두 값 중 어느 쪽이 이기는가".
- 부수 발견: 이 함수는 어떤 토크나이저 실패에도 fail-open이다. 즉 미래에 다른 에러 클래스가 생기면 같은 문제가 재발한다.

### AUD31-M05 (medium) — DB 장애 시 인증 라우트가 500, 로그인은 503

- 위치: `internal/api/admin_middleware.go:72`
- 원인: 자격증명 외 모든 에러를 500으로 하드코딩. 형제 함수 `writeAdminAuthError`(`admin_handlers.go:288`)는 `database.Classify`로 503 + `Retry-After: 1`을 반환한다.
- 재현(PostgreSQL 정지): 로그인 `503 + Retry-After: 1`, `/api/admin/collections`·`/session`·records 전부 **bare 500**.
- 영향: 일시적 장애가 서버 결함으로 보이고 백오프 힌트가 없다. `/health/ready`는 503을 정확히 반환하므로 오케스트레이터의 드레인은 정상 동작한다.

### AUD31-M06 (medium) — SQLSTATE class 53/58이 500으로 분류

- 위치: `internal/database/errors.go:41,50`
- 확인: `Classify`가 class 08은 prefix로 처리하지만, **53**(`53300` too_many_connections, `53200` out_of_memory, `53100` disk_full, `53400`)과 **58**(`58000` system_error, `58030` io_error, `58P01`, `58P02`)은 `KindUnknown` → 500이 된다. 직접 호출로 8개 코드 모두 확인.
- 영향: 커넥션 고갈·디스크 풀·I/O 오류는 재시도 가능한 unavailable 상태인데 500으로 나가 클라이언트가 백오프하지 않는다.

### AUD31-M07 (medium) — `migrate`/`doctor`/`admin`이 진단 정보를 모두 버림

- 위치: `internal/cli/run.go:261` (여기서만 logger를 붙인다)
- 재현: migration 2의 checksum을 손상시킨 뒤 `migrate up` → **`pgbase: command failed`만 출력**. 같은 조건에서 `serve`는 `apply system migrations: system migration 2 (record_page_indexes) checksum mismatch`를 정확히 보고한다.
- 영향: `docs/upgrades.md:19`가 "dirty·unavailable·예상치 못한 상태를 먼저 해결하라"고 요구하는데, 정작 그 상태를 진단할 수 없다. 업그레이드 절차에서 가장 필요한 명령들이 침묵한다.
- 동반 결함: `run.go:126,129`가 `reset-password` 실패도 `"create administrator"`로 라벨링한다(성공 경로 `run.go:132`는 올바르게 분기). 위 침묵 때문에 이 잘못된 라벨조차 표면화되지 않는다.

### AUD31-M08 (medium) — cleaner 실패가 영구히 침묵

- 위치: `internal/adminauth/cleaner.go:59-69`
- 확인: 에러를 `ctx.Err()` 확인에만 쓰고 버린다. import에 `log/slog`가 없고 `NewCleaner(pool, clock)`에 logger 파라미터가 없다(`pgbase/app.go:216`에서 그렇게 생성).
- 영향: 예컨대 least-privilege 차원에서 `REVOKE DELETE ON pgbase_meta.admin_rate_limits`를 하면 정리 작업이 영구 실패하는데 로그도 readiness 신호도 없다. rate-limit 삭제 실패가 세션 정리까지 멈춘다.
- 참고: 지수 백오프로 서버를 죽이지 않는 것은 AUD-M04의 의도된 수정 결과이며 올바르다. 문제는 관측 불가능성이다.

### AUD31-M09 (medium) — 긴 text 정렬값이 cursor 예산을 초과해 페이지네이션 파괴

- 위치: `internal/query/cursor.go:79` (`maxDecodedCursorSize = 4096`, cursor.go:17)
- 재현(라이브): 5000자 `text` 컬럼으로 정렬 → **1페이지 요청에서 `HTTP 400 invalid_cursor`**. cursor를 보내지도 않았는데 cursor 오류가 나온다. 서버가 자기 데이터에 대한 cursor를 만들지 못하는 상태.
- 임계값: 단일 text 정렬값 3873자까지 가능, 3874자부터 실패.
- 노출 경로: admin UI가 jsonb 아닌 모든 컬럼을 정렬 가능하게 하므로(`ui/admin/src/pages/records-page.tsx`) 헤더 한 번 클릭으로 발생.
- 범위 한정: 기본 정렬(`created_at, id`)과 개별 레코드 GET은 정상. 긴 컬럼 정렬만 실패.

### AUD31-L01 (low) — `/api/admin/collections/plan`만 Origin·CSRF 검사를 건너뛴다 (일관성 결함)

- 위치: `internal/api/router.go:48`
- 원인: 다른 모든 변경성 관리자 라우트는 `adminSessionMiddleware`(= origin 검사 + 인증 + CSRF)를 통과하는데, `plan`만 `authenticateAdminMiddleware`(인증만)로 감싸여 있다.
- 재현(라이브), 유효한 관리자 쿠키를 가진 상태:

  | 요청 | plan | apply(대조군) |
  | --- | --- | --- |
  | 쿠키 + 올바른 Origin + CSRF | 200 | 201 |
  | 쿠키 + `Origin: https://evil.example.com` + CSRF 없음 | **200** | **403** |
  | 쿠키 + Origin 없음 + CSRF 없음 | **200** | 403 |

- **실제 악용 가능성은 낮다.** 직접 확인한 완화 요소:
  - `plan`은 읽기 전용이다. 교차 출처 호출 전후 catalog version이 `0 → 0`으로 불변.
  - 응답에 `Access-Control-Allow-Origin`이 없어 브라우저가 공격자에게 본문을 읽어주지 않는다.
  - 존재/부재 oracle도 없다: 존재하는 컬렉션의 delete plan과 없는 컬렉션의 delete plan이 **둘 다 404**.
  - `PlanCreate`는 캐시된 카탈로그만 읽으므로(`internal/schema/service.go:43-48`) DoS 증폭도 없다.
- 따라서 이것은 "CSRF 취약점"이 아니라 **경계 일관성 결함**이다. 다만 `plan`이 나중에 상태를 갖게 되거나 CORS 설정이 추가되면 즉시 실제 취약점으로 바뀐다.
- 수정 방향: `plan`도 `adminSessionMiddleware`로 감싸 나머지와 통일한다. 읽기 전용이라 CSRF가 논리적으로 불필요하더라도, 경계를 라우트별로 다르게 두는 것 자체가 회귀 위험이다.

### AUD31-L02 (low) — `true`/`false`/`null` 이름 필드는 생성·정렬은 되지만 필터 불가

- 위치: `internal/query/filter.go:213` (parseOperand), `internal/schema/names.go:8-14`
- 원인: 이름 규칙이 `^[a-z][a-z0-9_]{0,47}$`이고 예약어는 `id`/`created_at`/`updated_at`/`pgbase_*`뿐이라 `true`/`false`/`null`이 허용된다. 그런데 filter 파서는 이 단어들을 식별자보다 먼저 리터럴로 소비한다.
- 재현(라이브): 필드 `true`(text), `null`(integer)로 컬렉션 생성 → 201. 레코드 기록 정상. `sort=true` → 200. 그러나 `filter=true='yes'`, `filter=null=5` → **422 `invalid_filter`** (대조군 `filter=ok='a'`는 200).
- 영향: 생성 시 경고 없이 영구히 질의 불가능한 컬럼이 만들어진다.

### AUD31-L03 (low) — 비밀번호 프롬프트가 인터럽트 불가 + 터미널 echo 미복구

- 위치: `internal/cli/password.go:55,62` (`term.ReadPassword`), `cmd/pgbase/main.go:13` (`signal.NotifyContext`)
- 원인: `signal.NotifyContext`가 컨텍스트를 취소하지만 `term.ReadPassword`는 raw mode blocking syscall이라 컨텍스트를 보지 않는다. 시그널은 포착되어 기본 종료 동작이 사라지는데 아무도 처리하지 않는다.
- 재현(실제 pty): `Password:` 프롬프트에서 SIGINT·SIGTERM 모두 무시, **SIGKILL 필요**. 종료 후 터미널이 **echo off 상태로 남는다**(운영자 셸에 입력이 보이지 않게 됨).

### AUD31-L04 (low) — prototype 이름 컬렉션 URL이 admin UI를 백지로 만듦

- 위치: `ui/admin/src/pages/records-page.tsx:29`, `record-edit-page.tsx:71`, `collection-edit-page.tsx:65`
- 원인: `catalog.collections[collectionName]`이 `Object.prototype` 멤버에 적중한다. `constructor`/`toString`/`valueOf`/`hasOwnProperty` 등은 truthy 함수라 `if (!definition)` 가드를 통과하고, 이후 `.fields.slice()`에서 throw.
- 재현(실제 Chromium): `/admin/collections/constructor/records` → `TypeError: Cannot read properties of undefined (reading 'slice')`, **body length 0**(완전 백지).
- 참고: 서버 이름 규칙상 그런 컬렉션은 만들 수 없으므로 URL을 직접 입력한 경우에만 발생한다.

### 테스트 공백 (위 결함들이 3라운드를 통과한 직접적 이유)

| 위치 | 공백 |
| --- | --- |
| `internal/api/security_test.go:107,111` | 중복 키 케이스가 login 경로에만 있다. 그 struct는 all-string이라 우연히 무력화 기법이 통하지 않는다. record/schema 경로에는 케이스가 없다. |
| `internal/database/pool_test.go:30` | statement timeout 테스트가 모두 1분 미만(`1500ms`, `5s`, `7s`)이라 `String()` 직렬화 결함이 보이지 않는다. |
| `internal/jsonvalue/fuzz_test.go:16` | PostgreSQL 호환성을 주장하면서 검증은 자기 재구현과의 self-consistency만 한다. 서버가 루프에 없다. |
| `internal/query/filter.go:315` | `maximumFilterNodes` 가드가 커버리지 0. verifier가 `-count=1`로 재확인: `filter.go:315.3,316.1 1 0`. 이 가드를 삭제해도 전체 테스트가 통과한다. |
| `internal/api/admin_middleware.go` 에러 경로 | `ErrInvalidCredentials`로만 테스트되어 DB unavailable 분기가 검증되지 않는다. |

## 3. 검증된 강점

감사 중 적극적으로 깨보려 했으나 견딘 항목들이다.

- **식별자 안전성이 2단계**: 이름은 `^[a-z][a-z0-9_]{0,47}$` 화이트리스트 + 예약어 차단을 통과해야 하고, SQL에는 `pgx.Identifier.Sanitize()`로만 나간다. filter/sort는 파서가 식별자를 만들지 않고 카탈로그의 실제 필드와 매칭해야 컬럼이 된다(`compiler.go:161,201`, `codec.go:37`에서 `unknown_field`). 인젝션 시도(filter/sort/limit/collection/field/type) 전부 차단, 테이블 무손상.
- **카탈로그가 오염돼도 DDL을 조종할 수 없다**: `pgbase_meta.collections.table_name`에 `victim"; DROP TABLE app.victim; --`를 직접 SQL로 주입한 뒤 `apply` 호출 → **409 `schema_conflict`**, victim 테이블 생존, DDL 미실행. `validateCatalogIntegrity`(`service.go:316`)가 apply 트랜잭션 **안에서** 모든 이름을 재검증한다. (`plan`은 읽기 전용이라 이 검사가 없다 — 올바른 배치다.)
- **동시성**: 같은 checksum/baseVersion으로 6-way 동시 `apply` → 정확히 1건 201, 5건 409, 테이블 1개. 빈 DB에 6-way 동시 `migrate up` → 3행, 중복 없음(transaction-scoped advisory lock + 5s `lock_timeout`). unique 제약 6-way 동시 삽입 → 1건 201, 5건 409, 행 1개.
- **cursor가 위조 불가**: HMAC이 version·collection ID·모든 정렬 필드와 방향·값·만료를 덮는다. 변조/다른 sort로 재사용/**다른 컬렉션에 재사용** 모두 400. 게다가 검증이 두 곳(`Decode`의 collection ID, `service.List`의 sort)에서 독립적으로 걸린다.
- **keyset 페이지네이션이 정확**: 동률이 많은 60행을 limit=7로 완주 → 9페이지, 중복 0, 누락 0. **동시 삽입 중 완주해도 중복 0**(offset 방식이라면 반복이 발생).
- **다중 인스턴스 일관성**: 인스턴스1이 컬렉션을 만들면 인스턴스2가 LISTEN/NOTIFY로 즉시 반영하고 바로 쓰기까지 성공. listener 연결을 서버 측에서 강제 종료해도 readiness 유지 후 재연결, 이후 전파도 정상.
- **비밀정보 취급**: Argon2id(64MiB, t=3, p=4) + `subtle.ConstantTimeCompare`. 세션은 원문이 아닌 HMAC만 저장(DB 유출만으로 세션 위조 불가). 도메인 분리된 HMAC. 캡처한 모든 로그에서 시크릿 출현 0건. 시작 실패 로그에서 `sslpassword` 미노출. rate limit subject도 해시.
- **세션 무효화가 요청마다 강제**: 만료·명시적 revoke·관리자 비활성화 3경로 모두 401. 비밀번호 재설정 시 기존 세션 즉시 무효화(AUD-H02 수정 확인).
- **입력 경계**: 1MB body 제한, filter 복잡도 제한(`filter_too_complex`), 250,000 깊이 중첩도 panic 없이 400, int32 overflow 422, numeric/bigint를 문자열로 전송해 `9007199254740993.000000001`까지 정확히 왕복.
- **트랜잭션 헬퍼**: `InTx`가 `committed` 플래그로 이중 종료를 막고, rollback은 취소된 컨텍스트와 분리된 bounded context로 실행(`tx.go:18-24`, `BoundedCleanupContext`).
- **`statement_timeout` 실효**: pool 연결에서 실제로 강제됨(500ms 설정 시 `pg_sleep(3)`이 SQLSTATE 57014로 취소).
- **장애 복구**: DB를 죽이면 즉시 500/503(행 없이), 재시작 후 **재시작 없이 약 2초 내 자동 복구**. SIGTERM에 exit 0, pool 연결 전부 반납.
- **에러 응답이 일관되게 안전**: 각 mapper가 500 기본값에서 좁혀가므로 미인식 에러는 generic으로만 나간다. panic recovery는 응답을 버퍼링해 부분 작성 유출을 막는다.
- **에러 분류 대부분은 정확**: 23505→409, 23502/23514/22P02→422, 40001/40P01/55P03/57014/57P01-03→503, class 08→503.
- **타입 왕복이 경계값에서 정확**: 직접 검증한 결과 — `bigint`는 int64 양끝(`9223372036854775807`, `-9223372036854775808`)을 정확히 보존하고 `9223372036854775808`은 422로 거부. `uuid`는 nil/max/UUIDv7 모두 왕복하며 대문자와 하이픈 없는 입력도 소문자 정규형으로 수용. `timestamptz`는 offset을 UTC로 정규화(`+09:00` → 03:00Z), 서기 1년과 9999년 처리, offset 없는 입력과 13월은 422. `numeric`은 `9007199254740993.000000001`까지 정확히 왕복(문자열 전송 덕분).
  - 다만 nanosecond 입력이 microsecond로 **조용히 절삭**된다(`.123456789Z` → `.123456Z`). PostgreSQL `timestamptz`의 실제 정밀도가 microsecond이므로 동작 자체는 올바르지만, API가 `RFC3339Nano`를 받아들이면서 이 절삭을 문서화하지 않는다(`docs/`에 관련 언급 없음). 정밀도가 중요한 클라이언트는 놓칠 수 있다.
- **미인증 상태에서 admin UI가 데이터를 노출하지 않는다**: 컬렉션 `secretcollection`을 만든 뒤 쿠키 없이 `/admin/`, `/admin/collections`, `/admin/collections/secretcollection/records`를 요청했다. 모두 200이지만(SPA fallback으로 의도된 동작) 본문은 빈 `<div id="root">`뿐이고 컬렉션 이름·필드명 출현 0건. SPA가 호출하는 API 4개(`/api/admin/collections`, `/session`, `/system`, records)는 모두 401. 즉 200은 셸일 뿐 데이터 경계는 API에서 정확히 막힌다.
- **`doctor`가 스키마 drift를 실제로 잡는다**: `alter table app.docs drop column body`로 카탈로그와 실제 테이블을 어긋나게 만든 뒤 `doctor` 실행 → `error collection_column_missing: A catalog collection column is missing`로 정확히 진단. 진단 코드가 11종(`collection_table_missing`, `collection_column_missing`, `collection_column_type_mismatch`, `collection_column_nullability_mismatch`, `collection_index_missing`, `collection_primary_key_missing`, `migration_checksum_mismatch`, `migration_not_current`, `migration_status_unavailable`, `schema_state_stale`, `database_unreachable`)으로 세분화돼 있어 운영 도구로서 실효성이 있다.
  - 다만 같은 drift 상태에서 레코드 조회 API는 **bare 500**을 반환한다(`internal_error`). 즉 진단은 가능하지만 런타임 응답은 원인을 알려주지 않는다. AUD31-M05/M06과 같은 계열의 분류 문제다.

## 4. 정직한 한계

- 아래 항목은 **코드 읽기 + 컨테이너 PostgreSQL** 기준이다. 실제 운영 환경(프록시 뒤 TLS, 여러 노드, 장기 부하, 대용량 데이터)은 검증하지 않았다.
- 부하·장시간 안정성·용량 한계는 측정하지 않았다. 벤치마크는 10,000행 단일 노드 기준이다.
- SQLSTATE 53300(커넥션 고갈)은 코드 레벨(`Classify` 직접 호출)로만 확인했다. 컨테이너의 `postgres` 역할이 superuser라 예약 슬롯 때문에 라이브 재현에 실패했다.
- PostgreSQL 16.13·17.9·18.3에서 전체 스위트를 실행했다(각각 skip 0건). 다만 버전별 e2e는 18.3에서만 추가 실행했다.
- AUD31-H01의 라이브 재현은 공유 bucket 행을 직접 차단 상태로 만들어 수행했다. 1000회 실제 요청을 50 IP로 보내는 실증은 하지 않았고, 대신 산술(1000/900초, IP당 20 상한 → 50 IP, 0.022 req/s)로 비용을 확인했다.

## 5. 이 결함들이 3라운드를 통과한 공통 원인

모두 **"검증됨"과 "실제로 실행됨" 사이의 틈**에 있다.

유닛 테스트는 validator를 검증하고, 통합 테스트는 기본값 근처의 값만 사용한다. 그래서 *유효하지만 흔치 않은 입력*이 전체 경로를 끝까지 통과하는 경우가 없다.

- 중복 키 가드는 **정상 JSON으로만** 테스트된다 → 토크나이저 자체를 중단시키는 입력이 없다.
- statement timeout은 **validator 통과**만 확인된다 → `String()` 출력이 PostgreSQL에 도달하는 테스트가 없다.
- `jsonvalue`는 **자기 재구현과 비교**된다 → PostgreSQL과의 실제 차이가 보이지 않는다.
- 미들웨어 에러 경로는 **자격증명 오류로만** 테스트된다 → DB 장애 분기가 비어 있다.
- filter 복잡도 가드는 **커버리지 0** → 삭제해도 CI가 통과한다.

권장: 경계값을 실제 PostgreSQL까지 통과시키는 differential test를 각 validator에 추가한다. 특히 `jsonvalue`는 PostgreSQL을 루프에 넣어야 존재 이유를 충족한다.

## 6. 감사 방법

- 8개 서브시스템을 독립 에이전트가 전수 정독 → 각 발견을 별도 verifier가 **반증 시도**(기본값 refuted, 코드를 직접 읽고 도달 가능성을 확인해야 생존) → 종합.
- 병행하여 오케스트레이터가 직접 라이브 검증: 실제 바이너리·실제 PostgreSQL·실제 Chromium·실제 pty·실제 컨테이너.
- 모든 finding은 코드 인용, 라이브 재현, 구체적 실패 경로를 갖춘다. 재현되지 않은 추측은 기록하지 않았다.
- 감사 중 만든 임시 probe test는 모두 삭제했고, `git status`는 이 문서 외 tracked 변경이 없음을 확인했다.

## 7. 수정 종결과 최종 재검증

2절은 기준 커밋의 수정 전 상태를 기록한 역사적 증거다. 이후 현재 워킹트리에서 High 12건, Medium 11건, Low 4건(총 27건)을 모두 수정했으며 알려진 미해결 구현 finding은 없다.

| 해결 영역 | 종결된 finding |
| --- | --- |
| 인증 제한과 운영 복구 | AUD31-H01, AUD31-H02 |
| timeout, query 입력, numeric, release gate, dirty migration, DDL, indexed text | AUD31-H03 ~ H09 |
| widening, numeric scale, 동시 수정, restore, doctor | AUD31-M10, AUD31-M11, AUD31-H10 ~ H12 |
| 식별자, 제약 오류, sort 비용, JSON, middleware/DB 오류, CLI, cleaner, cursor | AUD31-M01 ~ M09 |
| 관리자 mutation 경계, 리터럴 이름, 터미널 복구, prototype-safe UI lookup | AUD31-L01 ~ L04 |

각 수정은 해당 경계의 단위 또는 live PostgreSQL 회귀 테스트를 동반한다. 특히 입력을 조용히 무시하던 query parsing, PostgreSQL과 달랐던 JSON number 검증, 분 단위 statement timeout, schema apply 오류 분류, pooled prepared statement invalidation, numeric scale 보존, 동시 write 재시도, 복원 불완전 상태와 relation drift 진단을 실제 데이터베이스 경로에서 검증한다. 릴리스 workflow는 전체 backend/frontend/E2E gate가 성공한 커밋만 이미지를 발행하도록 연결했다.

Relation/schema lifecycle 구현 뒤에는 별도 독립 리뷰와 반증 검증을 수행해 다음 후속 문제도 수정했다.

- nested expand의 중복 경로가 지수적으로 materialize될 수 있던 구조를 선형 bounded plan으로 변경했다.
- reverse expand의 남은 row budget이 0인 정상 empty 결과를 오류로 오인하던 경계를 수정했다.
- 실행 중 catalog가 바뀐 경우 잘못된 physical relation binding을 사용할 수 있던 경로를 transaction 안에서 재검증한다.
- 4 MiB 응답 예산을 확인하기 전에 큰 target page를 과도하게 materialize하지 않도록 단계별로 계측·중단한다.
- relation sort의 correlated subquery를 bounded `LEFT JOIN` chain으로 바꾸고 numeric private cursor의 scale을 보존한다.
- relation sort는 target join 전에 root 100,000행 hard budget을 적용해 지원 범위 밖 비용을 안정적인 validation error로 거부한다.
- PostgreSQL 16/17의 deferred constraint SQLSTATE 차이와 1 ms statement-timeout 테스트의 초기화 순서/타이밍 flake를 제거했다.

최종 워킹트리 검증 결과:

| Gate | 결과 |
| --- | --- |
| PostgreSQL 16.13 | `go test -race ./... -count=1` 전체 통과, skip 0 |
| PostgreSQL 17.9 | `go test -race ./... -count=1` 전체 통과, skip 0 |
| PostgreSQL 18.3 | 최신 워킹트리 `go test -race ./... -count=1` 전체 통과, skip 0 |
| Docker black-box E2E | `go test -tags=e2e ./tests/e2e -v -count=2 -shuffle=on` 전체 통과 |
| Backup/restore | 실제 single/multiple relation과 ordered edge를 PostgreSQL 16.13의 빈 DB에 복원; metadata/value/order/doctor 검증 통과 |
| Admin UI | typecheck, 12 files / 80 tests, production build 통과; committed dist와 동기화 |
| 정적 gate | gofmt, `go vet ./...`, `go build ./...`, `git diff --check` 통과 |
| 보안·운영 artifact | reachable Go vulnerability 0, high npm vulnerability 0, Prometheus dashboard JSON 및 8개 alert rule 검증 통과 |

이 종결은 현재 문서화된 pre-v0.1 관리자용 PostgreSQL data plane과 bounded relation slice의 구현 완료를 뜻한다. 공개 사용자 인증/RBAC, 파일, realtime delivery worker, SDK/OpenAPI, online DDL, sharding과 실제 목표 부하의 장기 운영 SLO는 이번 finding 수정 범위가 아니며 완료로 주장하지 않는다.
