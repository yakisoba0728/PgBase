# Archive, cron and database helpers

`tools/archive/**`, `tools/cron/**`, `tools/dbutils/**`의 구현과 테스트 16개 전체 본문을 판독했다. [tools-extra-ledger.json](tools-extra-ledger.json)이 해당 경로의 tools ledger보다 우선한다. 실행 검증 결과가 아닌 소스 계약 비교다.

| 영역 | PocketBase v0.40.3 | PgBase baseline 35af64c | 의미 |
| --- | --- | --- | --- |
| 예약 작업 | 5개 calendar 필드, wildcard/list/range/step, hourly/daily/weekly/monthly/yearly 매크로. 기본 UTC·1분 경계에 맞춘 tick이며 작업을 별도 goroutine으로 실행한다. 같은 job ID를 다시 추가하면 교체한다. | 공개 JobSpec은 `@every` 1초–24시간 간격. PostgreSQL 시간·lease·fence를 사용하며 다중 인스턴스 실행 소유권을 관리한다. | PB는 달력 표현과 runtime job 관리가 넓고, PgBase는 DB 기반 실행 조정이 다르다. 분산 lease가 없는 것을 단일 프로세스 PB의 결함이라고 단정하지 않는다. |
| 날짜 의미론 | IsDue는 minute/hour/day/month/weekday를 모두 만족해야 한다. day-of-month와 weekday는 AND다. tick보다 긴 작업의 중첩 실행 방지나 실행 완료 대기를 Cron 자체가 제공하지 않는다. | 간격 스케줄이므로 PB calendar 식을 그대로 붙여넣을 수 없다. | 달력 예약 추가 시 시간대·DST·중첩·미실행 보충 정책을 명시해야 한다. |
| ZIP | 상대 경로 제외 목록, BestSpeed 압축, 생성 실패 시 ZIP 정리. 추출은 대상 경로 prefix를 확인하고 일반 파일·디렉터리만 복원하며 symlink 등은 건너뛴다. | PostgreSQL dump/restore와 별도 파일 객체 복원 절차를 제공한다. | PB 디렉터리 ZIP API를 그대로 복제해서 PG 일관성 백업이 된다고 볼 수 없다. |
| DB helper | SQLite CREATE INDEX 문자열의 이름·컬럼/표현식·collation·sort·WHERE 파싱/재구성, 느슨한 단일 unique index 검색. scalar/array/invalid-JSON을 보정하는 SQLite JSON 식을 생성한다. | Typed schema index 계획과 PostgreSQL query codec/compiler를 사용한다. | SQL 문법과 JSON null/배열 의미론을 포함한 차이이며 함수 단위 이식 대상이 아니다. |

근거: [PB cron](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/tools/cron/cron.go#L41), [calendar due](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/tools/cron/schedule.go#L44), [ZIP create](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/tools/archive/create.go#L19), [ZIP extract](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/tools/archive/extract.go#L18), [index parser](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/tools/dbutils/index.go#L135), [PgBase jobs](../../pgbase/jobs_options.go), [local jobs contract](../local-jobs.md), [backup contract](../backup-restore.md), [schema planner](../../internal/schema/planner.go), [codec](../../internal/query/codec.go).
