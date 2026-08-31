# Operational Telemetry 구현 계획

기준 설계: [Operational Telemetry 설계](../specs/2026-08-31-operational-telemetry-design.md)

## 작업 1 — Private registry와 bounded event kernel

- Prometheus client를 고정 버전으로 추가한다.
- `internal/observability`에 앱별 registry, HTTP/pool collectors, semantic event normalization을 구현한다.
- global registry와 arbitrary string label을 금지하는 테스트를 추가한다.

완료 증거: scoped unit test, race test, sentinel secret/cardinality test.

## 작업 2 — 안전한 opt-in 설정과 exposition

- `Options.MetricsToken`과 `PGBASE_METRICS_TOKEN`을 추가한다.
- 32–256 bytes와 bearer-token-safe 문자 계약을 검증하고 복사 소유권을 고정한다.
- 설정 시에만 `GET /metrics`를 등록하고 exact bearer 인증과 no-store를 적용한다.

완료 증거: default 404, malformed/missing/duplicate/wrong/correct token API test.

## 작업 3 — HTTP와 PostgreSQL pool 계측

- registered route template을 request 시작 전에 해석해 count/duration/inflight를 기록한다.
- raw URL과 query를 label에서 배제한다.
- pgx pool snapshot을 scrape-time collector로 노출한다.

완료 증거: dynamic record route sentinel test, pool live test, concurrent scrape/race test.

## 작업 4 — 도메인 operation 계측

- query retry와 exhaustion
- migration up/status
- schema create/update/delete apply
- listener connect/reload/readiness
- application readiness component
- administrator setup/login outcome
- relation schema/write/delete/expand outcome와 budget

기존 생성자는 호환 wrapper로 유지하고 App 조립만 recorder-aware constructor를 사용한다.

완료 증거: 각 bounded outcome의 unit/live PostgreSQL 회귀 테스트.

## 작업 5 — 운영 산출물과 black-box 검증

- 배포 설정, secret rotation, scrape 예시, label 예산을 문서화한다.
- Grafana dashboard와 Prometheus alert rules를 추가한다.
- Docker Compose E2E에서 인증 실패/성공 scrape와 핵심 series를 검증한다.
- disabled/enabled instrumentation benchmark를 기록한다.

완료 증거: 전체 Go/race/vet/E2E, rule syntax, docs link 검사.

로컬 기준선(Apple M5, Go 1.26.7, 2026-08-31)은 disabled HTTP 경로가 약 113–122 ns/op, 16 B/op, 1 alloc/op이고 enabled 경로가 약 401–415 ns/op, 56 B/op, 3 allocs/op였다. 이 값은 처리량 보장이 아니라 회귀 비교용이며, Backend CI가 query 및 observability benchmark 결과를 PostgreSQL 버전별 artifact로 보존한다. Prometheus rules는 고정 digest의 공식 `promtool`로, Grafana dashboard는 `jq`로 CI에서 구문 검증한다.

## 다음 묶음

Telemetry 완료 후 별도 설계에서 principal-aware Record Operations transaction kernel을 구현한다. 첫 vertical slice는 `Idempotency-Key`, optimistic concurrency, durable record audit, transactional outbox의 원자성 계약이다. public auth와 relation/realtime 경로가 이 seam을 우회하지 못하게 한 뒤 확장한다.
