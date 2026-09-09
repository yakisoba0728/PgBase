# PgBase의 실제 PocketBase 대비 상태

> 이 문서는 PR #29/P6 구현 이전 소스 감사의 역사적 스냅샷이다. 후속 구현 계약은 [P6 로컬 구현 계약](../phase2-local-contracts.md)과 [P7 로컬 구현 계약](../phase3-local-contracts.md), 완료 검증과 병합 상태는 [TODO](../../TODO.md)를 따른다.

> 소스 대조 감사 완료: 본문 734개, 생성물·자산 186개, 역사 참고 문서 3개. [coverage.json](coverage.json)의 미판독 소스는 0개다. 아래 우선순위는 후속 구현 후보이며 구현 완료 표시가 아니다.

PgBase는 로컬 앱 개발에 필요한 주요 BaaS 기능을 갖춘 상태다. 그러나 PocketBase의 API·관리자 작업·필드 동작·확장 생태계를 그대로 대체하는 제품 단계로 보기는 어렵다. 앞서 제시한 65–70%는 기능 이름을 중심으로 한 추정이며, 이번 소스 계약 비교의 확정 수치로 유지하지 않는다. SQLite와 PostgreSQL의 차이 때문에 그대로 옮길 필요가 없는 기능도 있어 단일 백분율은 특히 부정확하다.

## 구현된 기반과 실제 차이

| 관점 | 현재 PgBase | PocketBase 대비 남은 차이 | 근거 |
| --- | --- | --- | --- |
| 기본 앱 백엔드 | 스키마 관리, CRUD, 권한 규칙, 관계/확장, 사용자 인증, 메일 흐름, 파일과 SSE가 있다. | 응답·ID·필터·페이지·인증 토큰·파일 URL·batch 형식이 다르다. 기존 PB 클라이언트 코드를 연결하는 drop-in 대체품은 아니다. | [API](api.md), [Core](core.md) |
| 데이터 모델 | PostgreSQL 정확 숫자, UUID, JSONB, 단일/다중 관계, 복합 인덱스와 제한된 view를 지원한다. | email/URL/select/editor/geo/autodate/password 일반 필드와 필드별 검증·표시 설정, ID 기반 rename, raw SQL view 표현력이 부족하다. | [필드별 대조](core-fields.md) |
| 인증 | 암호·OAuth/PKCE·TOTP/복구 코드·이메일 검증/변경/재설정·realm·세션 폐기가 있다. | PB 이메일 OTP 로그인, impersonation, provider catalog, collection별 정책/메일 템플릿 편집 및 JWT refresh 규약 차이가 남는다. 인증 서비스 존재와 관리자 설정 UI 완성은 별개다. | [API](api.md), [UI](ui.md) |
| 파일 | local/S3, 파일 수명주기, 단기 토큰 및 JPEG/PNG 비율 유지 썸네일이 있다. | 필드별 크기/MIME/개수/필수 설정, 여러 crop/fit 변환, 레코드 multipart/batch 결합 방식이 다르다. | [필드](core-fields.md), [Tools](tools.md) |
| 관리자 작업 | collection designer, schema operation 복구, records, users, auth realms, service tokens, system/drift 상태를 관리한다. | 로그 검색/통계, UI 백업·복원, SMTP/S3 설정·시험, cron 관리, schema import/export, SQL console 및 record 일괄 작업이 남는다. | [UI](ui.md), [API](api.md) |
| 개발자 확장 | Go mutation hooks, 간격 작업, TypeScript SDK와 로컬 예제가 있다. | PB JSVM, 범용 model/request hooks, custom routes, migration 생성/up/down, Dart SDK 및 collection별 API preview와 동등하지 않다. | [확장·CLI](operations.md), [공개 지원 범위](../extensions-and-client-scope.md) |
| 실행·운영 | PostgreSQL 기반 로컬 개발 환경과 복원/부하/배포 절차 관련 코드·기존 기록이 있다. | PB의 단일 실행 파일+SQLite 디렉터리 운용과 다르다. 외부 서버·도메인·실제 운영 인증 검증은 이번 감사에 포함되지 않는다. | [운영](operations.md), [스케줄러](tools-extra.md) |

## 기능 공백과 설계 차이를 구분하기

- **직접적인 기능 공백:** 전용 field/editor 설정, 관리자 logs/backups/settings/cron 작업, record bulk/export, API preview, 일부 인증 관리 흐름.
- **동작을 먼저 결정해야 하는 차이:** Required의 빈 값 정의, 숫자와 ID 전송 형식, filter/pagination, refresh token 대신 revocable session, file lifecycle, SQL view와 typed view, calendar cron과 DB lease interval.
- **제품 방향을 선택해야 하는 확장:** JSVM, PocketBase SDK wire compatibility, Dart, arbitrary SQL HTTP, self-update. 기능 목록에 있다는 이유만으로 바로 복제하면 PgBase의 PostgreSQL 운영 계약과 충돌할 수 있다.

## 로컬 개발 범위의 후속 순서와 완료 기준

| 순서 | 작업 | 완료로 인정할 기준 |
| --- | --- | --- |
| 1 | 필드 계약과 데이터 보존 rename | 스키마/API/UI/SDK가 같은 설정을 사용하고, 기존 레코드·관계·view·index가 rename 뒤 보존되는 통합 검증. 빈 값·중복·정밀도 규약 문서화. |
| 2 | 레코드 관리 생산성 | 여러 레코드 선택/삭제/export/duplicate, column preference, relation picker 즉석 생성·수정. 권한 거부·부분 실패·동시 갱신을 사용자에게 정확히 표시. |
| 3 | 인증 설정과 메일 편집 | realm별 provider/정책/템플릿을 읽고 수정하고 로컬 inbox로 시험. 기존 세션과 토큰 무효화 규칙을 검증. |
| 4 | 로컬 운영 콘솔 | 로그 탐색과 cron 상태/수동 실행, 백업 작업 목록·진행·실패·복원 안내를 실제 PgBase 서비스에 연결. PostgreSQL와 파일 객체의 일관성 경계를 보존. |
| 5 | 필드별 파일 설정·변환 | 크기/MIME/개수와 crop/fit을 스키마로 관리하고, 기존 파일·실패 정리·권한·토큰·저장 비용의 계약을 검증. |
| 6 | API preview와 확장 범위 | 실제 지원 SDK/OpenAPI에서 예제를 만들고 실행하여 문서 drift 방지. Custom route와 hook 확대는 트랜잭션/재시도 계약을 먼저 고정. |

이 순서는 클라우드 계정이나 도메인 없이 진행 가능한 로컬 구현을 기준으로 한다. 이번 작업은 감사이며 해당 기능들을 새로 구현했다고 처리하지 않는다.

## 별도 발견한 upstream 동작

PocketBase UI의 cron 실패 처리, relation picker 로딩 상태, 지정된 record 편집 onbeforeopen callback은 원본 함수를 추출해 stub 환경에서 재현했다. [재현 스크립트](repro-ui.mjs)와 [UI 보고서](ui.md)에 범위가 있다. 이는 PgBase의 기능 공백과 별도이며, PocketBase 전체 앱의 브라우저 재현이나 전체 upstream 테스트 통과를 주장하지 않는다.
