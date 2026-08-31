# Record Operations 설계

상태: 2026-08-31 구현 기준

## 1. 목적

레코드 생성 응답이 유실됐을 때 중복 생성을 막고, 여러 인스턴스의 수정·삭제가 stale write를 허용하지 않으며, 실제 레코드 변경·감사 증거·향후 realtime 원본이 하나의 PostgreSQL commit으로만 보이게 한다. 이 경계는 이후 relation, public auth/rules, realtime delivery가 우회할 수 없는 principal-aware Record Operations transaction kernel이다.

현재 동적 컬렉션은 사용자 필드 1,597개와 시스템 열 `id`, `created_at`, `updated_at`으로 PostgreSQL의 1,600열 한계를 모두 사용할 수 있다. 따라서 revision 열이나 trigger를 `app.*` 테이블에 추가하지 않는다. 기존 system migration 1–3도 checksum 계약 때문에 수정하지 않는다.

## 2. 설계 비교와 선택

독립적으로 세 설계를 검토했다.

| 안 | 강점 | 비용 | 판정 |
| --- | --- | --- | --- |
| A: transaction kernel + lazy representation hash | 기존 record의 실제 응답 표현으로 strong validator를 만들고 호환 rollout이 단순하다. | legacy GET에서 이미 만든 응답 bytes를 hash해야 한다. | 기반안 |
| B: cutover epoch + explicit state machine | migration 이후 누락 state를 corruption으로 구분하기 쉽다. | pending/completed 제약, tombstone, epoch 상태가 첫 slice에 비해 복잡하다. | doctor 후속에 일부 채택 |
| C: minimal audit + standard If-Match set | 개인정보/값을 audit에서 배제하고 bounded tag list와 `*` 의미를 보존한다. | `updated_at`만으로 legacy validator를 만들면 표현 검출력이 약하다. | HTTP/audit 부분 채택 |

최종안은 A를 기반으로 C의 bounded HTTP precondition과 최소 audit를 결합한다. 동적 SQL compiler/codec은 `internal/query`에 남기고, `internal/recordops`가 principal 검증, catalog pinning, transaction/retry, idempotency, ETag, audit, outbox를 하나의 깊은 module로 소유한다. audit/outbox writer는 독립 public port가 아니며 반드시 같은 `pgx.Tx`만 받는다.

## 3. 공개 seam과 불변식

Record Operations는 인증 middleware가 만든 principal만 받는다. request body/header로 actor를 지정할 수 없고 raw session, email, idempotency key는 영속 audit나 log에 남기지 않는다.

```go
type Principal struct {
    Kind PrincipalKind
    ID   uuid.UUID
}

type EntityTagSet struct {
    Present bool
    Any     bool
    Values  []string
}

type Record struct {
    Data    map[string]any
    ETag    string
    Version int64
}

type MutationResult struct {
    Record        *Record
    EventID       uuid.UUID
    Replayed      bool
    Compatibility Compatibility
}
```

외부 호출은 list/get/create/update/delete의 명시적 request type을 사용한다. 모든 mutation은 다음을 보장한다.

- app record, revision, audit, outbox, idempotency result는 전부 commit되거나 전부 rollback된다.
- retry attempt에서 생성된 결과는 commit 전에 호출자에게 노출하지 않는다.
- principal/collection/record/request의 무제한 값은 metric label로 만들지 않는다.
- 직접 SQL로 `app.*`를 변경하는 것은 지원되는 writer 경로가 아니다.

## 4. System migration 4

`000004_record_operations.sql`은 `pgbase_meta`에만 객체를 만들고 기존 `app.*`를 ALTER하거나 scan하지 않는다.

### record revisions

`record_revisions`는 `(collection_id, record_id)`를 primary key로 하며 collection 삭제 시 cascade한다. `version >= 1`, 32-byte validator, 마지막 event ID, DB timestamp를 보존한다. 신규 create는 version 1이다. 기존 row는 sidecar 없이 논리적 legacy version 0으로 읽고 첫 성공 mutation에서 sidecar로 승격한다.

### committed audit

`record_audit_events`는 UUID event ID와 identity sequence, actor kind/ID, request ID, collection ID/name snapshot, record ID, operation, schema version/digest, 이전/새 record version과 ETag, 정렬된 changed field 이름, input hash, idempotency-key hash를 저장한다.

- collection/admin FK를 두지 않아 삭제 후에도 이력을 보존한다.
- record 값, email, session, raw key는 저장하지 않는다.
- 성공적으로 commit된 mutation만 기록한다. 거부/실패는 구조화 log와 metric의 영역이다.
- UPDATE/DELETE/TRUNCATE 거부 trigger로 애플리케이션 실수에 의한 변경을 막는다. operation별 version/ETag shape와 revision/outbox/idempotency의 audit-event 일치도 DB 제약으로 검증한다. DB owner에 대한 규제형 WORM은 별도 운영 통제다.

