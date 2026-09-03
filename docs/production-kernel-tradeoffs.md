# pgBase Production Kernel 트레이드오프 기준

이 문서는 `main`의 구현 결정을 바꿀 때 확인해야 하는 이유, 비용, 재검토 조건을 기록한다. 현재 동작과 인수인계는 [최종 핸드오프](production-kernel-handoff.md), 기준 구현 감사는 [2026-08-28 보고서](codebase-audit-2026-08-28.md), 최신 전수 재검증 결과는 [2026-08-31 보고서](codebase-audit-2026-08-31.md)를 기준으로 한다.

## 1. 제품과 데이터 경계

| 결정 | 얻는 것 | 비용과 제한 | 재검토 조건 |
| --- | --- | --- | --- |
| 프로젝트 하나가 PostgreSQL DB 하나를 소유 | 장애·백업·비밀·용량 경계가 명확하고 한 DB 안의 트랜잭션을 그대로 사용 | 여러 프로젝트를 한 프로세스에서 관리하지 못함 | control plane과 프로젝트별 독립 pool/lifecycle을 설계할 때 |
| PostgreSQL을 영속 상태와 동시성 조정의 단일 기준으로 사용 | 다중 인스턴스에서 별도 consensus 계층 없이 일관된 상태 공유 | DB 장애가 data plane 전체의 readiness에 직접 영향 | read replica, 지역 분산 또는 DB별 장애 격리가 필요할 때 |
| Production Kernel은 관리자용 data plane에 집중 | 인증·스키마·CRUD·relation·운영 경계를 작게 유지해 검증 가능 | 공개 사용자 인증/record rule, 파일, realtime, SDK가 없음 | 공개 애플리케이션 백엔드 계약을 별도 spec으로 승인할 때 |

## 2. 스키마와 쿼리

