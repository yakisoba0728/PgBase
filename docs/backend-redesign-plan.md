# 백엔드 전체 재설계 실행 계획

이 계획은 PostgreSQL 기반 독립 제품인 pgBase의 백엔드를 재설계하고, 데이터 보존·권한·장애 복구·클라이언트까지 검증하며 전체 백엔드를 새 구조로 재작성하기 위한 실행 순서다. 우선순위는 코드 구조·책임 분리·중복 및 불필요한 코드 제거다. 처리량·지연 최적화와 벤치마크는 후순위로 두며 재작성의 선행 조건이나 이번 완료 게이트로 요구하지 않는다. PocketBase는 제품 참고 사례다. SQLite 동작이나 PocketBase API·내부 구조의 재현을 완료 조건으로 삼지 않는다.

현재 `codex/postgres-backend-redesign`의 미커밋 변경은 선행 구현이다. [현재 구현 기록](backend-redesign.md)의 테스트 결과는 해당 실행 범위의 근거이며, 전체 재설계의 최종 인수를 대신하지 않는다. 계획 수립에는 데이터·인증·스토리지/비동기·런타임/확장 담당 에이전트 4개가 읽기 전용으로 참여했다.

계획 범위의 구현·독립 검토·통합 인수를 완료했다. 검증된 기존 기반은 재사용했고 성능 튜닝·운영 변경은 별도로 유지했다. 현재 실행 결과는 [백엔드 재작성과 통합 검증](backend-rewrite-validation.md)에 기록한다. 최초 실패와 수정 후 package 재검증을 포함해 지원 DB별 전체 package 결과를 대조한다.

## 제품과 설계 기준

1. 기존 pgBase 데이터와 핵심 기능은 보존한다. 외부 계약을 변경할 때는 실제 호출자, 새 계약, 전환 방법을 같은 작업에서 처리한다. PocketBase 호환성 자체는 필수 제품 요구사항이 아니다.
2. 기본 실행 경로는 pgBase native 계약만 사용한다. 외부 제품의 구문·응답·JS 전역을 유지할 필요가 있으면 선택 어댑터로 분리한다. 요청되지 않은 importer나 호환 프레임워크를 새로 만들지 않는다.
3. 기존 파일 배치나 내부 인터페이스를 보존해야 한다는 전제 없이 백엔드 전체를 새 구조로 재작성한다. 검증된 불변식·동작·테스트는 재사용하고 새 구조에 필요한 구현을 작성한다. 현재 Go 프로세스와 PostgreSQL을 기본 구성으로 유지한다. 미세서비스, 별도 메시지 브로커, DI container, generic repository, 새로운 ORM 도입을 전제하지 않는다. 기존 구현을 재사용하되 책임과 비용이 잘못된 부분은 교체한다.
4. PostgreSQL 타입·제약·인덱스·SQL 계획·잠금·격리 수준·writer fencing을 설계 근거로 사용한다. 복잡하다는 이유만으로 다중 writer, 커서, revision, outbox의 안전장치를 없애지 않는다.
5. 파일 이동·이름 변경·wrapper 추가·줄 수 감소만으로 작업을 완료하지 않는다. 책임과 의존 방향이 명확해지고 중복·불필요한 구현이 제거되어야 한다. 실행 의미와 실패 동작을 검증한다.
6. 운영 환경의 데이터 변경·백업 예약·배포·패키지 공개는 이 구현 계획의 실행 범위에 포함하지 않는다. 업그레이드·복원·백업 기능의 로컬 인수는 필수다.

## 목표 경계

| 경계 | 소유하는 책임 | 소유하지 않는 책임 |
| --- | --- | --- |
| HTTP·CLI·Go embedding·JS host | 입력 검증, 호출 신원, 응답과 오류 매핑 | 별도 레코드 쓰기·규칙 의미·중복 트랜잭션 |
| 레코드 서비스 | 레코드·관계·ID·revision·소유권·audit·outbox·idempotency의 원자성 | 외부 제품의 필터/페이지/모델 형식 |
| 조회 계획 | catalog에 바인딩된 typed filter·policy·sort·projection·expand와 SQL 실행 | 응답 원문 키 존재에 따른 실행 엔진 선택 |
| 인증·정책 | 관리자·사용자 realm·서비스 신원의 서로 다른 신뢰 경계와 정책 수명 | 모든 신원을 합친 거대한 인증 서비스 |
| 스키마·membership | DDL 계획·catalog 고정·업그레이드·writer/커서 세대 | 외부 DB 파일 가져오기 |
| 파일·저장소·복구 | Stage/Commit/Cleanup, revision fence, GC, backup/restore 일관성 | DB와 S3가 단일 ACID 트랜잭션이라는 가정 |
| 비동기 처리 | outbox·jobs의 claim/lease/fence, 재시도, retention, 종료 | 외부 부작용에 대한 근거 없는 exactly-once 보장 |
| 앱 조립 | 서비스 생성, 자원 소유 이전, 시작, readiness, drain, 실패 정리, 종료 | 도메인 정책의 별도 구현 |

