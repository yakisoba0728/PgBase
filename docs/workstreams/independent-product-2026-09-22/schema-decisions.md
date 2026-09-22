# PgBase schema 결정 — 2026-09-22

이 문서는 PgBase를 독립 제품으로 평가한다. PocketBase 비교는 다른 동작을 발견하는 참고 자료이며, 동일 구현을 요구하는 제품 명세나 완료율 분모가 아니다. 기존 [PB 비교 감사](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/schema-fields.md)는 비교 당시 기록으로 보존한다. 그 문서의 PB 호환 우선순위를 PgBase 결함 우선순위로 그대로 사용하지 않는다.

## 지금 수정: 뷰 이름 변경 시 text ID 정책 보존

**완료.** text ID 정책을 가진 source의 builder view를 이름 변경하면, rename 계획이 기존 `RecordID`를 누락했다. catalog projection은 계획의 빈 정책으로 기존 값을 덮어쓰고, 저장 시 빈 정책은 UUID 기본값으로 정규화될 수 있었다. 이름만 바꾸는 작업이 public ID 계약과 다음 schema export를 바꾸는 PgBase 자체 결함이다. PB와의 호환 여부가 판정 근거가 아니다.

- 원인과 수정: [collection_rename.go:27](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/collection_rename.go:27)의 `PlannedCollection`에 `RecordID: current.RecordID`를 보존했다. 다른 native 정책은 변경하지 않았다.
- 실제 적용 경로: [catalog.go:119](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/catalog.go:119), [store.go:472](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/store.go:472), [record_id.go:52](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/record_id.go:52).
- 회귀: [TestViewRenamePreservesInheritedTextRecordIDPolicy](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/collection_rename_integration_test.go:186)는 서비스 Preview/Apply, 새 Service의 Catalog, source와 renamed view의 ExportSchema에서 같은 사용자 지정 text 정책과 view identity가 유지되는지 확인한다. DB 내부 행을 assertion 대상으로 삼지 않았다.
- red: 실제 PostgreSQL에서 preview 정책이 `Kind:""`, 나머지 값 0/빈 문자열인 것을 관찰하여 실패했다.
- green: 한 필드 보존 수정 후 아래 PostgreSQL integration/race 회귀가 통과했다. 기존 paired/standalone auth recordId 문서 경로도 함께 통과했다.

```powershell
docker exec pgbase-remaining-runner go test -tags=integration ./internal/schema -run '^TestViewRenamePreservesInheritedTextRecordIDPolicy$' -count=1
# 수정 전: FAIL, rename preview record ID policy = {Kind:"", ...}

docker exec pgbase-remaining-runner go test -race -tags=integration ./internal/schema -run 'Test(ViewRename|CollectionRename|DeclarativeView|SchemaDocumentImportedAuthTextRecordID|SchemaDocumentStandaloneAuthTextRecordID)' -count=1
# 수정 후: PASS, internal/schema 3.104s
```

공유 runner의 기존 PostgreSQL을 사용했고 재시작하지 않았다. 변경 범위는 rename 계획의 한 필드와 해당 서비스 통합 회귀다. 이전 auth schema document/record ID 수정은 보존했다.

## 의도된 계약: 그대로 유지

