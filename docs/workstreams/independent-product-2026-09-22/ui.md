# 관리자 규칙·테마 UI의 native 계약 보완

이 작업은 PgBase 자체 서버와 화면의 계약 오류를 고친다. PocketBase 화면 복제나 기존 비교표의 모든 missing 항목 구현이 목적이 아니다. 2026-09-22 감사 산출물은 당시 관찰로 유지하며 수정하지 않았다. 기존 text ID, builder view, relation access 변경도 유지했다.

## 변경

- `ui/admin/src/backend/http-admin-ports.ts:512`: 규칙 PUT은 native 서버의 strict DTO에 맞춰 `{rules}`만 보낸다. UI port의 사용하지 않는 revision 인자와 화면 상태를 제거했다. 서버의 규칙 저장에 CAS를 새로 도입하거나 CAS가 있는 것처럼 표현하지 않는다.
- `ui/admin/src/pages/record-rules-editor.tsx:55`: preview는 현재 JSON에서 선택한 action의 AST 하나만 `rule`로 보낸다. 선택 action이 객체가 아니면 요청하지 않는다.
- 같은 editor에서 preset 변경은 현재 JSON draft를 기준으로 적용하고 Reset은 마지막 load/save 결과를 복원한다. 다른 action의 고급 편집을 버리지 않으며 select에도 현재 draft를 표시한다.
- 기존 `parseJSON`/`stringifyJSON`을 재사용해 규칙의 정확한 numeric 값을 load, preset 변경, preview, save에서 보존한다. 예전 `JSON.stringify`는 `LosslessNumber`를 객체로 표시했고 `JSON.parse`는 큰 정수를 반올림했다.
- 규칙 draft·action·사용자·레코드 ID가 달라지면 이전 preview 판정을 지운다. 오래된 allowed 표시를 현재 입력의 판정으로 남기지 않는다.
- `ui/admin/src/styles.css:5`: `prefers-color-scheme` media rule은 `data-theme="system"`에만 적용한다. OS 변경을 브라우저가 즉시 반영하며 명시적 light/dark 설정을 덮어쓰지 않는다. 불필요한 JS media listener/state는 추가하지 않았다.
- `ui/admin/src/pages/settings-page.tsx:50`: native `backups.keep` 설정에 0..10000 정수 입력을 연결했다. 빈 입력을 0으로 바꾸지 않으며 0은 무제한 보관임을 안내한다. 보관 개수는 manual/uploaded/scheduled 전체 archive를 센다. Backup storage 화면의 retention은 repository fallback이며, 관리형 앱에서는 자동 백업을 꺼도 Settings > Backups to keep이 우선한다고 명시했다.

## 실패를 확인한 경계

`ui/admin/src/pages/record-rules-http.test.tsx`는 memoryBackend 대신 실제 `httpBackend`, session provider와 화면을 함께 사용하여 최종 HTTP body/CSRF/cookie 옵션을 검사한다. fetch는 외부 HTTP 경계에서만 대체했다. native API의 DTO와 독립적인 기대 payload를 사용한다. 실제 Go 서버 연결은 아래 browser 회귀가 담당한다.

| 회귀 | 수정 전 관찰 | 수정 후 집중 검사 |
| --- | --- | --- |
| 규칙 저장 HTTP body | 불필요한 `revision:4`가 포함됨 | `{rules}` 및 CSRF/cookie 검사 통과 |
| view/delete 미리보기 | 단일 rule 대신 5개 action 전체 객체 전송 | 각각 선택한 draft AST만 전송 |
| 고급 draft 뒤 preset 변경/Reset | 다른 action의 deny 편집이 owner로 소실됨 | 편집 보존 및 마지막 서버 정책 복원 |
| numeric literal | `9007199254740993`이 LosslessNumber 내부 객체로 표시됨 | load·preset·preview·save 모두 동일 숫자 token 유지 |
| preview 뒤 입력 변경 | 이전 allowed 판정이 화면에 남음 | draft/사용자 변경 즉시 판정 제거 |
| backup Keep | 입력 자체가 없음 | 빈 입력 거절, 명시적 0 저장 및 범위 설명 검사 |