새 인터페이스가 필요하면 먼저 실제 호출자와 소유권을 문서화한다. 위 경계가 반드시 새 패키지·새 타입 하나씩을 요구하는 것은 아니다.

## 0단계 기준선과 범위 고정

**산출물:** 기능/호환 요소 목록, 외부 계약 변경표, 트랜잭션·권한·자원 불변식, 재현 가능한 동작·데이터 기준선, 검토한 선행 구현의 기준 commit과 patch·untracked manifest.

- tracked/untracked 파일과 내장 UI 산출물까지 포함해 현재 변경을 검토한다. 기존 변경을 덮거나 `git reset`으로 기준선을 만들지 않는다.
- 각 기능을 `pgBase 기본 기능 / 이전 저장 형식 읽기 / 선택 외부 어댑터 / 삭제 가능한 미사용 코드`로 분류한다. 텍스트 ID·관계·schema transfer·host 보안 정책을 이름만 보고 제거하지 않는다.
- schema 66의 기존 설치 fixture는 실제 기존 릴리스/commit으로 생성한 DB·파일·이전 `pocketbase` raw 정책 JSON으로 고정한다. 생성 소스·manifest·checksum을 기록하고 현재 코드로 재생성하지 않는다. 별도로 최신 fresh-install fixture를 만든다. 사용자·realm·정책·관계·외부 ID·파일 참조·revision·audit/outbox·jobs를 포함한다.
- HTTP·Go·JS·CLI·TypeScript·Dart·UI의 실제 호출자를 연결해 계약 표를 만든다. 현재 API의 `expression` 변경도 이 표에 포함한다.
- 기존 회귀를 완료 조건에 매핑한다. 내부 함수명·메서드 수·LOC만 고정한 테스트는 새 설계와 충돌하는지 검토하고, 필요한 경우 의존 방향·동작·자원 예산 검증으로 교체한다. 실패를 감추려고 테스트나 skip 정책을 약화하지 않는다.
- 현재 동작의 특성 테스트와 보존할 데이터 계약을 고정한 다음 재작성에 들어간다. 성능 fixture·수치 목표·벤치마크는 착수 조건에서 제외한다.

**종료 게이트:** 유지할 기능, 바꿀 외부 계약, 검사할 불변식이 모두 작업 ID에 연결되고 선행 구현의 알려진 미검증 경로가 아래 기반 작업에 배정되어야 한다.

## 1단계 공통 기반 확정

데이터와 정책의 공유 경계부터 구현한다. 이 단계의 공통 파일은 동시에 수정하지 않는다. 다른 에이전트는 fixture·특성 테스트·호출자 조사를 병렬로 준비한다.

