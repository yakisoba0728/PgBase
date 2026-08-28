# PgBase 전체 코드베이스 감사 보고서

- 감사일: 2026-08-28
- 구현 대상: `codex/production-kernel` (`5fea270fa2e2cf241b88a862f60b71e17a445ee1`)
- 공식 Codex Security 대상: 루트 `main` (`52599346b94c9a609b6a784cdfc780efbdd649d8`)
- 방식: 14개 병렬 조사 트랙, 공식 단일 패스 Codex Security 스캔, 수동 데이터 흐름 검토, 빌드·테스트·정적 분석·의존성 검사, 선택적 런타임 재현
- 최초 감사 시점의 코드 변경: 없음. 아래 12절은 같은 브랜치에서 수행한 후속 수정과 재검증 결과다.

> **종결 상태 (2026-08-28):** 확정 발견 사항 28건은 모두 수정·회귀 검증했다. 원래의 1~11절은 수정 전 증거와 판단을 보존하며, 현재 구현 상태는 12절을 기준으로 한다.

## 1. 결론

실제 구현 워크트리에서 **High 4건, Medium 13건, Low 11건(총 28건)**을 확인했다. 가장 먼저 고쳐야 할 문제는 다음 네 가지다.

1. 인증 실패 제한을 이메일 변경으로 우회할 수 있어 Argon2와 데이터베이스를 이용한 비인증 DoS가 가능하다.
2. 비밀번호 재설정과 동시 로그인 사이의 경쟁 조건으로 재설정 후에도 기존 비밀번호로 만든 세션이 살아남을 수 있다.
3. 새 설치에서 브라우저 관리자 설정 화면으로 진입할 수 없다.
4. 공식 복구 문서의 `pg_restore` 명령이 대상 DB를 지정하지 않아 실제 복구를 하지 않는다.

SQL/식별자 주입, 확인된 XSS, IDOR, CSRF 우회, 평문 세션 토큰 저장, 마이그레이션 원자성 파괴는 찾지 못했다. 의존성 검사에서도 현재 실행 경로에 도달 가능한 알려진 취약점은 확인되지 않았다. 다만 이는 해당 리비전과 검사 범위에 대한 결과이지, 결함이 전혀 없다는 보증은 아니다.

## 2. 범위와 중요한 구분

저장소 루트의 `main`은 `.gitignore`와 설계 문서 2개만 추적한다. 실제 Go/React 구현 147개 파일은 연결된 워크트리 `.worktrees/production-kernel`에 있다.

- 공식 Codex Security 스캔은 정책상 루트 `main`의 추적 파일 3개만 검사했고, 발견 사항 0건·커버리지 3/3으로 완료됐다.
- 실제 제품 감사는 구현 워크트리를 별도 대상으로 삼아 모든 추적 소스, 테스트, 배포 및 CI 파일을 조사했다.
- 따라서 “공식 스캔 0건”은 구현에 문제가 없다는 뜻이 아니다. 구현은 공식 스캔 리비전의 범위 밖이었고, 아래 28건은 병렬 구현 감사에서 확인됐다.

공식 스캔 ID는 `18c83a48-edfd-45a5-82a6-80142e3c48bc`다. Threat Analysis Connector는 연결되지 않아 자문 단계가 생략되었으며, 이 제한은 공식 스캔에도 기록했다.

## 3. 최초 감사 검증 결과 (수정 전)

| 검사 | 결과 |
|---|---|
| `go test ./...` | 통과 |
| `go vet ./...` | 통과 |
| `go test -race ./...` | 로컬 CGO 비활성화로 실행 불가; CI에는 PostgreSQL 기반 race 작업이 있음 |
| `npm run typecheck` | 통과 |
| `npm test` | 통과, 5개 파일·24개 테스트 |
| `npm audit --json` | 취약점 0건, 158개 의존성 |
| `govulncheck -show verbose ./...` | 호출 가능한 심볼/패키지 취약점 0건 |
| 런타임 비밀정보 재현 | 더미 `sslpassword`가 시작 실패 JSON 로그에 그대로 노출됨 |
| Git 상태 | 검사 전후 깨끗함; 본 보고서 추가만 별도 변경 |

`govulncheck`는 `golang.org/x/crypto/openpgp`의 모듈 수준 공지 GO-2026-5932를 표시했지만, PgBase는 해당 패키지를 import하거나 호출하지 않는다. 현재 리비전에서 도달 가능한 취약점으로 분류하지 않았다.

## 4. High 발견 사항

### AUD-H01 — 이메일 회전으로 인증 제한 우회 및 Argon2/DB 자원 고갈

로그인과 초기 설정은 공개 엔드포인트다. 실패 버킷의 키가 `HMAC(email + client address)`이므로 공격자는 유효한 형식의 존재하지 않는 이메일만 계속 바꾸면 이메일당 5회 제한을 우회할 수 있다. 각 시도는 존재하지 않는 계정에도 고비용 Argon2id 더미 검증을 수행하고 실패 행을 저장한다. 현재 Argon2 설정은 64 MiB, 4 lanes다.