규칙 전용 두 파일은 8개 테스트를 통과했다. 이 중 HTTP 경계 회귀는 6개이며 기존 화면 테스트는 2개다. Keep 회귀도 통과했다. 더 넓은 6파일 검사에서는 84개 중 83개가 통과했고, 병행 작업 중 바뀐 API rate-rule 오류 문구의 기존 assertion 1개가 실패했다. root 담당자가 해당 assertion을 갱신한 뒤 최종 전체 **613개 UI 테스트·typecheck·build**를 통과했다. UTF-8 label 후속 24개 검사 후 dist와 embedded Go binary도 다시 빌드했다. 이 담당자의 별도 `npm run typecheck` 역시 통과했다.

## 실제 브라우저 검증

`ui/admin/e2e/authenticated/independent-product.spec.ts`에 다음 두 흐름을 추가했다. 로컬 disposable native 서버 `127.0.0.1:58091`과 설치된 Chromium headless shell 1234를 사용한다. 저장소 기본 Playwright 설정은 수정하지 않았다.

1. System 설정 저장 → OS dark/light 변경 → 명시적 dark/light 선택 → System으로 복귀 후 새로고침.
2. 컬렉션/레코드 생성 및 user-access opt-in → view allow 저장/새로고침 → 저장하지 않은 deny/allow를 preview하여 실제 서버 판정 확인 → 컬렉션 정리.

이전 빌드에서 두 실패를 실제로 확인했다. System/OS dark 조건의 배경은 기대한 `rgb(17, 24, 39)` 대신 `rgb(245, 247, 251)`이었다. Rules Save는 기대한 HTTP 200 대신 HTTP 400을 반환했다. 테스트 준비 단계의 locator 및 settings PATCH 대기 수정은 제품 실패로 세지 않았다.

root의 dist/build 및 서버 재시작 후 같은 두 흐름이 통과했다. 기존 text ID/관계 선택, 관계 컬렉션 access, builder view 편집, auth scaffold ID 정책 4개 흐름도 함께 실행하여 **6개 모두 10.5초에 통과**했다. 이는 새 테스트 두 개만의 green이 아니라 기존 변경이 유지되는 것까지 포함한 실제 서버 검사다.

실행 명령(관리자 자격 증명은 ignored fixture 파일에서 환경 변수로만 로드):

```powershell
ui/admin/node_modules/.bin/playwright.cmd test independent-product.spec.ts remaining-development.spec.ts --config=.local/integration-runtime/playwright.config.ts --project=authenticated-chromium
```

실제 결과: `6 passed (10.5s)`. 로컬 설정의 설치된 Chromium 1234를 사용했고 서비스 origin은 fixture의 `http://127.0.0.1:58091`이었다.

## 통합 경계

- 보관 설정을 저장하는 즉시 기존 archive를 삭제하지 않는다. 관리형 앱은 `pgbase/backup_schedule.go:25`에서 scheduler enabled와 무관하게 `SetRetention`을 호출하고, `internal/backup/manager.go:379`의 override가 repository retention보다 우선한다. 정리는 manager의 성공한 archive publish 뒤 실행된다(`manager.go:367`). 수동/예약 create뿐 아니라 upload도 `publish`를 호출하므로 정리를 유발한다(`manager.go:435`). restore나 설정 변경 자체는 이 경로를 호출하지 않는다. 이번 후속 수정은 해당 우선순위/시점의 설명만 고쳤고 보관 동작은 바꾸지 않았다.
- 이 담당자는 SDK/Go/감사 문서/정책 상한을 수정하지 않았다. 초기 generated dist는 root가 통합 생성했으며, 보관 우선순위 도움말 후속 변경은 root 요청으로 `backup-storage-page.test.tsx` 4개 통과 후 `npm run build`로 다시 생성했다. 이 후속 변경은 문구만 바뀌어 6개 browser 흐름은 반복하지 않았다. 최종 JS asset은 `index-DbXeqqmK.js`이며 root가 이를 embed한 Go binary를 다시 빌드한다.
- 이번 test suite는 OS media 변경과 native rule wire 계약에 집중한다. 모든 관리자 화면의 모바일·접근성 인수를 뜻하지 않는다.
- 규칙 preview의 서버 지원 action은 list/view/update/delete이며 create preview를 새로 추가하지 않았다. backend 의미 검증과 collection 권한 판정은 서버에 남겨 두었다.
- API rate-limit label 검증·도움말은 root 소유 변경이다. 읽기 검토에서 UI가 registered route pattern, METHOD 우선순위, trailing slash prefix 및 caller/IP 범위를 설명하는 것을 확인했다.
