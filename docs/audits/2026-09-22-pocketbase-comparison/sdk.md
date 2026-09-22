# PocketBase 공식 SDK 대조 — 2026-09-22

**PgBase SDK는 PocketBase 공식 SDK의 drop-in 대체 패키지가 아니다.** 이 감사는 실제 공개 메서드·전송·상태 관리 계약을 비교한다. 서버나 UI에 기능이 있다는 이유로 SDK 메서드까지 구현된 것으로 세지 않았다.

- 공식 JS SDK version `0.28.1`, commit `01efa38294141f71bc60dc6359a06c29e19b93fb`: [package](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/package.json#L1-L19), [exports](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/index.ts#L1-L20).
- 공식 Dart SDK version `0.25.1`, commit `b3f0831c3bbb2257438fd77041c09f2826d316be`: [package](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/pubspec.yaml#L1-L14), [exports](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/pocketbase.dart#L1-L60).
- PgBase `codex/remaining-development`: HEAD `6993744a8f36db48ddfeaa8dae017656752d24c7` 및 현재 미커밋 변경. 새 raw JSON record I/O와 text-ID/auth schema 타입을 포함한다.
- 역사적 기준은 `docs/pocketbase-feature-comparison-current-2026-09-11.json`의 SDK 12개 행(`SDK-02..SDK-12, SDK-14`). 원래 ID를 유지하고 SDK별 차이 및 누락 서비스 행을 추가했다.
- [전체 JSON 증거](sdk.json): 31행 — equivalent 1, adapted 7, partial 16, missing 7. **이 숫자는 제품 완료율이 아니다.** 행 분리와 신규 서비스 때문에 과거 232개 분모와 직접 비교할 수 없다.

## 검증 해석

- `equivalent`: 해당 좁은 계약이 소스와 대응 테스트에서 일치한다. SDK 전체 호환을 뜻하지 않는다.
- `adapted`: 기능 목적은 제공하되 인터페이스/전송 계약을 의도적으로 바꿨다.
- `partial` / `missing`: 실제 동작 또는 공개 helper/service가 빠져 있다.
- `prior-run`: 바로 앞 개발 검증에서 TS `npm test`가 build, strict public type test, 기존 HTTP/auth/SSE 스크립트 및 Node 테스트 16개를 통과한 기록. 이번에 전체 suite를 재실행하지 않았다. PB의 모든 옵션을 검사했다는 뜻이 아니다.
- `executed-now`: 아래 비파괴 Node probe를 이번 감사에서 실행했다.
- `test-exists`: 테스트 소스를 확인했다. **Dart runtime과 Go→SDK 실제 서버 conformance는 이번 감사에서 실행하지 않았다.**
- `static-only`: 구현/exports/signature/호출 경로를 확인했다. 부재는 두 SDK 전체 메서드 검색과 public client 목록을 교차 확인했다.

이번 작업은 연구만 수행했으며 SDK 코드는 수정하지 않았다.

## 먼저 다뤄야 할 차이

1. **TS 기본 중복 요청 취소:** PB는 모든 service send에 기본 method+path key를 붙인다. PgBase는 low-level send의 명시적 key만 등록하며 Records는 우회한다. probe에서 동일 list 두 요청 모두 signal이 없었다. [PB](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/Client.ts#L555-L569), [sdk/typescript/src/index.ts:432](../../../sdk/typescript/src/index.ts#L432), [sdk/typescript/src/index.ts:785](../../../sdk/typescript/src/index.ts#L785).
2. **TS parsed 응답 본문 중 취소:** PB는 response.json의 abort를 다시 던진다. PgBase parsed request/_response는 오류를 undefined로 바꾼다. 실제 probe에서 get은 undefined로 성공했고 새 getJSON은 AbortError로 실패했다. [PB](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/Client.ts#L451-L490), [sdk/typescript/src/index.ts:438](../../../sdk/typescript/src/index.ts#L438), [sdk/typescript/src/index.ts:488](../../../sdk/typescript/src/index.ts#L488).
3. **token+record 지속 저장:** 공식 Local/Async store와 달리 PgBase TS/Dart storage는 token만 보존한다. SSR cookie round-trip이 durable store round-trip을 보장하지 않는다. Dart는 async queue가 있고 TS에는 대응 async store가 없다는 차이도 구분했다. [PB JS](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/stores/LocalAuthStore.ts#L42-L55), [PB Dart](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/async_auth_store.dart#L54-L79), [sdk/typescript/src/index.ts:145](../../../sdk/typescript/src/index.ts#L145), [sdk/dart/lib/src/auth.dart:8](../../../sdk/dart/lib/src/auth.dart#L8).
4. **shared realtime options:** PB 양 SDK의 subscription별 fields/filter/expand/query/headers, prefix unsubscribe와 disconnect hooks가 PgBase shared realtime에 없다. native records.events fields는 별도 표면이다. [PB JS](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RealtimeService.ts#L61-L86), [PB Dart](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/services/realtime_service.dart#L54-L96), [sdk/typescript/src/index.ts:539](../../../sdk/typescript/src/index.ts#L539), [sdk/dart/lib/src/realtime.dart:57](../../../sdk/dart/lib/src/realtime.dart#L57).
5. **운영 서비스의 SDK 범위:** backup artifact lifecycle/health/SQL 고수준 helper가 없고 settings/logs/collection metadata는 부분 구현이다. Dart generic admin.request는 수동 호출 통로지만 TS admin에는 공개 generic request도 없다. [PB 서비스 목록](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/Client.ts#L130-L173), [sdk/typescript/src/admin.ts:110](../../../sdk/typescript/src/admin.ts#L110), [sdk/dart/lib/src/admin.dart:76](../../../sdk/dart/lib/src/admin.dart#L76).
6. **TS/Dart 차이:** TS realm refresh/session/OTP에는 fields가 없지만 Dart에는 있다. Dart record get에는 expand가 없지만 TS에는 있다. 최신 PB JS filter는 arbitrary key를 치환하지만 PgBase TS는 identifier형 key만 허용한다. 공식 Dart와 PgBase Dart의 key grammar는 일치한다. [PB JS filter](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/Client.ts#L302-L344), [PB Dart filter](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/client.dart#L177-L207), [sdk/typescript/src/index.ts:690](../../../sdk/typescript/src/index.ts#L690), [sdk/dart/lib/src/client.dart:646](../../../sdk/dart/lib/src/client.dart#L646), [sdk/dart/lib/src/records.dart:81](../../../sdk/dart/lib/src/records.dart#L81).
7. **TS scaffold type:** `CollectionScaffolds.auth`가 `CollectionChange`로 선언됐지만 실제 PgBase 서버는 `{collection,authCollection}`을 반환한다. 기존 fixture도 단순 shape여서 이를 놓친다. PB는 자체 CollectionModel map이므로 wire 차이와 별개로 PgBase 자사 타입 불일치이다. [PB](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/CollectionService.ts#L65-L83), [sdk/typescript/src/admin.ts:45](../../../sdk/typescript/src/admin.ts#L45), [internal/schema/scaffolds.go:8](../../../internal/schema/scaffolds.go#L8), [sdk/typescript/test/admin.test.mjs:122](../../../sdk/typescript/test/admin.test.mjs#L122).

## 행별 결과

| ID | 기능 | 판정 | 검증 | 남은 차이 |
|---|---|---|---|---|
| SDK-02 | TypeScript record CRUD·페이지 조회·filter/sort/expand | partial | prior-run | 쓰기 응답 expand 옵션 누락. 엔드포인트와 필수 생성 idempotency key·수정/삭제 ETag는 의도적으로 다른 계약이며 PB SDK를 그대로 교체할 수 없다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RecordService.ts#L191-L307) |
| SDK-02-DART | Dart record CRUD·페이지 조회·expand | partial | test-exists | TS get과 달리 Dart 단건 get에도 expand가 없다. 임의 query/headers와 쓰기 expand도 고수준 Records 메서드에서 누락된다. [PB 원문](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/services/base_crud_service.dart#L55-L225) |
| SDK-03 | TypeScript fields projection 전달 범위 | partial | prior-run | realm refresh/session/OTP와 shared realtime.subscribe의 fields 전달이 빠져 있다. 이전의 모든 auth/realtime projection 완료 표기는 너무 넓다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/tools/options.ts#L51-L85) |
| SDK-03-DART | Dart fields projection 전달 범위 | partial | test-exists | TS realm의 누락은 Dart에는 없지만 shared realtime 구독 projection은 Dart도 누락된다. Dart 테스트는 이번 감사에서 실행하지 않았다. [PB 원문](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/services/realtime_service.dart#L54-L106) |
| SDK-04 | auth collection password·refresh·OTP·MFA | adapted | prior-run | PB의 RecordAuthResponse/JWT 대신 PgBase UserSession과 MFAChallengeError·opaque session token을 사용한다. 메일·MFA 메서드 배치와 refresh single-use 계약도 다르다. 외부 계정 관리/자동 refresh는 별도 행에서 평가한다. Dart는 소스·테스트 존재만 확인했다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RecordService.ts#L318-L400) |
| SDK-05 | OAuth browser helper | adapted | prior-run | 동일한 로그인 목적을 redirect/deeplink adapter로 구현한다. PB의 내장 popup 및 realtime callback 방법을 복제한 것은 아니며 host adapter 구현은 호출자 책임이다. 외부 OAuth provider E2E는 이번 검증에 포함되지 않았다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RecordService.ts#L549-L715) |
| SDK-06 | TypeScript AuthStore·지속 저장·SSR cookie | partial | executed-now | 실행 probe에서 storage로 새 AuthStore를 만들면 record=null이었다. AsyncAuthStore·storage event 동기화·public token/record onChange/fireImmediately가 없고 _onChange는 generation만 전달한다. SSR cookie는 secure/sameSite opt-in이며 만료/4096-byte 축소/전체 cookie 옵션이 없다. Opaque token의 isValid 차이는 설계 차이지만 동등한 expiry-aware state도 없다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/stores/BaseAuthStore.ts#L19-L238) |
| SDK-06-DART | Dart AuthStore·비동기 저장·SSR | partial | test-exists | token+record durable round-trip이 완성되지 않았다. TS와 달리 비동기 저장·직렬화는 구현되어 있으므로 둘을 동일한 'async 없음'으로 평가하면 안 된다. 공식 Dart에는 JS cookie helper와 같은 표면이 없고 PgBase cookie 지원은 추가 기능이다. [PB 원문](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/async_auth_store.dart#L34-L98) |
| SDK-07 | 요청 취소와 기본 duplicate auto-cancellation | partial | executed-now | 동일 records.list() 두 요청을 실행해 둘 다 signal=undefined임을 확인했다. 명시적 key 없는 send도 자동 취소되지 않는다. PB의 기본 서비스 중복 취소 계약은 불일치하며 existing keyed tests는 이 경로를 검사하지 않는다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/Client.ts#L498-L573) |
| SDK-08 | TypeScript 오류 normalization·본문 중단 | partial | executed-now | 실행 probe: 본문 수신 중 abort 시 records.get()은 undefined로 resolve, getJSON()은 AbortError reject. TS까지 cancellation/network/protocol/requestId 모두 정규화된다는 과거 설명은 사실이 아니다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/Client.ts#L451-L490) |
| SDK-08-DART | Dart 오류 normalization | adapted | test-exists | 기능상 HTTP/취소/네트워크 구분은 있으나 catch 타입과 메타데이터가 다르다. PgBase HTTP 오류는 PB의 전체 URL/response 보존 인터페이스가 아니다. Dart 실행 검증은 이번에 하지 않았다. [PB 원문](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/client_exception.dart#L1-L36) |
| SDK-09 | TypeScript filter placeholder helper 최신 JS 계약 | partial | executed-now | 공통 identifier key 테스트는 통과하지만 `filter('title={:my key}', {'my key':'exact'})`가 그대로 남는다. 최신 PB JS의 임의 key 계약과는 완전히 동등하지 않다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/Client.ts#L302-L344) |
| SDK-09-DART | Dart filter placeholder helper | equivalent | test-exists | 이 좁은 helper 계약은 소스와 대응 테스트가 일치한다. Dart 런타임을 이번 감사에서 실행했다는 뜻은 아니며 SDK 전체 drop-in 호환을 의미하지 않는다. [PB 원문](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/client.dart#L177-L207) |
| SDK-10 | realtime subscribe/unsubscribe·options·reconnect | partial | prior-run | subscription fields/filter/expand/custom headers, prefix unsubscribe와 공개 connection/disconnect hooks가 없다. 기본 reconnect budget도 PB JS 무한 재연결과 다르다. Dart도 동일 options/prefix gap; source/tests만 확인했다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RealtimeService.ts#L35-L215) |
| SDK-11 | transactional batch·multipart·upsert | adapted | prior-run | 요청 구성 목적은 제공하지만 fluent createBatch builder가 아니라 명시적 배열/file map이며 endpoint·envelope·ETag/idempotency 계약이 다르다. Batch FormData 자동 분해 및 여러 동일 key file 입력의 완전한 PB parity는 검증하지 않았다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/BatchService.ts#L23-L78) |
| SDK-12 | file URL/token·record multipart upload | partial | test-exists | 분리된 upload·token URL은 의도된 대안이지만 PB record create/update의 자동 FormData/file 변환과 filename 기반 공개 URL/다운로드 옵션은 제공하지 않는다. TS는 record FormData를 JSON.stringify하여 사용할 수 없다. 실제 파일 conformance는 이번 감사에서 재실행하지 않았다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/FileService.ts#L20-L70) |
| SDK-14 | Dart 관리자 세션 저장·동시 로그인 일관성 | adapted | test-exists | PB와 인증 모델이 의도적으로 다르다. Source/test existence는 확인했으나 이번 Dart runtime 결과는 없다. [PB 원문](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/client.dart#L40-L45) |
| SDK-X01 | auth record update/delete와 AuthStore 자동 동기화 | missing | static-only | 현재 인증 레코드를 수정·삭제해도 SDK cached auth.record가 자동으로 갱신/삭제되지 않는다. 별도 refresh/clear를 호출해야 한다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RecordService.ts#L256-L310) |
| SDK-X02 | JS request interceptors·공통 request options | partial | static-only | TS의 per-call fetch, after parsed-response hook, high-level custom query/headers가 없다. 전송 주입으로 일부를 수동 재구현할 수 있으나 PB hook contract와 동일하지 않다. 공식 Dart에도 JS와 같은 beforeSend/afterSend는 없으므로 이 gap은 JS 기준이다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/Client.ts#L65-L124) |
| SDK-X03 | getFullList·getFirstListItem·decode helpers | partial | test-exists | 전체 순회 목적은 adapted이며 first-match/404 편의 helper 및 decode customization은 빠져 있다. 호출자가 list+첫 항목 검사/iterator 수집을 작성해야 한다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/CrudService.ts#L12-L136) |
| SDK-X04 | JS linked external auth 관리 helpers | missing | static-only | JS public helper 대응이 없다. 공식 Dart도 dedicated deprecated helpers는 없으므로 Dart helper 누락으로 일반화하지 않는다. 서버에 외부 계정 관리가 있더라도 SDK 구현 증거로 계산하지 않았다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RecordService.ts#L1041-L1085) |
| SDK-X05 | HealthService.check | missing | static-only | SDK의 고수준 건강 상태 점검 helper가 누락된다. 서버 health endpoint 존재 여부와 분리한 판정이다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/HealthService.ts#L6-L26) |
| SDK-X06 | BackupService list/create/upload/delete/restore/download | missing | static-only | backupStorageSettings가 BackupService 기능 구현은 아니다. TS admin에는 공개 generic request도 없고 파일 업로드/download/restore helper가 양쪽에 없다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/BackupService.ts#L8-L131) |
| SDK-X07 | SQLService.run | missing | static-only | DB 운영 기능이 구현되어 있어도 SDK public SQL 서비스 대응은 누락이다. Dart generic request 수동 호출은 고수준 SDK parity가 아니다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/SQLService.ts#L4-L29) |
| SDK-X08 | SettingsService 전체 설정·메일 테스트 | partial | prior-run | 일반 설정 getAll/update와 SMTP testEmail 고수준 메서드는 없다. SDK settings 영역 전체를 완료로 볼 수 없다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/SettingsService.ts#L9-L123) |
| SDK-X09 | LogService 단건·통계·truncate | partial | static-only | 단건 로그·시간별 통계·truncate 및 PB filter 옵션 대응이 없다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/LogService.ts#L9-L95) |
| SDK-X10 | CollectionService metadata·scaffold typing | partial | static-only | getAllOAuth2Providers/dryRunViewQuery helper가 없다. 별도로 TS scaffolds() 반환 타입은 현재 자사 서버 구조와도 불일치하며 기존 테스트 fixture는 잘못된 단순 shape만 검사한다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/CollectionService.ts#L65-L121) |
| SDK-X11 | CronService 목록·실행 | adapted | prior-run | 함수명과 job metadata/queue 실행 계약이 다르지만 목록·실행 요청 목적은 제공한다. Dart는 이번 실행 검증 대상이 아니다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/CronService.ts#L8-L42) |
| SDK-X12 | JS superuser autoRefreshThreshold | missing | static-only | opaque native session 및 cookie admin에 맞는 자동 갱신 정책은 별도 설계가 필요하다. 현재 공개 SDK에 대응 기능은 없다. 공식 Dart에는 동일 JS superuser 자동 refresh 옵션이 없다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RecordService.ts#L374-L400) |
| SDK-X13 | 정밀 JSON 데이터 표현 | adapted | prior-run | PB와 달리 정밀 JSON I/O를 의도적으로 강화했다. TS batch/auth/realtime은 여전히 native parsing이고 Dart의 JsonValue wrapper는 공식 RecordModel과 source-compatible하지 않다. [PB 원문](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/Client.ts#L439-L472) |
| SDK-X14 | Dart RecordModel typed-path access | missing | static-only | exact JSON 모델은 의도된 표현 차이지만 PB get<T> nested-path/casting/default helper에 해당하는 편의 기능은 없다. [PB 원문](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/dtos/record_model.dart#L13-L105) |

## 실행 probe와 결과

현재 built SDK를 import하고 memory storage/fetch 및 표준 Response/ReadableStream을 주입했다. 외부 서버 요청·데이터 변경·코드 파일 작성 없이 실행했다.

첫 probe는 AuthStore.save 후 같은 storage로 새 store를 만들고, records.list 두 호출을 병렬 실행하고, filter의 공백 key를 검사했다.

```json
{
  "persistedValue": "opaque-token",
  "restoredRecord": null,
  "isValidForOpaqueToken": true,
  "duplicateServiceSignals": [null, null],
  "filterUnusualKey": "title={:my key}"
}
```

여기서 null은 signal이 undefined라는 probe 표기이다. opaque token을 nonempty로 판정하는 차이 자체를 JWT 검증 버그로 단정하지 않았다. 다만 API 이름 isValid가 expiry를 검사하지 않는다는 사실을 분리 기록했다.

두 번째 probe는 response stream에 첫 JSON 조각을 넣은 뒤 AbortSignal에서 AbortError로 stream을 중단했다.

```json
{
  "get": { "resolved": true, "value": "undefined" },
  "getJSON": { "resolved": false, "error": "AbortError" }
}
```

과거 keyed send/cookie/raw JSON 테스트 통과와 모순되지 않는다. 그 테스트가 default service key, durable record 복원, parsed-body interruption까지 모두 검사한 것은 아니다.

## 읽은 파일 범위

공식 코드는 `.local/pocketbase-comparison/js-sdk`, `.local/pocketbase-comparison/dart-sdk`의 위 pinned commit이다. 모든 파일을 동일 깊이로 정독했다는 주장은 하지 않는다.

- **JS 본문·해당 구현 범위 확인:** `src/index.ts`, `Client.ts`, `ClientResponseError.ts`; `stores/BaseAuthStore.ts`, `LocalAuthStore.ts`, `AsyncAuthStore.ts`; `services/CrudService.ts`, `RecordService.ts`, `RealtimeService.ts`, `FileService.ts`, `BatchService.ts`, `CollectionService.ts`, `SQLService.ts`; `tools/options.ts`, `formdata.ts`, `refresh.ts`. 추가로 `jwt.ts`/`cookie.ts` exports와 Settings/Logs/Health/Backups/Cron public method inventory를 확인했다.
- **Dart 본문·해당 구현 범위 확인:** `lib/pocketbase.dart`, `src/client.dart`, `auth_store.dart`, `async_auth_store.dart`, `client_exception.dart`, `services/base_crud_service.dart`, `record_service.dart`, `realtime_service.dart`. 나머지 settings/collection/log/health/backup/cron/sql/file/batch public methods 및 `dtos/record_model.dart` getters/path helper를 확인했다.
- **PgBase:** TS `src/index.ts`/`src/admin.ts`, README/package 및 runtime/public type tests; Dart `lib/pgbase.dart`, `src/auth.dart`, `client.dart`, `records.dart`, `files.dart`, `realtime.dart`, `transport.dart`, `errors.dart`, `exact_json.dart`, `admin.dart`, `test/sdk_test.dart`; `pgbase/sdk_integration_test.go`, `sdk_realtime_conformance_integration_test.go` 관련 경로; scaffold shape 확인을 위한 `internal/schema/scaffolds.go`.
- 테스트·구현에 명시되지 않은 모든 PB 옵션 조합, custom fetch platform, browser popup, 실제 OAuth provider, 실제 Dart 환경을 검증하지 않았다. 이 범위를 넘는 parity를 주장하지 않는다.