| 결정 | 얻는 것 | 비용과 제한 | 재검토 조건 |
| --- | --- | --- | --- |
| 스키마 변경을 `plan`과 `apply`로 분리 | SQL과 checksum을 검토하고 stale plan을 거부 | 단순 CRUD형 도구보다 한 단계 더 필요 | 자동 migration UX를 추가하더라도 checksum·기준 버전 불변식은 유지 |
| advisory lock과 단일 트랜잭션으로 DDL 적용 | 중복 적용과 부분 적용 방지 | DDL lock 대기와 maintenance window 가능 | online DDL을 별도 실행 모델로 설계할 때 |
| 카탈로그 버전을 영속 기준으로, `LISTEN/NOTIFY`를 힌트로 사용 | 알림 유실 후에도 재동기화 가능 | listener 장애 시 readiness를 내려 가용성보다 일관성을 선택 | eventual consistency를 명시적으로 허용할 API가 생길 때 |
| 식별자는 카탈로그에서 생성하고 값은 parameter binding | SQL 주입면 축소 | 임의 SQL은 제공하지 않음. filter 언어는 2026-08-29부터 제한 문법만 지원 | 표현식 비용 예산과 인덱스 전략을 확장할 때 |
| single relation은 `app` inline UUID, multiple relation은 `pgbase_relation` ordered edge table | single read 비용을 낮추고 multi order/cardinality를 DB constraint로 보존 | 세 번째 owned schema와 field별 edge/trigger object가 생기며 single/multiple 자동 전환을 지원하지 않음 | 실제 migration 요구와 lock/용량 근거로 online conversion을 별도 설계할 때 |
| target FK는 deferred `NO ACTION`, logical `restrict`/`cascade`/`set_null`은 Record Operations가 실행 | cascade/set-null affected record마다 revision/ETag/audit/outbox를 원자적으로 보존하고 required cycle도 정상 처리 | delete가 depth 32, record 10,000, edge 100,000 budget과 coarse collection lock을 사용해 큰 graph/경합을 거부할 수 있음 | load/lock evidence로 별도 bulk job 또는 더 세밀한 lock strategy를 설계할 때 |
| relation expand/filter/sort를 typed catalog plan과 hard budget으로 제한 | raw SQL 없이 forward/nested/reverse traversal과 cursor-safe sort 제공 | expand 4 KiB/depth 6/node 20/row 10,000/reverse 1,000/query-step 64/response 4 MiB, filter depth 3/join 8, sort depth 2/root 100,000; multi sort와 arbitrary join은 거부 | 운영 query 분포와 인덱스/비용 증거를 갖춘 새 query shape를 승인할 때 |
| 정렬 키 기반 cursor와 15분 만료·컬렉션 바인딩 | 안정적 순서, bounded token 재사용. root의 `id`·`created_at`·`indexed`·`unique` 필드는 index-ordered page가 가능 | 장기 bookmark 미지원. 관계 정렬은 policy-aware `NULLS LAST` join 때문에 root-wide sort가 필요할 수 있어 100,000-row guard를 적용한다. 큰 복합 키는 signed record anchor와 원래 정렬값 hash로 축약되므로 anchor 행 변경·삭제 시 cursor가 명시적으로 무효화됨 | 대형 관계 정렬용 projection index 또는 별도 cursor 상태 저장소가 필요할 때 |
| timestamp 기본값은 RFC3339의 서기 1년 이상만 허용 | PostgreSQL textual `timestamptz` 적용 실패를 plan 단계의 400으로 전환 | BC 기본값 미지원 | BC timestamp 제품 요구가 생기면 별도 문법과 round-trip 테스트 필요 |
| 생성·수정 시각은 PostgreSQL `statement_timestamp()` 사용 | 인스턴스 시계 차이로 인한 순서 역행 방지 | 애플리케이션에서 시간을 주입하는 테스트/기능이 제한 | 사용자 지정 event time은 system audit time과 별도 필드로 추가 |
| JSON과 numeric을 lossless하게 처리하고 UTF-8을 엄격 검증 | 큰 정수·극단 지수·Unicode의 조용한 손상 방지 | 일반 JSON decoder보다 구현과 UI 의존성이 복잡 | 공개 SDK도 동일한 정밀도 계약을 구현할 때 |
| 동적 table 대신 `pgbase_meta` sidecar에 record revision 저장 | 1,597개 사용자 필드 + 시스템 열 3개의 PostgreSQL 1,600열 상한을 깨지 않고 optimistic concurrency 제공 | 지원 writer를 우회한 직접 SQL은 revision/audit/outbox와 relation delete/lock invariants를 갱신하지 않음 | 별도 CDC/direct-SQL capture 계약을 승인할 때 |
| `/api/v1` missing guard header는 compatibility로 명시하고 UI는 즉시 strict header 사용 | 기존 pre-v0.1 client body/status를 보존하면서 안전한 경로를 기본 UX로 만듦 | 외부 legacy client는 여전히 non-idempotent create/last-write-wins를 선택할 수 있음 | versioned public API에서 key/If-Match를 400/428로 강제할 때 |
| 성공 mutation과 revision/audit/outbox/idempotency 결과를 한 transaction에서 기록 | 응답 유실 replay, stale-write 방지와 향후 durable delivery 원본을 하나의 commit으로 보장 | 매 write의 metadata/JSONB 비용과 지속 증가하는 outbox | delivery retention, partitioning과 archival 용량 근거가 생길 때 |
| audit에는 actor/action/resource/hash만, outbox에는 versioned delta만 저장 | record 값의 장기 audit 복제와 대형 full-snapshot write amplification 방지 | audit만으로 과거 record 전체 복원 불가, future policy projection은 새 payload version 필요 | 규제형 full history 또는 public-rule snapshot 요구가 승인될 때 |

## 3. 인증과 보안 경계

| 결정 | 얻는 것 | 비용과 제한 | 재검토 조건 |
| --- | --- | --- | --- |
| 관리자 세션 원문은 쿠키에만 두고 DB에는 HMAC hash 저장 | DB 유출 시 즉시 사용 가능한 세션 토큰 노출 방지 | token secret 교체 시 세션·CSRF·cursor·rate-limit namespace가 함께 무효화 | 키 버전과 단계적 rotation이 필요할 때 |
| same-origin, CSRF, `HttpOnly`, `SameSite=Lax`를 함께 적용 | 브라우저 mutation의 교차 origin 공격 차단 | reverse proxy와 `PGBASE_PUBLIC_URL` 설정 정확성이 필요 | cross-origin admin client가 필요하면 cookie auth와 분리된 API credential 설계 |
| 로그인 제한을 계정·네트워크 축으로 저장하고 Argon2 동시성을 제한 | 이메일 회전 우회와 비인증 자원 고갈 완화. 모든 관리자를 함께 잠그는 전역 failure bucket은 두지 않음 | DB write와 정상 burst 처리량 감소 | 실제 트래픽으로 threshold와 KDF worker 수를 용량 검증할 때 |
| trusted proxy 목록의 실제 peer만 forwarded 값을 신뢰 | spoofed scheme/IP 차단 | proxy topology 변경 시 설정 동기화 필요 | 다단 proxy 체인의 명시적 trust model을 도입할 때 |
| DSN redaction은 URL/keyword 파라미터를 구조적으로 찾고 키를 최대 세 번 decode | 단일·이중 인코딩 키와 공백/escape 포함 값의 시작 로그 유출 차단 | 모든 미래의 비표준 secret 키 이름을 자동 추론하지는 못함 | 새 credential 설정 키를 추가할 때 redaction corpus도 함께 확장 |

