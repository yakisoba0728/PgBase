# PocketBase v0.40.4 대비 관리자 UI 조사 — 2026-09-22

실제 upstream UI 소스와 현재 PgBase 작업 트리를 대조했다. 기존 UI 32개 행은 **adapted 14 / partial 18**이며, 기존 표에 독립 행으로 없던 UI 공백 8개를 **missing**으로 추가했다. 이는 제품 완성률이 아니다. 신규 세부 행은 기존 넓은 행의 일부와 겹치므로 40개 행을 서로 독립인 동일 가중치 기능으로 더하면 안 된다.

PocketBase 기준은 공식 저장소 v0.40.4, commit `5cec579da984436a258602a46a96302fbd31f77c`이다. 로컬 checkout은 `.local/pocketbase-comparison/pocketbase`다. PgBase 기준은 `codex/remaining-development`, HEAD `6993744a8f36db48ddfeaa8dae017656752d24c7` 및 2026-09-22 현재 미커밋 변경 전체다. 정확한 행 데이터와 각 측의 소스/테스트 anchor는 [ui.json](ui.json)에 있다.

## 실제로 끝까지 연결된 흐름

첫 관리자 설치와 백업 초기화, 컬렉션 생성·변경·삭제 계획, scaffold·복제·truncate, SQL/builder view preview/apply, 레코드 CRUD·draft·duplicate, 키보드 관계 선택, 백업 및 파일 저장소 설정·test, SQL 실행/history/CSV가 구현되어 있다. PgBase의 typed schema 계획, cursor, ETag, 작업 원장, bounded SQL, PostgreSQL 타입은 PB의 SQLite/SDK/모달 방식과 다르다. 이 차이는 목적이 완성된 좁은 흐름에 한해 adapted로 분류했다. SQL console의 adapted는 PB처럼 아무 테이블에나 임의 SQL을 실행한다는 뜻이 아니다.

현재 작업의 text ID 정책·명시적 ID·대소문자 보존·관계 picker/hydration·실제 ID `new` 처리, 관계가 있는 컬렉션의 user-access 설정, 기존 builder view 수정, `1e400` 필터 보존, auth scaffold/schema 문서의 ID 정책 연결은 이미 존재한다. 과거 누락으로 다시 세지 않았다. 예를 들어 [현재 browser 회귀](../../../ui/admin/e2e/authenticated/remaining-development.spec.ts)는 text ID, relation access, builder edit, auth scaffold를 다룬다.

## 가장 먼저 채울 공백

**우선 수정할 자체 API 계약 오류도 확인됐다.** `http-admin-ports.ts:522`는 rules PUT에 `{rules,revision}`을 보내지만 `internal/api/rules.go:52`의 strict DTO는 `rules`만 허용한다. preview는 단일 `Rule` 자리에 전체 5-action rules 객체를 보낸다(`record-rules-editor.tsx:49`, `internal/recordaccess/preview.go:16`). 이는 PB와의 UX 차이 이전에 Save/Preview가 실제 서버와 연결되지 않는 문제다. `record-rules-editor.test.tsx`의 memoryBackend 통과는 이 HTTP 연결을 보증하지 않는다. UI-11에 이 P1 공백을 포함했다.