- 영향: 인증 없이 CPU·메모리·DB 연결·실패 테이블을 동시에 고갈시켜 서비스 거부를 유발할 수 있다.
- 근거: `internal/api/router.go:39-42`, `internal/adminauth/service.go:174-205`, `internal/adminauth/token.go:32-34`, `internal/adminauth/password.go:17-21`, `internal/adminauth/password.go:45-58`, `internal/adminauth/store.go:175-201`.
- 권고: IP/네트워크, 계정, 전역 제한을 분리하고 KDF 동시 실행 세마포어를 둔다. 존재하지 않는 계정도 동일 응답 시간을 유지하되, 실패 행의 개수와 보존 기간에 상한을 둔다. 프록시/엣지 제한을 함께 적용한다.
- 분류: CWE-307, CWE-400.

### AUD-H02 — 비밀번호 재설정과 로그인 경쟁으로 재설정 후 세션 보존

로그인은 관리자 행을 잠그지 않고 이전 해시를 읽은 뒤, 트랜잭션 밖에서 비밀번호를 검증하고 새 세션을 삽입한다. 비밀번호 재설정은 다른 트랜잭션에서 비밀번호를 갱신하고 기존 세션을 삭제한다. 다음 순서가 가능하다.

1. 로그인 요청이 이전 비밀번호 해시를 읽는다.
2. 재설정이 새 비밀번호를 저장하고 기존 세션을 모두 삭제한 뒤 커밋한다.
3. 로그인 요청이 이전 해시 검증을 마치고 새 세션을 삽입한다.

그 결과 재설정이 끝난 후에도 이전 비밀번호로 인증된 세션이 유효해질 수 있다.

- 영향: 이전 비밀번호를 아는 공격자가 관리자 재설정 후에도 접근권을 유지할 수 있다.
- 근거: `internal/adminauth/store.go:81-97`, `internal/adminauth/service.go:182-224`, `internal/adminauth/service.go:142-166`, `internal/adminauth/store.go:60-70`.
- 권고: 관리자별 행 잠금 또는 advisory lock으로 비밀번호 변경과 세션 발급을 직렬화한다. 비밀번호/자격 증명 버전을 세션에 기록해 인증 시 비교하는 방어도 추가한다. 이 순서를 강제하는 동시성 테스트가 필요하다.

### AUD-H03 — 새 설치에서 브라우저 관리자 설정 경로가 도달 불가

프론트엔드는 `/api/admin/session`이 `404 setup_required`를 반환할 때만 초기 설정 화면으로 전환한다. 그러나 해당 GET 라우트는 인증 미들웨어 안에 있어 쿠키가 없는 새 설치에서는 핸들러에 도달하기 전에 401을 반환한다. 백엔드에는 프론트가 기대하는 `setup_required` 생산 경로도 없다.

- 영향: README와 빠른 시작 문서가 약속한 브라우저 온보딩이 새 설치에서 막힌다. 직접 API 또는 CLI를 아는 사용자만 우회할 수 있다.
- 근거: `internal/api/router.go:43`, `internal/api/admin_middleware.go:61-64`, `ui/admin/src/auth/session.tsx:48-55`.
- 권고: 인증 전용의 최소 상태 엔드포인트를 두거나, 세션 엔드포인트가 관리자 미설정 상태를 안전하게 구분해 반환하도록 계약을 통일한다. 빈 DB에서 실제 브라우저까지 잇는 E2E 테스트를 추가한다.

### AUD-H04 — 복구 문서의 `pg_restore`가 DB에 복구하지 않음

`docs/backup-restore.md`는 `PGSERVICE=pgbase-restore`를 설정한 뒤 `pg_restore`에 `-d/--dbname`을 전달하지 않는다. PostgreSQL 공식 문서상 DB 이름이 없으면 `pg_restore`는 DB에 직접 연결해 복구하지 않고 SQL 스크립트를 표준 출력으로 보낸다.

