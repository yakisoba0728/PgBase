# N6–N9 실행 계획

작성 기준: 2026-09-10, `edf55690599fc63966d3e99996f9954649961074`의 소스와 [next-work N6–N9](../../next-work.md#n6--jsvm). 계획 branch는 `codex/n6-n9-plan`, worktree는 `PgBase-worktrees/n6-n9/plan`이다. **현재 단계는 plan-only이며 제품 코드·DB·기존 설치를 변경하지 않는다.** 아래 체크박스는 앞으로 필요한 인수이며 아직 통과한 테스트가 아니다.

사용자는 N1–N5 병합·설치 후 N6/N7 병렬, 이어 N8/N9 병렬 진행을 승인했다. Rawls(Sol)가 N1–N5의 PR/required CI/merge/로컬 schema v40 설치와 중앙 문서를 소유한다. 이 계획의 존재는 그 gate의 완료 증거가 아니다. Astra는 조율·범위 판단만 맡으며 제품 구현·수정·검증을 맡지 않는다. 공개 npm/pub.dev/binary registry 및 release 게시도 이 범위 밖이다.

## 1. 시작 gate와 실행 순서

| 단계 | 실행과 담당 | 다음 단계로 넘길 증거 |
| --- | --- | --- |
| P — 현재 | Sol이 이 실행 계획과 [N6 상세 설계](../../workstreams/n6-n9/N6.md)를 별도 worktree에 작성·커밋 | 두 문서 커밋, 전체 완료기준 대응, 의존·제품 차이를 Rawls/Astra에 전달. 구현 완료 표시 금지 |
| G0 — 설치 후 배정 | 통합 Sol이 N1–N5 merge SHA, required CI, 설치 SHA/schema v40, 백업·doctor/readiness 결과를 확인하고 새 구현 base와 담당을 명시 | **설치 완료 + 통합 lead의 stage 수락·배정**. `edf5569`에서 제품 구현을 미리 시작하지 않음 |
| G1 — N6/N7 계약 | N6 Sol·N7 Terra가 배정된 독립 worktree에서 각 상태 문서의 concrete design/acceptance를 먼저 갱신. 통합 Sol이 공통 파일 계약 확정 | JS public Go facade/DTO, exact JSON, SDK error/cancellation, CLI/options 이름, 테스트 서비스 격리표 |
| G2 — N6/N7 구현·집중 검증 | 각 담당이 코드·문서·실제 앱 인수를 수행. 집중 테스트가 통과한 안정 커밋을 조기에 전달하고 독립 인수는 병행 | 커밋/파일/명령/exit/result/fixture 정리, 독립 Standards/Spec 검토 및 지적 해결 |
| G3 — 첫 통합 | Sol이 준비된 커밋을 통합하고 JS 앱 + Dart 실제 HTTP/Flutter 상호 인수, 관련 회귀 확인 | N6/N7 핵심 기준 완료, review 정리, 두 번째 병렬 작업의 고정 base. 완성되지 않은 binding을 N8/N9로 넘기지 않음 |
| G4 — N8/N9 | N8 Sol·N9 Terra가 새 독립 worktree에서 상세 설계부터 기록하고 구현·집중 테스트·실제 UI/native 인수 | SQL 인가·불변식·취소와 updater 서명·실제 교체·복구 증거, 독립 review |
| G5 — 최종 통합 | 통합 Sol이 전체 회귀·생성물·구조·지원 빌드·실제 browser/SDK/native acceptance 수행 | 최종 SHA에 대응한 증거와 제한. 로컬 구현/검증, 원격 병합, 기존 설치, 공개 배포 상태를 각각 구분 |

N6와 N8을 맡는 Sol 구현자와 통합 Sol 역할은 별도 task/worktree로 배정할 수 있다. 같은 담당이 통합을 수행하더라도 자기 코드의 독립 검토자로 계산하지 않는다. N6/N7 완료 전에 N8/N9 제품 코드를 앞당기지 않는다. 배정 이후의 routine product 선택은 상태 문서와 메시지로 기록하고 진행하며 Astra의 코딩을 기다리지 않는다.

## 2. 파일 소유와 충돌 경계

아래 새 경로·API 이름은 제안 계약이다. 기존 구현이 있다는 뜻이 아니다. production Go 파일 800줄 제한을 유지하고 거대 파일에 기능을 누적하지 않는다.

| 담당 | 단독 소유할 코드·기능 문서 | 상태 문서 |
| --- | --- | --- |
| N6 Sol | 신규 `internal/jsvm/`, `pgbase/javascript_*.go`, `pgbase/extension_*.go`, `internal/cli/javascript.go`, JS binding 선언/예제/집중 테스트, `docs/javascript.md` | [N6.md](../../workstreams/n6-n9/N6.md) |
| N7 Terra | 신규 `sdk/dart/`, Dart live conformance runner, `examples/dart/`, `examples/flutter/`, SDK 로컬 설치 문서 | `docs/workstreams/n6-n9/N7.md` — 별도 Terra plan worker 소유 |
| N8 Sol | 신규 `internal/sqlconsole/`, `internal/api/sql_console*.go`, `pgbase/sql_console*.go`, UI `pages/sql-console*`, UI 전용 backend adapter, SQL 콘솔 문서/테스트 | `docs/workstreams/n6-n9/N8.md` — 배정 시 생성 |
| N9 Terra | 신규 `internal/selfupdate/`, `internal/cli/update.go`, 버전/buildinfo module, 로컬 manifest/signing fixture 도구, updater 문서/테스트 | `docs/workstreams/n6-n9/N9.md` — 배정 시 생성 |
| 통합 Sol | `pgbase/options.go`, `app_construct.go`, app Start/Close composition, `internal/cli/run.go`·공통 config/env/help golden, 공통 API route registry·OpenAPI 생성물, `go.mod/go.sum`, UI app/nav/ports/http-backend·embedded build, 공통 CI/구조 예산, 시스템 migration 번호/등록 | 중앙 TODO·next-work·project-status·비교 문서·통합 상태 단독 소유 |

공유 seam 변경이 필요하면 담당이 자기 상태 파일에 **정확한 signature/DTO/파일/호출 시점/관련 테스트**를 쓰고 통합 lead에게 메시지를 보낸다. 통합 lead는 한 담당에게 해당 작은 공유 patch를 위임하거나 직접 적용한다. 승인 없는 두 worktree 동시 편집을 피하고, 필요한 공통 API는 안정 커밋으로 먼저 전달한다. 다른 담당의 worktree나 main에서 직접 수정하지 않는다.

- N6 옵션은 `Options.JavaScript`와 `--js-dir`, `PGBASE_JS_DIR`; 개발 명령은 `js check|types|serve --watch`. migration 기존 하위 명령에 같은 JS manifest source를 연결한다. 전체 합성은 통합 담당 소유다.
- N7는 HTTP 계약을 소비한다. 새 서버 endpoint를 Dart 편의 때문에 독자 추가하지 않는다. N6 custom route는 `/api` 등 예약 namespace를 침범하지 않으며 Dart `send` 확장 API의 인증도 client 종류를 따른다.
- N8 서버 경로는 제안 `/api/admin/sql/queries`, `/api/admin/sql/queries/{id}`와 해당 cancel 경로다. 오직 기존 admin session/origin/CSRF 체인에 등록한다. N9는 CLI 전용으로 HTTP 관리자 update endpoint를 만들지 않는다.
- N6/N7는 새 시스템 table이 필요하지 않은 설계다. N8 durable 실행 기록 또는 권한 설정 때문에 migration이 필요하면 **통합 lead가 설치된 최신 번호 다음을 배정**한다. 지금 41을 예약하지 않는다. N9의 파일 journal은 DB migration이 아니다.
- 시스템 migration **1–40 원본·checksum은 불변**이다. N6 JS 사용자 migration ID는 기존 Go 사용자 migration ID와 동일 ledger namespace이며 시스템 번호와 분리한다.
- 새 dependencies는 담당이 근거·정확한 버전/commit·라이선스·CGO/플랫폼 영향을 전달하고 통합 담당이 잠근다. engine의 `latest` 문자열이나 부동 Git ref를 제품 build에 남기지 않는다.

각 단계 전환, 안정 커밋, 테스트 결과 및 blocker 발생 시 자기 상태 문서를 먼저 갱신하고 lead에게 간결하게 보낸다. 남은 작업은 코드 수정/집중 검증/독립 인수/통합 의존으로 나눠 기록한다. 전체 suite 반복으로 인수나 handoff를 지연하지 않는다.

## 3. 현재 소스 대조와 채택 경계

| 기준 소스 | 확인한 현재 계약 | 이 계획에서의 사용·차이 |
| --- | --- | --- |
| [custom routes](../../../pgbase/custom_routes.go), [router](../../../internal/api/custom_routes.go), [설명](../../custom-routes.md) | Go `CustomRoute`, 명시적 public/user/admin/service admission, startup 등록, bounded buffered response | N6는 동일 router/인가에 JS handler를 연결. 임의 `/api` override·무제한 streaming route는 제공하지 않음 |
| [lifecycle registry](../../../internal/lifecycle/registry.go), [public hooks](../../../pgbase/lifecycle_hooks.go), [N5 대조](../../lifecycle-parity.md) | priority 순 Before veto/paired After, registry snapshot, After는 detached 최대 5초. payload마다 수정 범위가 다름 | N6는 typed Go adapters를 재사용. JS로 actor·idempotency·file identity·승인 schema checksum을 바꿀 수 없음 |
| [user migrations](../../../pgbase/user_migrations.go), [CLI](../../../internal/cli/user_migration.go), [문서](../../user-migrations.md) | `UserMigration`/동일 tx/Source checksum/history/safe history-sync 및 N1 Go source outbox 복구 | JS manifest를 같은 runner에 합성. 별도 ledger·serve 시 무조건 migration·PB식 누락 source 이력 삭제를 만들지 않음 |
| [jobs](../../../pgbase/jobs_options.go), [서비스](../../../pgbase/operations_services.go), [worker](../../../internal/jobs/service.go) | `@every`와 5-field cron/timezone, durable admin 수정 보존, 30초 handler/1분 lease, fenced at-least-once | JS도 동일 worker. `RunKey=jobID:fenceGeneration`은 시도 단위이며 논리 작업 전체의 exactly-once key가 아님 |
| [recordops](../../../internal/recordops/service.go), [query](../../../internal/query/service.go), [filestore](../../../internal/filestore/service.go) | 실서비스는 internal에 있고 범용 public CRUD/mail/file facade는 아직 없음 | N6에 좁은 Go facade가 선행되어야 함. app/pool/tx reflection이나 내부 HTTP loopback으로 대체하지 않음 |
| [TS SDK](../../../sdk/typescript/src/index.ts), [admin SDK](../../../sdk/typescript/src/admin.ts), [live harness](../../../pgbase/sdk_integration_test.go) | PgBase opaque sessions·UUID·정확 numeric, 실제 HTTP/SSE/file 검증. OpenAPI는 route 목록이지 완전한 payload schema가 아님 | N7가 메서드별 inventory를 만들고 실제 HTTP로 입증. PB wire 호환 및 route JSON만으로 SDK 자동 완성을 주장하지 않음 |
| [SQL View 검사](../../../internal/schema/sql_view_inspect.go), [UI backend](../../../ui/admin/src/backend/json.ts) | PG native parse/dependency 패턴, UUID id·unique 결과 검사가 View 전용. UI에는 lossless JSON 경로가 있음 | N8는 별도 query engine/result protocol. View 검사 전체 재사용이나 결과를 JS Number로 변환하지 않음 |
| [local artifacts](../../../scripts/build-local-artifacts.sh), [release 검증](../../../scripts/support/verify-release-attestation.sh) | 6개 OS/arch의 CGO=0 native 바이너리와 SHA256SUMS. 기존 cosign 검증은 container attestation용 | N9 native manifest/신뢰 root/파일 journal은 신규. container 공개키를 native artifact 서명 형식인 것처럼 사용하지 않음 |

PB 대조의 저장소 기준은 [재평가](../../pocketbase-current-reassessment.md)와 pin된 v0.40.3 소스다. 현재 PB 웹 문서는 2026-09-10에 별도로 확인했으며 향후 웹 변화와 저장소 pin을 혼동하지 않는다. JS는 Goja·CommonJS를 채택하지만 PB의 모든 `$app/$os` API, handler 직렬화 scope, 기본 자동 migration/자동 기록 및 history-sync 의미까지 복제하지 않는다. [PB JS overview](https://pocketbase.io/docs/js-overview/), [PB JS migrations](https://pocketbase.io/docs/js-migrations/).

기존 [local-jobs](../../local-jobs.md)의 interval-only 설명과 [extension scope](../../extensions-and-client-scope.md)의 과거 JS/Dart deferred 표시는 최종 구현 후 통합 담당이 실제 증거에 맞게 갱신한다. 이번 계획 커밋에서는 과거 상태나 중앙 완료 표시를 바꾸지 않는다.

## 4. N6 — JSVM (Sol)

구체 engine/module/API/Go hook/취소/권한 계약과 전체 인수는 [N6.md](../../workstreams/n6-n9/N6.md)가 단일 상세 설계다. 주요 선택은 pure-Go Goja, confined CommonJS snapshot, 호출마다 새 VM, typed PgBase host bindings, 명시적 사용자 migration, opt-in 개발 restart reload다. 신뢰된 로컬 애플리케이션 코드용이며 적대적 multi-tenant sandbox나 VM별 hard heap cap을 주장하지 않는다.

- [ ] **N6.1** 지원 문법·CommonJS 경로/순서/순환·`.d.ts`·외부 compile·개발 reload·PB 차이를 문서와 fixture로 입증한다.
- [ ] **N6.2** 실제 Go service 기반 route/hook/migration/job/records·mail·file 바인딩을 외부 Go module과 설치 바이너리의 JS 앱에서 실행한다. 필요한 Go facade가 없다는 이유로 binding을 생략하지 않는다.
- [ ] **N6.3** 취소/timeout/동시 admission/중첩 hook/공유 상태/Close/오류·quota를 검증한다. JS interrupt와 context-aware Go host 호출의 한계를 구분한다.
- [ ] **N6.4** 같은 앱에서 CRUD+N4 field options/정확 숫자·관계·파일, 메일 hook/SMTP fixture, durable job retry, JS migration Up/Down·checksum·실패 rollback을 입증한다. 소스 load 성공만으로 완료하지 않는다.

Goja는 pure Go이며 runtime을 여러 goroutine에서 동시에 사용하지 않는다. `Interrupt`는 실행 중인 native Go 함수를 강제로 중단하지 않는다. 이 사실 때문에 VM 재사용 pool 대신 새 VM·명시적 host deadline·입장 제한을 택한다. 정확한 dependency pseudo-version/commit은 G1에서 pin한다. [Goja README](https://github.com/dop251/goja), [Goja API](https://pkg.go.dev/github.com/dop251/goja).

## 5. N7 — Dart SDK (Terra)

N7 상세 SDK/runtime 설계는 별도 `n6-n9/n7` worktree의 Terra 담당이 `docs/workstreams/n6-n9/N7.md`에 기록한다. 아래는 전체 단계의 필수 acceptance와 공통 API 경계이며 상세 module/transport 선택의 중복 설계 문서가 아니다. 담당이 확정한 상세 계약을 통합 lead에게 전달하면 이 계획의 교차 기능 인수와 대조한다.

`sdk/dart`에 `publish_to: none`, `0.1.0-local` package를 만들고 public `PgBaseClient`, `PgBaseAdminClient`, `AuthStore`, `PgBaseException`, `CancellationToken`, lossless value/record DTO를 제공한다. package `http`의 native/browser Client 및 abortable streaming request를 감싼다. 조회가 아닌 mutation을 transport가 자동 재시도하지 않는다. [Dart http 공식 package](https://pub.dev/packages/http).

| 인수 ID | 구현 계약 | 필요한 실제 증거 |
| --- | --- | --- |
| N7.1 | MemoryAuthStore 기본, 비동기 persistence interface 및 사용자 secure-storage 예제. 로그인 결과와 logout의 generation race 방지. user token과 admin cookie/CSRF 분리. UUID는 문자열 검증, `numeric`/`int8`/SSE seq/JSONB 큰 수는 exact lexical wrapper·BigInt 경로, null과 omitted 구분. error는 status/code/details/request ID와 network/cancel/invalid-body 구분 | Dart VM과 Flutter web 모두 큰 정수/소수 roundtrip. 로그아웃 후 지연 login이 token을 복구하지 않음. auth store 교체·cancel completion·오류 복호화 테스트 |
| N7.2 | TS public 메서드 inventory를 baseline으로 auth realm/password/session/OTP/MFA/TOTP/reset/verification/email change/OAuth callback, CRUD/filter AST/sort/pagination/ETag/idempotency, 두 Batch 형식·multipart, protected file upload/download/delete/transform, SSE subscribe/replay/reconnect 제공 | 실제 서버 auth·권한·stale ETag·같은 key 재시도/다른 body 충돌·Batch rollback·파일 hash·SSE cursor 비교. `_request` 노출만으로 개별 기능 구현을 대신하지 않음 |
| N7.2-admin | admin login/resume/logout·catalog·guarded plan/apply/operation·lossless import/export, jobs list/configure/queue, logs, file/backup storage 설정·probe·file migration 상태/제어, impersonation을 최소 관리 API로 구현. N3 OTP challenge/IP 거절을 처리. 반환 redaction 보존 | native 명시적 cookie jar와 browser same-origin credential 모드 모두 admin session+CSRF/401·403. preview 이후 입력 수정에도 원래 승인 body/checksum을 적용. 저장소 비밀 반환 없음 |
| N7.3 | cancel은 pending send/response body/file/SSE를 끝내며 사용자 소유 Client를 임의 close하지 않음. SSE multiline frame/chunk split/heartbeat/큰 ID, bounded exponential backoff+jitter, cursor 오류·인증 만료는 서버 code별 terminal 처리. logout은 즉시 stream/adoption 중단 후 원격 revoke 결과 전달 | PgBase HTTP를 실제로 종료·재시작해 replay/reconnect, logout 중 reconnect 차단, 취소된 업로드 정리, 다운로드 해시, HTTP error envelope. transport mock만의 통과 금지 |
| N7.4 | 로컬 path 설치 문서, repo 외부 directory에서 package 소비하는 Dart CLI와 Flutter UI 앱. native/browser 의존을 conditional import로 분리. OAuth/SMTP는 로컬 provider fixture로 실행하고 외부 provider 인수와 구분 | `dart analyze`, `dart test`, 외부 Dart 실행, Flutter test/build와 **실제 Flutter browser 조작**. 로컬 SDK 설치·인증·CRUD·파일·realtime가 연결된 실행 로그/스크린 증거. pub.dev 게시 없음 |

정확 JSON은 `jsonDecode` 후 이미 손상된 Number를 문자열화하는 방식으로 해결하지 않는다. raw numeric token을 보존하는 codec을 SDK 경계에서 사용하고 encoder에서 숫자 token과 문자열을 구별한다. TS의 plain `number` 타입을 그대로 Dart web 안전성 근거로 삼지 않는다. 상태 문서에는 서버 endpoint/TS 메서드/Dart 메서드/인수 ID 행을 만들어 항목 누락을 검토한다. OpenAPI route 목록은 인벤토리 보조 자료다.

Flutter 예제는 SDK 외부 소비자가 [Dart path dependency](https://dart.dev/tools/pub/dependencies#path-packages)로 설치하는 흐름까지 포함한다. N6 custom route를 호출하여 동일 record/file/SSE를 관찰하는 통합 사례를 만든다. browser OAuth callback과 admin 쿠키는 정상 same-origin 흐름을 사용하고 CORS/CSRF를 끄지 않는다.

## 6. N8 — 관리자 SQL 콘솔 (Sol)

**기본 read-only + 명시적으로 활성화한 제한된 transactional write**를 채택한다. 기존 record writer는 revision·authorization·audit·outbox를 함께 보장하므로 PgBase 관리 table에 직접 DML을 허용하면 같은 불변식을 보장할 수 없다. 이는 모든 SQL write를 없애야 한다는 근거는 아니다. 쓰기는 운영자가 설정에 등록하고 사용자 migration으로 만든 **확장 소유 base table**에 한정한다. PgBase 관리 collection/record/system metadata/ledger는 guarded schema·record API와 사용자 migration을 계속 사용한다. SQL Console의 이 차이를 UI·문서·reject 오류에 명시한다.

확장 table은 `app`/PgBase 제어 namespace와 분리된 namespace와 명시 table allowlist를 사용한다. Console에서 DDL/권한/trigger/function을 생성하지 않는다. 쓰기 대상의 default/generated expression·trigger/rule/RLS policy·foreign key/cascade·함수 의존을 검사하여 PgBase 소유 객체나 허용되지 않은 효과에 도달하는 table은 write scope에서 제외한다. 단순 schema 이름만 보고 허용하지 않는다. 관리자용 허용 collection 읽기는 user row-rule 필터를 적용하는 public 조회와 다르며 password/secret/control ledger 컬럼은 읽기에서도 노출하지 않는다.

| 인수 ID | 구현 계약 | 검증 |
| --- | --- | --- |
| N8.1 | read 모드는 단일 SELECT/VALUES 및 읽기 SELECT CTE·join·subquery·group/order/limit의 제한 문법. write 모드는 명시 allowlist의 확장 table에 INSERT/UPDATE/DELETE·RETURNING, bound params, 최대 10개 statement 배열을 하나의 transaction으로 실행. DML CTE, SELECT INTO, CALL/DO/COPY/DDL/SET/직접 BEGIN·COMMIT·ROLLBACK/SQL 문자열의 다중 statement·임의 함수/cast는 거절 | read/write grammar 정상·우회 corpus, extension table 여러 DML commit/두 번째 실패 전체 rollback, managed table write·trigger/cascade 우회 거절. 단순 prefix/regex 검사 금지 |
| N8.2 | 기본 disabled 옵션, 활성화해도 read-only가 기본이며 operator의 write scope 설정과 요청의 write mode가 모두 필요. 기존 admin auth+origin+CSRF, DB credential은 서버 옵션에만 보관. 전용 non-superuser read role/pool 및 제한된 extension writer role, startup 권한 검사 fail closed. session별 2개/global 4개 실행, 대기 16개/250ms, SQL 총 64KiB, timeout 기본 5초·최대 30초, lock_timeout 1초, 결과 1000행·128열·4MiB, write affected rows 총 1000 hard cap | anonymous/user/service 거절·다른 admin session 결과/cancel 접근 거절, read 모드 DML 거절·write scope 밖 table 거절, limit·DB lock·대기 cancel·disconnect·pool 회수. write affected/result cap 초과는 commit 전 rollback |
| N8.2-log | POST 생성 시 random execution ID, GET 상태/결과, POST cancel. pending/running/succeeded/failed/canceled/timed_out/unknown 상태와 bounded result TTL 5분. durable 실행 log에는 actor ID·fingerprint·시간·행/byte·상태·SQLSTATE만 저장, raw SQL/param/result 비저장. write receipt는 digest·commit 여부·bounded 비민감 결과 요약을 보관 | 성공/거절/취소/재시작 abandoned 또는 commit-unknown의 구분, 보존 정책·조회 권한, 결과 만료 응답, 시작 log 실패 시 미실행. receipt 보존 기한 뒤 mutation 자동 재실행 금지. 새 migration 번호 요청 |
| N8.3 | 독립 SQL 페이지: 기본 read·명시 write transaction 선택, 허용 write 대상 표시, 실행/취소·dry-run rollback, column name/type/nullable value, error position, elapsed/rows/bytes/truncated·commit 상태, bounded 결과 탐색·복사. exact numeric/JSONB는 lossless codec | 실제 browser long query 취소·오류 수정·다음 query 성공, BigInt/decimal 복사, write 여러 statement 성공·실패/dry-run rollback·취소 후 최종 상태 확인. UI 테이블 렌더만으로 완료 금지 |
| N8.4 | read는 종료 시 rollback; write는 전체 검증/실행/result cap 확인 뒤 단 한 번 commit, dry-run·오류·취소는 rollback. write execution ID와 idempotency digest의 receipt를 같은 transaction에 남기고 commit 연결 유실은 새 connection으로 확인, 미확정은 unknown으로 표시. 타 session backend를 ID만으로 종료하지 못함 | extension data의 성공 commit/후속 statement 실패·lock·timeout·disconnect·Close rollback, commit 직후 응답 유실/동일 key replay. 관리 schema/catalog/version·record revision·audit/outbox는 전후 보존, idle tx/temp object/conn 누수 없음 |

구현 보안 gate: read-only transaction 하나만으로 임의 SQL 함수의 외부 효과나 secret read를 막는다고 가정하지 않는다. [PostgreSQL read-only transaction](https://www.postgresql.org/docs/current/sql-set-transaction.html). 허용 문법을 AST로 제한하고 식/호출/타입을 재귀 검사한 뒤, 기존 SQL View의 **PG native parse + dependency inspection 패턴**을 별도 검사 모듈에 적용한다. 허용 builtin은 명시적 목록이며 `pg_catalog` 또는 non-volatile이라는 이유만으로 모두 허용하지 않는다. implicit casts·operator 구현·aggregate/window 함수·view의 전이 의존도 검사한다. 검사 전 실행될 수 있는 사용자 정의 type input 함수도 제한 문법과 role 권한으로 차단한다.

읽기 compile이 temp view를 필요로 하면 전용 connection의 제한된 compile transaction과 read-only execution transaction을 구분하고 각 실패/반환 시 temp object를 제거한다. write AST는 table/column/전체 부수효과 의존을 먼저 검증하며 temp view 검사로 DML 검증을 대신하지 않는다. persistent CREATE 권한·BYPASSRLS·role inheritance/SET ROLE escalation·유해 extension 함수 실행 권한을 허용하지 않는다. writer는 허용 extension table과 runner의 고정 receipt SQL에 필요한 최소 권한만 받고 사용자 SQL에서는 receipt/log table 접근을 차단한다. receipt/log의 신규 schema는 통합 lead가 번호를 배정한다. metadata/DDL과의 race는 relation locks/catalog version 재검증으로 다룬다. 안전성을 입증할 수 없는 표현식·지원 PG 버전은 실행 전에 명시적으로 거절하며, 이 제한을 전체 write 인수의 생략 근거로 삼지 않는다.

write 요청의 각 statement는 개별 extended-protocol 실행으로 보내고 server가 transaction을 소유한다. cross-request 열린 transaction은 제공하지 않는다. client cancel이 commit과 경합할 때는 이미 committed인 결과를 canceled/rolled back으로 표시하지 않는다. 동일 실행 ID/같은 digest 재조회·재전송은 receipt를 돌려주고 다른 body는 충돌이다. DB serialization/deadlock 실패를 transport가 자동 mutation 재실행하지 않는다. receipt는 console transaction 결과 이력이며 PgBase record mutation audit/outbox에 가짜 record event를 만들지 않는다.

기존 `InspectSQLView`의 UUID id/유일성 확인은 임의 query에 맞지 않으며 결과 전체를 미리 읽을 수 있으므로 그대로 호출하지 않는다. PG extended protocol 단일 statement 실행을 유지한다. [PostgreSQL CREATE VIEW](https://www.postgresql.org/docs/18/sql-createview.html), [extended query 한계](https://www.postgresql.org/docs/18/libpq-exec.html). 일반 parser 채택이 더 적절하다면 코드 작성 전 정확한 dialect·pure-Go 지원·transitive dependency를 기록한다. `pg_query_go`의 CGO 요구 때문에 현재 6종 `CGO_ENABLED=0` 배포를 조용히 깨면 안 된다. [pg_query_go](https://github.com/pganalyze/pg_query_go).

## 7. N9 — self-update (Terra)

기본 trust는 배포자가 별도 전달한 Ed25519 공개키이며 native updater 전용이다. manifest v1은 배포 version별 commit·platform artifact 목록(GOOS/GOARCH/location/size/SHA256), 최소 updater·지원 schema 범위를 포함한다. **서명한 정확한 manifest bytes** 확인 후 현재 version과 명시 version/channel 정책에 맞는 최신 eligible version 및 해당 platform artifact를 선택하고 실제 다운로드하여 size/hash를 확인한다. 제공된 local file만 검증하는 도구로 범위를 축소하지 않는다. 다운로드한 manifest 안의 새 공개키를 그대로 trust root로 채택하지 않는다. 검증은 Go 표준 라이브러리 [crypto/ed25519](https://pkg.go.dev/crypto/ed25519)를 사용한다.

| 인수 ID | 구현 계약 | 실제 검증 |
| --- | --- | --- |
| N9.1 | `version --json`, `update check|apply|status|rollback`. 명시적 local manifest/artifact 및 HTTPS download source 지원. fixture loopback HTTP는 명시적으로 허용. current/requested/latest eligible version·prerelease/downgrade 정책, GOOS/GOARCH 선택, no-update/unsupported-platform 구분. download byte/deadline/redirect origin cap과 검증 후에만 candidate 실행 | 여러 version/platform을 담은 signed manifest로 선택·잘못된 key/signature/hash/platform·redirect·downgrade 거절, local TLS fixture의 실제 manifest/artifact HTTP download·중단·오류. old/new native 바이너리의 hash/권한/실제 버전 출력 |
| N9.1-swap | 같은 directory 임시 파일, regular-file/symlink 검증, fsync, 기존 mode 보존, 검증된 backup, 원자적 rename, directory fsync, phase journal. 프로세스 간 updater lock으로 중복 apply 방지 | 현재 파일 보존·old/new version 실행 출력·동시 update·파일 교체 race·journal crash point를 확인. update 성공을 다운로드 성공으로 계산하지 않음 |
| N9.2 | interrupted download·no-space/permission·검증 실패는 설치 전 종료. 교체 후 candidate 실행 실패/명시적 readiness 실패는 검증된 old 파일로 복구. recovery는 journal phase마다 idempotent하며 backup 변조 시 fail closed | 임시 설치 directory에서 download 중단, bad executable, nonzero version probe, rename 뒤 강제 종료, backup 변조, 쓰기 불가, 반복 recovery. 새 파일 경로의 실제 실행 후 실패 복원된 old version 재실행 |
| N9.3 | updater 자체는 DB migration/Down을 자동 실행하지 않음. 기존 server 실행 중이면 명시적 관리 restart 절차 없이는 apply 거절. signed compatibility range와 `doctor` 결과를 사용해 migration 필요 상태를 별도 표시 | 호환 old→new는 파일 교체 후 별도 시작/health/doctor. schema 변경 필요 시 stop→검증된 DB/files backup→검증 candidate의 명시 migration→binary activate→start/doctor; 실패 시 DB 복구와 파일 rollback을 구분하고 무조건 binary-only 복귀 금지 |
| N9.4 | help/JSON status/단계별 progress/안정 error code·exit, 로컬 test key 생성·manifest 서명·설치/update/rollback 예제. trust key 보관·rotation은 운영자가 명시적으로 수행 | 네트워크/DB 없는 `version`·help/check, local signed artifact 설치 재현, 오류에 URL credential·private key 노출 없음. 공개 release 게시 없음 |

지원 범위는 Unix same-filesystem atomic replace와 Windows의 실행 중 파일 잠금을 분리한다. Windows는 exit 후 동작하는 전용 helper/journal 방식을 구현하고 같은 서명·현재 파일 보존·복구 계약을 적용한다. native 인수는 현재 macOS host에서 반드시 수행하고, Linux/Windows는 6종 build와 플랫폼별 단위/fixture 결과를 별도 기재한다. 모든 OS에서 실제 update를 실행했다고 주장하지 않는다. Windows 경로에서 단순 `os.Rename` 성공을 가정하는 공통 구현은 허용하지 않는다.

DB schema와 파일 교체는 하나의 transaction이 아니다. 새 schema가 old binary와 호환되지 않으면 binary rollback만으로 복구를 완료 처리하지 않는다. 로컬 인수는 disposable DB와 install dir에 한정하며 현재 사용자의 PgBase 프로세스·실행 파일·서비스 manager를 updater 테스트로 변경하지 않는다.

## 8. 검증 분담·실제 인수·독립 review

각 담당은 자기 focused unit/race 및 해당 PostgreSQL integration을 소유한다. 기본 PG17/PG18 matrix는 통합 담당이 배정하며 작업마다 고유 DB/role/port/temp dir를 사용한다. SMTP capture·OAuth provider·MinIO·HTTP artifact fixture도 격리한다. main `.local`의 provisioning helper는 필요한 부분만 읽고 credential을 로그나 문서에 복사하지 않는다. 기존 사용자 DB/서버/파일은 사용하지 않는다.

| 검증 | 담당과 완료 증거 |
| --- | --- |
| N6 집중 | loader/VM/host unit+races, Go hooks/recordops/user migration/jobs/filestore 실제 DB, 외부 Go module + 설치 JS 앱 HTTP/browser. 정확 명령·테스트 이름·DB major·binary SHA·source fixture revision |
| N7 집중 | Dart analyze/test + actual PgBase conformance, 외부 local package CLI, Flutter 실제 browser, auth reconnect/file cancellation. Dart/Flutter/브라우저 버전·서버 SHA |
| N8 집중 | SQL grammar/privileges/cancel/result codec, 격리 PG17/18 read·extension write commit/rollback·receipt·lock·invariant assertions, 관리자 browser 실행/취소/복사. UI unit/typecheck 관련 부분 |
| N9 집중 | signer/manifest/downloader/journal/replace/recovery unit+races 및 native old/new executable 설치 실험. OS/arch·SHA·mode·각 fault phase 결과 |
| 독립 Standards | 통합 lead가 작성자가 아닌 Sol/Terra 검토자를 고정 base→후보 SHA로 배정. repo 정책, 800줄/module 경계, secret handling, migration 불변, error/cleanup, generated docs/build 검토 |
| 독립 Spec | 별도 시각으로 이 문서 N6.1–N9.4와 N6 상세 A항목을 소스·실제 결과와 대조. 빠진 acceptance·PB 차이·실제 실행 증거·취소/복구 경계를 검토 |
| 최종 전체 회귀 | **통합 Sol만** `scripts/test-go-unit.sh`, `scripts/test-go-integration.sh`의 skip 정책을 포함한 Go/race/PG+MinIO matrix, lint/vet, UI unit/typecheck/build/e2e, TS SDK live conformance/pack, Dart/Flutter, OpenAPI/내장 생성물 재현·구조 실측·6종 build 실행 |

review는 발견사항 없음/수정 커밋/재검증 결과를 남겨야 끝난다. P0/P1 및 acceptance를 깨는 P2는 해결 후 다시 확인한다. 수정하지 않는 지적은 근거와 제품 영향, 담당·후속 조건을 명시하며 필수 완료기준을 후속으로 밀어 완료 처리하지 않는다. 제품 diff가 없는 현재 계획 작성은 문서 완전성·링크·diff만 검사하며 위 suite를 실행한 것으로 기록하지 않는다.

최종 통합의 실제 시나리오는 다음 순서로 재현한다.

1. 새 DB에 Go/JS user migration으로 collection/관계/options/View/index를 만들고 설치된 바이너리에서 JS app을 시작한다. N1 Go source 자동 기록·history/recovery가 계속 동작함을 교차 확인한다.
2. repo 외부 Dart 앱과 Flutter browser에서 로그인→N6 확장 CRUD→exact numbers/ETag→Batch/file→SSE를 실행한다. JS hooks의 veto/commit, SMTP capture, job 실패→재시도를 같은 DB 증거로 연결한다.
3. 관리자 browser의 SQL 콘솔로 결과를 읽고 정확 숫자를 복사한다. 허용 extension table의 여러 DML을 commit하고 오류/dry-run/취소 rollback과 응답 유실 receipt를 확인한다. 관리 table write 우회 실패 후 record/audit/outbox/catalog가 보존됐음을 확인한다.
4. 별도 temp installation에서 서명된 old→new native update와 실패 복원을 실제 실행한다. 같은 계열의 disposable DB에서 compatibility gate와 명시 migration 순서를 검증한다.
5. 통합 lead가 최종 커밋에 대응한 전체 회귀와 독립 review를 묶어 보고한다. PR/merge/기존 설치는 그 단계의 별도 승인·소유권·증거를 따르며 public distribution은 계속 제외한다.

## 9. 의존·위험과 즉시 보고 조건

| 사항 | 처리와 보고 기준 |
| --- | --- |
| N1–N5 설치 gate | Rawls의 완료 메시지와 다음 배정 없이는 제품 구현 대기. 계획 작업은 진행 가능 |
| N6 in-process engine | hostile code·hard per-VM memory isolation 비지원. trusted JS라도 host call이 context를 무시하면 강제 종료 불가. quota/deadline으로 보장 가능한 범위와 subprocess stress 결과를 구분 |
| 공통 Go facade | public CRUD/mail/file 부재는 구체 선행 작업이다. existing hook/authorization semantics를 우회해야만 구현된다면 우회 대신 바로 lead에게 blocker 보고 |
| SQL 보안 | 허용 grammar·type input·view dependency·role privilege를 입증하지 못하면 N8 enabled 상태를 출하하지 않음. unsupported 범위는 명시하고 핵심 콘솔 인수는 계속 구현 |
| SDK 도구·native 플랫폼 | Dart/Flutter 또는 native OS가 없으면 즉시 환경 의존을 보고하고 설치 가능한 로컬 도구로 진행. build-only와 실제 실행 구분, mock으로 live acceptance 대체 금지 |
| 서명/DB 호환 | local test key만 사용하고 공개 채널 신뢰를 주장하지 않음. DB migration 이후 old binary 복원 가능 여부는 실제 schema range/backup 증거로 판단 |

완료 보고 형식: `phase / stable commits / changed files / exact tests and artifacts / remaining acceptance / blockers and limitations`. 통합 담당은 이 정보를 중앙 TODO에 반영한다. 계획 작성자는 두 할당 문서 밖의 공유 현황을 수정하지 않는다.
