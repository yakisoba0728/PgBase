# PocketBase v0.40.4 대조: extensions / migrations

비교일: 2026-09-22. 기존 EXT-01~18, MIG-01~08을 실제 구현 기준으로 다시 판정하고 신규 EXT-19~22, MIG-09를 추가했다. 총 31행: adapted 15, partial 13, missing 3, equivalent 0, unverified 0. 이 숫자는 기능 행 단위이며 중복된 원인(예: e.Next와 template)이 여러 복합 행에 걸쳐 있으므로 독립 결함 수로 읽으면 안 된다.

PocketBase 기준은 로컬 공식 저장소의 태그 **v0.40.4**, commit **5cec579da984436a258602a46a96302fbd31f77c**이다. PgBase는 **C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase**, 확인 시 HEAD **6993744a8f36db48ddfeaa8dae017656752d24c7** 및 이미 존재한 uncommitted 변경을 포함한 파일이다. 과거 readonly origin/main snapshot을 이번 판정 기준으로 사용하지 않았다.

기계 판독 결과: [extensions-migrations.json](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/extensions-migrations.json). 기존 행 이름은 [기존 비교 JSON](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/pocketbase-feature-comparison-current-2026-09-11.json)에서 가져왔으며 기존 완료 판정은 증거로 사용하지 않았다.

## 판정과 검증 범위

- equivalent: 이 좁은 PB 계약이 구현되고 관련 테스트가 존재하는 경우. 단지 동일 메서드 이름이 있다는 이유로 부여하지 않았다.
- adapted: 기능 의도는 구현됐지만 저장소·runtime·권한·공개 API가 의도적으로 다르다. PB 코드를 수정 없이 실행한다는 뜻은 아니다.
- partial: 실제 기능이 있으나 PB 동작 일부가 빠졌거나 근사한다.
- missing: 해당 공개 기능/진입점이 없거나 동작하지 않는다.
- verification의 test-exists는 관련 테스트 코드를 확인했다는 뜻이다. **이번 조사에서는 테스트, 빌드, Docker 변경/재시작을 실행하지 않았다.** 현재 dirty tree에서 테스트가 통과했다는 주장도 하지 않는다. static-only는 주로 부재 판정이다. 아래 testSource는 지원되는 하위 동작의 테스트이며 누락된 동작의 통과 증거가 아니다.

## 판정에 영향을 주는 주요 차이