1. **인증/권한 관리 UI (P1).** CRUD rule 편집은 JSON AST와 preset/preview로 존재한다. 그러나 PB의 [authRule/manageRule 전용 입력](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/collections/collectionRulesTab.js#L215), token 종류별 TTL/무효화, 임의 password identity field와 MFA 세부 정책에 대응하는 편집 화면은 없다. raw schema import는 일상 설정 화면과 구분했다.
2. **관리자와 사용자 관리 범위 (P1).** PB [Manage superusers](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/base/appHeader.js#L79)는 관리자 계정 CRUD로 이어진다. PgBase는 첫 관리자 생성과 현재 관리자 이메일/IP 편집이 있으나 다른 관리자 관리 화면이 없다. 반면 일반 auth 컬렉션 사용자의 생성·프로필 변경·삭제는 Records에서 가능하다. 별도 Users 화면에도 revoke/verification/reset/OAuth unlink/impersonation이 있다. 부족한 것은 Users의 realm/공개 레코드 식별·편집 연결 및 기간 조절이다.
3. **스키마 편집 세부 기능 (P1/P2).** 파일 protected·JSON 크기·날짜 min/max를 editor에서 설정할 수 없고 필드 복제/순서 변경, raw expression/partial index 편집, 선택 export·old/new 구조 diff가 없다. 전체 schema JSON과 SQL preview가 있다는 이유로 PB의 [선택/비교 import](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/settings/sync/importCollectionsReviewModal.js#L38)까지 완료로 보지 않았다.
4. **명확히 동작이 덜 연결된 작은 UI (P1/P2).** System 테마는 설정 값을 저장하지만 [PB의 matchMedia 연동](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/store.js#L297)에 대응하는 코드가 없다. 날짜 입력은 분까지만 표시하고 `step=1`이 없어 PB의 초 단위 편집이 불가능하다. 선택 행 JSON export는 전체 필터 결과 export와 다르다.
5. **탐색/운영 편의 (P2/P3).** 관계 목록 label 확장, 보편적 검색과 기록, 공개 파일 라이브러리, 컬렉션 검색/pin/ERD, 로그인 경고 템플릿, 로그 시간 histogram/zoom, 전역 production 편집 잠금이 빠져 있다. 확장 화면은 인증 후 등록 경로에서 동작하지만 PB의 guest/blank 라우트와 기존 컴포넌트·header 확장 전체를 대신하지 못한다.

## 오판을 피한 경계

- PB 자체에 built-in 다국어 선택기/번역 catalog가 확인되지 않는다. 양쪽 화면의 문자열은 주로 영어다. 따라서 **한국어화/다국어가 없다는 사실을 PB 대비 missing으로 추가하지 않았다.** 코드 편집기의 language 설정은 사용자 언어 설정이 아니다. 앱별 extension으로 번역하는 문제는 별도다.
- PB CSS는 좁은 화면에서 버튼·검색·sidebar 모양을 세분화하고, PgBase도 wrapping nav/scroll table/40rem breakpoint를 가진다. 모바일 지원 전체가 없다고 하지 않는다. 다만 이번에는 실제 모바일 viewport, touch, screen reader 전 흐름을 실행 검증하지 않았으므로 a11y/mobile 완전 동등도 주장하지 않는다.
- 관계 picker는 **빈 검색어 browse가 이미 있다.** 공백은 indexed field의 exact 검색보다 넓은 검색/필터 UX와 bounded hydration 이후 표시다.
- PB v0.40.4 기본 records list는 40행, relation picker는 50행이다. 과거 문서의 페이지 크기를 새 소스 확인 없이 옮기지 않았다.
- rich text는 Tiptap, 지도는 offline SVG를 택했다. 좁은 편집/좌표 선택 목적은 완성되어 adapted지만 TinyMCE 모든 플러그인이나 OpenStreetMap 도로 수준 탐색과 1:1 동일하지 않다.
- DTO/서버 기능의 존재만으로 UI 제공을 판정하지 않았다. UI의 absence 역시 서버 기능 부재를 뜻하지 않는다.

## 행별 판정

| ID | 기능 | 판정 | 우선순위 |
| --- | --- | --- | --- |
| UI-01 | 최초 설치·첫 관리자·백업 초기화 | adapted | none |
| UI-02 | 관리자 로그인·MFA·로그아웃 | partial | P1 |
| UI-03 | 라우팅·미저장 편집 이탈 보호 | adapted | none |
| UI-04 | theme·responsive·a11y | partial | P1 |
| UI-05 | admin UI extension 등록·화면 | partial | P2 |
| UI-06 | create/edit/delete | adapted | none |
| UI-07 | duplicate/truncate/scaffold | adapted | none |
| UI-08 | type·view SQL dry-run | adapted | none |
| UI-09 | field type·validation editor | partial | P1 |
| UI-10 | relation 제약·index | partial | P2 |
| UI-11 | CRUD 권한 규칙 편집·미리보기 | partial | P1 |
| UI-12 | schema export/import/diff | partial | P1 |
| UI-14 | collection API preview | partial | P2 |
| UI-15 | list/filter/sort/cursor paging | partial | P2 |
| UI-16 | persisted columns | adapted | none |
| UI-17 | bulk delete·JSON export | partial | P2 |
| UI-19 | CRUD/duplicate/draft | adapted | none |
| UI-20 | relation picker/inline target | partial | P2 |
| UI-21 | file mapping/thumbnail/download | partial | P2 |
| UI-22 | auth users lifecycle actions | partial | P1 |
| UI-23 | realm/options/OAuth/OTP/MFA | partial | P1 |
| UI-24 | mail template/SMTP test | partial | P2 |
| UI-25 | service tokens/admin application settings | partial | P2 |
| UI-26 | logs search/detail/statistics/truncate | partial | P2 |
| UI-27 | scheduled jobs/cron configure/run | adapted | none |
| UI-28 | backup lifecycle | adapted | none |
| UI-29 | file/backup storage settings/test | adapted | none |
| UI-30 | SQL console/history/result/CSV | adapted | none |
| UI-32 | keyboard relation selection | adapted | none |
| UI-33 | 리치 텍스트·HTML source 편집 | adapted | none |
| UI-34 | geoPoint 좌표와 지도에서 위치 선택 | adapted | none |
| UI-35 | 날짜·시간 picker와 local timezone 표시 | partial | P2 |
| UI-X01 | 여러 관리자 계정 생성·편집·삭제 | missing | P1 |
| UI-X02 | 필드 복제·순서 재배치 | missing | P2 |
| UI-X03 | 컬렉션 관계 개요 ERD | missing | P3 |
| UI-X04 | 인증 허용 규칙·사용자 관리 권한 규칙 전용 편집 | missing | P1 |
| UI-X05 | 인증 컬렉션 token 기간·종류별 일괄 무효화 | missing | P1 |
| UI-X06 | 기존 공개 파일을 선택하는 미디어 라이브러리 | missing | P2 |
| UI-X07 | 컬렉션 검색·pin·system 분리 내비게이션 | missing | P3 |
| UI-X08 | 운영 화면의 파괴적 제어 잠금 | missing | P2 |

## 검증 출처와 한계

이번 UI 조사는 읽기와 문서 생성만 수행했다. production 코드 수정·설치·브라우저/Docker 재시작·추가 테스트 실행은 하지 않았다.

`prior-run`은 같은 작업 트리의 [직전 개발 검증 기록](../../development-completion-2026-09-22.md)에 있는 Vitest 46파일/590테스트와 실제 서버 Playwright 6흐름의 결과를 재사용했다는 뜻이다. 해당 행에 연결한 테스트 정의를 확인했지만, 이 이전 실행이 PB UI의 모든 변형을 검증했다고 해석하면 안 된다. `test-exists`는 테스트 정의만 근거이고, `static-only`는 실행 검증 없는 소스 조사다. missing/partial의 부재 판단은 앱 라우트·해당 페이지·editor·port를 읽고 관련 심볼을 검색한 결과다. 특히 날짜 초 단위와 자동 테마는 이번 조사에서 새 실패 테스트를 실행하지 않았다.

## 읽은 소스 범위

아래는 행에 직접 인용한 구현 파일이다. 각 파일은 전체 또는 해당 계약/동작 구간을 읽었다. 별도로 source 검색으로 route, locale, field 옵션, token/auth 규칙, storage, tests의 실제 존재를 확인했다. 테스트 anchor 목록은 JSON의 `testSource`에 있다.

### PocketBase

- `ui/src/apiPreview/apiPreviewModal.js`
- `ui/src/auth/pageInstaller.js`
- `ui/src/auth/pageSuperuserLogin.js`
- `ui/src/base/appHeader.js`
- `ui/src/base/dropdownKeyboardNav.js`
- `ui/src/base/fieldSettings.js`
- `ui/src/base/leaflet.js`
- `ui/src/base/s3ConfigFields.js`
- `ui/src/base/tinymce.js`
- `ui/src/collections/collectionAuthOptionsTab.js`
- `ui/src/collections/collectionRulesTab.js`
- `ui/src/collections/collectionUpsertModal.js`
- `ui/src/collections/collectionViewQueryTab.js`
- `ui/src/collections/collectionsOverviewModal.js`
- `ui/src/collections/collectionsSidebar.js`
- `ui/src/collections/indexUpsertModal.js`
- `ui/src/collections/tokenOptionsAccordion.js`
- `ui/src/fields/date/input.js`
- `ui/src/fields/file/settings.js`
- `ui/src/logs/logsChart.js`
- `ui/src/records/recordFilePickerModal.js`
- `ui/src/records/recordUpsertModal.js`
- `ui/src/records/recordsList.js`
- `ui/src/records/recordsPickerModal.js`
- `ui/src/router.js`
- `ui/src/settings/application/pageApplicationSettings.js`
- `ui/src/settings/backups/backupsList.js`
- `ui/src/settings/crons/cronsList.js`
- `ui/src/settings/sql/pageSQLConsole.js`
- `ui/src/settings/sync/importCollectionsReviewModal.js`
- `ui/src/store.js`

추가로 `ui/src/base/appHeader.js`, `ui/src/main.js`, `ui/src/css/layout.css`, `ui/src/collections/collectionFieldsTab.js`, `ui/src/fields/json/settings.js`, `ui/src/fields/date/settings.js`, `ui/src/fields/geoPoint/input.js`, `ui/src/fields/editor/input.js`, `ui/src/collections/passwordAuthAccordion.js`, `ui/src/collections/mfaAccordion.js`, `ui/src/settings/sync/pageExportCollections.js`, `ui/src/records/recordImpersonateModal.js`, `ui/src/settings/backups/backupsForm.js`, `ui/src/settings/backups/backupRestoreModal.js`, `ui/src/settings/storage/pageStorageSettings.js`의 관련 구간을 확인했다.

### PgBase

- `internal/userauth/lifecycle.go`
- `ui/admin/src/app.tsx`
- `ui/admin/src/backend/ports.ts`
- `ui/admin/src/components/auth-administration-panel.tsx`
- `ui/admin/src/components/auth-policy-editor.tsx`
- `ui/admin/src/designer/collection-designer-state.ts`
- `ui/admin/src/designer/collection-designer.ts`
- `ui/admin/src/extensions/admin-extensions.ts`
- `ui/admin/src/extensions/extension-screen.tsx`
- `ui/admin/src/pages/auth-collections-page.tsx`
- `ui/admin/src/pages/backup-storage-page.tsx`
- `ui/admin/src/pages/collection-api-examples.tsx`
- `ui/admin/src/pages/collection-designer-page.tsx`
- `ui/admin/src/pages/collection-field-editor.tsx`
- `ui/admin/src/pages/collections-page.tsx`
- `ui/admin/src/pages/field-options-editor.tsx`
- `ui/admin/src/pages/geo-point-field.tsx`
- `ui/admin/src/pages/login-page.tsx`
- `ui/admin/src/pages/operations-console.tsx`
- `ui/admin/src/pages/profile-settings.tsx`
- `ui/admin/src/pages/record-edit-page.tsx`
- `ui/admin/src/pages/record-rules-editor.tsx`
- `ui/admin/src/pages/records-page.tsx`
- `ui/admin/src/pages/rich-text-field.tsx`
- `ui/admin/src/pages/schema-transfer-page.tsx`
- `ui/admin/src/pages/service-tokens-page.tsx`
- `ui/admin/src/pages/settings-page.tsx`
- `ui/admin/src/pages/setup-page.tsx`
- `ui/admin/src/pages/sql-console-page.tsx`
- `ui/admin/src/pages/storage-settings-page.tsx`
- `ui/admin/src/pages/users-page.tsx`
- `ui/admin/src/pages/view-definition-editor.tsx`
- `ui/admin/src/pages/view-sample-preview.tsx`
- `ui/admin/src/relation/relation-query.ts`
- `ui/admin/src/relation/relation-target-picker.tsx`
- `ui/admin/src/routing/admin-location.ts`
- `ui/admin/src/routing/unsaved-draft.tsx`
- `ui/admin/src/styles.css`
- `ui/admin/src/workspace/record-productivity.ts`
- `ui/admin/src/workspace/record-workspace.ts`

`docs/pocketbase-feature-comparison-current-2026-09-11.json`에서는 기존 UI 32개 ID/이름만 가져왔으며 이전의 “구현” 판정을 증거로 재사용하지 않았다.
