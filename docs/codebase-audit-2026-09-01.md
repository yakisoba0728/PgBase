# PgBase 전체 코드베이스 감사 보고서 (2026-09-01)

> **종결 상태 (2026-09-01):** 아래 확정 finding 37건 중 34건을 수정하고 각각 회귀 테스트로 고정한 뒤 PR #10(`03a0fc1`)으로 `main`에 병합했다. 미수정 3건과 잔여 커버리지 공백은 7절에 명시한다.

## 0. 이 문서의 상태

- 기준 커밋: `main` @ `05b6e50` (Merge pull request #9 from webisnoteasy/codex/relation-production-hardening)
- 종결 커밋: `main` @ `03a0fc1` (Merge pull request #10) — 68 파일, +2,706 / −231
- 이전 감사: [2026-08-28](codebase-audit-2026-08-28.md) 29건, [2026-08-31](codebase-audit-2026-08-31.md) 27건. 둘 다 "알려진 미해결 구현 finding 없음"으로 종결됐다.
- 이번 감사가 찾은 것은 **모두 그 두 라운드를 통과한 항목**이다. 공통 원인은 6절에 정리했다.

감사 규모: 3개 워크플로로 41개 에이전트를 운용했다.

| 라운드 | 에이전트 | 방식 | 산출 |
| --- | --- | --- | --- |
| 4라운드 | 29 | 패키지별 수직 분할 10 + 관심사별 수평 분할 10 → 적대적 검증 8 → 종합 1 | 후보 53 → 생존 29 → 확정 23 |
| 5라운드 | 8 | 소스 246개 파일 57,172줄을 줄 수 기준 8등분해 **전수 완독** | 21건 (중복·기반박 제외 후 14건) |
| 검증 | 5 | 확정 후보 44건을 4묶음으로 적대 검증 → 종합 1 | 생존 39 → 병합 후 **확정 37** |

## 1. 이번 감사의 방법론적 차이

이전 세 라운드는 전부 **주제별로** 범위를 나눴고, 에이전트가 열 파일을 스스로 골랐다. 그래서 아무도 열어보지 않은 파일이 남았다 — `SECURITY.md`가 실제로 그렇게 빠져 있다가 문서 담당 에이전트가 스스로 범위를 넓혀서 겨우 걸렸다(MEDIUM-6).

5라운드는 **행 단위 전수 커버리지**로 갔다. 빌드 산출물과 lockfile을 제외한 tracked 소스 246개 파일을 줄 수 기준으로 8등분하고, 각 에이전트에게 배정분을 첫 줄부터 마지막 줄까지 읽게 한 뒤 완독 목록을 구조화 출력으로 받아 배정 목록과 대조했다. **246/246 완독, 누락 0건.**

두 방식은 서로 다른 계층을 잡았다.

- 주제별 분할은 **계층을 가로지르는** 결함에 강했다. HIGH-1은 미들웨어와 핸들러를 겹쳐 봐야만 보인다. 적대적 검증이 후보 53건 중 24건을 실제로 걸러냈다.
- 전수 정독은 **테스트 착시**를 대량으로 잡았다. 테스트 파일을 한 줄씩 읽어야만 보이는 계층이다. 대신 검증 단계가 없어 21건 중 7건이 재탕이었고, 새 high는 나오지 않았다.

## 2. 검증 베이스라인

기준 커밋에서 라이브 PostgreSQL 16.13을 연결해 실행했다. `PGBASE_TEST_ADMIN_URL` 없이 실행하면 **496건 중 96건이 조용히 skip되면서도 모든 패키지가 `ok`로 보고되므로**, 이 변수 없는 실행 결과는 신뢰할 수 없다.

| 항목 | 결과 |
| --- | --- |
| `gofmt -l .` / `go build ./...` / `go vet ./...` | clean |
| TODO·FIXME·`nolint` 주석 | 0건 |
| 무시된 에러 (`_ =`) | 5건, 전부 정당한 cleanup 경로 |
| 패키지 의존 그래프 | 층위 위반·순환 없음 |

## 3. 확정 finding

### HIGH-1 (high) — Content-Type 정규화 불일치로 미인증 본문 크기 제한 완전 우회

- 위치: `internal/api/middleware.go:168`, 대응 지점 `internal/api/admin_handlers.go:245-250`
- 원인: 본문 제한을 **거는** 판정과 본문을 **읽는** 판정이 서로 다른 함수다. `bodyLimitMiddleware`는 `strings.HasPrefix`를, `decodeStrictJSON`은 `mime.ParseMediaType`을 쓴다. 후자는 내부에서 `strings.TrimSpace`(→ `unicode.IsSpace`, U+00A0 포함)를 수행하고 전자는 하지 않는다.
- 재현 (실제 `net.Listen` + `net/http` 서버, raw TCP):

  ```
  [normal]      gate: MaxBytesReader INSTALLED  read=1048576  readErr=http: request body too large
  [U+00A0 NBSP] gate: MaxBytesReader SKIPPED    read=5242880  readErr=<nil>
                rawCT=" application/json"  firstBytes=[c2 a0 61 70]  parsed="application/json"
  ```

  `net/http`가 헤더 값의 `c2 a0`을 obs-text로 보존하므로 게이트는 건너뛰고 디코더는 통과시킨다.
- 도달 경로: **미인증.** `router.go:48-50`의 `POST /api/admin/setup`과 `POST /api/admin/auth/login`은 `sameOriginMiddleware`만 거치고, rate limit은 `Login` 안에 있어 디코드가 끝난 뒤에야 작동한다. 남는 상한은 `ReadTimeout: 15s`뿐이다. 저장소 전체에서 `MaxBytesReader`는 `middleware.go:169` 한 곳뿐이라 대체 가드가 없다.
- 수정: Content-Type과 **무관하게** 모든 요청에 `MaxBytesReader`를 설치한다. 현재 어떤 라우트도 1 MiB 초과 본문을 정상 입력으로 받지 않으므로, 두 판정을 일치시키는 것보다 새 판정 함수가 또 어긋날 여지를 없애는 편이 안전하다.

### MEDIUM-2 (medium) — `WriteTimeout: 30s`가 DDL apply 응답을 절단

- 위치: `pgbase/app.go:274`
- AUD31-H08이 `set local statement_timeout = 0`으로 PostgreSQL 계층 상한을 없앴지만 HTTP write deadline은 그대로다. 30초를 넘는 schema apply는 **커밋에 성공하고도** 어떤 HTTP 상태도 전달하지 못한다. 재시도하면 baseVersion이 이미 올라가 409가 나와, 적용 여부를 판단할 근거가 응답에 없다. `docs/deployment.md:50`은 정반대("large schema work can finish")를 약속한다.

### MEDIUM-3 (medium) — keyset 페이지네이션이 동시 커밋 레코드를 영구 누락 *(미수정)*

- 위치: `internal/query/compiler.go:42`
- `created_at`이 `statement_timestamp()`(문 시각)으로 확정되는데 행 가시성은 COMMIT 시점에 생긴다. 그 간극에 커밋된 레코드가 이미 발급된 커서보다 앞선 `created_at`을 가지면 이후 어떤 페이지에도 나타나지 않는다. **오류도 중복도 없이** 목록 완주성이 깨진다. `id`도 트랜잭션 진입 전 생성되는 UUIDv7(`recordops/service.go:202`)이라 구제하지 못한다.
- 시계 대신 시퀀스/워터마크 기반 커서로 바꾸는 설계 변경이 필요해 이번 범위에서 제외했다.

### MEDIUM-4 (medium) — `created_at` 내림차순 정렬이 인덱스 순서를 쓸 수 없음

- 위치: `internal/query/compiler.go:394`, `internal/query/filter.go:376`, `internal/schema/planner.go:525`
- `compileOrder`가 모든 컬럼에 `NULLS LAST`를 붙이고 tiebreaker `id`는 항상 ASC로 덧붙여져, `sort=-created_at`이 `ORDER BY created_at DESC NULLS LAST, id ASC NULLS LAST`가 된다. 유일한 인덱스는 `(created_at, id)` ASC라 정방향·역방향 어느 쪽으로도 매칭되지 않는다.
- **라이브 실측 (PostgreSQL 16.13, 200,000행):**

  | ORDER BY | 계획 | 버퍼 | 시간 |
  | --- | --- | --- | --- |
  | `created_at ASC NULLS LAST, id ASC NULLS LAST` | Index Only Scan | 5 | 0.038 ms |
  | `created_at DESC NULLS LAST, id ASC NULLS LAST` (수정 전) | Parallel Seq Scan + top-N heapsort | 1,933 | 19.97 ms |
  | `created_at DESC NULLS LAST, id DESC NULLS LAST` | Parallel Seq Scan (여전히 실패) | 1,934 | 8.91 ms |
  | `created_at DESC NULLS FIRST, id DESC NULLS FIRST` (수정 후) | **Index Only Scan Backward** | **5** | **0.071 ms** |

  세 번째 행이 결정적이다 — **방향만 통일해서는 고쳐지지 않고 `NULLS FIRST`까지 함께 뒤집어야 한다.** 실제 keyset 술어를 WHERE에 넣어도 동일하다(109 버퍼 / 0.610 ms vs 1,933 버퍼 / 9.49 ms).
- 관리자 UI에서 `created_at` 헤더를 두 번 클릭하면 나오는 가장 흔한 조작이다.
- 수정: NULLS 배치를 컬럼별로 한 번만 결정해 `compileOrder`와 `compileKeyset`이 어긋날 수 없게 했다. NOT NULL 컬럼(`id`는 PK, `created_at`은 `not null`)의 내림차순만 `NULLS FIRST`가 되고, NULL 가능한 사용자 필드는 `NULLS LAST`를 유지한다. keyset 경계 술어도 배치에 맞춰 분기시켰다.

### MEDIUM-5 (medium) — 4 MiB 초과 레코드 한 건이 그 지점부터 목록을 막음

- 위치: `internal/query/root_page.go:114`, `internal/query/service.go:147`
- `root_page.go:114`의 가드가 `len(Items) > 0`을 요구하므로 초과 레코드는 페이지의 첫 항목으로는 **항상** 포함되고, 그러면 `service.go:147-149`의 `len(page.Items) <= 1`이 참이 되어 영구히 422를 반환한다. 커서로 건너뛸 수단이 없어 **그 레코드와 정렬순서상 뒤의 모든 레코드가 목록에서 도달 불가**가 된다.
- 원인은 `codec.go:111-113`이 indexed/unique 텍스트만 2048바이트로 제한하고 unindexed 텍스트는 무제한이라는 점이다(`codec_test.go:197`이 unbounded를 명시적으로 단언했다). 1 MiB 미만 PATCH를 여러 번 보내 4 MiB 초과 레코드를 만들 수 있다.
- 수정: 병합된 레코드의 직렬화 크기를 쓰기 시점에 제한해 읽기 경로가 구조적으로 막히지 않게 했다.

### MEDIUM-6 (medium) — `SECURITY.md`가 복구 불가능한 백업 절차를 지시

- 위치: `SECURITY.md:22`, `SECURITY.md:24`
- 백업 대상을 `pgbase_meta`와 `app` 두 스키마로만 지시한다. `docs/backup-restore.md:3`은 `pgbase_relation`까지 세 스키마가 모두 필요하고 하나라도 빠지면 복구 불가라고 명시한다. `app` 테이블은 `pgbase_relation`에 의존하지 않으므로 **복원 자체는 조용히 성공하고** 모든 multiple relation 값과 순서만 소실된다.
- `git log --follow`로 확인한 결과 `SECURITY.md`를 도입한 커밋이 relation을 구현한 `5c18012`보다 앞서고 이후 갱신되지 않았다. 같은 절이 이미 출시된 `relationships`를 미포함 기능으로 열거해 보안 보고 범위 밖으로 선언하고 있었다.

### FR-2 (medium) — delete graph의 파생 UPDATE만 다른 타임스탬프 함수를 씀

- 위치: `internal/recordops/delete.go:318`
- 사용자 테이블 쓰기 중 이 한 줄만 `transaction_timestamp()`를 쓰고 나머지(`internal/query/compiler.go:42,90`)는 `statement_timestamp()`를 쓴다. `docs/production-kernel-tradeoffs.md:26`이 명시한 단조성 계약을 delete가 유발하는 FK-clear UPDATE만 위반한다. 동시에 커밋되는 PATCH를 더 오래된 시각으로 덮어쓸 수 있다.

### FR-13 (medium) — 컬렉션 이름 타이핑 중 relation 타깃이 하이재킹됨

- 위치: `ui/admin/src/pages/collection-create-page.tsx:120`
- `onChange`가 `name !== "" && field.relation?.targetCollection === name`(클로저의 이전 state)만으로 self-relation을 판정한다. 기존 컬렉션 `users`를 타깃으로 고른 뒤 새 이름을 타이핑하다 우연히 `users`를 거치면(예: `users` → `users_profiles`) 그 순간부터 타깃이 매 keystroke마다 입력값을 따라간다. 관리자가 인지하지 못한 채 잘못된 스키마가 생성된다.

### FR-1 / LOW-10 (medium) — 점 없는 relation 필드 정렬이 relation-path 계획으로 내려감

- 위치: `internal/query/relation_filter_sort.go:178`
- `field.Type != schema.TypeRelation` 조건 때문에 `sort=owner`처럼 점 없이 루트 relation 필드를 지정하면 scalar fast path에서 배제되어 `resolveRelationPath`로 빠지고, target join과 100,000행 예산에 걸린다. 실제로는 스칼라 UUID 컬럼 정렬이면 충분하다.
- 4라운드는 low, 5라운드는 medium으로 평가했고 검증에서 두 항목이 같은 버그로 확인돼 병합했다.

### LOW 29건

| key | 위치 | 요약 |
| --- | --- | --- |
| LOW-7 | `internal/adminauth/service.go:218` | 실존 이메일은 row-lock으로 직렬화되고 비실존은 병렬 실행돼, 응답 시간 차이로 계정 존재 여부를 추론할 수 있다 |
| LOW-8 | `internal/schema/service.go:618` | `Service.Catalog`가 40001 직렬화 실패에 재시도가 없다(대조군 `query/service.go:179`에는 있다) |
| LOW-9 | `internal/schema/defaults.go:151` | timestamptz 기본값에 상한 연도 검증이 없어 9999년 근접값이 반올림 캐리로 5자리 연도가 되어 자기 round-trip이 깨진다 |
| LOW-11 | `migrations/system/embed_test.go:29` | checksum 불변성 테스트가 마이그레이션 5개 중 4개만 검사하고 최신(relation_schema)을 빠뜨렸다 |
| LOW-12 | `internal/doctor/checks.go:202` | doctor가 planner가 항상 만드는 `(created_at, id)` 인덱스를 전혀 검증하지 않는다 |
| LOW-13 | `ui/admin/src/pages/setup-page.tsx:28` | 서버가 절대 채우지 않는 `error.details[field]`를 참조하는 죽은 코드라 비밀번호 too_short/weak 메시지가 표시되지 않는다 |
| LOW-14 | `internal/recordops/cleaner.go:64` | 정리 실패 로그에 `err` 필드가 없어 원인이 사라진다 |
| LOW-15 | `internal/query/codec.go:290` | numeric non-finite 가드가 M11 수정으로 도달 불가가 됐는데, 검증한다는 테스트가 fixture 타입 불일치라는 다른 이유로 통과한다 |
| LOW-16 | `docs/production-kernel-handoff.md:327` | 인수인계 절차가 라이브 PostgreSQL 준비물 없이 `go test ./...`를 지시해 96건 silent skip을 통과로 오인하게 한다 |
| LOW-17 | `ui/admin/src/pages/record-edit-page.tsx:75` | 서버의 `value_too_long` 코드에 대한 UI 메시지 매핑이 없다 |
| LOW-19 | `ui/admin/src/pages/record-edit-page.tsx:324` | 삭제 실패 catch에 422 분기가 없어 relation restrict 사유가 UI에서 사라진다 |
| LOW-20 | `ui/admin/handler.go:71` | 잘못된 UTF-8·후행 슬래시로 생기는 `fs.ErrInvalid`가 SPA fallback 대신 500이 된다 |
| LOW-21 | `internal/query/service.go:150` | 페이지 트리밍 루프가 초과할 때마다 전체를 재-marshal해 worst-case O(n²)다 |
| LOW-22 | `internal/cli/password_terminal_darwin_test.go` | termios 로직이 darwin 전용 테스트로만 커버되는데 CI는 전부 ubuntu라 회귀 탐지 공백이다 |
| LOW-23 | `deployments/docker-compose.yml:38` | `PGBASE_SHUTDOWN_TIMEOUT` 기본 15s가 Compose 기본 `stop_grace_period`(10s)보다 커서 자기 문서 규칙을 위반한다 |
| FR-3 | `internal/query/relation_filter_sort.go:697` | 커서가 있으면 관계없는 검증 오류(`unknown_field` 등)까지 전부 `invalid_cursor`로 덮어쓴다 |
| FR-4 | `pgbase/options.go:108` | `StatementTimeout`에 상한 검증이 없어 극단값이 PostgreSQL GUC 상한을 넘으면 불투명한 오류가 된다 |
| FR-5 | `internal/relation/codec_test.go:108` | "required single clear" 라벨이 붙은 케이스가 실제로는 required **multiple** relation을 대상으로 한다 |
| FR-6 | `internal/cli/run.go:289` | DSN 파싱 실패 분기만 진단을 남기지 않아 migrate/doctor/admin이 아무 로그도 남기지 않는다 |
| FR-7/10/16 | `ui/admin/src/api/client.ts:37` | `typeof response.text !== "function"` 분기가 브라우저에선 도달 불가한 mock 전용 프로덕션 코드라, 페이지 테스트 8~9개가 출시되는 lossless-json 파서를 한 번도 실행하지 않는다 |
| FR-8 | `internal/schema/names.go:10` | `expand`가 예약어에 없어 그 이름의 필드를 만들면 그 컬렉션에서 expand가 영구히 불가해진다 |
| FR-9 | `internal/schema/defaults.go:46` | indexed/unique text **기본값**에 2048바이트 상한이 적용되지 않아 서버가 만든 값을 서버가 API로 되받지 못한다 |
| FR-11 | `internal/repositorypolicy/observability_artifacts_test.go:64` | 문서 링크 무결성 가드가 출발 문서를 열지도 않아 대상 파일만 있으면 통과한다 |
| FR-15 | `ui/admin/src/api/client.ts:40` | catch-all이 본문 읽기·파싱 실패를 `undefined`로 바꾸는데 호출부는 2xx를 성공으로 처리한다 |
| FR-18 | `internal/schema/listener_test.go:150` | `t.Cleanup` 역순 실행으로 drain 정리가 allowReady 정리보다 먼저 돈다 |
| FR-19 | `internal/recordops/service_test.go:745` | `errors.Is(ErrRecordNotFound, ErrRecordNotFound)`가 센티널을 자기 자신과 비교해 가드가 절대 실패할 수 없다 |
| FR-20 | `deployments/observability/prometheus-alerts.yml:86` | annotation은 "response-size guard"까지 덮는다고 쓰지만 응답 크기 초과는 `outcome="validation"`으로 기록돼 그 alert가 절대 발화하지 않는다 |
| FR-21 | `internal/recordops/service.go:74` | relation 텔레메트리를 `request.Expand != ""`로 켜서 `sort=owner.name`의 예산 거절이 무기록으로 사라지고, 라벨도 expand로 잘못 계상된다 |

## 4. 검증에서 탈락한 주장

다음 감사가 같은 것을 다시 파지 않도록 근거와 함께 기록한다.

- **FR-1 (LOW-10과 중복)** — 같은 `field.Type != schema.TypeRelation` 분기, 같은 `relation_sort_budget_exceeded`. 한 건으로 병합했다.
- **`Retry-After`가 CORS expose 목록에 없음** — 사실이나 도달 불가능한 피해자다. 관리자 쿠키가 `SameSite=Lax`라 교차 출처 fetch에 실리지 않아 401로 먼저 끝난다.
- **`session.tsx:53`의 `setup_required` 404 분기** — 도달 불가 관찰 자체는 정확하지만 [2026-08-28 감사 §7](codebase-audit-2026-08-28.md)에 이미 거의 동일한 문장으로 기록됐다. 재탕.
- **pocketbase 리서치 문서의 "Missing" 갭 매트릭스** — `git log --follow` 결과 이 문서를 도입한 커밋이 바로 그 항목(relation)을 구현한 커밋 자신이고, 문서 헤더가 스스로 `Status: primary-source research snapshot`이라 규정한다. 사후 갱신 누락이 아니라 사전 설계 스냅샷이다.
- **`PGBASE_TRUSTED_PROXIES` 기본값에서 network 축 rate-limit 붕괴** — 전제가 `docs/deployment.md:107`이 명령형으로 지시하는 설정을 어긴 상태다. 또한 `admitRateLimit`(`service.go:302-321`)은 차단 중이면 `recordRateLimitAttempt`를 실행하지 않으므로 `greatest(...)` 연장이 갱신되지 않는다 — 최대 15분 뒤 자동 만료되는 정상 rate limiter다.
- **로그 redaction의 URL 비밀번호 미마스킹** — 정규식 약점은 실재하나 도달 가능한 sink가 없다. `config.LoadEnv`가 스스로 `Normalized()`를 호출하고 CLI가 `configurationError{message:""}`로 감싸 DSN을 전혀 출력하지 않는다. defense-in-depth 공백으로만 남긴다.
- **`23503`/`23001`이 `Classify`에 미등록** — 사실이나 도달 불가. 데이터 계층 FK는 `NO ACTION DEFERRABLE`이고 cardinality/position 트리거가 명시적 `ERRCODE='23514'`(→422)를 지정한다. 메타데이터 `RESTRICT`는 `ApplyDelete`가 advisory lock + `schema_state FOR UPDATE` + 트랜잭션 내부 재계획 후에야 DDL을 실행해 TOCTOU 창이 없다. audit/outbox `RESTRICT`는 해당 테이블을 DELETE 하는 코드가 저장소에 없다.
- 그 외 기각: `schemaDigest`가 Unique/Indexed 포함(의도), 미사용 `idempotencyLifetime` 상수, catch-all `/`가 405를 가림(의도된 설계, 에러 봉투 계약 유지), 로그인 KDF가 pool 커넥션 점유(AUD-H02 수정의 의도된 결과), numeric default 후행 zero 절삭(테스트로 고정된 의도), create/update의 `?expand=` 미지원(구현 전 설계 아티팩트만 어긋남), `record_audit_events` retention 부재(공개된 트레이드오프), CLI usage에서 `healthcheck` 누락(테스트로 고정된 의도).

## 5. 확인된 강점

적극적으로 깨보려 했으나 견딘 것 중 [2026-08-31 §3](codebase-audit-2026-08-31.md)에 없는 것만 남긴다.

- **rate limiter가 차단 중 자기 연장하지 않는다.** `admitRateLimit`이 blocked이면 `recordRateLimitAttempt`를 건너뛰므로 공격자가 차단을 무한 연장하는 self-DoS가 성립하지 않는다. `LockRateLimits`가 키를 정렬 후 중복 제거해 획득하므로 버킷 간 데드락 순환도 없다.
- **설정 오류가 DSN을 로그로 흘리지 않는다.** `pgbase.New`가 받는 options는 이미 정규화된 값이라 같은 검증이 두 번 실패할 수 없다는 구조가 이를 보장한다.
- **커서 크기가 발급 시점에 유계다.** `cursor.go:85`가 private 경로로 전환하고 디코드 양쪽에서 base64 길이와 raw 길이를 모두 검사한다.
- **`fs.Sub` 기반 정적 핸들러가 traversal에 견고하다.** LOW-20의 `ErrInvalid`는 탈출이 아니라 오분류 문제일 뿐이다.
- **마이그레이션 checksum 강제가 실제로 작동한다.** LOW-11은 강제력이 없다는 게 아니라 그것을 트리거하는 편집을 테스트가 잡지 못한다는 문제다.
- **relation expand 예산 회계가 촘촘하다.** forward/reverse 모두 DB 접근 직전에 예산을 선차감하며 우회 경로를 찾지 못했다. depth 6 / node 20 상한으로 순환도 구조적으로 종료된다.

## 6. 이 결함들이 세 라운드를 통과한 공통 원인

[2026-08-31 §5](codebase-audit-2026-08-31.md)가 지목한 "검증됨과 실제로 실행됨 사이의 틈"이 이번에도 최대 수확이었다. 4·5라운드에서 관찰된 형태를 구체화한다.

**같은 값을 두 곳에서 다르게 판정한다.** HIGH-1이 정확히 이 형태다 — 제한을 거는 함수와 본문을 읽는 함수가 Content-Type을 다르게 정규화했다. MEDIUM-4도 같은 계열이다: 정렬 순서를 만드는 곳과 인덱스를 만드는 곳이 서로를 모른다.

**수정이 기존 가드를 도달 불가로 만든다.** AUD31-M11(numeric scale 보존)이 `scanDestination`의 목적지 타입을 `*pgtype.Text`로 바꾸면서 `*pgtype.Numeric` NaN 거부 분기를 죽은 코드로 만들었고(LOW-15), 그것을 지키던 테스트는 fixture 타입 불일치라는 **다른 이유로** 통과했다.

**테스트가 서버가 만들지 않는 응답을 mock한다.** `setup-page.test.tsx`가 서버가 절대 채우지 않는 `details` 모양을 수동 mock해 죽은 코드를 검증했고(LOW-13), `client.ts`의 mock 전용 분기는 페이지 테스트 8~9개가 출시되는 JSON 파서를 건너뛰게 했다(FR-7/10/16).

**가드가 스스로를 검사하지 않는다.** 문서 링크 무결성 가드가 출발 문서를 열지 않았고(FR-11), 센티널 비교가 자기 자신을 비교했으며(FR-19), checksum 테스트가 최신 마이그레이션을 빠뜨렸다(LOW-11).

**전수 커버리지 없이는 파일 자체가 누락된다.** `SECURITY.md`는 세 라운드 동안 어떤 에이전트의 범위에도 명시적으로 들어가지 않았다.

권장: 새 검증 로직을 추가할 때 **"이걸 삭제하면 어떤 테스트가 실패하는가"**를 묻고, 답이 없으면 그 테스트부터 쓴다. 이번 수정은 전부 이 기준으로 회귀 테스트를 동반했고, 몇 건은 수정을 되돌려 테스트가 실제로 실패하는지 확인(변이 검증)까지 거쳤다.

## 7. 수정 종결과 최종 검증

확정 37건 중 **34건을 수정**했다. 각 수정은 그것이 되돌려지면 실패하는 회귀 테스트를 동반한다.

수정 과정에서 **새 결함 3건을 자체 발견해 함께 고쳤다.**

1. **FR-8이 업그레이드 잠금을 만들 뻔했다.** `expand`를 `reservedNames`에 넣었는데, 그 맵은 `validateCatalogIntegrity`가 **모든 읽기(`EnsureCurrent`)마다 전 필드에** 적용한다. 즉 `expand` 필드를 가진 기존 DB는 업그레이드 순간 그 컬렉션만이 아니라 **데이터베이스 전체가 `ErrConflict`로 잠긴다.** 입력 정책(`reservedInputNames`)과 카탈로그 무결성(`reservedNames`)을 분리하고, 같은 실수를 막는 테스트를 넣었다.
2. **FR-9가 같은 위험을 반복했다.** `normalizeStoredDefault`가 정의만 되고 `validateCatalogIntegrity`에 연결되지 않아, 그 함수의 주석 자체가 "legacy oversized text default keeps failing every catalog read"라고 인정하고 있었다. 연결하고 라이브 회귀 테스트를 넣은 뒤, 연결을 되돌리면 정확히 그 오류로 실패하는 것을 확인했다.
3. **정렬 정합이 두 커널에서 갈렸다.** MEDIUM-4 수정이 `CompileList`에만 적용돼, 같은 `sort=-created_at`이 relation 필터 유무에 따라 동점 순서가 달라졌다. 정합 로직을 네 진입점이 공유하는 `effectiveSortKeys`로 올리고, 옛 동점 순서를 전제한 커서를 무효화하기 위해 `cursorVersion`을 4로 올렸다.

### 미수정 3건

| finding | 사유 |
| --- | --- |
| MEDIUM-3 | 시계 기반 커서를 시퀀스/워터마크 기반으로 바꾸는 설계 변경이 필요하다 |
| nullable 컬럼 DESC 인덱스 매칭 | NULL 가능 컬럼은 여전히 `NULLS LAST`라 단일 컬럼 인덱스를 역방향으로 못 탄다. 확대하려면 relation 커널의 `compileRelationOrder`와 `compileRelationKeyset`을 같은 커밋에서 함께 바꿔야 한다 |
| `PlanUpdate`의 legacy 기본값 거부 | `planner.go:337`이 재전송된 필드를 엄격 검증하므로, legacy 초과 기본값을 가진 컬렉션은 읽기는 되지만 저장은 여전히 거부된다 |

### 최종 검증 결과

라이브 PostgreSQL 16.13에 `PGBASE_TEST_ADMIN_URL`을 설정해 실행했다.

| Gate | 결과 |
| --- | --- |
| `gofmt -l .` / `go build ./...` / `go vet ./...` / `git diff --check` | clean |
| `go test -count=1 ./...` | 18/18 패키지 통과, **skip 0건** |
| `go test -race -count=1 ./...` | 18/18 패키지 통과 |
| Admin UI | typecheck, 12 파일 / 90 테스트, production build 통과, 커밋된 dist 동기화 |
| MEDIUM-4 실측 | `EXPLAIN`으로 Index Only Scan Backward 전환 확인 (1,933 → 5 버퍼) |
| CI (PR #10) | **16/16 통과** — PostgreSQL 16.13 / 17 / 18 매트릭스, e2e, security audit, backup/restore drill, frontend, 관측성 아티팩트 |
| CI (병합 후 `main`) | **5/5 통과** |

## 8. 이번 감사의 정직한 한계

- **탐색 단계는 라이브 PostgreSQL 없이 정적 정독으로만 수행했다.** 수정 검증에서야 라이브 DB를 붙였다. 그 전까지 다섯 라운드 전부 `go test`를 실행하지 않았다.
- **`EXPLAIN`을 실행한 것은 MEDIUM-4 하나뿐이다.** 다른 성능 관련 판정(LOW-21의 O(n²), relation 정렬 비용)은 상한 계산이지 실측이 아니다.
- **MEDIUM-3의 레이스는 재현하지 못했다.** 메커니즘은 코드로 확정했지만 실제 누락 창을 관측하려면 동시 트랜잭션을 겹쳐 실행해야 한다.
- **부하·용량·장시간 안정성은 측정하지 않았다.** 이전 두 감사와 같은 한계다.
- **탐색 중 도달하지 못한 범위:** `ui/admin/dist` 재빌드 대조(정황 판단만), `tests/e2e` 전수 정독, `docs/superpowers/` 8개 문서 전수, Windows·BSD 터미널 경로(해당 플랫폼에서 컴파일 불가).
- **검증 라운드는 각 finding을 검증자 1명이 판정했다.** 4라운드의 2렌즈 교차검증보다 얕은 구조다.
- **신뢰할 수 있는 것:** 코드 인용의 정확성(전 항목 file:line 직접 대조), 도달 경로의 정적 추적, HIGH-1·MEDIUM-4·LOW-9·LOW-20의 실행 재현, 그리고 최종 게이트가 skip 0으로 통과했다는 사실.