1. **Hook continuation은 미구현이다.** PB는 다음 handler와 기본 작업을 e.Next 안에서 호출하고 반환을 기다린다. PgBase JS는 next를 호출하면 bool만 바꾼 뒤 이를 버린다. 에러 veto와 우선순위 실행은 있지만 before → next → after의 중첩 제어가 없다. [tools/hook/hook.go:146](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/hook/hook.go#L146), [internal/jsvm/registration.go:221](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:221).
2. **일반 Model ORM과 tx 완료 callback은 record 전용 처리와 구분해야 한다.** PgBase record mutation의 최종 commit/rollback 결과 훅은 구현돼 있다. 그러나 RunModelMutation은 임의 콜백 wrapper이고 PB Model의 DBExporter/PreValidator/PostValidator를 갖춘 일반 ORM은 아니다. public TxInfo.OnComplete도 없다. [core/db.go:32](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/db.go#L32), [core/db_tx.go:79](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/db_tx.go#L79), [pgbase/model_lifecycle.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle.go:30), [pgbase/extension_app_transaction.go:84](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_transaction.go:84).
3. **중첩 transaction 재사용은 PB와 같다.** PB core/db_tx.go는 이미 dbx.Tx이면 fn(app)을 직접 호출한다. PgBase JS runInTransaction도 같은 facade/transaction을 재사용한다. 따라서 JS savepoint 부재를 PB 격차로 세지 않았다. PgBase Go TrustedDatabase의 중첩 pgx savepoint는 별도의 추가 기능이다. [core/db_tx.go:25](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/db_tx.go#L25), [internal/jsvm/pocketbase_globals.go:446](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:446), [pgbase/trusted_database.go:82](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/trusted_database.go:82).
4. **S3는 미지원 placeholder가 아니다.** 초기 호환 객체 뒤에 installPocketBaseFilesystemGlobals가 실행돼 최종 s3를 대체한다. 실제 configured S3/object storage 작업과 MinIO 테스트는 있다. 다만 bucket/endpoint/credentials 등 생성자 인자를 전부 무시하므로 PB의 독립 S3 client 생성과 동등하지 않다. system.setContext는 no-op, system.list는 100개/no cursor다. 파일 담당 행과 중복 신규 행은 만들지 않았다. [tools/filesystem/filesystem.go:66](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L66), [internal/jsvm/pocketbase_globals.go:115](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:115), [internal/jsvm/pocketbase_filesystem_globals.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_filesystem_globals.go:12), [pgbase/javascript_compatibility_conformance_integration_test.go:61](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_compatibility_conformance_integration_test.go:61).
5. **Migration 원자성의 단위가 다르다.** PB는 Up/Down 한 호출의 모든 대상 migration을 transaction 안에 둔다. PgBase는 migration 하나마다 commit한다. M1 성공 후 M2 실패 시 PgBase M1은 남는다. 단일 migration 내 DDL/ledger rollback이 구현된 사실은 이 차이를 없애지 않는다. [core/migrations_runner.go:129](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_runner.go#L129), [core/migrations_runner.go:191](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_runner.go#L191), [internal/usermigrate/runner.go:249](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:249), [internal/usermigrate/runner.go:276](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:276), [internal/usermigrate/runner.go:300](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:300).
6. **신규 bootstrap hook의 정확한 이름은 OnBootstrapClear다.** ClearBootstrap / OnBootstrapClear는 v0.40.4 실제 App/BaseApp에 있다. OnClearBootstrap은 실제 이름이 아니다. PgBase의 이전 resetBootstrapState operator callback을 새 훅 구현으로 세지 않았다. [core/base.go:475](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go#L475), [core/base.go:880](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go#L880), [internal/jsvm/pocketbase_globals.go:463](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:463).
7. **Goja의 한계를 PB 격차로 자동 계산하지 않았다.** PgBase가 Promise/thenable 결과를 거부하는 사실은 확인했다. 그러나 이번 PB 소스 대조에서 Promise async event loop를 PB 기능으로 확인하지 않았으므로 이를 PB 대비 missing 행으로 추가하지 않았다. per-invocation fresh VM·명시적 capability·manifest는 PgBase의 trusted in-process 실행 계약이다. native Windows release/공개 릴리스 검증의 성공 여부도 이번 정적 조사로 확정하지 않는다. [internal/jsvm/runtime.go:453](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime.go:453), [plugins/jsvm/jsvm.go:196](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/jsvm.go#L196).

## 전체 행

| ID | 기능 | 판정 | 우선순위 | 핵심 판단 |
| --- | --- | --- | --- | --- |
| EXT-01 | 애플리케이션 임베딩과 시작 수명주기 | adapted | none | 좁은 시작·종료 기능은 구현됨. PB의 ClearBootstrap/OnBootstrapClear는 별도 EXT-19 공백이다. |
| EXT-02 | 사용자 정의 HTTP 라우트 | adapted | none | 라우트 기능은 구현됨. 시작 시 등록과 net/http API는 의도적 차이이며 JS RequestEvent의 누락은 EXT-22로 분리한다. |
| EXT-03 | 라우트 그룹·미들웨어·스트리밍 응답 | adapted | none | Go 라우팅 기능 의도는 충족. PB router.Event API 호환을 의미하지 않으며 JS 임의 stream/flush는 EXT-22 범위다. |
| EXT-04 | 프로그래밍 방식 UIExtension 등록 | adapted | none | 등록·서빙 의도는 구현됨. PgBase 관리자 UI 경로와 배포 계약을 따른다. |
| EXT-05 | 우선순위·등록/해제 가능한 훅 체인 | partial | P1 | Next 안에서 downstream 결과를 기다리는 around middleware, Next 미호출 short circuit, downstream 오류 전파 계약이 없다. 단순 호출 가능성을 호환 완료로 볼 수 없다. |
| EXT-06 | 임의 Model CRUD 단계 훅 | partial | P1 | 임의 테이블의 일반 Model ORM, DBExporter/PreValidator/PostValidator 통합과 generic model의 외부 트랜잭션 최종 완료 콜백은 없다. wrapper 호출 후 success가 곧 outer commit 성공을 뜻하지 않는다. |
| EXT-07 | record·collection 요청/변경 훅 | adapted | none | 명명된 단계와 수정 기능은 구현됨. live Go event 대신 검증된 JSON patch를 쓰는 차이이며 Next continuation 미구현은 EXT-05에 기록한다. |
| EXT-08 | auth·mailer·file·realtime·backup 훅 범위 | adapted | none | 분야별 훅은 구현됨. payload/동기 veto 범위는 PgBase 계약이며 개별 PB 이벤트의 live method 전체가 동일하다는 뜻은 아니다. ClearBootstrap 신규 이벤트는 EXT-19 별도. |
| EXT-09 | 권한을 상속하는 record CRUD/list/batch helper | adapted | none | CRUD/list/batch 기능 의도는 구현됨. PB app ORM이 자동으로 HTTP 권한을 적용한다는 비교는 부정확하다. 정확한 PB query 인자/결과 호환 공백은 EXT-21. |
| EXT-10 | raw DB builder와 일반 transaction helper | partial | P1 | 트랜잭션은 실제 구현됨. PB dbx NewQuery/Select/Join/Bind/Scan 등의 전체 builder가 없고 반환 shape도 다르다. JS 중첩 savepoint 부재는 PB 공백이 아니다. txInfo.onComplete는 EXT-20. |
| EXT-11 | mailer·file·template helper | partial | P2 | 동적 template registry와 custom FuncMap, JS $template 객체가 없다. 사전 등록 template.render만으로 PB template 기능 전체를 충족하지 않는다. filesystem 세부 차이는 EXT-14 및 파일 영역 참조. |
| EXT-12 | 스크립트 발견·등록 모델과 TypeScript 입력 | adapted | none | 발견·등록을 명시적 manifest로 바꾼 의도적 실행 모델. PB 소스 파일을 그대로 복사하는 호환이 아니며 TS 변환은 PgBase 추가 기능. |
| EXT-13 | CommonJS 로컬 module 지원 | adapted | none | 로컬 CommonJS 의도는 구현됨. 외부/동적 비선언 module 탐색, Node 전체 런타임, VM 간 전역 상태 공유는 의도적으로 제공하지 않는다. |
| EXT-14 | $app/$dbx/$security/$os/$http/$filesystem/$apis globals | partial | P1 | 이름 존재만으로 계약 동등은 아니다. S3 생성자는 모든 인자를 무시하고 operator storage를 사용하며 setContext는 no-op, system.list는 100개 제한/no cursor다. fluent DB, template registry 등도 부분집합. S3 미지원이라는 초기 placeholder 판정은 틀리다. |
| EXT-15 | JS route·hook 등록 | partial | P1 | 명시적 slot/freeze는 의도적 등록 계약이지만 PB handler의 e.Next/live event 계약을 실행하지 못한다. 실제 continuation 공백 EXT-05와 RequestEvent 공백 EXT-22를 포함하므로 PB 1:1로는 partial. |
| EXT-16 | JS scheduled job/cron | partial | P2 | durable scheduled job은 구현됐다. 실행 중 동적 job 생성/교체/삭제라는 PB cron API 동작은 없다. |
| EXT-17 | namespaced record·mail·file·log capability | adapted | none | 작업 기능은 구현됨. capability/namespace는 PgBase의 명시적 실행 정책이며 PB 기능 이름과 완전한 API 동일성을 뜻하지 않는다. |
| EXT-18 | 개발 reload·타입 선언·custom VM binding·template | partial | P2 | reload/types/OnInit 기능은 존재한다. 이 복합 행의 PB $template Registry 동적 LoadFiles/LoadString/AddFuncs는 없다. native Windows release 검증은 이 정적 대조에서 재실행하지 않았다. |
| MIG-01 | Go·JavaScript 수동 migration 등록 | adapted | none | 수동 migration 등록·실행 의도는 구현됨. 전체 txApp 대신 migration 전용 API와 source checksum을 사용하는 의도적 계약 차이. |
| MIG-02 | up 실행과 원자적 rollback | partial | P1 | 단일 migration 실패 원자성은 구현됐으나 PB와 같은 전체 pending batch 원자성이 없다. 현재 테스트는 단일 실패 rollback을 검증하며 multi-migration batch 계약을 보장하지 않는다. |
| MIG-03 | down rollback 개수와 irreversible 표시 | partial | P1 | 개수와 irreversible 표시는 구현됐다. irreversible 명시적 거부와 음수 전체 처리 등은 PgBase 계약이지만 여러 down의 전체 transaction 원자성은 PB와 다르며 뒤 실패 때 이미 완료한 rollback을 복구하지 않는다. |
| MIG-04 | 동시 runner fencing | adapted | none | PostgreSQL용 병행 runner 제어 구현과 테스트가 있다. PB의 DB 잠금 방식과 동일 구현일 필요는 없다. |
| MIG-05 | history와 source checksum 상태 | adapted | none | history 의도는 구현됨. checksum/prefix 검증은 PB보다 추가된 제약이며 PB 자체가 source checksum을 제공한다는 기존 행 표현은 부정확하다. |
| MIG-06 | source에서 제거된 migration 이력 정리 | adapted | none | 이력 정리 기능은 구현됨. PB history-sync 명칭과 PgBase HistorySync의 의미를 혼동하면 안 된다. |
| MIG-07 | 관리자 schema 변경 자동 migration 기록 | partial | P2 | 자동 기록/복구는 구현됨. JS project를 위한 JS migration source 자동 생성 옵션은 없다. 수동 JS migration 지원과 자동 JS 생성 지원은 별개다. |
| MIG-08 | collection snapshot·diff 생성과 data-loss 분석 | adapted | none | snapshot/diff 및 위험 분석 의도는 구현됨. 생성언어 JS 공백은 MIG-07에 명시했고 PB보다 추가된 data-loss 판정은 동일성의 근거가 아니다. |
| EXT-19 | ClearBootstrap 및 OnBootstrapClear 수명주기 | missing | P2 | ClearBootstrap/clearBootstrap/OnBootstrapClear/onBootstrapClear 검색에서 구현 없음. 기존 reset callback과 종료 이벤트는 새 API/훅을 대신하지 못한다. |
| EXT-20 | 트랜잭션 최종 완료 callback TxInfo.OnComplete | missing | P1 | Go 또는 JS extension의 일반 after-commit/after-rollback 등록 및 callback 오류 병합 계약이 없다. record 내부 callback 존재는 public TxInfo 호환이 아니다. |
| EXT-21 | fluent ORM query 및 record helper 선택 인자 | partial | P1 | Select/Join/AndWhere/Bind/Scan 등의 일반 builder가 없다. findRecordsByFilter는 offset/params를 받지 않고 findAllRecords는 exprs를 전달하지 않으며 canAccessRecord는 caller requestInfo를 버린다. 현재 호출자의 권한을 쓰는 변경은 의도적이지만 동일 API로 무시되는 인자는 실질적 호환 공백이다. |
| EXT-22 | JS RequestEvent 응답·body·실시간 stream API | partial | P2 | PB e.json/e.bindBody/e.stream/e.flush/e.setCookie는 JS event에 없다. descriptor 기반 HTTP 응답은 구현됐지만 arbitrary incremental streaming/flush와 PB handler 소스 호환은 제공하지 않는다. Go net/http stream은 EXT-03에서 별도 구현으로 인정한다. |
| MIG-09 | 적용된 migration의 조건부 재실행 ReapplyCondition | missing | P2 | 이미 적용된 migration을 조건에 따라 재실행하는 PB 계약은 없다. strict immutable history 정책으로 대체했지만 1:1 기능은 missing. |

## 행별 근거

### EXT-01 — 애플리케이션 임베딩과 시작 수명주기

**adapted / none / test-exists**

PB 계약: BaseApp.Bootstrap/OnBootstrap와 앱 임베딩으로 저장소·설정·마이그레이션 초기화 및 서버 수명주기를 제어한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go#L412).

PgBase 구현: Go App.New/Start/Run/Close 및 before/after 단계 훅으로 임베딩과 시작·종료를 제공한다. PostgreSQL 연결/설정은 명시적 옵션 계약이다. [pgbase/app.go:303](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app.go:303), [pgbase/app.go:315](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app.go:315), [pgbase/app.go:347](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app.go:347), [pgbase/app.go:507](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app.go:507).

차이: 좁은 시작·종료 기능은 구현됨. PB의 ClearBootstrap/OnBootstrapClear는 별도 EXT-19 공백이다.

관련 테스트: [pgbase/app_lifecycle_hooks_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app_lifecycle_hooks_test.go:13), [pgbase/app_lifecycle_hooks_test.go:42](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app_lifecycle_hooks_test.go:42), [pgbase/app_lifecycle_hooks_test.go:75](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app_lifecycle_hooks_test.go:75)

### EXT-02 — 사용자 정의 HTTP 라우트

**adapted / none / test-exists**

PB 계약: OnServe Router에 method/path/handler 및 middleware를 등록하고 RequestEvent로 응답한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/binds.go#L150).

PgBase 구현: Go net/http CustomRoute를 시작 시 등록하며 명시적 인증 모드, 경로 예약 영역, 제한 수를 검증한다. [pgbase/custom_routes.go:104](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:104), [pgbase/custom_routes.go:111](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:111).

차이: 라우트 기능은 구현됨. 시작 시 등록과 net/http API는 의도적 차이이며 JS RequestEvent의 누락은 EXT-22로 분리한다.

관련 테스트: [pgbase/custom_routes_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes_integration_test.go:16), [pgbase/route_groups_test.go:10](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/route_groups_test.go:10)

### EXT-03 — 라우트 그룹·미들웨어·스트리밍 응답

**adapted / none / test-exists**

PB 계약: 라우트 그룹은 prefix와 middleware를 누적하며 Event는 JSON/Blob/Stream/Flush 등 응답을 제공한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/router/group.go#L33).

PgBase 구현: Go RouteGroup prefix/Use/Handle/Stream/Static과 net/http middleware 체인 및 실제 flush를 제공한다. [pgbase/custom_routes.go:27](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:27), [pgbase/custom_routes.go:33](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:33), [pgbase/custom_routes.go:41](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:41), [pgbase/custom_routes.go:52](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:52), [pgbase/custom_routes.go:57](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:57).

차이: Go 라우팅 기능 의도는 충족. PB router.Event API 호환을 의미하지 않으며 JS 임의 stream/flush는 EXT-22 범위다.

관련 테스트: [pgbase/route_groups_test.go:10](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/route_groups_test.go:10), [pgbase/custom_routes_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes_integration_test.go:16)

### EXT-04 — 프로그래밍 방식 UIExtension 등록

**adapted / none / test-exists**

PB 계약: UIExtension main.js 및 정적 파일을 app에 등록하고 관리자 UI 경로에서 로드한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/extensions.go#L19).

PgBase 구현: fs.FS 기반 UIExtension과 이름/경로 검증, 관리자 UI 확장 loader 및 정적 자원을 제공한다. [pgbase/ui_extension.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/ui_extension.go:13), [pgbase/ui_extension.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/ui_extension.go:18).

차이: 등록·서빙 의도는 구현됨. PgBase 관리자 UI 경로와 배포 계약을 따른다.

관련 테스트: [pgbase/ui_extension_integration_test.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/ui_extension_integration_test.go:18)

### EXT-05 — 우선순위·등록/해제 가능한 훅 체인

**partial / P1 / test-exists**

PB 계약: 우선순위가 있는 Handler 체인에서 e.Next가 다음 핸들러/기본 실행을 실제 호출한다. 호출 전후 처리가 가능하고 Next를 생략하면 실행을 중단한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/hook/hook.go#L146).

PgBase 구현: Go registry는 우선순위/해제/동시 스냅샷 실행을 제공하지만 before/after 순차 호출 구조다. JS e.next는 bool만 변경하고 그 값은 버린다. [internal/lifecycle/registry.go:49](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/lifecycle/registry.go:49), [internal/lifecycle/registry.go:91](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/lifecycle/registry.go:91), [internal/lifecycle/registry.go:124](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/lifecycle/registry.go:124), [internal/jsvm/registration.go:221](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:221).

차이: Next 안에서 downstream 결과를 기다리는 around middleware, Next 미호출 short circuit, downstream 오류 전파 계약이 없다. 단순 호출 가능성을 호환 완료로 볼 수 없다.

관련 테스트: [internal/lifecycle/registry_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/lifecycle/registry_test.go:11), [internal/lifecycle/registry_test.go:33](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/lifecycle/registry_test.go:33), [internal/jsvm/registration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration_test.go:12)

### EXT-06 — 임의 Model CRUD 단계 훅

**partial / P1 / test-exists**

PB 계약: 일반 Model Save/Delete/Validate가 PreValidator/PostValidator 및 model CRUD execute/success/error 훅을 호출한다. 트랜잭션 내 after-success/error는 최종 트랜잭션 완료와 연결된다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/db.go#L239).

PgBase 구현: RunModelMutation으로 사용자 콜백을 validate/execute/success/error 단계로 감싸고 record/collection 모델 facade를 제공한다. record 전용 결과 훅은 최종 앱 트랜잭션 완료에 연결돼 있다. [pgbase/model_lifecycle.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle.go:30), [pgbase/model_lifecycle.go:71](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle.go:71), [pgbase/model_lifecycle.go:92](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle.go:92), [pgbase/extension_bridge.go:429](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_bridge.go:429), [pgbase/extension_app_transaction.go:84](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_transaction.go:84).

차이: 임의 테이블의 일반 Model ORM, DBExporter/PreValidator/PostValidator 통합과 generic model의 외부 트랜잭션 최종 완료 콜백은 없다. wrapper 호출 후 success가 곧 outer commit 성공을 뜻하지 않는다.

관련 테스트: [pgbase/model_lifecycle_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle_test.go:12), [pgbase/model_lifecycle_test.go:33](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle_test.go:33), [pgbase/model_lifecycle_test.go:86](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle_test.go:86), [pgbase/extension_app_transaction_integration_test.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_transaction_integration_test.go:18)

### EXT-07 — record·collection 요청/변경 훅

**adapted / none / test-exists**

PB 계약: OnRecord*/OnCollection* 요청·검증·실행·결과 및 enrich 훅을 앱에서 노출하고 태그로 collection을 제한한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/binds.go#L43).

PgBase 구현: record/collection 요청 및 mutation 단계, 공개 auth 요청, collection 태그, record enrich를 제공한다. JSON payload와 허용된 patch 경로를 통해서만 변경한다. [pgbase/lifecycle_hooks.go:78](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks.go:78), [pgbase/lifecycle_hooks.go:161](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks.go:161), [pgbase/javascript_hooks.go:97](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_hooks.go:97), [pgbase/javascript_hooks.go:122](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_hooks.go:122), [pgbase/javascript_hooks.go:202](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_hooks.go:202).

차이: 명명된 단계와 수정 기능은 구현됨. live Go event 대신 검증된 JSON patch를 쓰는 차이이며 Next continuation 미구현은 EXT-05에 기록한다.

관련 테스트: [pgbase/lifecycle_hooks_integration_test.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks_integration_test.go:18), [pgbase/lifecycle_hooks_integration_test.go:259](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks_integration_test.go:259), [pgbase/javascript_collection_enrich_hooks_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_collection_enrich_hooks_integration_test.go:12), [pgbase/javascript_collection_enrich_hooks_integration_test.go:78](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_collection_enrich_hooks_integration_test.go:78), [pgbase/javascript_model_phase_hooks_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_model_phase_hooks_integration_test.go:12)

### EXT-08 — auth·mailer·file·realtime·backup 훅 범위

**adapted / none / test-exists**

PB 계약: 앱 인터페이스에 수명주기와 auth/mailer/file/realtime/backup 등 영역별 이벤트 훅을 제공한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/app.go#L739).

PgBase 구현: 영역별 before/after 이벤트와 veto/error 관찰을 lifecycle registry 및 JS manifest로 노출한다. [pgbase/lifecycle_hooks.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks.go:22), [pgbase/javascript_hooks.go:73](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_hooks.go:73), [pgbase/javascript_hooks.go:89](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_hooks.go:89).

차이: 분야별 훅은 구현됨. payload/동기 veto 범위는 PgBase 계약이며 개별 PB 이벤트의 live method 전체가 동일하다는 뜻은 아니다. ClearBootstrap 신규 이벤트는 EXT-19 별도.

관련 테스트: [pgbase/auth_lifecycle_hooks_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/auth_lifecycle_hooks_test.go:13), [pgbase/auth_lifecycle_hooks_integration_test.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/auth_lifecycle_hooks_integration_test.go:14), [pgbase/lifecycle_hooks_integration_test.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks_integration_test.go:18)

### EXT-09 — 권한을 상속하는 record CRUD/list/batch helper

**adapted / none / test-exists**

PB 계약: PB app의 record 조회/저장과 CanAccessRecord는 requestInfo 및 rule을 별도 적용할 수 있는 직접 앱 API다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_query.go#L599).

PgBase 구현: ExtensionContext와 pgbase.records facade는 호출 context/principal/capability를 물려받아 domain service의 CRUD/list/batch를 실행한다. [pgbase/extensions.go:20](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extensions.go:20), [pgbase/extensions.go:31](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extensions.go:31), [pgbase/extensions.go:86](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extensions.go:86), [pgbase/extensions.go:100](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extensions.go:100), [pgbase/extensions.go:113](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extensions.go:113), [pgbase/extension_bridge.go:530](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_bridge.go:530).

차이: CRUD/list/batch 기능 의도는 구현됨. PB app ORM이 자동으로 HTTP 권한을 적용한다는 비교는 부정확하다. 정확한 PB query 인자/결과 호환 공백은 EXT-21.

관련 테스트: [pgbase/javascript_records_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_records_integration_test.go:16)

### EXT-10 — raw DB builder와 일반 transaction helper

**partial / P1 / test-exists**

PB 계약: 앱 DB는 dbx builder이고 RunInTransaction은 tx app을 전달한다. 이미 transaction이면 기존 tx를 재사용한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/db_tx.go#L14).

PgBase 구현: Go TrustedDatabase는 Exec/Query/QueryRow와 RunInTransaction을 제공하며 Go 중첩은 pgx savepoint를 사용한다. JS app.runInTransaction은 실제 DB transaction에 record 작업을 묶고 중첩은 같은 app/tx를 재사용한다. [pgbase/trusted_database.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/trusted_database.go:30), [pgbase/trusted_database.go:50](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/trusted_database.go:50), [pgbase/trusted_database.go:82](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/trusted_database.go:82), [internal/jsvm/pocketbase_globals.go:445](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:445), [internal/jsvm/pocketbase_globals.go:446](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:446), [pgbase/extension_app_transaction.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_transaction.go:23).

차이: 트랜잭션은 실제 구현됨. PB dbx NewQuery/Select/Join/Bind/Scan 등의 전체 builder가 없고 반환 shape도 다르다. JS 중첩 savepoint 부재는 PB 공백이 아니다. txInfo.onComplete는 EXT-20.

관련 테스트: [pgbase/extension_app_transaction_integration_test.go:68](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_transaction_integration_test.go:68), [pgbase/javascript_database_objects_integration_test.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_database_objects_integration_test.go:14), [internal/jsvm/pocketbase_globals_test.go:124](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals_test.go:124)

### EXT-11 — mailer·file·template helper

**partial / P2 / test-exists**

PB 계약: 앱 mailer/filesystem과 template Registry의 AddFuncs/LoadFiles/LoadString/LoadFS/Render로 템플릿을 동적으로 만들고 렌더한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/template/registry.go#L73).

PgBase 구현: 실제 메일 발송·파일 작업, Go에서 사전 등록한 html/template의 shared render helper가 있다. [pgbase/templates.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/templates.go:18), [pgbase/templates.go:35](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/templates.go:35), [internal/jsvm/pocketbase_filesystem_globals.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_filesystem_globals.go:12).

차이: 동적 template registry와 custom FuncMap, JS $template 객체가 없다. 사전 등록 template.render만으로 PB template 기능 전체를 충족하지 않는다. filesystem 세부 차이는 EXT-14 및 파일 영역 참조.

관련 테스트: [pgbase/javascript_file_mail_integration_test.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_file_mail_integration_test.go:22), [pgbase/javascript_template_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_template_integration_test.go:12), [pgbase/templates_test.go:5](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/templates_test.go:5)

### EXT-12 — 스크립트 발견·등록 모델과 TypeScript 입력

**adapted / none / test-exists**

PB 계약: HooksDir의 .pb.js/.pb.ts를 발견해 로드하고 등록한다. PB .pb.ts 확장자는 IDE/린터용이고 Goja는 실제 TS 문법을 변환하지 않는다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/jsvm.go#L59).

PgBase 구현: operator manifest에서 route/hook/job 및 capability를 선언하고 immutable bundle을 로드한다. .ts/.tsx 입력은 esbuild로 변환한다. [internal/jsvm/bundle.go:165](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/bundle.go:165), [internal/jsvm/bundle.go:229](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/bundle.go:229), [internal/jsvm/registration.go:85](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:85).

차이: 발견·등록을 명시적 manifest로 바꾼 의도적 실행 모델. PB 소스 파일을 그대로 복사하는 호환이 아니며 TS 변환은 PgBase 추가 기능.

관련 테스트: [internal/jsvm/runtime_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime_test.go:16), [internal/jsvm/bundle_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/bundle_test.go:13), [internal/jsvm/registration_test.go:43](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration_test.go:43)

### EXT-13 — CommonJS 로컬 module 지원

**adapted / none / test-exists**

PB 계약: goja_nodejs require.Registry를 이용해 로컬 CommonJS module을 require한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/jsvm.go#L196).

PgBase 구현: 미리 snapshot한 로컬 module graph를 경로 경계와 module 수/크기 제한 아래 실행하며 각 invocation은 fresh VM이다. [internal/jsvm/bundle.go:165](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/bundle.go:165), [internal/jsvm/runtime.go:354](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime.go:354), [internal/jsvm/runtime.go:593](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime.go:593).

차이: 로컬 CommonJS 의도는 구현됨. 외부/동적 비선언 module 탐색, Node 전체 런타임, VM 간 전역 상태 공유는 의도적으로 제공하지 않는다.

관련 테스트: [internal/jsvm/runtime_test.go:86](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime_test.go:86), [internal/jsvm/bundle_test.go:93](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/bundle_test.go:93), [internal/jsvm/bundle_test.go:118](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/bundle_test.go:118)

### EXT-14 — $app/$dbx/$security/$os/$http/$filesystem/$apis globals

**partial / P1 / test-exists**

PB 계약: Go 앱과 dbx/security/os/http/filesystem/apis 기능을 VM에 binding한다. filesystem.NewS3는 주어진 endpoint/bucket/credential로 독립 클라이언트를 만든다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/binds.go#L312).

PgBase 구현: 다수 실제 capability host adapter가 존재하며 최종 Object.assign과 filesystem 설치가 초기 placeholder를 덮어쓴다. $filesystem.s3도 실제 configured object storage를 반환한다. [internal/jsvm/pocketbase_globals.go:445](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:445), [internal/jsvm/pocketbase_globals.go:450](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:450), [internal/jsvm/pocketbase_globals.go:462](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:462), [internal/jsvm/pocketbase_globals.go:463](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:463), [internal/jsvm/pocketbase_filesystem_globals.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_filesystem_globals.go:12), [internal/jsvm/pocketbase_filesystem_globals.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_filesystem_globals.go:15).

차이: 이름 존재만으로 계약 동등은 아니다. S3 생성자는 모든 인자를 무시하고 operator storage를 사용하며 setContext는 no-op, system.list는 100개 제한/no cursor다. fluent DB, template registry 등도 부분집합. S3 미지원이라는 초기 placeholder 판정은 틀리다.

관련 테스트: [internal/jsvm/pocketbase_globals_test.go:32](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals_test.go:32), [internal/jsvm/pocketbase_globals_test.go:102](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals_test.go:102), [pgbase/extension_compatibility_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_compatibility_test.go:15), [pgbase/extension_os_shapes_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_os_shapes_test.go:15), [pgbase/javascript_compatibility_conformance_integration_test.go:61](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_compatibility_conformance_integration_test.go:61)

### EXT-15 — JS route·hook 등록

**partial / P1 / test-exists**

PB 계약: routerAdd/routerUse와 onX 등록이 Go router 및 live event hook에 직접 연결된다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/binds.go#L150).

PgBase 구현: routerAdd/routerUse/onX를 manifest slot에 재생하고 handler closure를 보존한다. 실행 시작 후 등록은 freeze되며 JSON 입력/응답과 wrapper middleware를 사용한다. [internal/jsvm/registration.go:85](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:85), [internal/jsvm/registration.go:131](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:131), [internal/jsvm/registration.go:141](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:141), [internal/jsvm/registration.go:221](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:221).

차이: 명시적 slot/freeze는 의도적 등록 계약이지만 PB handler의 e.Next/live event 계약을 실행하지 못한다. 실제 continuation 공백 EXT-05와 RequestEvent 공백 EXT-22를 포함하므로 PB 1:1로는 partial.

관련 테스트: [pgbase/javascript_bootstrap_registration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_bootstrap_registration_test.go:16), [internal/jsvm/registration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration_test.go:12), [internal/jsvm/registration_test.go:43](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration_test.go:43), [internal/jsvm/registration_test.go:56](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration_test.go:56)

### EXT-16 — JS scheduled job/cron

**partial / P2 / test-exists**

PB 계약: cronAdd/cronRemove는 loader와 실행 VM 양쪽에 노출되어 실행 중에도 앱의 cron job을 추가/교체/제거한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/binds.go#L105).

PgBase 구현: operator가 사전 선언한 durable job을 cronAdd로 연결하고 service subject 아래 실행하며 재시도/idempotency를 지원한다. cronRemove는 predeclared job을 제거하지 못한다. [internal/jsvm/registration.go:151](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:151), [internal/jsvm/registration.go:177](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:177), [pgbase/javascript_jobs.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_jobs.go:24), [pgbase/extension_app_control.go:169](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_control.go:169).

차이: durable scheduled job은 구현됐다. 실행 중 동적 job 생성/교체/삭제라는 PB cron API 동작은 없다.

관련 테스트: [pgbase/javascript_jobs_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_jobs_integration_test.go:16), [pgbase/javascript_jobs_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_jobs_test.go:12)

### EXT-17 — namespaced record·mail·file·log capability

**adapted / none / test-exists**

PB 계약: PB는 $app 및 관련 bindings로 서버 권한을 가진 trusted script에서 CRUD/mail/file/log 기능을 호출한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/jsvm.go#L207).

PgBase 구현: require('pgbase')에 record/mail/file/log host facade가 있고 manifest capabilities·호출 principal·파일 namespace를 적용한다. [pgbase/extension_bridge.go:530](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_bridge.go:530), [internal/jsvm/runtime.go:593](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime.go:593), [internal/jsvm/runtime.go:635](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime.go:635).

차이: 작업 기능은 구현됨. capability/namespace는 PgBase의 명시적 실행 정책이며 PB 기능 이름과 완전한 API 동일성을 뜻하지 않는다.

관련 테스트: [pgbase/javascript_records_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_records_integration_test.go:16), [pgbase/javascript_file_mail_integration_test.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_file_mail_integration_test.go:22), [internal/jsvm/pocketbase_globals_test.go:61](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals_test.go:61)

### EXT-18 — 개발 reload·타입 선언·custom VM binding·template

**partial / P2 / test-exists**

PB 계약: HooksWatch reload, 타입 선언 생성, OnInit custom VM binding과 $template Registry를 제공한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/jsvm.go#L59).

PgBase 구현: CLI check/types/watch, 유효 generation 유지, JavaScriptOptions.OnInit 및 precompiled template.render를 제공한다. [internal/cli/javascript.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript.go:18), [internal/cli/javascript_watch.go:25](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript_watch.go:25), [internal/cli/javascript_watch.go:54](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript_watch.go:54), [pgbase/javascript_options.go:43](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_options.go:43), [pgbase/templates.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/templates.go:18).

차이: reload/types/OnInit 기능은 존재한다. 이 복합 행의 PB $template Registry 동적 LoadFiles/LoadString/AddFuncs는 없다. native Windows release 검증은 이 정적 대조에서 재실행하지 않았다.

관련 테스트: [internal/cli/javascript_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript_test.go:12), [internal/cli/javascript_watch_integration_test.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript_watch_integration_test.go:24), [internal/jsvm/runtime_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime_test.go:16), [pgbase/javascript_template_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_template_integration_test.go:12)

### MIG-01 — Go·JavaScript 수동 migration 등록

**adapted / none / test-exists**

PB 계약: Go MigrationsList.Register 또는 JS migrate(up,down)로 파일명 순서와 txApp 콜백을 등록한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_list.go#L61).

PgBase 구현: Go UserMigration source+callbacks, JavaScriptMigrationBundle의 고정 export를 등록한다. JS는 migration 전용 exec/query/applySnapshot bridge를 전달받는다. [pgbase/user_migrations.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/user_migrations.go:24), [pgbase/user_migrations.go:64](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/user_migrations.go:64), [pgbase/user_migrations.go:89](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/user_migrations.go:89), [pgbase/javascript_migrations.go:29](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migrations.go:29), [pgbase/javascript_migrations.go:58](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migrations.go:58), [pgbase/javascript_migrations.go:79](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migrations.go:79).

차이: 수동 migration 등록·실행 의도는 구현됨. 전체 txApp 대신 migration 전용 API와 source checksum을 사용하는 의도적 계약 차이.

관련 테스트: [pgbase/javascript_migrations_integration_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migrations_integration_test.go:13), [pgbase/javascript_migration_acceptance_integration_test.go:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migration_acceptance_integration_test.go:21), [internal/cli/javascript_migration_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript_migration_integration_test.go:17)

### MIG-02 — up 실행과 원자적 rollback

**partial / P1 / test-exists**

PB 계약: 한 번의 Up 호출에 속한 모든 미적용 migration을 중첩 aux/data transaction 안에서 실행한다. 뒤 migration이 실패하면 이번 호출의 앞 migration도 rollback된다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_runner.go#L129).

PgBase 구현: 각 migration의 DDL/data/ledger/schema-version은 applyOne의 한 transaction이다. Up은 applyOne을 반복하므로 먼저 성공한 migration은 이미 commit된다. [internal/usermigrate/runner.go:249](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:249), [internal/usermigrate/runner.go:296](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:296), [internal/usermigrate/runner.go:300](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:300), [internal/usermigrate/runner.go:361](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:361).

차이: 단일 migration 실패 원자성은 구현됐으나 PB와 같은 전체 pending batch 원자성이 없다. 현재 테스트는 단일 실패 rollback을 검증하며 multi-migration batch 계약을 보장하지 않는다.

관련 테스트: [internal/usermigrate/runner_integration_test.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner_integration_test.go:30), [internal/usermigrate/runner_integration_test.go:121](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner_integration_test.go:121)

### MIG-03 — down rollback 개수와 irreversible 표시

**partial / P1 / test-exists**

PB 계약: Down(number)는 최근 N개를 한 transaction에서 되돌린다. Down 콜백이 nil이면 콜백 없이 history를 제거한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_runner.go#L178).

PgBase 구현: DownN은 N개/음수 전체/0 no-op이며 migration별 transaction을 사용한다. irreversible down은 명시적 오류로 거부한다. [internal/usermigrate/runner.go:276](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:276), [internal/usermigrate/runner.go:279](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:279), [internal/usermigrate/runner.go:296](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:296).

차이: 개수와 irreversible 표시는 구현됐다. irreversible 명시적 거부와 음수 전체 처리 등은 PgBase 계약이지만 여러 down의 전체 transaction 원자성은 PB와 다르며 뒤 실패 때 이미 완료한 rollback을 복구하지 않는다.

관련 테스트: [internal/usermigrate/snapshot_integration_test.go:223](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:223), [internal/usermigrate/snapshot_integration_test.go:277](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:277), [internal/usermigrate/runner_integration_test.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner_integration_test.go:30)

### MIG-04 — 동시 runner fencing

**adapted / none / test-exists**

PB 계약: PB SQLite transaction과 migration ledger를 통해 적용 상태 읽기/쓰기를 직렬화한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_runner.go#L129).

PgBase 구현: PostgreSQL transaction advisory lock 두 개와 schema_state FOR UPDATE를 잡고 callback 및 ledger를 처리한다. [internal/usermigrate/runner.go:300](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:300), [internal/usermigrate/runner.go:304](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:304), [internal/usermigrate/runner.go:308](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:308).

차이: PostgreSQL용 병행 runner 제어 구현과 테스트가 있다. PB의 DB 잠금 방식과 동일 구현일 필요는 없다.

관련 테스트: [internal/usermigrate/runner_integration_test.go:180](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner_integration_test.go:180), [internal/usermigrate/schema_concurrency_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/schema_concurrency_integration_test.go:17), [pgbase/javascript_migration_acceptance_integration_test.go:163](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migration_acceptance_integration_test.go:163)

### MIG-05 — history와 source checksum 상태

**adapted / none / test-exists**

PB 계약: PB history는 file/applied를 저장하고 파일명 기준으로 적용 여부를 찾는다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_runner.go#L245).

PgBase 구현: source revision hash, applied history 및 manifest prefix를 검증하고 source 변경·history gap·history-only 상태를 보고한다. [pgbase/user_migrations.go:89](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/user_migrations.go:89), [internal/usermigrate/runner.go:49](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:49), [internal/usermigrate/runner.go:228](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:228), [internal/usermigrate/runner.go:422](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:422), [internal/usermigrate/runner.go:434](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:434).

차이: history 의도는 구현됨. checksum/prefix 검증은 PB보다 추가된 제약이며 PB 자체가 source checksum을 제공한다는 기존 행 표현은 부정확하다.

관련 테스트: [internal/usermigrate/runner_integration_test.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner_integration_test.go:30), [internal/usermigrate/snapshot_integration_test.go:382](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:382)

### MIG-06 — source에서 제거된 migration 이력 정리

**adapted / none / test-exists**

PB 계약: RemoveMissingAppliedMigrations는 등록 목록에 없는 migration의 history entry를 지운다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_runner.go#L229).

PgBase 구현: CleanupHistory는 source가 사라진 이력만 preview/삭제하고 transaction으로 실행한다. 별도 HistorySync는 증명된 snapshot의 baseline 등록 기능이다. [internal/usermigrate/runner.go:106](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:106), [internal/usermigrate/runner.go:163](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:163), [pgbase/user_migrations.go:148](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/user_migrations.go:148).

차이: 이력 정리 기능은 구현됨. PB history-sync 명칭과 PgBase HistorySync의 의미를 혼동하면 안 된다.

관련 테스트: [internal/usermigrate/runner_integration_test.go:266](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner_integration_test.go:266), [internal/usermigrate/snapshot_integration_test.go:312](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:312)

### MIG-07 — 관리자 schema 변경 자동 migration 기록

**partial / P2 / test-exists**

PB 계약: 관리자 collection 변경 후 TemplateLang 설정에 맞춰 Go 또는 JS migration diff source를 만들고 ledger에 적용 상태를 기록한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/migratecmd/automigrate.go#L18).

PgBase 구현: 관리자 schema 변경을 canonical snapshot 기반 executable Go migration으로 기록하고 파일 materialization을 durable pending record에서 복구한다. [internal/usermigrate/recorder.go:56](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder.go:56), [internal/usermigrate/recorder.go:63](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder.go:63), [internal/usermigrate/recorder.go:111](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder.go:111), [internal/usermigrate/recorder.go:239](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder.go:239).

차이: 자동 기록/복구는 구현됨. JS project를 위한 JS migration source 자동 생성 옵션은 없다. 수동 JS migration 지원과 자동 JS 생성 지원은 별개다.

관련 테스트: [internal/usermigrate/recorder_integration_test.go:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder_integration_test.go:21), [internal/usermigrate/recorder_integration_test.go:115](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder_integration_test.go:115), [internal/usermigrate/recorder_integration_test.go:186](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder_integration_test.go:186)

### MIG-08 — collection snapshot·diff 생성과 data-loss 분석

**adapted / none / test-exists**

PB 계약: PB collections snapshot 및 diff를 Go/JS migration source로 생성하여 importCollections로 재현한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/migratecmd/templates.go#L325).

PgBase 구현: canonical snapshot/diff와 data-loss 분석을 만들고 실행 가능한 Go up/down source를 생성한다. 파괴적 변경은 irreversible down으로 표시한다. [internal/usermigrate/generate.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/generate.go:30), [internal/usermigrate/generate.go:76](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/generate.go:76), [internal/usermigrate/generate.go:98](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/generate.go:98), [internal/usermigrate/generate.go:126](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/generate.go:126).

차이: snapshot/diff 및 위험 분석 의도는 구현됨. 생성언어 JS 공백은 MIG-07에 명시했고 PB보다 추가된 data-loss 판정은 동일성의 근거가 아니다.

관련 테스트: [internal/usermigrate/snapshot_integration_test.go:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:26), [internal/usermigrate/snapshot_integration_test.go:147](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:147), [internal/usermigrate/snapshot_integration_test.go:191](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:191), [internal/cli/user_migration_test.go:35](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/user_migration_test.go:35)

### EXT-19 — ClearBootstrap 및 OnBootstrapClear 수명주기

**missing / P2 / static-only**

PB 계약: v0.40.4 ClearBootstrap은 초기화된 앱의 DB builder를 닫는 정리를 OnBootstrapClear 훅 안에서 수행한다. 정확한 훅 이름은 OnBootstrapClear이다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go#L475).

PgBase 구현: $app.bootstrap/resetBootstrapState는 operator callback으로 제공되지만 ClearBootstrap API 및 OnBootstrapClear 이벤트 등록 표면은 없다. [pgbase/extension_app_control.go:68](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_control.go:68), [internal/jsvm/pocketbase_globals.go:463](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:463), [pgbase/lifecycle_hooks.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks.go:22).

차이: ClearBootstrap/clearBootstrap/OnBootstrapClear/onBootstrapClear 검색에서 구현 없음. 기존 reset callback과 종료 이벤트는 새 API/훅을 대신하지 못한다.

관련 테스트: 해당 누락 기능의 공개 계약 테스트를 찾지 못함; 구현 표면 검색과 관련 타입/호출 경로 정적 확인.

### EXT-20 — 트랜잭션 최종 완료 callback TxInfo.OnComplete

**missing / P1 / static-only**

PB 계약: TxInfo.OnComplete callback은 최종 commit 또는 rollback 후 txErr를 받고 callback 오류를 transaction 결과에 합친다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/db_tx.go#L79).

PgBase 구현: transactionInfo/txInfo는 transactional flag와 token만 반환한다. record 결과 훅의 내부 finalization은 있으나 extension이 완료 callback을 등록할 공개 API는 없다. [pgbase/extension_app_control.go:153](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_control.go:153), [internal/jsvm/pocketbase_globals.go:449](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:449), [internal/jsvm/pocketbase_globals.go:464](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:464), [pgbase/extension_app_transaction.go:84](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_transaction.go:84).

차이: Go 또는 JS extension의 일반 after-commit/after-rollback 등록 및 callback 오류 병합 계약이 없다. record 내부 callback 존재는 public TxInfo 호환이 아니다.

관련 테스트: 해당 누락 기능의 공개 계약 테스트를 찾지 못함; 구현 표면 검색과 관련 타입/호출 경로 정적 확인.

### EXT-21 — fluent ORM query 및 record helper 선택 인자

**partial / P1 / test-exists**

PB 계약: RecordQuery/ModelQuery는 dbx SelectQuery를 반환한다. FindRecordsByFilter는 offset/params, FindAllRecords는 표현식, CanAccessRecord는 supplied RequestInfo를 받는다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_query.go#L26).

PgBase 구현: recordQuery는 즉시 page data를 반환한다. modelQuery는 where/orderBy/limit/all/one/count만, collectionQuery는 제한된 hashExp id/name/type 필터만 제공한다. [internal/jsvm/pocketbase_globals.go:449](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:449), [internal/jsvm/pocketbase_globals.go:450](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:450), [internal/jsvm/pocketbase_globals.go:454](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:454), [internal/jsvm/pocketbase_globals.go:457](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:457), [pgbase/extension_app_query.go:70](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_query.go:70), [pgbase/extension_app_query.go:158](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_query.go:158).

차이: Select/Join/AndWhere/Bind/Scan 등의 일반 builder가 없다. findRecordsByFilter는 offset/params를 받지 않고 findAllRecords는 exprs를 전달하지 않으며 canAccessRecord는 caller requestInfo를 버린다. 현재 호출자의 권한을 쓰는 변경은 의도적이지만 동일 API로 무시되는 인자는 실질적 호환 공백이다.

관련 테스트: [internal/jsvm/pocketbase_globals_test.go:163](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals_test.go:163), [pgbase/extension_app_query_test.go:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_query_test.go:9), [pgbase/extension_app_query_integration_test.go:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_query_integration_test.go:21), [pgbase/extension_app_query_integration_test.go:74](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_query_integration_test.go:74)

### EXT-22 — JS RequestEvent 응답·body·실시간 stream API

**partial / P2 / test-exists**

PB 계약: JS handler는 live RequestEvent의 json/bindBody/stream/flush/setCookie 등으로 응답 및 body binding을 직접 제어한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/router/event.go#L191).

PgBase 구현: JS handler는 JSON request DTO와 opaque request/response handle을 받고 응답 descriptor를 반환한다. object/static/compressed 응답은 capability와 StreamResponse 아래 host가 실행한다. [pgbase/javascript_runtime.go:87](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_runtime.go:87), [pgbase/javascript_runtime.go:98](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_runtime.go:98), [pgbase/javascript_runtime.go:106](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_runtime.go:106), [pgbase/javascript_runtime.go:219](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_runtime.go:219), [pgbase/javascript_runtime.go:259](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_runtime.go:259), [internal/jsvm/runtime.go:439](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime.go:439).

차이: PB e.json/e.bindBody/e.stream/e.flush/e.setCookie는 JS event에 없다. descriptor 기반 HTTP 응답은 구현됐지만 arbitrary incremental streaming/flush와 PB handler 소스 호환은 제공하지 않는다. Go net/http stream은 EXT-03에서 별도 구현으로 인정한다.

관련 테스트: [pgbase/javascript_compatibility_conformance_integration_test.go:75](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_compatibility_conformance_integration_test.go:75), [pgbase/extension_objects_integration_test.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_objects_integration_test.go:24)

### MIG-09 — 적용된 migration의 조건부 재실행 ReapplyCondition

**missing / P2 / static-only**

PB 계약: Migration.ReapplyCondition은 이미 적용된 파일도 조건이 true이면 기존 history를 교체하고 다시 Up을 실행한다. [고정 commit 소스](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_list.go#L13).

PgBase 구현: UserMigration/내부 Migration은 source revision과 up/down을 보유하지만 reapply callback 필드/runner 분기가 없다. [pgbase/user_migrations.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/user_migrations.go:24), [internal/usermigrate/types.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/types.go:11), [internal/usermigrate/runner.go:422](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:422), [internal/usermigrate/runner.go:434](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:434).

차이: 이미 적용된 migration을 조건에 따라 재실행하는 PB 계약은 없다. strict immutable history 정책으로 대체했지만 1:1 기능은 missing.

관련 테스트: 해당 누락 기능의 공개 계약 테스트를 찾지 못함; 구현 표면 검색과 관련 타입/호출 경로 정적 확인.

## 읽기 범위와 검색 경계

전체 파일을 빠짐없이 줄마다 읽었다는 뜻이 아니다. 아래는 해당 계약의 핵심 함수와 주변 호출 경로를 직접 읽은 파일 목록이다. 테스트는 관련 테스트 함수와 assertion을 읽거나 동일 파일 내 필요한 위치를 좁혀 확인했다. 출력이 긴 파일은 여러 구간으로 읽었으며, 이 보고서는 프로젝트 전체 API 완전성의 증명이 아니다.

### PocketBase 구현

- [core/app.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/app.go#L1)
- [core/base.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go#L1)
- [core/db.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/db.go#L1)
- [core/db_tx.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/db_tx.go#L1)
- [core/record_query.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_query.go#L1)
- [core/migrations_list.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_list.go#L1)
- [core/migrations_runner.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_runner.go#L1)
- [tools/hook/event.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/hook/event.go#L1)
- [tools/hook/hook.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/hook/hook.go#L1)
- [tools/hook/tagged.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/hook/tagged.go#L1)
- [tools/router/group.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/router/group.go#L1)
- [tools/router/event.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/router/event.go#L1)
- [tools/template/registry.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/template/registry.go#L1)
- [tools/template/renderer.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/template/renderer.go#L1)
- [tools/filesystem/filesystem.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L1)
- [apis/extensions.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/extensions.go#L1)
- [plugins/jsvm/jsvm.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/jsvm.go#L1)
- [plugins/jsvm/binds.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/binds.go#L1)
- [plugins/migratecmd/automigrate.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/migratecmd/automigrate.go#L1)
- [plugins/migratecmd/migratecmd.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/migratecmd/migratecmd.go#L1)
- [plugins/migratecmd/templates.go:1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/migratecmd/templates.go#L1)

PB migration runner 및 hooks/router/filesystem/template 함수의 실제 본문을 기준으로 판단했다. core/migrations_runner_test.go의 ReapplyCondition 존재도 확인했다. plugins/jsvm/binds_test.go는 binding 테스트 위치를 검색했지만 PgBase 테스트의 실행 증거로 사용하지 않았다.

### PgBase 구현

- [internal/cli/javascript.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript.go:1)
- [internal/cli/javascript_watch.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript_watch.go:1)
- [internal/jsvm/bundle.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/bundle.go:1)
- [internal/jsvm/pocketbase_filesystem_globals.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_filesystem_globals.go:1)
- [internal/jsvm/pocketbase_globals.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:1)
- [internal/jsvm/registration.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:1)
- [internal/jsvm/runtime.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime.go:1)
- [internal/lifecycle/registry.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/lifecycle/registry.go:1)
- [internal/usermigrate/generate.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/generate.go:1)
- [internal/usermigrate/recorder.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder.go:1)
- [internal/usermigrate/runner.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:1)
- [internal/usermigrate/types.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/types.go:1)
- [pgbase/app.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app.go:1)
- [pgbase/custom_routes.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:1)
- [pgbase/extension_app_control.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_control.go:1)
- [pgbase/extension_app_query.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_query.go:1)
- [pgbase/extension_app_transaction.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_transaction.go:1)
- [pgbase/extension_bridge.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_bridge.go:1)
- [pgbase/extensions.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extensions.go:1)
- [pgbase/javascript_hooks.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_hooks.go:1)
- [pgbase/javascript_jobs.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_jobs.go:1)
- [pgbase/javascript_migrations.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migrations.go:1)
- [pgbase/javascript_options.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_options.go:1)
- [pgbase/javascript_runtime.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_runtime.go:1)
- [pgbase/lifecycle_hooks.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks.go:1)
- [pgbase/model_lifecycle.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle.go:1)
- [pgbase/templates.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/templates.go:1)
- [pgbase/trusted_database.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/trusted_database.go:1)
- [pgbase/ui_extension.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/ui_extension.go:1)
- [pgbase/user_migrations.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/user_migrations.go:1)

### PgBase 관련 테스트

- [internal/cli/javascript_migration_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript_migration_integration_test.go:1)
- [internal/cli/javascript_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript_test.go:1)
- [internal/cli/javascript_watch_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript_watch_integration_test.go:1)
- [internal/cli/user_migration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/user_migration_test.go:1)
- [internal/jsvm/bundle_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/bundle_test.go:1)
- [internal/jsvm/pocketbase_globals_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals_test.go:1)
- [internal/jsvm/registration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration_test.go:1)
- [internal/jsvm/runtime_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime_test.go:1)
- [internal/lifecycle/registry_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/lifecycle/registry_test.go:1)
- [internal/usermigrate/recorder_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder_integration_test.go:1)
- [internal/usermigrate/runner_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner_integration_test.go:1)
- [internal/usermigrate/schema_concurrency_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/schema_concurrency_integration_test.go:1)
- [internal/usermigrate/snapshot_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:1)
- [pgbase/app_lifecycle_hooks_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app_lifecycle_hooks_test.go:1)
- [pgbase/auth_lifecycle_hooks_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/auth_lifecycle_hooks_integration_test.go:1)
- [pgbase/auth_lifecycle_hooks_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/auth_lifecycle_hooks_test.go:1)
- [pgbase/custom_routes_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes_integration_test.go:1)
- [pgbase/extension_app_query_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_query_integration_test.go:1)
- [pgbase/extension_app_query_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_query_test.go:1)
- [pgbase/extension_app_transaction_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_transaction_integration_test.go:1)
- [pgbase/extension_compatibility_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_compatibility_test.go:1)
- [pgbase/extension_objects_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_objects_integration_test.go:1)
- [pgbase/extension_os_shapes_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_os_shapes_test.go:1)
- [pgbase/javascript_bootstrap_registration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_bootstrap_registration_test.go:1)
- [pgbase/javascript_collection_enrich_hooks_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_collection_enrich_hooks_integration_test.go:1)
- [pgbase/javascript_compatibility_conformance_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_compatibility_conformance_integration_test.go:1)
- [pgbase/javascript_database_objects_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_database_objects_integration_test.go:1)
- [pgbase/javascript_file_mail_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_file_mail_integration_test.go:1)
- [pgbase/javascript_jobs_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_jobs_integration_test.go:1)
- [pgbase/javascript_jobs_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_jobs_test.go:1)
- [pgbase/javascript_migration_acceptance_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migration_acceptance_integration_test.go:1)
- [pgbase/javascript_migrations_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migrations_integration_test.go:1)
- [pgbase/javascript_model_phase_hooks_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_model_phase_hooks_integration_test.go:1)
- [pgbase/javascript_records_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_records_integration_test.go:1)
- [pgbase/javascript_template_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_template_integration_test.go:1)
- [pgbase/lifecycle_hooks_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks_integration_test.go:1)
- [pgbase/model_lifecycle_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle_test.go:1)
- [pgbase/route_groups_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/route_groups_test.go:1)
- [pgbase/templates_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/templates_test.go:1)
- [pgbase/ui_extension_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/ui_extension_integration_test.go:1)

### 부재 검색

pgbase/, internal/jsvm/, sdk/javascript/에서 ClearBootstrap/clearBootstrap/OnBootstrapClear/onBootstrapClear/OnClearBootstrap/onClearBootstrap, OnComplete/onComplete, bindBody, setCookie, flush, $template, LoadFiles 및 일반 query API 이름을 검색했다. 단순 이름 검색만으로 끝내지 않고 최종 globals override, event 객체 생성, app control dispatch, migration 타입/runner 분기를 읽었다. OnCompleteHistory라는 이름의 무관한 테스트와 Go HTTP flush 테스트는 public tx callback 또는 JS RequestEvent 구현으로 세지 않았다.

## 남은 검증

현재 판정은 실제 소스와 테스트 존재를 대조한 결과다. 다음 실행 검증이 필요하다면 (1) e.Next 전후 순서/미호출 short circuit, (2) 두 pending migration 중 둘째 실패 시 첫째 잔존 여부, (3) tx completion callback 부재와 fluent query 예상 사용 예, (4) S3 생성자 인자에 의한 backend 전환 여부를 직접 테스트하면 차이를 빠르게 재현할 수 있다. 이번 작업에서는 새 테스트나 수정 코드를 만들지 않았다.
