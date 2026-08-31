# Record Operations 구현 계획

기준 설계: [Record Operations 설계](../specs/2026-08-31-record-operations-design.md)

## 작업 1 — schema-only system migration

- 기존 migration 1–3을 수정하지 않고 `000004_record_operations.sql`을 embed한다.
- revision, committed audit, outbox, completed idempotency result와 필요한 bounded index/append-only trigger를 만든다.
- populated v3 upgrade, 반복 실행, collection delete 보존, 1,597-field 무변경을 검증한다.

완료 증거: migration live tests와 checksum/column-count 회귀 테스트.

## 작업 2 — principal-aware transaction kernel

- `internal/recordops`에 principal, request/result, compatibility, stable error type을 정의한다.
- 기존 query compiler/codec/cursor를 재사용하되 transaction orchestration은 recordops가 소유한다.
- catalog pinning, deterministic schema/input digest, legacy/new ETag, physical-row-first lock order를 구현한다.
- 현재 bounded transaction retry에서 logical IDs가 안정적이고 attempt 결과가 commit 후에만 노출되게 한다.

완료 증거: unit/fuzz/live tests, rollback failure injection, race test.

## 작업 3 — create idempotency와 committed artifacts

- bounded `Idempotency-Key`, raw-key hashing, advisory-lock 기반 multi-instance claim/replay를 구현한다.
- create/update/delete의 revision, audit, outbox를 app mutation과 같은 transaction에 기록한다.
- 같은 key/same request replay, same key/different request conflict, in-flight conflict, expiry 경계를 검증한다.
- 각 인스턴스에서 실행 가능한 24시간 retention cleaner를 transaction당 256-row `SKIP LOCKED` 소배치로 연결하고, full batch는 짧은 yield 뒤 연속 drain한다. cleanup 실패는 log 후 다음 주기에 재시도하고 readiness를 바꾸지 않는다.

완료 증거: 두 pool 동시성 test에서 record/revision/audit/outbox/result가 각각 정확히 한 logical set.

## 작업 4 — HTTP precondition과 compatibility contract

- handler가 authenticated administrator UUID와 request ID만 principal/meta로 전달한다.
- bounded strong `If-Match` set, ETag/Location/replay/compatibility response header와 stable 400/409/412 오류를 추가한다.
- v1 missing-header 경로는 기존 성공 status/body를 유지하면서 compatibility 경로임을 명시한다.
- compatibility mode는 `PgBase-Compatibility`로만 표시한다. RFC 9745 `Deprecation`은 API resource 자체를 날짜와 정책 링크로 폐기할 때만 사용한다.
- CORS allow/expose와 보안 sentinel tests를 갱신한다.

완료 증거: handler/live security tests와 concurrent stale-write black-box test.

## 작업 5 — 번들 UI의 안전한 mutation

- API client가 response body와 ETag metadata를 보존할 수 있게 한다.
- create draft는 stable random idempotency key를, update/delete는 load한 ETag를 전달한다.
- 412에서 overwrite하지 않고 reload UX를 제공한다.

완료 증거: key 재사용/교체, ETag 전달, stale response UI tests와 typecheck/build.

## 작업 6 — 운영 검증과 독립 리뷰

- migration/transaction/concurrency/security/compatibility를 서로 다른 리뷰 관점에서 재검증한다.
- outbox writer의 원자성과 delivery 비범위를 deployment/handoff/tradeoff 문서에 반영한다.
- full Go/race/vet/build, Docker PostgreSQL E2E, UI, diff/artifact 검사를 실행한다.

완료 증거: 독립 finding 수정 내역과 반복 가능한 명령 결과.

## 다음 묶음

Record Operations가 안정화되면 relation/schema lifecycle을 먼저 설계한다. 그 뒤 public auth/rules가 동일 principal/mutation seam을 사용하게 하고, outbox delivery lease/fencing과 policy-aware realtime projection을 추가한다.
