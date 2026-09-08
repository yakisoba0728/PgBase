# pgBase 코드 감사 — 2026-09-08

대상 커밋: `a3065c88e2a82712a41408012b704db3eb9e71f4`.
변경분이 아닌 현재 저장소 전체를 대상으로 기능 정확성, 데이터 일관성, 동시성, 모듈 경계와 테스트 누락을 검토했다.
Sol(`gpt-5.6-sol`) 에이전트 3개가 영역을 나누어 검토했고, 주 에이전트가 발견 사항을 대조하고 별도 재현 및 실행 검증을 수행했다.

**확정 결함은 P2 3건, P3 1건이다.** 기존 테스트는 통과하지만, 스키마 변경과 레코드 크기 계약의 연결, 실패한 화면 이탈과 편집 상태의 연결, 페이지가 있는 조회와 관계 표시의 연결에 누락이 있다. 운영 명령의 실패 진단 누락도 재현했다. 아래 발견 사항과 실행 기록은 수정 전 감사 시점의 결과다.

## 검증 및 수정

네 건 모두 수정 전 실패하는 회귀 테스트로 재확인한 뒤 수정했다.

| 항목 | 반영한 수정 | 저장소 회귀 테스트 |
|---|---|---|
| A1 | 행을 늘릴 수 있는 스키마 변경의 DDL 후, 같은 트랜잭션에서 기존 레코드의 최종 JSON을 검사한다. 2 MiB 초과 시 DDL·카탈로그·버전 변경을 롤백한다. bigint/numeric 문자열, UTC 날짜, JSONB escaping, 다중 관계 배열을 포함하며 행을 하나씩 처리한다. drop-only 변경은 레거시 데이터 복구 경로로 유지한다. | [record size integration](../internal/recordops/record_size_live_integration_test.go), [schema boundary integration](../internal/recordops/schema_record_size_integration_test.go) |
| A2 | 이탈 확인은 draft 등록을 지우지 않는다. 편집기의 저장 완료·해제·unmount가 등록을 해제하므로 로그아웃 실패 후에도 보호가 유지된다. | [App](../ui/admin/src/app.test.tsx), [draft ledger](../ui/admin/src/routing/unsaved-draft.test.tsx) |
| A3 | 같은 ID 필터로 후속 커서를 조회한다. 배치 ID 수만큼으로 요청 횟수를 제한하며 실패·예산 소진 시 미확인 ID를 없는 대상으로 단정하지 않는다. unmount 후에는 다음 페이지를 요청하지 않는다. | [relation picker](../ui/admin/src/relation/relation-picker.test.tsx) |
| A4 | 상태 진단 쿼리와 활성 writer 조회의 DB 실패를 기존 CLI operation logger로 전달한다. 출력 스트림 오류는 그대로 반환한다. | [schema-index CLI](../internal/cli/schema_index_integration_test.go) |

A1의 스키마 검사는 행 수에 비례하는 읽기를 추가한다. 기존 statement timeout과 스키마 쓰기 잠금 안에서 실행되며, 검사 실패 시 변경이 커밋되지 않는다. 지원하지 않는 직접 SQL 데이터 변경을 복구하거나 기존 레코드를 자동으로 축소하는 마이그레이션은 아니다.

수정 후 로컬 검증: Go 1.26.7 전체 단위 테스트 및 PostgreSQL 16.13 전체 통합 테스트(race 포함), Go lint/vet, UI 386개 테스트·타입 검사·빌드, Chromium E2E 8개, Docker E2E를 통과했다. 추가 경계 회귀는 2 MiB 허용/1바이트 초과 거절과 롤백을 검증한다. HTTP 재검증에서는 8번째 기본값 추가가 422로 거절되고 이후 목록·단일 조회·일반 PATCH가 모두 200을 반환했다.

코드 분량 제한은 올리지 않았다. 데이터 그룹 변경은 +122/−111줄(순증 11줄)이며 실제 기준 16,734→16,745줄로 기존 16,747줄 제한 이내다. 새 검사 경로는 필드 메타데이터 생성, telemetry 종료 처리, 기본값 파싱과 카탈로그 중복 확인을 통합해 대부분 상쇄했다. Go+UI 전체 런타임은 35,246→35,264줄(제한 35,271), UI는 3,522→3,527줄(제한 3,527)이다. 공개 함수·타입·메서드 수는 462로 유지된다.

원래 감사의 `/tmp` 자료는 당시 로컬 진단 기록이며, 지속적인 재현·검증에는 위 저장소 테스트를 사용한다.