| ID | 구현할 계약 | 확인된 출발점 | 완료 게이트 |
| --- | --- | --- | --- |
| CORE-01 | 트랜잭션 소유자, 대상 앱/DB, 격리 수준, 재시도, commit/rollback, 완료 훅 책임 | `database/tx.go`의 공용 컨텍스트와 `recordops/{service,batch,truncate,hooks}.go` | borrowed CRUD/list/get/count/batch/truncate가 같은 계약을 따름. 독립 commit 금지. 지원할 수 없는 조합은 실행 전에 명시적으로 거부. 다른 앱/DB 혼용·취소·commit 실패·재시도·훅 중복 회귀 통과 |
| CORE-02 | 한 작업의 catalog snapshot과 내부 UUID/외부 ID 경계 | `recordops/read.go → pinnedTx → query.inTxCatalog`의 catalog 재확인, `recordidentity.Project` | catalog 획득·바인딩 중복을 trace로 확인 후 제거. validator·ETag·cursor·outbox는 내부 ID, 외부 응답은 public ID. 투영 실패와 UUID 모양의 텍스트 ID·중복 확장 map·최대 fanout 검증. 호출자 tx에서 catalog/정책 변경 후 rollback·commit 실패가 나도 전역 cache나 다른 요청에 미커밋 상태가 노출되지 않음 |
| POLICY-01 | 원문과 분리된 명시적 typed AST 실행 의미 | `recordaccess/rule_compile.go`, `query/compiler.go`, `realtime/service.go`의 `Rule.Expression` 존재 여부 분기 | 원문을 보유하거나 제거해도 같은 AST가 같은 권한 의미를 가짐. owner/prospective create/request/관계/역관계/함수/JSON/null/empty의 기존 의미를 특성 테스트로 고정 |
| POLICY-02 | 모든 진입점의 정책 정규화와 검증 | HTTP `ParseExpressions`와 JS `extension_app_schema.go`의 `accessrule.Parse` 경로 차이 | HTTP·Go·JS·schema import가 합의한 같은 정책 입력을 같은 AST로 변환. 마지막 action 오류도 전체 무변경. 이전 원문 키 읽기와 export/import/restore roundtrip 통과 |
| JS-01 | 신규 native bundle과 기존 bundle·저장된 JS migration의 실행 모드 | 검증/실행 VM의 무조건 legacy 전역 설치와 기존 `pgbase.scripts/v1` | 신규 native 모드를 명시적으로 버전/표시. 기존 bundle의 legacy 선택과 전환 규칙, 검증·실행·historical replay의 동일 installer 선택, 기존 source/checksum 보존, replay/restore를 모두 고정 |
| LIFE-01 | 자원·외부 부작용·worker의 소유와 종료 순서 | `pgbase/app_construct.go`, `app.go`, `background.go` | 자원 획득/소유 이전/실패 정리 표, HTTP drain→worker 취소/대기→서비스 종료→pool→writer gate 순서, fatal/retryable/cancellation 분류 고정 |

위 표의 경로 차이는 정적으로 확인했다. 모든 조합의 실제 장애가 재현됐다는 뜻은 아니며, 구현 전에 재현/특성 테스트를 작성해 기대 동작과 어긋나는 경우를 확정한다.

## 2단계 영역별 병렬 구현

공유 계약이 고정되면 구현 에이전트 최대 7개, 통합 담당 1개, 독립 검토/검증 담당 최대 2개로 운영한다. 총 10개 이하로 유지하고, 경합이나 테스트 자원 부족이 생기면 동시성을 줄인다.

| 담당 | 기본 파일 소유권 | 구현 작업 | 영역 완료 조건 |
| --- | --- | --- | --- |
| A 조회·ID | `internal/query/**`, `internal/recordidentity/**` | typed plan→SQL→실행→응답 단계 정리, list/get/count 정책 일치, 집계 SQL의 페이지 SQL 문자열 절단 제거, catalog/ID 변환 비용 정리, native/legacy filter·sort·expand 경계 분리 | scalar·relation filter/sort·nested expand·SQL view·hidden field·cursor·ID·bounded total 회귀와 SQL 수/메모리 예산 통과 |
| B 쓰기·관계 | `internal/database/**`, `internal/recordops/**`, `internal/relation/**` | CRUD/batch/cascade/truncate의 공통 원자성, 중복 트랜잭션·재시도·훅 경로 제거, idempotency·revision·audit/outbox·파일 participant 연결 | deadlock/serialization·replay·cascade 경합·취소/실패·외부 트랜잭션 회귀 통과. 대량 truncate의 원자 크기 제한과 별도 장기 작업 계약을 명시하고 임의 chunking으로 의미를 바꾸지 않음 |
| C 스키마·업그레이드 | `internal/schema/**`, `internal/migrate/**`, `internal/membership/**`, 배정된 migration 파일 | PostgreSQL 타입·제약·인덱스·view 의존·DDL 잠금·catalog·writer fencing, 기존 설치 업그레이드 | 기존 checksum 불변, schema 66 upgrade와 fresh install의 결과 일치, DDL↔CRUD와 writer 종료/복귀·cursor 세대·restore 검증 |
| D 인증·권한 | `internal/adminauth/**`, `userauth/**`, `serviceauth/**`, `authpolicy/**`, `accessrule/**`, `recordaccess/**`, `identity/**`, `apiratelimit/**` | 정책 실행·입력 공통화, 서로 다른 신원의 경계 유지, 자격 증명·세션·realm 수명 정리, 미사용 평가 경로 검토 | 계정 존재 여부에 따른 KDF 경로, KDF 동시성, refresh 경쟁·revoke·realm 교차·OTP/MFA/OAuth 재사용·currentPassword·scope·CSRF/proxy/IP·비밀값 redaction 회귀 통과 |
| E 파일·저장소·복구 | `internal/filestore/**`, `internal/backup/**`, `internal/durablefile/**` | Stage/Commit/Cleanup, GC·storage migration·backup gate/lock 순서, 외부 I/O와 DB 점유 경계, durable 복구 상태 | local/S3 파일·GC publication 경합·revision/lease 유실·취소·중단/재시작·손상 archive·복원 target 분리 검증. 참조 객체 삭제와 DB/파일 불일치 없음 |
| F 비동기·관측 | `internal/realtime/**`, `internal/jobs/**`, `internal/observability/**`, 배정된 lifecycle 구현 | outbox 순서·claim/publish/ack·retention, jobs at-least-once/fence·재시작, 로그 큐/drop/flush, backlog·pool wait 등 필요한 계측 | publish/ack 사이 장애, stale worker·lease expiry·dead-letter retry·slow subscriber·정책 폐기·handler panic/취소 무시·DST·queue 포화/종료 회귀 통과 |
| G 확장·호스트 | `internal/jsvm/**`, `pgbase/javascript_*`, 배정된 `pgbase/extension_*` | native host와 legacy installer 분리, host 파일/HTTP/명령/프로세스 정책을 native 소유자로 이동, manifest route/hook/job/migration 유지 | native VM이 legacy 전역을 자동 설치하지 않음. 양쪽 경로가 같은 권한·actor·취소·시간/호출/입출력/stack 제한을 적용. allocation/peak-memory는 측정 예산으로 검증하고 강제 heap 상한으로 표현하지 않음. VM 검증/실행·queue 포화·transaction outcome·종료 회귀 통과 |

