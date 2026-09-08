# 실행 파일·확장·개발 워크플로 대조

기준은 PocketBase `v0.40.3` / `5684ee24f1e88f71a2ede73d20cf547fbd509e16`, PgBase `35af64c3e4736af0a8a6d2d3ecb36640e035840a`다. 이 보고서는 전체 감사 중 operations 영역 결과이며 다른 영역 완료를 뜻하지 않는다.

## 실제 읽은 범위

[operations-ledger.json](operations-ledger.json)에 107개를 기록했다. 82개는 구현·테스트·문서·텍스트 fixture 전체 본문을 읽었다. 22개는 생성된 타입 정의, lockfile, PNG, SQLite fixture의 생성 경로·메타데이터를 확인했다. 과거 changelog 3개는 버전 제목을 확인한 참고 문서다. 생성된 24,820줄 타입 정의와 과거 changelog 전체를 읽었다고 계산하지 않는다. upstream 코드를 빌드하거나 테스트를 실행한 결과는 아니다.

## 확인한 차이

| 기능 | PocketBase 근거 | 현재 PgBase 대응 | 판정 |
| --- | --- | --- | --- |
| JS 확장 | [기본 실행 파일 등록](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/examples/base/main.go#L89), `plugins/jsvm/binds.go`의 hook·route·DBx·mail·filesystem·HTTP 바인딩 | [범위 명세](../extensions-and-client-scope.md), `internal/recordops/hooks.go`의 Go mutation before/after | JS 미구현, Go 확장은 일부 제공 |
| 라우터·미들웨어 확장 | [JS routerAdd/routerUse](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/plugins/jsvm/binds.go#L150), Go ServeEvent | `internal/api/router.go`의 내부 고정 route registry, `pgbase.Options`에는 사용자 router 등록점 없음 | 동등한 공개 확장 API 없음 |
| 사용자 마이그레이션 작성 | [migrate plugin](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/plugins/migratecmd/migratecmd.go#L106): up/down/create/collections/history-sync, JS/Go snapshot/diff | `internal/cli/run.go`: 시스템 `migrate status/up --through`, 스키마 plan/apply 및 operation receipts | 서로 다른 범위; 사용자 up/down 파일 생성 미구현 |
| 관리자 스키마 변경 자동 기록 | [automigrate](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/plugins/migratecmd/automigrate.go#L18): 필드 ID 기반 추가/삭제/수정, 역방향 템플릿, provider secret 제외 | PgBase의 DB operation history는 적용 상태·복구 추적용 | 이식 가능한 마이그레이션 소스 자동 생성 없음 |
| 스케줄러 | [cron 바인딩](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/plugins/jsvm/binds.go#L105), 런타임 add/remove | `pgbase/jobs_options.go`, [local jobs](../local-jobs.md): Go handler와 1초–24시간 `@every`, PostgreSQL lease/fence | 일부 제공, 일정 문법·등록 방식·지속성 다름 |
| 앱 정적 사이트 제공 | [pb_public와 SPA fallback](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/examples/base/main.go#L111) | `internal/api/router.go`: `/admin` 내장 UI, 그 외 미등록 경로 404 | 사용자 public 디렉터리 제공 미구현 |
| 자체 업데이트 | [update 명령](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/plugins/ghupdate/ghupdate.go#L120): 플랫폼 asset, 실행 파일 교체·실패 복원 | `internal/cli/run.go`의 명령 목록에 update 없음 | 미구현; 수동 배포와 별개 |
| 관리자 CLI | [superuser 명령](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/cmd/superuser.go#L18): create/update/upsert/delete/otp/ips | `internal/cli/run.go`: admin create/reset-password | 일부 제공 |
| 백업 운영 | PocketBase 기본 실행 파일의 updater가 core 백업 기능 사용; 세부 백업 API는 API/core 보고서 참조 | [backup/restore](../backup-restore.md): PostgreSQL dump/PITR와 별도 object bytes, 복원 drill | 동등한 단일 data-directory 제품 경험 아님 |

## 구현보다 계약을 먼저 정해야 하는 부분

- PgBase의 mutation hook은 안정적인 이벤트 ID, 트랜잭션 전 검사와 commit 후 best-effort 관찰이다. PocketBase의 여러 모델·요청·메일·파일 수명주기 hook과 동일하다고 설명하면 안 된다.
- PocketBase JS warm VM pool은 설정 크기를 넘으면 임시 VM을 만든다. pool 크기를 실행 동시성 한도라고 해석하면 잘못이다. `pool.go`와 App reset 테스트까지 읽어 확인했다.
- PocketBase JS 바인딩은 Go 동작을 노출하며 Promise 오류 처리에 별도 한계가 있다. JS 파일을 읽어 실행하는 기능 하나만 추가해서 호환 완성이라고 볼 수 없다.
- PgBase 시스템 마이그레이션의 체크섬·정확한 목표 버전 receipt와 사용자 앱 스키마를 재현하는 migration authoring은 별도 기능이다.
- PostgreSQL 외부 DB와 SQLite 내장 DB의 운영 차이는 미구현 항목과 분리해야 한다. PgBase의 lease/fence·doctor·복원 검증을 PocketBase와 다르다는 이유만으로 결함 처리하지 않는다.

## 후속 작업 후보

1. 사용자 schema snapshot과 버전 관리 가능한 up/down migration 계약 설계.
2. 지원하려는 Go 공개 hook/event 목록과 custom route API 확정.
3. JS 호환이 실제 목표라면 모듈 로딩·바인딩·타입 생성·오류 전파까지 별도 milestone으로 추적.
4. 앱 정적 파일 제공과 관리자 CLI 누락 명령의 필요성 결정.
5. 백업 UI/API와 PostgreSQL 운영 도구 연동 범위 정의.

위 항목은 기능 대조 결과다. 재현하지 않은 upstream 결함이나 새 구현 완료를 주장하지 않는다.
