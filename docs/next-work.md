# 다음 작업과 완료 기준

2026-11-13 최신 작업은 [운영 준비 보완](production-readiness-2026-11-13.md)을 따른다. 과거 N/M/P/O 단계는 [구현 이력](project-status.md)과 [TODO 이력](../TODO.md)에 보존한다.

| 우선순위 | 항목 | 현재 상태 / 완료 기준 |
| --- | --- | --- |
| 1 | 운영 백업·복원 | 소유자 요청으로 이번 작업에서 제외. 운영 백업을 만들지 않는다. 서버 밖 DB·파일·키 사본과 복원 검증은 여전히 없음 |
| 2 | SQL view·auth snapshot 일관성 | 후속 코드와 재현/정상 회귀 test 추가. 전체 검사와 CI 후 병합 |
| 3 | 서비스 토큰 로그·검색·파일 metadata·오류 field | schema 66 및 UI 구현. migration/복원·HTTP·UI test와 CI 후 배포 |
| 4 | 검증·문서 | Dart analyze/unit·실제 HTTP, PostgreSQL matrix·race·browser·release gate와 운영 배포 근거를 최신 문서에 기록 |
| 5 | 공개 패키지 | npm/pub.dev package 이름·scope·공개 여부·token은 별도 소유자 결정. GitHub 이미지 릴리스와 구분 |

현재 관리자는 이미 존재하며 관리자 UI·backend-fixes·의존성 PR #124는 병합·배포됐다. 이들을 미구현/병합 대기로 다시 세지 않는다. 2026-11-13 main CI에서 Dart live HTTP acceptance, multi-process durability와 Windows self-update는 통과했다. 최신 공개 이미지 릴리스는 후속 release 결과를 기록하기 전까지 v0.2.3이다.
