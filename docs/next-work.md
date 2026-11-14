# 다음 작업과 완료 기준

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
| 남음 | UI 개편 사후 확인 | 운영 로그인 후 화면 확인, 서버 `doctor`·binary 일치 job, 초기 setup·login OTP·다크/좁은 폭 화면의 browser 확인. 배포 전 서버 dump는 뜨지 않았다(UI만 변경) |
| 소유자 결정 | 다음 릴리스 tag | v0.3.1 이후 schema 67과 UI 개편이 tag 없이 main에 있다. 릴리스하려면 전체 release gate를 다시 통과해야 한다 |
| 남음 | 관리자 UI 추가 항목 | 작업 실행 이력은 서버 endpoint가 없어 미구현. browser suite는 Chromium 전용이고 CI의 E2E workflow는 수동 실행만 가능 |
| 다음 유지보수 | 선택적 의존성 갱신 | #126–#132는 보류 사유와 함께 닫음. 이번 릴리스의 필수 보안 패치는 포함. 새 버전은 다음 주기에 재검증 |
| 별도 소유자 결정 | npm/pub.dev 공개 패키지 | 이름·scope·공개 여부·token 미정. SDK source/conformance는 검증했으며 registry 배포는 하지 않음 |

과거 N/M/P/O 단계와 날짜별 미완료 표시는 [구현 이력](project-status.md)과 [TODO 이력](../TODO.md)에 당시 상태로 보존한다. 현재 열린 작업의 합계로 해석하지 않는다. schema 66 적용 뒤 이전 binary만 다시 배포하는 롤백은 지원하지 않는다.
