# N1–N5 원격 병합과 기존 설치 반영

완료: 2026-09-10 03:24 KST 설치 인수 PASS, 03:28 KST 공용 기록 마감. 승인된 push→PR→필수 CI→정상 merge→기존 설치 백업·적용·인수를 완료했다. Sol이 설치하고 Terra가 절차·복구를 독립 검토했다. 제품 코드 변경, 보호 규칙 우회, force push는 없었다.

## 제품·문서 커밋과 CI

- [PR #34](https://github.com/yakisoba0728/PgBase/pull/34): 로컬 인수 `edf55690599fc63966d3e99996f9954649961074`에 승인 문서만 추가한 `432bfaa483eaa3eea1dfe68fefa90a0ec7dc1484`를 제출했다. 02:44 KST 정상 squash merge와 설치 제품은 모두 `2939f1543e4889c0ffa967eae398c91d0ecbb6ce`다. 제출·merge tree는 동일하다.
- origin/default branch는 `yakisoba0728/PgBase`/`main`. active ruleset `22511139`의 strict required checks 5개 모두 SUCCESS, bypass actor 없음. CI 수정·재실행은 없었다.
- [Backend CI](https://github.com/yakisoba0728/PgBase/actions/runs/34382981970): Go unit5m21s, PostgreSQL integration / 17.9 16m54s. 최종 tree의 전체 Go unit/race, PG17+MinIO integration/race·SDK live를 포함한다.
- [Frontend CI](https://github.com/yakisoba0728/PgBase/actions/runs/34382981951) `test`1m17s, [audit](https://github.com/yakisoba0728/PgBase/actions/runs/34382981935)1m3s, Observability artifacts25s. manual/release 전용 macOS terminal·Release phase artifacts는 PR의 비필수 skip이다.
- 설치 기록은 merged main 기반 `codex/n1-n5-installed-status`의 **로컬 문서 후속 커밋**이다. 제품 병합·설치 SHA와 구분하며 새 제품 PR이나 공개 릴리스가 아니다.

## 설치 결과와 보존

- Binary `.local/pgbase/bin/pgbase`, SHA-256 `77788e37c9b3c82fd934a0186992321aa9d0f0bd4a82d258eace20030cdce492`. 내장 VCS revision은 정확한 merge SHA, `vcs.modified=false`, Go1.26.8/Darwin ARM64다.
- PostgreSQL17.9 `pgbase-local-postgres-1`/`pgbase-local_data`, `127.0.0.1:55440`; schema **38→40**, 1–40 연속·1–38 원본 불변. PgBase는 `127.0.0.1:8090`, 완료 시 PID19035로 실행 중이다.
- 기존 `.local/pgbase/environment`, `files`, `backups`, 관리자 자격 증명을 보존했다. process 경로·binary hash·readiness·doctor(zero findings)·기존 관리자 login/logout·schema export·N4 non-applying preview·local storage 설정·managed backup 생성/다운로드/hash/삭제가 통과했다.
- N1 snapshot/history와 의도적으로 disabled인 auto-status, N3 CLI 가용성을 확인했다. 실제 앱은 **0 tables/0 rows**, 활성 관리자1명, 파일 객체0개·기존 managed archives4개다. app dump/config/admin·MFA·정책/files/archives fingerprint 보존을 확인했다. nonempty 설치 데이터 보존을 주장하지 않는다.

최신 복구점은 `/Users/yakisoba0728/Documents/GitHub/PgBase/.local/backups/pre-n1-n5-2939f1543e48-20260910-030338`이다. 보호 디렉터리에 fresh DB custom dump/list, 이전 binary/config/자격 증명 기록, files/archives/mail 상태와 hash inventory를 보존했다. 이 정확한 fresh dump를 격리 PG17에 복원하고 merged SHA로38→40·doctor/readiness/login·보존 canary를 통과한 뒤 live에 적용했다.

물리 fallback `/Users/yakisoba0728/Documents/GitHub/PgBase/.local/backups/pre-n1-n5-preparation-20260910-023051/postgres-data-volume.tar.gz`도 hash/전체 archive 검증 후 새 disposable volume에 실제 복원·PG17.9/schema38 기동·logical dump 추출을 통과했다. 전용 복원 container/volume은 정리했고 원본 volume은 보존했다. 최초 식별 당시 PgBase는 stale PID/8090 listener 없음, PG는00:21:48 KST부터 정지 상태였다. 준비 단계가 기존 실행 제품을 중단한 것은 아니다.

## 두 installer 오류와 실제 복구 이력

1. 03:10 첫 시도는 정확한 제품/schema40와 HTTP/login/backup smoke가 정상이었다. 그러나 installer가 opt-in이 꺼진 `migration auto-status`에 성공 exit를 요구해 실패했다. 정확한 exit1과 disabled 메시지를 기대하도록 수정했다.
2. 03:14 두 번째도 제품과 core smoke는 정상이었다. 이전 시도의 N1 snapshot 증거 파일을 덮어쓰려다 거절됐다. 시도별 증거 경로와 명시적 nonexistence/collision guard로 수정했다.

두 경우 모두 증거 처리 오류가 포괄적인 recovery 조건을 발동해 **정상 제품을 불필요하게 rollback**했다. 실제 복구는 이전 binary/config/schema38, doctor/readiness/login, app/admin-policy/files/archives 보존 및 원래 PgBase/PG 정지 상태까지 성공했다. 제품 결함이나 migration 수정은 없었다. 실패 시점 schema40 dump와 복구 결과는 private stage의 `failed-install-preserved-20260910-031014`, `failed-install-preserved-20260910-031440`에 보존했다.

03:23 내부 신호를 최종 installer SHA-256 `cd68eba44cb7b1266deee6968650ffa96fdd40f6b759a3ef6a8e48e2c81632c5`로 갱신했다. 추가 승인·검토 round 없이 같은 검증된 복구점으로 실행하여03:24 최종 exit0·설치 인수를 완료했다. 정상 core 이후 비변경 증거 오류는 해당 증거를 좁게 수정해야 하며 제품 오류로 보고하지 않는다.

## 검증 경계와 증거 위치

이전 전체 로컬 인수는 [통합 기록](integration.md)에 있다. 과거 backup500 제품 수정 주장과 20회 재현 근거는 폐기된 가설 이력이다. 실제 원인은 임시 harness의 non-waiting assertion이 browser를 닫아 in-flight 요청을 취소한 것이며 `830278d`를 `2837802`에서 exact revert했다. 최종 backup affected unit/race·PG18+MinIO 3건·checked-in auto-wait browser·독립 review가 통과했고, 이번 원격 CI는 exact 최종 tree의 전체 Go/PG17 gates를 추가로 통과했다. 설치 후 이미 green인 전체 검사를 반복하지 않았다.

N1 자동 기록·N2 public/SPA는 기존 설정대로 opt-in 비활성 상태다. N3 정책 변경·삭제로 기존 관리자 접근을 변형하지 않았다. N5 callbacks는 standalone binary에 등록되지 않으므로 실제 live hook 실행을 주장하지 않고 병합된 integration tests를 근거로 삼는다. 공개 release/npm/pub.dev, cloud deploy·외부 provider 설정은 수행하지 않았다. N6/Sol·N7/Terra는 별도 승인으로 전용 worktree에서 병렬 구현 중이며 그 계획·코드는 이 문서 커밋에 섞지 않았다.

비공개 증거 root: `.local/verification/n1-n5-release-stage/`. `installed-result.md`, `install-final-result.md`, `install-status.md`, `install-procedure.md`, `install-finish-events.jsonl`과 backup의 `upgrade-stage=complete`를 보존한다. 설치 SHA 표식은 `.local/verification/n1-n5-local-installed-commit`이다. `.local`은 clone에 포함되지 않으며 비밀 값은 공용 문서에 복사하지 않는다.