- 영향: 명령이 성공처럼 보이더라도 대상 DB는 비어 있을 수 있다. 출력이 캡처되면 COPY 데이터, 비밀번호 해시, 세션 해시가 터미널/CI 로그에 노출될 수 있다. 업그레이드 절차도 이 복구 경로에 의존한다.
- 근거: `docs/backup-restore.md:78-84`, [PostgreSQL pg_restore 문서](https://www.postgresql.org/docs/current/app-pgrestore.html).
- 권고: 예를 들어 `pg_restore --dbname='service=pgbase-restore' --single-transaction ...`처럼 대상을 명시하고, 빈 임시 클러스터에 실제 복원한 뒤 행 수·스키마·로그 비노출을 검증하는 자동 복구 훈련을 추가한다.

## 5. Medium 발견 사항

### AUD-M01 — 요청 본문 읽기 제한시간 부재

서버는 `ReadHeaderTimeout`만 설정하고 본문에는 크기 제한만 둔다. 엄격한 JSON 디코더는 EOF까지 기다리므로 공격자가 로그인/설정 같은 공개 라우트의 본문을 매우 천천히 보내 연결과 goroutine을 점유할 수 있다. `ReadTimeout` 또는 요청별 body deadline, `IdleTimeout`, 프록시 제한을 함께 설정해야 한다. 근거: `internal/pgbase/app.go:220-224`, `internal/api/middleware.go:107-113`, `internal/api/admin_handlers.go:163-180`.

### AUD-M02 — `sslpassword`가 시작 오류 로그에 노출

DSN 오류 정리 목록이 `password`, `token`, `secret`만 다루고 `sslpassword`는 빠뜨린다. pgx 파싱 오류가 원래 DSN을 포함하며 상위 CLI가 이를 JSON 로그로 기록한다. 더미 sentinel을 사용한 런타임 재현에서 전체 `sslpassword`가 출력되는 것을 확인했다. 구조적으로 DSN을 파싱해 허용 필드만 기록하고, 드라이버 오류를 외부에 기록하기 전 안전한 타입으로 감싸야 한다. 근거: `internal/observability/redact.go:10-15` 및 시작 오류 로깅 경로. 분류: CWE-532.

### AUD-M03 — `pool_max_conns=1` 허용 시 연결 고갈/교착

설정은 풀 크기 1을 허용하지만 스키마 listener가 전용 연결 하나를 장기 보유한다. 그 결과 일반 요청, readiness, 정리 작업이 연결을 얻지 못한다. `doctor`도 catalog rows를 열어 둔 상태에서 같은 풀에 중첩 질의해 단일 연결에서 멈출 수 있다. 최소 풀 크기를 강제하거나 listener에 별도 풀/연결을 제공하고, 중첩 질의 전에 rows를 닫거나 결과를 materialize해야 한다. 근거: `internal/schema/listener.go:95-130`, `internal/doctor/checks.go:69-104`.

### AUD-M04 — 일시적 정리 작업 오류가 전체 서버 종료를 유발

인증 실패 정리기는 일시적인 DB/lock/query 오류에도 즉시 반환한다. 앱은 백그라운드 컴포넌트 하나의 첫 반환을 치명적 오류로 보고 전체 서버를 종료한다. 이는 일시적 DB 장애에서 회복한다는 설계 목표와 충돌한다. 오류를 분류해 지수 백오프로 재시도하고, 명시적인 비복구 오류만 프로세스 종료로 승격해야 한다. 근거: `internal/adminauth/cleaner.go:35-47`, `internal/pgbase/app.go:355-377`, `internal/pgbase/app.go:272-284`.

### AUD-M05 — UI와 백엔드의 스키마 기본값 문법 불일치

UI는 boolean에서 정확히 `"true"`가 아닌 모든 문자열을 false로 바꾸고, integer를 JavaScript `Number`로 보내지만 백엔드는 정수 기본값을 문자열로 요구한다. timestamp의 `now` 객체는 문자열이 되며, 빈 문자열은 UI에서 “기본값 없음”과 구분할 수 없다. 그 결과 preview/apply가 실패하거나 의도와 다른 스키마를 만들 수 있다. 공유된 타입/파서와 문법별 UI 컨트롤을 사용하고 계약 테스트를 추가해야 한다. 근거: `ui/admin/src/pages/collection-create-page.tsx:9-17`, `ui/admin/src/pages/collection-create-page.tsx:90-91`, `internal/schema/defaults.go:22-105`, `internal/schema/defaults.go:150-156`.

### AUD-M06 — JSONB의 큰 숫자가 관리자 UI 왕복 중 손상

브라우저는 API 응답을 `Response.json()`으로 파싱하고 UI는 JSONB를 `JSON.stringify`/`JSON.parse`로 편집한다. PostgreSQL과 백엔드는 원시 JSON 숫자를 보존하지만 JavaScript는 `9007199254740993`을 반올림하고 `1e400` 같은 값을 비유한 값 또는 `null`로 바꿀 수 있다. 편집 후 저장하면 원본 데이터가 조용히 손상된다. lossless JSON parser/문자열 편집 또는 서버 측 원문 보존 비교가 필요하다. 근거: `ui/admin/src/api/client.ts:67-69`, `ui/admin/src/pages/record-edit-page.tsx:13-16`, `ui/admin/src/pages/record-edit-page.tsx:29-48`, `internal/query/codec.go:118-122`, `internal/query/codec.go:239-246`.

### AUD-M07 — 레코드 목록의 무인덱스 정렬과 응답 메모리 증폭

모든 목록은 `(created_at, id)`로 필터·정렬하지만 컬렉션 생성 시 해당 복합 인덱스를 만들지 않는다. 또한 최대 201개의 전체 레코드를 한꺼번에 materialize하므로 레코드당 1 MiB라면 응답 하나가 약 200 MiB까지 커질 수 있다. 큰 테이블에서 전체 sort와 메모리 압박이 결합된다. 복합 인덱스를 기본 생성하고 행 수뿐 아니라 응답 바이트 예산을 제한해야 한다. 근거: `internal/schema/planner.go:86-117`, `internal/query/compiler.go:96-123`, `internal/query/service.go:61-73`.

### AUD-M08 — 애플리케이션 시계로 인한 감사시각 역행과 커서 누락

생성·수정 시각을 각 프로세스의 `time.Now()`로 생성한다. 다중 인스턴스의 시계가 어긋나면 `updated_at`이 역행하고, 이미 발급한 `(created_at,id)` 커서보다 과거 시각으로 삽입된 레코드는 이후 페이지 순회에서 영구 누락될 수 있다. DB 시계를 단일 기준으로 사용하거나 단조성/순서를 DB에서 보장해야 한다. 근거: `internal/query/service.go:30-38`, `internal/query/service.go:102-106`, `internal/query/service.go:119`, `internal/query/compiler.go:96-105`.

### AUD-M09 — 재시도 가능한 PostgreSQL/전송 오류가 일관되지 않게 500 처리

오류 분류는 serialization failure `40001`, deadlock `40P01`, 연결/전송 단절을 다루지 않는다. repeatable-read 트랜잭션에서 발생할 수 있는 이 오류들은 unknown 500으로 매핑되고 재시도 가능성도 전달되지 않는다. SQLSTATE와 네트워크 오류를 명시적으로 분류해 409 또는 503과 `Retry-After` 정책으로 매핑하고 장애 주입 테스트를 추가해야 한다. 근거: `internal/database/errors.go:30-43`, `internal/query/service.go:143-157`, `internal/api/record_handlers.go:151-174`.

### AUD-M10 — 공개 `App.Handler()` 사용자 정의 호스팅 계약이 동작하지 않음

`Handler()`는 HTTP handler만 반환하지만 readiness 전환, schema sync/listener, cleaner 시작은 `Run()` 안에서만 수행된다. 사용자가 반환된 handler를 자체 서버에서 제공하면 영원히 ready가 되지 않고 백그라운드 기능도 없다. 이후 `Run()`을 호출하면 두 번째 listener를 만든다. lifecycle의 `Start/Close`와 transport serving을 분리하거나 `Handler()`를 비공개/문서상 비지원으로 바꿔야 한다. 근거: `internal/pgbase/app.go:229-230`, `internal/pgbase/app.go:245-250`, `internal/pgbase/app.go:324-330`, `internal/pgbase/app.go:348-383`.

### AUD-M11 — `doctor --json`이 초기 DB 연결 실패 시 JSON을 출력하지 않음

DB open 실패에서 명령이 JSON report 생성 경로 전에 반환한다. 자동화가 구조화된 출력을 요청했어도 일반 오류/종료만 받아 설계 계약을 지키지 못한다. 모든 실패 단계를 `doctor.Report`로 정규화하고 stdout에는 항상 유효한 JSON 하나만 출력해야 한다. 근거: `internal/cli/run.go:153-159`.

### AUD-M12 — planner가 PostgreSQL이 적용할 수 없는 계획을 성공으로 반환

필드 수 상한이 없어 시스템 열 3개를 더했을 때 PostgreSQL 1600열 한계를 넘길 수 있다. 임의 길이 numeric 기본값과 극단적인 JSON 숫자도 preview/checksum 단계는 통과하지만 DDL apply에서 실패해 500이 된다. 원자적 롤백 때문에 부분 스키마는 남지 않지만 plan/apply 계약이 깨진다. 필드 수, numeric 정밀도·스케일·길이, JSON 숫자 범위를 계획 단계에서 검증해야 한다. 근거: `internal/schema/planner.go:26-57`, `internal/schema/planner.go:86-104`, `internal/schema/defaults.go:101-124`, `internal/schema/defaults.go:171-186`, `internal/schema/service.go:71-96`, `internal/api/schema_handlers.go:168-183`.

### AUD-M13 — 잘못된 Unicode/NUL 처리로 조용한 치환 또는 DB 500

표준 `encoding/json`은 잘못된 UTF-8과 단독 surrogate를 U+FFFD로 치환할 수 있다. text codec은 엄격한 UTF-8과 NUL을 검증하지 않고, JSONB 검증 경로는 원시 바이트를 유지한다. 따라서 서로 다른 입력이 같은 문자열로 합쳐지거나 PostgreSQL이 NUL을 거부해 500이 될 수 있다. decoder 전에 UTF-8을 검사하고 surrogate/NUL 정책을 명시해 400으로 반환해야 한다. 근거: `internal/query/codec.go:92-97`, `internal/query/codec.go:118-122`, `internal/query/codec.go:263-271`, `internal/query/codec.go:284-292`.

## 6. Low 발견 사항

| ID | 발견 사항 | 근거 및 권고 |
|---|---|---|
| AUD-L01 | 설계상 세션 TTL은 12시간보다 짧게만 조정되어야 하지만 구현은 더 긴 값을 허용한다. | `internal/pgbase/options.go:35-37`, `internal/pgbase/options.go:82-84`; 최대값을 검증한다. |
| AUD-L02 | 커서가 컬렉션에 바인딩되지 않고 만료도 없어 다른 컬렉션에서 재사용하면 행을 건너뛸 수 있다. | `internal/query/cursor.go:21-29`, `internal/query/cursor.go:61-101`; collection ID와 expiry를 MAC payload에 포함한다. |
| AUD-L03 | 만료·폐기된 관리자 세션을 삭제하는 작업이 없어 테이블이 계속 증가한다. | 세션 store/cleaner 경로; 실패 제한 정리와 별도로 세션 보존 정책을 구현한다. |
| AUD-L04 | 마이그레이션 “applied” 로그가 바깥 트랜잭션 commit 전에 기록되어 이후 실패 시 거짓 성공 로그가 남는다. | `internal/migrate/runner.go:77-82`, `internal/database/tx.go:29-32`; commit 후에 성공을 기록한다. |
| AUD-L05 | `--password-stdin`은 1024바이트 검증 전에 줄 전체를 읽어 로컬 입력으로 메모리를 크게 사용할 수 있다. | `internal/cli/password.go:23-38`, `internal/adminauth/password.go:61-64`; `LimitReader`를 사용한다. |
| AUD-L06 | UI가 세션 만료시각을 버리고 mount 때만 확인하며 전역 401 처리도 없어 만료 후 민감 데이터가 화면에 남는다. | session provider/API client; 타이머와 전역 401 logout을 추가한다. 인증 우회는 아니다. |
| AUD-L07 | 루트 `.dockerignore`가 없어 `.git`, 워크트리, `deployments/.env`가 Docker build context로 전송될 수 있다. | 현재 Dockerfile은 선택적 COPY라 이미지 포함은 확인되지 않았지만 daemon/원격 builder 노출면을 줄이도록 ignore를 추가한다. |
| AUD-L08 | lockfile의 비루트 158개 항목 중 103개가 `resolved`와 `integrity`를 모두 갖지 않는다. | 버전은 고정돼 있지만 콘텐츠 바인딩이 약하다. 사용 중인 npm 버전에서 lockfile을 재생성하고 `npm ci` 재현성을 검증한다. |
| AUD-L09 | Docker/Compose/CI의 이미지가 digest가 아닌 변경 가능한 태그에 의존한다. | 런타임·빌더·서비스 이미지를 검증된 digest로 고정하고 갱신 자동화를 둔다. |
| AUD-L10 | CI에 정기 의존성/컨테이너 스캔, SBOM, provenance/signing gate가 없다. | 현재 `npm audit`/`govulncheck` 결과는 깨끗하지만 회귀 방지를 위한 보안 gate가 필요하다. |
| AUD-L11 | `pgbase.New`가 goroutine을 시작하지 않는다고 문서화했지만 `pgxpool`은 내부 health goroutine을 시작한다. | lifecycle 계약을 실제 동작에 맞게 수정하거나 pool 생성을 start 단계로 미룬다. |

## 7. 테스트 및 품질 공백

다음은 현재 취약점 자체라기보다 위 문제를 놓치게 만든 검증 공백이다.

- 실제 백엔드와 브라우저를 잇는 E2E가 없다. 프론트 테스트는 백엔드가 만들지 않는 `404 setup_required`를 mock해 AUD-H03을 가린다.
- PostgreSQL E2E가 하나의 공유 DB와 고정 이름을 사용해 순서 의존적이며 `-shuffle` 또는 `-count=2`에서 반복 안전하지 않다.
- migration lock 테스트는 대기가 실제로 일어났는지 충분히 증명하지 않는다 (`internal/migrate/runner_test.go:67-106`).
- 관리자 라우트 테스트 일부는 단지 `!= 404`만 확인해 상태와 body 계약 회귀를 잡지 못한다 (`internal/api/admin_handlers_test.go:15-57`).
- panic recovery는 downstream이 이미 status/body를 쓴 뒤 panic하면 500으로 바꿀 수 없는데 이 경로를 검증하는 테스트가 없다 (`internal/api/middleware.go:56-74`).
- pagination live test가 동일 timestamp, 페이지 전체의 정렬·중복·누락 없는 합집합을 검증하지 않는다.
- `doctor` 실패 테스트가 실제 실패 pool을 사용하지 않아 AUD-M11과 단일 연결 교착을 잡지 못한다.
- 설계 문서에 있는 fuzz/generated security corpus가 아직 없다.

## 8. 확인된 방어와 기각한 주요 가설

- 스키마 식별자는 검증 후 pgx identifier quoting을 사용하고 값은 parameter binding한다. 확인된 SQL/식별자 주입 경로는 없다.
- 관리자 변경 라우트는 세션 인증과 CSRF/Origin 검사를 적용하며, 공유 관리자 모델 안에서 확인된 IDOR은 없다.
- 비밀번호는 Argon2id로, 세션은 원문이 아닌 해시로 저장한다.
- 마이그레이션과 스키마 apply는 advisory lock, checksum, 트랜잭션을 사용해 부분 적용을 방지한다.
- 정적 UI, CSP, 캐시 정책에서 확인된 XSS 경로는 없다.
- Docker 런타임은 non-root/distroless이며 Compose는 read-only, capability drop, no-new-privileges를 사용한다.
- GitHub Actions는 최소 권한과 SHA 고정을 사용한다.
- `npm audit`과 `govulncheck`에서 현재 실행 경로의 알려진 취약점은 발견되지 않았다.

## 9. 추가 설계 결정이 필요한 위험

아래 항목은 현재 구현의 명확한 위반으로 확정하기보다 제품 계약을 정해야 하는 위험이다.

- 레코드 생성에 idempotency key가 없어 commit 직후 응답이 끊기면 수동 재시도로 중복 생성될 수 있다.
- 수정·삭제에 optimistic concurrency/ETag가 없어 여러 관리자 작업은 last-write-wins가 된다.
- JSONB의 SQL `NULL`과 JSON `null`을 API에서 구분할 필요가 있는지 명시돼 있지 않다.
- setup 영구 비활성화 marker는 없지만 현재 관리자 삭제 API도 없다. 향후 삭제/다중 관리자 기능 전에 정책을 확정해야 한다.
- `PublicURL`, secure cookie, trusted proxy는 배포자 정책에 의존한다. production preset 또는 시작 시 위험 경고를 고려할 수 있다.

## 10. 수정 우선순위

### 즉시 차단(P0)

1. AUD-H02 재설정/로그인 직렬화 및 자격 증명 버전 도입.
2. AUD-H01 다축 rate limit, Argon2 동시성 상한, 실패 저장 상한.
3. AUD-H04 복구 명령 수정 및 자동 restore drill.
4. AUD-H03 빈 DB 브라우저 온보딩 E2E와 API 계약 통일.
5. AUD-M02 구조적 DSN redaction.

### 다음 릴리스(P1)

AUD-M01, M03, M04, M05, M06, M07, M08, M09를 해결하고 PostgreSQL·브라우저 통합 회귀 테스트를 추가한다.

### 후속 강화(P2)

나머지 lifecycle/CLI/input 문제, 세션·커서 보존 정책, Docker context와 supply-chain 재현성, fuzz·SBOM·정기 취약점 검사를 처리한다.

## 11. 공식 스캔 산출물과 사용량

공식 스캔은 커버리지 3/3, 발견 0건으로 완료됐다. 생성된 canonical 산출물은 `report.md`, `coverage.json`, `findings.json`, `scan-manifest.json`, `exports/results.sarif`다.

측정된 공식 스캔 사용량은 총 49,155,117 tokens(입력 48,837,210, 출력 317,907, reasoning 출력 164,650, cached 입력 46,468,864), 15 threads였다. 이 값은 구현 워크트리 병렬 감사의 전체 사용량이 아니라 공식 Codex Security 실행이 보고한 측정치다.

## 12. 수정 및 재검증 결과

### 12.1 종결 요약

| 결과 | 수량 | 의미 |
|---|---:|---|
| Fixed | 28 | 원래 재현 경로를 차단했고 관련 회귀 테스트를 추가했다. |
| No change | 0 | 발견 사항을 오탐 또는 허용 동작으로 기각하지 않았다. |
| Blocked | 0 | 코드 수정이 막힌 발견 사항은 없다. |

수정 원칙은 공개 API의 안전한 실패, PostgreSQL 원자성·잠금 순서 보장, 프로세스 간 DB 시계 일관성, 손실 없는 데이터 왕복, 제한된 자원 사용, 재현 가능한 공급망이다. 원래 정상 동작인 SQL 식별자 인용, 트랜잭션 기반 스키마 적용, 세션 토큰 해시 저장, non-root/distroless 런타임은 유지했다.

### 12.2 발견 사항별 수정 증거

| ID | 결과 | 수정 및 검증 |
|---|---|---|
| AUD-H01 | Fixed | 계정·네트워크·전역 버킷을 분리하고 Argon2id 동시 실행을 2개로 제한했다. 성공 요청은 공유 버킷에서 자기 예약만 원자적으로 반환하며, 실패·이메일 회전·동시 KDF 상한 테스트를 추가했다 (`internal/adminauth/service.go`, `store.go`, `types.go`, `service_test.go`). |
| AUD-H02 | Fixed | 로그인 트랜잭션이 관리자 행을 `FOR UPDATE`로 잠근 상태에서 비밀번호 확인과 세션 생성을 수행한다. 재설정과 이전 비밀번호 로그인의 순서를 강제한 경쟁 테스트로 재설정 이후 구 세션이 생기지 않음을 검증했다 (`internal/adminauth/store.go`, `service.go`, `service_test.go`). |
| AUD-H03 | Fixed | 인증 없는 `GET /api/admin/status` 계약을 추가하고 UI가 세션 401 이후 최초 설치 상태를 조회하게 했다. 실제 빈 DB에서 설정 화면, 관리자 생성, 새로고침 후 세션 보존까지 Playwright E2E로 검증한다 (`internal/api/router.go`, `admin_handlers.go`, `ui/admin/src/auth/session.tsx`, `ui/admin/e2e/onboarding.spec.ts`). |
| AUD-H04 | Fixed | `pg_restore`에 서비스 DSN과 `--single-transaction`을 명시했다. 빈 DB로 custom-format dump를 복원하고 migration/admin 행 수를 확인하는 정기 restore drill을 추가했다 (`docs/backup-restore.md`, `.github/workflows/backup-restore.yml`). |
| AUD-M01 | Fixed | HTTP 서버에 header 5초, body read 15초, write 30초, idle 60초 제한을 설정했다 (`pgbase/app.go`, `pgbase/app_test.go`). |
| AUD-M02 | Fixed | 이름이 `password`로 끝나는 DSN/필드를 대소문자와 무관하게 마스킹해 `sslpassword`도 로그에 남지 않는다. sentinel 회귀 테스트가 원문 비밀 미노출을 확인한다 (`internal/observability/redact.go`, `redact_test.go`). |
| AUD-M03 | Fixed | 풀 최소 크기를 2로 강제하고 Doctor가 catalog rows를 먼저 materialize·close한 뒤 중첩 조회한다. 최대 2개 풀에서 1개 연결을 점유한 상태의 완료 테스트를 추가했다 (`internal/database/pool.go`, `internal/doctor/checks.go`, `checks_test.go`). |
| AUD-M04 | Fixed | cleaner가 세션과 rate-limit 행을 batch 삭제하며 일시 오류에 100ms~1분 지수 백오프로 재시도한다. 취소, 재시도, 복구 테스트를 추가했다 (`internal/adminauth/cleaner.go`, `cleaner_test.go`). |
| AUD-M05 | Fixed | UI 기본값 입력을 백엔드 문법과 일치시켰다. 기본값 사용 여부를 별도 제어하고 boolean, 정수 문자열, timestamp `now`, 빈 문자열, JSONB를 타입별로 보존하는 계약 테스트를 추가했다 (`ui/admin/src/pages/collection-create-page.tsx`, `collection-defaults.test.ts`). |
| AUD-M06 | Fixed | 모든 UI API 응답·요청과 JSONB 편집을 `lossless-json` 기반으로 전환했다. 안전 정수는 number, 큰 정수·극단 지수는 원문 숫자로 보존하고 duplicate key는 거부한다 (`ui/admin/src/api/json.ts`, `client.ts`, `client.test.ts`, record pages). |
| AUD-M07 | Fixed | 모든 컬렉션에 `(created_at, id)` 인덱스를 만들고 기존 컬렉션을 system migration 2로 backfill한다. 목록 응답은 행 수 외에 4 MiB 직렬화 예산도 적용하고 다음 cursor를 유지한다 (`internal/schema/planner.go`, `migrations/system/000002_record_page_indexes.sql`, `internal/query/service.go`, 관련 테스트). |
| AUD-M08 | Fixed | 생성·수정 SQL이 애플리케이션 시계 대신 PostgreSQL `statement_timestamp()`를 사용한다. 애플리케이션 시계를 2099년으로 왜곡해도 DB 시각이 반환되는 live 테스트를 추가했다 (`internal/query/compiler.go`, `service.go`, `service_test.go`). |
| AUD-M09 | Fixed | SQLSTATE `40001`, `40P01`, 연결 class 08, shutdown 계열과 네트워크/EOF 오류를 retryable로 분류하고 API가 503과 `Retry-After`를 반환한다 (`internal/database/errors.go`, API record/schema/admin handlers 및 테스트). |
| AUD-M10 | Fixed | caller-owned transport용 `App.Start(ctx)`를 추가해 readiness, schema sync/listener, cleaner lifecycle을 명시적으로 시작한다. `Handler()`만으로는 lifecycle이 시작되지 않으며 `Start` 후 `Run` 이중 시작은 거부한다 (`pgbase/app.go`, `app_test.go`, handoff 문서). |
| AUD-M11 | Fixed | `doctor --json`은 설정 파싱, DB open, 검사 실패를 모두 하나의 유효한 JSON report로 stdout에 기록한다. 닫힌 실제 풀과 도달 불가 DSN도 `database_unreachable`로 확인했다 (`internal/cli/run.go`, `doctor_test.go`, `internal/doctor/checks_test.go`). |
| AUD-M12 | Fixed | 사용자 필드를 1,597개로 제한하고 PostgreSQL numeric/JSON 숫자의 정수부·소수부 한계를 plan 단계에서 검사한다 (`internal/schema/planner.go`, `internal/jsonvalue/json.go`, planner/jsonvalue 테스트). |
| AUD-M13 | Fixed | JSON decode 전 UTF-8, NUL, 단독 surrogate를 엄격히 거부하고 text/JSONB/default/query 경로가 같은 validator를 사용한다. 단위·fuzz corpus가 치환 또는 DB 500 대신 400 계약을 검증한다 (`internal/jsonvalue`, `internal/api/middleware.go`, `internal/query/codec.go`). |
| AUD-L01 | Fixed | 관리자 세션 TTL을 양수이면서 최대 12시간으로 제한했다 (`pgbase/options.go`, `options_test.go`). |
| AUD-L02 | Fixed | cursor v2 MAC payload에 collection ID와 15분 만료시각을 포함해 교차 컬렉션 재사용과 영구 재사용을 거부한다 (`internal/query/cursor.go`, cursor 단위/fuzz 테스트). |
| AUD-L03 | Fixed | cleaner가 만료·폐기 세션도 bounded batch로 지속 정리한다 (`internal/adminauth/cleaner.go`, `cleaner_test.go`). |
| AUD-L04 | Fixed | migration 성공 로그를 commit 성공 이후에만 기록하고 commit 실패 시 성공 로그가 없음을 테스트했다 (`internal/migrate/runner.go`, `runner_test.go`). |
| AUD-L05 | Fixed | stdin을 최대 비밀번호 길이+newline까지만 읽어 초과 입력을 bounded 상태에서 거부한다 (`internal/cli/password.go`, `password_test.go`). |
| AUD-L06 | Fixed | UI가 `expiresAt`을 보존해 만료 타이머로 상태를 제거하고 모든 API 401을 전역 logout으로 연결한다 (`ui/admin/src/auth/session.tsx`, `src/api/client.ts`, 관련 테스트). |
| AUD-L07 | Fixed | `.git`, worktree, env, node_modules, dist, coverage, Playwright 산출물과 dump/log를 build context에서 제외하는 루트 `.dockerignore`를 추가했다. |
| AUD-L08 | Fixed | npm lockfile을 재생성했다. 비-link 패키지 163개 모두 `resolved`와 `integrity`를 가지며 `npm ci --ignore-scripts`가 성공한다 (`ui/admin/package-lock.json`). |
| AUD-L09 | Fixed | Go/Node/distroless/PostgreSQL 이미지를 digest로 고정하고 Dependabot이 Docker·Actions·Go·npm 갱신을 제안하게 했다 (`deployments/*`, backend/compose workflow, `.github/dependabot.yml`). |
| AUD-L10 | Fixed | 정기 `govulncheck`, `npm audit`, 이미지 build/Trivy, SPDX SBOM을 추가하고 tag release에 BuildKit provenance·SBOM과 GitHub provenance/SBOM attestations를 발행한다 (`.github/workflows/security.yml`, `release-image.yml`). |
| AUD-L11 | Fixed | `pgbase.New`가 pgxpool 내부 goroutine을 시작한다는 실제 lifecycle 계약으로 API·handoff·계획 문서를 바로잡았다 (`pgbase/app.go`, `docs/production-kernel-handoff.md`, production-kernel 계획). |

### 12.3 품질 공백 종결

- 빈 DB 브라우저 온보딩은 Playwright와 실제 백엔드를 잇는 CI로 추가했다.
- E2E 컬렉션 이름을 실행별로 고유하게 만들고 `-count=2 -shuffle=on`을 CI 필수 조건으로 설정했다.
- advisory migration lock이 해제 전 반환하지 않고 해제 후 진행하는지를 시간 경계로 검증한다.
- 관리자 route 테스트는 정확한 status와 error/body 계약을 확인한다.
- panic recovery가 downstream partial header/body를 폐기하고 깨끗한 500만 내보내도록 buffering middleware와 테스트를 추가했다.
- 동일 timestamp의 전체 페이지 합집합이 DB `(created_at,id)` 순서와 정확히 일치함을 live PostgreSQL에서 확인한다.
- Doctor는 닫힌 실제 풀과 최소 풀에서 연결 하나가 남은 조건을 모두 테스트한다.
- 엄격 JSON/Unicode와 signed cursor에 seed corpus 기반 fuzz target을 추가했다.

### 12.4 최종 실행 증거

| 명령 | 결과 |
|---|---|
| `go test ./... -count=1` | 통과, 모든 Go·UI embed 패키지 |
| `go test -tags=e2e ./tests/e2e -v -count=2 -shuffle=on` | 통과, 4개 black-box 시나리오를 셔플 순서로 2회 실행 |
| `go vet ./...` | 통과 |
| `go test -race ./... -count=1` | 로컬 Windows에서 CGO 비활성화로 실행 불가; PostgreSQL 16/17/18 CI가 동일 검사를 필수 실행 |
| `npm ci --ignore-scripts` | 통과 |
| `npm run typecheck` | 통과 |
| `npm test -- --run` | 통과, 8개 파일·31개 테스트 |
| `npm run build` | 통과, production admin bundle 생성 |
| `npm audit --audit-level=high` | 취약점 0건 |
| `govulncheck ./...` | 호출 가능한 취약점 0건 |
| strict JSON/Unicode fuzz (3초) | 통과, 약 430,000 executions |
| signed cursor fuzz (3초) | 통과, 약 558,000 executions |
| `actionlint` | 통과, 모든 GitHub Actions 문법·표현식 검증 |
| `docker compose ... config --quiet` | 필수 비밀의 검증용 값을 주입해 통과 |
| production Docker image | build 통과; runtime user `nonroot:nonroot`, `/bin/sh` 부재 확인 |
| Playwright Chromium onboarding | 통과, 빈 DB 설정·관리자 생성·새로고침 후 세션 보존 1건 |
| PostgreSQL 16 restore drill | 통과, `system_migrations=2`, `admins=1` 복원 확인 |

로컬 실행과 별도로 image build·browser onboarding·black-box 반복 E2E·restore drill·Trivy/SBOM은 저장소 CI에서도 독립적으로 실패를 차단한다. Windows 로컬의 유일한 실행 제한은 CGO 비활성화로 인한 race detector이며, Linux CI의 PostgreSQL 16/17/18 매트릭스가 이를 수행한다.

### 12.5 9절 설계 위험의 처리 경계

9절의 idempotency key, ETag/optimistic concurrency, SQL `NULL`과 JSON `null` 정책, 향후 관리자 삭제 시 setup marker, production preset은 이번 감사에서 결함으로 확정한 28건이 아니라 공개 제품 계약이 먼저 필요한 별도 기능이다. 현재 불변식은 관리자 삭제 API 없음, setup은 관리자 존재 시 비활성, 동시 수정은 last-write-wins, JSON 필드의 API `null`은 JSON null로 취급하는 것이다. 이 계약을 바꾸는 작업은 호환성 있는 별도 spec과 migration으로 진행하며, 본 수정에서 암묵적으로 API 의미를 변경하지 않았다.
