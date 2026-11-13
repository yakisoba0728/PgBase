# 백엔드 재작성과 통합 검증

아래 결과는 최초 재작성 후보의 검증 기록이다. 후속 독립 검토에서 9건의 누락과 추가 경계 조건 2건이 발견되어 수정했다. 현재 소스의 수정 내용·재검토·검증 범위는 [독립 검토 결과](backend-independent-review.md)를 기준으로 한다. 최초 통과 기록만으로 재작성 완료를 판단하지 않는다.

이 작업은 [실행 계획](backend-redesign-plan.md)에 따라 PostgreSQL 기반 pgBase의 서비스 경계·실행 책임·오류와 종료 처리를 재구성했다. 성능 벤치마크와 처리량 튜닝은 후순위로 두었다. 기본 기능과 검증된 저장·암호화·잠금 기반을 재사용하며 대체된 중복 구현과 미사용 실행 경로를 제거했다.

운영 배포·릴리스 게시·운영 백업 설정은 하지 않았다. 기존 migration SQL과 checksum을 수정하지 않았으며 시스템 스키마는 66이다.

## 구현 범위

| 영역 | 최종 구조와 동작 |
| --- | --- |
| PostgreSQL 트랜잭션 | 소유자만 commit/rollback/retry를 수행한다. borrowed transaction은 대상 DB·상태·격리 계약을 검사한다. batch/truncate는 savepoint로 참여하고 실패 cleanup이 불확실하면 부분 commit을 막는다. |
| 레코드 쓰기 | CRUD·batch·truncate·검증의 공통 실행 경계를 `recordops/transaction.go`에 모았다. identity·revision·ownership·audit·outbox·파일 participant와 완료 훅의 원자성을 유지한다. |
| 조회 | list/get/count의 typed 계획과 SQL 실행을 정리했다. 집계가 페이지 SQL 문자열을 절단하지 않고 공용 row source에서 렌더링된다. native 커널에서 외부 호환 분기를 제거하고 입력·출력 어댑터로 옮겼다. |
| 필터와 ID | 기본 필터 parser가 single-quoted와 JSON double-quoted 문자열을 처리하므로 SDK binding에 별도 호환 요청이 필요 없다. ID 투영은 수집→일괄 해석→성공 후 적용하며 실패 시 입력을 유지한다. |
| Catalog·migration | 전역 cache에는 committed snapshot만 게시한다. 미커밋 candidate 공유와 rollback cleanup map을 제거했다. migration 실행·잠금·이력 기록의 중복 커널을 통합했다. |
| 규칙·권한 | 원문은 표시 metadata다. scalar/catalog 실행 의미는 명시적 AST binding에 저장하며 공통 정책 compiler가 owner·request·relation·event를 처리한다. HTTP·JS·schema import는 공통 parser와 최종 projected catalog 검증을 사용한다. |
| 인증 | 관리자·사용자 realm·서비스 신원의 별도 신뢰 경계와 세션·폐기·KDF·OTP/MFA/OAuth 계약을 유지한다. production 호출자가 없는 scalar auth evaluator를 제거하고 실제 SQL 평가 회귀로 옮겼다. |
| 파일·복구 | 업로드 byte 검증을 통합하고 staged handle 복사본의 완료와 lease 해제를 한 번만 처리한다. variant queue는 PostgreSQL trigger가 소유한다. backup manager 종료·receipt 실패 진단과 durable writer의 panic cleanup을 정리했다. |
| Realtime·jobs | snapshot별 정책 준비와 공통 live admission을 사용한다. jobs의 미사용 instance와 중복 완료 SQL을 제거하고 claim/lease/generation fence를 유지한다. |
| 로그 | 페이지 조회 중복을 제거하고 flush/shutdown의 소유권·완료 신호·최종 오류를 공유한다. 진단 로그의 bounded queue/drop 정책을 durable audit/outbox와 구분한다. |
| 앱 생명주기 | 자원 획득 즉시 App이 소유한다. 생성 실패와 정상 종료가 같은 `closeServices` 경로를 사용한다. readiness 하강→HTTP drain→worker 종료→서비스→pool→writer gate 순서를 유지한다. |
| JavaScript | `pgbase.scripts/v2`는 native module API만 설치한다. 명시적 v1은 기존 전역 어댑터를 선택한다. 검증·bootstrap·실행은 같은 VM 초기화를 사용한다. Host 보안·운영 정책과 호환 입력 alias를 분리했다. |
| JS migration·타입 | 기존 v1 source envelope와 checksum을 보존한다. v2에는 runtime format을 기록한다. 기본 타입 선언과 `js types`는 native이며 `--legacy`와 별도 legacy 선언을 제공한다. |
| CLI·SDK·UI | CLI directory flag와 env 우선순위를 통합했다. TS/Dart의 정책 snapshot/replay와 실제 native filter binding을 검증한다. UI는 typed 규칙과 표현식을 단일 정책으로 저장하며 실패 시 편집을 유지한다. |
| 지원 서비스 | SMTP 입력 검증·취소, settings key 교체의 commit 경계, SQL console validation의 종료/취소, user migration의 공통 안전 트랜잭션, self-update의 중복 분기를 정리했다. |

개별 메서드 수·소스 줄 수를 고정하던 내부 구현 테스트는 실제 typed 계약·의존 방향·native 커널 경계·UI route wrapper 검증으로 교체했다. `tools/repostats`는 진단 보고용으로 유지한다. 데이터·권한·실패·회귀 테스트와 skip 정책은 계속 적용한다.

