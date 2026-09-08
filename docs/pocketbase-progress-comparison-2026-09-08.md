# PocketBase 대비 pgBase 기능 진행률

확인일: 2026-09-08 (Asia/Seoul).

pgBase 기준: 정식 `v0.1.0`, commit `3677a6b182b440a158e96d10482b1552030c18f5`.
PocketBase 기준: [최신 정식 릴리스 `v0.40.3`](https://github.com/pocketbase/pocketbase/releases/tag/v0.40.3), 2026-09-06 발행, commit [`5684ee24f1e88f71a2ede73d20cf547fbd509e16`](https://github.com/pocketbase/pocketbase/tree/5684ee24f1e88f71a2ede73d20cf547fbd509e16). GitHub API와 공식 문서에서 확인했다. 고정한 upstream 소스와 상세 출처는 [upstream 조사](pocketbase-upstream-capabilities-2026-09-08.md)에 기록한다.

> 이 표는 v0.1.0에 고정한 비교 기록이다. 이후 M1 구현 내용은 아래 부록과 [실시간 TODO](../TODO.md)를 참고한다.

## 판단

**PocketBase와 같은 범위의 애플리케이션 백엔드를 제공한다는 기준에서 약 35%, 합리적인 표현 범위는 30–40%다.** 아래 가중 평가의 계산값은 35.5/100이다. 이것은 검토자가 정한 기능 범위 지표이며, 공식 프로젝트 일정의 완료율·남은 개발시간·성능·품질·테스트 커버리지가 아니다. 다른 제품 우선순위를 선택하면 가중치와 결과도 달라진다.

pgBase는 관리자용 PostgreSQL 컬렉션·레코드·관계 관리 백엔드로 사용할 수 있고, 그 범위의 정식 릴리스를 완료했다. 일반 사용자가 가입하고 자기 데이터만 다루는 PocketBase형 서비스 백엔드에는 추가 기능이 필요하다. 공개 사용자 인증과 레코드 규칙이 없으므로 높은 내부 구현 완성도가 일반 사용자용 기능 제공으로 이어지는 것은 아니다.

## 평가 방법과 기능 표

대표 사용자 기능을 10개 영역으로 묶고 합계 100의 가중치를 부여했다. 구현 계수는 0=기능 없음, 0.25=통합 기반만 있음, 0.5=주요 경로 일부, 0.75=핵심 경로 구현 및 검증·주변 기능 공백, 1=선정 영역의 비교 기능 제공이다. 계수도 검토자의 판단이며 기능 개수를 실제 전수 계수한 값은 아니다.

| 영역 | 가중치 | pgBase 계수 | 점수 | 현재 근거와 남은 차이 |
| --- | ---: | ---: | ---: | --- |
| 컬렉션·스키마·필드 | 10 | 0.75 | 7.5 | 생성/수정/삭제 plan/apply, 타입·기본값·제약·관계 구현. Auth/View 컬렉션, 일부 특수 필드와 복합 인덱스 미제공. [스키마 모델](../internal/schema/types.go) |
| 레코드 CRUD·쿼리·관계 | 15 | 0.75 | 11.25 | CRUD, 커서, filter/sort, single/multiple 관계, 정방향·역방향·중첩 expand, 삭제 전파 구현. 일반 사용자 정책과 결합되지 않았으며 PocketBase 쿼리/API 호환이나 batch API는 제공하지 않음. [라우트](../internal/api/router.go), [쿼리](../internal/query), [관계 E2E](../tests/e2e/relation_test.go) |
| 관리자 UI | 10 | 0.75 | 7.5 | 관리자 setup/login, 컬렉션 디자이너, 레코드 편집, 관계 선택, 스키마 작업 복구·시스템 상태 화면 구현. 파일·사용자 인증·규칙 등 전체 PocketBase 관리 기능은 미제공. [페이지](../ui/admin/src/pages), [앱](../ui/admin/src/app.tsx) |
| 일반 사용자 인증 | 15 | 0 | 0 | 관리자 인증만 있음. Auth 컬렉션, 일반 사용자 가입/로그인, OAuth2, OTP/MFA, 사용자 메일 인증/비밀번호 재설정 없음. 관리자 CLI 암호 재설정은 별개. [identity](../internal/identity/subject.go), [관리자 인증](../internal/adminauth) |
| 레코드 접근 규칙 | 10 | 0 | 0 | CRUD route가 관리자 세션을 요구. 사용자별 소유권, list/view/create/update/delete 규칙의 저장·평가 기능 없음. [라우트 선언](../internal/api/router.go), [principal 검증](../internal/identity/subject.go) |
| 파일·스토리지 | 10 | 0 | 0 | 레코드 file 필드, 업로드/다운로드 API, S3 어댑터, 보호 파일, 썸네일 없음. UI 정적 파일 임베드는 사용자 파일 기능이 아님. [필드 타입](../internal/schema/types.go), [전체 라우트](../internal/api/router.go) |
| 실시간 구독 | 8 | 0 | 0 | 트랜잭션에 outbox 이벤트를 쓰지만 소비·전송 worker와 클라이언트 구독 API 없음. 스키마 LISTEN/NOTIFY는 레코드 realtime 기능이 아님. [outbox writer](../internal/recordops/artifact.go), [앱 수명주기](../pgbase/app.go) |
| 앱 확장·hook·작업·메일 | 7 | 0.25 | 1.75 | 공개 Go App 생성/시작/종료/HTTP Handler 임베딩 기반은 있음. PocketBase 수준의 공개 record hook API, JavaScript 확장 런타임, 사용자 정의 cron/메일 기능은 없음. [App](../pgbase/app.go), [Options](../pgbase/options.go) |
| 클라이언트 SDK·API 사용 경험 | 5 | 0 | 0 | 공식 JS/Dart SDK 또는 대응 SDK 배포 없음. 내부 관리자 HTTP 어댑터는 공개 SDK가 아님. PocketBase 클라이언트와 API 호환되지 않음. 생성 OpenAPI도 없지만, PocketBase가 생성 OpenAPI를 제공한다고 전제해 감점한 것은 아님. [관리자 어댑터](../ui/admin/src/backend/http-backend.ts), [라우트](../internal/api/router.go) |
| 배포·운영 사용 경험 | 10 | 0.75 | 7.5 | 서명된 Linux AMD64 GHCR 이미지, 5개 release gate, PG16/17/18 복원, 진단/metrics/CLI/마이그레이션 보유. 별도 PostgreSQL 설정이 필요하고 PocketBase식 여러 플랫폼의 즉시 실행 바이너리 배포·통합 관리 경험에는 공백. [배포 문서](deployment.md), [릴리스](https://github.com/yakisoba0728/PgBase/releases/tag/v0.1.0) |
| **합계** | **100** | | **35.5** | **사용자에게는 약 35%, 30–40%의 추정 범위로 설명** |

영역 간 의존성은 존재한다. 인증 부재를 데이터 무결성 미구현으로 계산하지 않았고, outbox 기반만으로 realtime 기능 완료 점수를 주지 않았다. UI 항목은 지원 기능의 관리 경험도 평가하므로 전체 UI 기능 공백을 반영한다. 이 지표는 공수 산정용 독립 작업 목록이 아니다.

## 지금 가능한 사용과 아직 어려운 사용

- **가능:** 관리자가 컬렉션과 관계를 설계하고 레코드를 입력·수정·검색하는 내부 도구, 단일 프로젝트 PostgreSQL 기반 데이터 관리, 현재 관리자 인증 모델을 이해하고 사용하는 개발/검증 배포.
- **추가 구현 필요:** 일반 사용자의 회원가입, 사용자 A/B 데이터 분리, 사용자 프로필 사진 등 파일 관리, 채팅·알림용 realtime 구독, 공식 SDK로 프런트엔드에서 직접 연결하는 PocketBase형 앱.
- 일반 사용자에게 관리자 자격증명을 제공하는 방식으로 인증·규칙의 공백을 대체해서는 안 된다. 현재 데이터 API가 관리자 전용이라는 실제 제품 경계다.

## 이번에 확인한 구현 근거

1. `internal/api/router.go:94`부터 레코드 list/get은 `accessAdminSameOriginRead`, create/update/delete는 `accessAdminSession`이다. `/api/v1` 이름이 공개 사용자 인증 지원을 의미하지 않는다.
2. `internal/identity/subject.go:9`의 유일한 identity kind는 `Administrator`이며, `Valid()` 역시 그것만 허용한다.
3. `internal/schema/types.go:14`는 8개 scalar 타입과 relation을 제공한다. 파일 타입이나 컬렉션의 auth/view 구분이 없다.
4. `internal/schema/relation_types.go`와 `internal/query`는 single/multiple 관계, 삭제 정책, bounded expand/filter/sort를 구현한다. `tests/e2e/relation_test.go`는 재시작과 여러 인스턴스에서 관계 CRUD/expand 및 삭제 정책을 검증한다.
5. `internal/recordops/artifact.go:207`은 outbox를 트랜잭션에 기록한다. 전체 route와 앱 background 구성에 구독·전송 기능은 없다.
6. `pgbase/app.go:445`의 `Handler()` 및 공개 수명주기 API는 Go 앱에 임베드할 기반을 제공한다. 공개 event-hook/JS 실행 API와 동일하지 않다.
7. [v0.1.0 release workflow](https://github.com/yakisoba0728/PgBase/actions/runs/34195239096)는 15개 job 모두 성공했다. 이 근거는 현행 구현·릴리스 검증의 증거이며 미구현 제품 기능이나 운영 트래픽의 처리량 보장은 아니다.

이번 비교에서는 코드를 읽고 기존 성공 실행 결과를 확인했다. 소스 수정이 없는 비교 작업이므로 전체 테스트를 다시 실행하지 않았다.

## 이전 상태에서 진전한 부분

- [2026-08-31 비교 문서](pocketbase-reference-research-2026-08-31.md)의 relation/expand/reverse/filter/sort `Missing` 항목들은 현재 구현됐다. 과거 문서의 표를 그대로 현재 진행률로 사용하면 진전을 과소평가한다.
- 서명된 실제 GHCR 후보, 동일 digest를 사용하는 전체 gate, 재빌드 없는 승격, 정식 GitHub Release까지 완료됐다.
- 반대로 공개 인증·규칙·파일·realtime·SDK는 여전히 공백이다. 릴리스 번호를 붙인 것만으로 이 기능들이 생기지는 않는다.
- `README.md`의 `pre-v0.1` 설명과 `production-kernel-handoff.md`의 과거 릴리스·CI 상태는 갱신이 필요하다. 이번 평가는 소스와 실제 `v0.1.0` 실행 결과를 우선했다. 기존 기록을 고치거나 소스 동작을 변경한 작업은 아니다.

## 다음 구현 순서 제안

1. **일반 사용자 identity와 레코드 접근 규칙을 함께 설계·구현:** 회원가입/로그인, 기본 사용자 수명주기와 소유자 조건을 갖추고 query/mutation/relation 경로 모두에 권한을 적용한다. 완료 기준은 두 사용자가 서로의 레코드를 조회·변경할 수 없는 실제 API/E2E 시나리오다.
2. **공개 API 계약과 최소 TypeScript SDK:** 가입/로그인/CRUD/페이지네이션/에러를 작은 예제 앱에서 사용할 수 있게 한다. 기존 PocketBase SDK 호환 여부는 별도 제품 결정이다.
3. **파일:** 사용자·규칙 모델을 재사용하는 record-bound upload/read/delete와 storage 수명주기부터 구현하고 썸네일 등으로 확장한다.
4. **Outbox 전송과 realtime:** 이벤트 전달, 재연결, 사용자 권한 변경, 여러 인스턴스의 구독 테스트를 포함한다.
5. **확장 API와 나머지 제품 기능:** hook, 작업 스케줄링, 메일, view 컬렉션, 특수 필드·고급 쿼리와 관리 UX를 사용 사례에 맞춰 확장한다.

SQLite와 동일한 저장 구조, 샤딩, SaaS control plane, cross-project relation은 이번 PocketBase 동등 기능 평가의 필수 항목으로 넣지 않았다. PostgreSQL 채택과 여러 앱 인스턴스 운영은 pgBase의 설계 방향이며, 실제 대규모 성능 우위는 별도 부하·장애 측정 없이는 주장할 수 없다.

## 공식 기준 자료

- [PocketBase 저장소](https://github.com/pocketbase/pocketbase)
- [Introduction 및 플랫폼 배포](https://pocketbase.io/docs/)
- [Collections](https://pocketbase.io/docs/collections/)
- [Authentication](https://pocketbase.io/docs/authentication/)
- [API rules and filters](https://pocketbase.io/docs/api-rules-and-filters/)
- [Files handling](https://pocketbase.io/docs/files-handling/)
- [Realtime API](https://pocketbase.io/docs/api-realtime/)
- [상세 고정 버전 출처](pocketbase-upstream-capabilities-2026-09-08.md)

## 2026-09-08 M1 구현 부록

v0.1.0 이후 소스에 프로젝트 사용자 가입/로그인/세션/로그아웃과 opt-in 컬렉션의 소유자 전용 CRUD를 추가했다. 실제 HTTP 두 사용자 격리, 전체 Go race·PostgreSQL 통합 검사, PostgreSQL 16/17/18 백업·복원, 데이터가 있는 v9→v11 업그레이드를 검증했다. [사용법](user-access.md)과 [설계·범위](public-identity-owner-access-design.md)에 계약을 기록했다.

이는 위 평가의 일반 사용자 인증·접근 규칙 영역 중 첫 사용 경로를 구현한 것이다. Auth 컬렉션, 이메일 검증/암호 복구/OAuth/MFA, 임의 규칙 및 관계별 권한은 아직 미구현이다. 따라서 기존 35.5점에 임의로 완료율을 더하지 않았으며, 전체 영역을 다시 평가할 때 점수를 갱신한다. 배포된 v0.1.0 이미지에는 M1이 포함되지 않는다. PR/병합·후속 릴리스 상태는 TODO에서 별도로 관리한다.