## 수정 전 감사 기록

감사 시작 시 추적 파일은 526개였다. 저장소의 `tools/repostats` 정의로 Go 런타임 31,724줄/148파일, UI 런타임 3,522줄/27파일, Go 테스트 220파일, Vitest 테스트 24파일, Playwright 테스트 7파일이다. Go 런타임 수치는 도구 정의상 `internal/testutil`을 포함한다.

| 담당 | 검토 범위 |
|---|---|
| Sol 데이터 커널 | schema, query, recordops, relation, membership, cursor, database, recordartifact, jsonvalue, identity, validation, SQL 마이그레이션 |
| Sol API·런타임 | api, adminauth, cli, config, doctor, observability, releasephase, pgbase, cmd, UI Go 임베딩 |
| Sol UI·전달 구성 | 관리자 UI, UI 테스트, Docker·Compose, 워크플로, 스크립트, 저장소 정책, 테스트 도구와 관련 문서 |
| 주 에이전트 | 모듈 간 계약 대조, 결함 재현, 지원 PostgreSQL 버전별 통합 테스트, Docker·브라우저 E2E, 복구 테스트 |

**A1 — [P2] 성공한 스키마 변경이 기존 레코드를 조회·수정할 수 없는 크기로 만든다.**

주요 위치: [스키마 적용](https://github.com/webisnoteasy/PgBase/blob/a3065c88e2a82712a41408012b704db3eb9e71f4/internal/schema/operation_apply.go#L272), [새 필드 추가 계획](https://github.com/webisnoteasy/PgBase/blob/a3065c88e2a82712a41408012b704db3eb9e71f4/internal/schema/planner.go#L209), [레코드 크기 계약](https://github.com/webisnoteasy/PgBase/blob/a3065c88e2a82712a41408012b704db3eb9e71f4/internal/recordops/types.go#L15).

레코드 Create/Update는 최종 레코드 JSON을 2 MiB로 제한하지만, 스키마 Apply는 기본값을 가진 컬럼을 추가한 뒤 기존 레코드의 크기를 검증하지 않는다. `ADD COLUMN ... DEFAULT`는 기존 행에서 읽히는 값도 늘린다. 따라서 레코드 쓰기 경로가 보장하던 제한을 스키마 변경 경로가 우회한다.

실제 HTTP 서버와 PostgreSQL 16.13에서 다음 순서로 재현했다. 테스트 대상 레코드나 컬렉션을 직접 SQL로 조작하지 않았다.

1. 관리자 설정과 컬렉션 생성 후 작은 레코드를 생성한다. 목록 조회는 200이다.
2. JSONB 기본값이 256 KiB인 필드를 하나씩 17회 추가한다. 이전 필드의 기본값은 다음 변경에서 제거한다. 기본값을 제거해도 이미 존재하는 레코드의 필드 값은 유지된다.
3. 각 plan/apply 요청은 HTTP 본문 제한 1 MiB 이내이다. 실제 apply 본문은 262,372~262,924바이트였고, 17회 모두 200이었다.
4. 이후 조회와 일반 필드 수정이 실패한다.

```text
GET list:             HTTP 422  details.response = ["response_too_large"]
GET single:           HTTP 500  code = "response_too_large"
PATCH ordinary field: HTTP 422  details.record = ["record_too_large"]
```

별도의 커널 재현에서도 최종 레코드 JSON 4,456,856바이트를 확인했다. 2 MiB 쓰기 제한과 단일 HTTP 조회 제한 4,198,400바이트를 모두 넘는다. 스키마 변경은 기존 행 전체에 영향을 주므로 여러 행이 함께 이 상태가 될 수 있다. 데이터 자체가 삭제된 것은 아니지만, 일반 관리자 화면에서 조회와 편집을 계속할 수 없고 크기를 줄이는 별도 조치가 필요하다.

수정 방향은 레코드와 스키마 변경이 같은 최종 크기 계약을 지키도록 하는 것이다. 기존 행을 바꾸는 기본값 추가 등을 적용 트랜잭션에서 검증해 롤백하거나, 크기 초과를 명시적으로 처리하는 변경 절차를 제공해야 한다. 단일 Get의 HTTP 상한만 높이면 목록 및 일반 PATCH 실패는 남는다.

추가할 회귀 테스트는 “정상 레코드 생성 → 기본값 추가/제거를 포함한 스키마 변경 → 목록·단일 조회·일반 수정”을 연결해야 한다. 현재 크기 테스트는 Create/Update의 제한과 이미 큰 레거시 행의 축소를 다루지만 스키마 변경에 의한 증가를 다루지 않는다.

재현 자료: `/tmp/pgbase-audit-http-size-repro.py`, `/tmp/pgbase-audit-http-size-repro.log`, `/tmp/pgbase-audit-schema-size-overlay.json`, `/tmp/pgbase-audit-schema-size-repro.log`.

`tests/README.md`의 과거 대형 Get 계약은 후속 handoff 문서에서 낡았다고 설명되어 있다. 따라서 과거 계약만 근거로 별도의 Get 회귀를 중복 집계하지 않았다. A1은 현재의 정상 HTTP 요청으로 확인했으므로 이 문서 해석에 의존하지 않는다.

**A2 — [P2] 로그아웃 실패 후 미저장 편집 내용에 대한 이탈 경고가 사라진다.**

주요 위치: [로그아웃 처리](https://github.com/webisnoteasy/PgBase/blob/a3065c88e2a82712a41408012b704db3eb9e71f4/ui/admin/src/app.tsx#L40), [draft ledger 해제](https://github.com/webisnoteasy/PgBase/blob/a3065c88e2a82712a41408012b704db3eb9e71f4/ui/admin/src/routing/unsaved-draft.tsx#L19), [dirty 상태 등록](https://github.com/webisnoteasy/PgBase/blob/a3065c88e2a82712a41408012b704db3eb9e71f4/ui/admin/src/routing/unsaved-draft.tsx#L46).

`signOut`은 비동기 `logout()`을 기다리기 전에 `drafts.mayLeave()`를 호출한다. 사용자가 확인하면 ledger가 즉시 비워진다. 로그아웃 요청이 실패하면 편집 화면은 유지되지만 ledger는 복구되지 않는다. `useUnsavedDraft`는 dirty 여부가 계속 `true`이면 다시 등록하지 않는다.

재현 순서는 레코드 편집 → Sign out → 미저장 내용 폐기에 동의 → 로그아웃 요청 실패 → 화면에서 추가 편집 → Collections 이동이다. 첫 확인은 허용하고 두 번째 확인은 거부하도록 설정한 React 테스트에서도 확인 함수는 한 번만 호출되었고 이동이 진행됐다. 따라서 실패 이후 새로 입력한 내용도 경고 없이 사라질 수 있다.

```text
expected confirmation to be called 2 times, but got 1 time
```

수정 방향은 이탈 허용 여부 확인과 실제 draft 해제를 분리하거나, 로그아웃 실패 시 아직 존재하는 dirty 편집 상태를 다시 등록하는 것이다. 단순히 “사용자가 이전에 폐기에 동의했다”는 상태를 유지하면 그 후 새로 입력한 내용까지 보호하지 못한다.

기존 테스트는 미저장 내용 폐기 거부와 깨끗한 화면의 로그아웃 실패를 각각 다루지만 둘을 결합하지 않는다. 실패 후 추가 편집과 `beforeunload` 보호까지 검증해야 한다.

**A3 — [P2] 관계 선택기가 첫 페이지에 없는 유효한 대상을 unavailable로 표시한다.**

주요 위치: [선택 대상 조회](https://github.com/webisnoteasy/PgBase/blob/a3065c88e2a82712a41408012b704db3eb9e71f4/ui/admin/src/relation/relation-picker.ts#L108), [없는 대상으로 분류](https://github.com/webisnoteasy/PgBase/blob/a3065c88e2a82712a41408012b704db3eb9e71f4/ui/admin/src/relation/relation-picker.ts#L119), [배치 크기 가정](https://github.com/webisnoteasy/PgBase/blob/a3065c88e2a82712a41408012b704db3eb9e71f4/ui/admin/src/relation/relation-query.ts#L12).

선택된 관계 ID의 표시 이름을 채울 때 ID 50개씩 `records.list`로 조회한다. 배치가 페이지의 행 수 제한 이내이면 전체 ID가 돌아온다고 가정하지만, 백엔드 페이지는 4 MiB 응답 크기에도 제한된다. 각 레코드가 정상 쓰기 제한 이내여도 여러 레코드를 합친 페이지는 잘릴 수 있다.

hydration 경로는 `page.nextCursor`를 무시하고, 첫 페이지에 없는 ID를 전부 `missing`에 넣는다. 이미 `claimed`에도 넣었으므로 후속 hydration으로 해소되지 않는다. 같은 hook의 검색 경로는 커서를 처리하지만 선택 대상 hydration 경로는 처리하지 않는다.

두 ID를 선택하고 첫 ID와 `nextCursor: "more"`가 있는 응답을 제공하는 React hook 테스트로 재현했다.

```text
later-page selection:
  expected available: true
  received available: false
```

유효한 관계가 실제로 삭제되는 것은 아니지만, 화면은 존재하지 않는 대상으로 잘못 표시한다. 사용자가 이 표시를 근거로 정상 관계를 제거할 수 있다.

수정 방향은 같은 필터의 커서를 제한된 예산 안에서 이어 읽고, 조회가 끝났을 때에만 없는 ID를 확정하는 것이다. 조회 예산이나 오류 때문에 끝까지 확인하지 못한 ID는 UUID 표시를 유지하고 미확인으로 취급해야 한다. 해결을 위해 무제한 개별 조회를 추가할 필요는 없다.

A2·A3의 보존된 재현 자료: `/tmp/pgbase-audit-ui-probes/audit-repro.test.tsx`, `/tmp/pgbase-audit-ui-probes/failing-output.txt`, `/tmp/pgbase-audit-ui-probes/README.md`. 실행 시에만 임시 테스트를 저장소에 복사했으며 실행 후 제거했다.

**A4 — [P3] `schema-index status`가 DB 조회에 실패하면 원인 진단을 출력하지 않는다.**

주요 위치: [status 명령 분기](https://github.com/webisnoteasy/PgBase/blob/a3065c88e2a82712a41408012b704db3eb9e71f4/internal/cli/schema_index.go#L97), [DB 오류 반환](https://github.com/webisnoteasy/PgBase/blob/a3065c88e2a82712a41408012b704db3eb9e71f4/internal/cli/schema_index.go#L131), [명령 실패 진단 계약](https://github.com/webisnoteasy/PgBase/blob/a3065c88e2a82712a41408012b704db3eb9e71f4/internal/cli/database.go#L73).

`activate`, `build`, `rollback` 분기는 실패 시 `operationError`로 동작 이름과 원인을 기록한다. `status` 분기는 `writeSchemaIndexStatus` 오류를 그대로 반환한다. 최상위 `Execute`가 원인을 포함하지 않는 공통 문구만 출력하므로, 연결 이후 상태 조회나 writer 목록 조회에 실패하면 실제 원인을 확인할 수 없다.

PostgreSQL 16.13에서 capability 테이블을 잠그고 statement timeout을 100ms로 설정한 별도 재현의 출력은 다음과 같다.

```text
exit = 1
stdout = ""
stderr = "pgbase: command failed\n"
```

종료 코드는 올바르므로 동작 중단이나 데이터 손상으로 분류하지 않았다. 하지만 rollout 상태를 확인하는 운영 명령에서 타임아웃·DB 오류 등의 진단이 빠진다. status 오류도 다른 분기와 같은 `operationError` 경로로 전달하고 연결 이후 실패를 테스트해야 한다.

재현 자료: `/tmp/pgbase-audit-schema-index-error_test.go`, `/tmp/pgbase-audit-schema-index-overlay.json`, `/tmp/pgbase-audit-schema-index-error.log`.

**실행 검증**

| 검증 | 결과 | 범위·환경 |
|---|---|---|
| Go 단위 테스트 | 통과 | 기존 suite, 최초 로컬 Go 1.27.0 |
| Go 단위 race 검사 | 통과 | 기존 suite, 최초 로컬 Go 1.27.0 |
| PostgreSQL 16.13 통합 + race | 통과 | Go 1.26.7, 전체 `integration` suite |
| PostgreSQL 17.9 통합 + race | 통과 | Go 1.26.7, 전체 `integration` suite |
| PostgreSQL 18.3 통합 + race | 통과 | Go 1.26.7, 전체 `integration` suite |
| Go vet | 통과 | 일반 및 `integration` 빌드 |
| 고정된 golangci-lint | 통과 | 0 issues |
| gofmt·shellcheck | 통과 | 포맷 변경 없음, 저장소 shell scripts 검사 |
| UI Vitest | 통과 | 24파일, 381테스트 |
| UI 타입 검사·빌드 | 통과 | Node 24.19.0, 추적된 dist와 차이 없음 |
| Docker Go E2E | 통과 | 현재 소스 이미지, package 58.461초 |
| 백업·복구 drill | 통과 | 기본 PostgreSQL 16/17/18 matrix, package 13.646초 |
| Playwright | 통과 | Chromium, 8/8, retries 0, 10.0초 |
| 추가 결함 재현 | 해당 동작 확인 | A1 실제 HTTP 및 커널, A2·A3 React 테스트, A4 실제 DB 오류 시 출력 |

통합 suite는 각각 `GOTOOLCHAIN=go1.26.7`과 테스트 전용 DB URL을 지정해 `scripts/test-go-integration.sh -race -count=1`로 실행했다. PostgreSQL 이미지는 CI에 선언된 digest를 사용했다. 이 suite는 단위 테스트도 포함하므로 CI의 Go 버전에서도 기본 테스트가 실행됐다.

Docker E2E는 `go test -tags=e2e ./tests/e2e -count=1`, 복구 테스트는 `go test -tags=restore ./tests/restore -count=1`로 실행했다. Playwright는 `/tmp`에 빌드한 현재 소스 바이너리 2개와 독립된 DB·origin을 사용했고, onboarding 및 authenticated 프로젝트를 모두 실행했다. 브라우저 검증이 registry의 릴리스 후보 이미지에 대한 검증이라는 뜻은 아니다.

기존 테스트 통과와 추가 재현 실패는 모순되지 않는다. 이번 결함은 기본 시나리오의 개별 동작보다 여러 동작을 연결했을 때 드러나는 계약 누락이다.

**개선 관찰 사항 — 확정 결함 수에 포함하지 않음**

- 레코드 삭제 확인은 단일 레코드만 언급하지만, 설정된 incoming cascade에 따라 연결 레코드도 삭제될 수 있다. 관계 설정 UI에서 cascade 의미는 설명하고 있고 삭제 동작 자체는 설계된 기능이다. 별도 영향 범위 안내는 유용하지만, 이를 구현 결함이나 P1로 단정하지 않았다.
- schema-health 전체 실행 예산은 60초이고 일반 PostgreSQL statement timeout의 기본값은 5초다. 한 문장이 5초를 넘으면 전체 예산 이전에 종료되는 것은 재현됐지만, 두 제한을 별도로 두는 설계일 수도 있다. 단일 대형 진단 쿼리에 대한 의도를 명확히 하고 문서·주석과 일치시킬 필요가 있다.
- 커서 만료 시각은 트랜잭션 시작 시각에 기반하므로 락 대기가 남은 수명을 줄인다. 이것이 의도한 스냅샷 시점 의미인지, 발급 시점부터의 수명을 약속하는지 구분해야 한다. 짧은 대기로 수명이 줄었다는 사실만으로 사용자에게 잘못된 커서를 전달하는 결함으로 집계하지 않았다.

**모듈 경계에 대한 판단**

레코드 변경은 `recordops`가 트랜잭션과 revision·audit·outbox를 소유하고, 조회와 관계 연산은 별도 모듈에 둔다. 스키마 변경도 계획·실행·durable receipt를 분리하며, UI는 backend port와 편집 상태를 구분한다. 이번 감사에서 전면 재작성의 근거는 찾지 못했다.

수정의 우선 대상은 이 경계들을 연결하는 계약이다. A1은 스키마 변경이 레코드의 최종 불변조건에 참여하지 않는 문제, A2는 비동기 실패와 로컬 상태 해제가 분리된 문제, A3은 페이지가 있는 조회를 완결된 집합으로 해석하는 문제다. 각 경계를 통과하는 회귀 테스트를 추가하는 것이 단순한 파일 분할이나 테스트 수 증가보다 직접적인 개선이다.

**감사의 한계와 산출물**

주요 production 모듈과 관련 계약을 영역별로 검토했고, 큰 테스트 파일의 모든 assertion을 한 줄씩 검토한 것은 아니다. 지원 DB 버전별 실행은 수행했으나 모든 데이터 규모·동시 실행 순서·운영 환경을 증명하지 않는다. 과거 phase 바이너리 전체를 다시 빌드하는 `releaseartifacts` matrix와 실제 GitHub-hosted release gate는 이번에 재실행하지 않았다.

개별 Sol 보고서는 `/tmp/pgbase-audit-data-sol.md`, `/tmp/pgbase-audit-api-sol.md`, `/tmp/pgbase-audit-ui-delivery-sol.md`에 보존한다. 통합 결과는 이 문서가 기준이며, 에이전트의 가설을 그대로 확정 결함으로 채택하지 않았다. `/tmp` 재현 자료와 로그는 임시 자료이므로 핵심 조건과 관측 결과를 본문에도 기록했다.

감사용 PostgreSQL 컨테이너 3개와 앱 프로세스는 종료·정리했다. 임시 UI 테스트도 제거했으며, 저장소 변경은 이 감사 문서 추가뿐이다.
