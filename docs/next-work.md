# 다음 작업과 완료 기준

2026-11-18 기준: main과 운영 모두 `c2872ba6`(schema 69)이다. 관리자 UI 전체 화면 점검의 수정([PR #151](https://github.com/yakisoba0728/PgBase/pull/151)·[PR #153](https://github.com/yakisoba0728/PgBase/pull/153))을 배포했다. 운영은 여전히 관리자가 없는 setup 대기 상태다. 범위와 검증은 [현재 상태](project-status.md) §16을 따른다.

2026-11-17 기준: main은 `d64c729f`(schema 69)이고 운영은 main `57507741`을 빈 데이터로 새로 설치한 상태다. 플랫폼 강화([PR #148](https://github.com/yakisoba0728/PgBase/pull/148))를 병합하고 같은 날 배포했으며, main `57507741`을 [v0.4.0](https://github.com/yakisoba0728/PgBase/releases/tag/v0.4.0)으로 게시했다. 범위와 검증은 [현재 상태](project-status.md) §15를 따른다.

2026-11-15 기준: main은 `5cb9ea0d`(schema 67)이고 같은 commit이 운영에 배포돼 있다. 최신 릴리스 tag는 v0.3.1(schema 66)이라 그 뒤의 백엔드 재작성(PR #135–#137, schema 67), PR #138–#141, 관리자 UI 개편([PR #142](https://github.com/yakisoba0728/PgBase/pull/142))과 크기·모션 후속([PR #144](https://github.com/yakisoba0728/PgBase/pull/144))은 tag 없이 main에만 있다. 이번 UI 개편의 범위와 검증은 [현재 상태](project-status.md) §13을 따른다. 아래 2026-11-13 문단과 표는 당시 기록에 이번 항목을 더한 것이다.

2026-11-13 보완은 PR [#125](https://github.com/yakisoba0728/PgBase/pull/125)·[#133](https://github.com/yakisoba0728/PgBase/pull/133)으로 병합하고, 전체 CI·릴리스 gate 통과 후 [v0.3.1](https://github.com/yakisoba0728/PgBase/releases/tag/v0.3.1)과 운영 서버에 반영했다(schema 66). [구현·검증 근거](production-readiness-2026-11-13.md), [배포 기록](deployment-2026-11-13.md)을 따른다. 이번 최종 점검의 필수 코드 수정·문서·병합·배포 항목은 완료했다.

| 구분 | 항목 | 현재 상태 / 다음 조건 |
| --- | --- | --- |
| 소유자 요청으로 제외 | 운영 백업·복원 | 운영 백업 생성·외부 저장소·예약 설정을 하지 않음. 서버 밖 DB·파일·키 사본과 실제 복원 인수는 미완료. 소유자가 재개를 요청할 때 진행 |
| 완료 | SQL view·auth snapshot 일관성 | catalog·whole-row·기존 view 검사, realm 최종 diff 보존과 삭제 정책 회귀 통과 |
| 완료 | 서비스 토큰 로그·검색·파일 metadata·오류 field | schema 66·UI 구현, migration/복원·HTTP·UI·CI 통과, 운영 반영 |
| 완료 | 검증·문서·릴리스 | PostgreSQL matrix·race·Dart·E2E·restore·security·서명 gate 통과, v0.3.1 게시, 운영 schema 66/66·health 정상 |
| 완료 | 관리자 UI 개편([PR #142](https://github.com/yakisoba0728/PgBase/pull/142)) | 공용 컴포넌트·디자인 토큰·모션·결함 수정, unit 381·browser 23·필수 CI 통과, main 병합과 운영 배포(schema 67 그대로) |
| 완료 | UI 크기 확대·모션 강화([PR #144](https://github.com/yakisoba0728/PgBase/pull/144)) | PocketBase v0.40.6을 크기 기준으로만 참고해 글자·컨트롤·표 행 확대, 모션 재조정. unit 386·browser 23·필수 CI 통과, 운영 배포 |
| 완료 | 로딩·토스트 통일과 조회 최적화([PR #146](https://github.com/yakisoba0728/PgBase/pull/146)) | 토스트 하단 중앙·3 tone, 재조회 시 이전 내용 유지, cache 정책과 prefetch, count 생략·hydration 배치·`LIKE` 컴파일·gzip. unit 403·browser 23·필수 CI 통과, 운영 배포(schema 67 그대로) |
| 완료 | 플랫폼 강화 병합([PR #148](https://github.com/yakisoba0728/PgBase/pull/148)) | schema 68·69, 읽기 commit의 WAL flush 대기 제거, 보안·저장소·realtime·SDK 결함 수정. 전체 qualification 15개 job 통과, 부하 gate p99 13.2ms(기준 250ms, 1회). main 병합, 운영 배포(schema 69/69) |
| 완료 | 운영 초기화(소유자 지시) | DB·파일 volume과 서버 백업을 지우고 main `57507741`을 빈 상태로 재배포. 69/69, doctor 통과, setup 대기 |
| 완료 | 관리자 UI 전체 화면 점검([PR #151](https://github.com/yakisoba0728/PgBase/pull/151)·[PR #153](https://github.com/yakisoba0728/PgBase/pull/153)) | 173장 캡처로 찾은 깨짐 약 45건 수정, 회귀 6건 수정 후 재확인. unit 459·필수 CI 통과, 운영 배포(schema 69 그대로) |
| 완료(소유자 결정) | frontend audit gate 범위([PR #152](https://github.com/yakisoba0728/PgBase/pull/152)) | 배포 의존성만 막고 개발 의존성은 경고. picomatch GHSA-fc5q-39xc-vjpf 패치가 나오면 재검토 |
| 남음 | `internal/filestore` 업로드 게시 test 간헐 실패 | "upload publication blocked behind its own lease". PR #148의 lease 경로 경합 여부 조사 필요 |
| 남음 | UI 점검에서 미룬 항목 | 날짜·시간 입력 표기, 화면 쪽 email·URL·필수 검증, 모바일 표 보조 열, jobs pane 옆 목록, 비활성 danger 버튼 대비. Safari·Firefox·setup 화면·모바일 overlay 미확인 |
| 소유자 작업 | 운영 첫 관리자·설정 | setup token으로 첫 관리자를 만들고 SMTP 등 운영 설정을 다시 넣는다. 그 전까지 공개 호스트는 setup 대기 상태다 |
| 소유자 작업 | Dokploy API key 교체 | 작업 세션 기록에 key가 노출됐다 |
| 남음 | 운영 사후 확인 | 로그인 후 화면·CRUD·realtime·jobs·파일 동작은 운영에서 확인하지 않았다. 운영 백업은 없다 |
| 소유자 결정 | 쓰기 경로 WAL flush | 쓰기 요청당 약 2.5회. outbox 전달 commit을 완화하면 줄지만 전달·내구성 계약이 바뀐다 |
| 남음 | 보안 검토 대기 | CodeQL 25건의 조치 불필요 분류 재확인, 이미지 의존성 30건 검토 |
| 소유자 결정 | 운영 요청 지연 | 한국 접속이 Cloudflare LAX로 가서 요청당 약 0.5초. proxy 해제(DNS only)·유료 plan·서버 이전 중 선택. 코드로는 줄일 수 없다 |
| 소유자 결정 | 대량 텍스트 검색 | 컬렉션 텍스트 필드 trigram index가 없어 50만 행 검색 1.3초. migration·쓰기 비용·disk 증가가 따른다 |
| 남음 | UI 개편 사후 확인 | 운영 로그인 후 화면 확인, 서버 `doctor`·binary 일치 job, 초기 setup·login OTP·다크/좁은 폭 화면의 browser 확인. 배포 전 서버 dump는 뜨지 않았다(UI만 변경) |
| 완료 | 릴리스 [v0.4.0](https://github.com/yakisoba0728/PgBase/releases/tag/v0.4.0) | main `57507741`, schema 69. 릴리스 gate 통과, 이미지 `v0.4.0`·`latest` 승격, GitHub 릴리스와 첨부 8개 게시 |
| 남음 | `internal/cli` watch test flake | `TestJavaScriptWatchKeepsValidGenerationAndDoesNotRunMigrations`가 main push CI에서 한 번 실패, 재실행 통과. 응답 확인 직후 stdout을 세는 timing 문제 |
| 남음 | 관리자 UI 추가 항목 | 작업 실행 이력은 서버 endpoint가 없어 미구현. browser suite는 Chromium 전용이고 CI의 E2E workflow는 수동 실행만 가능 |
| 다음 유지보수 | 선택적 의존성 갱신 | #126–#132는 보류 사유와 함께 닫음. 이번 릴리스의 필수 보안 패치는 포함. 새 버전은 다음 주기에 재검증 |
| 별도 소유자 결정 | npm/pub.dev 공개 패키지 | 이름·scope·공개 여부·token 미정. SDK source/conformance는 검증했으며 registry 배포는 하지 않음 |

과거 N/M/P/O 단계와 날짜별 미완료 표시는 [구현 이력](project-status.md)과 [TODO 이력](../TODO.md)에 당시 상태로 보존한다. 현재 열린 작업의 합계로 해석하지 않는다. schema 66 적용 뒤 이전 binary만 다시 배포하는 롤백은 지원하지 않는다.
