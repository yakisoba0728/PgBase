# PocketBase 비교 ②: 로컬 구현 계약

> 기록 범위 안내: 이 문서는 해당 단계의 계약·검증 이력을 보존합니다. 현재 전체 상태는 [구현 이력](project-status.md), 남은 작업은 [후속 작업](next-work.md), PocketBase 비교는 [현재 재평가](pocketbase-current-reassessment.md)를 따릅니다. 본문의 당시 미구현/제외 항목은 후속 단계에서 달라졌을 수 있습니다.

작업 기준은 PR #29 (`33816df`), PocketBase 비교 소스는 v0.40.3이다. 이 문서는 P6의 동작 계약이며, 검증·병합 완료 여부는 [TODO](../TODO.md)의 체크 상태를 따른다. 외부 클라우드, 실제 OAuth 애플리케이션, 실제 메일 서비스 계정 검증은 포함하지 않는다.

## 이번 범위와 남아 있는 차이

| ②의 영역 | 연결한 로컬 기능 | PocketBase와의 명시적 차이 |
| --- | --- | --- |
| 파일 | 크기/MIME/개수/필수 정책, 변환, 원자적 multipart Batch | UUID 파일·단기 다운로드 토큰; Required는 최소 1개이며 별도 minSelect는 지원하지 않음 |
| OAuth·메일 | 30개 provider 프리셋·응답 정규화, Apple callback, 아바타, HTML/텍스트 메일 | 검증 이메일이 없는 신규 계정은 자동 생성하지 않음; 실제 외부 앱·SMTP 인증은 별도 |
| SQL View | JOIN/집계/계산·타입 추론·정의 수정·독립 접근 정책 | UUID id 필요, 읽기 전용, 페이지마다 현재 snapshot |
| Batch | 경로 기반 POST/PATCH/DELETE/PUT와 파일의 단일 transaction | PgBase 경로/ETag/세션 사용; PocketBase wire protocol 대체 아님 |
| 로그 | DB 지속성·검색·상세·통계·삭제·보관 상한 | 제한된 비동기 큐; 초과 시 drop 집계 |
| cron | 5필드 calendar·timezone/DST·동적 Go handler | DB lease/fence; 함수는 프로세스 시작 시 재등록 |
| 웹 백업 | 온라인 DB+local/S3 snapshot·아카이브·fresh restore·activation | PostgreSQL major 일치 도구 및 CREATE DATABASE 권한 필요; S3 복원도 독립 local 파일 트리 |
| Go hooks | request/record/mail/file before·after와 등록/해제 | 명시한 6개 lifecycle 종류; PocketBase의 모든 내부 hook 이름/API 호환은 아님 |

이번 범위는 ③의 JSVM, Dart SDK, 이메일 OTP 로그인, impersonation, 임의 SQL 콘솔, 자동 self-update를 구현하지 않는다. 외부 스테이징/운영 배포와 실제 OAuth·메일 서비스 계정 검증도 사용자 요청에 따라 대기한다. PostgreSQL 기반 운영, UUID, 세션·ETag·커서 계약은 PgBase의 설계 차이로 유지한다. 따라서 이 표의 로컬 기능 완료를 PocketBase 전체의 100% 호환으로 해석하지 않는다.

## 필수 파일과 원자적 레코드 생성

필수 파일 필드를 가진 레코드는 파일 없는 JSON create로 먼저 만들 수 없다. 레코드 본문과 파일을 multipart Batch로 함께 보내야 한다. 파일 입력을 지정한 필드는 그 요청의 파일 목록으로 교체한다. 입력을 생략한 파일 필드는 유지한다. 최종 transaction에서 필수 파일이 빠지면 레코드·파일 metadata·outbox 전체를 롤백한다. 관리자 화면의 생성/저장도 같은 경로를 사용하며, 선택한 파일은 새로고침 후 다시 선택해야 한다.

TypeScript SDK의 로그인된 `client` 예시:

```ts
const receipt = await client.batchMultipart([
  {
    method: "POST",
    url: "/api/v1/collections/documents/records",
    headers: { "Idempotency-Key": crypto.randomUUID() },
    body: { title: "Document", attachment: { $file: "document" } },
  },
], { document: { blob: selectedFile, filename: selectedFile.name } });
```

응답이 유실되었을 때는 같은 키와 같은 본문·파일로 재시도한다. 다른 파일로 같은 키를 재사용하면 충돌한다. PUT 재요청은 상태를 수렴시키지만 일반 update처럼 새 revision/ETag/outbox를 만든다.

## 웹 백업과 복원

`PGBASE_BACKUPS_DIRECTORY`를 전용 로컬 디렉터리로 지정하면 CLI `serve`가 `pgbase.RunManaged`를 사용한다. 관리자 Operations 화면에서 백업 생성, 목록, 다운로드, 업로드, 삭제, 복원을 실행한다. 변경 요청에는 기존 관리자 세션과 CSRF가 적용된다. 백업 작업은 프로세스마다 하나씩 실행되며, 작업 영수증은 DB 밖의 전용 디렉터리에 남는다.