로그 마스킹은 보조 방어다. 호출부는 원문 credential을 구조화 로그 필드에 넣지 않는 불변식을 유지한다. 비민감 query 진단은 남겨 시작 실패를 운영자가 분석할 수 있게 했다.

## 4. 수명주기와 가용성

| 결정 | 얻는 것 | 비용과 제한 | 재검토 조건 |
| --- | --- | --- | --- |
| caller-owned HTTP transport는 `Start` 후 `Handler` 사용 | worker, listener, readiness 수명주기가 명시적 | `Handler()`만 호출하는 embedding은 준비되지 않은 상태 | API 변경 시 이 순서를 compile-time으로 표현할 수 있을 때 |
| readiness는 DB, migration, listener 동기화를 모두 요구 | 일관성을 보장하지 못하는 인스턴스에 트래픽 차단 | DB/listener의 짧은 장애도 instance 제거로 이어짐 | degraded read-only mode를 별도 상태와 API로 설계할 때 |
| 요청 header/body/write/idle 제한과 JSON/응답 예산 적용 | slow client와 메모리 증폭 제한 | 대용량 import/export를 일반 API로 처리할 수 없음 | streaming 또는 비동기 bulk job을 별도 endpoint로 제공할 때 |
| graceful shutdown에서 readiness를 먼저 내리고 drain | rollout 중 새 요청 차단과 진행 중 요청 보호 | orchestrator grace가 앱 timeout보다 길어야 함 | 장시간 job을 별도 worker/lease 모델로 분리할 때 |
| metrics token이 있을 때만 앱별 private Prometheus registry와 `/metrics`를 생성 | global registry 충돌 없이 embed/replica마다 격리되고 미사용 경로 비용이 없음 | scraper credential과 앱을 함께 회전해야 하며 Go/process collector와 trace는 아직 없음 | 실제 두 번째 exporter, runtime/build signal 또는 distributed trace 요구가 생길 때 |
| 도메인은 닫힌 semantic event만 내고 observability 모듈이 label을 소유 | 이메일·ID·경로·SQL 같은 무제한/민감 label 유입을 구조적으로 차단 | 새 component/operation/outcome을 추가할 때 enum·series 예산·회귀 테스트도 변경해야 함 | log만으로 원인 분석이 불가능한 새 bounded dimension이 입증될 때 |
| HTTP와 장기 operation에 서로 다른 고정 histogram bucket 사용 | replica 합산 가능한 지연 분포와 30초 HTTP/10분 operation tail 관측 | bucket 수만큼 series와 scrape 크기가 증가 | staging 분포로 native histogram 또는 더 좁은 bucket 계약을 검증할 때 |

## 5. 배포와 공급망