Identity sequence는 rollback gap과 commit 순서 역전이 가능하므로 global resume cursor나 commit order로 약속하지 않는다.

### transactional outbox

`record_outbox`는 event ID를 audit event에 연결하고 `record.mutation.v1` topic, resource/version/operation과 versioned JSON mutation envelope를 저장한다. create/update는 DB round-trip 뒤 정규화된 변경 값을, delete는 tombstone metadata를 담는다. 전체 record snapshot을 매 mutation마다 복제하지 않아 최대 필드/대형 record의 write amplification을 제한한다.

이번 slice는 writer까지만 제공한다. delivery state, consumer lease/fencing, retry/dead-letter, NOTIFY, SSE projection은 후속 설계다. outbox row가 보인다면 record/revision/audit도 commit됐다는 의미만 보장한다.

### create idempotency result

`record_idempotency`는 `(actor_kind, actor_id, collection_id, key_hash)` 범위로 completed create 결과만 저장한다. request hash, record/event ID, 원래 201 response JSON과 ETag, DB 기준 created/expiry 시간을 가진다.

- raw key 대신 domain-separated SHA-256을 저장한다.
- 기본 replay window는 24시간이다.
- 만료 row는 동일 key 처리 중 lock 아래 제거할 수 있다. 각 인스턴스의 readiness-neutral cleaner도 시작 시와 5분마다 `FOR UPDATE SKIP LOCKED` transaction당 최대 256개를 지운다. full batch 뒤에는 짧게 양보하고 다음 batch를 이어서 비워 leader 없이 backlog가 유입률보다 느리게 정리되는 문제를 막는다.
- validation/conflict/precondition/dependency 실패는 결과로 고정하지 않는다.

## 5. ETag와 legacy 전환

새 revision ETag는 collection ID, record ID, random 32-byte validator, deterministic schema digest를 domain-separated SHA-256으로 묶은 opaque strong tag다.

```text
"pgb1.<base64url-sha256>"
```

schema digest는 record 표현에 영향을 주는 field ID/name/type/ordinal/nullability/default와 codec version을 포함한다. 필드 추가·삭제·타입 변경 뒤에는 이전 tag가 일치하지 않는다. token secret을 사용하지 않아 secret rotation이 ETag를 무효화하지 않는다.

기존 row는 GET이 이미 생성한 canonical response JSON bytes와 collection/record/schema digest를 hash한 `pgb0` strong ETag를 반환한다. GET은 sidecar write를 만들지 않는다. 첫 update/delete는 physical row를 `FOR UPDATE`로 잠근 뒤 같은 legacy ETag를 재계산해 비교하고, 성공할 때 version 1/new validator로 승격한다. 이 방식은 startup backfill 없이 실제 legacy 표현을 검증한다.

## 6. Idempotency-Key

현재 IETF 문서는 RFC가 아니라 만료된 Internet-Draft이므로 그 오류 의미를 참고하되 wire grammar를 pgBase가 명시한다. 키는 정확히 한 header, 16–128 ASCII bytes, `[A-Za-z0-9._~-]`이고 raw 값은 기록하지 않는다. request hash는 operation/collection과 lossless canonical JSON을 domain-separated SHA-256으로 계산한다.

같은 scope/key에서:

- 같은 request hash의 completed 결과는 원래 201 body/ETag를 replay한다.
- 다른 request hash는 `409 idempotency_conflict`다.
- 아직 다른 transaction이 같은 key를 처리 중이면 `409 idempotency_in_progress`다.

다중 인스턴스 직렬화에는 actor/collection/key hash에서 파생한 transaction advisory lock을 쓴다. hash collision은 드문 불필요한 직렬화만 만들며 correctness를 바꾸지 않는다. mutation retry에서 record/event/validator ID는 logical operation 단위로 안정적으로 재사용한다.

## 7. If-Match와 lost-update 방지

