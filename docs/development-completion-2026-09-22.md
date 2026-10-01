# 2026-09-22 개발 공백 보완

기준은 사용자가 선택한 로컬 `origin/main` 스냅샷 `6993744a8f36db48ddfeaa8dae017656752d24c7`이다. 이 문서는 `codex/remaining-development`의 로컬 변경과 검증을 기록한다. 이전 설치·병합·공개 릴리스 기록을 갱신한 것으로 해석하지 않는다.

## 구현

| 확인한 공백 | 변경과 보장 |
| --- | --- |
| 온라인 백업의 확장 filesystem 객체 누락 | 같은 PostgreSQL snapshot에서 `extension_objects`도 수집한다. 복사 중 객체가 교체되더라도 snapshot의 불변 버전을 복원하며 GC 차단을 유지한다. local/S3 원본 삭제 후 독립 복원본의 바이트를 검증했다. |
| 관리자 UI의 text ID 지원 누락 | 컬렉션 생성·복제의 ID 정책, 명시적 레코드 ID 입력, 관계 검색·선택·hydration·즉석 생성을 연결했다. 대소문자와 UUID 모양의 text ID를 그대로 보존한다. 실제 ID `new`의 생성 URL 및 임시저장 충돌도 구분했다. |
| 관계 컬렉션의 사용자 접근 설정 차단 | 이미 지원되는 서버의 접근 설정 API에 UI를 연결했다. incoming/outgoing 관계가 있다는 이유만으로 설정을 막지 않는다. |
| builder view 수정 차단 | 기존 declarative view update 계약으로 preview/apply한다. 복합·null 필터는 projection 수정 시 보존하고 명시적으로 교체·제거할 수 있다. |
| TypeScript JSONB 정밀도 손실 경로 | `listJSON/getJSON/createJSON/updateJSON`이 JSON 원문을 송수신한다. 인증, 오류, 취소, ETag/If-Match 및 생성 멱등성 키 처리는 기존 transport를 공유한다. 타입에 text ID 정책과 declarative view update도 반영했다. |

기존 TypeScript parsed API는 유지한다. 원문을 정확하게 해석하려면 호출자가 lossless parser를 사용해야 하며, batch·auth profile·realtime까지 원문 API가 확대된 것은 아니다. 예제와 범위는 [SDK README](../sdk/typescript/README.md)에 있다.

Windows 백업 단위 검사에서 드러난 절대 경로 검증과 테스트 fixture의 열린 archive handle도 수정했다. 새 migration은 없다. 과거에 생성된 archive에 확장 객체 바이트가 빠졌다면 이 수정으로 그 archive가 보완되지는 않으므로 새 백업을 생성해야 한다.

추가 검토에서 드러난 두 경계도 수정했다. PostgreSQL의 정확한 숫자 `1e400`을 브라우저 숫자 입력이 지우지 않도록 text 입력과 lossless parsing을 사용한다. 인증 스키마 문서에는 선택적 `authCollections[].recordId`를 연결하여 standalone 및 paired 인증 컬렉션의 정책을 export/import에서 보존한다. 양쪽 정책 충돌과 기존 정책 변경은 preview/apply에서 거절하고, 생략은 기존 정책을 유지한다.

## 검증

