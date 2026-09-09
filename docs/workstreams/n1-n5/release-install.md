# N1–N5 원격 병합과 기존 설치 반영

2026-09-10 사용자 승인은 로컬 인수 완료 `edf5569`의 push→PR→required CI→정상 merge와 기존 PG17/schema38/HTTP8090 설치의 백업→binary/schema40 적용→실제 인수까지 포함한다. 이전 문서의 원격·설치 제외는 당시 범위다. N6–N9 구현, 공개 release/npm/pub.dev, cloud deploy, 외부 provider 설정은 이 단계에서 수행하지 않는다.

## 원격 상태

- 시작 branch `codex/n1-n5-integration`, clean `edf55690599fc63966d3e99996f9954649961074`.
- origin `yakisoba0728/PgBase`, default `main`, 기준 `238cc37d03f3e15cdd33a626cb7f8873cb48768c`. 기존 N1–N5 PR 없음.
- main active ruleset `22511139`는 strict required checks `Go unit`, `Observability artifacts`, `PostgreSQL integration / 17.9`, `test`, `audit`를 요구한다. 승인 review 수는 0, review thread 해결 필수, bypass actor 없음. force push·보호 규칙 우회 없이 PR을 병합한다.
- PR 작성 중. 설치는 required CI와 merge 완료 후 실행한다.

## 설치 준비·소유권

02:25 KST: 별도 `install-stage` worktree의 GPT-5.6 Sol이 `.local/verification/phase4-*` 기록과 live 상태를 식별하고, `ci-stage`의 GPT-5.6 Terra가 CI 및 설치 절차를 독립 검토한다. 기존 worker ID 재호출 실패로 모델을 명시한 별도 실행을 사용했다. 권한·자동승인 거절은 없다. 현재 checkout의 공용 문서는 통합 담당 단일 소유다.

확인할 기존 설치는 PG17/schema38/HTTP8090이며, 정확한 process/config/binary/files/archives 경계와 별도 backup destination을 먼저 기록한다. DB·files·archives·config·binary의 inventory/hash 및 복원 검증을 갖춘 중단→백업→candidate canary→migration38→40→재시작 절차를 작성·독립 검토한 뒤 실행한다. 실패 시 복구 절차도 같은 기록에 포함한다. 준비 자료는 비공개 `.local/verification/n1-n5-release-stage/`에 보존하며 비밀 값은 공용 문서에 기록하지 않는다.

## 인수 근거와 남은 단계

로컬 전체 인수는 [통합 기록](integration.md)에 있다. 전체 Go unit/race·PG18+MinIO integration/race는 `830278d`에서 green이며, 잘못된 backup product 가설은 `2837802`에서 exact revert했다. 최종 backup 상태는 unit/race·PG18+MinIO 3건·checked-in auto-wait 방식 browser·독립 review로 다시 확인했다. 광범위 green은 변경 없는 package에 적용하고 backup은 이 영향 검증으로 보완한다. 임시 harness의 non-waiting assertion이 원인이며 product500 수정 완료로 보고하지 않는다.

남은 단계는 PR required CI, 정상 merge SHA 확정, 검증 가능한 별도 백업, 설치 binary/hash/schema40 확인, doctor/readiness/login과 적용 가능한 N1–N5 smoke·데이터 보존이다. 제품 merge SHA와 설치 제품 SHA 및 이후 상태 문서 commit은 구분해서 기록한다.
