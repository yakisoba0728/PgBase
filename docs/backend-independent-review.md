# 재작성 후 독립 검토

최초 재작성의 검증 통과 뒤에도 아래 9건이 남아 있었다. 구현자와 별개의 에이전트 4명이 데이터·정책·파일·완결성을 나누어 검토하고, 별도 수정 담당이 보완한 뒤 원래 검토자가 다시 확인했다. 기존 코드의 호출자·공통 실행 경계·대체 구현과 중복 책임을 함께 확인했다. PocketBase 구조를 내부 설계 기준으로 채택하지 않았으며, 기존 v1 실행 의미는 명시적 호환 경계에 유지했다.

## 발견과 수정

| ID | 누락 | 수정과 회귀 |
| --- | --- | --- |
| IR1 | borrowed 단일 쓰기를 batch child로 취급해 validator·공개 ID·숨김 필드 투영을 생략 | 명시적 batch 표식을 사용한다. 단일 쓰기는 savepoint 안에서 검증·투영하고 실패 시 되돌린다. JS transaction에서 오류를 잡고 commit해도 잘못된 변경이 남지 않는다. |
| IR2 | batch의 idempotent replay를 너무 일찍 공개 ID로 투영 | 검증과 participant 실행까지 내부 ID를 유지하고 최종 응답에서 투영한다. text·UUID 형태의 공개 ID 회귀를 추가했다. |
| IR3 | 양쪽 Many operand가 같은 SQL alias를 사용해 권한 비교를 자기 비교로 바꿈 | 공통 operand projection scope에서 양쪽 바인딩을 분리한다. relation·auth·request 배열 비교의 실제 SQL 결과를 검증했다. |
| IR4 | cross-collection Many operand의 기준 FROM 누락 | collection row source를 포함한다. relation과 JSON each 조회 회귀를 추가했다. |
| IR5 | 저장 시 런타임 request 값이 없어 정상 정책을 거부 | 구조·타입 검증을 실행 값 평가와 분리한다. method·query·header·auth 정책의 저장부터 실행까지 검증했다. 실제 실행에서 요청이 없으면 계속 거부한다. |
| IR6 | staged 파일의 원래 lease backend가 죽어 GC된 뒤에도 publish 가능 | reference lock 아래에서 원래 transition lease와 모든 추가 staged object의 소유권을 확인한다. 단일·다중 handle, backend 종료·GC·lock 인계를 검증했다. |
| IR7 | JS 검증의 timeout이 VM을 interrupt하지 않아 top-level 무한 루프에 걸림 | context 취소를 VM interrupt에 연결하고 watcher 종료·state 정리를 기다린다. 무한 루프 회귀를 추가했다. |
| IR8 | bundle을 v2로 바꾸면 적용된 v1 migration의 checksum이 달라짐 | migration별 `runtimeFormat`을 사용하고 v1 envelope bytes를 보존한다. v1 적용→mixed v2 추가→백업·복원→양쪽 Down을 검증했다. 기존 v1 migration은 v2 bundle에서 `runtimeFormat: "pgbase.scripts/v1"`을 지정한다. |
| IR9 | 규칙 preview가 UUID만 받아 text ID collection에서 동작하지 않음 | UI→HTTP→서비스에 공개 문자열 ID를 전달하고 같은 transaction에서 내부 ID로 해석한다. 없는 ID는 deny하고 일반 mutation의 strict ID 검증은 유지한다. |

IR3/IR5 재검토에서 빈 query/header의 `:length` 처리와 helper alias가 유효한 collection 이름을 가리는 문제도 발견했다. modifier 적용을 누락하지 않도록 고치고 예약 prefix 및 outer alias 회피를 적용했다. 실제 충돌 이름의 테이블과 빈 값 정책을 검증했다. 네 검토자 모두 원래 finding의 해결을 재확인했으며, 수정 경계에서 추가 확정 문제는 보고하지 않았다.

## 검증 근거

실패 재현과 수정 후 로그는 `.local/redesign/independent/`에 보관한다. `transactions-before.log`, `policy-before.log`, `policy-edge-before.log`, `storage-before.log`, `js-regression-before.log`가 수정 전 근거다. UI text ID 전달과 HTTP preview, native JS transaction validator, mixed migration의 실제 복원 경로에도 회귀를 추가했다.

최종 검사 결과는 아래에 기록한다. 최초 재작성의 전체 3-major matrix·보안 검사·cross-build 기록과 이번 변경의 검증 결과를 구분한다. 현재 소스는 최초 candidate 이미지·binary와 다르므로 이전 산출물을 현재 소스의 출시 산출물로 사용하면 안 된다.

이번 수정 소스로 이미지를 재빌드하고 동일 digest의 E2E·3-major 복원·보안 검증도 완료했다. 현재 후보와 검증 근거는 [후보 이미지 최종 검증](backend-candidate-validation.md)에 기록한다.

| 검사 | 결과·로그 (`.local/redesign/independent/`) |
| --- | --- |
| 전체 Go unit·race·skip 정책 | 통과, `final-unit.log` |
| Formatting·lint·vet | 통과, `final-format.log`, `final-lint.log`, `final-vet.log` |
| PostgreSQL 17.9·S3 영향 package 전체 integration·race | 11개 package 통과, `final-integration.log` (accessrule·userauth·recordaccess·recordidentity·recordops·filestore·query·realtime·backup·jsvm·pgbase) |
| PostgreSQL 16.13/18.3 신규 SQL·lease 회귀 | 각 5개 package 통과, `final-pg16-regressions.log`, `final-pg18-regressions.log` |
| JS migration의 v1→mixed v2·백업·복원·Down | 통과, `js-integration.log` |
| UI unit·타입·내장 build | 184개 통과, `ui-all.log`, `ui-typecheck.log`, `final-ui-build.log` |
| 실제 브라우저·관리형 백업 | 18개 통과, `final-browser.log` |

검증용 binary는 UI 재빌드 후 `.local/redesign/independent/pgbase`에 생성했다. SHA256은 `dbf3aab080472d1ccaf4afa2f592785990b6c0460b352309821947a56260a5c6`이다. 현재 소스의 파일별 SHA256은 `final-source-manifest.json`에 기록한다. 브라우저용 두 앱과 DB, 검증용 PostgreSQL·S3 container·volume은 검사 후 제거했다.

운영 배포, 운영 데이터·실제 외부 OAuth/SMTP, 처리량 측정과 각 대상 OS의 네이티브 실행은 검증 범위에 포함하지 않는다. 독립 검토와 회귀 통과가 결함 부재를 보장하지 않는다.
