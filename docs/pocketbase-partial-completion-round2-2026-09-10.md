# PocketBase 부분 구현 2차 완료 ledger — 2026-09-10

## 고정 기준과 판정 원칙

- 기준선: `1c0f967`의 232행 비교표를 JSON으로 고정 복사했다. 파일은 `.local/partial-completion-round2/baseline.json`이며 SHA-256은 `0f98b4b68beb9a48172124462855bd8c74d6cfb42d2986fe4d350d56ac52cc94`다.
- PocketBase 기준: [공식 v0.40.3 pinned commit](https://github.com/pocketbase/pocketbase/tree/5684ee24f1e88f71a2ede73d20cf547fbd509e16) `5684ee24f1e88f71a2ede73d20cf547fbd509e16`.
- 기준선 집계: 구현 199, 부분 8, 미구현 25. 이 문서는 그중 **부분 8행만** 완료 조건으로 추적한다.
- 완료는 설정 구조체·타입·wrapper·단위 테스트 하나의 존재만으로 판정하지 않는다. 각 기능의 실제 HTTP/SDK/extension 진입점에서 정책이 실행되고, 허용·거절·우회·실패/rollback 경로가 검증돼야 한다.
- PostgreSQL 저장 방식과 PocketBase wire가 다른 것은 허용되는 설계 차이다. 그러나 사용자가 기대할 정책·권한·capability가 호출 경로에서 빠지면 `부분`으로 유지한다.

## 담당과 현재 공백

| ID | 기능 | 담당 | 현재 확인된 범위 | 남은 공백 |
| --- | --- | --- | --- | --- |
| SCH-02 | auth collection data model | SolSchema | typed auth profile/relation/file·일부 rule 모델 | authRule/manageRule 전 경로와 PB shape round-trip |
| AUTH-15 | OAuth unlink | SolRealtime | identity 목록·unlink route/UI | 마지막 sign-in identity 보호의 auxiliary DELETE 우회 |
| RULE-03 | PB filter syntax/variables | SolQuery | scalar 비교·AND/OR·일부 modifier | PB 변수·relation resolver·문법 범위 |
| RULE-05 | authRule | SolRealtime | password registration/login | OAuth·OTP·MFA·refresh 적용 |
| RULE-06 | manageRule | SolSchema | 현재 user/제한된 update | delegated 다른 auth record 관리 |
| FIL-13 | extension filesystem facade | TerraAdmin | namespaced object read/write/delete | list/copy/stream과 file-system capability |
| EXT-14 | JSVM globals | SolAdmin | `require("pgbase")` DB/object 일부 | 요구한 global/capability matrix |
| OPS-07 | global API rate rules | TerraRuntime | admin/user auth rate policy | endpoint label 전역 rule과 middleware |

## 행별 완료 수용 조건

### SCH-02 — auth collection data model

| 확인 항목 | 완료 기준 |
| --- | --- |
| 저장·투영 | auth collection의 typed fields, profile/relation/file, 5 record rules, `authRule`, `manageRule`, token/auth settings를 create/read/update/import/export에서 손실 없이 보존한다. |
| 정책 실행 | password, OAuth, OTP, MFA, refresh와 auth-record CRUD의 실제 route/service 진입점이 같은 persisted rule을 읽어 허용/거절한다. |
| 권한 경계 | anonymous/user/admin 및 self/delegated target을 분리하고, hidden field·relation/file access도 policy 결과를 따른다. |
| 실패 검증 | malformed rule, unknown field, disabled/empty/null rule, denied target, transaction 실패에서 schema/record/policy가 반쯤 저장되지 않는다. |
| 증거 | PostgreSQL integration으로 각 auth method와 import/export round-trip을 검증하고 PB-compatible collection response를 확인한다. |

### AUTH-15 — OAuth unlink

| 확인 항목 | 완료 기준 |
| --- | --- |
| 대상 확인 | identity list와 unlink는 owner/admin 권한 및 provider/subject를 정확히 바인딩한다. |
| 마지막 로그인 수단 | password, OTP, OAuth identity 조합을 계산해 마지막 유효 sign-in 수단 제거를 모든 unlink 진입점에서 거절한다. |
| 우회 차단 | UI route뿐 아니라 auxiliary/system-record DELETE, concurrent unlink, 직접 service 호출도 동일 guard를 지난다. |
| 실패 검증 | denied/missing/last identity/concurrent request가 identity·session·audit 상태를 손상시키지 않는다. |

### RULE-03 — PocketBase filter syntax/variables

| 확인 항목 | 완료 기준 |
| --- | --- |
| 문법 | PB filter 문자열의 비교, 논리식, null/empty, modifier, field-to-field 및 supported relation path를 bounded parser로 해석한다. |
| 변수 | `@request.auth.*`, `@request.body.*`, record/collection context를 실제 policy evaluation 값으로 resolve한다. |
| relation | forward/reverse relation resolver와 type-aware parameterized SQL compiler가 scalar와 relation 조건을 같은 policy에서 처리한다. |
| 실패 검증 | 미지원 함수/경로, type mismatch, injection-like literal, limit 초과가 deterministic validation error이며 native owner rule은 변하지 않는다. |

### RULE-05 — authRule

| 확인 항목 | 완료 기준 |
| --- | --- |
| 전 경로 적용 | password registration/login, OAuth start/complete, OTP request/verify, MFA complete, refresh 각각이 같은 realm `authRule`을 평가한다. |
| 효과 | deny rule은 token/session/user creation 전에 차단하고 allow rule은 필요한 request/auth/origin context를 사용한다. |
| 경계 | null/empty rule의 PB 의미와 disabled collection, existing/new identity, concurrent completion을 분리한다. |
| 실패 검증 | 각 method별 deny가 side effect 없이 끝나며 success/error audit 및 origin alert가 policy와 모순되지 않는다. |

### RULE-06 — manageRule

| 확인 항목 | 완료 기준 |
| --- | --- |
| 대상 범위 | self뿐 아니라 authorized delegated target의 auth-record read/update/delete와 auxiliary identity 관리를 `manageRule`로 판정한다. |
| field 보호 | email/password/verified/active/hidden field mutation이 manage permission과 별도 credential invariant 모두를 지난다. |
| 우회 차단 | direct service, API route, auxiliary record route, batch/transaction 경로가 같은 rule binding을 사용한다. |
| 실패 검증 | denied delegated update/delete와 concurrent target change가 credential·identity·session을 변경하지 않는다. |

### FIL-13 — extension용 generic filesystem facade

| 확인 항목 | 완료 기준 |
| --- | --- |
| capability | extension permission/manifest와 startup capability 둘 다 확인한 filesystem facade를 Go/JS에 제공한다. |
| 동작 | namespace/object key list, copy/move 또는 명시적 동등 동작, streaming read/write, delete/stat을 bounded key/size 정책으로 제공한다. |
| 저장소 | local/S3 및 storage migration/backup lifecycle에서 같은 logical object semantics를 유지한다. |
| 실패 검증 | 권한 없음, namespace traversal, partial stream/write, backend 오류, cancellation이 다른 namespace나 metadata를 손상시키지 않는다. |

### EXT-14 — JSVM PocketBase globals

| 확인 항목 | 완료 기준 |
| --- | --- |
| 명시 capability matrix | `$app/$dbx/$security/$os/$http/$filesystem/$apis` 각각을 제공하거나 PgBase 대체 capability와 의도적 비지원 목록을 manifest/API 계약으로 명시한다. |
| 안전 경계 | database, HTTP, filesystem, crypto, process 접근은 startup·manifest·route/hook/job context의 권한을 모두 검사한다. |
| 실제 실행 | JS route/hook/job에서 각 지원 binding이 동작하며, forbidden binding은 존재하지 않거나 stable denial을 준다. |
| 실패 검증 | transaction rollback, HTTP timeout/origin, filesystem traversal, process/secret leak 및 capability escalation을 통합 검증한다. |

### OPS-07 — PB-style configurable global API rate rules

| 확인 항목 | 완료 기준 |
| --- | --- |
| 설정 | persisted runtime settings에 enabled, label/audience rule, rate/burst/window 또는 동등한 명시적 모델을 저장·검증·reload한다. |
| 적용 | 일반 public/user/service/admin API middleware가 route label과 principal/client address를 사용해 rule을 실행한다. 로그인 전용 버킷만으로는 완료가 아니다. |
| 운영 | rule 수정/삭제가 live service에 원자 반영되고 request log/response가 retry 정보와 stable error를 낸다. |
| 실패 검증 | rule 우선순위, trusted proxy/IP key, concurrent requests, reload failure, disabled mode가 다른 route를 오차 없이 처리한다. |

## 최종 판정 절차

1. 담당자는 commit, 실제 code path, 실행 명령과 PASS output, 허용/거절/실패 사례를 ledger에 제출한다.
2. 보고 담당자는 기준선 JSON의 해당 행과 수용 조건을 대조한다. 관련 infrastructure만 있고 실제 entry path가 없으면 완료로 바꾸지 않는다.
3. root 통합 HEAD에서 재검증된 근거만 `구현`으로 승격한다. 새로운 원래 미구현 행은 별도 source-audit 증거 없이는 이 ledger 밖에서 상태를 바꾸지 않는다.
