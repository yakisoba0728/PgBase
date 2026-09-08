# PocketBase 전체 소스 대조 감사 — 완료 보고

요청: PocketBase 소스 전체를 내려받아 파일별로 읽고, GPT 5.6 Sol 에이전트로 현재 PgBase와 대조한다. 문서상의 기능 이름이나 기존 65–70% 추정을 결론으로 삼지 않는다.

- Upstream: `pocketbase/pocketbase`, tag `v0.40.3`, commit `5684ee24f1e88f71a2ede73d20cf547fbd509e16`.
- PgBase baseline: `35af64c3e4736af0a8a6d2d3ecb36640e035840a`.
- 전체 Git clone: `.local/upstream/pocketbase` (외부 소스를 PgBase 소스로 재배포하지 않음).
- 추적 파일 923개 전체의 경로·종류·크기·줄 수·SHA256·담당은 [inventory.json](inventory.json)에 기록한다.
- `read`: 담당자가 전체 본문을 실제 읽은 handwritten source/test/document. `inspected_artifact`: generated/vendored asset, binary, lockfile 등을 용도·원본·메타데이터로 확인한 항목. 둘을 같은 독해 완료로 집계하지 않는다.
- 합산 결과: 본문 판독 734개, 생성물·자산 186개, 역사 참고 문서 3개. 미판독 소스 0개. 생성물·과거 변경 이력을 본문 독해 완료에 섞어 세지 않는다.

| 영역 | 파일 | 담당 | 상태 |
| --- | ---: | --- | --- |
| APIs, forms, mails | 90 | GPT 5.6 Sol — public_auth | 90 read 완료 |
| Core, migrations | 147 | GPT 5.6 Sol / Root field 담당 | 147 read 완료 |
| Admin UI | 394 | GPT 5.6 Sol / Root CSS 담당 | 230 read / 164 artifact |
| Tools | 185 | GPT 5.6 Sol / Root archive·cron·DB helpers | 185 read 완료 |
| Plugins, CLI, tests fixtures, root/build/release | 107 | Root 총괄 | 82 read / 22 artifact / 3 reference |

## 감사 기준

1. 각 API/모델/화면의 정상·오류·경계 동작을 구현과 테스트로 추적한다.
2. PgBase 대응 구현을 직접 열어 확인하고 `provided`, `partial`, `missing`, `different`를 구분한다. 이름이 비슷하다는 이유로 동등 처리하지 않는다.
3. 실제 외부 환경 미검증과 코드 미구현을 별도 표시한다.
4. 차이는 upstream pinned source line과 PgBase 대응 파일을 근거로 기록한다.
5. 사용자가 요청한 코드·기능 감사이며 보안 항목만으로 범위를 축소하지 않는다.
6. 소스 독해와 코드 실행 검증은 별개다. 실행하지 않은 동작을 재현했다고 기록하지 않는다.

## 변경 기록

- 2026-09-09: 전체 저장소 clone, immutable upstream/PgBase 기준 고정, 923-file inventory 생성, Sol 2명 독해 시작. 기존 진행률은 전수 대조 전 가설로 취급한다.

- Historical changelog 3개는 `inspected_reference`: 버전 목록을 확인한 참고 문서이며 전체 본문 독해 수에 포함하지 않는다. 현재 구현·테스트·현재 릴리스 문서는 실제 본문을 읽는다.
- Root의 `core-fields-ledger.json`은 core ledger의 동일 경로 indexed보다 우선한다.

## 보고서

- [통합 평가와 후속 구현 완료 기준](assessment.md)
- [API·인증·메일](api.md), [Core](core.md), [필드 상세](core-fields.md)
- [관리자 UI](ui.md), [공통 도구](tools.md), [cron·archive·DB helpers](tools-extra.md)
- [확장·CLI·운영](operations.md)
- [합산 coverage](coverage.json): 담당 ledger와 root override를 병합하고 923개 체크섬을 검사한다. `coverage.py`는 기록의 일관성 검사이며 실제 독해를 대신하지 않는다.
- Root override: `core-fields-ledger.json`, `ui-css-ledger.json`, `tools-extra-ledger.json`.
- [Upstream UI 함수 재현](repro-ui.mjs): cron 오류 처리·관계 선택창 로딩·레코드 편집 확장 callback의 stub 재현.

## 최종 검증

- 공식 clone은 shallow가 아닌 전체 Git 저장소이며, 정확히 `v0.40.3`에 고정했다.
- 모든 담당 ledger의 경로 집합·중복·상태·메모와 scoped override를 확인했다. 합산 923개가 inventory와 일치하고 체크섬 변경은 없다.
- [`coverage.py`](coverage.py) 실행 결과 pending 0. 이는 기록과 파일 무결성 검사이며, 독해 근거는 각 파일의 검토 메모다.
- [`repro-ui.mjs`](repro-ui.mjs)의 원본 함수 추출/stub 재현 3개 통과. 전체 PocketBase 앱을 기동하거나 전체 upstream 테스트 스위트를 실행한 결과는 아니다.
- 감사 보고서의 로컬 링크 및 pinned upstream 경로·행 범위, `git diff --check` 확인.
- PgBase 제품 코드와 pinned upstream 소스는 변경하지 않았다. 보고서·검토 기록·재현 도구·TODO만 작성했다.
- 2026-09-09: Sol 에이전트의 API/core/UI/tools 전수 본문 검토 및 root의 field/CSS/operations/helper 검토 완료. 출력이 잘린 범위는 재열람 후 완료 처리했다.