## 명시적인 계약 변경

- 규칙 원문은 `expression`, 실행 binding은 `scalar` 또는 `catalog`다. 기존 저장 데이터의 `pocketbase` 원문 키는 decode 시 catalog binding으로 이행한다. 새 원문 metadata 유무로 실행 의미를 추론하지 않는다.
- 규칙을 포함하는 schema import는 최종 catalog 검증을 거친다. low-level caller가 validator를 생략하면 정확한 기본 owner-only 정책만 허용하고 custom 정책은 적용 전에 거부한다.
- 공개 realtime의 collection 구독·전달·collection-shaped broadcast도 collection opt-in과 action rules를 모두 요구한다. 관리자와 scoped service token의 별도 admission은 유지한다.
- borrowed write에서 hooks를 사용하면 `WithTransactionOutcome`을 전달해야 한다. idempotent create와 delete/batch/truncate는 호출자 Read Committed 계약을 요구하며 서비스가 격리를 바꾸지 않는다. lazy writer는 borrowed transaction 전에 준비되어야 한다.
- truncate는 기존 delete budget과 같은 **100,000 roots**를 상한으로 사용한다. 초과 시 mutation 전 `truncate_budget_exceeded`로 거부하고 부분 chunk commit을 수행하지 않는다.
- 신규 JS bundle은 native v2를 사용한다. 기존 v1 bundle과 역사 migration은 명시적 legacy 실행 의미를 유지한다. deprecated Go host 옵션 alias와 native Host를 동시에 지정하면 모호한 입력으로 거부한다.

## 검증 결과

지원 PostgreSQL 16/17/18 각각 63개 package 결과를 전체 목록과 대조해 누락·미해결 실패가 없음을 확인했다. 17에서는 전체 integration/race, 16/18에서는 전체 integration을 실행했다. 최초 실패와 수정 뒤 해당 package 전체 재검증을 합산한 상세 결과는 `integration-matrix-results.json`에 보관한다.

최종 전체 Go unit/race·formatting·lint·vet, UI 183개 테스트·타입 검사·내장 build, 브라우저 18개 시나리오(관리형 백업 포함), Docker E2E, 전체 restore drill, 최종 source의 schema66→동일 candidate upgrade/restore 3-major matrix, TS/Dart unit·실제 HTTP conformance와 6-platform cross-build가 통과했다.

## 검증 산출물

검증 로그와 disposable fixture는 저장소의 무시된 `.local/redesign/` 아래 보관한다. 최종 source 파일 목록과 SHA는 `final-source-manifest.json`, 운영 이미지 취약점 결과는 `security-image.json`, SPDX SBOM은 `security-sbom.spdx.json`에 기록한다. 기준 원본의 patch·untracked 파일·manifest도 같은 디렉터리에 보존했다.

| 검사 | 근거 |
| --- | --- |
| 전체 Go unit/race·skip 정책 | `root-unit-race.log` |
| Formatting·blocking lint·vet | `formatting.log`, `root-lint.log`, `root-vet.log` |
| PostgreSQL 16/17/18 전체 integration·17 race | `final-pg16.log`, `final-pg18.log`, `final-pg17-core.log`, `final-pg17-app.log`, 실패 package 재검증 `final-pg*-replay.log` |
| 실제 schema 66 기존 commit→동일 새 binary upgrade/restore | `H-final-candidate.log`, `H-final-candidate/`의 source·dump·file checksum manifest |
| 전체 restore drill | `final-restore.log` |
| TS·Dart·실제 HTTP conformance | `H-client*.log`, scoped Dart 3.13.3 analyze/test 결과 |
| 관리자 UI | `ui-typecheck.log`, `ui-unit-node24.log`, `ui-build.log`, `browser-e2e.log` |
| Docker E2E·생산 이미지 | `docker-e2e.log`, `docker-build.log` |
| 보안·공급망 | `security-govulncheck.log`, `security-npm-admin.log`, `security-image.json`, `security-sbom.spdx.json` |
| 플랫폼 빌드 | `cross-build.log`, `.local/artifacts/`의 여섯 실행 파일과 checksum |

최초 matrix에서 Debian PostgreSQL 도구의 실행 경로를 Alpine 경로로 사용한 로컬 wrapper 오류가 발생했다. 이를 container PATH 기반 실행으로 수정하고 영향 package를 다시 검증했다. 이전 recorder fixture의 잘못된 `{}` 정책도 유효한 owner-only 정책으로 바꾸어 전체 해당 package를 재검증했다. 공개 realtime HTTP fixture는 collection opt-in을 명시하고, 비활성 상태의 403→활성화→구독 성공을 확인하도록 갱신했다. 최초 실패를 숨기지 않고 원본 로그와 재검증 로그를 함께 보관한다.

Go vulnerability 검사는 도달 가능한 취약점 0건이며, 호출되지 않는 요구 module의 취약점 1건은 원본 검사 로그에 남아 있다. 생산 이미지 HIGH/CRITICAL 결과는 0건이다.

실제 외부 OAuth·SMTP 사업자, 운영 데이터와 운영 저장소, 각 대상 OS의 네이티브 실행은 이 로컬 인수의 대상이 아니다. 플랫폼 cross-build는 해당 OS에서의 실행을 증명하지 않는다. 기존 테스트 fixture와 실제 로컬 PostgreSQL/S3·HTTP·브라우저 경로로 계약을 검증한다.
