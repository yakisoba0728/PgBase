# Operational Telemetry 설계

상태: 2026-08-31 구현 기준

## 1. 목적

기존 Production Kernel은 한 프로젝트의 관리자용 데이터 코어를 제공하지만, 요청 지연·오류, PostgreSQL pool 포화, transaction retry, schema listener, readiness, migration, 인증 실패를 수치로 판정할 수 없었다. 이후 relation, public auth/rules, outbox/realtime, file 기능을 추가하기 전에 낮은 cardinality의 운영 관측성과 반복 가능한 SLO 증거 경계를 먼저 만든다.

PocketBase의 collection 중심 제품 표면은 참고하되, 단일 서버 전제의 broker·scheduler 구조는 복제형 pgBase에 복사하지 않는다. 근거와 후속 순서는 [PocketBase 참고 조사](../../pocketbase-reference-research-2026-08-31.md)를 기준으로 한다.

## 2. 불변식

- `PGBASE_METRICS_TOKEN`이 비어 있으면 계측과 `GET /metrics` route가 모두 없다.
- token이 있으면 32–256 bytes의 bearer-token-safe 문자만 허용하고, scrape는 constant-time 인증을 통과해야 한다.
- metric registry는 `App`마다 독립적이며 Prometheus global registry를 변경하지 않는다.
- HTTP label은 등록된 route template, bounded method, 1xx–5xx/unknown status class만 사용한다. raw path와 query는 사용하지 않으며 exact status는 구조화 log에 남긴다.
- email, IP, administrator/collection/record ID, cookie, token, DSN, SQL, SQLSTATE 원문, 오류 문자열은 label이나 metric value에 넣지 않는다.
- operation, outcome, retry cause, readiness component는 닫힌 enum에서만 생성한다. 잘못된 event는 고정 사유의 dropped counter로 모은다.
- telemetry 실패는 제품 요청 결과나 readiness를 바꾸지 않는다. 잘못된 시작 설정만 fail-fast한다.
- PostgreSQL pool은 scrape 시 snapshot만 읽으며 telemetry를 위한 DB query나 background goroutine을 만들지 않는다.

Prometheus 공식 지침처럼 단위와 type suffix를 metric 이름에 포함하고, 사용자 ID나 이메일 같은 무제한 label을 금지한다. Histogram은 replica 사이에서 합산 가능한 request latency 근거로 사용한다.

## 3. 모듈 경계

`internal/observability`가 private Prometheus registry, metric 정의, label 정규화, HTTP status capture, pool collector, scrape 인증을 숨기는 deep module이다. 도메인 호출부에는 bounded semantic `Event`를 받는 단일 `Recorder` port만 보인다.

```go
type Recorder interface {
    Record(context.Context, Event)
}

func OperationEvent(Component, Operation, Outcome, time.Duration) Event
func TransactionRetryEvent(RetryCause, bool) Event
func ReadinessEvent(ReadinessComponent, bool) Event
```

HTTP와 exposition은 metric 구현 자체가 소유한다.

```go
func (m *Metrics) WrapHTTP(*http.ServeMux, http.Handler) http.Handler
func (m *Metrics) Handler(token []byte) http.Handler
```

OpenTelemetry provider, OTLP exporter, remote collector factory는 이번 public interface에 포함하지 않는다. 실제 두 번째 exporter adapter나 distributed trace 요구가 생길 때 현재 semantic event seam 뒤에 추가한다.

## 4. 측정 범위

- HTTP: request count, duration histogram, inflight
- PostgreSQL pool: current/max connections, acquire/cancel/wait/create/destroy 누적값
- record transaction: cached-plan invalidation, serialization failure, deadlock retry와 최종 소진
- system migration: up/status outcome과 duration
- collection schema: create/update/delete apply outcome과 duration
- schema listener: connect/reload outcome과 readiness transition
- readiness: HTTP/database/migrations/schema-listener의 최근 bounded 상태와 transition
- administrator auth: setup/login의 success, validation, invalid credentials, rate limit, setup unavailable, dependency unavailable, cancellation, failure
- relation: schema apply, relation field write, relation-aware delete, expand의 success/validation/conflict/budget/unavailable/cancellation/failure

