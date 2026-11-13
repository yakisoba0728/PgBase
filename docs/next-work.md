# 다음 작업과 완료 기준

2026-11-13 보완은 PR [#125](https://github.com/yakisoba0728/PgBase/pull/125)·[#133](https://github.com/yakisoba0728/PgBase/pull/133)으로 병합하고, 전체 CI·릴리스 gate 통과 후 [v0.3.1](https://github.com/yakisoba0728/PgBase/releases/tag/v0.3.1)과 운영 서버에 반영했다(schema 66). [구현·검증 근거](production-readiness-2026-11-13.md), [배포 기록](deployment-2026-11-13.md)을 따른다. 이번 최종 점검의 필수 코드 수정·문서·병합·배포 항목은 완료했다.

| 구분 | 항목 | 현재 상태 / 다음 조건 |
| --- | --- | --- |
| 소유자 요청으로 제외 | 운영 백업·복원 | 운영 백업 생성·외부 저장소·예약 설정을 하지 않음. 서버 밖 DB·파일·키 사본과 실제 복원 인수는 미완료. 소유자가 재개를 요청할 때 진행 |
| 완료 | SQL view·auth snapshot 일관성 | catalog·whole-row·기존 view 검사, realm 최종 diff 보존과 삭제 정책 회귀 통과 |
| 완료 | 서비스 토큰 로그·검색·파일 metadata·오류 field | schema 66·UI 구현, migration/복원·HTTP·UI·CI 통과, 운영 반영 |
| 완료 | 검증·문서·릴리스 | PostgreSQL matrix·race·Dart·E2E·restore·security·서명 gate 통과, v0.3.1 게시, 운영 schema 66/66·health 정상 |
| 다음 유지보수 | 선택적 의존성 갱신 | #126–#132는 보류 사유와 함께 닫음. 이번 릴리스의 필수 보안 패치는 포함. 새 버전은 다음 주기에 재검증 |
| 별도 소유자 결정 | npm/pub.dev 공개 패키지 | 이름·scope·공개 여부·token 미정. SDK source/conformance는 검증했으며 registry 배포는 하지 않음 |

과거 N/M/P/O 단계와 날짜별 미완료 표시는 [구현 이력](project-status.md)과 [TODO 이력](../TODO.md)에 당시 상태로 보존한다. 현재 열린 작업의 합계로 해석하지 않는다. schema 66 적용 뒤 이전 binary만 다시 배포하는 롤백은 지원하지 않는다.