| 결정 | 얻는 것 | 비용과 제한 | 재검토 조건 |
| --- | --- | --- | --- |
| distroless non-root runtime | shell·패키지 관리자·권한 공격면 축소 | 운영 중 컨테이너 내부 디버깅이 어려움 | 별도 debug image를 만들되 production image는 유지 |
| base/runtime/service/BuildKit 이미지를 digest로 고정 | 같은 입력의 재현성과 tag 변조 방어 | 보안 업데이트가 자동 반영되지 않음 | Dependabot 제안 검토와 정기 rebuild를 운영 gate로 유지 |
| 릴리스 tag 원문을 `VERSION` build arg로 전달 | `v1.2.3` 같은 배포 식별자가 `/api/admin/system`에 그대로 노출 | 로컬 build는 명시하지 않으면 `dev` | semver 정규화가 필요하면 표시 버전과 artifact tag 계약을 함께 변경 |
| moving channel을 채널별로 직렬화하고 OCI version label의 단조 증가를 요구 | 늦게 끝난 과거 workflow가 `latest`/`alpha`/`beta`/`rc`를 역행시키지 못함 | label 없는 기존 channel은 immutable exact tag로 현재 버전을 한 번 bootstrap해야 하며, 없으면 승격이 멈춤 | registry가 조건부 tag 갱신이나 원자적 compare-and-swap을 제공할 때 외부 writer까지 포함한 fence로 확장 |
| release workflow가 backend·frontend·E2E·security reusable workflow를 모두 선행 실행 | DB 없는 이미지 build에서 조용히 skip되는 테스트 대신 실제 PostgreSQL과 브라우저 검증이 publish를 차단 | tag release 시간이 늘고 검증 job이 일반 push와 중복 실행될 수 있음 | 동일 커밋의 검증 결과를 안전하게 재사용하는 immutable promotion 절차가 생길 때 |
| release builder는 고정 digest의 `docker-container` driver 사용 | GitHub runner 기본 BuildKit 변화에 덜 의존 | digest 갱신과 builder bootstrap 시간이 필요 | 새 digest를 독립 inspect/bootstrap하고 provenance·SBOM 회귀를 통과할 때 |
| system migration은 명시적 job, 개발 Compose만 auto-migrate | 다중 인스턴스 rollout에서 migration owner가 명확 | 배포 단계가 하나 늘고 무중단을 자동 보장하지 않음 | migration compatibility matrix와 expand/contract 절차가 구현될 때 |

`.github/workflows/release-image.yml`은 외부 registry를 변경하는 경로다. 로컬 검증은 pinned builder bootstrap, workflow 정책 테스트, Docker E2E까지 수행하며 실제 tag push·GHCR publish·attestation 발행은 릴리스 승인 후에만 수행한다.

## 6. 테스트 전략

| 결정 | 얻는 것 | 비용과 제한 | 재검토 조건 |
| --- | --- | --- | --- |
| 단위·live PostgreSQL·Docker black-box·Playwright를 계층화 | parser부터 실제 브라우저와 컨테이너까지 서로 다른 실패를 포착 | CI 시간과 Docker/Chromium 의존성 증가 | 빠른 PR lane을 만들더라도 merge/release gate의 전체 계층은 유지 |
| Docker E2E를 `-count=2 -shuffle=on`으로 실행 | 공유 상태·순서 의존·정리 누락 탐지 | 이미지 build와 서비스 재기동 시간이 증가 | test isolation이 더 강해져도 정기 반복 실행은 유지 |
| 정책을 코드 테스트로 고정 | mutable builder나 version 누락의 재발 방지 | workflow 포맷 변경 시 parser 테스트도 수정 필요 | YAML AST 기반 검사로 교체할 수 있으나 동일 불변식을 검사 |

## 7. 의도적으로 남긴 위험

다음은 현재 결함으로 숨기지 않고 제품 계약이 필요한 후속 작업으로 남겼다.

- `/api/v1`은 header가 없는 기존 client를 명시적 compatibility 경로로 허용한다. `Idempotency-Key`/`If-Match`를 쓰는 client와 번들 UI만 중복 생성·stale write 보호를 받는다.
- transactional outbox는 writer와 원자성까지만 구현됐다. delivery worker, lease/fencing, retry/dead-letter, retention, policy-aware projection과 SSE가 아직 없다.
- 공개 사용자 인증, RBAC/record rule, API token/service account가 없다.
- single/multiple relation과 bounded expand/filter/sort는 있지만 cross-project relation, edge attribute, arbitrary join, multi relation sort와 범용 query 언어는 없다.
- 현재 panic recovery는 일반 JSON/UI 응답을 원자적으로 만들기 위해 body를 버퍼링한다. SSE/realtime를 추가할 때는 기존 일반 route의 500 계약을 유지하면서 `Flusher`와 disconnect를 보존하는 streaming 전용 middleware 경계를 먼저 설계해야 한다.
- online DDL, zero-downtime upgrade, read replica, sharding 보장이 없다.
- 실제 트래픽의 load/soak/partition 시험과 SLO, 운영 restore drill의 RPO/RTO 확정이 남아 있다.
- 실제 tag release와 GHCR publish/attestation은 로컬 검증 범위 밖이다.

이 항목을 구현할 때는 기존 불변식을 암묵적으로 완화하지 말고 별도 spec, migration/호환성 계획, 실패 모드 테스트를 함께 추가한다.
