# PgBase 자체 제품 보완과 후속 작업

기준: 로컬 `origin/main`의 `6993744a8f36db48ddfeaa8dae017656752d24c7` 및 `codex/remaining-development` 작업 트리. 2026-09-22 사용자 지시에 따라 PgBase를 PocketBase에서 아이디어를 얻은 독립 프로젝트로 취급한다. 아래 내용은 로컬 개발·검증 기록이며 병합, 기존 설치 교체 또는 공개 배포를 의미하지 않는다.

## 판단 기준

자체 문서와 공개 API가 약속한 데이터 무결성, 접근 제어, 관리 화면·SDK의 실제 사용 흐름을 우선한다. [고정 소스 비교 감사](../../audits/2026-09-22-pocketbase-comparison/README.md)는 수정 전 참고 자료로 보존한다. 그 문서의 partial/missing 수는 제품 결함 수나 남은 개발량이 아니다.

PostgreSQL 트랜잭션·opaque 세션·엄격한 타입·NULL 의미·참조 대상 삭제 시 cascade·schema import의 merge 정책·전체 백업 저장소 보관 정책을 유지한다. PocketBase의 SQLite/JWT/ORM, 동일 REST envelope, 모든 JavaScript 별칭을 복제하는 것은 기본 목표가 아니다. 선택한 호환 표면은 명시한 범위 안에서만 보장한다.

## 첫 번째 보완 묶음

| 영역 | 변경과 실제 검증 | 상세 기록 |
| --- | --- | --- |
| 조회 접근 제어·커서 | 사용자·익명 필터의 모든 피연산자에 숨김 필드 검사. 날짜·거리 함수의 첫 페이지와 커서 이어보기 수정. 관리자·저장된 규칙 접근과 기존 scalar 커서 형식 유지 | [query](query.md) |
| 인증 무결성 | 이메일 action/OTP를 발송 주소에 바인딩. OAuth·비밀번호 가입에 공통 트랜잭션 정책과 required profile 검사 적용 | [auth](auth.md) |
| 실시간 | 애플리케이션 topic 유지, 장기 SSE 연결, 숫자 재연결 커서, 여러 topic의 전달 순서·체크포인트 보완 | [realtime](realtime.md) |
| 관리 화면 | 규칙 저장·선택 action preview의 실제 HTTP 계약 수정, 초안·정확 숫자 보존, OS 테마 적용, 백업 보관 개수 입력 | [ui](ui.md) |
| SDK | 응답 본문 중단 오류 전파, 선택형 인증 상태 저장, 조회 자동 취소, 실제 서버에 맞춘 scaffold 타입, 실시간 terminal 오류에서 재연결 종료 | [sdk](sdk.md) |
| JavaScript 파일 기능 | 운영자가 설정한 저장소 명시, 무시되던 인자 거절, 페이지 목록과 잘림 오류 제공 | [filesystem](filesystem.md) |
| 뷰·스키마 | text ID를 상속한 뷰의 이름 변경에서 ID 정책 보존. import의 collection 삭제와 field/index 보존 범위 명시 | [schema](schema-decisions.md) |
| 운영 설정 | 지원하지 않는 tag·공백·query/fragment label 신규 저장 거절, 기존 설정의 업그레이드 읽기 보존, UI 설명·검증 일치 | [operations](operations.md) |

앞선 같은 작업 트리의 백업·auth ID 전송·관계 선택기·뷰 편집·JSON 원문 SDK 보완은 [이전 개발 기록](../../development-completion-2026-09-22.md)을 함께 따른다.

## 이어서 구현한 작업

