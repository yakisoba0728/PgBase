# PgBase 코드베이스 점검 — 2026-09-11 갱신

PocketBase 대조 원본은 [공식 v0.40.3 pinned commit](https://github.com/pocketbase/pocketbase/tree/5684ee24f1e88f71a2ede73d20cf547fbd509e16) `5684ee24f1e88f71a2ede73d20cf547fbd509e16`이다. 최종 코드 검증 HEAD는 `bd76b1d1aaf28839072fa71e6b05cf39219053e9`다.

원래 232행은 구현 129·부분 68·미구현 35였고, source audit 기준선 `1c0f967`은 구현 199·부분 8·미구현 25였다. Round 2는 구현 211·부분 0·미구현 21까지 닫았다. Round 3는 `a3a3293`의 고정 artifact(SHA-256 `1a7f84540b9aae3eb442dc361b2cd270ce1c30d16e2104fcbbcacf9e6a90f15a`)에서 남은 21행을 구현하고 최종 통합 검증했다. 상세 결과는 [기능 비교표](pocketbase-feature-comparison-current-2026-09-10.md), [Round 3 완료 ledger](pocketbase-unimplemented-completion-round3-2026-09-11.md)에 있다. [Round 2 ledger](pocketbase-partial-completion-round2-2026-09-10.md)는 당시 기록으로 보존한다.

## 현재 판정

| 상태 | 원래 | Round 2 | 현재 | 판단 기준 |
| --- | ---: | ---: | ---: | --- |
| 구현 | 129 | 211 | 232 | route/service/SDK/UI 진입점이 호출 가능하고 행별 거절·rollback·동시성 조건 및 최종 suite를 검증했다. |
| 부분 | 68 | 0 | 0 | 수용 조건 일부만 충족한 행 없음 |
| 미구현 | 35 | 21 | 0 | 고정된 232행 범위에서 수용 조건이 열린 행 없음 |

## Round 3에서 확인한 주요 경계

- schema는 stable-ID rename, OID-bound SQL view refresh, truncate, base/auth/view scaffold, safe advanced index와 `deleteMissing`을 제공한다. 동적·모호한 SQL view 의존성, `CONCURRENTLY` 및 표현하지 못하는 raw index 조항은 의미를 버리지 않고 거절한다.
- record ID는 public external text alias와 authoritative internal UUID를 함께 사용한다. base alias는 collection별 unique이고 auth alias는 모든 auth realm에서 UUID-auth ID 충돌까지 포함해 global unique다. PostgreSQL PK/FK, principal/token, cursor와 `@rowid` authority는 UUID다.
- record modifier는 number/select/relation/file을 transaction에서 적용한다. query pagination은 explicit `compat=pocketbase` adapter이며 기존 native cursor를 보존하고 total/items snapshot 및 count 없는 `skipTotal`을 제공한다.
- arbitrary application realtime topic은 raw 값을 보존하되 collection namespace 충돌을 막는 `@app/<base64url>` wire codec을 쓴다. UI extension은 operator-trusted startup asset이다.
- TLS/ACME는 static 및 managed restore 경로에서 검증했다. ACME 증거는 실제 local RFC8555 test CA까지이며 public DNS/CA 발급은 아니다.

## 최종 검증

`bd76b1d1`에서 `go test ./...`, `go vet ./...`, `go test -tags integration -p 2 ./... -count=1`이 통과했다. `internal/recordidentity`, `internal/recordops`, `internal/query`, `internal/userauth`, `internal/realtime`, `pgbase` integration race도 통과했다. 실제 SDK text-ID/auth HTTP conformance는 두 번 통과했고, TypeScript build/types/tests 8개, Dart analyze와 tests 30개, UI 44 files/533 tests와 build를 확인했다. Dart의 기존 info lint 1개와 UI의 기존 chunk-size warning은 실패가 아니다.

실제 Chromium 검증은 최종 HEAD 이전의 schema+UI 통합 head에서 수행했다. Auth scaffold와 duplicate가 실제 auth realm을 만들고 각각 register 201/login 200 및 PostgreSQL realm/user 상태를 보존했으며 base/view scaffold, named truncate와 UI extension 화면도 확인했다. 최종 `bd76b1d1`에서는 UI unit/build를 재실행했다.

최종 구조 측정은 data 27,498, handwritten 97,941, UI 7,101, interfaces 94, exports 1,553, constructors 63, UI hooks 180, UI exports 250, production 800줄 초과 파일 0이다. 마지막 commit은 doctor fixed-cost probe를 39에서 41로 조정했고 catalog-size equality를 유지한다.

232개 `구현`은 이 비교표의 좁은 기능 수용 조건을 충족했다는 뜻이다. PocketBase HTTP wire, 공식 SDK package, ORM 또는 storage를 수정 없이 교체할 수 있다는 뜻은 아니다. PostgreSQL 대응 의미와 명시적 compatibility adapter는 상세표의 차이 열을 따른다.
