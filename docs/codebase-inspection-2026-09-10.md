# PgBase 코드베이스 점검 — 2026-09-10 갱신

기준선은 `a7b8ac0c4c3605aa68708eea4fe4c760a12e0a10`, 구현 집계 기준 HEAD는 `c0e5b0da1876017b37de1afc0f1ae0899b7b2f8f`이다. 원래 점검에서 부분으로 남긴 68개 행을 post-baseline commit, 코드 경로와 회귀 테스트 파일에 다시 연결했다. 상세 항목은 [기능 비교표](pocketbase-feature-comparison-current-2026-09-10.md), 수용 조건과 commit ledger는 [부분 구현 ledger](pocketbase-partial-implementation-2026-09-10.md)에 있다.

## 현재 판정

| 상태 | 원래 | 현재 | 판단 기준 |
| --- | ---: | ---: | --- |
| 구현 | 129 | 197 | route/service가 실제 호출 가능하고 관련 회귀 테스트 파일이 있다. |
| 부분 | 68 | 0 | 남은 부분 구현 행 없음. |
| 미구현 | 35 | 35 | 새 구현 근거가 없는 원래 행을 유지했다. |

## 구현 범위에서 확인한 주요 경계

- schema: compatibility metadata, PB catalog compatibility route, safe field physical identity, view sample/viewQuery mapping을 추가했다.
- query/records: PB filter/LIKE, field comparison, reverse relation sort, lenient expand, response projection, conditional PUT/no-op PATCH를 구현했다.
- auth/files/realtime: typed auth records, purpose-token rotation, OAuth descriptors, protected/public files, multiplex subscriptions과 policy re-evaluation을 추가했다.
- extensions/SDK/UI/operations: trusted transaction/logical object helpers, TypeScript bundle/templates, SDK state/realtime helpers, admin settings/operations UI, persisted runtime encryption을 추가했다.

## 기능 충족도와 native 차이

197개 구현 행은 원래 수용 조건의 사용자 기능이 호출 가능하고 검증됐다는 뜻이다. PocketBase wire/ORM과 동일하다는 뜻은 아니다. 특히 EXT-06은 automatic PB Model ORM이 아니라 callback-owned transaction의 opt-in `RunModelMutation` wrapper이며, SDK-05는 host popup/deeplink I/O를 adapter에 맡기는 orchestration이다. 각 차이는 상세표에 남겼다.

전체 Go unit/vet/PG17.9+MinIO integration, TypeScript, Dart, UI build/Vitest, fresh browser profile/settings·backup/restore·S3 scenarios는 final logs에서 통과했다. 두 보류 행은 전체 suite 실패가 아니라 원래 행의 좁은 acceptance wording에 대한 직접 증거를 기다리는 상태다.
