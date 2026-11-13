# 2026-11-13 운영 준비 보완

의존성 통합 PR #124 이후의 보완을 PR [#125](https://github.com/yakisoba0728/PgBase/pull/125)(`34d1d03f`)와 [#133](https://github.com/yakisoba0728/PgBase/pull/133)(`043dc2b3`)으로 병합했다. schema 66을 추가한다. 아래 구현과 검증·운영 반영을 구분한다.

## 구현

- SQL view 검사: `pg_depend`가 생략하는 pinned catalog를 resolved query tree의 permission reference로 검사한다. 전체 행의 JSON 변환도 password field를 포함하면 거부한다. 저장된 managed view의 참조를 재귀 검사하고 시작 preflight에서 기존 SQL view도 검사한다. 정상 projection·aggregation·count(*)는 유지한다. PostgreSQL 16/17/18의 permission bitmap 규약을 사용한다.
- Migration snapshot: 최종 field diff만 realm에 동기화해 중간 관계 분리 단계가 profile 값을 지우지 않게 한다. auth collection 생성은 realm을 함께 담는 schema import로 처리한다. auth 삭제는 기본 realm·사용자·다른 realm 관계를 검사한다. PgBase의 public migration 함수는 실제 auth validator를 제공하고, 직접 schema 함수를 호출하는 embedding은 auth 변경 때 validator를 제공해야 한다.
- Schema 66: request log의 service principal 허용, log 검색과 email 부분 문자열 검색의 pg_trgm 인덱스, dead-letter 부분 인덱스. 기존 migration checksum은 보존한다. migration 계정은 pg_trgm을 설치할 수 있어야 한다. 짧은 검색어는 trigram 인덱스의 효과가 제한된다.
- 백업 archive 형식은 유지하되 pg_dump가 확장 정의도 포함한다. schema 66의 인덱스를 새 DB에 복원할 때 pg_trgm operator class가 누락되지 않게 한다. **이는 제품 복원 기능의 회귀 수정이며 운영 백업 생성이 아니다.**
- 관리자 native record 목록의 `files=true`는 `{fileMetadata:{recordId:{field:[file]}}}`를 추가한다. 기본 응답은 유지한다. 200 records·10,000 files로 제한하고 기존 file authorization·policy를 거친다. 사용자·service principal에는 허용하지 않는다. UI는 목록 metadata로 기존 field cache를 채우며 download authorization은 계속 수행한다.
- storage·backup·SMTP 검증은 오류 field를 `error.details`에 담는다. SMTP는 발신 주소 형식도 검사한다. malformed schema import는 `document`를 표시한다.
- React와 React DOM, Tiptap 패키지를 Dependabot group으로 묶고 Node 24 지원 중 @types/node major 자동 갱신은 제외한다. Backend CI에서 Dart SDK analyze·unit test를 추가한다.

