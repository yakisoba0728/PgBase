# PocketBase v0.40.4 ↔ PgBase 실제 소스 1:1 비교

**2026-09-22 판정: PgBase의 핵심 제품 기능은 넓게 구현돼 있지만, PocketBase와 전체 동작이 일치하는 완성품은 아니다.** 기존 문서의 `232개 구현 / 부분 0 / 미구현 0`은 제한된 수용 조건의 이력이었다. 실제 공식 소스와 현재 실행 경로를 대조하면 세부 동작 차이, 자체 UI↔서버 연결 결함, 공개 API 누락이 남아 있다.

앞선 작업에서 보완한 다섯 공백(확장 객체 백업, text ID UI, 관계 접근 설정 UI, builder view 편집, TypeScript 원문 JSON)은 해당 범위의 완료 기록으로 유지한다. 이번 조사는 그보다 넓은 **원본 PocketBase 동작과의 비교**다.

## 비교 기준과 범위

| 대상 | 고정 기준 |
|---|---|
| PocketBase 공식 GitHub | [v0.40.4](https://github.com/pocketbase/pocketbase/releases/tag/v0.40.4), `5cec579da984436a258602a46a96302fbd31f77c`, 2026-09-12 release |
| 공식 JavaScript SDK | [0.28.1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/package.json), `01efa38294141f71bc60dc6359a06c29e19b93fb` |
| 공식 Dart SDK | [0.25.1](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/pubspec.yaml), `b3f0831c3bbb2257438fd77041c09f2826d316be` |
| PgBase | 사용자가 선택한 로컬 origin/main `6993744a8f36db48ddfeaa8dae017656752d24c7` + `codex/remaining-development` 미커밋 구현 |
| 작업 트리 증거 | [파일 SHA256 manifest](source-manifest.json), fingerprint `80f900c08bbf28650fe1f8d338a86c48ac3f63e51bac53fb02e78b57a5b3e514` |
| 대조 항목 | 과거 232개 전부 + 새로 분리한 세부 기능·누락 61개 = 293개 |
| 인용 범위 | 공식 저장소 3곳의 서로 다른 소스/테스트 파일 181개를 행별 근거로 인용 |

비교는 기능·공개 API 계약별이다. 공식 저장소 모든 줄, 모든 export, 모든 입력 조합을 검증했다는 뜻은 아니다. 61개에는 SDK별 구분과 기존 큰 항목에서 분리한 세부 항목도 있어 **61개의 독립 결함**을 뜻하지 않는다. 도메인 사이에도 같은 원인을 다른 사용자 표면에서 설명하는 경우가 있다. 전체 소스 정적 분석 도구나 침투 시험을 수행한 보고서도 아니다.

## 판정 집계

| 판정 | 기존 232개 | 추가 61개 | 합계 | 뜻 |
|---|---:|---:|---:|---|
| 좁은 계약 일치 | 8 | 1 | **9** | 명시한 기능 의미가 대응하고 테스트 근거가 있음 |
| 대응 구현·설계 차이 | 94 | 5 | **99** | 목적에 맞는 구현이 있으나 PG/API/보안·수명 모델이 다름 |
| 보완 필요 | 129 | 30 | **159** | 일부 PB 동작·옵션이 없거나 현재 실행 경로에 결함이 있음 |
| 미구현 | 1 | 24 | **25** | 해당 공개 연산·UI 또는 정확한 PB 기능 경로가 없음 |
| 판정 보류 | 0 | 1 | **1** | 정적 의심 경로는 있으나 영향 확인을 위한 재현 필요 |
| 합계 | 232 | 61 | **293** | 완료율 계산용 분모가 아님 |

`좁은 계약 일치`조차 제품 전체를 “완벽” 또는 drop-in compatible이라고 보장하지 않는다. 일부 행은 테스트 소스만 확인한 `test-exists`다. 어떤 이름의 기능이 존재하더라도 API 서명, 입력·출력, 실패·트랜잭션·재연결 의미가 다르면 같은 구현으로 처리하지 않았다.

| 영역 | 계약 일치 | 대응 구현 | 보완 필요 | 미구현 | 보류 |
|---|---:|---:|---:|---:|---:|
| 스키마·필드 | 2 | 11 | 28 | 3 | 0 |
| 레코드·조회 | 3 | 12 | 20 | 1 | 0 |
| 인증·권한·메일 | 0 | 9 | 45 | 1 | 1 |
| 파일·실시간 | 2 | 15 | 13 | 2 | 0 |
| 확장·마이그레이션 | 0 | 15 | 13 | 3 | 0 |
| SDK | 1 | 7 | 16 | 7 | 0 |
| 관리자 UI | 0 | 14 | 18 | 8 | 0 |
| 운영·배포 | 1 | 16 | 6 | 0 | 0 |

## 완료 범위를 분명히 말할 수 있는 부분

**좁은 원본 계약과 대응하는 부분**에는 view 레코드 쓰기 금지(REC-06), 순서 보장·원자적 batch 처리 핵심(REC-08), page count/skipTotal(QRY-17), dependency-aware collection 삭제(SCH-06), required blank/zero 판정(SCH-11), 파일 최대 개수 제한(FIL-02), SSE 기본 연결 헤더(RT-01), panic 후 500 request log(OPS-16), Dart filter placeholder helper(SDK-09-DART)가 있다. 주변 API/실시간 수명/옵션까지 완료라는 판정은 아니다.

**PgBase 자체 계약으로 구현된 큰 기능**도 상당하다.

- PostgreSQL 스키마 plan/preview/apply와 durable operation receipt, typed record CRUD, 정확한 text public ID, 관계 저장·ETag·생성 멱등성.
- 별도 auth realm, password/OAuth/OTP/TOTP·recovery 로그인, DB 세션 폐기, 레코드 규칙·관리자/사용자/service 주체 구분.
- local/S3 파일 저장과 토큰, PG exported snapshot 백업, extension filesystem 객체와 원본 제거 뒤 복원까지의 경로.
- Go 확장 API, trusted Goja 실행, custom routes/hooks, durable jobs와 사용자 migrations.
- 관리자 컬렉션/레코드 편집, 관계 picker, builder view 변경, 스토리지·백업·로그·SQL 콘솔.
- TypeScript/Dart SDK, TS 정밀 JSON 원문 연산, HTTPS/ACME·health·CLI·서명된 self-update.

이 목록은 “기능이 없다”는 오해를 막기 위한 것이다. 위 기능이 포함된 큰 비교 행은 원본의 세부 옵션이 빠져 있으면 여전히 partial이다. 각 구현·테스트는 [전체 대조표](matrix.md)에서 확인할 수 있다.

## 먼저 보완할 실제 동작

| 우선 | 항목 | 현재 차이와 영향 | 증거 수준 |
|---|---|---|---|
| 1 | **QRY-22 숨김 필드 검사** | non-admin `title = secret`에서 오른쪽 hidden 필드가 두 guard를 통과해 SQL에 포함된다. 결과/건수로 숨김 값 추론 가능성을 차단해야 한다. | guard/compiler 재현 1개 실행. DB/HTTP 정보 노출 실행은 안 함 |
| 1 | **FLD-12 다중 관계 cascade** | source→[A,B]에서 A 삭제 시 PB는 B 참조와 source를 유지한다. PgBase cascade는 source 자체를 삭제 집합에 넣는다. | 양쪽 실제 삭제 소스 대조; 동일 DB 차등 fixture는 안 함 |
| 1 | **RULE-07 / UI-11 규칙 저장·preview** | UI는 `{rules,revision}`을 보내지만 서버의 strict decoder는 rules만 받는다. preview는 선택 action 대신 다섯 규칙 전체를 한 rule로 보낸다. | frontend와 backend 연결 소스 대조; 기존 UI mock은 이 경계를 검사하지 않음 |
| 1 | **RT-03/07/10 실시간 수명·복구** | application topic을 polling에서 일반 collection으로 읽는 경로, production WriteTimeout 미해제, UUID PB_CONNECT ID와 numeric 재연결 parser 충돌, 여러 topic cursor 순서 문제가 있다. | 정적 실행 경로 분석. 장시간/연결 중단 재현 필요 |
| 1 | **AUTH-34/35/36 인증 전환** | OAuth 신규 가입 createRule/required 필드 적용 경로, email 변경 뒤 이전 verification token 무효화, 검증 시 기존 credential 정리의 차이. AUTH-35는 미확정 의심이다. | 정적 분석만. 운영 취약점으로 확정하지 않음 |
| 1 | **OPS-19 백업 보존 범위** | PB 자동 백업 한도와 달리 PgBase 한도는 수동·업로드 백업도 지울 수 있다. 자동/수동 정책 분리 또는 명확한 별도 계약이 필요하다. | 정적 대조 + 현재 전체 보존 동작 unit 재실행 |
| 1 | **OPS-07 rate-limit 태그** | PB collection:action 규칙을 실제 PgBase admission 라벨이 생성하지 않는다. 경로 규칙은 구현돼 있다. | admission source 대조 |
| 2 | **RULE-03/05/09/11/13 권한 언어** | request 변수 규칙 저장, 원래 HTTP 호출자 context, hidden 필드를 사용하는 stored rule, null/LIKE 조건이 PB와 다르다. 조건에 따라 허용되는 레코드가 달라질 수 있다. | 소스·기존 테스트. 전체 HTTP 차등 검증 필요 |
| 2 | **SCH-24/26 스키마 재적용** | deleteMissing이 생략 필드를 제거하지 않으며, UI에서 수정 가능한 builder view도 schema import에서는 변경을 거절한다. | 실제 import/planner 소스 대조 |
| 2 | **SDK-06/07/08/X10** | token만 저장해 재생성 후 record 손실, 기본 CRUD 중복 취소 없음, JSON 본문 취소를 undefined 성공으로 처리, scaffold 타입과 자체 서버 응답 불일치. | 앞의 세 경로는 Node probe 실행, scaffold는 소스 대조 |

Query 일반 호환에도 null/빈 문자열·LIKE escape·따옴표·literal 비교·`strftime('%w',…)`·비인덱스 sort·fields/expand 차이가 있다. 파일에는 Range/conditional response, inline/download switch, EXIF 회전, thumbnail fallback 차이가 남는다. 이들은 [records/query](records-query.md), [files/realtime](files-realtime.md)에 구체적인 입력과 구현 위치를 적었다.

## 명시적으로 없는 기능

25개 행의 전체 목록은 [미구현 상세](matrix.md)에 포함되어 있다. 주요 작업 묶음은 다음과 같다.

- **PB 전송 형식:** PB batch wire adapter. 일반 record POST/PATCH의 직접 multipart는 해당 CRUD 행의 부분 구현으로 따로 집계했다.
- **스키마/필드:** 동시에 두 필드 이름을 교환하는 변경, view file/relation 의미, text `:autogenerate` modifier.
- **확장:** `ClearBootstrap/OnBootstrapClear`, 공개 `TxInfo.OnComplete`, migration `ReapplyCondition`.
- **파일/메일:** EXIF orientation 처리, 인자로 다른 storage를 선택하는 `$filesystem.s3(...)`, SMTP 비활성 시 sendmail fallback.
- **SDK:** auth record 수정/삭제와 AuthStore 자동 동기화, JS external-auth helper, health/backup/SQL 서비스, JS superuser 자동 refresh 옵션, Dart typed-path RecordModel helper.
- **UI:** 여러 관리자 계정 CRUD, auth/manage 규칙 전용 편집, 목적별 token TTL/일괄 무효화, 필드 복제·순서 변경, 관계 ERD, 공개 파일 미디어 라이브러리, 컬렉션 검색/pin/system 분리, 운영 제어 잠금.

PB의 SQLite 자체, JWT 포맷, 관리자 bearer 인증을 채택하지 않은 점은 이 목록의 미구현으로 세지 않았다. 해당 구조 차이는 [구조·대표 endpoint 대조](architecture.md)에 따로 명시했다.

## 검증 범위와 다음 완료 기준

이번 감사에서는 PgBase query 관찰 subtest 15개, record 관련 선별 unit 9개, field/schema/파일 관련 선별 unit, 운영 3개 package 집중 검사, SDK Node probe를 실행했다. query 관찰 테스트의 PASS는 **현재 차이가 재현됐음**을 뜻한다. 감사용 원본과 로그를 보존했다. 공식 PB 서버/Go1.27 런타임과의 양쪽 HTTP 실행은 하지 않았으며 PB 기대값은 고정 공식 소스와 upstream 테스트에서 확인했다.

행별 verification은 `executed-now` 49, `prior-run` 42, `test-exists` 153, `static-only` 49이다. 이는 실행 테스트 개수가 아니라 **근거 분류를 부여한 행 수**이며 동일 테스트가 여러 행을 뒷받침할 수 있다. 해당 행의 모든 호환성을 검증했다는 뜻도 아니다. [이전 개발 검증](../../development-completion-2026-09-22.md)의 전체 Go unit, backup/schema integration/race, UI 590 tests·브라우저 6흐름, TS 검증은 이번 원본 conformance와 구분한다.

권장 개발 순서는 다음과 같다.

1. 숨김 query operand, cascade, 규칙 UI 연결, realtime 장시간·reconnect를 재현 테스트로 고정하고 수정한다.
2. AUTH-34/35/36을 격리 DB fixture에서 검증하고 실제 이메일·credential 변경 경계를 완성한다. 보류 항목은 재현 결과에 따라 판정을 확정한다.
3. null/LIKE/request/auth context와 import를 **같은 입력의 PB/PG 차등 테스트**로 고정한다. native 계약과 PB compat 경로를 구분한다.
4. 파일 전송·확장 continuation·migration 실행 단위·SDK public 표면·관리 UI의 빠진 기능을 항목별로 닫는다.
5. 실제 설치 업그레이드/복구, 여러 호스트·부하, 외부 OAuth/SMTP/공개 ACME, Windows updater와 공개 릴리스를 별도 인수한다.

각 수정의 완료 조건은 함수명 존재나 mock 단위 통과가 아니라 **사용자가 접근하는 route/UI/SDK에서 기대 동작과 실패 동작이 입증되는 것**이다. 모든 partial을 PB 그대로 바꿀 필요는 없지만, 유지할 차이는 문서·타입·테스트에 명시하고 silent no-op은 제거해야 한다. 이 감사에서는 제품 코드를 수정하거나 배포하지 않았다.

## 상세 자료

- [전체 293행 읽기용 대조표](matrix.md) / [구조화 JSON](matrix.json)
- [스키마·필드](schema-fields.md) / [레코드·조회](records-query.md)
- [인증·권한·메일](auth-rules-mail.md) / [파일·실시간](files-realtime.md)
- [확장·마이그레이션](extensions-migrations.md) / [SDK](sdk.md)
- [관리자 UI](ui.md) / [운영·배포](operations.md) / [구조·API](architecture.md)
- [범위·참조 자동 검증 결과](validation.txt) / [작업 트리 파일 manifest](source-manifest.json)