온라인 백업은 PostgreSQL repeatable-read 트랜잭션에서 `pg_export_snapshot()`을 호출하고 `pg_dump --snapshot`으로 같은 시점의 DB를 저장한다. 파일 목록도 같은 트랜잭션에서 읽는다. 파일 ID별 객체는 불변이며, 원본·기본 썸네일·추가 변환 모두 목록에 포함된다. 복사하는 동안 PostgreSQL advisory lock으로 물리 파일 GC를 막고, 별도 공유 스키마 잠금으로 DDL을 막는다. 레코드 쓰기와 논리 삭제는 계속 가능하다. 참조된 파일을 읽지 못하면 백업을 게시하지 않는다.

로컬 파일과 S3/MinIO 객체 모두 같은 아카이브 형식으로 내려받는다. 업로드는 gzip/tar 2 GiB, 풀린 데이터 8 GiB, 파일 100,000개로 제한한다. 경로 이동·링크·중복 엔트리·알 수 없는 최상위 파일·손상된 gzip·manifest/파일 해시 불일치를 거부한다. 백업에는 인증 DB도 들어 있으므로 아카이브와 백업 디렉터리는 관리자 데이터로 취급한다. 배포용 환경 변수나 서버의 비밀 설정 파일은 아카이브에 포함하지 않는다.

복원은 같은 PostgreSQL 서버에 고유 이름의 **새 DB**를 만들고 새로운 로컬 파일 트리에 복원한다. S3에서 생성한 백업도 이 독립 로컬 파일 트리로 복원된다. DB 계정에는 새 DB를 만들 권한이 필요하다. `pg_dump`/`pg_restore`와 서버/백업의 PostgreSQL major 버전은 일치해야 한다. PgBase의 마이그레이션·카탈로그 검사와 앱 구성을 통과한 다음 요청을 배출하고 실행 대상을 전환한다. 오래 유지되는 요청 때문에 제한 시간 안에 배출하지 못하면 기존 실행 대상을 유지하고 실패한다.

활성화 전에 실패한 새 DB와 파일 트리는 정리한다. 영수증 게시 결과가 불확실하면 두 대상을 보존하여 재시작이 삭제된 대상을 참조하지 않도록 한다.

전환 결과는 0600 권한의 `active-target.json`에 저장한다. 다음 시작에서도 이 대상을 사용한다. 원래 DB와 파일은 자동 삭제하지 않는다. 전환 후 복원 시점 이후에 만들어진 로그인 세션은 사라질 수 있으므로 화면은 재로그인을 안내한다. 중단된 작업은 재시작 시 실패 영수증으로 바뀌며, 완료 여부를 추정하여 재실행하지 않는다. 기존 대상을 다시 사용하려면 프로세스를 정지하고 activation receipt를 별도 보관한 뒤 원래 환경 설정으로 시작한다.

임베더는 `RunManaged`를 사용하거나 `BackupOptions.Activate`로 자신의 실행 전환 코드를 제공한다. `Activate`가 오류를 반환할 때는 새 대상이 활성화되지 않았음을 보장해야 한다. 외부 전환 결과를 확인할 수 없는 콜백은 `ErrActivationOutcomeUnknown`을 반환하여 새 대상을 보존한다. 일반 `New`/`Run`에서 웹 생성·다운로드는 가능하지만, 활성화 콜백이 없으면 복원을 거부한다.

## SQL View 읽기

SQL View는 `view.sql`로 정의한다. PostgreSQL이 결과 컬럼의 타입을 추론하고, preview/apply 양쪽에서 쿼리·의존성·식별자 계약을 검사한다. 사용자 SQL은 등록된 컬렉션을 참조하는 SELECT여야 하며, 출력에는 안정적인 비어 있지 않은 UUID `id`가 필요하다. JOIN, 집계, GROUP BY, 계산식을 사용할 수 있다. DB에 임의 명령을 실행하는 SQL 콘솔과는 다른 기능이다.

View의 레코드는 읽기 전용이다. 일반 SQL View는 자체 접근 설정과 규칙을 갖는다. 관리자에게만 보이다가 명시적으로 사용자 접근과 list/view 규칙을 설정하면 해당 결과가 노출된다. 관리자는 JOIN/집계 결과에 어떤 원본 데이터가 포함되는지 고려하여 이 규칙을 정한다. 단일 source/projection으로 만드는 기존 선언형 View는 기존의 source 접근 정책을 유지한다.

SQL View 행에는 생성 ordinal이나 가짜 변경 revision을 만들지 않는다. 읽기 ETag는 결과 내용으로 계산한다. 커서는 컬렉션·카탈로그 버전·필터·정렬·인증 주체·규칙 변경에 묶인다. 각 페이지는 현재 DB 스냅샷을 읽으므로 페이지 사이 원본 변경에 따라 새로운 결과가 나타나거나 기존 결과가 사라질 수 있다. 이는 기본 레코드의 생성 membership 제한과 구분된다. 원본 변경으로 `id`가 중복되거나 비어 버린 경우에는 읽기를 실패시켜 임의 행을 반환하거나 일부를 건너뛰지 않는다.

## Go lifecycle hooks

