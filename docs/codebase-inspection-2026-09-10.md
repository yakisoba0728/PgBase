# PgBase 코드베이스 점검 — 2026-09-10 갱신

PocketBase 대조 원본은 [공식 v0.40.3 pinned commit](https://github.com/pocketbase/pocketbase/tree/5684ee24f1e88f71a2ede73d20cf547fbd509e16) `5684ee24f1e88f71a2ede73d20cf547fbd509e16`이다.

기준선은 `a7b8ac0c4c3605aa68708eea4fe4c760a12e0a10`, 최종 구현 검증 HEAD는 `cd6684d79bbec793a6e03ea34812223261a845be`이다. 원래 점검에서 부분으로 남긴 68개 행을 post-baseline commit, 코드 경로와 회귀 테스트 파일에 다시 연결했다. 상세 항목은 [기능 비교표](pocketbase-feature-comparison-current-2026-09-10.md), 수용 조건과 commit ledger는 [부분 구현 ledger](pocketbase-partial-implementation-2026-09-10.md)에 있다.

## 현재 판정

| 상태 | 원래 | 현재 | 판단 기준 |
| --- | ---: | ---: | --- |
| 구현 | 129 | 211 | route/service가 실제 호출 가능하고 관련 회귀·통합 테스트가 최종 HEAD에서 통과했다. |
| 부분 | 68 | 0 | 원래 부분 행의 명시 수용 조건을 모두 닫았다. |
| 미구현 | 35 | 21 | 새 구현 근거가 없는 원래 행을 유지했다. |

## 구현 범위에서 확인한 주요 경계

- schema: compatibility metadata, PB catalog compatibility route, safe field physical identity, view sample/viewQuery mapping을 추가했다.
- query/records: PB filter/LIKE, field comparison, reverse relation sort, lenient expand, response projection, conditional PUT/no-op PATCH를 구현했다.
- auth/files/realtime: typed auth records, purpose-token rotation, OAuth descriptors, protected/public files, multiplex subscriptions과 policy re-evaluation을 추가했다.
- extensions/SDK/UI/operations: trusted transaction/logical object helpers, TypeScript bundle/templates, SDK state/realtime helpers, admin settings/operations UI, persisted runtime encryption을 추가했다.

## 기능 충족도와 native 차이

211개 구현 행은 원래 수용 조건의 사용자 기능이 호출 가능하고 검증됐다는 뜻이다. PocketBase wire/ORM과 동일하다는 뜻은 아니다. 특히 EXT-06은 automatic PB Model ORM이 아니라 callback-owned transaction의 opt-in `RunModelMutation` wrapper이며, SDK-05는 host popup/deeplink I/O를 adapter에 맡기는 orchestration이다. 각 차이는 상세표에 남겼다.

최종 `cd6684d`에서 `go test ./...`, `go vet ./...`, PG17.9+MinIO integration `-p 2`, realtime/userauth/jsvm/pgbase race와 구조 gate가 모두 통과했다. SDK/UI는 코드 통합 전용 검증 HEAD `20b0ea6`에서 TypeScript·Dart 실제 HTTP conformance, UI 524 tests/build와 task-only fresh fixture의 authenticated Chromium 22개 고유 테스트를 통과했다. destructive restore/archive/profile 상태는 독립 fixture로 검증했다. source audit 기준 199/8/25에서 round2 결과를 반영한 최종 집계는 211/0/21이다.
