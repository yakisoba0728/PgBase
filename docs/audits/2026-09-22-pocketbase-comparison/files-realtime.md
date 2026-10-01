# PocketBase v0.40.4 직접 대조: files / realtime

2026-09-22. 공식 PocketBase checkout `5cec579da984436a258602a46a96302fbd31f77c`와 PgBase `codex/remaining-development`의 현재 작업 트리를 비교했다. PgBase 미커밋 수정도 포함한다. 기존 files 13행·realtime 7행을 모두 다시 판정하고 신규 12행을 추가했다. 기계 판독 결과는 [files-realtime.json](files-realtime.json)이다.

32행 중 equivalent 2, adapted 15, partial 13, missing 2다. 중첩되는 기능 안의 세부 계약을 분리한 분류이므로 구현률 백분율이 아니다.

이번에는 source/test를 읽었으며 실행하지 않았다. `test-exists`는 저장소의 관련 테스트 존재를 뜻하고 이번 성공을 뜻하지 않는다. 특히 application topic 유지, multiplex 연결 수명·재연결·전역 cursor 순서는 **static-only**로, 실행 재현하지 않은 코드 기반 결론이다. Production source와 테스트를 수정하지 않았고 Docker를 재시작하지 않았다. research skill의 1차 자료 접근을 사용했다. 하위 연구 위임은 agent limit에 막혀 순차 조사했다.

## 주요 차이

