# PgBase 감사 개선 계획 패키지

고정 기준 `5cb721ff5590bf89b45ea9087d2ace0b74fe7127`의 tracked file **1,406개**, **313,082줄**, **57개 영역**, **71개 배치**를 PASS1/PASS2로 기록했다. 두 pass는 서로 다른 reviewer이며 파일별 blob·범위·관찰 메타데이터를 함께 보존한다. 이 coverage와 metadata는 이해의 자동 증명이 아니다.

이 패키지는 생성 설정·문서·구조화 감사 결과를 검토완료 계획으로 묶는다. generated/config/structured 파일은 생성원·구조·무결성 관점에서 검토했으며 새 정밀 재현은 **0건**, 제품 source/tests 수정은 **0건**이다. 제품 변경을 포함하지 않는다.

- [검토완료 계획](plan.md) · [구조화 계획](plan.json)
- [현재 finding 등록부](findings.json) · [통합 coverage](coverage.json) · [inventory](inventory.json)
- [검증 결과](validation.json) · [설계 결정](architect-decisions.md)
- [최종 위험 검토](final-risk-review.md) · [최종 위험 부록](final-risk-review-addendum.md)
- [역사 ledger 재검증](historical-ledger-recheck.json)
- [오프라인 파일 브라우저](audit-browser.html)
- [원본 PASS1/PASS2 증거 묶음](evidence.zip)

최종 원인별 항목은 **59개**다. 정적 코드 결함 24개, 계약 위험 3개, 유지보수 18개, 반려 7개, 보류 7개이며 개선 계획 대상은 45개다. 계획 대상에 포함되어도 계약 위험과 선택적 유지보수는 문서의 선행 조건을 먼저 확인한다.

우선순위와 실행 단계는 Root 최종 [등록부](findings.json)를 기준으로 한다. 원 검토자의 판정은 이력을 위해 보존했다. 추가 판정 중 Dart SSE는 P2, gzip 협상은 P3로 최종 조정했다. [도구 및 출처 재검증](root-tooling-recheck.json)은 감사 도구 경로와 중앙 인벤토리에 관한 오탐 반려 근거다. 앱 브라우저의 로컬 `file:` URL 정책 때문에 화면 동작 검증은 하지 못했다. HTML 구조 점검과 JavaScript 구문 검사는 별도로 수행했다.