표에 명시되지 않은 파일은 통합 담당이 기본 소유하며, 병렬 수정 전에 작업 ID와 파일 단위로 소유권을 배정한다. 권한 컴파일러 연결부처럼 두 담당이 만나는 파일은 1단계에서 먼저 고정한다. 이후 `query/access_rule.go`, `query/compiler.go`, `realtime/service.go`의 변경 권한은 작업별로 한 담당자에게만 배정하며 다른 담당자는 변경 요청과 회귀를 전달한다. `internal/relation`은 B가 단독 소유하고 A는 필요한 조회 API 변경을 B에게 요청한다.

다음 처리량·잠금 최적화 후보는 후순위다. 필요할 때 측정해 결정하고 현재 재작성의 완료 조건과 분리한다.

- 파일 GC의 외부 저장소 Delete가 SQL 트랜잭션 안에서 실행된다. S3 지연에 따른 connection·lock 점유를 측정한다. 분리한다면 durable claim→외부 삭제→fenced ack와 publication/backup 상호 배제를 함께 설계한다.
- realtime publish의 공통 delivery lock은 sequence·순서·prune과 연결되어 있다. 직렬화 비용을 측정하고 순서·replay·retention 계약을 만족하는 대안을 선택한다. 잠금만 제거하지 않는다.
- `EvaluateAuthRecordRule`의 순수 scalar 평가는 조사상 production 호출자가 없다. SQL 기반 특성 테스트로 옮긴 뒤 미사용 구현을 삭제할 수 있는지 확인한다. 평가 엔진을 하나 더 유지하기 위한 추상화를 만들지 않는다.

## 3단계 앱과 모든 호출자 통합

**통합 담당 단독 소유:** `pgbase/app.go`, `app_construct.go`, `background.go`, 공통 options·서비스 연결 파일, `internal/api/**`의 공통 라우터·route registry·오류 처리·도메인 handler, migration 번호/등록, 공통 CI와 문서의 상태.

영역 담당은 자신의 서비스 생성/Close 계약과 필요한 연결 변경을 통합 담당에게 전달한다. 공통 파일을 동시에 수정하지 않는다. migration 번호는 통합 담당이 순서대로 배정하며 과거 migration은 수정하지 않는다.