| 주제 | 결정 | PgBase 근거 |
| --- | --- | --- |
| relation cascade | 어느 참조 target 삭제로 source가 cascade closure에 들어가는 현재 의미를 유지한다. PB의 마지막 연결만 남았을 때 삭제하는 의미로 바꾸지 않는다. | [배포 계약:143](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/deployment.md:143)은 fixed-point closure를 명시한다. [관계 editor:97](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collection-field-editor.tsx:97)은 cascade가 source records를 삭제한다고 설명한다. [delete.go:339](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/relation/delete.go:339)이 그 의미를 구현한다. |
| scalar default / NULL / strict input | 기본 SQL NULL, 명시 default, 정확한 scalar 입력과 숫자 표현을 유지한다. PB coercion이나 implicit zero를 전체 타입에 추가하지 않는다. | [필드 계약:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/local-product-completion.md:21)은 required가 SQL NOT NULL 기준이며 false/zero/empty text를 허용한다고 명시한다. [codec.go:145](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:145), [planner.go:626](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:626). |
| import의 누락 fields/indexes | import에서 생략한 기존 field/index는 보존한다. PB `deleteMissing`을 맞추기 위해 column을 삭제하지 않는다. | [schema transfer:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/schema-import-export.md:9)은 이름 기반 merge와 누락 fields/indexes 보존을 데이터 보호 계약으로 명시한다. [schema_document.go:480](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:480), [schema_document.go:517](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:517). |
| 점유된 field 이름으로 rename | 두 기존 field 이름의 atomic swap을 현재 계약의 결함으로 보지 않는다. | [필드 계약:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/local-product-completion.md:11)은 occupied-name rename 거절을 명시한다. [planner.go:288](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:288)의 거절은 이 계약과 일치한다. |
| identity·물리 이름을 뺀 export | DB UUID를 다른 환경에 복사하거나 이름 변경을 추론하지 않는 portable schema를 유지한다. | [schema transfer:3,9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/schema-import-export.md:3)은 logical names와 ID/physical identifier 생략, name merge를 명시한다. |

## 별도 설계로 보류

| 주제 | 결정 | 이유와 후속 범위 |
| --- | --- | --- |
| builder view 수정의 schema import | 직접 편집이 가능하다는 이유만으로 import의 readonly 제한을 즉시 제거하지 않는다. 현재는 문서화된 제한이며 별도 native 개선 후보로 둔다. | [schema transfer:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/schema-import-export.md:13)은 기존 declarative view readonly를 명시한다. [schema_document.go:345](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:345)은 이를 거절하고 [view.go:132](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view.go:132)은 직접 update를 구현한다. 개선 시 projection/filter 변경, dependent views, field presentation, preview/apply 동일성 및 stale plan을 함께 정의해야 한다. |
| auth 설정 전체 이동 | portable schema를 런타임 settings/비밀 전체 backup으로 확대하지 않는다. auth 권한·정책·TTL·text ID의 현재 roundtrip을 유지한다. | [auth_schema_document.go:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/auth_schema_document.go:21)은 지원 범위를 명확한 DTO로 제한한다. paired/standalone text ID roundtrip은 [회귀:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/auth_record_id_document_integration_test.go:14), [회귀:40](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/auth_record_id_document_integration_test.go:40)으로 검증된다. OAuth 설정·메일 템플릿 등을 이동하려면 settings bundle의 소유권·비밀 처리·충돌 정책부터 정해야 한다. PB 옵션이 더 많다는 것만으로 현재 native roundtrip 버그라고 부르지 않는다. |
| SQL view의 public text ID / relation / file 의미 | 지원 범위를 넓히는 새 제품 기능으로 분리한다. | 현재 SQL view는 UUID physical identity와 scalar inference 계약이다. alias identity resolution·파일 원본 권한·relation expansion을 함께 설계해야 하며 이번 identity-preserving rename 수정과 섞지 않는다. |

## 문서 정리 완료: deleteMissing의 좁은 확장

현재 구현의 `SchemaDocument.deleteMissing`은 **문서에 없는 catalog collection 삭제**를 추가한다. 누락 system collection과 auth realm은 preview blocker로 보호하며, 누락 fields/indexes 보존 merge 규칙은 그대로다. [schema_document.go:282](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:282), [schema_delete_missing.go:53](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_delete_missing.go:53), [회귀:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/delete_missing_integration_test.go:14).

[schema-import-export.md:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/schema-import-export.md:9)을 현재 동작에 맞춰 기본 merge와 명시적 collection 삭제 옵션으로 구분했다. 누락 fields/indexes 보존과 기존 declarative view의 import readonly 제한도 명시했다. 역사적 [phase3-local-contracts.md:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/phase3-local-contracts.md:21)은 deleteMissing 자체가 없던 단계의 기록으로 유지한다. 설명을 바로잡았으며 destructive import 의미나 상세 설계는 확대하지 않았다.