1. **Custom topic 연결 유지 — RT-03, P1, static-only.** subscribe는 application=true를 인식하지만 delivery loop는 이를 건너뛰지 않고 모든 구독을 collection reader에 전달한다. application 구독은 collection이 빈 문자열이므로 실제 reader가 거부하고 PB_ERROR 뒤 연결을 종료하는 경로가 있다. 기존 HTTP 테스트는 stub으로 연결 직후 한 메시지만 받는다. [internal/api/realtime_sessions.go:255](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:255), [internal/realtime/service.go:94](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service.go:94), [internal/api/realtime_sessions_test.go:53](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions_test.go:53). PocketBase는 [generic client channel](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/subscriptions/client.go#L176)을 사용한다.

2. **Multiplex timeout/reconnect — RT-07, P1, static-only.** Production WriteTimeout은 30초인데 multiplex 경로가 해제·연장하지 않는다. PB_CONNECT의 SSE id는 UUID지만 Last-Event-ID parser는 숫자만 받으므로 첫 record event 이전의 일반 SSE 재연결이 거부될 수 있다. [internal/api/realtime_sessions.go:220](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:220), [internal/api/realtime.go:190](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime.go:190), [pgbase/app_construct.go:409](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app_construct.go:409), [pgbase/managed.go:161](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/managed.go:161). PocketBase는 [write deadline 해제와 5분 idle/30분 max timer](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime.go#L43)를 명시한다. Legacy stream의 30분 context/deadline 갱신을 multiplex 지원 근거로 사용하지 않았다.

3. **Multiplex 전역 replay cursor — RT-10, P1, static-only.** DB outbox의 delivery sequence, stale lease 방어, per-record 순서는 구현됐지만 multiplex는 topic별 batch를 순차 전송한다. A의 seq20을 보낸 뒤 B의 seq10을 보내기 전에 끊기면 shared cursor20으로 재연결 시 10을 건너뛸 수 있다. 늦게 출력한 작은 ID는 이미 전달한 큰 ID의 중복 replay도 유발할 수 있다. [internal/realtime/outbox.go:99](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/outbox.go:99), [internal/api/realtime_sessions.go:255](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:255), [internal/realtime/service_integration_test.go:136](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service_integration_test.go:136). PB 자체는 [client ID만 사용](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/subscriptions/message.go#L20)하므로 이는 PgBase가 추가한 replay 기능의 문제다.

4. **권한·삭제 이벤트 — RT-05/08/09/13.** PB는 [record topic에 viewRule, collection topic에 listRule](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime.go#L606)을 사용하고 [삭제 전에 접근 확인과 enrichment](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime.go#L465)를 준비한다. PgBase는 모든 topic에 list AND view를 요구하며 relation-filtered delete를 건너뛰고 delete expand를 하지 않는다. PB options.headers도 표현할 수 없다. Administrator payload도 hidden field가 제거된다. [internal/realtime/service.go:243](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service.go:243), [internal/api/realtime_sessions.go:279](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:279), [internal/api/realtime_sessions.go:509](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:509), [internal/realtime/service.go:348](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service.go:348).

5. **JS filesystem facade — FIL-13/17/18.** `$filesystem.s3(...)`는 모든 인자를 무시하고 현재 configured store를 반환한다. System.list는 첫 100개만 반환하고 setContext는 no-op이다. Go/top-level paginated API와 DeletePrefix host 구현은 pagination을 처리한다. [internal/jsvm/pocketbase_filesystem_globals.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_filesystem_globals.go:12), [internal/jsvm/pocketbase_filesystem_globals.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_filesystem_globals.go:15), [pgbase/extension_objects.go:499](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_objects.go:499). PB [NewS3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L66), [List](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L233), [DeletePrefix](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L425)와 비교했다. Configured S3 backend 자체가 없다는 의미는 아니다.

6. **File HTTP/transform — FIL-14/15/16.** PB [ServeContent 기반 download](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L544)는 Range/conditional, media inline, download=true를 지원한다. PgBase record file 경로는 전체 io.Copy와 attachment 응답이다. PB [EXIF auto-orientation](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L643), [기본 100x100와 원본 fallback](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file.go#L164)도 같지 않다. [internal/api/file_handlers.go:84](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:84), [internal/api/file_handlers.go:211](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:211), [internal/filestore/variants.go:140](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants.go:140). Extension System.serve를 record-file HTTP의 근거로 혼동하지 않았다.

7. **Standalone multipart publication — FIL-19, P2, static-only.** 첫 파일 publish 후 추가 part를 검사하고 보상 Delete 오류를 무시한다. Delete hook veto/권한 변경 시 오류 응답에도 attachment가 남을 수 있다. [internal/api/file_handlers.go:192](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:192), [internal/filestore/service.go:447](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:447). 별도 multipart batch의 staging/동일 DB transaction 원자성과는 다른 경로다.

## 구현된 계약과 의도적인 차이

Multipart batch, maxSelect/required, field modifiers, hash/size/MIME/image budget, private/public 필드, principal/file/variant token, PNG/JPEG/GIF/WebP transform, cache와 advisory lock, local/S3 storage/runtime 설정이 존재한다. `field+`, `+field`, `field-` 미지원이라는 예전 설명은 현재와 맞지 않는다. [pgbase/batch_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/batch_integration_test.go:17), [internal/api/batch_file_modifiers.go:45](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/batch_file_modifiers.go:45), [internal/filestore/staging.go:170](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/staging.go:170), [internal/filestore/variants.go:33](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants.go:33), [internal/filestore/storage_settings.go:284](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/storage_settings.go:284).

PB protected 파일은 [token으로 얻은 auth로 view rule을 평가](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file.go#L108)한다. Invalid token은 nil auth이므로 public view rule이면 허용될 수 있다. PgBase는 protected가 기본값이며 private HTTP URL에 현재 인증과 token을 모두 요구한다. Token 하나로 private image URL을 공유할 수 있다는 설명은 PgBase에 적용하지 않았다. [internal/filestore/service.go:380](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:380), [internal/api/file_handlers.go:211](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:211).

PB는 [auth loss 시 socket을 유지하고 auth만 해제](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime.go#L251)한다. PgBase는 재확인 때 연결을 종료한다. 이는 adapted로 분류했다. Custom broker는 enqueue 전에 인증하지만 custom-message wire write 직전에는 다시 검사하지 않는다. [enqueue 전 철회 테스트](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions_test.go:160)가 더 강한 즉시 철회 보장을 증명하지 않는다.

Physical file deletion은 metadata 변경과 durable GC로 분리된다. Required 마지막 파일 삭제는 거부한다. Generic DeletePrefix는 전체 prefix를 페이지로 수집하고 순차 삭제하다 첫 오류에서 종료하므로 PB의 errors slice 반환과 다르다. [internal/filestore/service.go:478](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:478), [internal/filestore/staging.go:210](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/staging.go:210), [pgbase/extension_objects.go:499](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_objects.go:499). Local rename 시험은 원자적 publish를 검사하며 power-loss durability 시험은 아니다.

## 이전 실행 증거

Extension object online backup 누락은 현재 작업 트리의 앞선 수정으로 해결되어 있다. 해당 작업에서 PostgreSQL 17.9/MinIO local·S3 red → green 및 `go test -race -tags=integration ./internal/backup -count=1` 통과(4.133초)를 확인했고 native backup tests도 통과했다. 이는 **prior-run**이며 이번 비교에서 실행한 결과가 아니다. [internal/backup/extension_restore_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/extension_restore_integration_test.go:1), [internal/backup/online.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/online.go:1). Historical backup 행은 operations 보고서에 있으므로 중복 추가하지 않았다. 새 수정이 과거 불완전 archive를 소급 복구하지는 않는다.

## 읽은 1차 자료

PocketBase: `apis/file.go`, `file_test.go`, `record_crud.go` multipart 추출; `core/field_file.go`, `field_file_test.go`, `validators/file.go`, `record_tokens.go`, `base.go` NewFilesystem; `tools/filesystem/filesystem.go`와 해당 tests; `apis/realtime.go`, `realtime_test.go`; `tools/subscriptions/client.go`, `client_test.go`, `message.go`.

PgBase: `internal/api/{file_handlers,batch_handlers,batch_file_modifiers,realtime,realtime_sessions,realtime_broker,deadline}.go` 및 관련 tests; `internal/filestore/{service,staging,public,image,variants,local,s3,storage_settings,storage_gate}.go` 및 unit/integration tests; `internal/realtime/{service,outbox}.go`와 snapshot/retry/service tests; `internal/jsvm/pocketbase_filesystem_globals.go`; `pgbase/{file_services,file_options,extension_objects,app_construct,managed}.go`; `pgbase/{file,batch,realtime,storage_settings_http,extension_objects,javascript_filesystem_stream,javascript_filesystem_serve}_integration_test.go`.

위 목록은 전체를 읽은 파일과 소유 함수·대표 테스트 구간을 읽은 파일을 포함한다. JSON 각 행에 구체적인 source/test anchor를 남겼다. Test 이름만으로 feature 전부가 검증됐다고 판정하지 않았다.

## 행별 판정

| ID | Feature | Status | Verification | Priority |
|---|---|---|---|---|
| FIL-01 | record multipart의 여러 attachment part | adapted | test-exists | none |
| FIL-02 | file field 최대 선택 수/cardinality | equivalent | test-exists | none |
| FIL-03 | 파일 크기와 MIME 정책 | adapted | test-exists | none |
| FIL-04 | record 변경과 파일 attach/remove | adapted | test-exists | none |
| FIL-05 | 보호 파일 field | adapted | test-exists | none |
| FIL-06 | 원본 download URL·filename 응답 | adapted | test-exists | none |
| FIL-07 | 단기 download token | adapted | test-exists | none |
| FIL-08 | thumb WxH/t/b/f 문법 | adapted | test-exists | none |
| FIL-09 | 이미지 transform format 범위 | adapted | test-exists | none |
| FIL-10 | thumbnail cache/concurrent generation | adapted | test-exists | none |
| FIL-11 | 로컬 object store | adapted | test-exists | none |
| FIL-12 | S3 backend와 runtime 설정 | adapted | test-exists | none |
| FIL-13 | extension용 generic filesystem facade | partial | test-exists | P1 |
| RT-01 | SSE connection endpoint/headers | equivalent | test-exists | none |
| RT-02 | subscribe/unsubscribe API | adapted | test-exists | none |
| RT-03 | arbitrary subscription topics | partial | static-only | P1 |
| RT-04 | guest/user/superuser/service SSE availability | adapted | test-exists | none |
| RT-05 | subscription filter/expand와 access 재판정 | partial | test-exists | P1 |
| RT-06 | event envelope | adapted | test-exists | none |
| RT-07 | connection lifetime과 reconnect recovery | partial | static-only | P1 |
| FIL-14 | 추가: record file Range·conditional·inline/download 응답 | partial | static-only | P2 |
| FIL-15 | 추가: EXIF orientation을 반영하는 thumbnail | missing | static-only | P2 |
| FIL-16 | 추가: default 100x100 thumb와 original fallback | partial | static-only | P2 |
| FIL-17 | 추가: $filesystem.s3 인자로 독립 storage 선택 | missing | static-only | P1 |
| FIL-18 | 추가: System.list 전체 prefix inventory | partial | static-only | P2 |
| FIL-19 | 추가: 잘못된 standalone multipart upload의 publication 원자성 | partial | static-only | P2 |
| RT-08 | 추가: record topic은 viewRule, collection topic은 listRule | partial | static-only | P1 |
| RT-09 | 추가: subscription options의 query·headers request context | partial | static-only | P2 |
| RT-10 | 추가: durable replay와 multiplex 전역 cursor 순서 | partial | static-only | P1 |
| RT-11 | 추가: 인증 해제 후 연결·재인증 semantics | adapted | test-exists | none |
| RT-12 | 추가: collection ID alias로 realtime 구독 | partial | static-only | P2 |
| RT-13 | 추가: superuser realtime hidden-field enrichment | partial | static-only | P2 |