- 앱 자원의 획득·실패 정리·성공 시 소유 이전·정상 종료를 하나의 명시적인 목록과 대조한다. `New`가 listener/worker를 시작하지 않는 계약, `Start`와 caller-owned HTTP server, `Run`의 listener 소유 경로를 유지한다.
- HTTP는 기존 route 선언의 access/response/deadline 경계를 유지하며 서비스 호출과 오류 매핑에 집중한다. 전체 라우터를 새 프레임워크로 교체하지 않는다.
- 서비스 계약이 안정되면 구현 에이전트를 재배치해 다음을 병렬 처리한다: **CLI/config/Go embedding**, **TypeScript SDK**, **Dart SDK**, **관리자 UI**, **업그레이드/복원 fixture**.
- env/flag 우선순위, CLI exit code·출력 golden·terminal 취소/echo 복원, Go 공개 옵션과 trusted transaction, SDK 오류/정밀도/취소/realtime, UI의 실제 기능 흐름을 함께 갱신한다.
- JS는 신뢰된 확장 모델을 유지한다. 현재 Goja가 제공하지 않는 강제 heap 상한이나 미신뢰 코드 실행·새 프로세스 격리를 이번 완료 조건으로 추가하지 않는다. 기존 입출력·stack·JSON depth·동시성·취소 제한은 유지하고 메모리 회귀를 측정한다.
- 신규 native bundle은 명시적 실행 모드/버전을 갖고, 기존 `pgbase.scripts/v1` bundle은 명시적으로 선택한 legacy adapter에서만 실행한다. 검증 VM과 실행 VM은 같은 installer를 선택한다. 저장된 역사 JS migration은 기록된 실행 모드를 사용하고, 모드가 없는 기존 기록은 legacy-v1로 해석한다. 원본 source와 checksum은 바꾸지 않는다. 필요한 adapter가 없으면 데이터 적용 전에 명확히 실패해야 하며 현재 기본 설정으로 과거 소스의 의미를 바꾸지 않는다. 이 버전/모드 메타데이터의 저장·업그레이드·복원은 C와 G가 JS-01에서 함께 확정한다.
- 기존 `$app`·`Record`·`$os`·`$filesystem`, 호환 페이지 API와 SDK 호출은 기능별 이행표에 따라 native 또는 선택 어댑터로 옮긴다. pgBase 자체 schema transfer와 외부 SQLite 데이터 importer는 분리한다. 새 importer 구현은 이번 재설계에 끼워 넣지 않는다.

**종료 게이트:** native 서비스가 legacy installer/adapter에 의존하지 않고, 어댑터를 제거하는 빌드/fixture에서도 native 핵심 인수가 통과해야 한다. 외부 계약을 바꾼 항목에는 누락된 호출자가 없어야 한다.

## 4단계 독립 검토와 동작 인수

구현하지 않은 에이전트가 구조·책임·의존 방향·중복·미사용 코드와 트랜잭션·권한·동시성·장애 경계를 검토한다. 별도 검증 담당은 이전 동작과 새 구현을 비교한다. 발견한 필수 수정은 파일 소유자가 해결하고 관련 회귀를 다시 수행한다.

완료 기준은 새 구조에서 기능·데이터·권한·오류·취소·복구 계약이 연결되고, 대체된 이전 내부 구현과 임시 연결 코드가 제거되는 것이다. 기존 구현을 그대로 둔 채 새 facade만 추가하는 상태로 끝내지 않는다. 이전 저장 형식을 읽는 최소 변환과 명시적으로 유지할 선택 어댑터만 남긴다.

처리량·지연·SQL 계획의 성능 측정은 재작성 이후 별도 작업으로 둔다. 기존 보안 제한·응답 크기·동시성·실행 시간·자원 예산과 그 회귀는 계속 유지한다.

## 5단계 최종 완료 게이트

아래 항목이 모두 통과해야 전체 재설계를 완료로 표시한다. 부분 통과·허용되지 않은 skip·남은 연결 TODO를 전체 완료로 보고하지 않는다.