Metric endpoint 자체도 고정 route이므로 cardinality를 늘리지 않는다. 세부 장애 식별은 기존 request ID가 있는 구조화 log에서 수행한다.

## 5. 의존성과 수명주기

- in-process: registry, counters, histograms, enum normalization, bearer verifier
- local-substitutable: PostgreSQL과 pgx pool; 기존 격리 DB integration test를 사용한다.
- true external: Prometheus-compatible scraper. pgBase는 pull endpoint만 제공하며 scraper 장애를 애플리케이션 장애로 취급하지 않는다.

`New`는 registry를 만들지만 network listener나 goroutine을 추가하지 않는다. 기존 application listener가 opt-in route를 제공하므로 `Start`, `Run`, `Close` 계약은 바뀌지 않는다. 여러 instance의 series는 scraper에서 합산한다.

## 6. 보안과 cardinality 예산

Bearer header는 정확히 하나만 허용한다. missing, duplicate, malformed, wrong credential은 exporter를 호출하지 않고 거부한다. 응답에는 `Cache-Control: no-store`를 설정한다.

새 label을 추가하는 변경은 다음을 함께 제시해야 한다.

1. 값의 닫힌 집합 또는 강제 상한
2. 왜 기존 log로 충분하지 않은지
3. 예상 최대 series 수
4. sentinel secret/PII 부재 회귀 테스트

collection 이름, tenant/project 이름, route raw path를 metric label로 추가하는 변경은 허용하지 않는다.

### 초기 series 예산

| 영역 | 강제 상한 | 인스턴스당 최대 series |
| --- | --- | ---: |
| HTTP | 등록 route 32개 이하, method-agnostic route 3개 이하, unmatched 1개, method 10개, status class 6개. Method-bound route는 GET/HEAD를 고려해 2개 method까지 계산하므로 route-method 상한은 `(29 × 2) + (3 × 10) + (unmatched 1 × 10) = 98`이다. HTTP histogram은 finite bucket 13개이므로 label tuple당 bucket 14개(+Inf 포함)+sum+count다. | `(route-method 98 × status 6 × (counter 1 + histogram 16)) + inflight 98 = 10,094` |
| Application operation | 유효한 component/operation 조합 13개 × outcome 10개. operation histogram은 finite bucket 16개이므로 tuple당 bucket 17개(+Inf 포함)+sum+count다. | `130 × (counter 1 + histogram 19) = 2,600` |
| Retry/readiness/self/pool | retry 3 causes × exhausted 2, readiness gauge 4, transition 4 × 2, dropped 1, pool 13 | `32` |
| 합계 | 외부 scraper가 붙이는 job/instance/cluster label 조합은 별도 | `12,726 이하` |

이 값은 가능한 label 조합의 보수적 ceiling이며 일반 트래픽에서 모두 생성되는 목표가 아니다. `internal/repositorypolicy`가 direct mux route 32개와 method-agnostic route 3개 ceiling을 검사하고, observability unit test가 status class와 enum을 닫힌 집합으로 고정한다. route나 bucket을 늘릴 때는 표, dashboard 비용, sentinel 부재 테스트를 같은 변경에서 갱신한다.

## 7. 완료 조건

- opt-in/off, bearer 인증, route-template, panic/implicit status, pool snapshot, retry/outcome, readiness/auth event 테스트
- sentinel email, IP, UUID, token, query, DSN이 exposition에 없는 회귀 테스트
- `go test -race ./...`, `go vet ./...`, Docker E2E scrape 검증
- dashboard, 초기 alert rules, 배포 및 cardinality 문서
- disabled hot path와 enabled HTTP instrumentation benchmark 기록

## 8. 비범위와 후속 순서

이번 telemetry slice 자체는 trace, log shipping, remote-write, public auth, realtime delivery, object storage, control plane을 구현하지 않는다. 이후 구현된 relation slice는 같은 bounded event seam에 `relation_schema`, `relation_write`, `relation_delete`, `relation_expand`만 추가했으며 collection/field/relation/record 식별자는 계속 금지한다. 다음 제품 단계는 public auth/rules, realtime, file, control plane 순으로 이어간다.