| 영역 | 완료한 범위 | 상세 기록 |
| --- | --- | --- |
| 필드 비교 커서 | 양쪽 필드 ID·타입·modifier 바인딩, 숫자 비교 첫 조회, contains 방향 구분, 잘못된 연산자·타입의 SQL 실행 전 거절 | [continuation-query](continuation-query.md) |
| 관리자 계정 | 별도 관리 API·화면의 목록·생성·활성화·직접 비밀번호 변경·세션 폐기. 자기 비활성화 및 동시 상호 비활성화 보호, pending credential 정리와 잠금 순서 보완 | [administrators](continuation-administrators.md), [UI](continuation-ui.md) |
| 가입 프로필 | 기본·이름 있는 auth 컬렉션 및 신규 OAuth 가입의 `fields` 입력, 필수·타입·관계·고유성·생성 규칙 검사, 정확한 숫자 저장/조회 | [continuation-signup](continuation-signup.md) |
| 인증 정책 화면 | 최대 비밀번호 바이트·정규식·로그인 identity 필드·MFA 유효 시간·MFA 규칙 편집 및 저장 | [continuation-ui](continuation-ui.md) |
| SDK | TypeScript·Dart 가입 `profile` 옵션, 실시간 `onStatus`·`status`·`resetCursor`, 연결 후 오류·재연결·종료 통지 | [continuation-sdk](continuation-sdk.md) |

두 번째 묶음의 최종 통합 검증과 소스 측정은 [continuation-validation](continuation-validation.md)에 별도로 기록한다. 아래 첫 번째 묶음 수치를 두 번째 묶음 결과로 읽지 않는다.

## 업그레이드 시 달라지는 계약

- 구형 pending 이메일 action/OTP는 새 바인딩으로 재발급해야 한다. 구/신 인증 프로세스를 혼합한 순차 배포 조건은 [auth](auth.md)를 따른다. DB migration 추가는 없다.
- 기존에 저장됐지만 실제 route와 매칭되지 않던 rate label은 계속 읽을 수 있다. 설정을 저장하기 전에 route pattern으로 수정해야 한다. 부팅 시 삭제하거나 보안 정책으로 유효하다고 간주하지 않는다.
- JavaScript `s3()` 무인자 별칭은 유지한다. 인자 있는 호출과 `setContext()`는 명시적으로 실패한다. 100개를 넘는 파일 목록은 `listPage()`로 순회한다.
- 실시간 durable resume의 보장은 숫자 체크포인트를 받은 이후부터다. 첫 연결의 체크포인트 이전과 process-local application topic의 내구성을 구분한다.
- 수정된 field-to-field 및 일부 contains 커서는 이전 토큰을 버리고 첫 페이지부터 다시 조회해야 한다. 일반 literal·함수 커서는 보존하며, 구/신 서버 혼합 배포 및 역사적으로 모호한 토큰의 한계는 [continuation-query](continuation-query.md)를 따른다.
- 가입 `fields`와 SDK `profile`은 선택 항목이다. 기존 OAuth 로그인·계정 연결에서는 프로필을 덮어쓰지 않는다. 프로필 검증 실패 후 실제 제공자의 일회용 authorization code 재사용은 보장하지 않으므로 OAuth를 다시 시작해야 할 수 있다.

## 다음 실행 순서와 완료 기준

| 우선순위 | 자체 제품 작업 | 완료 기준 |
| --- | --- | --- |
| 1 | 검증된 변경 묶음의 리뷰·병합 준비 | 첫 번째 및 후속 묶음의 검증 기록을 기준으로 변경 집합 검토. 병합·설치는 실제 수행 증거로 별도 기록 |
| 2 | 조회 문법의 남은 경계 | 이번 검토에서 식별한 literal-left `?` 연산자 정규화의 의미를 재현하고 지원/거절 계약 확정. 수정한 field-to-field 바인딩과 구분 |
| 3 | 데이터·파일 사용성 | builder view import 갱신, 이미지 EXIF·투명도 및 HTTP Range 요구 검토. 선택한 기능만 자체 입력·실패 계약과 회귀로 확정 |
| 4 | SDK의 자체 API 범위 정리 | 남은 서버 기능을 실제 앱 사용 흐름 순으로 추가. 이번 상태 알림·가입 입력 완료와 SDK의 서버 전체 기능 포괄을 구분 |
| 5 | 실제 운영 인수 | 보존한 설치의 복사본으로 업그레이드·복구·롤백, Windows self-update 실행, 실제 메일/OAuth/S3 provider 및 여러 프로세스의 부하·장애 시험. 필요한 환경이 준비되면 로컬 fixture 결과와 구분하여 기록 |