확장 Security image scan에서 x/net 0.61.0의 CVE-2026-78661(HIGH)을 발견해 0.62.0으로 올렸다. 일반 govulncheck의 호출 경로 검사와 이미지의 포함 모듈 검사는 범위가 달라, 두 결과를 함께 확인한다. [공식 Go advisory](https://pkg.go.dev/vuln/GO-2026-6860).

## 검증 상태

집중 검증: SQL catalog·whole-row·기존 view 우회 거부와 정상 쿼리, snapshot field rename/Down 값 보존·validator 누락·사용자가 있는 realm 삭제 거부, service-token 로그 principal, 관리자 bulk file metadata·사용자 접근 거부, literal substring 검색, field별 오류, UI file cache seed를 포함한다.

- Go 1.26.11 unit/race·go vet·lint 통과. govulncheck의 호출되는 취약점 0.
- Node 24.22.2: typecheck, UI 33파일/182 tests, build 통과.
- 최신 binary·schema 66·실제 CSP·독립 PostgreSQL 17 DB 두 개에 대해 browser suite 18/18 통과(관리형 백업 opt-in 포함, 모두 격리 테스트 데이터).
- schema 66 이후 DB/로컬 파일·S3 파일·extension object의 독립 복원 회귀 통과.
- [확장 Backend](https://github.com/yakisoba0728/PgBase/actions/runs/43367289853): PostgreSQL 16.13/17.9/18.3, Dart analyze/unit·실제 HTTP, multi-process durability, Windows self-update, macOS terminal, release-phase artifacts 통과.
- [확장 Security](https://github.com/yakisoba0728/PgBase/actions/runs/43367740347): x/net 수정 후 이미지 검사 통과. stale undici 알림 3건은 실제 main lockfile의 8.12.4와 공식 수정 버전을 대조해 inaccurate로 정리했다. 열린 Dependabot 보안 알림 0건.
- 종료 경합 수정은 deterministic unit/race 25회와 원래 PostgreSQL race 재현 test 30회 반복 통과. PR #125·#133 필수 CI 각 5개 통과 후 병합. 최신 main의 Windows self-update도 통과했다.
- `043dc2b3` main CI: [Backend](https://github.com/yakisoba0728/PgBase/actions/runs/43371117456), [Frontend](https://github.com/yakisoba0728/PgBase/actions/runs/43371117457), [Security](https://github.com/yakisoba0728/PgBase/actions/runs/43371117507) 모두 통과.

## 운영 제약

2026-11-13 소유자가 “백업은 하지마 일단 지금”이라고 지시했다. 이번 작업은 운영 백업을 생성하거나 외부·예약 백업을 구성하지 않는다. 서버 밖 복구 사본과 복원 인수는 완료되지 않았다. 이 위험을 일반적인 “운영 준비 완료”로 닫지 않는다.

Schema 65→66은 명시적인 migration이 필요하다. 기존 앱을 정지한 뒤 migrate와 새 앱 시작을 진행한다. schema 변경 후 이전 binary만 배포하는 롤백은 지원하지 않는다. 기존 unsafe SQL view가 있으면 startup preflight가 거부하므로 새 규칙에 맞게 수정해야 한다.

운영의 실제 사용자 데이터에 대한 CRUD·파일 변경·IP allowlist 저장은 수행하지 않는다. 로그인 세션의 읽기 전용 확인은 배포 기록에 별도로 남긴다. 계정·암호·token·키·사용자 데이터는 문서나 CI 로그에 기록하지 않는다.

## 릴리스 gate에서 발견한 종료 경합

v0.3.0 candidate의 PostgreSQL 17 race gate에서 `TestStartedAppSamplesSystemStatusOnceThroughTheNamedTaskRegistry` cleanup이 membership writer의 `write ... i/o timeout`을 실패로 보고했다. 정상 종료가 이미 context를 취소한 뒤 드라이버가 write timeout으로 응답하는 경우였다. 공통 background runner가 취소된 context의 transport timeout에 취소 원인을 연결하도록 수정한다. 실행 중 timeout과 취소 도중의 비-timeout 실패는 계속 보고한다. 세 경우를 시간에 의존하지 않는 unit test로 고정한다. v0.3.0 태그는 이동하지 않으며 실패한 candidate를 promotion하지 않는다. 후속 릴리스는 v0.3.1로 검증한다.

## 의존성 PR 정리

PR #110–#121은 #124에 통합하거나 Node 26 types 보류 사유로 닫았다. 이후 생성된 선택적 버전 갱신 #126–#132는 검증된 schema 66 릴리스의 의존성 집합을 유지하기 위해 보류 사유를 남기고 닫았다. 필수 보안 패치 x/net 0.62.0은 포함했다. 선택적 새 버전은 다음 유지보수 주기에 다시 검토한다.

## 릴리스와 운영 반영 완료

- [v0.3.1](https://github.com/yakisoba0728/PgBase/releases/tag/v0.3.1), source `043dc2b325af3bbb0f1caf69568f239fbed7e0c6`. [Signed release run 43371132129](https://github.com/yakisoba0728/PgBase/actions/runs/43371132129) 전체 성공: PostgreSQL 16.13/17.9/18.3, 17 race 두 shard, Dart conformance, browser E2E, independent restore, security, macOS, immutable release phases. Windows self-update는 같은 main의 Backend run에서 통과했다(release gate에서는 의도적으로 제외).
- `v0.3.1`·`latest` digest는 `sha256:07bbcff43b2644069acb3a1ae10d854ff2eb4ddcea1501243b104ee53d5bc7b6`. 서명 검증된 provenance/SBOM의 subject와 일치한다. GHCR는 private 유지. GitHub release에 증거·checksum을 첨부했다.
- Dokploy source build `043dc2b3` 배포: 기존 writer 정지 후 migration 실행, `2026-11-13T08:08:59.796Z` 완료. schema 65→66. 첫 요청은 빌드 중 Dokploy API가 일시적으로 502를 반환한 뒤 cancelled로 확인됐다. 같은 설정 재시도는 성공했다. 첫 중단의 원인은 확정하지 못했다.
- 실제 운영 관리자 세션의 읽기 전용 확인: migration 66/66, DB 연결·listener 준비, schema health 정상·finding 0, 기존 레코드 수 유지. HTTP health·deep link·인증 경계와 JS/CSS/lazy chunk SHA256을 확인했다. 앱·PostgreSQL healthy, migrate·files-init exit 0. 비밀값·환경 변수·도메인·volume·autoDeploy(false) 보존. [배포 세부](deployment-2026-11-13.md).
- 운영 백업 생성·예약 설정은 하지 않았다. 운영 데이터 CRUD·파일 변경·IP allowlist 저장과 외부 접속 IP의 정확한 일치는 확인하지 않았다. 해당 변경 흐름은 격리된 browser/HTTP test로 검증했다.