`If-Match`는 [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html#name-if-match)의 strong comparison과 412 의미를 따른다. 정확히 한 header block을 1 KiB 이하로 제한하고 `*` 또는 최대 8개의 entity tag를 받는다. weak tag는 strong comparison에서 일치하지 않는다. malformed/과도한 값은 `400 invalid_precondition`, 존재하는 record의 stale 값은 `412 precondition_failed`이며 현재 `ETag`를 응답한다. RFC 9110 §13.2.1에 따라 schema/type/body 같은 normal request validation은 precondition보다 먼저 평가하고, 이미 존재하지 않는 record의 정상 실패는 404를 유지한다.

동일 ETag로 두 인스턴스가 PATCH/DELETE하면 physical record row를 먼저 잠그고 sidecar를 같은 순서로 확인하므로 한 요청만 성공한다. winner가 update면 나머지는 새 revision을 보고 412가 되고, winner가 delete면 이미 사라진 target에 404를 받는다. header가 없는 `/api/v1` 요청은 현재 body/status 호환을 위해 serialized last-write-wins로 처리하되 audit/응답에 compatibility 사용을 명시한다. 번들 UI는 이 경로를 사용하지 않는다. 향후 versioned public API에서 [RFC 6585의 428](https://www.rfc-editor.org/rfc/rfc6585.html#section-3)로 precondition을 강제한다.

## 8. Transaction 순서

### Create

1. principal/header/body를 bounded validation하고 stable IDs/hash를 준비한다.
2. key가 있으면 Read Committed transaction의 첫 statement로 transaction advisory lock을 시도한다. 이 순서와 격리 수준은 lock 함수용 `SELECT`가 먼저 만든 snapshot과 직전 creator commit 사이의 replay 누락을 막는다. key가 없는 경로는 Repeatable Read를 유지한다.
3. `schema_state`를 `FOR SHARE`로 잠근 뒤 current catalog와 collection을 pin하고 completed result를 확인한다. Schema apply는 DDL 전에 같은 row를 `FOR UPDATE`하므로 physical table과 catalog의 lock order가 고정된다.
4. dynamic INSERT 후 committed representation을 읽는다.
5. revision version 1, audit, outbox, completed idempotency result를 쓴다.
6. commit 뒤에만 결과를 반환한다.

### Update

1. current catalog를 pin하고 `CompileUpdate`로 schema/type/body normal request validation을 끝낸다.
2. physical row를 `FOR UPDATE`로 읽고 sidecar 또는 legacy representation에서 현재 strong ETag를 계산한다.
3. supplied `If-Match`를 비교하고 mismatch면 write 없이 끝낸다.
4. 미리 compile한 dynamic PATCH/RETURNING을 실행하고 revision 증가/승격, audit/outbox를 쓴다.
5. commit 뒤 새 body/ETag를 반환한다.

### Delete

Update와 같은 lock/precondition 순서를 따른 뒤 app row와 sidecar를 삭제하고 이전 version+1의 tombstone audit/outbox를 남긴다.

현재 bounded retry 대상 `0A000`, `40001`, `40P01`만 유지한다. 일반 network/commit 오류를 자동 재실행하지 않는다. create는 같은 key 재시도로 결과를 조정할 수 있고, update/delete는 GET의 현재 ETag로 상태를 확인한다. PostgreSQL의 `INSERT ... ON CONFLICT` 원자성 및 Repeatable Read/serialization retry 지침은 [공식 INSERT](https://www.postgresql.org/docs/current/sql-insert.html), [transaction isolation](https://www.postgresql.org/docs/current/transaction-iso.html), [serialization failure handling](https://www.postgresql.org/docs/current/mvcc-serialization-failure-handling.html)을 따른다.

## 9. HTTP와 UI 계약

| 요청 | 성공 계약 |
| --- | --- |
| GET one | 200 body + `ETag` |
| POST first | 201 body + `ETag`, `Location`, `Idempotency-Replayed: false` |
| POST replay | 원래 201/body/ETag/Location + `Idempotency-Replayed: true` |
| PATCH | 200 body + 새 `ETag` |
| DELETE | 204 |

헤더가 없는 compatibility 요청에는 `PgBase-Compatibility`를 보낸다. CORS allow에는 `Idempotency-Key`, `If-Match`, expose에는 `ETag`, `Location`, `Idempotency-Replayed`, `PgBase-Compatibility`를 추가한다.

표준 `Deprecation` 응답 헤더는 compatibility mode 표시에 사용하지 않는다. [RFC 9745](https://www.rfc-editor.org/rfc/rfc9745.html#section-2.1)은 값을 Structured Field Date로 요구하고 응답의 target resource 자체가 폐기 예정이거나 이미 폐기됐다는 의미를 부여한다. 따라서 실제 API resource/version을 폐기할 때만 정확한 날짜와 `rel="deprecation"` 정책 링크를 함께 제공한다.

UI는 create draft마다 `crypto.randomUUID()`를 만들고 성공하거나 입력이 바뀌기 전까지 같은 body/key를 사용한다. edit load가 받은 ETag를 PATCH/DELETE에 전달하며 412에서 덮어쓰지 않고 reload 안내를 제공한다.

## 10. 완료 조건과 비범위

- v3 populated DB와 빈 DB의 v4 migration, v1–v3 checksum 불변
- 1,597-field collection에서 app column 변화 없음
- multi-pool same-key create가 하나의 record/event/result로 수렴
- same-ETag concurrent PATCH/DELETE에서 정확히 하나만 성공
- audit/outbox/idempotency failure injection 시 app mutation도 rollback
- legacy `pgb0` GET과 첫 guarded mutation의 `pgb1` 승격
- raw key/session/email/record value가 audit/log/metric에 없는 회귀 테스트
- API/CORS/UI header 및 409/412/compatibility 계약
- full Go/race/vet/build, PostgreSQL E2E, UI typecheck/test/build

Public user auth/rules, relation, outbox delivery worker, realtime/SSE, full change history 복원, online backfill, direct SQL capture, 규제형 외부 WORM archive는 이번 범위가 아니다.