UI 기능이 없더라도 API·CLI 경로가 있는 경우는 사용성 공백으로 분류한다. 외부 환경 검증과 공개 릴리스는 구현 완료와 동일시하지 않는다. 필요가 확정되지 않은 PocketBase 전용 별칭·batch wire·ORM 동작은 후속 작업의 기본 목록에서 제외한다.

## 첫 번째 묶음의 검증 기록

개별 red→green 증거와 한계는 위 영역별 문서에 있다. 단순 컴파일만으로 완료 처리하지 않고 실제 PostgreSQL, HTTP, MinIO, 브라우저 경로를 검증했다.

- Go 전체 unit/race 및 skip guard 실행: 최종 함수 보완을 포함해 전체를 다시 실행하여 통과했다. 최초 검사에서 실패한 query/data group 소스 규모 상한은 정확 측정으로 갱신했다. Windows Git worktree 경로 때문에 전역 소스 집계 검사 한 개는 Windows에서 별도 실행해 통과했다(최종 구조 검사 0.810초). Bash 및 실행 비트가 필요한 나머지 repository policy는 Linux에서 검증했다.
- `go vet ./...`, 변경 Go 파일 `gofmt`, `git diff --check`, 상태 문서 인덱스 검사 통과.
- UI 전체 48개 파일/613개 테스트, typecheck/build 통과. 이후 UTF-8 byte limit와 보관 정책 도움말 수정은 해당 테스트 24개 및 4개와 재빌드로 검증했다. 실제 서버 Chromium에서 신규 2개 + 기존 흐름 4개, 총 6개 통과. 최종 내장 UI 바이너리 재빌드 후 readiness 200 확인.
- PostgreSQL 17.9 및 MinIO, `test-go-integration.sh -race -p 2 -count=1`과 skip guard로 8개 패키지 전체 통과: query 94.380초, schema 65.755초, userauth 33.948초, realtime 2.177초, runtimesettings 2.313초, apiratelimit 1.181초, api 23.748초, pgbase 194.059초. 함수 커서·파서 보완 뒤에는 영향받는 query와 userauth 패키지 전체를 다시 실행해 각각 90.893초 / 25.913초 통과했다.
- TypeScript `npm test`: build, 소스/생성물 public type 검사, 기존 conformance script, Node 테스트 28개 통과. Dart 3.13.0 고정 Docker 이미지에서 analyze 문제 없음, 테스트 47개 통과. 선택한 저장소·HTTP transport와 정확 JSON 계약을 검사했으며 실제 외부 OAuth/SMTP 제공자 검증을 의미하지 않는다.

구조 검사는 새 파일까지 포함한 임시 Git index로 측정했고 실제 index는 변경하지 않았다. `6993744` 대비 손으로 작성한 Go/UI runtime은 +1,133/-387줄, 순증 746줄(103,346 → 104,092)이다. 앞선 보완 작업의 103,590줄 대비 이번 증가량은 502줄이다. Go 93,160 / UI 10,932, data group 28,340, query 7,746으로 여유분 없이 상한을 맞췄다. 숨김 필드 검사 이후 함수 커서·입력 오류 보완이 추가한 160줄을 포함한다. 800줄 초과 runtime 파일은 0개이며 Go interface 96 / export 1,575 / constructor 64, UI export 312를 유지했다. UI effect 1개는 수정한 규칙의 이전 preview 결과를 지운다(전체 직접 state/effect 181).

관계 cascade 의미와 기존 credential 정리 정책은 독립 검토로 유지 근거를 확인했다. 인증 및 실시간 변경에도 별도 읽기 검토를 적용했고 현재 계약에서 추가 수정이 필요한 finding은 없었다. 구형 pending credential 재발급, 초기 실시간 checkpoint 이전의 재연결, 외부 provider·실제 설치 검증은 위 제한과 후속 작업에 명시했다.

변경은 `codex/remaining-development`의 로컬 작업 트리에 있으며 이번 실행에서 커밋·병합·설치 교체는 하지 않았다. 검증 후 작업용 `pgbase-remaining-*` 서버·runner·PostgreSQL·MinIO 컨테이너는 정지했다. 기존 다른 컨테이너와 역사적 감사 자료는 유지했다.