- Go 1.26.8, PostgreSQL 17.9, MinIO의 별도 disposable fixture를 사용했다.
- 새 확장 백업 회귀가 local/S3 모두 `file not found`로 실패하는 것을 확인한 뒤 수정하여 통과했다.
- 전체 `internal/backup` integration/race와 Windows backup unit을 통과했다.
- 관리자 UI 최종 전체 Vitest 46개 파일·590개 테스트(`npm test -- --maxWorkers=4`), typecheck/build를 통과했다. 동시 Go 빌드와 기본 worker 실행에서는 변경하지 않은 storage 테스트 1개가 5초 제한에 걸려 worker 수를 제한해 전체를 재검증했다. 요청 변환 통합 89개, 정확한 numeric 필터 19개, 인증 정책 연결 8개 집중 검사도 통과했다.
- 전체 schema integration/race를 통과했다. 인증 정책 회귀 4개는 paired/standalone export/import, 생략·충돌·불변성, preview 뒤 경쟁 생성 시 전체 rollback을 검증한다.
- TypeScript `npm test`: 빌드, 공개 타입 검사, HTTP/auth/SSE 계약 및 Node 테스트 16개를 통과했다. 원문 JSON 테스트는 실제 로컬 HTTP 서버도 사용한다.
- 최종 전체 Go unit 검사는 `jq`를 갖춘 Linux fixture에서 `CAP_DAC_OVERRIDE`를 제거하여 통과했다. Windows worktree의 Git 경로를 사용하는 `TestRuntimeStructuralBudgetsDoNotDrift`만 Linux 실행에서 제외하고 Windows의 전체 새 파일을 포함한 별도 index로 실행하여 통과했다. `go vet ./internal/backup ./internal/schema`도 통과했다.
- 실제 서버에서 신규 Playwright 4개 흐름을 통과했다: text ID 생성·편집·관계 선택, incoming/outgoing 관계의 접근 설정 저장, builder view projection/filter 수정·재조회, 인증 scaffold 정책과 실제 대문자 ID 사용자 생성·조회·export. 기존 collection 및 scalar/relation record 흐름 2개도 통과했다. 지정 Chromium 1243 다운로드가 시간 초과되어 설치된 Chromium 1234를 별도 로컬 설정으로 사용했으며 저장소의 Playwright 설정은 바꾸지 않았다.

이 기록은 외부 provider 계정, 여러 호스트의 운영 부하, 실제 설치 교체 또는 공개 배포의 검증을 뜻하지 않는다.

## 구조 실측

기존 `tools/repostats` 분류기를 유지하고 별도 `GIT_INDEX_FILE`에 기존 파일과 새 파일을 모두 넣어 측정했다. 실제 작업 index는 변경하지 않았다. 주석·소스를 제외하거나 줄 수를 줄이려고 압축하지 않았으며 다음 작업을 위한 여유분을 추가하지 않았다.

| 항목 | 기준 스냅샷 | 변경 후 |
| --- | ---: | ---: |
| Go runtime 줄 | 92,605 | 92,687 |
| UI runtime 줄 | 10,741 | 10,903 |
| 전체 handwritten runtime 줄 | 103,346 | 103,590 |
| Data-group runtime 줄 | 28,079 | 28,159 |
| Go interface / export / constructor | 96 / 1,575 / 64 | 동일 |
| UI 직접 state/effect 호출 | 180 | 180 |
| UI export | 307 | 312 |
| 800줄 초과 runtime 파일 | 0 | 0 |

Runtime 변경은 +401/−157, 순증가 244줄이다. `collection-designer`의 중복 요청 변환을 기존 state 모듈에 모아 800줄 제한을 유지했다. 새 UI export 5개는 ID 정책 타입·편집기, 공통 designer 명령 변환 및 바이트 제한을 지키는 관계 hydration 두 연산이다. Auth 문서 정책의 정규화·보존·충돌 검사에 schema 내부 80줄이 추가되었다. Go 선언과 page hook 상한은 그대로이며 새 runtime 줄 수와 UI export 상한만 정확한 실측값으로 갱신했다. TypeScript SDK는 기존 분류에서 Go/admin UI runtime 밖이며 SDK 자체 검증을 별도로 수행한다.

## 후속 범위

이 작업은 위 다섯 제품 공백을 닫는다. 실제 설치의 최신 스키마까지 업그레이드·복구 인수, Windows updater 전체 수명주기의 네이티브 실행, 실제 provider·부하·여러 호스트 검증, 공개 릴리스는 후속 작업이다. 이전 유지보수 감사에서 보류한 구조 개선과 정책 결정도 자동으로 완료 처리하지 않는다.