- [x] 기능·외부 계약·legacy 이행표의 모든 항목에 구현/삭제/선택 어댑터 처리와 검증 근거가 있다.
- [x] 트랜잭션 소유권·정책 의미·내부/외부 ID·DDL/잠금·outbox·파일 publication·자원 종료 불변식이 모든 진입점에서 검증된다.
- [x] Go formatting·blocking lint·vet·전체 unit/race와 skip 정책이 통과한다.
- [x] 기존 Security and supply chain workflow의 reachable Go vulnerability 검사·npm audit·생산 image scan·SBOM과 해당 보안 정책 gate가 통과한다. 독립 검토는 권한·자격 증명·입력 경계·비밀값·host capability 변경도 확인한다.
- [x] PostgreSQL 16/17/18 지원 matrix 전체 integration을 통과하고 주 검증 버전의 전체 race suite를 통과한다. patch/image는 저장소에 고정된 버전을 사용하고 바꾸면 별도 검증한다.
- [x] schema 66→최신 upgrade와 fresh install, local/S3 restore, concurrent DDL/CRUD·writer fencing·취소·프로세스 중단/재시작·자원 누수 검증이 통과한다.
- [x] TypeScript/Dart unit·실제 HTTP conformance, 관리자 UI typecheck/test/build·전체 브라우저 E2E, CLI·Go embedding·native/legacy JS 인수가 통과한다.
- [x] 동일 후보 소스/산출물로 검증하고 내장 UI bundle·CLI/container·migration의 대응 관계를 확인한다.
- [x] 대체된 이전 내부 구현·중복·미사용 코드·임시 연결 TODO를 정리했고, 미해결의 데이터 손실·권한 우회·독립 commit·잘못된 재시도·참조 객체 삭제·이벤트 누락·종료 누수 문제가 없다.
- [x] 독립 검토에서 발견한 필수 수정이 닫혔고, 변경 계약·업그레이드·복원 방법·롤백 가능 범위·운영 설정 문서가 현재 소스와 일치한다.

이 계획의 완료 산출물은 검증된 소스와 출시 가능한 후보 및 근거다. 운영 배포·릴리스 게시·운영 백업 설정은 수행했다고 표시하지 않는다.

후속 독립 검토에서 최초 완료 판정에 빠진 결함이 발견되었다. 위 체크리스트의 최초 검증 기록과 구분하여 [독립 검토 결과](backend-independent-review.md)에 수정과 최종 검증 근거를 기록한다.

## 에이전트 실행 규칙

- 토큰을 아끼기 위해 작업에 필요한 파일과 직접 호출자만 읽는다. 같은 파일·전체 문서·전체 diff를 반복해서 읽거나 대량 출력하지 않는다.
- 역할과 파일 범위가 분리된 구현만 병렬로 맡긴다. 조사·검토를 같은 범위로 중복 실행하지 않으며, 검토 담당은 구현과 별개로 필요한 변경 경계만 확인한다.
- 보고는 변경 파일·계약 변경·검증 결과·남은 문제로 짧게 제한한다. 동일 상태 보고, 전체 계획 재출력, 불필요한 성능 조사와 새 추상화는 생략한다.

- 각 작업은 `작업 ID / 담당 / 선행 작업 / 허용 파일 / 계약 변경 / 필요한 회귀 / 산출물 / 미해결 위험`을 가진다. 상태는 준비→구현→검토→통합 검증→완료로 관리한다.
- 기본은 현재 checkout과 파일 소유권을 이용한다. 독립 빌드나 충돌 해결에 실제로 필요할 때만 별도 worktree를 만든다. 다른 에이전트의 수정·브랜치·fixture를 reset/정리하지 않는다.
- 공유 타입·migration·전역 설정·앱 조립·공통 API 파일을 임의로 수정하지 않는다. 담당자를 바꿀 때는 진행 중 diff와 계약을 전달하고 소유권을 명시적으로 넘긴다.
- 영역 담당은 필요한 집중 테스트를 돌리고, 전체 suite는 통합 검증 담당이 실행한다. 모든 에이전트가 같은 DB·S3 fixture와 CPU를 동시에 점유하지 않도록 프로젝트·포트·DB를 분리하고 자원 한도를 둔다.
- 빠른 피드백은 기존 CI shard와 skip-audit 도구를 재사용한다. 테스트 timeout·skip·예산을 느슨하게 바꿔 통과시키지 않는다.
- 실패 시 작업 ID·재현 조건·관측 결과·수정 파일·검증 결과를 기록한다. 같은 실패를 다른 에이전트가 중복 조사하지 않도록 공유한다.
- 후속 단계가 남았으면 계속 다음 작업으로 넘어간다. 영역 에이전트의 완료 보고는 전체 완료가 아니며 통합 담당이 최종 게이트를 확인한다.

실제 실행 순서는 **기준선 → 공통 트랜잭션/정책/수명 계약 → 7개 영역 병렬 구현 → 앱·클라이언트 통합 → 독립 검토·동작/장애 인수 → 최종 전체 게이트**다.
