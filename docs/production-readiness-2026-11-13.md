# 2026-11-13 운영 준비 보완

기준은 의존성 통합 PR #124의 `8b078a8c`, schema 65다. 이 후속 변경은 schema 66을 추가한다. 아래 구현과 검증·운영 반영을 구분한다.

## 구현

- SQL view 검사: `pg_depend`가 생략하는 pinned catalog를 resolved query tree의 permission reference로 검사한다. 전체 행의 JSON 변환도 password field를 포함하면 거부한다. 저장된 managed view의 참조를 재귀 검사하고 시작 preflight에서 기존 SQL view도 검사한다. 정상 projection·aggregation·count(*)는 유지한다. PostgreSQL 16/17/18의 permission bitmap 규약을 사용한다.
- Migration snapshot: 최종 field diff만 realm에 동기화해 중간 관계 분리 단계가 profile 값을 지우지 않게 한다. auth collection 생성은 realm을 함께 담는 schema import로 처리한다. auth 삭제는 기본 realm·사용자·다른 realm 관계를 검사한다. PgBase의 public migration 함수는 실제 auth validator를 제공하고, 직접 schema 함수를 호출하는 embedding은 auth 변경 때 validator를 제공해야 한다.
- Schema 66: request log의 service principal 허용, log 검색과 email 부분 문자열 검색의 pg_trgm 인덱스, dead-letter 부분 인덱스. 기존 migration checksum은 보존한다. migration 계정은 pg_trgm을 설치할 수 있어야 한다. 짧은 검색어는 trigram 인덱스의 효과가 제한된다.
- 백업 archive 형식은 유지하되 pg_dump가 확장 정의도 포함한다. schema 66의 인덱스를 새 DB에 복원할 때 pg_trgm operator class가 누락되지 않게 한다. **이는 제품 복원 기능의 회귀 수정이며 운영 백업 생성이 아니다.**
- 관리자 native record 목록의 `files=true`는 `{fileMetadata:{recordId:{field:[file]}}}`를 추가한다. 기본 응답은 유지한다. 200 records·10,000 files로 제한하고 기존 file authorization·policy를 거친다. 사용자·service principal에는 허용하지 않는다. UI는 목록 metadata로 기존 field cache를 채우며 download authorization은 계속 수행한다.
- storage·backup·SMTP 검증은 오류 field를 `error.details`에 담는다. SMTP는 발신 주소 형식도 검사한다. malformed schema import는 `document`를 표시한다.
- React와 React DOM, Tiptap 패키지를 Dependabot group으로 묶고 Node 24 지원 중 @types/node major 자동 갱신은 제외한다. Backend CI에서 Dart SDK analyze·unit test를 추가한다.

## 검증 상태

집중 검증: SQL catalog·whole-row·기존 view 우회 거부와 정상 쿼리, snapshot field rename/Down 값 보존·validator 누락·사용자가 있는 realm 삭제 거부, service-token 로그 principal, 관리자 bulk file metadata·사용자 접근 거부, literal substring 검색, field별 오류, UI file cache seed를 포함한다.

- Go 1.26.11 unit/race·go vet·lint 통과. govulncheck의 호출되는 취약점 0.
- Node 24.22.2: typecheck, UI 33파일/182 tests, build 통과.
- 최신 binary·schema 66·실제 CSP·독립 PostgreSQL 17 DB 두 개에 대해 browser suite 18/18 통과(관리형 백업 opt-in 포함, 모두 격리 테스트 데이터).
- schema 66 이후 DB/로컬 파일·S3 파일·extension object의 독립 복원 회귀 통과.
- PostgreSQL 16/17/18 원격 matrix, Dart analyze/unit, release gate와 운영 반영 결과는 완료 후 추가한다. 기록되지 않은 검증을 완료로 해석하지 않는다.

## 운영 제약

2026-11-13 소유자가 “백업은 하지마 일단 지금”이라고 지시했다. 이번 작업은 운영 백업을 생성하거나 외부·예약 백업을 구성하지 않는다. 서버 밖 복구 사본과 복원 인수는 완료되지 않았다. 이 위험을 일반적인 “운영 준비 완료”로 닫지 않는다.

Schema 65→66은 명시적인 migration이 필요하다. 기존 앱을 정지한 뒤 migrate와 새 앱 시작을 진행한다. schema 변경 후 이전 binary만 배포하는 롤백은 지원하지 않는다. 기존 unsafe SQL view가 있으면 startup preflight가 거부하므로 새 규칙에 맞게 수정해야 한다.

운영 로그인 이후 CRUD·파일·호출자 IP는 계정 접근이 가능한 경우에만 확인한다. 계정·암호·token·키·사용자 데이터는 문서나 CI 로그에 기록하지 않는다.