`Options.Hooks`에 `*pgbase.HookRegistry`를 전달한다. zero value registry를 사용할 수 있으며 `On(kind, Hook)`은 멱등적인 등록 해제 함수를 반환한다. 낮은 `Priority`부터 실행하고 같은 우선순위는 등록 순서를 따른다. 실행 중인 작업은 시작 시점의 hook 목록을 유지한다. 등록과 해제는 동시 호출할 수 있다.

| 종류 | Data | 실행 의미 |
| --- | --- | --- |
| `HookRequest` | `*RequestEvent` | 라우팅 전 요청/응답 writer를 연결하고 요청 완료 후 관찰한다. After에서 응답을 쓰지 않는다. |
| `HookRecordBefore` | `Mutation` | 트랜잭션 내부 검증이다. 실패 시 쓰기를 롤백하며 재시도 시 다시 실행될 수 있다. |
| `HookRecordAfter` | `Mutation` | 커밋 성공 후 관찰한다. 이미 완료된 DB 작업을 되돌리지 않는다. |
| `HookMailSend` | `*MailMessage` | 환경 SMTP와 컬렉션 SMTP 모두 전송 전에 본문/제목을 조정하거나 거부할 수 있다. |
| `HookFileUpload` | `*FileEvent` | 저장 전에 검증하고, standalone 또는 Batch의 결과가 확정된 후 완료를 관찰한다. |
| `HookFileDelete` | `*FileEvent` | 삭제 전 검증과 결과 관찰. |

Before의 오류는 작업에 전달한다. After는 결과를 관찰하는 콜백으로 오류를 반환하지 않으며, panic은 다른 관찰자를 막지 않는다. After에는 최대 5초의 독립 컨텍스트를 주며 콜백이 이를 준수해야 한다. 트랜잭션 내 콜백에서 외부 전송 같은 되돌릴 수 없는 작업을 수행하면 안 된다. 커밋 이후 hook은 영구 전달 큐가 아니므로 확실한 재전달에는 기존 outbox를 사용한다. 기존 `MutationHooks`도 유지된다. 예제는 [examples/lifecycle](../examples/lifecycle/main.go)에 있다.

## 로그와 예약 작업

요청 로그는 PostgreSQL에 저장하고 기본 7일/100,000행으로 제한한다. 큐는 기본 4,096건으로 제한하고 초과·쓰기 실패 수를 관찰할 수 있다. 메서드, 등록된 route template, 상태, 시간, 소요 시간만 저장하며 헤더·본문·원본 URL·query string은 저장하지 않는다. flush는 호출 시 FIFO 범위까지 처리하여 지속적인 새 요청 때문에 종료 확인이 무한히 지연되지 않게 한다.

예약 작업은 기존 `@every`와 5필드 cron, IANA timezone을 지원한다. 달력 계산은 DST를 반영한다. `App.RegisterJob`/`RemoveJob`으로 실행 중 handler를 등록·제거할 수 있다. Go 함수는 DB에 직렬화할 수 없으므로 프로세스 시작 시 다시 등록해야 한다. DB에 저장된 관리자의 스케줄 수정은 유지하고 등록되지 않은 작업은 화면에 표시한다. 기존 lease/fence와 재시도 계약을 유지한다.

## 측정된 구조 예산

사용자가 승인한 ② 신규 범위를 PR #29 `33816df`와 비교했다. 새 파일을 stage한 뒤 `GOTOOLCHAIN=go1.26.8 go run ./tools/repostats --compare 33816df --pretty`로 재현한다. 런타임 변경은 +5,861/−551줄, 순증 5,310줄이다. 기계적 Go runtime 집계에는 비교 카테고리에서 support로 분류되는 migration embedding 20줄도 포함된다.

| 측정 | PR #29 | P6 상한 |
| --- | ---: | ---: |
| Go runtime | 43,538 | 48,643 |
| 관리자 UI runtime | 4,956 | 5,181 |
| 합계 | 48,494 | 53,824 |
| 데이터 패키지 합계 | 19,382 | 20,175 |
| query 패키지 | 5,466 | 5,543 |
| Go interface | 37 | 43 |
| Go 공개 선언 | 698 | 803 |
| Go constructor | 37 | 39 |
| UI page state/effect | 76 | 90 |
| UI export | 169 | 176 |
| 800줄 초과 production 파일 | 0 | 0 |

새 경계는 백업 생성/저장/복원 관리자, 요청 로그 저장, lifecycle registry, SQL View 검사와 파일 staging transaction 참여자다. 기존 App 구성은 별도 파일로 추출했고, 파일당 800줄 제한은 그대로 지킨다. 이 값은 실제 측정과 같은 여유 없는 상한이며, 기존 구현 압축이나 테스트 제거로 성장분을 감추지 않는다. HTTP route 상한은 avatar·로그 상세/통계/삭제·백업·Apple callback을 포함한 실제 registry 구성으로 재측정하여 113→122로 갱신한다. Method-agnostic 경로 상한 3은 유지한다. Doctor는 SQL View 열의 존재를 읽는 고정 probe 하나가 추가되어 36→37이며 컬렉션 개수와 독립적이다.
