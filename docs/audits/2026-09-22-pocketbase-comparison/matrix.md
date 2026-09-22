# PocketBase v0.40.4 항목별 전체 대조표

기존 232행 + 추가 61행 = 293행. 판정 정의·우선순위·검증 한계는 [종합 보고서](README.md)를 따른다. 중간 fragment 파일은 이 표의 집계에 포함하지 않았다.

## SCH-01 · base collection CRUD

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: 관리자 collection create/update/delete가 모델과 레코드 테이블을 함께 변경한다.

PgBase: 생성·수정·삭제를 preview/checksum/guarded apply로 제공하며 PostgreSQL DDL·메타데이터·감사·버전·영수증을 같은 트랜잭션으로 처리한다.

차이/남은 범위: PB 직접 POST/PATCH wire가 아닌 PgBase 계획/적용 API를 사용하는 완성된 native CRUD. 세부 제약 차이는 다른 SCH 행에 별도 기록한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L18) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync.go#L21)

PgBase 근거: [internal/api/schema_handlers.go:209](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/schema_handlers.go:209) / [internal/schema/planner.go:120](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:120) / [internal/schema/operation_apply.go:196](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/operation_apply.go:196)

테스트/검증 근거: [internal/schema/service_integration_test.go:29](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/service_integration_test.go:29) / [internal/schema/service_integration_test.go:264](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/service_integration_test.go:264) / [internal/schema/service_integration_test.go:901](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/service_integration_test.go:901) / [internal/schema/service_integration_test.go:1115](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/service_integration_test.go:1115)

## SCH-02 · auth collection data model

**보완 필요** · P1 · prior-run · 기존 항목

PocketBase: auth collection 한 모델에 일반 필드/7개 규칙, OAuth2, passwordAuth, MFA, OTP(length/template 포함), authAlert, token 설정 및 이메일 템플릿을 둔다.

PgBase: auth authority와 catalog를 분리하고 nullable 7개 규칙, typed profile fields, recordId, password/MFA/OTP policy, token TTL을 관리·전송한다. 최근 auth recordId paired/standalone import/export 수정도 현재 코드에 포함되어 있다.

차이/남은 범위: SchemaDocumentAuthCollection/Policy에는 OAuth2 설정·auth alert·메일 템플릿·OTP length·PB token secrets/file-token configuration이 없다. 다른 settings API에 존재하는 기능까지 없다고 단정하지 않지만 auth collection aggregate/schema 이동이 PB 전체 모델을 보존하지는 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L99) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model.go#L381)

PgBase 근거: [internal/userauth/collections.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/collections.go:23) / [internal/schema/auth_schema_document.go:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/auth_schema_document.go:21) / [internal/schema/auth_schema_document.go:191](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/auth_schema_document.go:191)

테스트/검증 근거: [internal/schema/auth_collection_document_integration_test.go:28](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/auth_collection_document_integration_test.go:28) / [internal/schema/auth_record_id_document_integration_test.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/auth_record_id_document_integration_test.go:14) / [ui/admin/e2e/authenticated/remaining-development.spec.ts:152](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/e2e/authenticated/remaining-development.spec.ts:152)

## SCH-03 · catalog list and single lookup

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: collection 목록은 id/created/updated/name/system/type으로 filter/sort하고 search pagination 및 skipTotal을 지원하며 id/name 단건 조회를 제공한다.

PgBase: native map 및 compat=pocketbase pagination envelope, scalar filter/sort, case-insensitive id/name/compatibilityName 조회가 구현되어 있다.

차이/남은 범위: compat 목록에는 updated 필터/정렬 필드가 없고 skipTotal을 처리하지 않는다. perPage는 200 초과를 거절하며 PB는 최대 1000으로 정규화한다. 단건 DTO도 native shape다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L34) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L61) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/provider.go#L27) / [소스 4](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/provider.go#L191)

PgBase 근거: [internal/api/schema_handlers.go:421](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/schema_handlers.go:421) / [internal/api/schema_handlers.go:539](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/schema_handlers.go:539) / [internal/api/schema_handlers.go:619](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/schema_handlers.go:619)

테스트/검증 근거: [internal/api/schema_handlers_test.go:52](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/schema_handlers_test.go:52) / [internal/api/schema_handlers_test.go:69](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/schema_handlers_test.go:69) / [internal/api/schema_handlers_test.go:130](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/schema_handlers_test.go:130)

## SCH-04 · name, system and type invariants

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: collection name은 1..255의 word 문자 및 case-insensitive 유일성을 요구한다. 기존 type/system flag, system 이름/규칙은 변경할 수 없다.

PgBase: type/system flag 및 system rename/delete 보호, native identifier 규칙과 compatibilityName 별칭, case-insensitive 별칭 중복 검증이 있다.

차이/남은 범위: native 이름은 소문자 48자 제한(별칭은 PB 범위)이다. 추가로 RuleService.Set/ValidateRules 경로에는 collection.System에 따른 rule 변경 금지 검사가 없어 PB system-rule 불변조건과 다르다(정적 경로 확인; 실행 재현은 하지 않음).

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L100) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L166) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L505)

PgBase 근거: [internal/schema/planner.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:18) / [internal/schema/planner.go:192](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:192) / [internal/schema/names.go:10](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/names.go:10) / [internal/schema/service.go:309](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/service.go:309) / [internal/schema/collection_rename.go:101](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/collection_rename.go:101) / [internal/recordaccess/rules.go:49](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/rules.go:49)

테스트/검증 근거: [internal/schema/compatibility_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/compatibility_integration_test.go:12) / [internal/schema/planner_test.go:224](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_test.go:224)

## SCH-05 · collection rename lifecycle

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: 같은 collection ID를 유지하며 table/index schema를 갱신하고 rename과 field 변경을 한 update에서 처리한다.

PgBase: collectionId로 rename 대상을 선택하고 physical table/index/relation objects 및 view/rule 참조를 같은 schema transaction에서 갱신한다.

차이/남은 범위: rename 자체는 구현되어 있으나 DDL을 추가 발생시키는 필드/인덱스 변경과 rename을 한 번에 요청하면 rename_requires_unchanged_definition으로 거절한다. PB 해당 결합 update는 지원한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync.go#L50) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync_test.go#L29)

PgBase 근거: [internal/schema/collection_rename.go:48](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/collection_rename.go:48) / [internal/schema/collection_rename.go:77](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/collection_rename.go:77) / [internal/schema/collection_reference_rename.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/collection_reference_rename.go:13)

테스트/검증 근거: [internal/schema/collection_rename_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/collection_rename_integration_test.go:12) / [internal/schema/collection_rename_integration_test.go:102](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/collection_rename_integration_test.go:102) / [internal/schema/collection_rename_integration_test.go:146](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/collection_rename_integration_test.go:146)

## SCH-06 · dependency-aware collection delete

**좁은 계약 일치** · none · test-exists · 기존 항목

PocketBase: 다른 collection relation 참조가 있으면 일반 collection delete를 거절하며 system collection을 보호한다.

PgBase: incoming relation과 view 의존성을 계획 단계에서 거절하고 안전한 경우 소유 table/view/edge objects를 삭제한다.

차이/남은 범위: 이 행은 참조 보호와 안전한 삭제만 비교한다. PB IntegrityChecks(false) 내부 우회 및 import 전용 semantics는 SCH-24 범위다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model.go#L687)

PgBase 근거: [internal/schema/planner.go:465](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:465) / [internal/schema/field_options.go:103](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/field_options.go:103)

테스트/검증 근거: [internal/schema/sql_view_service_integration_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/sql_view_service_integration_test.go:11) / [internal/schema/relation_planner_test.go:392](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/relation_planner_test.go:392) / [internal/schema/service_integration_test.go:1115](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/service_integration_test.go:1115)

## SCH-07 · truncate collection records

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: view를 제외한 collection을 한 트랜잭션에서 비우며 ordinary delete의 cascade/file lifecycle을 수행한다.

PgBase: 관리자 truncate는 stable collection lock 아래 일반 record delete graph를 호출하여 hooks, files, audit/outbox를 보존한다.

차이/남은 범위: system collection은 PB truncate의 일반 precondition과 달리 일괄 거절한다. 전체 root ID를 메모리에 모으므로 PB 500개씩 조회보다 메모리 사용이 collection 크기에 비례한다. relation cascade 의미 차이는 FLD-12가 추가로 적용된다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L183) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_query.go#L221)

PgBase 근거: [internal/recordops/truncate.go:27](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/truncate.go:27) / [internal/recordops/truncate.go:59](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/truncate.go:59) / [internal/api/schema_handlers.go:175](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/schema_handlers.go:175)

테스트/검증 근거: [internal/recordops/truncate_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/truncate_integration_test.go:16) / [internal/recordops/truncate_concurrency_integration_test.go:40](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/truncate_concurrency_integration_test.go:40)

## SCH-08 · schema scaffolds

**대응 구현·설계 차이** · none · executed-now · 기존 항목

PocketBase: base/auth/view의 시스템 필드·타입별 옵션·인덱스가 포함된 collection 시작 모델을 반환한다.

PgBase: base/view portable document 및 auth의 paired catalog/authority scaffold를 반환한다. auth system 필드와 기본 password/MFA/OTP/rules가 포함되고 import로 생성된다.

차이/남은 범위: PgBase wire, owner rules, UUID 기본 ID, password 12-byte 기본 정책은 의도된 native 기본값이다. PB auth 옵션 전체 이식의 한계는 SCH-02에 별도 기록한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L201) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model.go#L398) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L50)

PgBase 근거: [internal/schema/scaffolds.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/scaffolds.go:22) / [internal/api/schema_handlers.go:169](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/schema_handlers.go:169) / [ui/admin/src/designer/collection-designer-state.ts:264](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/designer/collection-designer-state.ts:264)

테스트/검증 근거: [internal/schema/scaffolds_test.go:10](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/scaffolds_test.go:10) / [internal/schema/scaffolds_test.go:38](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/scaffolds_test.go:38) / [internal/schema/scaffolds_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/scaffolds_integration_test.go:12) / [ui/admin/e2e/authenticated/remaining-development.spec.ts:152](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/e2e/authenticated/remaining-development.spec.ts:152)

## SCH-09 · collection record rules

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: list/view/create/update/delete의 null(잠김), 빈 문자열(허용), filter 문자열을 collection schema에 보존·검증한다.

PgBase: typed AST rule 저장·preview API와 compat PocketBaseRules 변환이 있으며 null/empty/expression source를 보존한다.

차이/남은 범위: 이 행은 5개 규칙의 schema 저장/변환만 판정한다. 별도 userAccess opt-in 및 전체 규칙 표현식의 실행 범위는 records/query audit, system rule 보호는 SCH-04 범위다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model.go#L358) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L131) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L477)

PgBase 근거: [internal/api/rules.go:20](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/rules.go:20) / [internal/accessrule/pocketbase.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase.go:15) / [internal/recordaccess/rules.go:49](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/rules.go:49)

테스트/검증 근거: [internal/recordaccess/pocketbase_rule_integration_test.go:173](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/pocketbase_rule_integration_test.go:173) / [internal/recordaccess/rules_validation_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/rules_validation_test.go:13)

## SCH-10 · common field metadata and system protection

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: stable field id/name/system/hidden/presentable/help metadata를 저장하며 system field 삭제/rename을 금지한다.

PgBase: UUID field ID, logical Name/physical ColumnName, system/hidden/presentable/help를 저장·export하며 system 필드 rename/delete를 거절한다.

차이/남은 범위: UUID 및 별도 physical identity로 구현된 대응 기능. collection 이름/필드 이름 범위 차이는 native 식별자 정책이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field.go#L69) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L425)

PgBase 근거: [internal/schema/types.go:33](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/types.go:33) / [internal/schema/planner.go:323](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:323) / [internal/schema/planner.go:357](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:357) / [internal/schema/schema_document.go:108](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:108)

테스트/검증 근거: [internal/schema/compatibility_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/compatibility_integration_test.go:12) / [internal/schema/field_presentation_test.go:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/field_presentation_test.go:26) / [internal/schema/field_contract_integration_test.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/field_contract_integration_test.go:14)

## SCH-11 · required blank and zero-value semantics

**좁은 계약 일치** · none · test-exists · 기존 항목

PocketBase: required bool은 true, number는 nonzero, text는 nonempty, JSON은 빈 JSON 값 이외를 요구한다.

PgBase: validationMode=pocketbase에서 false/0/빈 text/빈 JSON을 required 위반으로 거절하고 mode/옵션 강화 때 저장된 값도 검사한다.

차이/남은 범위: 이 판정은 명시적 pocketbase mode와 올바른 입력 타입의 required 검사에 한정한다. 기본 postgres mode는 NULL 기준이고 PB coercion/defaults 전체는 FLD/SCH-12에서 partial이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_bool.go#L115) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_number.go#L150) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_text.go#L224) / [소스 4](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_json.go#L190)

PgBase 근거: [internal/query/codec.go:145](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:145) / [internal/query/codec.go:171](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:171) / [internal/query/codec.go:295](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:295) / [internal/schema/field_preflight.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/field_preflight.go:18)

테스트/검증 근거: [internal/query/codec_test.go:127](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:127) / [internal/schema/compatibility_integration_test.go:70](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/compatibility_integration_test.go:70)

## SCH-12 · field defaults

**보완 필요** · P1 · executed-now · 기존 항목

PocketBase: bool=false, number=0, text='' 등 타입별 implicit non-null zero default를 사용한다.

PgBase: typed explicit SQL DEFAULT를 제공하고 editor/geoPoint에는 고정 zero default를 설정하지만 일반 optional scalar에는 implicit zero를 넣지 않는다.

차이/남은 범위: 일반 optional text/bool/number의 누락 값은 명시 default가 없으면 NULL이 가능하다. pocketbase validationMode도 PB 기본값을 자동 설치하지 않는다. 따라서 omitted/null/empty의 PB 계약과 같지 않다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_bool.go#L99) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_number.go#L129) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_text.go#L162) / [소스 4](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_json.go#L116)

PgBase 근거: [internal/schema/planner.go:606](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:606) / [internal/schema/planner.go:626](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:626) / [internal/schema/defaults.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/defaults.go:23)

테스트/검증 근거: [internal/schema/planner_test.go:246](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_test.go:246) / [internal/schema/defaults_test.go:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/defaults_test.go:9) / [internal/schema/advanced_fields_integration_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/advanced_fields_integration_test.go:13)

## SCH-13 · single-field index and uniqueness

**대응 구현·설계 차이** · none · executed-now · 기존 항목

PocketBase: collection raw indexes로 일반 단일 필드 index/unique constraint를 생성·검증한다.

PgBase: field Indexed/Unique flag와 structured/raw index로 단일 scalar index 생성·변경·삭제 및 기존 데이터 위반 처리를 지원한다.

차이/남은 범위: 일반 scalar 단일 index 기능의 대응 판정. nullable unique, index 물리 이름, SQL 표현식/타입 범위 차이는 PostgreSQL 정책 및 SCH-12/SCH-15 범위다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L525) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync.go#L325)

PgBase 근거: [internal/schema/planner.go:673](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:673) / [internal/schema/planner_indexes.go:41](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_indexes.go:41)

테스트/검증 근거: [internal/schema/planner_test.go:388](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_test.go:388) / [internal/schema/planner_test.go:411](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_test.go:411) / [internal/schema/service_integration_test.go:1210](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/service_integration_test.go:1210) / [internal/schema/service_integration_test.go:1292](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/service_integration_test.go:1292)

## SCH-14 · compound indexes

**보완 필요** · P3 · executed-now · 기존 항목

PocketBase: CREATE INDEX 정의의 여러 column 및 unique 복합 인덱스를 제공한다.

PgBase: 2..8개 fields 또는 최대 8개 structured/raw keys를 갖는 named compound/unique index와 lifecycle이 구현되어 있다.

차이/남은 범위: 최대 8개 key 제한이 PB raw index 계약보다 좁다. relation/file/JSON 등 indexable type 제한은 SCH-15에 함께 기록한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L538) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync_test.go#L45)

PgBase 근거: [internal/schema/planner_indexes.go:41](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_indexes.go:41) / [internal/schema/planner_indexes.go:56](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_indexes.go:56) / [internal/schema/planner_indexes.go:118](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_indexes.go:118)

테스트/검증 근거: [internal/schema/compound_index_test.go:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/compound_index_test.go:9) / [internal/schema/compound_index_test.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/compound_index_test.go:30) / [internal/schema/compound_index_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/compound_index_integration_test.go:16)

## SCH-15 · expression, partial and raw SQL indexes

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: SQLite에서 유효한 raw CREATE INDEX의 expression/collation/order/WHERE 정의를 검증·적용한다.

PgBase: fields/keys/rawSql 세 형태로 PostgreSQL btree expression/partial index를 안전하게 compile/export/import한다. SQL AST를 검사한다.

차이/남은 범위: 함수는 lower/upper/length/btrim/abs, collation은 C/POSIX로 제한된다. JSON/relation/file/geo/password는 index key에서 거절한다. PB에서 가능한 다른 유효 expression이나 NOCASE collation/raw index를 그대로 대응하지 않는다. PostgreSQL 전용 INCLUDE/CONCURRENTLY 미지원은 PB gap으로 세지 않았다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L538) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync.go#L325)

PgBase 근거: [internal/schema/planner_indexes.go:41](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_indexes.go:41) / [internal/schema/planner_indexes.go:154](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_indexes.go:154) / [internal/schema/planner_indexes.go:171](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_indexes.go:171) / [internal/schema/planner_indexes.go:271](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_indexes.go:271)

테스트/검증 근거: [internal/schema/advanced_index_test.go:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/advanced_index_test.go:9) / [internal/schema/advanced_index_test.go:31](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/advanced_index_test.go:31) / [internal/schema/advanced_index_integration_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/advanced_index_integration_test.go:15)

## SCH-16 · scalar field rename then drop/name reuse

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: stable field ID로 rename된 값을 보존하고 옛 이름의 새 필드 추가/삭제를 처리한다.

PgBase: field UUID와 고정 physical ColumnName을 사용하며 이름 재사용 시 별도 physical 이름을 배정하므로 retained 값/인덱스를 보존한다.

차이/남은 범위: 논리 이름/물리 이름 분리 방식의 완성된 rename/reuse 기능. 서로 점유 중인 두 이름의 동시 교환은 SCH-25의 별도 미지원 동작이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync.go#L94)

PgBase 근거: [internal/schema/planner.go:281](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:281) / [internal/schema/planner.go:309](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:309) / [internal/schema/planner.go:454](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:454)

테스트/검증 근거: [internal/schema/field_contract_integration_test.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/field_contract_integration_test.go:14) / [internal/schema/field_contract_integration_test.go:36](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/field_contract_integration_test.go:36) / [internal/schema/planner_test.go:374](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_test.go:374)

## SCH-17 · renamed multi-relation collection delete

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: relation 필드를 rename한 collection도 이후 삭제할 수 있으며 해당 record table을 제거한다.

PgBase: edge table 방식의 relation도 보존된 ColumnName/Storage.Name에서 소유 object 이름을 계산하여 삭제한다.

차이/남은 범위: PB JSON relation column과 PostgreSQL edge table 저장방식이 다르며 사용자 삭제 기능은 대응한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync.go#L111) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model.go#L721)

PgBase 근거: [internal/schema/planner.go:490](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:490) / [internal/schema/relation_types.go:10](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/relation_types.go:10)

테스트/검증 근거: [internal/schema/field_contract_integration_test.go:205](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/field_contract_integration_test.go:205) / [internal/schema/relation_planner_test.go:392](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/relation_planner_test.go:392)

## SCH-18 · field add, drop and type change policy

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: field 추가/삭제를 지원하며 기존 field ID의 타입 변경은 금지한다. 추가 필드는 타입별 zero/default로 채운다.

PgBase: 필드 추가/삭제와 integer→bigint/numeric widening을 지원한다. 나머지 타입 변경은 거절한다.

차이/남은 범위: required scalar를 나중에 추가할 때 테이블이 비어 있어도 명시 default가 없으면 missing_default로 거절한다. PB는 implicit default를 사용한다. numeric widening은 native 확장이나 pocketbase mode에도 별도 금지 분기가 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L220) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync.go#L80)

PgBase 근거: [internal/schema/planner.go:348](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:348) / [internal/schema/planner.go:379](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:379) / [internal/schema/planner.go:637](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:637) / [internal/schema/type_changes.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/type_changes.go:12)

테스트/검증 근거: [internal/schema/planner_test.go:342](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_test.go:342) / [internal/schema/planner_test.go:441](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_test.go:441) / [internal/schema/planner_test.go:486](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_test.go:486)

## SCH-19 · view creation and field inference

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: SELECT query로 view fields를 추론하며 id는 text로 정규화한다. ROW_NUMBER() 같은 계산 id도 허용한다.

PgBase: declarative source/projection/filter 및 inspected PostgreSQL SQL view를 제공하며 의존성·타입·현재 결과의 non-null/unique id를 검사한다. builder view는 source recordId 정책을 상속한다.

차이/남은 범위: SQL view id는 UUID만 허용하므로 PB 문자열/숫자/ROW_NUMBER id view를 표현하지 못한다. SQL 출력은 제한된 scalar 타입·최대 128 fields, 함수는 안전한 pg_catalog 범위다. file/relation inference는 SCH-27에서 별도 분류한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/view.go#L82) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/view.go#L117) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_query.go#L253)

PgBase 근거: [internal/schema/view.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view.go:16) / [internal/schema/view.go:58](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view.go:58) / [internal/schema/sql_view_inspect.go:35](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/sql_view_inspect.go:35) / [internal/schema/sql_view_inspect.go:136](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/sql_view_inspect.go:136)

테스트/검증 근거: [internal/schema/sql_view_inspect_integration_test.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/sql_view_inspect_integration_test.go:14) / [internal/schema/sql_view_inspect_integration_test.go:43](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/sql_view_inspect_integration_test.go:43) / [internal/schema/view_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view_integration_test.go:12)

## SCH-20 · dry-run view sample

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: 관리자 dry-run은 길이 제한된 SELECT query의 inferred fields와 최대 10개 sample record를 반환한다.

PgBase: SampleView는 query 5000-byte 제한, 검사 후 최대 10행/fields/dependencies를 반환하고 inspection savepoint를 정리한다.

차이/남은 범위: 지원 SQL 집합 내에 대응하는 preview 기능. PB sample 대신 native items/dependencies shape, bytes 길이 제한을 사용하며 SQL 지원 제약은 SCH-19에 기록한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L262) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/view.go#L138)

PgBase 근거: [internal/schema/view_sample.go:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view_sample.go:9) / [internal/schema/view_sample.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view_sample.go:17) / [internal/schema/sql_view_inspect.go:35](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/sql_view_inspect.go:35) / [internal/api/schema_handlers.go:655](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/schema_handlers.go:655)

테스트/검증 근거: [internal/schema/compatibility_integration_test.go:49](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/compatibility_integration_test.go:49) / [internal/api/schema_operations_live_test.go:106](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/schema_operations_live_test.go:106)

## SCH-21 · view update and dependency guards

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: viewQuery update는 새 query로 필드를 다시 추론하고 view를 갱신한다.

PgBase: 직접 계획/적용 API는 SQL view와 builder view의 projection/filter 수정을 처리하고 source field/view 의존성을 보호한다. 최신 UI도 기존 builder state를 hydrate하고 수정 preview/apply를 수행한다.

차이/남은 범위: 지원되는 view 모델의 직접 수정 기능은 완료. dependent SQL view가 있는 수정은 거절하는 native guard이며 import의 builder 수정 누락은 SCH-26이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L118) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L77) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model.go#L877)

PgBase 근거: [internal/schema/view.go:101](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view.go:101) / [internal/schema/view.go:132](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view.go:132) / [internal/schema/view.go:277](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view.go:277) / [internal/api/schema_handlers.go:244](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/schema_handlers.go:244)

테스트/검증 근거: [internal/schema/compatibility_integration_test.go:49](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/compatibility_integration_test.go:49) / [internal/schema/sql_view_service_integration_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/sql_view_service_integration_test.go:11) / [ui/admin/e2e/authenticated/remaining-development.spec.ts:104](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/e2e/authenticated/remaining-development.spec.ts:104)

## SCH-22 · schema export

**보완 필요** · P1 · prior-run · 기존 항목

PocketBase: dashboard는 collection list JSON을 export하며 created/updated 및 OAuth2 provider secrets를 제외하고 ID/fields/indexes/auth options를 유지한다.

PgBase: repeatable-read snapshot으로 정렬된 pgbase.schema/v1 논리 schema를 내보낸다. field metadata, indexes, rules, auth policy/TTLs 및 text recordId를 양쪽 auth/catalog에 보존한다.

차이/남은 범위: DB/field identity 생략은 native portable format의 의도된 변화다. 그러나 OAuth2 enabled/mappedFields 등 비밀이 아닌 설정과 authAlert/메일 template/OTP length도 export schema 모델에 없어서 PB 전체 설정 이동에 못 미친다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/settings/sync/pageExportCollections.js#L24) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L99)

PgBase 근거: [internal/schema/schema_document.go:19](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:19) / [internal/schema/schema_document.go:71](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:71) / [internal/schema/schema_document.go:102](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:102) / [internal/schema/auth_schema_document.go:46](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/auth_schema_document.go:46)

테스트/검증 근거: [internal/schema/schema_document_integration_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document_integration_test.go:13) / [internal/schema/auth_collection_document_integration_test.go:28](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/auth_collection_document_integration_test.go:28) / [internal/schema/auth_record_id_document_integration_test.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/auth_record_id_document_integration_test.go:14) / [ui/admin/e2e/authenticated/remaining-development.spec.ts:152](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/e2e/authenticated/remaining-development.spec.ts:152)

## SCH-23 · atomic schema import and preview

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: ID 우선, 이름 fallback으로 기존 collection에 merge하고 views를 뒤로 정렬하여 한 트랜잭션으로 import한다.

PgBase: savepoint 기반 preview, projected catalog, baseVersion/checksum/idempotency로 DDL·rules·auth policy를 원자 적용한다. 현재 fields/indexes를 name으로 merge한다.

차이/남은 범위: portable 문서에 collection ID가 없고 name 기준 매칭이어서 PB처럼 같은 ID+새 name을 가져와 기존 collection을 rename하지 못한다. 기존 declarative view update 및 deleteMissing의 field/realm 한계는 SCH-26/SCH-24에 별도 기록한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_import.go#L31) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_import.go#L51) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_import.go#L136)

PgBase 근거: [internal/schema/schema_document.go:156](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:156) / [internal/schema/schema_document.go:262](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:262) / [internal/schema/schema_document.go:480](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:480) / [internal/schema/schema_document.go:531](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:531)

테스트/검증 근거: [internal/schema/schema_document_integration_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document_integration_test.go:13) / [internal/schema/schema_document_ledger_integration_test.go:162](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document_ledger_integration_test.go:162) / [internal/schema/auth_record_id_document_integration_test.go:112](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/auth_record_id_document_integration_test.go:112)

## SCH-24 · import deleteMissing

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: deleteMissing=true는 누락된 모든 non-system collections 및 non-system schema fields를 제거하고 system은 보존한다.

PgBase: 문서에 없는 catalog collection을 dependency-safe 순서로 삭제하지만 누락 system collection/auth authority는 preview blocker로 보호한다.

차이/남은 범위: importedFields는 DeleteMissing와 무관하게 기존 누락 필드를 항상 추가하므로 PB의 필드 삭제가 구현되지 않는다. PB는 system을 건너뛰지만 PG는 전체 요청을 거절하고, non-system auth realm의 missing 삭제도 거절한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_import.go#L34) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_import.go#L85) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_import.go#L155)

PgBase 근거: [internal/schema/schema_delete_missing.go:10](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_delete_missing.go:10) / [internal/schema/schema_delete_missing.go:53](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_delete_missing.go:53) / [internal/schema/schema_document.go:282](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:282) / [internal/schema/schema_document.go:498](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:498)

테스트/검증 근거: [internal/schema/delete_missing_integration_test.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/delete_missing_integration_test.go:14) / [internal/schema/delete_missing_integration_test.go:31](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/delete_missing_integration_test.go:31) / [internal/schema/delete_missing_integration_test.go:52](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/delete_missing_integration_test.go:52)

## SCH-25 · atomic swap of existing field names

**미구현** · P2 · static-only · 추가 확인 항목

PocketBase: 기존 두 field ID의 name/title→title/name 교환을 임시 column 이름을 써서 한 schema update로 처리한다.

PgBase: stable ID rename은 지원하지만 목표 이름이 다른 기존 field에 점유되어 있으면 계획 단계에서 중단한다.

차이/남은 범위: 동시 name swap 작업은 rename_target_exists로 거절된다. 기존 테스트는 한 field rename+옛 이름 재사용만 검증하며 두 기존 ID의 교환은 검증하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync.go#L97)

PgBase 근거: [internal/schema/planner.go:281](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:281) / [internal/schema/planner.go:288](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:288)

테스트/검증 근거: [internal/schema/field_contract_integration_test.go:36](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/field_contract_integration_test.go:36)

## SCH-26 · declarative view changes through schema import

**보완 필요** · P2 · static-only · 추가 확인 항목

PocketBase: 기존 view collection의 새 viewQuery를 import하여 갱신할 수 있다.

PgBase: native import는 view 신규 생성·동일 정의 no-op·SQL view 수정은 처리하나 기존 declarative view의 바뀐 projection/filter/source는 거절한다.

차이/남은 범위: 직접 UI/API update가 가능한 builder view를 export→정의 변경→import하면 view_update_unsupported다. 직접 update 회귀 테스트 통과를 import 지원 증거로 사용하지 않았다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_import.go#L63) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_import.go#L170) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L77)

PgBase 근거: [internal/schema/schema_document.go:339](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:339) / [internal/schema/schema_document.go:345](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:345) / [internal/schema/view.go:132](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view.go:132)

테스트/검증 근거: [internal/schema/schema_document_integration_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document_integration_test.go:13) / [internal/schema/compatibility_integration_test.go:49](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/compatibility_integration_test.go:49) / [ui/admin/e2e/authenticated/remaining-development.spec.ts:104](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/e2e/authenticated/remaining-development.spec.ts:104)

## SCH-27 · relation and file semantics in views

**미구현** · P2 · static-only · 추가 확인 항목

PocketBase: source relation/file field를 view alias로 추론하고 view file 요청을 원본 record/file로 해결한다.

PgBase: declarative view projection은 relation/file/password를 거절한다. SQL view는 scalar type만 추론하며 Relation/Options를 지운다.

차이/남은 범위: relation expansion 및 source file download metadata를 유지하는 view field 작업이 없다. scalar SQL로 ID/filename 값을 흉내 내는 것과 PB view relation/file 계약은 다르다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/view.go#L202) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/view.go#L457) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/view.go#L470)

PgBase 근거: [internal/schema/view.go:38](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view.go:38) / [internal/schema/view.go:68](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view.go:68) / [internal/schema/sql_view_inspect.go:142](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/sql_view_inspect.go:142)

테스트/검증 근거: [internal/schema/sql_view_inspect_integration_test.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/sql_view_inspect_integration_test.go:14)

## FLD-01 · bool field

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: PrepareValue casts supported input to bool; storage defaults false and required accepts only true.

PgBase: boolean codec accepts JSON true/false only. PocketBase validation mode now rejects required false; optional null remains SQL NULL and omitted optional fields have no automatic false default.

차이/남은 범위: P2: PB coercion (for example string true) and false default/optional-null normalization are not implemented, including in PocketBase validation mode. Required false behavior is implemented; the old row's suggestion that this is still missing is stale.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_bool.go#L99-L119) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_bool_test.go#L29-L44)

PgBase 근거: [internal/query/codec.go:145](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:145) / [internal/query/codec.go:166](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:166) / [internal/schema/planner.go:607](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:607)

테스트/검증 근거: [internal/query/codec_test.go:66](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:66) / [internal/query/codec_test.go:127](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:127) / [internal/query/codec_test.go:161](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:161)

## FLD-02 · number field

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: A float64 number is prepared by cast.ToFloat64, rejects nonfinite numbers, supports Min/Max and OnlyInt, and rejects required zero.

PgBase: Separate int32, bigint and exact decimal PostgreSQL types enforce min/max and PB-mode required nonzero. Bigint/numeric accept and emit strings; integer accepts only plain JSON integer spelling.

차이/남은 범위: P2: Exact typed numbers are useful native adaptations, but PB number wire coercion is absent, as is an independent OnlyInt flag over the full PB numeric range. JSON decimal 1.2 is rejected by numeric (expects string), numeric strings are rejected by integer, and optional null is not normalized to zero.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_number.go#L129-L169) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_number.go#L198-L208) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_number_test.go#L30-L46)

PgBase 근거: [internal/query/codec.go:175](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:175) / [internal/query/codec.go:203](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:203) / [internal/fieldvalue/options.go:178](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options.go:178) / [internal/schema/types.go:52](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/types.go:52)

테스트/검증 근거: [internal/query/codec_test.go:19](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:19) / [internal/query/codec_test.go:127](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:127) / [internal/query/codec_test.go:161](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:161) / [internal/fieldvalue/options_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options_test.go:11)

## FLD-03 · text validators and autogenerate

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: Text casts inputs to string; optional empty text bypasses min/pattern; nonempty values use rune min/max (default max 5000), regex, and create-time regex autogeneration for a zero/empty value.

PgBase: Strict JSON string codec supports rune min/max, regex and bounded cryptographic generation. Generation happens only when create input omits the field; explicit empty/null are not replaced. Unindexed text has a 1 MiB byte cap and indexed/unique text a 2048-byte cap.

차이/남은 범위: P2: No PB default 5000-rune limit, no optional-empty validator bypass, no string coercion; explicit empty text does not trigger PB autogeneration. The PG random regex subset/2048-byte generation bound is intentionally narrower. Explicit-empty mismatch is asserted by an existing integration test.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_text.go#L173-L175) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_text.go#L222-L256) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_text.go#L348-L373)

PgBase 근거: [internal/fieldvalue/options.go:100](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options.go:100) / [internal/fieldvalue/random_pattern.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/random_pattern.go:16) / [internal/query/codec.go:109](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:109) / [internal/query/codec.go:219](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:219) / [internal/recordops/generated_fields.go:29](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields.go:29)

테스트/검증 근거: [internal/fieldvalue/options_test.go:57](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options_test.go:57) / [internal/recordops/generated_fields_integration_test.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields_integration_test.go:24) / [internal/recordops/generated_fields_integration_test.go:59](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields_integration_test.go:59) / [internal/query/codec_test.go:207](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:207)

## FLD-04 · custom text primary key

**대응 구현·설계 차이** · P2 · executed-now · 기존 항목

PocketBase: Record id is immutable primary-key text with pattern/min/max/autogeneration and forbidden/reserved filesystem names. For custom patterns PB also checks duplicates using COLLATE NOCASE.

PgBase: Text record identity is a durable exact-case alias to internal UUID with default 15-character lowercase generation, custom length/pattern bounds, forbidden/reserved names, transactional uniqueness, CRUD/relation/auth projection and no UUID fallback. Case-distinct IDs are intentionally separate.

차이/남은 범위: P2 for a PB wire-compatibility promise: PB rejects custom IDs differing only by case, whereas PgBase deliberately accepts them; PG limits IDs to 255 runes and defaults native collections to UUID. Functional custom-ID capability is present, but identity policy is an explicit adaptation, not 1:1 equivalence. Only policy unit tests were run now; identity integration tests were inspected.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_text.go#L161-L175) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_text.go#L185-L215) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_text.go#L259-L277)

PgBase 근거: [internal/schema/record_id.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/record_id.go:12) / [internal/schema/record_id.go:87](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/record_id.go:87) / [internal/recordidentity/service.go:41](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordidentity/service.go:41) / [internal/recordidentity/service.go:99](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordidentity/service.go:99) / [internal/userauth/record_identity.go:28](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/record_identity.go:28)

테스트/검증 근거: [internal/schema/record_id_test.go:5](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/record_id_test.go:5) / [internal/recordidentity/service_integration_test.go:19](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordidentity/service_integration_test.go:19) / [internal/recordops/text_identity_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/text_identity_integration_test.go:16) / [internal/recordops/text_identity_integration_test.go:55](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/text_identity_integration_test.go:55) / [internal/userauth/text_record_identity_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/text_record_identity_integration_test.go:16) / [ui/admin/e2e/authenticated/remaining-development.spec.ts:4](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/e2e/authenticated/remaining-development.spec.ts:4) / [ui/admin/e2e/authenticated/remaining-development.spec.ts:152](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/e2e/authenticated/remaining-development.spec.ts:152)

## FLD-05 · email field and domain constraints

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: Optional empty email is allowed; nonempty email is syntax checked. OnlyDomains/ExceptDomains match the extracted domain exactly and cannot both be configured.

PgBase: text format=email uses net/mail, lowercases/normalizes configured domains, matches parent domain and subdomains, and supports simultaneous allow/deny with deny precedence.

차이/남은 범위: P2: allow example.com accepts user@sub.example.com in PG but fails PB's exact membership; both allow+deny may coexist only in PG; optional empty string is rejected by PG's mail parser instead of accepted. No compatibility-mode switch changes these semantics.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_email.go#L117-L170)

PgBase 근거: [internal/fieldvalue/options.go:56](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options.go:56) / [internal/fieldvalue/options.go:120](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options.go:120) / [internal/fieldvalue/options.go:238](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options.go:238) / [internal/schema/field_options.go:70](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/field_options.go:70)

테스트/검증 근거: [internal/fieldvalue/options_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options_test.go:11) / [internal/fieldvalue/options_test.go:35](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options_test.go:35) / [internal/query/field_options_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/field_options_test.go:11)

## FLD-06 · URL field and domain constraints

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: Optional empty URL is allowed; nonempty values use the PB URL validator. Domain restrictions compare exact parsed u.Host, including any port, and only/except lists are mutually exclusive.

PgBase: text format=url accepts only http/https URLs with a hostname; domain matching uses lowercased Hostname(), ignoring port, and includes subdomains with deny precedence.

차이/남은 범위: P2: optional empty input is rejected; parent-domain/subdomain and port matching differ from PB exact u.Host membership. For example allow example.com accepts https://example.com:8080 in PG but not PB. Scheme coercion and mutually-exclusive PB option names are not adapted.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_url.go#L117-L172)

PgBase 근거: [internal/fieldvalue/options.go:128](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options.go:128) / [internal/fieldvalue/options.go:238](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options.go:238) / [internal/fieldvalue/options.go:250](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options.go:250)

테스트/검증 근거: [internal/fieldvalue/options_test.go:35](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options_test.go:35) / [internal/query/field_options_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/field_options_test.go:11)

## FLD-07 · date field min/max

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: DateTime accepts PB's UTC space-separated millisecond layout and common cast-supported inputs, permits zero/empty optional values, rejects required zero and enforces min/max. It serializes UTC as YYYY-MM-DD HH:mm:ss.SSSZ or empty string for zero.

PgBase: timestamptz decoder and option bounds require RFC3339Nano; values are normalized to UTC and min/max are validated, including existing-row tightening. Encoder emits RFC3339Nano and optional null remains null.

차이/남은 범위: P2: PB's own date wire string (space between date/time), empty date, numeric timestamp/common-date coercion and required-zero handling are not matched. The previous row's claim that PB wire date formatting is applied is contradicted by the current encoder and canonical JSON test.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_date.go#L113-L146) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/types/datetime.go#L11-L12) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/types/datetime.go#L96-L110) / [소스 4](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/types/datetime.go#L129-L157)

PgBase 근거: [internal/query/codec.go:252](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:252) / [internal/query/codec.go:527](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:527) / [internal/schema/file_options.go:40](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/file_options.go:40) / [internal/schema/field_preflight.go:55](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/field_preflight.go:55)

테스트/검증 근거: [internal/query/codec_test.go:145](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:145) / [internal/query/codec_test.go:161](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:161) / [internal/query/codec_test.go:250](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:250) / [internal/schema/compatibility_integration_test.go:70](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/compatibility_integration_test.go:70)

## FLD-08 · single and multiple select values

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: MaxSelect<=1 selects the last normalized unique string; MaxSelect>1 emits a unique string array. Empty optional selection is allowed. Values and max count are checked, and changing MaxSelect switches scalar/multiple mode.

PgBase: text+allowedValues models scalar select and jsonb+allowedValues models multiple select with min/max validation. Direct writes require the exact chosen scalar/array shape and reject duplicates; modifier paths separately deduplicate.

차이/남은 범위: P2: PB's scalar/list input normalization, duplicate elimination and optional empty scalar are absent on direct writes. PG text/jsonb type-change prohibition prevents an in-place scalar↔multiple select switch that PB supports through MaxSelect.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_select.go#L136-L207) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_select.go#L214-L225)

PgBase 근거: [internal/query/codec.go:219](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:219) / [internal/query/codec.go:288](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:288) / [internal/fieldvalue/options.go:116](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options.go:116) / [internal/fieldvalue/options.go:149](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options.go:149) / [internal/recordops/modifiers.go:214](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/modifiers.go:214) / [internal/schema/type_changes.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/type_changes.go:12)

테스트/검증 근거: [internal/query/field_options_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/field_options_test.go:11) / [internal/fieldvalue/options_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options_test.go:11) / [internal/recordops/modifiers_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/modifiers_test.go:11)

## FLD-09 · JSON field required and maxSize

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: JSON strings containing JSON-looking content normalize to those JSON values; validation enforces a default 1 MiB byte maximum (also when MaxSize=0). Required rejects null, empty string/array/object.

PgBase: JSONB supports all JSON shapes, explicit byte MaxSize, PB-mode required blank predicate and tightening preflight; strict PostgreSQL-safe JSON validation rejects NUL, malformed Unicode and numbers outside PostgreSQL range.

차이/남은 범위: P2: No implicit JSON 1 MiB maximum when MaxSize is absent; explicit MaxSize=0 rejects nonempty bytes instead of selecting PB's default. JSON string "true" remains a string instead of becoming boolean true. JSONB representability differences are additional documented engine adaptations.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_json.go#L25-L25) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_json.go#L120-L194) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_json.go#L207-L214)

PgBase 근거: [internal/query/codec.go:288](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:288) / [internal/query/codec.go:319](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:319) / [internal/jsonvalue/json.go:117](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsonvalue/json.go:117) / [internal/schema/field_preflight.go:83](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/field_preflight.go:83)

테스트/검증 근거: [internal/query/codec_test.go:49](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:49) / [internal/query/codec_test.go:127](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:127) / [internal/query/codec_test.go:145](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:145) / [internal/query/codec_test.go:161](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec_test.go:161) / [internal/schema/compatibility_integration_test.go:70](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/compatibility_integration_test.go:70)

## FLD-10 · file count, size, MIME and thumbs

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: Single/multiple files enforce count, per-file byte size, MIME and required. Thumbnail generation auto-orients EXIF images, preserves GIF, writes WebP thumbnails as PNG, always allows 100x100 and falls back to original on unsupported/failed thumbnail.

PgBase: Metadata-backed file policy enforces count, size, MIME and required; supports configured bounded variants and PNG/JPEG/GIF/WebP decode. File cap is 100 MiB/100 items; non-PNG thumbnails are JPEG and image decode has no EXIF auto-orientation.

차이/남은 범위: P2: Missing EXIF auto-orientation and GIF/WebP alpha-preserving thumbnail encoding change image results. PB original fallback/default 100x100 behavior is not reproduced. Zero options and larger PB count/size configurations are rejected; PG also applies a global MIME allowlist. Schema accepts e.g. 5000x1 yet renderer caps dimensions at 4096.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_file.go#L226-L307) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L640-L691) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file.go#L178-L223)

PgBase 근거: [internal/schema/file_options.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/file_options.go:12) / [internal/schema/file_options.go:63](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/file_options.go:63) / [pgbase/file_services.go:152](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_services.go:152) / [internal/filestore/image.go:75](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/image.go:75) / [internal/filestore/variants.go:163](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants.go:163) / [internal/filestore/service.go:403](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:403)

테스트/검증 근거: [internal/schema/file_options_test.go:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/file_options_test.go:9) / [internal/filestore/variants_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants_test.go:15) / [internal/filestore/variants_test.go:28](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants_test.go:28) / [internal/filestore/variants_test.go:54](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants_test.go:54) / [pgbase/file_integration_test.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_integration_test.go:23)

## FLD-11 · per-field protected file toggle

**대응 구현·설계 차이** · P2 · executed-now · 기존 항목

PocketBase: File fields default public (Protected=false); protected delivery checks collection ViewRule and can restore the authenticated record from the supplied file token.

PgBase: Per-field protected/public metadata controls delivery and revokes existing public access when toggled. Native default is protected=true. Private delivery requires a current user/admin session plus a token bound to file, variant, user and expiry.

차이/남은 범위: P2 for PB wire compatibility: default visibility is reversed, and a PB-style protected-image URL with only a file token does not satisfy PG's additional session requirement. The toggle capability exists; stricter token/session authorization is an explicit delivery adaptation.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_file.go#L128-L135) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file.go#L113-L141)

PgBase 근거: [internal/schema/types.go:78](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/types.go:78) / [pgbase/file_services.go:165](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_services.go:165) / [internal/api/file_handlers.go:56](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:56) / [internal/api/file_handlers.go:211](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:211) / [internal/filestore/service.go:563](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:563)

테스트/검증 근거: [internal/filestore/policy_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/policy_test.go:11) / [internal/filestore/local_test.go:73](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/local_test.go:73) / [pgbase/file_integration_test.go:131](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_integration_test.go:131) / [pgbase/file_integration_test.go:191](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_integration_test.go:191)

## FLD-12 · relation cardinality, bounds and delete action

**보완 필요** · P1 · executed-now · 기존 항목

PocketBase: Relations normalize unique string IDs and last-value scalar selection, permit optional empty even with MinSelect>0, and support CascadeDelete. On target deletion, cascade deletes the source only when no linked IDs remain; otherwise it removes only the deleted link.

PgBase: Single relations use FK and multiple relations ordered edges with transactional graph deletion. Bounds cap at 1000 and require Required==(MinSelect>0). onDelete offers restrict/cascade/set_null; cascade places any source referencing a deleted target in the deletion closure.

차이/남은 범위: P1: source->[A,B], deleting A with cascade deletes source in PG, while PB retains source->[B]; mapping CascadeDelete to cascade can therefore over-delete. P2: optional+positive-min schemas are rejected, duplicate/single-array/empty-string normalization differs, and PG's default restrict differs from PB noncascade link removal.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_relation.go#L169-L214) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_model.go#L1613-L1643)

PgBase 근거: [internal/schema/relation_types.go:130](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/relation_types.go:130) / [internal/schema/relation_types.go:193](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/relation_types.go:193) / [internal/schema/relation_types.go:207](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/relation_types.go:207) / [internal/relation/codec.go:66](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/relation/codec.go:66) / [internal/relation/delete.go:339](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/relation/delete.go:339)

테스트/검증 근거: [internal/relation/codec_test.go:89](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/relation/codec_test.go:89) / [internal/schema/relation_types_test.go:234](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/relation_types_test.go:234) / [internal/relation/delete_live_test.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/relation/delete_live_test.go:18)

## FLD-13 · password field

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: Password field hides plaintext, stores bcrypt, validates required/min/max/pattern and per-field Cost, and exposes trusted hash getter/raw-hash assignment for existing hashes.

PgBase: General collection password fields are write-only; preparePassword validates then hashes with fixed Argon2id and verify helper. Responses/artifacts omit secrets. Default max length is 71, but explicit greater max is allowed.

차이/남은 범위: P2: No per-field bcrypt Cost option or verification/import of PB bcrypt hashes. Submitted hash text is treated as plaintext and rehashed, so PB password-field data cannot be migrated losslessly through this interface. Argon2id is an intentional algorithm adaptation, but cost/raw-hash contract remains absent.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_password.go#L89-L94) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_password.go#L146-L239) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_password.go#L287-L307)

PgBase 근거: [internal/schema/types.go:51](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/types.go:51) / [internal/schema/file_options.go:108](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/file_options.go:108) / [internal/recordops/generated_fields.go:158](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields.go:158) / [internal/passwordfield/password.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/passwordfield/password.go:16)

테스트/검증 근거: [internal/recordops/generated_fields_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields_test.go:13) / [internal/recordops/generated_fields_test.go:35](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields_test.go:35) / [internal/recordops/generated_fields_integration_test.go:150](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields_integration_test.go:150) / [internal/recordops/generated_fields_integration_test.go:231](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields_integration_test.go:231)

## FLD-14 · autodate field

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: At least OnCreate or OnUpdate is enabled; record.Set(field,value) is a no-op; configured create/update interceptors set the current DateTime. Trusted SetRaw can override the automatic date.

PgBase: Read-only autodate fields use statement_timestamp() on configured create/update operations; schema requires at least one flag.

차이/남은 범위: P2: Supplying an autodate value causes field_read_only and rejects the mutation instead of PB's no-op; timestamp wire/default semantics also differ (FLD-07). No evidence of PB's trusted SetRaw override seam for this field.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_autodate.go#L123-L150) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_autodate.go#L153-L205)

PgBase 근거: [internal/recordops/generated_fields.go:51](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields.go:51) / [internal/recordops/generated_fields.go:70](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields.go:70) / [internal/schema/file_options.go:87](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/file_options.go:87)

테스트/검증 근거: [internal/recordops/generated_fields_integration_test.go:150](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields_integration_test.go:150) / [internal/recordops/generated_fields_integration_test.go:231](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields_integration_test.go:231) / [internal/schema/advanced_fields_test.go:8](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/advanced_fields_test.go:8)

## FLD-15 · editor field

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: Editor casts input to HTML string; default empty value, required nonempty, 5 MiB default byte maximum, configurable MaxSize up to max-safe JSON integer and ConvertURLs presentation option.

PgBase: Editor is nonnull text with default empty, required-empty rejection, 5 MiB default/per-field byte maximum and ConvertURLs metadata. Schema limits MaxSize to the overall 8 MiB record budget.

차이/남은 범위: P2: Core string validation matches for normal inputs, but PB cast-to-string inputs (including null→empty) are rejected by PG's strict string decoder. Configured PB editor values above PG's 8 MiB record budget cannot be represented; 1:1 equivalence is therefore not established.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_editor.go#L115-L166) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_editor.go#L55-L71)

PgBase 근거: [internal/query/codec.go:219](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:219) / [internal/query/codec.go:456](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:456) / [internal/schema/planner.go:607](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:607) / [internal/schema/file_options.go:96](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/file_options.go:96) / [internal/schema/types.go:72](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/types.go:72) / [internal/schema/record_size.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/record_size.go:18)

테스트/검증 근거: [internal/query/advanced_fields_test.go:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/advanced_fields_test.go:26) / [internal/schema/advanced_fields_integration_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/advanced_fields_integration_test.go:13) / [internal/schema/field_contract_integration_test.go:85](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/field_contract_integration_test.go:85)

## FLD-16 · geoPoint field

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: GeoPoint is normalized through types.GeoPoint.Scan into lon/lat with zero defaults. PB tests nil, empty string and empty object→{lon:0,lat:0}; coordinate bounds and required Null Island rejection apply.

PgBase: GeoPoint JSONB defaults to zero point, requires an object with exactly lon and lat keys, rejects nonfinite/out-of-range coordinates and required zero point; query filter/sort support exists.

차이/남은 범위: P2: PB accepts empty and partial objects through zero-filled struct normalization; PG requires exactly two keys (test explicitly rejects {lon:1}) and rejects nil/empty input. The previous statement that PB also requires an exact two-key input object was inaccurate.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_geo_point.go#L109-L143) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/types/geo_point.go#L40-L83) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_geo_point_test.go#L31-L50)

PgBase 근거: [internal/query/codec.go:463](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:463) / [internal/schema/planner.go:610](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:610)

테스트/검증 근거: [internal/query/advanced_fields_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/advanced_fields_test.go:12) / [internal/query/advanced_fields_integration_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/advanced_fields_integration_test.go:15) / [internal/schema/advanced_fields_integration_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/advanced_fields_integration_test.go:13)

## FLD-17 · text :autogenerate write modifier

**미구현** · P2 · static-only · 추가 확인 항목

PocketBase: fieldName:autogenerate casts the supplied prefix to string and appends a generated value when AutogeneratePattern is configured; this setter can be used explicitly during create/update.

PgBase: Write modifier parser recognizes only prefix + and suffix +/-; unknown field:autogenerate is rejected before typed write. Existing omitted-on-create generation does not expose the modifier.

차이/남은 범위: P2: Explicit PB text regeneration/prefix generation modifier is absent and was omitted from the earlier feature denominator. Add adapter behavior and vectors if PB mutation compatibility is promised.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_text.go#L376-L393) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_text_test.go#L675-L720)

PgBase 근거: [internal/recordops/modifiers.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/modifiers.go:24) / [internal/recordops/modifiers.go:55](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/modifiers.go:55) / [internal/recordops/generated_fields.go:42](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields.go:42)

테스트/검증 근거: [internal/recordops/modifiers_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/modifiers_test.go:11) / [internal/recordops/generated_fields_integration_test.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields_integration_test.go:24)

## REC-01 · 레코드 목록 조회

**대응 구현·설계 차이** · P3 · executed-now · 기존 항목

PocketBase: 통합 /api/collections/{collection}/records에서 이름 또는 collection ID를 받아 기본 page/perPage/totalItems/totalPages/items 응답.

PgBase: 권한별 /api/v1 경로에 cursor/limit 기본 목록과 명시적 PocketBase offset adapter가 있다.

차이/남은 범위: compat=pocketbase와 page/perPage/skipTotal 중 하나를 함께 지정해야 PB envelope가 활성화된다. compat만으로는 cursor envelope를 유지한다. PB 원래 경로와 collection ID selector는 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L27)

PgBase 근거: [internal/api/router.go:155](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/router.go:155) / [internal/api/record_handlers.go:173](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers.go:173) / [internal/recordops/read.go:68](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/read.go:68)

테스트/검증 근거: [internal/api/record_handlers_test.go:103](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers_test.go:103)

선별 단위 테스트 실행 PASS; PB 대조 HTTP 실행 아님.

## REC-02 · 단일 레코드 조회

**대응 구현·설계 차이** · P3 · executed-now · 기존 항목

PocketBase: 이름 또는 collection ID와 record ID로 단일 조회하고 fields/expand를 적용한다.

PgBase: UUID 또는 컬렉션별 text public ID를 정확한 대소문자로 resolve하고 fields/expand, ETag를 반환한다.

차이/남은 범위: 과거 문서의 UUID-only 설명은 틀렸다. text ID는 구현되었으나 PB 원래 경로·collection ID selector·기본 PB metadata envelope는 그대로 호환되지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L144)

PgBase 근거: [internal/api/record_handlers.go:217](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers.go:217) / [internal/recordops/read.go:104](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/read.go:104)

테스트/검증 근거: [internal/api/record_handlers_test.go:58](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers_test.go:58) / [internal/recordops/text_identity_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/text_identity_integration_test.go:16)

CRUD handler 단위 실행 PASS; text ID 통합 테스트는 존재 확인, 이번 감사에서 재실행 안 함.

## REC-03 · 레코드 생성(JSON/multipart)

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: 동일 POST record endpoint에서 JSON·multipart와 file modifiers를 받아 200+record를 반환한다.

PgBase: POST create JSON 및 UUID/text ID, 감사·revision·outbox·선택적 idempotency가 구현되었다. 파일 포함 원자 생성은 별도 native multipart batch로 제공한다.

차이/남은 범위: 일반 create는 application/json만 수용하고 multipart를 거절한다. PB 200과 달리 Pg는201+Location/ETag를 반환한다. PB CRUD alias가 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L209)

PgBase 근거: [internal/api/record_handlers.go:264](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers.go:264) / [internal/api/record_handlers.go:332](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers.go:332) / [internal/api/admin_handlers.go:364](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/admin_handlers.go:364) / [internal/recordops/service.go:75](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/service.go:75)

테스트/검증 근거: [internal/api/record_handlers_test.go:58](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers_test.go:58) / [pgbase/batch_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/batch_integration_test.go:17) / [internal/recordops/service_integration_test.go:142](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/service_integration_test.go:142)

JSON CRUD handler 단위 실행 PASS; 파일·idempotency 실제 DB 테스트는 존재 확인.

## REC-04 · 레코드 부분 갱신과 빈 PATCH

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: PATCH nil body 또는 {} 모두200이며 lifecycle hooks를 거친다.

PgBase: {} patch는 compiler와 mutation pipeline에서 허용한다. HTTP input decoder는 JSON object를 요구한다.

차이/남은 범위: body가 전혀 없으면 strict JSON decode EOF로400, JSON null은 input==nil로400. 기존 REC-04 완료 표시는 HTTP nil-body 계약까지 증명하지 못했다. 직접 multipart PATCH도 미지원.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L392)

PgBase 근거: [internal/api/record_handlers.go:335](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers.go:335) / [internal/api/record_handlers.go:582](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers.go:582) / [internal/api/admin_handlers.go:383](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/admin_handlers.go:383) / [internal/query/compiler.go:89](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/compiler.go:89)

테스트/검증 근거: [internal/query/compiler_test.go:126](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/compiler_test.go:126) / [internal/api/record_handlers_test.go:467](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers_test.go:467)

{} compiler 단위 실행 PASS; body 없음 mismatch는 decoder와 PB 명시적 테스트의 소스 대조.

## REC-05 · 레코드 삭제

**대응 구현·설계 차이** · P3 · executed-now · 기존 항목

PocketBase: DELETE 성공204, 권한 및 relation 삭제 처리.

PgBase: DELETE 성공204, optional If-Match/ETag와 relation 처리.

차이/남은 범위: 핵심 삭제 능력은 대응하지만 권한별 URL·ID 정책·에러 envelope가 다르다. 조건부 삭제는 Pg의 추가 기능.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L531)

PgBase 근거: [internal/api/record_handlers.go:431](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers.go:431) / [internal/api/record_handlers.go:470](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers.go:470)

테스트/검증 근거: [internal/api/record_handlers_test.go:58](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers_test.go:58) / [pgbase/batch_integration_test.go:270](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/batch_integration_test.go:270)

CRUD handler 단위 실행 PASS; 실제 DB delete batch 테스트는 존재 확인.

## REC-06 · 뷰 컬렉션 레코드 읽기 전용

**좁은 계약 일치** · none · test-exists · 기존 항목

PocketBase: View는 읽을 수 있고 create/update/delete를 거절한다.

PgBase: SQL/builder view 조회를 제공하고 모든 mutation kernel에서 view guard를 적용한다.

차이/남은 범위: 이 행의 좁은 사용자 능력은 대응한다. 응답 오류 코드·경로까지 동일하다는 뜻은 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L216)

PgBase 근거: [internal/recordops/service.go:126](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/service.go:126) / [internal/recordops/service.go:336](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/service.go:336) / [internal/recordops/service.go:502](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/service.go:502)

테스트/검증 근거: [pgbase/sql_view_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/sql_view_integration_test.go:12)

소스 대조 및 통합 테스트 존재 확인; 이번 감사에서는 view 통합 재실행 안 함.

## REC-07 · 레코드 JSON metadata·가시성

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: PublicExport는 visible fields와 collectionId/collectionName/expand를 반환하고 DateTime은 공백 구분 millisecond UTC 형식이다.

PgBase: 기본 native created_at/updated_at, opt-in 읽기에서 PB alias collectionId/collectionName/created/updated를 추가하고 hidden/email visibility를 적용한다.

차이/남은 범위: compat projection은 native keys를 보존한 shallow copy이며 RFC3339Nano 값을 단순 alias한다. PB 기본 JSON과 date 문자열이 완전히 같지 않다. create/update는 PocketBaseCompatibility flag를 서비스에 전달하지 않는다. 숨긴 입력도 PB는 비관리자 입력에서 제거하지만 Pg는 hidden_field로거절한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_model.go#L1267)

PgBase 근거: [internal/responseprojection/record.go:5](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/responseprojection/record.go:5) / [internal/query/codec.go:408](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:408) / [internal/api/record_handlers.go:295](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers.go:295) / [internal/api/record_handlers.go:373](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers.go:373) / [internal/recordops/field_visibility.go:10](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/field_visibility.go:10)

테스트/검증 근거: [internal/responseprojection/record_test.go:5](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/responseprojection/record_test.go:5) / [internal/recordops/field_visibility_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/field_visibility_test.go:12) / [pgbase/field_visibility_integration_test.go:10](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/field_visibility_integration_test.go:10)

projection·hidden input 단위 실행 PASS; serializer 동일성은 미충족을 소스로 확인.

## REC-08 · 순서 보장 원자적 batch

**좁은 계약 일치** · none · test-exists · 기존 항목

PocketBase: 입력 순서 CRUD를 단일 transaction에서 실행하고 실패 시 전체 rollback.

PgBase: PostgreSQL transaction 내부 ordered loop, create/update/delete/upsert와 file participant·검증·감사/outbox가 함께 commit/rollback한다.

차이/남은 범위: 원자성의 핵심 능력은 대응. PB hook middleware recursion 구조와 wire 계약 동일성은 별도 행에서 다룬다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/batch.go#L180)

PgBase 근거: [internal/recordops/batch.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/batch.go:23) / [internal/recordops/batch.go:53](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/batch.go:53) / [internal/recordops/batch.go:86](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/batch.go:86)

테스트/검증 근거: [internal/recordops/batch_integration_test.go:56](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/batch_integration_test.go:56) / [internal/recordops/batch_integration_test.go:110](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/batch_integration_test.go:110) / [pgbase/batch_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/batch_integration_test.go:17)

소스 및 실제 DB 회귀 테스트 존재 확인; 이번 감사에서 DB 재실행 안 함.

## REC-09 · PocketBase batch 요청·응답 wire

**미구현** · P1 · static-only · 기존 항목

PocketBase: POST /api/batch; requests의 /api/collections/{nameOrId}/records[/{id}][?query]; 최상위 [{body,status}] 배열.

PgBase: Native batch는 구현되었다. /api/v1/{...}/collections/batch, requests 또는 operations; {results:[{status,headers,body,eventId,etag,version}]} 구조.

차이/남은 범위: PB batch wire adapter 자체가 없다. Pg URL regexp는 /api/(v1|admin)/collections/{name}/records 경로만, per-operation ?fields/?expand도거절. 원래 PB SDK batch 요청을 그대로 처리할 수 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/batch.go#L26)

PgBase 근거: [internal/api/router.go:194](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/router.go:194) / [internal/api/batch_handlers.go:57](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/batch_handlers.go:57) / [internal/api/batch_handlers.go:204](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/batch_handlers.go:204)

테스트/검증 근거: [pgbase/batch_integration_test.go:205](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/batch_integration_test.go:205) / [pgbase/batch_integration_test.go:270](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/batch_integration_test.go:270)

서로 다른 parser/router/serializer 소스 확인. PB wire adapter의 통합 테스트를 발견하지 못함.

## REC-10 · PUT upsert와 조건부 갱신

**대응 구현·설계 차이** · P3 · test-exists · 기존 항목

PocketBase: batch PUT /records body.id가 있으면 조회 후update, 없거나 미존재면create.

PgBase: batch PUT /records/{id} 또는 typed upsert의 ID를 resolve하여 create/update; absent If-Match 요청은 update 경로로 실패 처리하여 부활 방지.

차이/남은 범위: Native upsert는 구현되었지만 PB의 body.id/ID 생략 생성 wire와 다르다. Pg는 URL id필수이고 optional If-Match는 추가 계약이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/batch.go#L40)

PgBase 근거: [internal/api/batch_handlers.go:475](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/batch_handlers.go:475) / [internal/recordops/batch.go:150](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/batch.go:150) / [internal/recordops/batch.go:175](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/batch.go:175)

테스트/검증 근거: [internal/recordops/batch_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/batch_integration_test.go:17) / [internal/recordops/batch_integration_test.go:34](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/batch_integration_test.go:34) / [pgbase/batch_integration_test.go:205](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/batch_integration_test.go:205)

구현 및 regression 테스트 존재 확인; 이번 감사에서 DB 실행 안 함.

## REC-11 · multipart batch와 파일 참조

**대응 구현·설계 차이** · P3 · test-exists · 기존 항목

PocketBase: @jsonPayload에 요청 JSON, requests.N.fileField 또는 requests[N].fileField의 multipart 파일 업로드.

PgBase: requests라는 text part에 batch envelope, 별도 명명 file part와 body의 {$file:name} 참조. file participant와 원자 rollback.

차이/남은 범위: 파일 포함 batch 기능은 있지만 multipart part 이름/참조 방식과 결과 envelope가 달라 PB multipart batch는 직접 미호환.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/batch.go#L91)

PgBase 근거: [internal/api/batch_handlers.go:364](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/batch_handlers.go:364) / [internal/api/batch_handlers.go:393](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/batch_handlers.go:393) / [internal/api/batch_file_modifiers.go:200](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/batch_file_modifiers.go:200)

테스트/검증 근거: [pgbase/batch_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/batch_integration_test.go:17)

소스 및 실제 파일/DB rollback 테스트 존재 확인; 이번 감사에서 통합 실행 안 함.

## REC-12 · batch enable·한도·timeout

**대응 구현·설계 차이** · P3 · test-exists · 기존 항목

PocketBase: Batch enabled/maxRequests/timeout(seconds)/maxBodySize 설정으로 admission·transaction 제한.

PgBase: persisted runtime enabled/maxRequests/maxBodyBytes/timeoutMs, HTTP+kernel에서 제한. 1~100 requests,100~300000ms validation.

차이/남은 범위: 능력은 대응하지만 설정명·단위·기본값·상한이 다르다. Disabled의 PB403 대비 Pg503 차이도 있다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/batch.go#L95)

PgBase 근거: [internal/runtimesettings/settings.go:25](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/runtimesettings/settings.go:25) / [internal/runtimesettings/settings.go:95](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/runtimesettings/settings.go:95) / [internal/api/batch_handlers.go:73](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/batch_handlers.go:73) / [internal/recordops/batch.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/batch.go:17)

테스트/검증 근거: [pgbase/runtime_settings_http_integration_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/runtime_settings_http_integration_test.go:11)

소스 및 settings HTTP/DB regression 존재 확인; 이번 감사에서 통합 실행 안 함.

## REC-13 · 숫자 증가·감소 modifier

**대응 구현·설계 차이** · P3 · executed-now · 기존 항목

PocketBase: number field+/-는 cast.ToFloat64를 이용한 덧셈·뺄셈.

PgBase: integer/bigint/numeric에 +/-를 적용하고 PostgreSQL row lock 아래 stored preimage에서 계산한다. bigint/numeric 입력은 문자열이고 rational 계산한다.

차이/남은 범위: 더 엄격하고 정밀한 native 숫자 정책이다. PB의 float/coercion 허용과 Pg typed/string 입력은 동일하지 않다. 동시 equal-length modifier key 순서도 PB는 길이만 정렬, Pg는 길이+lexical이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_number.go#L211)

PgBase 근거: [internal/recordops/modifiers.go:46](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/modifiers.go:46) / [internal/recordops/modifiers.go:126](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/modifiers.go:126) / [internal/recordops/service.go:352](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/service.go:352)

테스트/검증 근거: [internal/recordops/modifiers_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/modifiers_test.go:11) / [internal/recordops/modifiers_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/modifiers_integration_test.go:17)

modifier 단위 실행 PASS; concurrent PostgreSQL 테스트는 존재 확인.

## REC-14 · select·relation·file prepend/append/remove modifier

**대응 구현·설계 차이** · P3 · executed-now · 기존 항목

PocketBase: +field/field+/field-를 지원하고 단일 선택은 정규화한 마지막 값을 사용. mixed same-length modifier의 순서는 Go map iteration 이후 길이만 stable sort하여 보장되지 않는다.

PgBase: select/relation은 ordered dedupe와 마지막 단일값, file은 native batch 참가자에 prepend/append/remove를 제공한다. direct assign 먼저, 같은길이는 lexical 순서로처리.

차이/남은 범위: 개별 연산 능력은 구현. PB 동일길이 modifier tie순서와 완전히 동등하다고 주장할 수 없고, 파일 modifier HTTP 입구는 PB direct multipart가 아닌 native batch이다. 각 modifier 입력의 PB coercion과 Pg string-only validation도 차이가 있다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_select.go#L229)

PgBase 근거: [internal/recordops/modifiers.go:46](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/modifiers.go:46) / [internal/recordops/modifiers.go:223](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/modifiers.go:223) / [internal/api/batch_file_modifiers.go:173](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/batch_file_modifiers.go:173)

테스트/검증 근거: [internal/recordops/modifiers_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/modifiers_test.go:11) / [internal/recordops/modifiers_integration_test.go:57](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/modifiers_integration_test.go:57) / [pgbase/batch_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/batch_integration_test.go:17)

select/direct/mixed modifier 단위 실행 PASS; relation/file DB 테스트는 존재 확인.

## QRY-01 · 불리언 문법과 괄호

**대응 구현·설계 차이** · P3 · test-exists · 기존 항목

PocketBase: &&, ||, 괄호; public search는 3500 bytes/200 expressions 제한.

PgBase: 동일 우선순위 파서와 parameterized SQL. 4096 bytes/100 AST nodes의 별도 한도.

차이/남은 범위: 문법의 핵심 능력은 대응한다. PB보다 큰 byte 한도/더 작은 node 한도라 허용하는 긴 식의 집합은 다르다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/filter.go#L119)

PgBase 근거: [internal/query/filter.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/filter.go:14) / [internal/query/filter.go:93](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/filter.go:93) / [internal/query/filter.go:126](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/filter.go:126)

테스트/검증 근거: [internal/query/compiler_test.go:478](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/compiler_test.go:478)

## QRY-02 · 스칼라 비교 연산자와 null 의미론

**보완 필요** · P1 · executed-now · 기존 항목

PocketBase: =, !=, <, <=, >, >=. text null/빈문자열을 정규화하고 !=도 NULL에 안전한 IS NOT을 사용.

PgBase: 여섯 연산자를 SQL로 컴파일하지만 compat에서도 일반 text = null은 IS NULL, !=는 PostgreSQL !=.

차이/남은 범위: title=null에 빈 문자열이 매칭되지 않고 nullable field != nonempty에서 NULL이 제외된다. PB의 text null/empty 및 NULL-safe inequality와 불일치.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/filter.go#L340)

PgBase 근거: [internal/query/relation_filter_sort.go:619](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:619) / [internal/query/relation_filter_sort.go:644](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:644)

테스트/검증 근거: [internal/query/compiler_test.go:225](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/compiler_test.go:225) / [docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:52](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:52)

## QRY-03 · contains / not-contains

**보완 필요** · P1 · executed-now · 기존 항목

PocketBase: ~ / !~는 LIKE이며 unescaped %가 없으면 escape 후 %로 감싼다.

PgBase: compat는 LIKE/NOT LIKE, native는 strpos. escaped %도 단순 strings.Contains로 wildcard로 간주.

차이/남은 범위: title ~ 'a\%b'를 PB처럼 부분 문자열 패턴으로 감싸지 않는다. public SQL LIKE와 별도 in-memory scalarContains 구현도 다르다. SQLite/PG LIKE의 기본 case 처리 역시 동등성 검증이 필요하다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/filter.go#L496)

PgBase 근거: [internal/query/relation_filter_sort.go:666](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:666) / [internal/query/scalar_predicate.go:286](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/scalar_predicate.go:286)

테스트/검증 근거: [internal/query/pocketbase_compat_live_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_compat_live_test.go:15) / [docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:60](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:60)

## QRY-04 · any-match 비교 연산자

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: ?=, ?!=, ?~, ?!~, ?<, ?<=, ?>, ?>=; MultiValuer :each 및 relation 다중값을 any/all 구분해 평가.

PgBase: 연산자 토큰은 모두 인식하고 scalar·relation EXISTS 일부 지원. public query 경로는 JSONB/file terminal을 거절하고 scalar select로 저장되는 JSONB 배열도 일반 :each 평가가 없다.

차이/남은 범위: 기존 테스트는 title/large_count 같은 scalar의 토큰 컴파일 중심이다. PB select/file/JSON multi value와 일반 relation comparison의 all-match 대 any-match 의미론까지 완성됐다고 할 수 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/filter.go#L185)

PgBase 근거: [internal/query/filter.go:299](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/filter.go:299) / [internal/query/relation_filter_sort.go:84](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:84) / [internal/query/relation_filter_sort.go:619](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:619)

테스트/검증 근거: [internal/query/pocketbase_filter_completion_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_filter_completion_test.go:11) / [internal/query/relation_codec_test.go:123](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_codec_test.go:123)

## QRY-05 · 피연산자 배치·field-to-field 비교

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: literal/literal, literal/field, field/field, function operands를 resolver로 해석.

PgBase: 같은 타입의 root field 비교와 literal-left 비교 구현. parser는 literal/literal을 거절하며 양쪽 임의 함수나 relation field 조합은 제한.

차이/남은 범위: PB 테스트가 허용하는 2 < 5도 거절한다. root field=relation.field 조합 및 function-on-right 등 피연산자 조합을 PB oracle로 확대 검증해야 한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/filter.go#L167)

PgBase 근거: [internal/query/filter.go:170](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/filter.go:170) / [internal/query/relation_filter_sort.go:114](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:114) / [internal/query/relation_filter_sort.go:696](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:696)

테스트/검증 근거: [internal/query/pocketbase_compat_live_test.go:32](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_compat_live_test.go:32) / [docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:17)

## QRY-06 · 문자열·숫자·불리언·null literal

**보완 필요** · P1 · executed-now · 기존 항목

PocketBase: fexpr가 single/double quote 문자열과 escape, number/bool/null을 해석하고 field resolver가 null을 정규화.

PgBase: single quote parser와 compat double quote 정규화, 엄격 typed literal decoder.

차이/남은 범위: title = 'a "b" c'와 backslash apostrophe 문자열을 거절한다. double quote 정규화가 single-quoted literal 내부까지 바꾸는 문제가 있다. 날짜는 RFC3339 timezone을 요구해 PB 날짜 문자열/coercion과도 다르다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/filter.go#L273)

PgBase 근거: [internal/query/filter.go:226](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/filter.go:226) / [internal/query/filter.go:310](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/filter.go:310) / [internal/query/service.go:261](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/service.go:261) / [internal/query/compiler.go:394](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/compiler.go:394)

테스트/검증 근거: [internal/query/pocketbase_page_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_page_test.go:11) / [docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:16)

## QRY-07 · 정방향 relation filter

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: relation 경로를 최대 6단계 따라가고 다중 relation의 multi-match 의미론을 적용.

PgBase: forward relation을 EXISTS로 계획하며 depth3/joins8, target 정책을 적용.

차이/남은 범위: PB가 허용하는 4~6 hop은 거절한다. 일반 relation field 비교가 기본 EXISTS로 컴파일되어 PB multi-match 제약과 세부 결과 차이가 남는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver_runner.go#L441)

PgBase 근거: [internal/query/relation_filter_sort.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:14) / [internal/query/relation_filter_sort.go:127](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:127) / [internal/query/relation_filter_sort.go:733](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:733)

테스트/검증 근거: [internal/query/relation_filter_sort_test.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort_test.go:18) / [internal/query/relation_filter_sort_test.go:50](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort_test.go:50)

## QRY-08 · JSON 객체 속성·배열 index filter

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: jsonField.key/index의 JSON_EXTRACT 결과를 비교; JSON null/타입에 맞는 비교.

PgBase: root/relation terminal JSON path와 index를 jsonb #>>로 추출하고 비교 literal 타입으로 numeric/bool cast.

차이/남은 범위: 잘 형성된 동종 scalar path는 지원하지만 mixed JSON 값에 무조건 ::numeric/::boolean cast하므로 PB JSON 비교와 다르다. path-to-path 비교·heterogeneous 값/배열에 대한 public HTTP differential 검증이 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver_runner.go#L461)

PgBase 근거: [internal/query/relation_filter_sort.go:282](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:282) / [internal/query/relation_filter_sort.go:602](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:602)

테스트/검증 근거: [internal/query/pocketbase_filter_completion_test.go:33](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_filter_completion_test.go:33) / [internal/recordaccess/pocketbase_conformance_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/pocketbase_conformance_integration_test.go:1)

## QRY-09 · 역방향 relation filter

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: collection name 또는 id의 _via_ 경로와 relation resolver 6 hop.

PgBase: catalog.Incoming에서 source.Name_via_field를 해석하고 correlation/target 정책 적용.

차이/남은 범위: 역방향 filter 자체는 구현됐다. PB collection ID 기반 _via_ selector, 4~6 hop 및 전체 multi-match 조합은 제공되지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver_runner.go#L492)

PgBase 근거: [internal/query/relation_filter_sort.go:255](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:255) / [internal/query/relation_filter_sort.go:733](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:733)

테스트/검증 근거: [internal/query/relation_filter_sort_test.go:77](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort_test.go:77) / [internal/query/relation_filter_sort_live_test.go:19](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort_live_test.go:19)

## QRY-10 · lower·length·each·isset·changed modifier

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: 문자열 lower, MultiValuer length/each, request body isset/changed를 문맥별 지원.

PgBase: rule AST/compiler에 request modifiers; public list compiler는 text/editor lower/length와 relation each 일부를 별도로 처리.

차이/남은 범위: rule compiler가 풍부해도 public filter 경로와 같지 않다. select/file JSON 배열 length/each는 public list에서 지원되지 않는다. 두 compiler의 modifier/case/coercion 일치 검증 필요.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver_runner.go#L736)

PgBase 근거: [internal/query/relation_filter_sort.go:632](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:632) / [internal/query/access_rule.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/access_rule.go:14) / [internal/accessrule/pocketbase_resolver.go:434](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_resolver.go:434)

테스트/검증 근거: [internal/query/n4_modifiers_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/n4_modifiers_test.go:12) / [internal/query/access_rule_test.go:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/access_rule_test.go:26) / [internal/accessrule/pocketbase_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_test.go:1)

## QRY-11 · geoDistance·strftime 함수

**보완 필요** · P1 · executed-now · 기존 항목

PocketBase: geoDistance 네 인수, strftime 1~10 인수/SQLite format+time-value+modifiers; relation resolver 입력도 지원.

PgBase: root geoPoint Haversine/제한된 strftime to_char. accessrule의 strftime은 일부 modifiers도 지원하지만 public query compiler는 3번째 인수부터 거절.

차이/남은 범위: strftime('…',date,'start of year') public filter 거절. %w를 D로 바꾸어 weekday 기준이 0→1 달라진다. rule %s도 epoch 아닌 SSSS로 치환한다. 모든 SQLite substitutions/time-value/modifiers 및 relation args는 미완성.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/token_functions.go#L64)

PgBase 근거: [internal/query/compiler.go:315](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/compiler.go:315) / [internal/query/compiler.go:363](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/compiler.go:363) / [internal/accessrule/pocketbase_resolver.go:326](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_resolver.go:326) / [internal/accessrule/pocketbase_resolver.go:391](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_resolver.go:391)

테스트/검증 근거: [internal/query/pocketbase_filter_completion_test.go:53](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_filter_completion_test.go:53) / [docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:36](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:36)

## QRY-12 · 시간 macro와 @request/@collection 식별자

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: UTC @now/day/month/year macros. @request/@collection는 rule에서, HTTP 사용자 filter에서는 superuser에 한해 허용.

PgBase: UTC macros와 rule resolver의 request/collection 경로 구현. public list는 @request를 parser에서 거절하고 @collection은 parse 뒤 relation resolver에서 거절.

차이/남은 범위: 시간 macro 부분과 rule 특수값은 구현됐다. PB superuser가 쓸 수 있는 public list @request/@collection filter까지는 연결되지 않았다. PB는 일반 사용자에게도 허용한다는 뜻이 아님.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/identifier_macros.go#L16)

PgBase 근거: [internal/query/filter.go:372](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/filter.go:372) / [internal/query/access_rule.go:35](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/access_rule.go:35) / [internal/accessrule/pocketbase_resolver.go:420](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_resolver.go:420) / [internal/query/relation_filter_sort.go:247](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:247)

테스트/검증 근거: [internal/query/pocketbase_rule_conformance_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_rule_conformance_test.go:16) / [docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:19](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:19)

## QRY-13 · 다중 스칼라 sort·안정 tie-break

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: 최대 8 sort fields, whitespace trim, resolver가 허용한 unindexed 필드도 sort 가능.

PgBase: 8개/중복 거절/id tie-break. root 일반 컬렉션은 indexed scalar만 허용.

차이/남은 범위: PB가 허용하는 unindexed text field sort를 unindexed_field로 거절한다. 입력 whitespace/중복 처리와 기본 ordering도 다르다. PG 성능 정책을 유지할지 compat에서 완화할지 결정 필요.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/sort.go#L25)

PgBase 근거: [internal/query/filter.go:426](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/filter.go:426) / [internal/query/compiler.go:206](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/compiler.go:206)

테스트/검증 근거: [internal/query/compiler_test.go:225](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/compiler_test.go:225) / [docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:69](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:69)

## QRY-14 · relation sort

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: forward/back relation을 resolver로 정렬하며 relation depth는6.

PgBase: forward/multi/reverse relation SQL sort와 stable cursor, depth2 및 indexed terminal/100000 root-row cap.

차이/남은 범위: 핵심 relation sort는 있으나 PB가 허용하는 3~6 hop, unindexed terminal, 일부 JSON/다중값 sort는 제한된다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/sort.go#L37)

PgBase 근거: [internal/query/relation_filter_sort.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:16) / [internal/query/relation_filter_sort.go:167](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:167) / [internal/query/relation_filter_sort.go:210](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:210)

테스트/검증 근거: [internal/query/relation_filter_sort_test.go:164](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort_test.go:164) / [internal/query/pocketbase_compat_live_test.go:42](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_compat_live_test.go:42)

## QRY-15 · @random·@rowid sort

**대응 구현·설계 차이** · P3 · test-exists · 기존 항목

PocketBase: @random은 RANDOM(), @rowid는 SQLite physical _rowid_.

PgBase: @random은 PostgreSQL random(), @rowid는 durable internal UUID adapter.

차이/남은 범위: 의도적인 엔진 차이다. @rowid가 SQLite 정수 삽입 순서와 같다는 보장은 없다. text public ID lexical 순서도 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/sort.go#L25)

PgBase 근거: [internal/query/filter.go:444](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/filter.go:444) / [internal/query/relation_filter_sort.go:170](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:170) / [internal/query/record_identity_sql.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/record_identity_sql.go:1)

테스트/검증 근거: [internal/query/pocketbase_page_test.go:25](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_page_test.go:25) / [internal/query/pocketbase_page_integration_test.go:68](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_page_integration_test.go:68) / [internal/query/text_identity_integration_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/text_identity_integration_test.go:15)

## QRY-16 · page 번호 기반 임의 페이지 이동

**대응 구현·설계 차이** · P3 · test-exists · 기존 항목

PocketBase: page/perPage, default1/30/max1000; offset로 임의 페이지 조회.

PgBase: 명시적 compat page adapter, 동일 default/max/offset, overflow 거절.

차이/남은 범위: 페이지 기능은 구현. /api/v1/...에서 compat=pocketbase와 page/perPage/skipTotal 중 하나를 지정해야 활성화되며 PB 기본 route/drop-in은 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/provider.go#L282)

PgBase 근거: [internal/api/record_handlers.go:173](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers.go:173) / [internal/query/service.go:82](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/service.go:82) / [internal/query/service.go:126](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/service.go:126)

테스트/검증 근거: [internal/api/record_handlers_test.go:103](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers_test.go:103) / [internal/query/pocketbase_page_integration_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_page_integration_test.go:15)

## QRY-17 · totalItems·totalPages·skipTotal

**좁은 계약 일치** · none · test-exists · 기존 항목

PocketBase: total count/page 메타데이터; skipTotal이면 -1/-1로 count 생략.

PgBase: PB adapter 안에서 동일 권한 필터와 repeatable-read snapshot으로 exact count, skipTotal에서 count 생략.

차이/남은 범위: 활성화된 PB page adapter의 이 좁은 계약은 대응한다. PB 전체 route·serializer 동일성을 의미하지 않으며 latest upstream을 상대로 이번 감사에서 DB differential 재실행은 하지 않았다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/provider.go#L294)

PgBase 근거: [internal/query/service.go:107](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/service.go:107) / [internal/query/expand.go:384](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/expand.go:384)

테스트/검증 근거: [internal/query/pocketbase_page_integration_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_page_integration_test.go:15) / [internal/query/pocketbase_page_integration_test.go:68](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_page_integration_test.go:68)

## QRY-18 · fields 응답 projection

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: 전체 성공 JSON envelope에 wildcard/dot path/excerpt를 적용; missing field는 조용히 생략.

PgBase: 레코드 projection, nested wildcard/array/excerpt 구현. schema root validation 및 자체 regex HTML stripper.

차이/남은 범위: fields=missing은 PB의 {} 대신 오류. list envelope의 items.id/page 선택과 record-field 선택을 동일하게 지원하지 않는다. inline HTML 'Hello<b>world</b>'가 'Hello world'로 바뀌어 PB DOM text 처리와 다르다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/router/event.go#L191)

PgBase 근거: [internal/query/projection.go:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/projection.go:26) / [internal/responseprojection/projection.go:40](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/responseprojection/projection.go:40) / [internal/responseprojection/projection.go:207](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/responseprojection/projection.go:207)

테스트/검증 근거: [internal/query/projection_test.go:47](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/projection_test.go:47) / [docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:74](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:74)

## QRY-19 · 정방향·중첩 expand

**대응 구현·설계 차이** · P3 · test-exists · 기존 항목

PocketBase: comma relation path를 최대6단계 확장하고 대상 view 권한을 적용.

PgBase: forward/sibling/nested expand depth6, breadth-first hydration, target 권한, nodes20/rows10000/query64/response bytes budget.

차이/남은 범위: 주요 기능은 구현됐고 PB보다 추가적인 자원 한도가 있다. budget 초과/empty expand 응답 차이는 QRY-21에서 따로 분류한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_query_expand.go#L54)

PgBase 근거: [internal/query/expand_plan.go:35](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/expand_plan.go:35) / [internal/query/expand_types.go:88](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/expand_types.go:88) / [internal/query/expand.go:140](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/expand.go:140)

테스트/검증 근거: [internal/query/expand_nested_live_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/expand_nested_live_test.go:16) / [internal/query/expand_nested_live_test.go:209](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/expand_nested_live_test.go:209)

## QRY-20 · 역방향 _via_ expand

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: collection name/id _via_field 및 deprecated collection(field) syntax를 확장; root당 최대1000개, unique 관계면 단일값.

PgBase: catalog source.Name_via_field를 reverse node로 확장, job-wide reverse row budget1000.

차이/남은 범위: 현행 name_via_field 핵심은 구현됐다. collection ID selector/아직 PB가 받는 legacy syntax는 없고, PB root당 limit와 Pg job-wide 예산·에러 처리도 다르다. PB는 unique back-relation을 단일 object로 만들지만 Pg executeReverseExpandNode는 항상 plural:true 배열을 붙인다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_query_expand.go#L75)

PgBase 근거: [internal/query/expand_plan.go:191](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/expand_plan.go:191) / [internal/query/expand_types.go:90](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/expand_types.go:90) / [internal/query/expand_reverse.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/expand_reverse.go:1)

테스트/검증 근거: [internal/query/expand_nested_live_test.go:88](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/expand_nested_live_test.go:88) / [internal/query/expand_nested_live_test.go:158](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/expand_nested_live_test.go:158)

## QRY-21 · expand JSON·invalid path 처리

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: invalid path 오류는 비치명이며 성공 응답 계속; depth6 초과는 잘라내고, 유효 관계 expand 요청은 대상이 없어도 expand:{} 키를 초기화.

PgBase: compat에서 missing/invalid path skip. budget/depth 오류는 그대로 반환하고 empty Expand map은 JSON 키를 생략.

차이/남은 범위: PB는 7단계 요청을6단계까지 처리하지만 Pg는 expand_budget_exceeded로 전체 오류. 정상 empty 관계를 요청한 경우 PB expand:{}와 Pg key omission이 다르다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_query_expand.go#L65)

PgBase 근거: [internal/query/expand_plan.go:95](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/expand_plan.go:95) / [internal/query/expand_types.go:63](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/expand_types.go:63)

테스트/검증 근거: [internal/query/pocketbase_compat_live_test.go:52](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_compat_live_test.go:52) / [docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:86](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:86)

## QRY-22 · 숨김 필드의 모든 query 피연산자 검증 (추가 감사 항목)

**보완 필요** · P1 · executed-now · 추가 확인 항목

PocketBase: resolver는 양쪽 operands를 각각 해석하고 non-superuser의 hidden field를 거절.

PgBase: non-admin query guard와 relation plan guard가 주 field/rootField/path를 검사하지만 otherField/otherRootField와 function argument를 빠뜨린다.

차이/남은 범위: 실제 unit repro에서 non-admin title = secret(Hidden:true)이 두 guard를 모두 통과하고 SQL에 secret을 포함했다. 응답 자체에서 hidden을 제거해도 필터 결과/건수로 추론될 여지가 있어 우선 수정 필요. DB/HTTP 공격은 실행하지 않았으며 원격 누출 확정으로 과장하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver_runner.go#L750)

PgBase 근거: [internal/query/field_visibility.go:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/field_visibility.go:9) / [internal/query/field_visibility.go:47](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/field_visibility.go:47) / [internal/query/relation_filter_sort.go:498](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/relation_filter_sort.go:498) / [internal/query/expand.go:245](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/expand.go:245)

테스트/검증 근거: [internal/query/field_visibility_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/field_visibility_test.go:1) / [docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:100](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/query-observations_test.go.txt:100)

## AUTH-01 · superuser/administrator

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: _superusers는 시스템 auth collection이다. 공통 record CRUD/인증과 collection auth 설정을 쓰며 JWT와 전역 SuperuserIPs 정책을 사용한다.

PgBase: 별도 admins/admin_sessions 모델, 일회 setup token, Argon2id 비밀번호, 서버 저장 opaque cookie/bearer session, CSRF와 관리자별 CIDR 허용목록을 제공한다.

차이/남은 범위: 관리자 기능은 존재하지만 _superusers record CRUD/collection OAuth·OTP·MFA 설정 모델이나 정적 superuser JWT를 대체하지 않는다. 별도 관리자 모델은 의도적 구조 차이이며 PB API 1:1은 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/migrations/1640988000_init.go#L291) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L44) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_tokens.go#L48)

PgBase 근거: [internal/adminauth/service.go:79](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/service.go:79) / [internal/adminauth/service.go:266](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/service.go:266) / [internal/adminauth/service.go:395](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/service.go:395) / [internal/adminauth/mfa.go:134](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/mfa.go:134)

테스트/검증 근거: [internal/adminauth/service_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/service_integration_test.go:17) / [internal/adminauth/mfa_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/mfa_integration_test.go:16)

## AUTH-02 · administrator MFA

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: _superusers도 일반 auth MFA 흐름을 사용한다. 첫 password/OAuth2/OTP 인증 후 mfaId를 발급하고 다른 인증 방식으로 동일 사용자임을 확인한다.

PgBase: 관리자 password login은 등록된 TOTP를 함께 검증한다. 관리자 email OTP는 CLI 발급 코드의 별도 로그인 경로다.

차이/남은 범위: PB의 mfaId와 두 로그인 방식 조합, 조건부 _superusers MFA 설정이 없다. CLI-issued OTP 로그인은 TOTP를 호출하지 않는 별도 관리 경로로, 무권한 원격 우회가 확인된 것은 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L182) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_password_test.go#L392)

PgBase 근거: [internal/adminauth/service.go:273](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/service.go:273) / [internal/adminauth/mfa.go:92](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/mfa.go:92) / [internal/adminauth/email_otp.go:82](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/email_otp.go:82)

테스트/검증 근거: [internal/adminauth/mfa_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/mfa_integration_test.go:16) / [internal/adminauth/email_otp_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/email_otp_integration_test.go:12)

## AUTH-03 · named auth collection/realm

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: 서로 독립적인 auth collection에 record와 collection별 인증 정책을 둔다.

PgBase: auth_collections별 사용자 identity/session/policy를 분리한다. metadata users+profile 저장과 collection 공개 record ID 정책으로 구현한다.

차이/남은 범위: PostgreSQL 공유 metadata 모델과 UUID 내부 identity가 PB collection table 모델을 대체한다. 이름별 격리는 테스트가 있으나 이름/ID 라우팅 차이는 AUTH-13에 별도 기록한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L100) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth.go#L73)

PgBase 근거: [internal/userauth/collections.go:158](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/collections.go:158) / [internal/userauth/collections.go:305](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/collections.go:305) / [internal/userauth/service.go:246](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service.go:246)

테스트/검증 근거: [internal/userauth/collections_integration_test.go:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/collections_integration_test.go:26) / [internal/schema/auth_record_id_document_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/auth_record_id_document_integration_test.go:16)

## AUTH-04 · arbitrary auth record fields

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: Auth record는 일반 collection field 타입·제약·인덱스·relation/file을 가진다. OAuth createData와 일반 CRUD도 동일 record validation을 따른다.

PgBase: 최대64 custom typed fields를 JSON profile에 저장하며 scalar/JSON/geo/relation/file/password 등을 검증하고 CRUD·규칙을 적용한다.

차이/남은 범위: custom field ID, Unique, Default를 거부하며 auth field 타입·옵션 모델이 PB와 같지 않다. 공통 record index/field 변경 모델이 아니며 public 가입 payload에 custom fields를 전달할 수 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L100) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_oauth2.go#L260)

PgBase 근거: [internal/userauth/auth_record_model.go:25](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_record_model.go:25) / [internal/userauth/auth_records.go:311](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_records.go:311) / [internal/userauth/auth_records.go:390](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_records.go:390)

테스트/검증 근거: [internal/userauth/auth_record_model_integration_test.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_record_model_integration_test.go:22) / [internal/userauth/auth_record_model_integration_test.go:328](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_record_model_integration_test.go:328) / [internal/userauth/auth_record_model_integration_test.go:365](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_record_model_integration_test.go:365)

## AUTH-05 · self registration

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: 일반 POST collection records가 auth 가입이다. createRule와 전체 record field validation, password/passwordConfirm 및 custom data를 적용하고 생성 record를 반환한다.

PgBase: 별도 /register가 email/password만 받아 registrationEnabled와 createRule를 검사하고 사용자 및 session을 생성한다.

차이/남은 범위: custom fields/명시적 공개 id/passwordConfirm/emailVisibility 등 PB 가입 record payload를 지원하지 않는다. 가입과 로그인 session 발급이 결합되고 OAuth 가입의 createRule 경로는 AUTH-34에 별도 기록한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L209) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/record_upsert.go#L16)

PgBase 근거: [internal/api/auth_collections.go:330](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/auth_collections.go:330) / [internal/userauth/service.go:42](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service.go:42) / [internal/userauth/service.go:306](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service.go:306)

테스트/검증 근거: [internal/userauth/collections_integration_test.go:62](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/collections_integration_test.go:62) / [internal/userauth/auth_record_model_integration_test.go:94](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_record_model_integration_test.go:94)

## AUTH-06 · password identity fields

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: 설정된 identityFields에서 검색하며 요청 identityField로 특정 필드를 선택할 수 있다. single-column unique index의 NOCASE collation을 존중한다.

PgBase: email 및 선언된 visible text profile field를 순서대로 검색하고 identity uniqueness를 service에서 검증한다.

차이/남은 범위: 요청별 identityField 선택이 없고 text profile 비교는 고정 exact comparison이다. PB unique-index/collation 계약과 hidden field 허용 범위를 그대로 제공하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_password.go#L115) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_password_test.go#L267) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_password_test.go#L561)

PgBase 근거: [internal/userauth/service.go:209](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service.go:209) / [internal/userauth/auth_records.go:494](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_records.go:494) / [internal/userauth/auth_admin.go:176](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:176)

테스트/검증 근거: [internal/userauth/identity_policy_schema_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/identity_policy_schema_integration_test.go:16) / [internal/userauth/auth_admin_integration_test.go:69](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin_integration_test.go:69)

## AUTH-07 · 비밀번호 최소 길이 정책

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: PasswordField.Min은 문자(rune) 수 기준이고 설정 범위는 1..71이다.

PgBase: collection minimumPasswordBytes를 12..128 bytes로 제한하고 자격 증명 작성·로그인에 적용한다.

차이/남은 범위: 짧은 비밀번호를 허용하는 PB 정책을 표현할 수 없으며 다중바이트 문자열의 길이 의미가 다르다. 강한 기본 정책과 호환 범위 차이를 구별해야 한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_password.go#L179) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_password.go#L211)

PgBase 근거: [internal/userauth/auth_admin.go:176](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:176) / [internal/userauth/service.go:235](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service.go:235) / [internal/userauth/service.go:663](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service.go:663)

테스트/검증 근거: [internal/userauth/auth_admin_integration_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin_integration_test.go:15) / [internal/adminauth/password_test.go:42](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/password_test.go:42)

## AUTH-08 · password login

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: identity/password 검증 후 authRule·MFA를 적용하고 JWT와 record를 반환한다. 알려지지 않은 identity에도 비밀번호 검증을 수행한다.

PgBase: 계정·네트워크 제한, dummy Argon2id 검증, 잠긴 최신 사용자 상태 재확인 후 authRule·MFA와 opaque session 발급을 적용한다.

차이/남은 범위: 기본 비밀번호 로그인 기능은 구현되어 있지만 wire 응답(user/expiresAt), Argon2id, DB session 방식은 의도적으로 다르다. identity 선택·정책·MFA 범위는 AUTH-06/07/18/30에 따로 기록한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_password.go#L15) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L35)

PgBase 근거: [internal/userauth/service.go:120](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service.go:120) / [internal/userauth/service.go:330](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service.go:330) / [internal/adminauth/password.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/password.go:22)

테스트/검증 근거: [internal/userauth/service_integration_test.go:69](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service_integration_test.go:69) / [internal/userauth/auth_fix_regression_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_fix_regression_integration_test.go:17) / [internal/userauth/auth_rule_enforcement_integration_test.go:28](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_rule_enforcement_integration_test.go:28)

## AUTH-09 · session refresh

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: refreshable JWT는 새 JWT를 발급하되 이전 JWT를 개별 폐기하지 않는다. static token은 그대로 반환하고 authRule를 재검사한다.

PgBase: 일반 session row의 token hash를 원자적으로 교체하며 이전 bearer는 즉시 무효화된다. 같은 session ID를 유지하며 sliding TTL을 연장하고 authRule를 재검사한다.

차이/남은 범위: refresh 기능은 stateful rotation으로 의도적으로 변경되었다. 이전 token 재시도와 병렬 refresh가 PB처럼 동작하지 않으며 impersonation refresh는 거절한다(AUTH-26).

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_refresh.go#L9) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_tokens.go#L48) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_refresh_test.go#L87)

PgBase 근거: [internal/userauth/session_refresh.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/session_refresh.go:15)

테스트/검증 근거: [internal/userauth/auth_methods_integration_test.go:49](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_methods_integration_test.go:49)

## AUTH-10 · logout and revoke

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: auth endpoint 목록에 session logout/revoke가 없다. JWT는 record tokenKey/collection secret/expiry에 묶이며 key 변경으로 기존 token들을 무효화한다.

PgBase: logout은 해당 session을 revoke하고 관리자/사용자 lifecycle은 session 목록 전체 폐기와 credential 변경 시 폐기를 지원한다.

차이/남은 범위: 명시적 서버 session revoke는 PB의 stateless 토큰 모델보다 추가된 기능이다. PB tokenKey와 session 단위 폐기는 같은 단위가 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth.go#L10) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_tokens.go#L48) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_query.go#L483)

PgBase 근거: [internal/userauth/service.go:286](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service.go:286) / [internal/userauth/lifecycle.go:86](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/lifecycle.go:86) / [internal/adminauth/service.go:417](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/service.go:417)

테스트/검증 근거: [internal/userauth/service_integration_test.go:33](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service_integration_test.go:33) / [internal/userauth/lifecycle_integration_test.go:79](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/lifecycle_integration_test.go:79) / [internal/userauth/lifecycle_integration_test.go:209](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/lifecycle_integration_test.go:209)

## AUTH-11 · auth methods

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: password enabled/identityFields, OTP·MFA enabled/duration, OAuth displayName/logo/state/authURL/PKCE 값과 legacy 필드를 공개한다.

PgBase: public methods에 password/OTP/MFA enabled, identityFields 및 provider name/startEndpoint를 제공하고 민감 설정을 숨긴다.

차이/남은 범위: OTP/MFA duration과 provider 표시명/logo가 없다. authURL·state·PKCE는 별도 시작 endpoint 구조로 바뀌며 PB auth-methods 응답을 그대로 제공하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_methods.go#L13) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_methods.go#L83)

PgBase 근거: [internal/userauth/auth_methods.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_methods.go:15) / [internal/userauth/auth_admin.go:19](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:19)

테스트/검증 근거: [internal/userauth/auth_methods_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_methods_integration_test.go:16) / [internal/api/auth_collections_methods_test.go:66](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/auth_collections_methods_test.go:66)

## AUTH-12 · provider catalog/config

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: collection OAuth2.Providers는 공식 provider factories와 custom endpoints/scopes/PKCE/extra 설정, mappedFields를 가진다.

PgBase: realm별 encrypted client secret, endpoints/scopes/redirect allowlist/claim names/issuer/JWKS 설정을 저장한다. HTTP exchange와 여러 provider profile normalizer가 있다.

차이/남은 범위: PB provider factory 설정 전체를 구현한 것이 아니다. PKCE를 항상 보내고 PB provider별 extra/PKCE toggle/mappedFields와 일부 token/userInfo 교환 특성은 없다. 명칭이 존재하는 normalizer만으로 실제 provider login parity를 입증할 수 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L407) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L527) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/auth/auth.go#L16)

PgBase 근거: [internal/userauth/auth_admin.go:56](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:56) / [internal/userauth/oauth_http.go:46](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_http.go:46) / [internal/userauth/oauth_profiles.go:20](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_profiles.go:20)

테스트/검증 근거: [internal/userauth/auth_admin_integration_test.go:236](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin_integration_test.go:236) / [internal/userauth/oauth_http_test.go:47](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_http_test.go:47) / [internal/userauth/oauth_profiles_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_profiles_test.go:12)

## AUTH-13 · realm routing

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: collection 경로는 auth collection 이름 또는 ID를 받으며 refresh는 같은 collection의 auth만 허용한다.

PgBase: 공개 /api/v1/auth/{authCollection} 경로는 validated collection name으로 찾고 session의 collection을 확인한다. OAuth state에도 realm을 저장한다.

차이/남은 범위: 이름 경로의 격리는 구현됐지만 PB name-or-ID 라우팅과 동일하지 않다. UUID collection ID를 public realm 경로로 사용할 수 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth.go#L73)

PgBase 근거: [internal/api/auth_collections.go:327](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/auth_collections.go:327) / [internal/userauth/collections.go:305](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/collections.go:305) / [internal/userauth/oauth.go:251](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth.go:251)

테스트/검증 근거: [internal/userauth/collections_integration_test.go:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/collections_integration_test.go:26) / [internal/userauth/collections_integration_test.go:100](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/collections_integration_test.go:100) / [internal/userauth/auth_methods_integration_test.go:49](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_methods_integration_test.go:49)

## AUTH-14 · signup and link

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: 기존 provider 관계, 로그인한 동일 collection 사용자, provider email의 기존 사용자 순서로 연결한다. createData로 신규 record를 만들고 검증 상태에 따라 password/external links 보호 처리를 한다.

PgBase: provider+subject+realm에 묶은 stateful PKCE 흐름, 신규 OAuth 사용자 생성, 현재 비밀번호에 묶인 명시적 link를 구현한다. 같은 email의 기존 사용자는 충돌로 처리한다.

차이/남은 범위: 이메일 기반 자동 연결, 로그인 session만으로 OAuth-only 사용자 추가 link, createData 및 기존 사용자 empty-email/verified 동기화가 PB와 다르다. 명시적 연결 정책 자체는 의도적인 보안 선택이나 PB 전체 사용 흐름은 누락된다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_oauth2.go#L118) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_oauth2.go#L260)

PgBase 근거: [internal/userauth/oauth.go:105](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth.go:105) / [internal/userauth/oauth.go:262](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth.go:262) / [internal/userauth/oauth.go:350](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth.go:350)

테스트/검증 근거: [internal/userauth/collections_integration_test.go:100](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/collections_integration_test.go:100) / [internal/userauth/oauth_empty_email_integration_test.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_empty_email_integration_test.go:14) / [internal/userauth/oauth_apple_integration_test.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_apple_integration_test.go:22)

## AUTH-15 · OAuth unlink

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: _externalAuths record list/view/delete 규칙에 따라 자신의 연결을 조회·삭제할 수 있으며 일반 system-record CRUD를 사용한다.

PgBase: 현재 사용자 provider 목록과 unlink, 관리자 auxiliary 삭제를 제공하며 계정 row lock과 마지막 로그인 수단 보존 검사를 적용한다.

차이/남은 범위: 마지막 OAuth 로그인 수단 삭제를 거부하는 추가 정책이 있고 PB _externalAuths 범용 CRUD/filter 응답 계약은 없다. PB의 연결 record providerId/collectionRef/recordRef 정보도 축약된다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/external_auth_model.go#L18) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud_external_auth_test.go#L12) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/migrations/1640988000_init.go#L20)

PgBase 근거: [internal/userauth/oauth.go:113](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth.go:113) / [internal/userauth/oauth.go:175](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth.go:175) / [internal/userauth/auth_auxiliary.go:136](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_auxiliary.go:136)

테스트/검증 근거: [internal/userauth/auth_admin_integration_test.go:98](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin_integration_test.go:98) / [internal/userauth/auth_rule_enforcement_integration_test.go:176](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_rule_enforcement_integration_test.go:176)

## AUTH-16 · profile mapping/avatar

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: OAuth mappedFields로 provider id/name/username/avatarURL을 custom field에 넣고 createData가 우선한다. avatar 대상이 file이면 내려받아 일반 record file로 저장한다.

PgBase: provider별 subject/verified email/avatar URL 정규화와 신규 가입 시 제한된 avatar download/storage를 제공한다.

차이/남은 범위: name/username/id를 임의 collection field로 매핑하거나 createData로 덮어쓰는 기능이 없다. avatar는 별도 blob 저장(2MiB,4096px)이며 일반 auth file field로 매핑되지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_oauth2.go#L285) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/auth/auth.go#L142)

PgBase 근거: [internal/userauth/oauth_profiles.go:20](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_profiles.go:20) / [internal/userauth/oauth.go:436](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth.go:436) / [internal/userauth/oauth_avatar.go:32](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_avatar.go:32)

테스트/검증 근거: [internal/userauth/oauth_profiles_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_profiles_test.go:12) / [internal/userauth/oauth_profiles_test.go:40](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_profiles_test.go:40) / [internal/userauth/oauth_avatar_test.go:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_avatar_test.go:9)

## AUTH-17 · email OTP login

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: 가변 길이 OTP(default8), 10..86400초 TTL, 실패 횟수 제한, 단회 소모를 제공한다. 성공 시 보낸 주소와 현재 email이 같으면 verified로 전환하며 MFA 비활성 시 password를 무작위화한다.

PgBase: 고정6자리 HMAC OTP, 신규 발급 시 이전 코드 consume, realm 정책 TTL/최대시도, 잠금·단회 소모 및 등록된 TOTP challenge를 제공한다.

차이/남은 범위: 코드 길이·TTL·재발급 의미가 다르며 OTP 성공 경로에는 PB의 verified 갱신/조건부 password 무작위화가 보이지 않는다. 코드가 특정 email snapshot에 묶여 있지 않은 점도 별도 회귀 검증이 필요하다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_otp_request.go#L16) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_otp.go#L15) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L314)

PgBase 근거: [internal/userauth/email_otp.go:27](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_otp.go:27) / [internal/userauth/email_otp.go:113](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_otp.go:113) / [internal/userauth/auth_admin.go:176](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:176)

테스트/검증 근거: [internal/userauth/email_otp_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_otp_integration_test.go:16) / [internal/userauth/email_otp_integration_test.go:147](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_otp_integration_test.go:147) / [internal/userauth/email_otp_integration_test.go:180](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_otp_integration_test.go:180)

## AUTH-18 · MFA challenge completion

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: password/OAuth2/OTP 중 서로 다른 두 authMethod를 동일 collection/record에서 성공하면 mfaId를 소비하고 token을 발급한다. TOTP enrollment가 전제되지 않는다.

PgBase: 비밀번호/OAuth/OTP 첫 단계 후 활성 TOTP factor가 있을 때 challenge를 발급하고 TOTP 또는 recovery code로 완료한다. challenge/코드 재사용을 막는다.

차이/남은 범위: TOTP/recovery 방식은 구현되어 있으나 PB password+email OTP, OAuth+password 등 조합은 없다. MFAEnabled여도 활성 factor가 없는 사용자에게 challenge가 생기지 않는 코드 계약이며 이를 미등록 사용자에 대한 PB식 강제 MFA와 혼동하면 안 된다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L182) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers_test.go#L467)

PgBase 근거: [internal/userauth/mfa.go:53](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/mfa.go:53) / [internal/userauth/mfa.go:220](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/mfa.go:220) / [internal/userauth/mfa.go:353](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/mfa.go:353)

테스트/검증 근거: [internal/userauth/mfa_integration_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/mfa_integration_test.go:15) / [internal/userauth/auth_rule_enforcement_integration_test.go:144](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_rule_enforcement_integration_test.go:144)

## AUTH-19 · MFA conditional policy

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: MFA.Rule은 full record filter이며 requestInfo·record·relation을 평가한다. 비어 있으면 전체 사용자에게 MFA를 요구한다.

PgBase: 빈 규칙 또는 {field,equals} JSON scalar equality만 허용하고 verified/custom profile field를 비교한다. 활성 factor가 있을 때만 평가한다.

차이/남은 범위: PB filter 문법, 복합식/관계/request 조건을 지원하지 않는다. 빈 규칙의 의미도 등록 factor 조건에 의해 더 좁다. 현재 테스트는 full PB 조건 정책을 검증하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L145) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L348)

PgBase 근거: [internal/userauth/mfa.go:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/mfa.go:26) / [internal/userauth/mfa.go:53](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/mfa.go:53)

테스트/검증 근거: [internal/userauth/auth_admin_integration_test.go:69](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin_integration_test.go:69) / [internal/userauth/mfa_integration_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/mfa_integration_test.go:15)

## AUTH-20 · password reset request

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: Public POST /api/collections/{collection}/request-password-reset accepts a validated email only when collection PasswordAuth.Enabled is true. Unknown accounts and recently sent requests receive 204; successful sends establish an in-memory two-minute resend suppression. Delivery runs in the background.

PgBase: Public default/named-realm request-password-reset endpoints normalize email, apply account/network buckets, issue a hashed opaque action token, attempt mail synchronously, and suppress unknown-account/delivery failure disclosure.

차이/남은 범위: Collection path and error/throttling behavior differ. The reviewed PgBase request path has no collection PasswordEnabled check; unlike PB it can issue a reset while password authentication is disabled. Invalid email is nondisclosing success rather than PB validation error. PB's two-minute resend suppression and asynchronous delivery are absent from this path.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_request.go#L16) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_request.go#L41)

PgBase 근거: [internal/api/email_actions.go:28](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/email_actions.go:28) / [internal/api/auth_collections.go:68](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/auth_collections.go:68) / [internal/userauth/email_actions.go:93](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:93) / [internal/userauth/service.go:408](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service.go:408)

테스트/검증 근거: [internal/userauth/email_actions_integration_test.go:219](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions_integration_test.go:219) / [internal/userauth/email_actions_integration_test.go:287](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions_integration_test.go:287) / [pgbase/email_actions_integration_test.go:126](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/email_actions_integration_test.go:126) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_request_test.go#L45)

## AUTH-21 · password reset confirmation

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: Public collection-scoped confirmation requires token, password and matching passwordConfirm; validates token purpose and collection, saves the new password, and marks an unverified record verified only if its current email still matches the token email claim. Saving password rotates the record token key.

PgBase: Confirmation accepts token/newPassword, validates the realm password policy, consumes a purpose-bound, expiring, password-hash-bound database token once, updates password_hash and revokes sessions.

차이/남은 범위: PgBase does not set verified=true after a successful reset, so PB workflows that use password reset as email proof remain unverified. It has no passwordConfirm field or collection-scoped confirm URL, and opaque database tokens are not PB JWTs.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_confirm.go#L39) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_confirm.go#L65) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_model.go#L1443)

PgBase 근거: [internal/api/email_actions.go:42](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/email_actions.go:42) / [internal/userauth/email_actions.go:137](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:137) / [internal/userauth/email_actions.go:341](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:341)

테스트/검증 근거: [internal/userauth/email_actions_integration_test.go:219](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions_integration_test.go:219) / [pgbase/email_actions_integration_test.go:129](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/email_actions_integration_test.go:129) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_confirm_test.go#L102)

## AUTH-22 · verification request/confirm

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: Public collection-scoped request accepts email, validates it, conceals missing accounts with 204, suppresses repeat sends for two minutes and skips already-verified records; public confirmation requires a collection-bound verification token, checks its email claim, and returns 204. Superuser verification routes reject because superusers are already verified.

PgBase: Request requires an authenticated non-impersonated user session; public confirmation consumes an opaque single-use token and sets that user verified. Already verified users do not receive another verification mail. Native action page supports explicit submit.

차이/남은 범위: No public email-address verification-request contract or collection-scoped confirmation path. Reusing a consumed token fails, while PB accepts a still-valid verification token for an already-verified record. Additional verification trust-transition and email-binding checks are isolated in AUTH-35/36 below; their PgBase consequences are static observations, not executed reproductions.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_request.go#L16) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_request.go#L34) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm.go#L83)

PgBase 근거: [internal/api/email_actions.go:20](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/email_actions.go:20) / [internal/api/email_actions.go:54](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/email_actions.go:54) / [internal/userauth/email_actions.go:55](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:55)

테스트/검증 근거: [internal/userauth/email_actions_integration_test.go:144](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions_integration_test.go:144) / [internal/userauth/email_actions_integration_test.go:177](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions_integration_test.go:177) / [pgbase/email_actions_integration_test.go:117](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/email_actions_integration_test.go:117) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm_test.go#L90)

## AUTH-23 · email change request/confirm

**대응 구현·설계 차이** · P2 · test-exists · 기존 항목

PocketBase: Authenticated record requests a unique different newEmail without a password; public confirmation requires token plus current password, verifies token collection and newEmail, then saves the new email and verified=true. Email changes rotate the record token key.

PgBase: Authenticated non-impersonated user supplies currentPassword and newEmail at request time; the new email receives a purpose-bound, password-hash-bound token. Public token-only confirmation updates email/verified and revokes sessions.

차이/남은 범위: Password proof is moved from confirmation to request, so PB request/confirmation payloads and user flow cannot be used unchanged. PgBase checks collisions with other users but does not reject requesting the user's same current email as PB does. Confirm endpoints and token formats differ.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_email_change_request.go#L22) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_email_change_request.go#L61) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_email_change_confirm.go#L63) / [소스 4](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_model.go#L1451)

PgBase 근거: [internal/api/email_actions.go:54](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/email_actions.go:54) / [internal/userauth/email_actions.go:173](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:173) / [internal/userauth/email_actions.go:289](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:289)

테스트/검증 근거: [internal/userauth/email_actions_integration_test.go:258](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions_integration_test.go:258) / [pgbase/email_actions_integration_test.go:133](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/email_actions_integration_test.go:133) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_email_change_confirm_test.go#L12)

## AUTH-24 · verified/active account management

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: PB record CRUD restricts direct verified/email changes to superusers or callers allowed by a nonempty manageRule, in addition to normal record access. A matching manageRule is evaluated against any authenticated record; PB's built-in auth fields do not include a separate active boolean.

PgBase: PgBase provides administrator verified/active/session-revoke operations and typed auth-record CRUD. Sensitive user-driven updates require both update rule and a nonempty manage rule evaluated with distinct actor/target; tests reject a manager from another realm. Native active=false revokes sessions.

차이/남은 범위: The old report's claim that delegated manageRule was absent is obsolete. Current PgBase has it, but same-realm actor policy is narrower than PB's authenticated-record rule context; native active/session administration is an adaptation rather than a PB built-in active-field equivalent.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/record_upsert.go#L148) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L729) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L108) / [소스 4](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model.go#L941)

PgBase 근거: [internal/userauth/auth_admin.go:428](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:428) / [internal/userauth/lifecycle.go:51](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/lifecycle.go:51) / [internal/userauth/auth_records.go:144](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_records.go:144) / [internal/userauth/managed_authorization.go:65](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/managed_authorization.go:65)

테스트/검증 근거: [internal/userauth/auth_record_model_integration_test.go:184](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_record_model_integration_test.go:184) / [pgbase/user_management_integration_test.go:25](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/user_management_integration_test.go:25)

## AUTH-25 · new-device auth alert/auth origins

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: When collection AuthAlert.Enabled and a real auth method is present, PB fingerprints real IP plus truncated User-Agent, skips alert on first origin, alerts subsequent new origins, records or updates origin only after the helper succeeds, and keeps up to five origins. Alert delivery failure is logged without failing authentication.

PgBase: Successful auth flows record an HMAC fingerprint of network address, atomically deduplicate concurrent origins, update last-seen, and send best-effort mail when an auth_alert template exists. User auxiliary list/get/delete routes and administrator extension queries exist.

차이/남은 범위: Same-IP devices with different User-Agents are not distinguished. Template presence replaces PB's enabled flag/default template. The reviewed insertion path has no first-origin suppression or five-origin pruning, records an origin even when mail fails, and exposes a different auxiliary record/route schema. The old statement that users cannot list origins is obsolete.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L121) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L587) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L56)

PgBase 근거: [internal/userauth/auth_auxiliary.go:38](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_auxiliary.go:38) / [internal/userauth/auth_auxiliary.go:55](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_auxiliary.go:55) / [internal/userauth/email_actions.go:430](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:430) / [internal/userauth/service.go:109](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service.go:109) / [internal/api/auth_collections.go:77](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/auth_collections.go:77)

테스트/검증 근거: [internal/userauth/email_actions_integration_test.go:63](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions_integration_test.go:63) / [internal/userauth/auth_record_model_integration_test.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_record_model_integration_test.go:22) / [pgbase/extension_app_auth_exact_integration_test.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_auth_exact_integration_test.go:23)

## AUTH-26 · impersonation

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: superuser가 대상 auth record의 nonrefreshable static JWT를 발급한다. duration>=0이며0은 collection 기본값, 별도 짧은 최대값이 없고 관리자 session에 종속되지 않는다.

PgBase: 1..15분 impersonation session을 발급하며 actor admin session과 target에 묶고 admin expiry/정책 TTL로 상한을 제한한다.

차이/남은 범위: 장기간 static token/0 duration 기본값/관리자 session 독립 사용과 refresh 반환 계약을 제공하지 않는다. actor-bound 짧은 세션은 의도적 정책이지만 PB impersonation 전체 범위와 같지 않다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_impersonate.go#L13) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_tokens.go#L39) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_refresh.go#L9)

PgBase 근거: [internal/userauth/impersonation.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/impersonation.go:16) / [internal/userauth/session_refresh.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/session_refresh.go:15)

테스트/검증 근거: [internal/userauth/impersonation_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/impersonation_integration_test.go:17) / [internal/userauth/impersonation_integration_test.go:92](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/impersonation_integration_test.go:92) / [internal/userauth/impersonation_integration_test.go:101](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/impersonation_integration_test.go:101)

## AUTH-27 · OAuth provider inventory helper

**보완 필요** · P2 · static-only · 기존 항목

PocketBase: meta/oauth2-providers는 등록된 모든 provider를 order/name으로 정렬하여 name, displayName, logo를 반환한다.

PgBase: helper는 google/github/microsoft 세 starter configuration의 endpoint와 scope를 items envelope로 반환한다.

차이/남은 범위: 공식 전체 provider inventory가 아니며 displayName/logo/order가 없다. 30여 profile normalizer가 있다는 사실과 helper inventory 범위를 구별해야 한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L217) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/auth/auth.go#L16)

PgBase 근거: [internal/userauth/auth_admin.go:77](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:77) / [internal/api/auth_collections.go:91](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/auth_collections.go:91)

테스트/검증 근거:

## AUTH-28 · Apple client-secret helper

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: 입력 clientId/teamId/keyId/PKCS8 privateKey/duration(1..15777000)으로 Apple audience ES256 JWT를 만들어 secret 필드로 반환한다.

PgBase: 동일 claim·ES256·최대 lifetime의 secret 생성 helper가 있다. Team/Key ID는10자리 영숫자, P256 PKCS8만 허용하며 비밀키를 저장하지 않는다.

차이/남은 범위: 핵심 secret 생성 기능은 구현됐다. endpoint/envelope와 stricter key/id validation은 의도적 차이다. 서명 검증 테스트가 있으나 Apple 외부 인증 서버에 실제 교환한 검증은 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/apple_client_secret_create.go#L18) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/settings.go#L122)

PgBase 근거: [internal/userauth/apple_client_secret.go:27](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/apple_client_secret.go:27) / [internal/api/auth_admin.go:62](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/auth_admin.go:62)

테스트/검증 근거: [internal/userauth/apple_client_secret_test.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/apple_client_secret_test.go:18) / [internal/userauth/apple_client_secret_test.go:61](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/apple_client_secret_test.go:61) / [internal/api/auth_apple_client_secret_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/auth_apple_client_secret_test.go:17)

## AUTH-29 · PB auth auxiliary system record handling

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: _externalAuths/_mfas/_otps/_authOrigins는 system collection record/proxy이며 공통 CRUD/filter/pagination과 record hooks를 사용한다. MFA record는 진행 중인 첫 인증 challenge다.

PgBase: 관리자·manage-rule 사용자용 auxiliary list/get/delete와 bounded helper query를 제공한다. secrets를 숨기고 PostgreSQL metadata row를 요약한다.

차이/남은 범위: mfas는 PB 진행 challenge 대신 TOTP enrollment factor를 가리킨다. 범용 system record CRUD/filter/pagination/필드·hook 모델과 동일하지 않으며 select summary로 제한된다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/external_auth_model.go#L18) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/mfa_model.go#L17) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/otp_model.go#L18) / [소스 4](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/auth_origin_model.go#L15)

PgBase 근거: [internal/userauth/auth_auxiliary.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_auxiliary.go:18) / [internal/userauth/auth_auxiliary.go:80](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_auxiliary.go:80) / [internal/userauth/auth_auxiliary.go:165](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_auxiliary.go:165)

테스트/검증 근거: [internal/userauth/auth_record_model_integration_test.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_record_model_integration_test.go:22) / [internal/userauth/auth_rule_enforcement_integration_test.go:176](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_rule_enforcement_integration_test.go:176)

## AUTH-30 · 사용자 암호의 최대 길이·pattern·hash cost 옵션

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: PasswordField Max(default71), Pattern, bcrypt Cost를 field 설정으로 제공하고 길이는 rune 단위다. bcrypt 입력 바이트는 잘라 사용한다.

PgBase: realm maximumPasswordBytes/pattern을 지원하며 기본 credential hash는 고정 Argon2id다. service 정책 상한4096이지만 public validatePassword는1024 bytes를 허용한다.

차이/남은 범위: credential hash cost를 realm별 설정할 수 없고 byte/rune 및 최대값 의미가 다르다. 설정 가능한4096과 public 경로 실제1024 상한도 일치하지 않는다. custom password field의 bcrypt 설정은 계정 로그인 credential cost와 별개다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_password.go#L39) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_password.go#L179) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_password.go#L211)

PgBase 근거: [internal/userauth/auth_admin.go:176](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:176) / [internal/userauth/service.go:235](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service.go:235) / [internal/userauth/service.go:663](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/service.go:663) / [internal/adminauth/password.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/password.go:22)

테스트/검증 근거: [internal/userauth/auth_admin_integration_test.go:69](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin_integration_test.go:69) / [internal/adminauth/password_test.go:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/password_test.go:9)

## AUTH-31 · 목적별 token secret·duration 설정

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: auth/reset/emailChange/verification/file 목적별 secret과10..94670856초 duration을 collection에 저장하고 record tokenKey와 결합해 JWT를 만든다.

PgBase: session/verify/password_reset/email_change/otp/impersonation 목적별 암호화 key/version과60..86400초 TTL을 제공한다. rotation과 duration-only update를 구분한다.

차이/남은 범위: realm별 file-token secret/TTL 목적이 없고 TTL 범위가 PB의 기본5일 auth token도 표현하지 못한다. record tokenKey·JWT와 versioned stateful token 의미가 다르다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L292) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_tokens.go#L48) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_tokens.go#L145)

PgBase 근거: [internal/userauth/token_policy.go:20](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/token_policy.go:20) / [internal/userauth/token_policy.go:49](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/token_policy.go:49) / [internal/userauth/token_policy.go:116](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/token_policy.go:116)

테스트/검증 근거: [internal/userauth/auth_admin_integration_test.go:123](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin_integration_test.go:123) / [internal/userauth/auth_admin_integration_test.go:157](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin_integration_test.go:157) / [internal/userauth/email_otp_integration_test.go:96](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_otp_integration_test.go:96)

## AUTH-32 · 인증 레코드 emailVisibility 공개 제어

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: emailVisibility=false일 때 일반 공개 record에서 email을 숨기되 owner/superuser/실행된 manageRule에 해당하는 actor는 볼 수 있다.

PgBase: record의 visibility flag와 owner/admin/active same-realm manager 공개 예외를 구현하고 응답 email을 제거한다.

차이/남은 범위: same-realm manager로 제한되어 PB의 cross-collection auth manageRule와 다르다. 익명 공개 record 경로는 Anonymous principal fallback으로 존재하므로 부재로 판단하지 않았다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_model.go#L1266) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L437)

PgBase 근거: [internal/userauth/auth_records.go:198](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_records.go:198) / [internal/userauth/managed_authorization.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/managed_authorization.go:1) / [internal/api/auth_records.go:194](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/auth_records.go:194)

테스트/검증 근거: [internal/userauth/auth_record_model_integration_test.go:259](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_record_model_integration_test.go:259) / [internal/userauth/text_record_identity_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/text_record_identity_integration_test.go:16)

## AUTH-33 · OIDC userInfo 없는 id_token 기반 로그인

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: userinfo URL이 없으면 id_token claims를 사용한다. audience/time 검증과 선택적 issuer allowlist/JWKS 서명 검증을 지원하고 표준 name/username/picture 및 bool-like verified를 추출한다.

PgBase: userinfo 미설정 시 mandatory issuer/JWKS, RS256 signature/kid/audience/azp/expiry/nonce를 검사한 뒤 generic claim mapping으로 로그인한다.

차이/남은 범위: 안전한 제한적 fallback은 존재하지만 mandatory JWKS/nonce, RS256-only와 bool-only generic email_verified, 필수 email decode, name/username 미매핑으로 PB의 지원 provider/claim 범위를 전부 표현하지 못한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/auth/oidc.go#L75) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/auth/oidc.go#L120) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/auth/oidc.go#L133)

PgBase 근거: [internal/userauth/oauth_http.go:46](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_http.go:46) / [internal/userauth/oauth_http.go:102](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_http.go:102) / [internal/userauth/oauth_oidc.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_oidc.go:22)

테스트/검증 근거: [internal/userauth/oauth_http_test.go:93](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_http_test.go:93) / [internal/userauth/oauth_http_test.go:131](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth_http_test.go:131)

## AUTH-34 · OAuth 신규 가입 createRule·required field 적용

**보완 필요** · P1 · static-only · 추가 확인 항목

PocketBase: OAuth 신규 사용자는 createData/mapped fields로 내부 POST collection records를 수행해 createRule와 동일 field validation을 통과해야 한다.

PgBase: 신규 OAuth identity는 registration flags와 email collision을 검사한 뒤 users와 identity를 직접 insert하고 authRule를 검사한다.

차이/남은 범위: 정적 경로상 registrationEnabled=true 및 허용 authRule일 때 createRule=false/required custom field가 OAuth 신규 가입에 적용되지 않는 것으로 보인다. 이 조합을 실행 재현한 결과는 없으며 기존 테스트는 authRule과 realm 격리만 검사한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_oauth2.go#L260) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_oauth2.go#L412)

PgBase 근거: [internal/userauth/oauth.go:355](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth.go:355) / [internal/userauth/oauth.go:385](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth.go:385) / [internal/userauth/oauth.go:405](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth.go:405)

테스트/검증 근거: [internal/userauth/collections_integration_test.go:100](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/collections_integration_test.go:100) / [internal/userauth/auth_rule_enforcement_integration_test.go:68](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_rule_enforcement_integration_test.go:68)

## AUTH-35 · verification token invalidation after an email-only account update

**판정 보류** · P1 · static-only · 추가 확인 항목

PocketBase: PB verification confirmation rejects a token whose email claim no longer matches the record; record saves also rotate tokenKey on email changes, invalidating existing signed record tokens.

PgBase: Verification token issuance stores no email payload; confirmation matches active user, purpose, expiry, unused state and a digest of current password hash, then sets verified. Email-only typed auth-record updates delete sessions but the reviewed path does not invalidate pending email-action rows.

차이/남은 범위: Static concern, not an executed reproduction or confirmed vulnerability: after issuing a still-valid verification token, an administrator/authorized manager changes only the email without changing the password; old-token confirmation appears able to verify the new address because no original-email comparison/invalidation is visible in the reviewed path. No regression test for that exact sequence was located. Reproduce under controlled fixtures before treating impact as established.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm.go#L89) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_model.go#L1451)

PgBase 근거: [internal/userauth/email_actions.go:75](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:75) / [internal/userauth/email_actions.go:82](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:82) / [internal/userauth/email_actions.go:364](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:364) / [internal/userauth/auth_records.go:469](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_records.go:469) / [migrations/system/000012_user_email_actions.sql:4](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/migrations/system/000012_user_email_actions.sql:4)

테스트/검증 근거: [internal/userauth/email_actions_integration_test.go:144](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions_integration_test.go:144) / [internal/userauth/email_actions_integration_test.go:177](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions_integration_test.go:177)

## AUTH-36 · verification trust-transition cleanup of old password and OAuth links

**보완 필요** · P1 · static-only · 추가 확인 항목

PocketBase: When verification changes an existing record from unverified to verified, PB randomizes the password if password auth is disabled. Its verified-upgrade model hook deletes pre-existing external auth links transactionally and rotates tokenKey when links existed.

PgBase: ConfirmVerification updates only verified and updated_at inside email-token consumption. No password randomization, pre-existing OAuth identity cleanup or session revocation is visible in that operation; the existing successful verification test intentionally retains the session.

차이/남은 범위: The reviewed verification transition does not implement PB's password and pre-existing external-auth cleanup. This is a static contract difference; exploitability or resulting account compromise has not been reproduced. PgBase tests cover verified state and single-use token handling, not these cleanup conditions.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm.go#L44) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/external_auth_model.go#L141)

PgBase 근거: [internal/userauth/email_actions.go:82](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:82) / [internal/userauth/email_actions.go:341](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:341) / [internal/userauth/auth_admin.go:428](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:428)

테스트/검증 근거: [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm_test.go#L156) / [internal/userauth/email_actions_integration_test.go:144](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions_integration_test.go:144)

## MAIL-01 · SMTP configuration

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: Global SMTP settings expose enabled, host, port, username/password, TLS, AUTH PLAIN or LOGIN, and optional EHLO/HELO localName; sender display name/address and app metadata are global settings. SMTP-disabled fallback is assessed separately in MAIL-06.

PgBase: Process-wide SMTP options and per-auth-collection encrypted SMTP credentials support address/from, username/password, serverName, implicit TLS, timeout and explicitly local insecure development mode. Blank submitted passwords preserve existing stored secrets; API reads redact them.

차이/남은 범위: The transport implements smtp.PlainAuth only and exposes no AUTH LOGIN selector or custom EHLO localName. TLS policy is deliberately stricter off-loopback. Global settings JSON, sender display-name metadata and realm configuration are not interchangeable with PB.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/settings_model.go#L356) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/settings_model.go#L521) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/smtp.go#L64)

PgBase 근거: [pgbase/email_options.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/email_options.go:12) / [internal/userauth/auth_admin.go:390](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:390) / [internal/userauth/auth_admin.go:472](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:472) / [internal/mailer/smtp.go:20](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/mailer/smtp.go:20)

테스트/검증 근거: [pgbase/email_actions_integration_test.go:151](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/email_actions_integration_test.go:151) / [internal/mailer/smtp_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/mailer/smtp_test.go:12) / [internal/mailer/smtp_test.go:28](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/mailer/smtp_test.go:28)

## MAIL-02 · auth mail templates

**대응 구현·설계 차이** · P2 · test-exists · 기존 항목

PocketBase: Each auth collection stores five configurable HTML email templates: verification, password reset, email change, OTP and auth alert, with subject/body and built-in defaults.

PgBase: Per-auth-collection mail templates accept verify/password_reset/email_change/otp/auth_alert, each with subject, required text body and optional HTML body. All five have active service send paths; HTML is wrapped and ordinary attachments can be supplied through hooks.

차이/남은 범위: PB template fields/default HTML/layout are not drop-in compatible with PgBase's purpose identifiers and required text/placeholder validation. No-template actions use simple native text fallbacks; auth alerts require an explicitly stored template. Placeholder capability is separately partial in MAIL-03.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L121) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L145) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_templates.go#L13)

PgBase 근거: [internal/userauth/auth_admin.go:95](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:95) / [internal/userauth/auth_admin.go:341](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:341) / [internal/userauth/email_actions.go:388](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:388) / [internal/userauth/email_actions.go:437](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:437) / [internal/userauth/email_otp.go:191](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_otp.go:191)

테스트/검증 근거: [internal/userauth/auth_admin_integration_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin_integration_test.go:15) / [internal/userauth/email_actions_integration_test.go:100](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions_integration_test.go:100) / [pgbase/email_actions_integration_test.go:133](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/email_actions_integration_test.go:133)

## MAIL-03 · placeholders/action links

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: PB resolves {APP_NAME}, {APP_URL}, {TOKEN}, {OTP}, {OTP_ID}, {ALERT_INFO} and nonhidden {RECORD:field} values in both subject and HTML body; string record values are escaped. Default action links target /_/#/auth/confirm-*/<token>.

PgBase: Templates interpolate native {{action_url}}, {{otp}}, {{otp_id}} or {{alert}} into body/HTML. Generated action links point to /auth/action#action=...&token=...; page clears the fragment and sends confirmation only on explicit submission.

차이/남은 범위: App metadata, arbitrary nonhidden record placeholders, raw token placeholder and all subject substitution are absent from reviewed rendering paths. Native placeholder/action-link/token syntax differs; copying a PB template does not produce an equivalent rendered message.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record.go#L251) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L257) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_templates.go#L3)

PgBase 근거: [internal/userauth/auth_admin.go:341](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:341) / [internal/userauth/auth_admin.go:369](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:369) / [internal/userauth/email_actions.go:393](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:393) / [internal/userauth/email_otp.go:191](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_otp.go:191) / [internal/api/email_actions.go:88](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/email_actions.go:88)

테스트/검증 근거: [internal/userauth/email_actions_integration_test.go:100](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions_integration_test.go:100) / [pgbase/email_actions_integration_test.go:133](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/email_actions_integration_test.go:133) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record_test.go#L48)

## MAIL-04 · template preview/SMTP test

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: The settings test-email form takes recipient, a selected one of five auth templates, and optional auth collection (default _superusers), builds a synthetic record, and sends the chosen real rendered template through the normal mail lifecycle.

PgBase: Separate administrator endpoints preview a submitted native template using a supplied action URL/sample OTP/alert, and send a fixed 'PgBase SMTP test' message through the collection transport.

차이/남은 범위: SMTP test does not accept a template selector or exercise stored auth-template rendering/record placeholders. Preview and transport smoke test therefore do not implement PB's selected-template test-email contract. A preview-specific regression test was not located in the inspected tests.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/test_email_send.go#L21) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/test_email_send.go#L77)

PgBase 근거: [internal/api/auth_admin.go:50](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/auth_admin.go:50) / [internal/userauth/auth_admin.go:369](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:369) / [internal/userauth/auth_admin.go:416](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:416)

테스트/검증 근거: [pgbase/email_actions_integration_test.go:151](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/email_actions_integration_test.go:151) / [internal/api/auth_admin_test.go:25](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/auth_admin_test.go:25) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/test_email_send_test.go#L13)

## MAIL-05 · auth mail hooks

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: Each of five auth mails raises MailerRecordEvent with App, Record, replaceable Mailer, mutable Message and action metadata (token/newEmail, otpId/password, info); the generic OnMailerSend hook runs inside the selected client's Send call after the purpose event.

PgBase: Generic mail.send and purpose-specific hooks can veto or mutate delivery and receive paired outcomes; MailEvent provides Purpose, Message and Meta. OTP and alert services supply metadata, and the decorator wraps both configured SMTP and injected senders.

차이/남은 범위: Purpose events do not expose PB Record or replaceable Mailer/App. Verification/reset/email-change sends do not populate token/newEmail Meta. Generic hook precedes purpose hooks, reversing PB order. These are behavioral hook-contract gaps beyond naming differences.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record.go#L37) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record.go#L75) / [소스 3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record.go#L153) / [소스 4](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record.go#L229) / [소스 5](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go#L673)

PgBase 근거: [pgbase/lifecycle_hooks.go:190](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks.go:190) / [pgbase/lifecycle_hooks.go:228](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks.go:228) / [internal/userauth/email_actions.go:423](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:423) / [internal/userauth/email_actions.go:455](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions.go:455) / [internal/userauth/email_otp.go:211](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_otp.go:211) / [internal/userauth/auth_admin.go:472](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:472)

테스트/검증 근거: [pgbase/lifecycle_hooks_test.go:39](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks_test.go:39) / [pgbase/extension_runtime_regression_test.go:149](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_runtime_regression_test.go:149) / [internal/userauth/email_actions_integration_test.go:100](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/email_actions_integration_test.go:100)

## MAIL-06 · automatic sendmail fallback when SMTP is disabled

**미구현** · P3 · static-only · 추가 확인 항목

PocketBase: App.NewMailClient returns the built-in Sendmail transport when global SMTP.Enabled is false; Sendmail locates and invokes the system sendmail executable.

PgBase: Mailer selection uses a per-realm SMTP configuration or an explicitly configured process sender; absence of both returns 'user email actions unavailable'. No built-in sendmail selection/transport was found in internal/mailer, email options or sender setup.

차이/남은 범위: Deployments relying on the host sendmail command require custom sender integration or SMTP configuration. This omitted transport feature cannot be inferred from generic SMTP support.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go#L653) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/sendmail.go#L43)

PgBase 근거: [internal/userauth/auth_admin.go:472](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_admin.go:472) / [pgbase/email_options.go:27](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/email_options.go:27) / [internal/mailer/mailer.go:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/mailer/mailer.go:26)

테스트/검증 근거: [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base_test.go#L180)

## MAIL-07 · generic mail envelope, inline attachments and HTML-only messages

**보완 필요** · P2 · test-exists · 추가 확인 항목

PocketBase: PB Message supports per-message named From, multiple To/Cc/Bcc, arbitrary headers, regular and inline attachment readers, text and/or HTML; SMTP derives plain text from HTML when Text is empty.

PgBase: Native Message and script mail bridge support one recipient, subject, required text, optional HTML and bounded ordinary attachments; sender is transport-owned. SMTP encodes multipart alternative/mixed and limits total attachment bytes to 8 MiB.

차이/남은 범위: No native Cc/Bcc/multiple recipients, per-message From, custom headers or inline attachment fields. HTML-only messages are rejected because Text is mandatory, instead of PB's HTML-to-text fallback. Ordinary attachment tests do not establish these missing envelope capabilities.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/mailer.go#L12) / [소스 2](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/smtp.go#L93)

PgBase 근거: [internal/mailer/mailer.go:6](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/mailer/mailer.go:6) / [internal/mailer/smtp.go:62](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/mailer/smtp.go:62) / [internal/mailer/smtp.go:139](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/mailer/smtp.go:139) / [pgbase/extension_bridge_mail.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_bridge_mail.go:12)

테스트/검증 근거: [internal/mailer/smtp_test.go:55](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/mailer/smtp_test.go:55) / [internal/mailer/smtp_test.go:76](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/mailer/smtp_test.go:76) / [pgbase/javascript_file_mail_integration_test.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_file_mail_integration_test.go:22)

## RULE-01 · five CRUD actions

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: Collections have independent nullable list/view/create/update/delete rule strings; nonempty rules filter the corresponding operation.

PgBase: Five persisted typed AST actions, PocketBase string import/export adapter, transaction-bound row evaluation, and native owner defaults are implemented. Collection admission remains an additional independent opt-in gate.

차이/남은 범위: Different storage and admission model; absence of stored policy means owner-only, while PB collection rules default to locked. This row establishes five-action capability, not syntax or wire equivalence.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L52)

PgBase 근거: [internal/accessrule/rule.go:47](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/rule.go:47) / [internal/accessrule/pocketbase.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase.go:15) / [internal/recordaccess/rules.go:86](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/rules.go:86) / [internal/recordaccess/subject.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/subject.go:16) / [internal/recordops/access_context.go:27](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/access_context.go:27)

테스트/검증 근거: [pgbase/record_rules_integration_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/record_rules_integration_test.go:13) / [internal/recordaccess/pocketbase_rule_integration_test.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/pocketbase_rule_integration_test.go:23)

## RULE-02 · owner/authenticated/comparison

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: Rules support field comparisons and request auth fields, including arbitrary literal comparisons against @request.auth.id; record ownership is expressed through fields rather than a built-in ownership side table.

PgBase: Native owner/authenticated/all/any/comparison AST and PB field-to-auth-ID comparisons exist. The parser special-cases auth ID empty-string equality into authenticated/unauthenticated predicates.

차이/남은 범위: @request.auth.id = 'nonempty-id' is rejected by accessRuleComparison, despite being valid PB. Native owner is intentionally different from id = @request.auth.id; tests correctly keep them distinct. Existing tests cover empty ID/field references, not arbitrary literal ID equality.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver_runner.go#L199)

PgBase 근거: [internal/accessrule/rule.go:150](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/rule.go:150) / [internal/accessrule/rule.go:169](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/rule.go:169) / [internal/query/access_rule.go:121](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/access_rule.go:121) / [internal/query/access_rule.go:146](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/access_rule.go:146)

테스트/검증 근거: [internal/query/access_rule_test.go:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/access_rule_test.go:26) / [internal/query/access_rule_test.go:52](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/access_rule_test.go:52) / [pgbase/record_rules_integration_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/record_rules_integration_test.go:13)

## RULE-03 · PocketBase filter syntax/variables

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: PB filter strings support quoted literals, request operands, relation/back-relation/collection references, modifiers, functions, and identifier macros.

PgBase: Catalog-aware PB AST/compiler supports many comparisons, relations, JSON paths, any/each semantics, geoDistance, strftime and selected UTC macros. A compatibility rules endpoint parses PB strings.

차이/남은 범위: Not full PB grammar: access-rule parsing calls parseFilterRequest without the double-quote normalization used by the record-query compatibility path, so PB double-quoted rule literals fail. Only selected date macros are supported. More critically, ValidateRules supplies nil request context for list/view/delete, so request-variable rules such as @request.method = 'GET' cannot be saved there; create/update use empty request maps and reject auth literals. The broad conformance test directly inserts policy SQL and therefore does not test this save boundary.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver.go#L97)

PgBase 근거: [internal/query/access_rule.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/access_rule.go:17) / [internal/query/filter.go:219](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/filter.go:219) / [internal/api/rules.go:29](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/rules.go:29) / [internal/accessrule/pocketbase_resolver.go:362](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_resolver.go:362) / [internal/recordaccess/rules.go:86](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/rules.go:86)

테스트/검증 근거: [internal/recordaccess/pocketbase_conformance_integration_test.go:29](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/pocketbase_conformance_integration_test.go:29) / [internal/query/pocketbase_rule_conformance_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/pocketbase_rule_conformance_test.go:17) / [internal/recordaccess/rules_validation_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/rules_validation_test.go:13)

## RULE-04 · rule null/empty semantics

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: A null rule locks the action to superusers, an empty string opens it, and a nonempty expression filters records. Locked state is different from an expression that matches no rows.

PgBase: PB rule adapter round-trips null as deny and empty string as allow. Auth collection rule storage separately preserves nullable strings. Native record execution combines these predicates with collection admission.

차이/남은 범위: Base-record null is lowered to the same deny AST used for ordinary filtering, losing the locked-versus-filter distinction at execution. A null-derived list rule produces 200 empty when collection admission is enabled; PB returns 403. Empty allow also does not open PgBase without its independent admission opt-in. Auth null/empty tests only demonstrate auth storage and selected session paths.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L52)

PgBase 근거: [internal/accessrule/pocketbase.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase.go:15) / [internal/accessrule/pocketbase.go:44](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase.go:44) / [internal/recordaccess/rules.go:104](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/rules.go:104) / [internal/recordaccess/subject.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/subject.go:30)

테스트/검증 근거: [internal/accessrule/pocketbase_test.go:8](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_test.go:8) / [internal/userauth/nullable_rules_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/nullable_rules_integration_test.go:16) / [pgbase/record_rules_integration_test.go:57](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/record_rules_integration_test.go:57)

## RULE-05 · authRule

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: After credentials succeed and on refresh, AuthRule filters the authenticating record using the original HTTP RequestInfo; @request.auth is the preexisting caller, not automatically the authenticating target. Null locks authentication; empty allows. Refusal is 403.

PgBase: Nullable persisted authRule is evaluated before transaction-bound session issuance, OAuth/OTP/MFA credential consumption and refresh. Current custom fields and auth-to-auth relations are supported.

차이/남은 범위: Session evaluation always makes the authenticating target the principal and supplies no original HTTP method/query/header/body/context. Thus rules involving @request.auth or request metadata differ, including @request.auth.id != '' during an unauthenticated password login. PgBase returns invalid_credentials/401 rather than PB 403. The auth resolver loads only auth collections, so @collection/base-record relation reachability is also narrower. Tests verify target-field allow/deny and rollback, not original-caller/request-context parity.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L53)

PgBase 근거: [internal/userauth/auth_rule_resolver.go:168](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_rule_resolver.go:168) / [internal/userauth/auth_rule_resolver.go:188](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_rule_resolver.go:188) / [internal/userauth/auth_rule_resolver.go:196](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_rule_resolver.go:196) / [internal/userauth/session_refresh.go:85](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/session_refresh.go:85) / [internal/userauth/mfa.go:265](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/mfa.go:265) / [internal/userauth/oauth.go:406](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/oauth.go:406)

테스트/검증 근거: [internal/userauth/auth_rule_enforcement_integration_test.go:28](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_rule_enforcement_integration_test.go:28) / [internal/userauth/auth_rule_enforcement_integration_test.go:70](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_rule_enforcement_integration_test.go:70) / [internal/userauth/auth_rule_enforcement_integration_test.go:108](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_rule_enforcement_integration_test.go:108) / [internal/userauth/auth_rule_enforcement_integration_test.go:150](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_rule_enforcement_integration_test.go:150) / [internal/userauth/auth_record_model_integration_test.go:114](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_record_model_integration_test.go:114)

## RULE-06 · manageRule

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: An authenticated caller satisfying a nonempty manageRule gains auth-record manager privileges after create/update access passes. Empty/null grants no manager access. Ordinary permitted self password updates can use oldPassword without manage privileges; manager rules may inspect auth from another collection.

PgBase: Delegated update evaluates distinct actor and target, requires update rule plus manage rule for email/password/verified/active changes, revokes affected credentials, and uses deleteRule for deletion. Empty manage grants no privileges.

차이/남은 범위: Actor lookup is hard-bound to the target auth collection; tests explicitly assert cross-realm managers are rejected, unlike PB's rule-controlled request auth. Authorized auth-record password updates always require manageRule and do not accept the PB oldPassword alternative through this record mutation path. Separate account password-change APIs are not the same record-update contract.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L732)

PgBase 근거: [internal/userauth/managed_authorization.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/managed_authorization.go:14) / [internal/userauth/auth_records.go:144](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_records.go:144) / [internal/userauth/auth_rule_resolver.go:71](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_rule_resolver.go:71) / [internal/userauth/auth_records.go:209](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_records.go:209) / [internal/api/auth_records.go:245](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/auth_records.go:245)

테스트/검증 근거: [internal/userauth/auth_record_model_integration_test.go:184](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_record_model_integration_test.go:184) / [internal/userauth/auth_record_model_integration_test.go:239](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_record_model_integration_test.go:239) / [internal/userauth/auth_record_model_integration_test.go:259](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_record_model_integration_test.go:259)

## RULE-07 · rule administration/preview

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: Collection administration stores and validates PB filter strings through collection rule fields; the resolver evaluates those strings with request and catalog context. PB does not define PgBase's dedicated unsaved-row preview endpoint.

PgBase: Admin GET/PUT offers native AST and ?compat=pocketbase strings. The preview backend can evaluate an unsaved single rule against up to 50 records, including relations and supplied request context.

차이/남은 범위: Older claim that backend relation/request preview is absent is stale. UI still edits raw AST and supplies no request context editor. Its save adapter sends {rules, revision}, while native PUT strictly decodes a struct with only rules. Preview sends JSON.parse(draft), the full five-action Rules object, as one rule instead of selecting draft[previewAction]. The UI mock tests do not assert these bodies against actual endpoint decoding. PB text editing and usable end-to-end save/preview are not established.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L130)

PgBase 근거: [internal/api/rules.go:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/rules.go:21) / [internal/recordaccess/preview.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/preview.go:16) / [internal/recordaccess/preview.go:60](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/preview.go:60) / [ui/admin/src/pages/record-rules-editor.tsx:25](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-rules-editor.tsx:25) / [ui/admin/src/pages/record-rules-editor.tsx:47](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-rules-editor.tsx:47) / [ui/admin/src/backend/http-admin-ports.ts:518](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/backend/http-admin-ports.ts:518)

테스트/검증 근거: [internal/recordaccess/preview_regression_integration_test.go:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/preview_regression_integration_test.go:21) / [pgbase/record_rules_integration_test.go:70](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/record_rules_integration_test.go:70) / [ui/admin/src/pages/record-rules-editor.test.tsx:19](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-rules-editor.test.tsx:19)

## RULE-08 · rule denial HTTP status and result contract (omission)

**보완 필요** · P1 · test-exists · 추가 확인 항목

PocketBase: Locked action:403; nonmatching list:200 empty; failed create predicate:400; failed view/update/delete predicate:404; failed authRule:403.

PgBase: Record predicate failures hide existing rows with 404 and list predicates filter before pagination. Create denial maps to ErrDenied/403. Authentication rule failure maps to invalid_credentials/401.

차이/남은 범위: Create predicate and authRule statuses differ from PB, and null-lock distinction is lost for base-row list/view/update/delete. Existing integration test explicitly expects 403 on create predicate denial, so this is a documented implementation difference, not proof of PB conformance.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L308)

PgBase 근거: [internal/recordops/access_context.go:39](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/access_context.go:39) / [internal/api/record_handlers.go:612](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers.go:612) / [internal/api/user_handlers.go:229](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/user_handlers.go:229) / [internal/userauth/auth_rule_resolver.go:201](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/auth_rule_resolver.go:201)

테스트/검증 근거: [pgbase/record_rules_integration_test.go:45](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/record_rules_integration_test.go:45) / [pgbase/record_rules_integration_test.go:50](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/record_rules_integration_test.go:50) / [pgbase/record_rules_integration_test.go:61](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/record_rules_integration_test.go:61)

## RULE-09 · actual request environment and auth projection (omission)

**보완 필요** · P1 · test-exists · 추가 확인 항목

PocketBase: @request.context is a scalar with default/expand/realtime/protectedFile/batch/oauth2/otp/password values. @request.auth exposes current auth record fields and relations; method/body/query/normalized headers derive from the real request, and absent values resolve NULL.

PgBase: Record HTTP creates method/query/normalized-header maps, four auth fields (id/email/verified/created), and a Context object containing requestId. Compiler supports @request.context.<key> and requires explicit auth-map keys.

차이/남은 범위: PB scalar @request.context = 'default' cannot be represented by the PgBase dotted Context map. Custom auth fields/collection identity/relations are absent from real record HTTP auth context; missing auth keys cause compilation errors instead of PB NULL semantics. Conformance tests manually construct a richer RequestContext and do not establish actual HTTP wiring parity.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/event_request.go#L152)

PgBase 근거: [internal/api/record_handlers.go:37](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/record_handlers.go:37) / [internal/accessrule/rule.go:111](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/rule.go:111) / [internal/query/access_rule.go:78](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/access_rule.go:78) / [internal/accessrule/pocketbase_resolver.go:434](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_resolver.go:434) / [internal/accessrule/pocketbase_resolver.go:445](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_resolver.go:445)

테스트/검증 근거: [internal/recordaccess/pocketbase_conformance_integration_test.go:141](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/pocketbase_conformance_integration_test.go:141) / [internal/query/access_rule_test.go:62](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/access_rule_test.go:62)

## RULE-10 · privileged subject rule bypass (omission)

**대응 구현·설계 차이** · none · static-only · 추가 확인 항목

PocketBase: Authenticated superusers bypass collection record rules, including locked null rules.

PgBase: Administrator operations bypass row rules. PgBase additionally has scoped service principals: collection/action grants are checked, then service principals bypass user row predicates.

차이/남은 범위: Administrator behavior has a corresponding PB role, but scoped service bypass is a PgBase extension without a PB-equivalent role. The listed integration test uses administrator rule administration; it is not a dedicated bypass conformance test.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L52)

PgBase 근거: [internal/recordaccess/subject.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/subject.go:16) / [internal/recordops/access_context.go:27](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/access_context.go:27)

테스트/검증 근거: [pgbase/record_rules_integration_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/record_rules_integration_test.go:13)

## RULE-11 · hidden fields in stored API rules (omission)

**보완 필요** · P1 · static-only · 추가 확인 항목

PocketBase: Stored API rules can reference hidden fields; allowHiddenFields=true is used during rule compilation, then restricted only for unprivileged client search filters.

PgBase: The PB catalog resolver rejects hidden non-relation fields unconditionally. Native typed AST field resolution can still address scalar hidden fields, so the missing portion is PB rule-string/catalog behavior.

차이/남은 범위: A valid PB stored rule such as hidden_status = 'published' cannot be saved/compiled through PgBase's PB rule path. PB explicitly tests a hidden title in a stored listRule; no corresponding PgBase parity assertion was found in inspected tests.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L64)

PgBase 근거: [internal/accessrule/catalog_resolver.go:44](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/catalog_resolver.go:44) / [internal/accessrule/rule.go:346](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/rule.go:346) / [internal/recordaccess/rule_compile.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/rule_compile.go:18)

테스트/검증 근거:

## RULE-12 · collection aliases and shared-row correlation (omission)

**보완 필요** · P1 · test-exists · 추가 확인 항목

PocketBase: @collection.<nameOrId>:<alias>.<field> resolves collection identity separately from a stable join alias. Repeated aliases use the same joined row; distinct aliases permit separate joined rows.

PgBase: Unaliased @collection.name.field resolves a catalog name and compiles per-comparison multi-value EXISTS/NOT EXISTS predicates. Forward and reverse relations are supported.

차이/남은 범위: ResolveCollection performs exact name lookup and has no nameOrId/alias handling. Shared-row correlation across multiple @collection comparisons is not proven by existing tests, which use only one such comparison per policy; compiler structure uses separate subqueries. A PB alias rule cannot be imported as-is. Potential cross-row differences require focused executable differential tests before assigning a security impact.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver_runner.go#L157)

PgBase 근거: [internal/accessrule/catalog_resolver.go:163](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/catalog_resolver.go:163) / [internal/accessrule/pocketbase_resolver.go:424](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_resolver.go:424) / [internal/accessrule/pocketbase_resolver.go:228](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_resolver.go:228)

테스트/검증 근거: [internal/recordaccess/pocketbase_conformance_integration_test.go:126](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/pocketbase_conformance_integration_test.go:126) / [internal/recordaccess/pocketbase_rule_integration_test.go:93](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/pocketbase_rule_integration_test.go:93)

## RULE-13 · predicate NULL/empty and explicit LIKE wildcard semantics (omission)

**보완 필요** · P1 · test-exists · 추가 확인 항목

PocketBase: For ordinary non-JSON operands, PB equality normalizes null and empty string and inequality is NULL-aware; JSON extraction can disable fallback. LIKE wraps unpatterned literal text but preserves explicit unescaped percent wildcard placement.

PgBase: PB SQL compiler treats null literals as IS NULL/IS NOT NULL and other comparisons as COALESCE(lhs op rhs,false); contains always emits '%' || rhs || '%'.

차이/남은 범위: Ordinary null/empty equality is not PB-compatible; current PgBase test explicitly keeps title=null distinct from title=''. LIKE 'prefix%' becomes '%prefix%' instead of retaining prefix anchoring. Existing tests cover basic contains/null cases, not PB explicit wildcard and NULL fallback fixtures. These differences can change which records a policy grants.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/filter.go#L334)

PgBase 근거: [internal/accessrule/pocketbase_resolver.go:188](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_resolver.go:188) / [internal/accessrule/pocketbase_resolver.go:203](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_resolver.go:203) / [internal/accessrule/pocketbase_resolver.go:212](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_resolver.go:212)

테스트/검증 근거: [internal/query/access_rule_test.go:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/access_rule_test.go:26) / [internal/recordaccess/pocketbase_rule_integration_test.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/pocketbase_rule_integration_test.go:23) / [internal/accessrule/pocketbase_resolver_test.go:56](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/accessrule/pocketbase_resolver_test.go:56)

## FIL-01 · record multipart의 여러 attachment part

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: Record create/update extracts every upload under field, +field, field+ names before saving.

PgBase: A multipart batch uses a requests JSON part and named file parts referenced through $file. It stages all bytes, then commits record changes and attachment metadata in one PostgreSQL transaction.

차이/남은 범위: Complete atomic batch capability with a different request protocol; the standalone upload endpoint accepts one file part.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L690)

PgBase 근거: [internal/api/batch_handlers.go:364](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/batch_handlers.go:364) / [internal/api/batch_file_modifiers.go:155](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/batch_file_modifiers.go:155) / [internal/filestore/staging.go:170](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/staging.go:170)

테스트/검증 근거: [pgbase/batch_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/batch_integration_test.go:17) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_file_test.go#L832)

## FIL-02 · file field 최대 선택 수/cardinality

**좁은 계약 일치** · none · test-exists · 기존 항목

PocketBase: MaxSelect <=1 means one file; larger values allow multiple files; Required requires at least one. PocketBase has no FileField minSelect.

PgBase: Field MaximumCount and Required are checked under a per-record/field advisory lock. Both staged batch removals and standalone deletion reject an empty required field.

차이/남은 범위:

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_file.go#L101)

PgBase 근거: [internal/filestore/service.go:288](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:288) / [internal/filestore/service.go:478](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:478) / [internal/filestore/staging.go:202](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/staging.go:202) / [internal/filestore/staging.go:245](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/staging.go:245)

테스트/검증 근거: [pgbase/batch_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/batch_integration_test.go:17) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_file_test.go#L227)

## FIL-03 · 파일 크기와 MIME 정책

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: Default maxSize is 5 MiB; field MIME list is optional; configured MIME types are checked by detected file type.

PgBase: Enforces field and global size/type ceilings, declared size and SHA-256, MIME sniffing and image pixel limits. Global maximum is 100 MiB and the default MIME list is restrictive.

차이/남은 범위: Intentional additional global budgets and MIME declaration checks; empty configuration is not PocketBase's allow-any MIME mode.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_file.go#L279)

PgBase 근거: [internal/filestore/service.go:257](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:257) / [internal/filestore/image.go:112](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/image.go:112) / [pgbase/file_options.go:25](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_options.go:25) / [pgbase/file_services.go:151](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_services.go:151)

테스트/검증 근거: [internal/filestore/service_integration_test.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service_integration_test.go:22) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_file_test.go#L227)

## FIL-04 · record 변경과 파일 attach/remove

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: Record file setters support replace, append field+, prepend +field and filename removal field-. Upload failure cleans previously uploaded files; successful saves delete detached objects.

PgBase: Batch field modifiers now support replace/append/prepend/remove and preserve order. Metadata changes share the record transaction; physical deletion is deferred through durable object GC.

차이/남은 범위: The old comparison's unsupported-modifier claim is stale. Physical deletion timing differs intentionally; see FIL-19 for the separate standalone malformed-upload edge.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_file.go#L336)

PgBase 근거: [internal/api/batch_file_modifiers.go:45](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/batch_file_modifiers.go:45) / [internal/api/batch_file_modifiers.go:250](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/batch_file_modifiers.go:250) / [internal/filestore/staging.go:210](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/staging.go:210)

테스트/검증 근거: [pgbase/batch_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/batch_integration_test.go:17) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_file_test.go#L715) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_file_test.go#L969)

## FIL-05 · 보호 파일 field

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: Protected files evaluate the collection view rule using auth derived from a file token. An invalid/missing token supplies nil auth, which may still pass an explicitly public view rule. Protected defaults false.

PgBase: Fields default protected; explicit protected=false enables the public file-ID URL. Authenticated access checks current record/field policy; hiding or protecting a field revokes access through existing URLs/tokens.

차이/남은 범위: Secure default and authenticated-token policy intentionally differ from PocketBase; do not claim identical public/protected semantics.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file.go#L108)

PgBase 근거: [internal/filestore/service.go:69](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:69) / [internal/filestore/service.go:380](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:380) / [internal/filestore/public.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/public.go:14) / [pgbase/file_services.go:100](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_services.go:100)

테스트/검증 근거: [pgbase/file_integration_test.go:131](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_integration_test.go:131) / [pgbase/file_integration_test.go:191](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_integration_test.go:191) / [internal/filestore/policy_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/policy_test.go:11) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file_test.go#L92)

## FIL-06 · 원본 download URL·filename 응답

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: GET /api/files/{collection name or ID}/{recordId}/{filename} resolves a record file and supplies its download filename.

PgBase: Exposes UUID file-ID URLs and Content-Disposition filenames, plus file metadata/publicUrl. Authenticated and public paths differ.

차이/남은 범위: URL identity and wire format differ. Range/conditional/inline behavior is separately missing in FIL-14.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file.go#L85)

PgBase 근거: [internal/api/file_handlers.go:47](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:47) / [internal/api/file_handlers.go:84](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:84) / [internal/api/file_handlers.go:211](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:211)

테스트/검증 근거: [pgbase/file_integration_test.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_integration_test.go:23) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file_test.go#L92)

## FIL-07 · 단기 download token

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: POST /api/files/token creates an auth-record file token; URL token identifies that auth record, which is then checked against each file's view rule.

PgBase: Per-file token includes principal ID/kind, variant and expiry; minting and opening reauthorize current policy. TTL defaults 5 minutes, capped at 15. The private HTTP URL also needs caller authentication.

차이/남은 범위: Token-only links suitable for unauthenticated image elements are not the private PgBase contract; use the SDK/authenticated route or explicitly public fields.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_tokens.go#L145)

PgBase 근거: [internal/filestore/service.go:529](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:529) / [internal/filestore/service.go:563](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:563) / [internal/api/file_handlers.go:211](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:211) / [pgbase/file_options.go:41](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_options.go:41)

테스트/검증 근거: [internal/filestore/local_test.go:73](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/local_test.go:73) / [pgbase/file_integration_test.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_integration_test.go:23) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file_test.go#L19)

## FIL-08 · thumb WxH/t/b/f 문법

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: Supports centered WxH, top/bottom crop, fit and zero-axis aspect-preserving resize.

PgBase: Parses the same principal size modes as variant values, with explicit declared field variants and stricter dimension/pixel/byte bounds. Rendering uses nearest-neighbor sampling.

차이/남은 범위: Request parameter is variant, not thumb. No pixel-identical rendering claim: PocketBase uses imaging.Linear; current PgBase crop test mainly checks dimensions.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L598)

PgBase 근거: [internal/filestore/variants.go:19](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants.go:19) / [internal/filestore/variants.go:140](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants.go:140)

테스트/검증 근거: [internal/filestore/variants_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants_test.go:15) / [internal/filestore/variants_test.go:79](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants_test.go:79) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem_test.go#L1005)

## FIL-09 · 이미지 transform format 범위

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: Record HTTP thumbnails accept PNG/JPEG/GIF/WebP. Generic CreateThumb also understands TIFF/BMP; output follows supported encoder or falls back to PNG.

PgBase: Record transforms decode PNG/JPEG/GIF/WebP. PNG remains PNG; JPEG/GIF/WebP become JPEG, using a single frame. GIF/WebP require explicit allowed MIME configuration.

차이/남은 범위: Output encoding/transparency differs intentionally. Generic TIFF/BMP transforms are not implemented; EXIF orientation is a separate missing behavior in FIL-15.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file.go#L22)

PgBase 근거: [internal/filestore/image.go:65](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/image.go:65) / [internal/filestore/variants.go:140](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants.go:140) / [internal/api/file_handlers.go:117](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:117)

테스트/검증 근거: [internal/filestore/variants_test.go:28](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants_test.go:28) / [internal/filestore/variants_test.go:54](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants_test.go:54) / [internal/api/file_handlers_test.go:31](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers_test.go:31) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem_test.go#L1005)

## FIL-10 · thumbnail cache/concurrent generation

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: Caches thumbnails by path; singleflight deduplicates one key and a configurable semaphore bounds simultaneous generation and wait.

PgBase: Caches variant receipts and bytes; PostgreSQL advisory locks serialize generation across processes; publication fencing and reference-aware cleanup handle failed metadata commits.

차이/남은 범위: Cross-process DB locking replaces local singleflight; PocketBase's PB_THUMBS_MAX_WORKERS/MAX_WAIT knobs are not exposed.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file.go#L219)

PgBase 근거: [internal/filestore/variants.go:33](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants.go:33) / [internal/filestore/variants.go:81](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants.go:81)

테스트/검증 근거: [internal/filestore/variants_integration_test.go:20](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants_integration_test.go:20) / [internal/filestore/variants_integration_test.go:61](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants_integration_test.go:61) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file_test.go#L539)

## FIL-11 · 로컬 object store

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: Local filesystem stores objects under its configured root and supports read/write/delete operations.

PgBase: Validated physical object keys stay under a configured root. Writes use a same-directory temporary file then rename; canceled writes remove temporary files and delete is idempotent.

차이/남은 범위: Opaque object keys and metadata-backed publication differ from PocketBase's record-directory layout; neither cited test establishes power-loss durability.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L96)

PgBase 근거: [internal/filestore/local.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/local.go:13) / [internal/filestore/local.go:38](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/local.go:38) / [internal/filestore/local.go:91](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/local.go:91)

테스트/검증 근거: [internal/filestore/local_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/local_test.go:17) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem_test.go#L432)

## FIL-12 · S3 backend와 runtime 설정

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: NewFilesystem selects local or S3 using runtime app settings including endpoint, bucket, region, credentials and path style.

PgBase: Configured S3 backend supports put/open/delete; runtime settings redact/encrypt credentials, test connectivity, rotate credentials and fence storage cutovers. Location changes with existing inventory require migration.

차이/남은 범위: Configured storage support is complete within the stricter migration contract. This does not establish arbitrary $filesystem.s3(...) targets; see FIL-17.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go#L750)

PgBase 근거: [internal/filestore/s3.go:33](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/s3.go:33) / [internal/filestore/storage_settings.go:284](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/storage_settings.go:284) / [internal/filestore/storage_settings.go:377](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/storage_settings.go:377)

테스트/검증 근거: [internal/filestore/storage_settings_integration_test.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/storage_settings_integration_test.go:22) / [pgbase/storage_settings_http_integration_test.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/storage_settings_http_integration_test.go:14) / [internal/filestore/storage_migration_integration_test.go:238](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/storage_migration_integration_test.go:238)

## FIL-13 · extension용 generic filesystem facade

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: System exposes local/S3 selection, readers/writers, copy, complete List, DeletePrefix, Serve, CreateThumb and context control.

PgBase: Namespace-scoped extension object storage supports immutable versions, transactional publication, streaming, pagination, copy/delete and a JS System-shaped facade. Some facade methods do not implement the PB contract.

차이/남은 범위: $filesystem.s3 ignores its arguments; System.list truncates at 100; setContext is a no-op. DeletePrefix itself paginates all keys, then deletes serially and stops at the first error rather than returning PB's collected errors.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L41)

PgBase 근거: [pgbase/extension_objects.go:256](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_objects.go:256) / [pgbase/extension_objects.go:364](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_objects.go:364) / [pgbase/extension_objects.go:499](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_objects.go:499) / [internal/jsvm/pocketbase_filesystem_globals.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_filesystem_globals.go:12)

테스트/검증 근거: [pgbase/extension_objects_integration_test.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_objects_integration_test.go:24) / [pgbase/extension_objects_integration_test.go:119](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_objects_integration_test.go:119) / [pgbase/extension_objects_integration_test.go:391](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_objects_integration_test.go:391) / [pgbase/javascript_filesystem_serve_integration_test.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_filesystem_serve_integration_test.go:18)

## RT-01 · SSE connection endpoint/headers

**좁은 계약 일치** · none · test-exists · 기존 항목

PocketBase: GET /api/realtime returns text/event-stream, no-store, X-Accel-Buffering:no, and a PB_CONNECT event containing clientId.

PgBase: The multiplex route exposes the same endpoint, headers and connect envelope.

차이/남은 범위: This narrow row does not assert connection lifetime or reconnect-ID equivalence; see RT-07.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime.go#L60)

PgBase 근거: [internal/api/realtime_sessions.go:102](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:102) / [internal/api/realtime_sessions.go:216](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:216)

테스트/검증 근거: [pgbase/realtime_integration_test.go:133](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/realtime_integration_test.go:133) / [internal/api/realtime_sessions_test.go:53](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions_test.go:53) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime_test.go#L23)

## RT-02 · subscribe/unsubscribe API

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: POST replaces a client's subscriptions; empty list unsubscribes all; maximum 1000 topics; validates originating IP and permits guest-to-auth upgrade only.

PgBase: POST performs atomic subscription replacement under a delivery mutex, validates client/IP/principal, permits empty lists and preserves cursors for unchanged subscriptions.

차이/남은 범위: PgBase validates collection access/options at subscription time, rejects duplicate/empty entries and requires JSON; PocketBase stores generic topic strings and authenticates at delivery.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime.go#L173)

PgBase 근거: [internal/api/realtime_sessions.go:345](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:345) / [internal/api/realtime_sessions.go:411](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:411)

테스트/검증 근거: [internal/api/realtime_sessions_test.go:203](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions_test.go:203) / [pgbase/realtime_integration_test.go:133](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/realtime_integration_test.go:133) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime_test.go#L112)

## RT-03 · arbitrary subscription topics

**보완 필요** · P1 · static-only · 기존 항목

PocketBase: Subscriptions are arbitrary strings; application code can send a named message through the subscriptions broker, including names unrelated to collections.

PgBase: Custom topics use a reserved @ namespace; PublishApplication reversibly encodes arbitrary bounded UTF-8 topics. An exact-match broker delivers JSON payloads with bounded queues.

차이/남은 범위: Static lifecycle defect: replace skips collection validation for application topics, but the 1-second delivery loop does not skip them and calls ReadFor with an empty collection. The real reader rejects it, producing PB_ERROR and disconnect. The existing custom-topic test publishes immediately and uses a stub; it does not cover this tick.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/subscriptions/client.go#L176)

PgBase 근거: [internal/api/realtime_broker.go:86](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_broker.go:86) / [internal/api/realtime_broker.go:124](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_broker.go:124) / [internal/api/realtime_sessions.go:509](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:509) / [internal/api/realtime_sessions.go:255](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:255)

테스트/검증 근거: [internal/api/realtime_sessions_test.go:53](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions_test.go:53) / [internal/api/realtime_sessions_test.go:141](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions_test.go:141) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/subscriptions/client_test.go#L97)

## RT-04 · guest/user/superuser/service SSE availability

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: Connect is public; subscribe may associate a guest or auth record, including superusers. PocketBase has no PgBase service principal type.

PgBase: Optional admission supports guest, user bearer, administrator cookie and scoped service bearer; records are checked under the principal's current rules/scopes.

차이/남은 범위: Administrator credential protocol differs and service principals are a PgBase extension.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime.go#L218)

PgBase 근거: [internal/api/realtime_sessions.go:114](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:114) / [internal/api/realtime_sessions.go:150](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:150) / [internal/realtime/service.go:86](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service.go:86)

테스트/검증 근거: [pgbase/realtime_integration_test.go:19](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/realtime_integration_test.go:19) / [pgbase/realtime_integration_test.go:133](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/realtime_integration_test.go:133) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime_test.go#L321)

## RT-05 · subscription filter/expand와 access 재판정

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: Each topic carries options.query and options.headers; record topics use ViewRule, collection topics ListRule. Filter, expand, fields and enrichment apply before sending; delete delivery is prepared before deletion.

PgBase: Reads committed events under current ACLs, filters scalar fields and projects output. Relation filters query the current record; expand is fetched for non-delete events.

차이/남은 범위: Relation-filtered delete events are explicitly skipped and deleted records never expand. List AND view rules are combined for every topic. PB options.headers/custom query context are unsupported; see RT-08/09.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime.go#L606)

PgBase 근거: [internal/realtime/service.go:212](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service.go:212) / [internal/api/realtime_sessions.go:266](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:266) / [internal/api/realtime_sessions.go:279](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:279) / [internal/api/realtime_sessions.go:289](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:289)

테스트/검증 근거: [pgbase/realtime_integration_test.go:133](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/realtime_integration_test.go:133) / [pgbase/realtime_integration_test.go:336](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/realtime_integration_test.go:336) / [internal/realtime/service_integration_test.go:28](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service_integration_test.go:28)

## RT-06 · event envelope

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: Record messages use the subscription string as SSE event name and {action,record} as data; SSE id is the client ID.

PgBase: Multiplex messages use the same action/record envelope and subscription event name, but numeric durable delivery IDs. The legacy per-collection endpoint has its own record/cursor envelopes.

차이/남은 범위: Envelope shape matches on the multiplex route; field enrichment and ID/replay semantics do not fully match.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime.go#L715)

PgBase 근거: [internal/api/realtime_sessions.go:317](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:317) / [internal/api/realtime.go:151](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime.go:151) / [internal/realtime/service.go:51](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service.go:51)

테스트/검증 근거: [pgbase/realtime_integration_test.go:133](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/realtime_integration_test.go:133) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/subscriptions/message.go#L20)

## RT-07 · connection lifetime과 reconnect recovery

**보완 필요** · P1 · static-only · 기존 항목

PocketBase: Disables server write deadline; defaults to 5-minute idle and 30-minute maximum lifetime. Reconnect creates a fresh client and requires subscription resubmission; no durable replay is promised.

PgBase: Legacy stream has a 30-minute context, refreshed write deadlines and durable cursor events. Multiplex stream polls each second, retains per-topic cursors and accepts numeric Last-Event-ID.

차이/남은 범위: Multiplex never clears the production 30-second WriteTimeout or implements PB idle/max timers. PB_CONNECT writes a UUID SSE id that its numeric reconnect parser rejects before the first record event. Expired multiplex cursors become generic PB_ERROR on polling. Existing legacy replay tests do not prove multiplex recovery.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime.go#L43)

PgBase 근거: [internal/api/realtime_sessions.go:171](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:171) / [internal/api/realtime_sessions.go:220](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:220) / [internal/api/realtime.go:190](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime.go:190) / [pgbase/app_construct.go:409](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app_construct.go:409) / [pgbase/managed.go:161](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/managed.go:161)

테스트/검증 근거: [pgbase/realtime_integration_test.go:282](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/realtime_integration_test.go:282) / [internal/realtime/service_integration_test.go:181](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service_integration_test.go:181) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime_test.go#L23)

## FIL-14 · 추가: record file Range·conditional·inline/download 응답

**보완 필요** · P2 · static-only · 추가 확인 항목

PocketBase: Record downloads call System.Serve, which uses http.ServeContent for Range and conditional requests, serves allowed media inline and accepts download=true to force attachment.

PgBase: Record file handlers copy the whole stream using io.Copy, set no-store and always use attachment disposition. Query validation rejects download.

차이/남은 범위: No partial-content/conditional behavior or inline-versus-download toggle on record files. Extension System.serve has a separate implementation and must not be used as evidence for these routes.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L544)

PgBase 근거: [internal/api/file_handlers.go:84](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:84) / [internal/api/file_handlers.go:211](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:211)

테스트/검증 근거: [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem_test.go#L934) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem_test.go#L972) / [pgbase/file_integration_test.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_integration_test.go:23)

## FIL-15 · 추가: EXIF orientation을 반영하는 thumbnail

**미구현** · P2 · static-only · 추가 확인 항목

PocketBase: CreateThumb decodes with imaging.AutoOrientation(true), then resizes the oriented image.

PgBase: Decodes standard image pixels and resamples them without processing EXIF orientation.

차이/남은 범위: Phone JPEGs whose display orientation is stored in EXIF can produce rotated thumbnails. No PgBase orientation fixture was found/read; no runtime reproduction was performed.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L643)

PgBase 근거: [internal/filestore/image.go:90](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/image.go:90) / [internal/filestore/variants.go:140](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants.go:140)

테스트/검증 근거: [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem_test.go#L1005) / [internal/filestore/variants_test.go:79](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants_test.go:79)

## FIL-16 · 추가: default 100x100 thumb와 original fallback

**보완 필요** · P2 · static-only · 추가 확인 항목

PocketBase: 100x100 is allowed independently of configured thumbs. Invalid/unconfigured sizes, unsupported formats or generation failure fall back to the original and expose ThumbError to the download hook.

PgBase: Open requires a permitted variant and propagates transform/open errors. The legacy thumbnail variant is configured separately; arbitrary 100x100 is not automatically permitted.

차이/남은 범위: PocketBase-compatible fallback and implicit 100x100 behavior are absent. This affects old file URLs that rely on fallback rather than errors.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file.go#L164)

PgBase 근거: [internal/filestore/service.go:380](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:380) / [internal/filestore/service.go:416](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:416) / [internal/filestore/variants.go:104](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants.go:104)

테스트/검증 근거: [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/file_test.go#L92) / [internal/filestore/variants_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/variants_test.go:15)

## FIL-17 · 추가: $filesystem.s3 인자로 독립 storage 선택

**미구현** · P1 · static-only · 추가 확인 항목

PocketBase: NewS3 constructs a filesystem for the supplied bucket, region, endpoint, credentials and path-style flag.

PgBase: The JS s3 function accepts those arguments but returns objectSystem() without using any of them. Operations use the configured namespace-backed object store.

차이/남은 범위: Calling the method with another target does not access that target and can instead affect current configured storage. Existing on-S3 tests verify configured backend operation, not argument handling.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L66)

PgBase 근거: [internal/jsvm/pocketbase_filesystem_globals.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_filesystem_globals.go:15) / [pgbase/extension_objects.go:424](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_objects.go:424)

테스트/검증 근거: [pgbase/javascript_filesystem_serve_integration_test.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_filesystem_serve_integration_test.go:22)

## FIL-18 · 추가: System.list 전체 prefix inventory

**보완 필요** · P2 · static-only · 추가 확인 항목

PocketBase: System.List iterates until EOF and returns every matching object.

PgBase: Go ExtensionObjects.List and top-level $filesystem.list expose pagination; System.list requests only the first 100 and returns the object array without nextCursor.

차이/남은 범위: The System-shaped method silently truncates inventories above 100 objects. DeletePrefix is a separate host path that does paginate; this finding does not allege its 100-object truncation.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/filesystem/filesystem.go#L233)

PgBase 근거: [internal/jsvm/pocketbase_filesystem_globals.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_filesystem_globals.go:12) / [internal/jsvm/pocketbase_filesystem_globals.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_filesystem_globals.go:17) / [pgbase/extension_objects.go:364](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_objects.go:364)

테스트/검증 근거: [pgbase/extension_objects_integration_test.go:391](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_objects_integration_test.go:391)

## FIL-19 · 추가: 잘못된 standalone multipart upload의 publication 원자성

**보완 필요** · P2 · static-only · 추가 확인 항목

PocketBase: Record request parsing gathers multipart files before record saving; FileField interceptors clean up uploaded bytes on execution failure.

PgBase: Standalone file upload publishes through service.Upload before checking that the multipart body has reached EOF. An extra/malformed part triggers compensating Delete whose error is ignored.

차이/남은 범위: A deletion veto or changed authorization can leave a published attachment despite a rejected malformed request. This is a source-derived failure path, not a reproduced failure, and does not invalidate the separate atomic batch implementation.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L690)

PgBase 근거: [internal/api/file_handlers.go:192](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:192) / [internal/api/file_handlers.go:200](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/file_handlers.go:200) / [internal/filestore/service.go:447](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/filestore/service.go:447)

테스트/검증 근거: [pgbase/file_integration_test.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/file_integration_test.go:23) / [pgbase/batch_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/batch_integration_test.go:17) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_file_test.go#L832)

## RT-08 · 추가: record topic은 viewRule, collection topic은 listRule

**보완 필요** · P1 · static-only · 추가 확인 항목

PocketBase: The subscription rule map selects ViewRule for collection/recordId and ListRule for collection/* or collection.

PgBase: eventQuery compiles List AND View regardless of request.PublicID/RecordID.

차이/남은 범위: A record with a public/allowed view rule but locked list rule cannot emit to its record topic; a collection subscription can also be stricter than PB when only list allows access. No test covering distinct list/view permission combinations was found in the cited realtime tests.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime.go#L606)

PgBase 근거: [internal/realtime/service.go:233](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service.go:233) / [internal/realtime/service.go:243](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service.go:243)

테스트/검증 근거: [internal/realtime/service_integration_test.go:42](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service_integration_test.go:42) / [pgbase/realtime_integration_test.go:336](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/realtime_integration_test.go:336)

## RT-09 · 추가: subscription options의 query·headers request context

**보완 필요** · P2 · static-only · 추가 확인 항목

PocketBase: Client.Subscribe parses options={query:{...},headers:{...}}, normalizes header names and supplies both maps to realtime access checks.

PgBase: Topic parser permits only direct filter,expand,fields query keys. Delivery uses the connection HTTP request's rule context.

차이/남은 범위: PB SDK subscription options/header-dependent rules cannot be represented per topic. Direct filter/expand/fields alone do not establish equivalent request context.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/subscriptions/client.go#L176)

PgBase 근거: [internal/api/realtime_sessions.go:509](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:509) / [internal/api/realtime_sessions.go:266](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:266)

테스트/검증 근거: [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/subscriptions/client_test.go#L116) / [pgbase/realtime_integration_test.go:133](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/realtime_integration_test.go:133)

## RT-10 · 추가: durable replay와 multiplex 전역 cursor 순서

**보완 필요** · P1 · static-only · 추가 확인 항목

PocketBase: PocketBase sends live client-channel messages with client IDs and does not provide a durable ordered replay cursor.

PgBase: PgBase adds a durable outbox, fenced delivery sequence and 24-hour retention. Database readers order each topic separately; multiplex emits each subscription's whole batch before proceeding to the next.

차이/남은 범위: Added PgBase capability has an unverified global resume guarantee: e.g. first topic emits seq20 before second topic's pending seq10; disconnect at20 and reconnect with one shared cursor can skip10. Conversely later smaller IDs can replay already delivered higher events. DB single-topic ordering tests do not cover this multiplex case.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/subscriptions/message.go#L20)

PgBase 근거: [internal/realtime/outbox.go:62](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/outbox.go:62) / [internal/realtime/outbox.go:99](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/outbox.go:99) / [internal/realtime/service.go:280](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service.go:280) / [internal/api/realtime_sessions.go:255](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:255) / [internal/api/realtime_sessions.go:318](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:318)

테스트/검증 근거: [internal/realtime/service_integration_test.go:136](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service_integration_test.go:136) / [internal/realtime/service_integration_test.go:181](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service_integration_test.go:181) / [pgbase/realtime_integration_test.go:282](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/realtime_integration_test.go:282)

## RT-11 · 추가: 인증 해제 후 연결·재인증 semantics

**대응 구현·설계 차이** · none · test-exists · 추가 확인 항목

PocketBase: Auth-record deletion/token-key/collection-secret change clears client auth but leaves the socket active; subsequent subscribe can authenticate again.

PgBase: Multiplex revalidates its credential each polling tick and disconnects on loss; reconnect obtains a fresh client. Broker enqueue also checks credentials.

차이/남은 범위: Intentional disconnect rather than downgrade-to-guest. Authorization is checked before broker enqueue, not again in the custom-message write branch; do not infer a stronger immediate post-revocation wire guarantee from the enqueue-only test.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime.go#L251)

PgBase 근거: [internal/api/realtime_sessions.go:247](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:247) / [internal/api/realtime_sessions.go:488](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:488) / [internal/api/realtime_sessions.go:59](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:59)

테스트/검증 근거: [pgbase/realtime_integration_test.go:133](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/realtime_integration_test.go:133) / [internal/api/realtime_sessions_test.go:160](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions_test.go:160) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime_test.go#L675)

## RT-12 · 추가: collection ID alias로 realtime 구독

**보완 필요** · P2 · static-only · 추가 확인 항목

PocketBase: Record broadcast matches both collection name and collection ID topic prefixes.

PgBase: ReadFor looks up collections WHERE name=$1 and the topic parser passes the raw first segment through.

차이/남은 범위: UUID collection IDs are not resolved as collection aliases even though PB accepts IDs. Exact text record identity tests exercise record identity, not collection aliases.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime.go#L606)

PgBase 근거: [internal/api/realtime_sessions.go:516](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/realtime_sessions.go:516) / [internal/realtime/service.go:94](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service.go:94)

테스트/검증 근거: [internal/realtime/service_integration_test.go:42](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service_integration_test.go:42) / [pgbase/realtime_integration_test.go:133](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/realtime_integration_test.go:133)

## RT-13 · 추가: superuser realtime hidden-field enrichment

**보완 필요** · P2 · static-only · 추가 확인 항목

PocketBase: Before serialization, superusers explicitly unhide collection fields; auth owner/superuser/manager email visibility rules and enrich hooks are applied.

PgBase: Administrator bypasses event ACL and hidden-field projection validation, but publicSnapshot unconditionally removes every hidden field before any output projection.

차이/남은 범위: Administrator subscribers cannot receive hidden fields like PB superusers; accepting a requested hidden field in validation does not restore it in the payload. Auth-record-specific enrich parity also is not established by the snapshot tests.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime.go#L667)

PgBase 근거: [internal/realtime/service.go:245](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service.go:245) / [internal/realtime/service.go:304](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service.go:304) / [internal/realtime/service.go:348](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/service.go:348)

테스트/검증 근거: [internal/realtime/snapshot_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/snapshot_test.go:13) / [internal/realtime/snapshot_test.go:34](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/realtime/snapshot_test.go:34) / [공식 테스트](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/realtime_test.go#L560)

## EXT-01 · 애플리케이션 임베딩과 시작 수명주기

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: BaseApp.Bootstrap/OnBootstrap와 앱 임베딩으로 저장소·설정·마이그레이션 초기화 및 서버 수명주기를 제어한다.

PgBase: Go App.New/Start/Run/Close 및 before/after 단계 훅으로 임베딩과 시작·종료를 제공한다. PostgreSQL 연결/설정은 명시적 옵션 계약이다.

차이/남은 범위: 좁은 시작·종료 기능은 구현됨. PB의 ClearBootstrap/OnBootstrapClear는 별도 EXT-19 공백이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go#L412)

PgBase 근거: [pgbase/app.go:303](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app.go:303) / [pgbase/app.go:315](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app.go:315) / [pgbase/app.go:347](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app.go:347) / [pgbase/app.go:507](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app.go:507)

테스트/검증 근거: [pgbase/app_lifecycle_hooks_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app_lifecycle_hooks_test.go:13) / [pgbase/app_lifecycle_hooks_test.go:42](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app_lifecycle_hooks_test.go:42) / [pgbase/app_lifecycle_hooks_test.go:75](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app_lifecycle_hooks_test.go:75)

## EXT-02 · 사용자 정의 HTTP 라우트

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: OnServe Router에 method/path/handler 및 middleware를 등록하고 RequestEvent로 응답한다.

PgBase: Go net/http CustomRoute를 시작 시 등록하며 명시적 인증 모드, 경로 예약 영역, 제한 수를 검증한다.

차이/남은 범위: 라우트 기능은 구현됨. 시작 시 등록과 net/http API는 의도적 차이이며 JS RequestEvent의 누락은 EXT-22로 분리한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/binds.go#L150)

PgBase 근거: [pgbase/custom_routes.go:104](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:104) / [pgbase/custom_routes.go:111](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:111)

테스트/검증 근거: [pgbase/custom_routes_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes_integration_test.go:16) / [pgbase/route_groups_test.go:10](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/route_groups_test.go:10)

## EXT-03 · 라우트 그룹·미들웨어·스트리밍 응답

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: 라우트 그룹은 prefix와 middleware를 누적하며 Event는 JSON/Blob/Stream/Flush 등 응답을 제공한다.

PgBase: Go RouteGroup prefix/Use/Handle/Stream/Static과 net/http middleware 체인 및 실제 flush를 제공한다.

차이/남은 범위: Go 라우팅 기능 의도는 충족. PB router.Event API 호환을 의미하지 않으며 JS 임의 stream/flush는 EXT-22 범위다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/router/group.go#L33)

PgBase 근거: [pgbase/custom_routes.go:27](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:27) / [pgbase/custom_routes.go:33](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:33) / [pgbase/custom_routes.go:41](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:41) / [pgbase/custom_routes.go:52](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:52) / [pgbase/custom_routes.go:57](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes.go:57)

테스트/검증 근거: [pgbase/route_groups_test.go:10](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/route_groups_test.go:10) / [pgbase/custom_routes_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/custom_routes_integration_test.go:16)

## EXT-04 · 프로그래밍 방식 UIExtension 등록

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: UIExtension main.js 및 정적 파일을 app에 등록하고 관리자 UI 경로에서 로드한다.

PgBase: fs.FS 기반 UIExtension과 이름/경로 검증, 관리자 UI 확장 loader 및 정적 자원을 제공한다.

차이/남은 범위: 등록·서빙 의도는 구현됨. PgBase 관리자 UI 경로와 배포 계약을 따른다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/extensions.go#L19)

PgBase 근거: [pgbase/ui_extension.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/ui_extension.go:13) / [pgbase/ui_extension.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/ui_extension.go:18)

테스트/검증 근거: [pgbase/ui_extension_integration_test.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/ui_extension_integration_test.go:18)

## EXT-05 · 우선순위·등록/해제 가능한 훅 체인

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: 우선순위가 있는 Handler 체인에서 e.Next가 다음 핸들러/기본 실행을 실제 호출한다. 호출 전후 처리가 가능하고 Next를 생략하면 실행을 중단한다.

PgBase: Go registry는 우선순위/해제/동시 스냅샷 실행을 제공하지만 before/after 순차 호출 구조다. JS e.next는 bool만 변경하고 그 값은 버린다.

차이/남은 범위: Next 안에서 downstream 결과를 기다리는 around middleware, Next 미호출 short circuit, downstream 오류 전파 계약이 없다. 단순 호출 가능성을 호환 완료로 볼 수 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/hook/hook.go#L146)

PgBase 근거: [internal/lifecycle/registry.go:49](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/lifecycle/registry.go:49) / [internal/lifecycle/registry.go:91](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/lifecycle/registry.go:91) / [internal/lifecycle/registry.go:124](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/lifecycle/registry.go:124) / [internal/jsvm/registration.go:221](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:221)

테스트/검증 근거: [internal/lifecycle/registry_test.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/lifecycle/registry_test.go:11) / [internal/lifecycle/registry_test.go:33](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/lifecycle/registry_test.go:33) / [internal/jsvm/registration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration_test.go:12)

## EXT-06 · 임의 Model CRUD 단계 훅

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: 일반 Model Save/Delete/Validate가 PreValidator/PostValidator 및 model CRUD execute/success/error 훅을 호출한다. 트랜잭션 내 after-success/error는 최종 트랜잭션 완료와 연결된다.

PgBase: RunModelMutation으로 사용자 콜백을 validate/execute/success/error 단계로 감싸고 record/collection 모델 facade를 제공한다. record 전용 결과 훅은 최종 앱 트랜잭션 완료에 연결돼 있다.

차이/남은 범위: 임의 테이블의 일반 Model ORM, DBExporter/PreValidator/PostValidator 통합과 generic model의 외부 트랜잭션 최종 완료 콜백은 없다. wrapper 호출 후 success가 곧 outer commit 성공을 뜻하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/db.go#L239)

PgBase 근거: [pgbase/model_lifecycle.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle.go:30) / [pgbase/model_lifecycle.go:71](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle.go:71) / [pgbase/model_lifecycle.go:92](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle.go:92) / [pgbase/extension_bridge.go:429](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_bridge.go:429) / [pgbase/extension_app_transaction.go:84](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_transaction.go:84)

테스트/검증 근거: [pgbase/model_lifecycle_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle_test.go:12) / [pgbase/model_lifecycle_test.go:33](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle_test.go:33) / [pgbase/model_lifecycle_test.go:86](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/model_lifecycle_test.go:86) / [pgbase/extension_app_transaction_integration_test.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_transaction_integration_test.go:18)

## EXT-07 · record·collection 요청/변경 훅

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: OnRecord*/OnCollection* 요청·검증·실행·결과 및 enrich 훅을 앱에서 노출하고 태그로 collection을 제한한다.

PgBase: record/collection 요청 및 mutation 단계, 공개 auth 요청, collection 태그, record enrich를 제공한다. JSON payload와 허용된 patch 경로를 통해서만 변경한다.

차이/남은 범위: 명명된 단계와 수정 기능은 구현됨. live Go event 대신 검증된 JSON patch를 쓰는 차이이며 Next continuation 미구현은 EXT-05에 기록한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/binds.go#L43)

PgBase 근거: [pgbase/lifecycle_hooks.go:78](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks.go:78) / [pgbase/lifecycle_hooks.go:161](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks.go:161) / [pgbase/javascript_hooks.go:97](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_hooks.go:97) / [pgbase/javascript_hooks.go:122](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_hooks.go:122) / [pgbase/javascript_hooks.go:202](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_hooks.go:202)

테스트/검증 근거: [pgbase/lifecycle_hooks_integration_test.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks_integration_test.go:18) / [pgbase/lifecycle_hooks_integration_test.go:259](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks_integration_test.go:259) / [pgbase/javascript_collection_enrich_hooks_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_collection_enrich_hooks_integration_test.go:12) / [pgbase/javascript_collection_enrich_hooks_integration_test.go:78](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_collection_enrich_hooks_integration_test.go:78) / [pgbase/javascript_model_phase_hooks_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_model_phase_hooks_integration_test.go:12)

## EXT-08 · auth·mailer·file·realtime·backup 훅 범위

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: 앱 인터페이스에 수명주기와 auth/mailer/file/realtime/backup 등 영역별 이벤트 훅을 제공한다.

PgBase: 영역별 before/after 이벤트와 veto/error 관찰을 lifecycle registry 및 JS manifest로 노출한다.

차이/남은 범위: 분야별 훅은 구현됨. payload/동기 veto 범위는 PgBase 계약이며 개별 PB 이벤트의 live method 전체가 동일하다는 뜻은 아니다. ClearBootstrap 신규 이벤트는 EXT-19 별도.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/app.go#L739)

PgBase 근거: [pgbase/lifecycle_hooks.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks.go:22) / [pgbase/javascript_hooks.go:73](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_hooks.go:73) / [pgbase/javascript_hooks.go:89](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_hooks.go:89)

테스트/검증 근거: [pgbase/auth_lifecycle_hooks_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/auth_lifecycle_hooks_test.go:13) / [pgbase/auth_lifecycle_hooks_integration_test.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/auth_lifecycle_hooks_integration_test.go:14) / [pgbase/lifecycle_hooks_integration_test.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks_integration_test.go:18)

## EXT-09 · 권한을 상속하는 record CRUD/list/batch helper

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: PB app의 record 조회/저장과 CanAccessRecord는 requestInfo 및 rule을 별도 적용할 수 있는 직접 앱 API다.

PgBase: ExtensionContext와 pgbase.records facade는 호출 context/principal/capability를 물려받아 domain service의 CRUD/list/batch를 실행한다.

차이/남은 범위: CRUD/list/batch 기능 의도는 구현됨. PB app ORM이 자동으로 HTTP 권한을 적용한다는 비교는 부정확하다. 정확한 PB query 인자/결과 호환 공백은 EXT-21.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_query.go#L599)

PgBase 근거: [pgbase/extensions.go:20](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extensions.go:20) / [pgbase/extensions.go:31](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extensions.go:31) / [pgbase/extensions.go:86](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extensions.go:86) / [pgbase/extensions.go:100](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extensions.go:100) / [pgbase/extensions.go:113](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extensions.go:113) / [pgbase/extension_bridge.go:530](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_bridge.go:530)

테스트/검증 근거: [pgbase/javascript_records_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_records_integration_test.go:16)

## EXT-10 · raw DB builder와 일반 transaction helper

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: 앱 DB는 dbx builder이고 RunInTransaction은 tx app을 전달한다. 이미 transaction이면 기존 tx를 재사용한다.

PgBase: Go TrustedDatabase는 Exec/Query/QueryRow와 RunInTransaction을 제공하며 Go 중첩은 pgx savepoint를 사용한다. JS app.runInTransaction은 실제 DB transaction에 record 작업을 묶고 중첩은 같은 app/tx를 재사용한다.

차이/남은 범위: 트랜잭션은 실제 구현됨. PB dbx NewQuery/Select/Join/Bind/Scan 등의 전체 builder가 없고 반환 shape도 다르다. JS 중첩 savepoint 부재는 PB 공백이 아니다. txInfo.onComplete는 EXT-20.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/db_tx.go#L14)

PgBase 근거: [pgbase/trusted_database.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/trusted_database.go:30) / [pgbase/trusted_database.go:50](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/trusted_database.go:50) / [pgbase/trusted_database.go:82](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/trusted_database.go:82) / [internal/jsvm/pocketbase_globals.go:445](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:445) / [internal/jsvm/pocketbase_globals.go:446](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:446) / [pgbase/extension_app_transaction.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_transaction.go:23)

테스트/검증 근거: [pgbase/extension_app_transaction_integration_test.go:68](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_transaction_integration_test.go:68) / [pgbase/javascript_database_objects_integration_test.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_database_objects_integration_test.go:14) / [internal/jsvm/pocketbase_globals_test.go:124](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals_test.go:124)

## EXT-11 · mailer·file·template helper

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: 앱 mailer/filesystem과 template Registry의 AddFuncs/LoadFiles/LoadString/LoadFS/Render로 템플릿을 동적으로 만들고 렌더한다.

PgBase: 실제 메일 발송·파일 작업, Go에서 사전 등록한 html/template의 shared render helper가 있다.

차이/남은 범위: 동적 template registry와 custom FuncMap, JS $template 객체가 없다. 사전 등록 template.render만으로 PB template 기능 전체를 충족하지 않는다. filesystem 세부 차이는 EXT-14 및 파일 영역 참조.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/template/registry.go#L73)

PgBase 근거: [pgbase/templates.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/templates.go:18) / [pgbase/templates.go:35](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/templates.go:35) / [internal/jsvm/pocketbase_filesystem_globals.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_filesystem_globals.go:12)

테스트/검증 근거: [pgbase/javascript_file_mail_integration_test.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_file_mail_integration_test.go:22) / [pgbase/javascript_template_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_template_integration_test.go:12) / [pgbase/templates_test.go:5](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/templates_test.go:5)

## EXT-12 · 스크립트 발견·등록 모델과 TypeScript 입력

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: HooksDir의 .pb.js/.pb.ts를 발견해 로드하고 등록한다. PB .pb.ts 확장자는 IDE/린터용이고 Goja는 실제 TS 문법을 변환하지 않는다.

PgBase: operator manifest에서 route/hook/job 및 capability를 선언하고 immutable bundle을 로드한다. .ts/.tsx 입력은 esbuild로 변환한다.

차이/남은 범위: 발견·등록을 명시적 manifest로 바꾼 의도적 실행 모델. PB 소스 파일을 그대로 복사하는 호환이 아니며 TS 변환은 PgBase 추가 기능.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/jsvm.go#L59)

PgBase 근거: [internal/jsvm/bundle.go:165](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/bundle.go:165) / [internal/jsvm/bundle.go:229](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/bundle.go:229) / [internal/jsvm/registration.go:85](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:85)

테스트/검증 근거: [internal/jsvm/runtime_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime_test.go:16) / [internal/jsvm/bundle_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/bundle_test.go:13) / [internal/jsvm/registration_test.go:43](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration_test.go:43)

## EXT-13 · CommonJS 로컬 module 지원

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: goja_nodejs require.Registry를 이용해 로컬 CommonJS module을 require한다.

PgBase: 미리 snapshot한 로컬 module graph를 경로 경계와 module 수/크기 제한 아래 실행하며 각 invocation은 fresh VM이다.

차이/남은 범위: 로컬 CommonJS 의도는 구현됨. 외부/동적 비선언 module 탐색, Node 전체 런타임, VM 간 전역 상태 공유는 의도적으로 제공하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/jsvm.go#L196)

PgBase 근거: [internal/jsvm/bundle.go:165](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/bundle.go:165) / [internal/jsvm/runtime.go:354](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime.go:354) / [internal/jsvm/runtime.go:593](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime.go:593)

테스트/검증 근거: [internal/jsvm/runtime_test.go:86](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime_test.go:86) / [internal/jsvm/bundle_test.go:93](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/bundle_test.go:93) / [internal/jsvm/bundle_test.go:118](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/bundle_test.go:118)

## EXT-14 · $app/$dbx/$security/$os/$http/$filesystem/$apis globals

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: Go 앱과 dbx/security/os/http/filesystem/apis 기능을 VM에 binding한다. filesystem.NewS3는 주어진 endpoint/bucket/credential로 독립 클라이언트를 만든다.

PgBase: 다수 실제 capability host adapter가 존재하며 최종 Object.assign과 filesystem 설치가 초기 placeholder를 덮어쓴다. $filesystem.s3도 실제 configured object storage를 반환한다.

차이/남은 범위: 이름 존재만으로 계약 동등은 아니다. S3 생성자는 모든 인자를 무시하고 operator storage를 사용하며 setContext는 no-op, system.list는 100개 제한/no cursor다. fluent DB, template registry 등도 부분집합. S3 미지원이라는 초기 placeholder 판정은 틀리다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/binds.go#L312)

PgBase 근거: [internal/jsvm/pocketbase_globals.go:445](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:445) / [internal/jsvm/pocketbase_globals.go:450](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:450) / [internal/jsvm/pocketbase_globals.go:462](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:462) / [internal/jsvm/pocketbase_globals.go:463](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:463) / [internal/jsvm/pocketbase_filesystem_globals.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_filesystem_globals.go:12) / [internal/jsvm/pocketbase_filesystem_globals.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_filesystem_globals.go:15)

테스트/검증 근거: [internal/jsvm/pocketbase_globals_test.go:32](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals_test.go:32) / [internal/jsvm/pocketbase_globals_test.go:102](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals_test.go:102) / [pgbase/extension_compatibility_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_compatibility_test.go:15) / [pgbase/extension_os_shapes_test.go:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_os_shapes_test.go:15) / [pgbase/javascript_compatibility_conformance_integration_test.go:61](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_compatibility_conformance_integration_test.go:61)

## EXT-15 · JS route·hook 등록

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: routerAdd/routerUse와 onX 등록이 Go router 및 live event hook에 직접 연결된다.

PgBase: routerAdd/routerUse/onX를 manifest slot에 재생하고 handler closure를 보존한다. 실행 시작 후 등록은 freeze되며 JSON 입력/응답과 wrapper middleware를 사용한다.

차이/남은 범위: 명시적 slot/freeze는 의도적 등록 계약이지만 PB handler의 e.Next/live event 계약을 실행하지 못한다. 실제 continuation 공백 EXT-05와 RequestEvent 공백 EXT-22를 포함하므로 PB 1:1로는 partial.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/binds.go#L150)

PgBase 근거: [internal/jsvm/registration.go:85](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:85) / [internal/jsvm/registration.go:131](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:131) / [internal/jsvm/registration.go:141](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:141) / [internal/jsvm/registration.go:221](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:221)

테스트/검증 근거: [pgbase/javascript_bootstrap_registration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_bootstrap_registration_test.go:16) / [internal/jsvm/registration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration_test.go:12) / [internal/jsvm/registration_test.go:43](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration_test.go:43) / [internal/jsvm/registration_test.go:56](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration_test.go:56)

## EXT-16 · JS scheduled job/cron

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: cronAdd/cronRemove는 loader와 실행 VM 양쪽에 노출되어 실행 중에도 앱의 cron job을 추가/교체/제거한다.

PgBase: operator가 사전 선언한 durable job을 cronAdd로 연결하고 service subject 아래 실행하며 재시도/idempotency를 지원한다. cronRemove는 predeclared job을 제거하지 못한다.

차이/남은 범위: durable scheduled job은 구현됐다. 실행 중 동적 job 생성/교체/삭제라는 PB cron API 동작은 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/binds.go#L105)

PgBase 근거: [internal/jsvm/registration.go:151](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:151) / [internal/jsvm/registration.go:177](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/registration.go:177) / [pgbase/javascript_jobs.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_jobs.go:24) / [pgbase/extension_app_control.go:169](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_control.go:169)

테스트/검증 근거: [pgbase/javascript_jobs_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_jobs_integration_test.go:16) / [pgbase/javascript_jobs_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_jobs_test.go:12)

## EXT-17 · namespaced record·mail·file·log capability

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: PB는 $app 및 관련 bindings로 서버 권한을 가진 trusted script에서 CRUD/mail/file/log 기능을 호출한다.

PgBase: require('pgbase')에 record/mail/file/log host facade가 있고 manifest capabilities·호출 principal·파일 namespace를 적용한다.

차이/남은 범위: 작업 기능은 구현됨. capability/namespace는 PgBase의 명시적 실행 정책이며 PB 기능 이름과 완전한 API 동일성을 뜻하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/jsvm.go#L207)

PgBase 근거: [pgbase/extension_bridge.go:530](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_bridge.go:530) / [internal/jsvm/runtime.go:593](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime.go:593) / [internal/jsvm/runtime.go:635](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime.go:635)

테스트/검증 근거: [pgbase/javascript_records_integration_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_records_integration_test.go:16) / [pgbase/javascript_file_mail_integration_test.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_file_mail_integration_test.go:22) / [internal/jsvm/pocketbase_globals_test.go:61](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals_test.go:61)

## EXT-18 · 개발 reload·타입 선언·custom VM binding·template

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: HooksWatch reload, 타입 선언 생성, OnInit custom VM binding과 $template Registry를 제공한다.

PgBase: CLI check/types/watch, 유효 generation 유지, JavaScriptOptions.OnInit 및 precompiled template.render를 제공한다.

차이/남은 범위: reload/types/OnInit 기능은 존재한다. 이 복합 행의 PB $template Registry 동적 LoadFiles/LoadString/AddFuncs는 없다. native Windows release 검증은 이 정적 대조에서 재실행하지 않았다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/jsvm/jsvm.go#L59)

PgBase 근거: [internal/cli/javascript.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript.go:18) / [internal/cli/javascript_watch.go:25](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript_watch.go:25) / [internal/cli/javascript_watch.go:54](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript_watch.go:54) / [pgbase/javascript_options.go:43](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_options.go:43) / [pgbase/templates.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/templates.go:18)

테스트/검증 근거: [internal/cli/javascript_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript_test.go:12) / [internal/cli/javascript_watch_integration_test.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript_watch_integration_test.go:24) / [internal/jsvm/runtime_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime_test.go:16) / [pgbase/javascript_template_integration_test.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_template_integration_test.go:12)

## MIG-01 · Go·JavaScript 수동 migration 등록

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: Go MigrationsList.Register 또는 JS migrate(up,down)로 파일명 순서와 txApp 콜백을 등록한다.

PgBase: Go UserMigration source+callbacks, JavaScriptMigrationBundle의 고정 export를 등록한다. JS는 migration 전용 exec/query/applySnapshot bridge를 전달받는다.

차이/남은 범위: 수동 migration 등록·실행 의도는 구현됨. 전체 txApp 대신 migration 전용 API와 source checksum을 사용하는 의도적 계약 차이.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_list.go#L61)

PgBase 근거: [pgbase/user_migrations.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/user_migrations.go:24) / [pgbase/user_migrations.go:64](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/user_migrations.go:64) / [pgbase/user_migrations.go:89](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/user_migrations.go:89) / [pgbase/javascript_migrations.go:29](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migrations.go:29) / [pgbase/javascript_migrations.go:58](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migrations.go:58) / [pgbase/javascript_migrations.go:79](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migrations.go:79)

테스트/검증 근거: [pgbase/javascript_migrations_integration_test.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migrations_integration_test.go:13) / [pgbase/javascript_migration_acceptance_integration_test.go:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migration_acceptance_integration_test.go:21) / [internal/cli/javascript_migration_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/javascript_migration_integration_test.go:17)

## MIG-02 · up 실행과 원자적 rollback

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: 한 번의 Up 호출에 속한 모든 미적용 migration을 중첩 aux/data transaction 안에서 실행한다. 뒤 migration이 실패하면 이번 호출의 앞 migration도 rollback된다.

PgBase: 각 migration의 DDL/data/ledger/schema-version은 applyOne의 한 transaction이다. Up은 applyOne을 반복하므로 먼저 성공한 migration은 이미 commit된다.

차이/남은 범위: 단일 migration 실패 원자성은 구현됐으나 PB와 같은 전체 pending batch 원자성이 없다. 현재 테스트는 단일 실패 rollback을 검증하며 multi-migration batch 계약을 보장하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_runner.go#L129)

PgBase 근거: [internal/usermigrate/runner.go:249](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:249) / [internal/usermigrate/runner.go:296](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:296) / [internal/usermigrate/runner.go:300](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:300) / [internal/usermigrate/runner.go:361](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:361)

테스트/검증 근거: [internal/usermigrate/runner_integration_test.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner_integration_test.go:30) / [internal/usermigrate/runner_integration_test.go:121](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner_integration_test.go:121)

## MIG-03 · down rollback 개수와 irreversible 표시

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: Down(number)는 최근 N개를 한 transaction에서 되돌린다. Down 콜백이 nil이면 콜백 없이 history를 제거한다.

PgBase: DownN은 N개/음수 전체/0 no-op이며 migration별 transaction을 사용한다. irreversible down은 명시적 오류로 거부한다.

차이/남은 범위: 개수와 irreversible 표시는 구현됐다. irreversible 명시적 거부와 음수 전체 처리 등은 PgBase 계약이지만 여러 down의 전체 transaction 원자성은 PB와 다르며 뒤 실패 때 이미 완료한 rollback을 복구하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_runner.go#L178)

PgBase 근거: [internal/usermigrate/runner.go:276](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:276) / [internal/usermigrate/runner.go:279](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:279) / [internal/usermigrate/runner.go:296](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:296)

테스트/검증 근거: [internal/usermigrate/snapshot_integration_test.go:223](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:223) / [internal/usermigrate/snapshot_integration_test.go:277](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:277) / [internal/usermigrate/runner_integration_test.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner_integration_test.go:30)

## MIG-04 · 동시 runner fencing

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: PB SQLite transaction과 migration ledger를 통해 적용 상태 읽기/쓰기를 직렬화한다.

PgBase: PostgreSQL transaction advisory lock 두 개와 schema_state FOR UPDATE를 잡고 callback 및 ledger를 처리한다.

차이/남은 범위: PostgreSQL용 병행 runner 제어 구현과 테스트가 있다. PB의 DB 잠금 방식과 동일 구현일 필요는 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_runner.go#L129)

PgBase 근거: [internal/usermigrate/runner.go:300](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:300) / [internal/usermigrate/runner.go:304](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:304) / [internal/usermigrate/runner.go:308](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:308)

테스트/검증 근거: [internal/usermigrate/runner_integration_test.go:180](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner_integration_test.go:180) / [internal/usermigrate/schema_concurrency_integration_test.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/schema_concurrency_integration_test.go:17) / [pgbase/javascript_migration_acceptance_integration_test.go:163](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_migration_acceptance_integration_test.go:163)

## MIG-05 · history와 source checksum 상태

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: PB history는 file/applied를 저장하고 파일명 기준으로 적용 여부를 찾는다.

PgBase: source revision hash, applied history 및 manifest prefix를 검증하고 source 변경·history gap·history-only 상태를 보고한다.

차이/남은 범위: history 의도는 구현됨. checksum/prefix 검증은 PB보다 추가된 제약이며 PB 자체가 source checksum을 제공한다는 기존 행 표현은 부정확하다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_runner.go#L245)

PgBase 근거: [pgbase/user_migrations.go:89](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/user_migrations.go:89) / [internal/usermigrate/runner.go:49](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:49) / [internal/usermigrate/runner.go:228](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:228) / [internal/usermigrate/runner.go:422](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:422) / [internal/usermigrate/runner.go:434](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:434)

테스트/검증 근거: [internal/usermigrate/runner_integration_test.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner_integration_test.go:30) / [internal/usermigrate/snapshot_integration_test.go:382](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:382)

## MIG-06 · source에서 제거된 migration 이력 정리

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: RemoveMissingAppliedMigrations는 등록 목록에 없는 migration의 history entry를 지운다.

PgBase: CleanupHistory는 source가 사라진 이력만 preview/삭제하고 transaction으로 실행한다. 별도 HistorySync는 증명된 snapshot의 baseline 등록 기능이다.

차이/남은 범위: 이력 정리 기능은 구현됨. PB history-sync 명칭과 PgBase HistorySync의 의미를 혼동하면 안 된다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_runner.go#L229)

PgBase 근거: [internal/usermigrate/runner.go:106](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:106) / [internal/usermigrate/runner.go:163](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:163) / [pgbase/user_migrations.go:148](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/user_migrations.go:148)

테스트/검증 근거: [internal/usermigrate/runner_integration_test.go:266](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner_integration_test.go:266) / [internal/usermigrate/snapshot_integration_test.go:312](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:312)

## MIG-07 · 관리자 schema 변경 자동 migration 기록

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: 관리자 collection 변경 후 TemplateLang 설정에 맞춰 Go 또는 JS migration diff source를 만들고 ledger에 적용 상태를 기록한다.

PgBase: 관리자 schema 변경을 canonical snapshot 기반 executable Go migration으로 기록하고 파일 materialization을 durable pending record에서 복구한다.

차이/남은 범위: 자동 기록/복구는 구현됨. JS project를 위한 JS migration source 자동 생성 옵션은 없다. 수동 JS migration 지원과 자동 JS 생성 지원은 별개다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/migratecmd/automigrate.go#L18)

PgBase 근거: [internal/usermigrate/recorder.go:56](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder.go:56) / [internal/usermigrate/recorder.go:63](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder.go:63) / [internal/usermigrate/recorder.go:111](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder.go:111) / [internal/usermigrate/recorder.go:239](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder.go:239)

테스트/검증 근거: [internal/usermigrate/recorder_integration_test.go:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder_integration_test.go:21) / [internal/usermigrate/recorder_integration_test.go:115](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder_integration_test.go:115) / [internal/usermigrate/recorder_integration_test.go:186](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/recorder_integration_test.go:186)

## MIG-08 · collection snapshot·diff 생성과 data-loss 분석

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: PB collections snapshot 및 diff를 Go/JS migration source로 생성하여 importCollections로 재현한다.

PgBase: canonical snapshot/diff와 data-loss 분석을 만들고 실행 가능한 Go up/down source를 생성한다. 파괴적 변경은 irreversible down으로 표시한다.

차이/남은 범위: snapshot/diff 및 위험 분석 의도는 구현됨. 생성언어 JS 공백은 MIG-07에 명시했고 PB보다 추가된 data-loss 판정은 동일성의 근거가 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/migratecmd/templates.go#L325)

PgBase 근거: [internal/usermigrate/generate.go:30](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/generate.go:30) / [internal/usermigrate/generate.go:76](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/generate.go:76) / [internal/usermigrate/generate.go:98](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/generate.go:98) / [internal/usermigrate/generate.go:126](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/generate.go:126)

테스트/검증 근거: [internal/usermigrate/snapshot_integration_test.go:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:26) / [internal/usermigrate/snapshot_integration_test.go:147](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:147) / [internal/usermigrate/snapshot_integration_test.go:191](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/snapshot_integration_test.go:191) / [internal/cli/user_migration_test.go:35](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/user_migration_test.go:35)

## EXT-19 · ClearBootstrap 및 OnBootstrapClear 수명주기

**미구현** · P2 · static-only · 추가 확인 항목

PocketBase: v0.40.4 ClearBootstrap은 초기화된 앱의 DB builder를 닫는 정리를 OnBootstrapClear 훅 안에서 수행한다. 정확한 훅 이름은 OnBootstrapClear이다.

PgBase: $app.bootstrap/resetBootstrapState는 operator callback으로 제공되지만 ClearBootstrap API 및 OnBootstrapClear 이벤트 등록 표면은 없다.

차이/남은 범위: ClearBootstrap/clearBootstrap/OnBootstrapClear/onBootstrapClear 검색에서 구현 없음. 기존 reset callback과 종료 이벤트는 새 API/훅을 대신하지 못한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go#L475)

PgBase 근거: [pgbase/extension_app_control.go:68](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_control.go:68) / [internal/jsvm/pocketbase_globals.go:463](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:463) / [pgbase/lifecycle_hooks.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/lifecycle_hooks.go:22)

테스트/검증 근거:

## EXT-20 · 트랜잭션 최종 완료 callback TxInfo.OnComplete

**미구현** · P1 · static-only · 추가 확인 항목

PocketBase: TxInfo.OnComplete callback은 최종 commit 또는 rollback 후 txErr를 받고 callback 오류를 transaction 결과에 합친다.

PgBase: transactionInfo/txInfo는 transactional flag와 token만 반환한다. record 결과 훅의 내부 finalization은 있으나 extension이 완료 callback을 등록할 공개 API는 없다.

차이/남은 범위: Go 또는 JS extension의 일반 after-commit/after-rollback 등록 및 callback 오류 병합 계약이 없다. record 내부 callback 존재는 public TxInfo 호환이 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/db_tx.go#L79)

PgBase 근거: [pgbase/extension_app_control.go:153](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_control.go:153) / [internal/jsvm/pocketbase_globals.go:449](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:449) / [internal/jsvm/pocketbase_globals.go:464](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:464) / [pgbase/extension_app_transaction.go:84](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_transaction.go:84)

테스트/검증 근거:

## EXT-21 · fluent ORM query 및 record helper 선택 인자

**보완 필요** · P1 · test-exists · 추가 확인 항목

PocketBase: RecordQuery/ModelQuery는 dbx SelectQuery를 반환한다. FindRecordsByFilter는 offset/params, FindAllRecords는 표현식, CanAccessRecord는 supplied RequestInfo를 받는다.

PgBase: recordQuery는 즉시 page data를 반환한다. modelQuery는 where/orderBy/limit/all/one/count만, collectionQuery는 제한된 hashExp id/name/type 필터만 제공한다.

차이/남은 범위: Select/Join/AndWhere/Bind/Scan 등의 일반 builder가 없다. findRecordsByFilter는 offset/params를 받지 않고 findAllRecords는 exprs를 전달하지 않으며 canAccessRecord는 caller requestInfo를 버린다. 현재 호출자의 권한을 쓰는 변경은 의도적이지만 동일 API로 무시되는 인자는 실질적 호환 공백이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_query.go#L26)

PgBase 근거: [internal/jsvm/pocketbase_globals.go:449](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:449) / [internal/jsvm/pocketbase_globals.go:450](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:450) / [internal/jsvm/pocketbase_globals.go:454](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:454) / [internal/jsvm/pocketbase_globals.go:457](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals.go:457) / [pgbase/extension_app_query.go:70](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_query.go:70) / [pgbase/extension_app_query.go:158](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_query.go:158)

테스트/검증 근거: [internal/jsvm/pocketbase_globals_test.go:163](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/pocketbase_globals_test.go:163) / [pgbase/extension_app_query_test.go:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_query_test.go:9) / [pgbase/extension_app_query_integration_test.go:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_query_integration_test.go:21) / [pgbase/extension_app_query_integration_test.go:74](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_app_query_integration_test.go:74)

## EXT-22 · JS RequestEvent 응답·body·실시간 stream API

**보완 필요** · P2 · test-exists · 추가 확인 항목

PocketBase: JS handler는 live RequestEvent의 json/bindBody/stream/flush/setCookie 등으로 응답 및 body binding을 직접 제어한다.

PgBase: JS handler는 JSON request DTO와 opaque request/response handle을 받고 응답 descriptor를 반환한다. object/static/compressed 응답은 capability와 StreamResponse 아래 host가 실행한다.

차이/남은 범위: PB e.json/e.bindBody/e.stream/e.flush/e.setCookie는 JS event에 없다. descriptor 기반 HTTP 응답은 구현됐지만 arbitrary incremental streaming/flush와 PB handler 소스 호환은 제공하지 않는다. Go net/http stream은 EXT-03에서 별도 구현으로 인정한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/router/event.go#L191)

PgBase 근거: [pgbase/javascript_runtime.go:87](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_runtime.go:87) / [pgbase/javascript_runtime.go:98](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_runtime.go:98) / [pgbase/javascript_runtime.go:106](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_runtime.go:106) / [pgbase/javascript_runtime.go:219](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_runtime.go:219) / [pgbase/javascript_runtime.go:259](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_runtime.go:259) / [internal/jsvm/runtime.go:439](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jsvm/runtime.go:439)

테스트/검증 근거: [pgbase/javascript_compatibility_conformance_integration_test.go:75](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/javascript_compatibility_conformance_integration_test.go:75) / [pgbase/extension_objects_integration_test.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/extension_objects_integration_test.go:24)

## MIG-09 · 적용된 migration의 조건부 재실행 ReapplyCondition

**미구현** · P2 · static-only · 추가 확인 항목

PocketBase: Migration.ReapplyCondition은 이미 적용된 파일도 조건이 true이면 기존 history를 교체하고 다시 Up을 실행한다.

PgBase: UserMigration/내부 Migration은 source revision과 up/down을 보유하지만 reapply callback 필드/runner 분기가 없다.

차이/남은 범위: 이미 적용된 migration을 조건에 따라 재실행하는 PB 계약은 없다. strict immutable history 정책으로 대체했지만 1:1 기능은 missing.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/migrations_list.go#L13)

PgBase 근거: [pgbase/user_migrations.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/user_migrations.go:24) / [internal/usermigrate/types.go:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/types.go:11) / [internal/usermigrate/runner.go:422](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:422) / [internal/usermigrate/runner.go:434](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/usermigrate/runner.go:434)

테스트/검증 근거:

## SDK-02 · TypeScript record CRUD·페이지 조회·filter/sort/expand

**보완 필요** · P2 · prior-run · 기존 항목

PocketBase: RecordService/CrudService는 page/perPage 목록과 getOne/create/update/delete를 제공하며 읽기·쓰기 RecordOptions에서 expand를 지원한다.

PgBase: TS는 native cursor list, 명시적 getList PB page adapter, get/getWithETag, create/update/remove 및 iterate를 제공한다. get은 expand를 전달하지만 create/update의 RecordWriteOptions는 fields만 허용한다.

차이/남은 범위: 쓰기 응답 expand 옵션 누락. 엔드포인트와 필수 생성 idempotency key·수정/삭제 ETag는 의도적으로 다른 계약이며 PB SDK를 그대로 교체할 수 없다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RecordService.ts#L191-L307)

PgBase 근거: [sdk/typescript/src/index.ts:35](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:35) / [sdk/typescript/src/index.ts:55](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:55) / [sdk/typescript/src/index.ts:785](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:785) / [sdk/typescript/src/index.ts:828](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:828)

테스트/검증 근거: [sdk/typescript/test/sdk.test.mjs:267](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/sdk.test.mjs:267) / [sdk/typescript/test/sdk.test.mjs:310](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/sdk.test.mjs:310) / [sdk/typescript/test/public-api.test.ts:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/public-api.test.ts:13)

## SDK-02-DART · Dart record CRUD·페이지 조회·expand

**보완 필요** · P2 · test-exists · 추가 확인 항목

PocketBase: Dart BaseCrudService는 getList/getOne/create/update에 expand/fields/query/headers를 전달한다.

PgBase: PgBase Dart는 cursor list와 getList page adapter, exact JsonObject CRUD와 ETag를 제공하지만 get/getWithEtag와 create/update에 fields만 노출한다.

차이/남은 범위: TS get과 달리 Dart 단건 get에도 expand가 없다. 임의 query/headers와 쓰기 expand도 고수준 Records 메서드에서 누락된다.

공식 근거: [소스 1](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/services/base_crud_service.dart#L55-L225)

PgBase 근거: [sdk/dart/lib/src/records.dart:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/records.dart:17) / [sdk/dart/lib/src/records.dart:40](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/records.dart:40) / [sdk/dart/lib/src/records.dart:81](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/records.dart:81) / [sdk/dart/lib/src/records.dart:99](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/records.dart:99)

테스트/검증 근거: [sdk/dart/test/sdk_test.dart:250](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:250) / [sdk/dart/test/sdk_test.dart:353](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:353) / [sdk/dart/test/sdk_test.dart:381](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:381)

## SDK-03 · TypeScript fields projection 전달 범위

**보완 필요** · P1 · prior-run · 기존 항목

PocketBase: 공식 RecordOptions/CommonOptions/RecordSubscribeOptions는 레코드·인증·구독 호출에 fields를 허용한다.

PgBase: 일반 record list/get/write, global auth completion, batch와 native events에는 fields가 있다. explicit AuthRealm refresh/session/requestOTP/completeOTP는 AbortSignal만 받는다.

차이/남은 범위: realm refresh/session/OTP와 shared realtime.subscribe의 fields 전달이 빠져 있다. 이전의 모든 auth/realtime projection 완료 표기는 너무 넓다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/tools/options.ts#L51-L85)

PgBase 근거: [sdk/typescript/src/index.ts:45](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:45) / [sdk/typescript/src/index.ts:301](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:301) / [sdk/typescript/src/index.ts:393](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:393) / [sdk/typescript/src/index.ts:690](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:690) / [sdk/typescript/src/index.ts:699](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:699) / [sdk/typescript/src/index.ts:539](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:539)

테스트/검증 근거: [sdk/typescript/test/sdk.test.mjs:171](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/sdk.test.mjs:171) / [sdk/typescript/test/sdk.test.mjs:267](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/sdk.test.mjs:267) / [sdk/typescript/test/public-api.test.ts:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/public-api.test.ts:21)

## SDK-03-DART · Dart fields projection 전달 범위

**보완 필요** · P2 · test-exists · 추가 확인 항목

PocketBase: 공식 Dart RecordService와 RealtimeService는 auth/record 응답 및 subscription별 fields를 지원한다.

PgBase: PgBase Dart record·realm refresh/session/OTP/profile는 fields를 노출한다. shared RealtimeService.subscribe는 topic/listener만 받는다.

차이/남은 범위: TS realm의 누락은 Dart에는 없지만 shared realtime 구독 projection은 Dart도 누락된다. Dart 테스트는 이번 감사에서 실행하지 않았다.

공식 근거: [소스 1](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/services/realtime_service.dart#L54-L106)

PgBase 근거: [sdk/dart/lib/src/records.dart:81](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/records.dart:81) / [sdk/dart/lib/src/client.dart:646](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:646) / [sdk/dart/lib/src/client.dart:695](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:695) / [sdk/dart/lib/src/realtime.dart:57](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/realtime.dart:57)

테스트/검증 근거: [sdk/dart/test/sdk_test.dart:353](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:353) / [sdk/dart/test/sdk_test.dart:857](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:857)

## SDK-04 · auth collection password·refresh·OTP·MFA

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: 공식 RecordService는 컬렉션별 password/refresh/OTP 인증 응답을 auth store에 반영한다.

PgBase: 양쪽 PgBase SDK는 realm(name) register/login/methods/refresh와 OTP, global MFA/TOTP·메일 작업을 제공하고 auth generation을 확인하여 오래된 인증 응답의 store 덮어쓰기를 막는다.

차이/남은 범위: PB의 RecordAuthResponse/JWT 대신 PgBase UserSession과 MFAChallengeError·opaque session token을 사용한다. 메일·MFA 메서드 배치와 refresh single-use 계약도 다르다. 외부 계정 관리/자동 refresh는 별도 행에서 평가한다. Dart는 소스·테스트 존재만 확인했다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RecordService.ts#L318-L400)

PgBase 근거: [sdk/typescript/src/index.ts:393](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:393) / [sdk/typescript/src/index.ts:662](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:662) / [sdk/typescript/src/index.ts:690](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:690) / [sdk/dart/lib/src/client.dart:237](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:237) / [sdk/dart/lib/src/client.dart:605](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:605)

테스트/검증 근거: [sdk/typescript/test/sdk.test.mjs:202](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/sdk.test.mjs:202) / [sdk/typescript/test/sdk.test.mjs:369](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/sdk.test.mjs:369) / [sdk/dart/test/sdk_test.dart:250](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:250)

## SDK-05 · OAuth browser helper

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: 공식 JS authWithOAuth2는 provider 정보, 일회성 realtime 연결, 기본 popup opener와 OAuth 완료·정리를 조합한다.

PgBase: TS/Dart oauthWithBrowser는 start→host adapter open/waitForCallback→origin/state/code 확인→complete→항상 close를 수행한다.

차이/남은 범위: 동일한 로그인 목적을 redirect/deeplink adapter로 구현한다. PB의 내장 popup 및 realtime callback 방법을 복제한 것은 아니며 host adapter 구현은 호출자 책임이다. 외부 OAuth provider E2E는 이번 검증에 포함되지 않았다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RecordService.ts#L549-L715)

PgBase 근거: [sdk/typescript/src/index.ts:722](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:722) / [sdk/dart/lib/src/client.dart:749](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:749) / [sdk/dart/lib/src/client.dart:913](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:913)

테스트/검증 근거: [sdk/typescript/test/sdk.test.mjs:234](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/sdk.test.mjs:234) / [sdk/dart/test/sdk_test.dart:567](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:567)

## SDK-06 · TypeScript AuthStore·지속 저장·SSR cookie

**보완 필요** · P1 · executed-now · 기존 항목

PocketBase: BaseAuthStore는 token+record, JWT expiry 기반 isValid, public onChange/fireImmediately, cookie import/export를 제공한다. LocalAuthStore/AsyncAuthStore는 token+record를 함께 저장한다.

PgBase: PgBase TS는 메모리 token+record와 cookie round-trip, 동기 TokenStorage/browserTokenStorage를 제공하지만 storage에는 token만 저장하며 isValid는 nonempty 검사이다.

차이/남은 범위: 실행 probe에서 storage로 새 AuthStore를 만들면 record=null이었다. AsyncAuthStore·storage event 동기화·public token/record onChange/fireImmediately가 없고 _onChange는 generation만 전달한다. SSR cookie는 secure/sameSite opt-in이며 만료/4096-byte 축소/전체 cookie 옵션이 없다. Opaque token의 isValid 차이는 설계 차이지만 동등한 expiry-aware state도 없다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/stores/BaseAuthStore.ts#L19-L238)

PgBase 근거: [sdk/typescript/src/index.ts:136](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:136) / [sdk/typescript/src/index.ts:145](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:145) / [sdk/typescript/src/index.ts:182](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:182) / [sdk/typescript/src/index.ts:220](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:220)

테스트/검증 근거: [sdk/typescript/test/sdk.test.mjs:174](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/sdk.test.mjs:174)

## SDK-06-DART · Dart AuthStore·비동기 저장·SSR

**보완 필요** · P1 · test-exists · 추가 확인 항목

PocketBase: 공식 Dart AsyncAuthStore는 token과 RecordModel을 직렬화하고 큐로 저장하며 AuthStore.onChange는 token+record 이벤트를 보낸다.

PgBase: PgBase Dart는 async storage 작업 직렬화, generation 방어, User record 메모리/SSR cookie round-trip을 제공한다. AuthStorage.write는 token만 받으며 changes는 generation Stream<int>이다.

차이/남은 범위: token+record durable round-trip이 완성되지 않았다. TS와 달리 비동기 저장·직렬화는 구현되어 있으므로 둘을 동일한 'async 없음'으로 평가하면 안 된다. 공식 Dart에는 JS cookie helper와 같은 표면이 없고 PgBase cookie 지원은 추가 기능이다.

공식 근거: [소스 1](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/async_auth_store.dart#L34-L98)

PgBase 근거: [sdk/dart/lib/src/auth.dart:8](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/auth.dart:8) / [sdk/dart/lib/src/auth.dart:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/auth.dart:26) / [sdk/dart/lib/src/auth.dart:31](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/auth.dart:31) / [sdk/dart/lib/src/auth.dart:71](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/auth.dart:71)

테스트/검증 근거: [sdk/dart/test/sdk_test.dart:512](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:512) / [sdk/dart/test/sdk_test.dart:524](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:524) / [sdk/dart/test/sdk_test.dart:652](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:652)

## SDK-07 · 요청 취소와 기본 duplicate auto-cancellation

**보완 필요** · P1 · executed-now · 기존 항목

PocketBase: 공식 JS는 모든 send 경로에서 default method+path request key를 사용하고 requestKey:null로 끌 수 있으며 서비스 호출도 이를 상속한다.

PgBase: PgBase TS AbortSignal 및 send(...,{requestKey}) 취소는 구현되어 있으나 Records/Auth는 private request/_response를 호출해 keyedSignal을 거치지 않는다. Dart는 공식 Dart에 없는 keyed request 취소를 추가로 제공한다.

차이/남은 범위: 동일 records.list() 두 요청을 실행해 둘 다 signal=undefined임을 확인했다. 명시적 key 없는 send도 자동 취소되지 않는다. PB의 기본 서비스 중복 취소 계약은 불일치하며 existing keyed tests는 이 경로를 검사하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/Client.ts#L498-L573)

PgBase 근거: [sdk/typescript/src/index.ts:427](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:427) / [sdk/typescript/src/index.ts:432](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:432) / [sdk/typescript/src/index.ts:512](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:512) / [sdk/typescript/src/index.ts:785](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:785) / [sdk/dart/lib/src/client.dart:43](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:43) / [sdk/dart/lib/src/client.dart:402](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:402)

테스트/검증 근거: [sdk/typescript/test/sdk.test.mjs:183](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/sdk.test.mjs:183) / [sdk/typescript/test/records-json.test.mjs:105](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/records-json.test.mjs:105) / [sdk/dart/test/sdk_test.dart:484](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:484)

## SDK-08 · TypeScript 오류 normalization·본문 중단

**보완 필요** · P1 · executed-now · 기존 항목

PocketBase: Client.send는 모든 오류를 ClientResponseError(url/status/response/isAbort/originalError)로 감싸며 response.json 중 AbortError도 다시 throw한다.

PgBase: PgBaseError는 status/code/details/etag를 보존하지만 network/abort는 원래 오류로 남고 URL/requestId/original response가 없다. 기존 parsed request/_response는 모든 json 오류를 undefined로 삼킨다. 새 raw JSON 경로는 text 읽기 실패를 전파한다.

차이/남은 범위: 실행 probe: 본문 수신 중 abort 시 records.get()은 undefined로 resolve, getJSON()은 AbortError reject. TS까지 cancellation/network/protocol/requestId 모두 정규화된다는 과거 설명은 사실이 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/Client.ts#L451-L490)

PgBase 근거: [sdk/typescript/src/index.ts:117](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:117) / [sdk/typescript/src/index.ts:438](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:438) / [sdk/typescript/src/index.ts:444](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:444) / [sdk/typescript/src/index.ts:488](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:488)

테스트/검증 근거: [sdk/typescript/test/sdk.test.mjs:320](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/sdk.test.mjs:320) / [sdk/typescript/test/records-json.test.mjs:87](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/records-json.test.mjs:87) / [sdk/typescript/test/records-json.test.mjs:134](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/records-json.test.mjs:134)

## SDK-08-DART · Dart 오류 normalization

**대응 구현·설계 차이** · none · test-exists · 추가 확인 항목

PocketBase: 공식 Dart ClientException은 URL/statusCode/response/originalError/isAbort를 단일 타입에 보존한다.

PgBase: PgBase Dart는 HTTP PgBaseError(status/code/details/etag/requestId), PgBaseCancellation, PgBaseNetworkError(cause), PgBaseProtocolError를 분리하고 transport/body 오류를 매핑한다.

차이/남은 범위: 기능상 HTTP/취소/네트워크 구분은 있으나 catch 타입과 메타데이터가 다르다. PgBase HTTP 오류는 PB의 전체 URL/response 보존 인터페이스가 아니다. Dart 실행 검증은 이번에 하지 않았다.

공식 근거: [소스 1](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/client_exception.dart#L1-L36)

PgBase 근거: [sdk/dart/lib/src/errors.dart:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/errors.dart:1) / [sdk/dart/lib/src/transport.dart:154](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/transport.dart:154) / [sdk/dart/lib/src/client.dart:956](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:956)

테스트/검증 근거: [sdk/dart/test/sdk_test.dart:138](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:138) / [sdk/dart/test/sdk_test.dart:447](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:447) / [sdk/dart/test/sdk_test.dart:472](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:472) / [sdk/dart/test/sdk_test.dart:503](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:503)

## SDK-09 · TypeScript filter placeholder helper 최신 JS 계약

**보완 필요** · P2 · executed-now · 기존 항목

PocketBase: 현재 공식 JS filter는 params의 실제 모든 key를 RegExp.escape하여 정확히 치환하므로 공백 등도 key로 쓸 수 있다.

PgBase: PgBase TS는 string/number/bool/null/date/aggregate escaping과 단일 패스 치환을 제공하지만 key 정규식은 [\w.-]+로 제한한다.

차이/남은 범위: 공통 identifier key 테스트는 통과하지만 `filter('title={:my key}', {'my key':'exact'})`가 그대로 남는다. 최신 PB JS의 임의 key 계약과는 완전히 동등하지 않다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/Client.ts#L302-L344)

PgBase 근거: [sdk/typescript/src/index.ts:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:9) / [sdk/typescript/src/index.ts:366](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:366)

테스트/검증 근거: [sdk/typescript/test/sdk.test.mjs:150](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/sdk.test.mjs:150)

## SDK-09-DART · Dart filter placeholder helper

**좁은 계약 일치** · none · test-exists · 추가 확인 항목

PocketBase: 공식 Dart는 [\w.-]+ placeholder와 null/num/bool/string/UTC DateTime/JSON aggregate escaping을 사용한다.

PgBase: PgBase Dart는 같은 key grammar, escaping 분기, missing placeholder 유지와 한 번의 치환을 구현한다.

차이/남은 범위: 이 좁은 helper 계약은 소스와 대응 테스트가 일치한다. Dart 런타임을 이번 감사에서 실행했다는 뜻은 아니며 SDK 전체 drop-in 호환을 의미하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/client.dart#L177-L207)

PgBase 근거: [sdk/dart/lib/src/client.dart:72](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:72)

테스트/검증 근거: [sdk/dart/test/sdk_test.dart:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:9)

## SDK-10 · realtime subscribe/unsubscribe·options·reconnect

**보완 필요** · P1 · prior-run · 기존 항목

PocketBase: 공식 JS RealtimeService는 topic별 query/headers options, prefix/listener unsubscribe, isConnected/onDisconnect, 재연결을 제공한다.

PgBase: PgBase TS/Dart는 공유 SSE topic/listener, unsubscribe, bounded reconnect와 custom application topic을 구현한다. TS options는 reconnect 설정뿐이고 Dart subscribe는 topic/listener뿐이다.

차이/남은 범위: subscription fields/filter/expand/custom headers, prefix unsubscribe와 공개 connection/disconnect hooks가 없다. 기본 reconnect budget도 PB JS 무한 재연결과 다르다. Dart도 동일 options/prefix gap; source/tests만 확인했다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RealtimeService.ts#L35-L215)

PgBase 근거: [sdk/typescript/src/index.ts:78](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:78) / [sdk/typescript/src/index.ts:524](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:524) / [sdk/typescript/src/index.ts:564](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:564) / [sdk/dart/lib/src/realtime.dart:31](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/realtime.dart:31) / [sdk/dart/lib/src/realtime.dart:57](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/realtime.dart:57)

테스트/검증 근거: [sdk/typescript/test/realtime-terminal.test.mjs:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/realtime-terminal.test.mjs:1) / [sdk/typescript/test/sdk.test.mjs:160](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/sdk.test.mjs:160) / [sdk/dart/test/sdk_test.dart:64](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:64) / [pgbase/sdk_realtime_conformance_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/sdk_realtime_conformance_integration_test.go:1)

## SDK-11 · transactional batch·multipart·upsert

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: BatchService builder는 collection별 create/update/upsert/delete를 모아 multipart batch request와 file parts를 보낸다.

PgBase: PgBase TS/Dart는 compact BatchOperation 및 HTTP-shaped requests, PUT upsert와 explicit multipart file mapping을 제공한다.

차이/남은 범위: 요청 구성 목적은 제공하지만 fluent createBatch builder가 아니라 명시적 배열/file map이며 endpoint·envelope·ETag/idempotency 계약이 다르다. Batch FormData 자동 분해 및 여러 동일 key file 입력의 완전한 PB parity는 검증하지 않았다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/BatchService.ts#L23-L78)

PgBase 근거: [sdk/typescript/src/index.ts:88](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:88) / [sdk/typescript/src/index.ts:373](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:373) / [sdk/typescript/src/index.ts:383](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:383) / [sdk/dart/lib/src/records.dart:215](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/records.dart:215) / [sdk/dart/lib/src/client.dart:487](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:487)

테스트/검증 근거: [sdk/typescript/test/sdk.test.mjs:292](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/sdk.test.mjs:292) / [sdk/dart/test/sdk_test.dart:314](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:314)

## SDK-12 · file URL/token·record multipart upload

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: FileService는 record+filename으로 공개/보호 URL(thumb/download query 포함)을 만들며 RecordService.create/update는 FormData/File/Blob payload를 받는다.

PgBase: PgBase 양 SDK는 레코드별 Files upload/list/delete, file UUID token/variant URL을 제공한다. Dart는 download bytes도 제공하지만 TS는 URL 생성 후 fetch를 호출한다. Records create/update는 JSON 전용이다.

차이/남은 범위: 분리된 upload·token URL은 의도된 대안이지만 PB record create/update의 자동 FormData/file 변환과 filename 기반 공개 URL/다운로드 옵션은 제공하지 않는다. TS는 record FormData를 JSON.stringify하여 사용할 수 없다. 실제 파일 conformance는 이번 감사에서 재실행하지 않았다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/FileService.ts#L20-L70)

PgBase 근거: [sdk/typescript/src/index.ts:828](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:828) / [sdk/typescript/src/index.ts:1001](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:1001) / [sdk/dart/lib/src/files.dart:54](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/files.dart:54) / [sdk/dart/lib/src/files.dart:87](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/files.dart:87) / [sdk/dart/lib/src/records.dart:99](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/records.dart:99)

테스트/검증 근거: [pgbase/sdk_integration_test.go:97](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/sdk_integration_test.go:97) / [sdk/dart/test/sdk_test.dart:749](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:749)

## SDK-14 · Dart 관리자 세션 저장·동시 로그인 일관성

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: 공식 SDK의 관리자 권한은 _superusers RecordService와 AuthStore bearer token 흐름이다.

PgBase: PgBase Dart는 별도 PgBaseAdminClient cookie/CSRF와 generation을 사용하며 늦은 Set-Cookie/401이 최신 세션을 덮지 않도록 방어한다.

차이/남은 범위: PB와 인증 모델이 의도적으로 다르다. Source/test existence는 확인했으나 이번 Dart runtime 결과는 없다.

공식 근거: [소스 1](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/client.dart#L40-L45)

PgBase 근거: [sdk/dart/lib/src/admin.dart:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/admin.dart:12) / [sdk/dart/lib/src/admin.dart:76](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/admin.dart:76) / [sdk/dart/lib/src/admin.dart:443](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/admin.dart:443)

테스트/검증 근거: [sdk/dart/test/sdk_test.dart:413](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:413) / [sdk/dart/test/sdk_test.dart:425](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:425) / [sdk/dart/test/sdk_test.dart:699](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:699)

## SDK-X01 · auth record update/delete와 AuthStore 자동 동기화

**미구현** · P1 · static-only · 추가 확인 항목

PocketBase: 공식 JS/Dart RecordService는 현재 auth record를 update하면 store record를 merge하고 delete하면 store를 clear한다.

PgBase: PgBase Records.update/remove와 realm.updateProfile는 응답 반환만 하며 auth record merge/clear를 수행하지 않는다.

차이/남은 범위: 현재 인증 레코드를 수정·삭제해도 SDK cached auth.record가 자동으로 갱신/삭제되지 않는다. 별도 refresh/clear를 호출해야 한다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RecordService.ts#L256-L310)

PgBase 근거: [sdk/typescript/src/index.ts:712](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:712) / [sdk/typescript/src/index.ts:851](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:851) / [sdk/typescript/src/index.ts:875](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:875) / [sdk/dart/lib/src/records.dart:128](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/records.dart:128) / [sdk/dart/lib/src/records.dart:160](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/records.dart:160) / [sdk/dart/lib/src/client.dart:788](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:788)

테스트/검증 근거:

## SDK-X02 · JS request interceptors·공통 request options

**보완 필요** · P2 · static-only · 추가 확인 항목

PocketBase: Client.beforeSend/afterSend는 URL/options/parsed data를 바꾸며 SendOptions는 per-call fetch/query/headers 및 language header를 지원한다.

PgBase: PgBase TS 생성자는 공통 fetch 주입, low-level send는 RequestInit를 허용하지만 lifecycle hooks·lang·service-level 공통 request options는 없다. Dart는 http.Client 주입과 raw request(query/headers)를 제공한다.

차이/남은 범위: TS의 per-call fetch, after parsed-response hook, high-level custom query/headers가 없다. 전송 주입으로 일부를 수동 재구현할 수 있으나 PB hook contract와 동일하지 않다. 공식 Dart에도 JS와 같은 beforeSend/afterSend는 없으므로 이 gap은 JS 기준이다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/Client.ts#L65-L124)

PgBase 근거: [sdk/typescript/src/index.ts:237](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:237) / [sdk/typescript/src/index.ts:432](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:432) / [sdk/typescript/src/index.ts:493](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:493) / [sdk/dart/lib/src/client.dart:402](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:402) / [sdk/dart/lib/src/transport.dart:50](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/transport.dart:50)

테스트/검증 근거:

## SDK-X03 · getFullList·getFirstListItem·decode helpers

**보완 필요** · P2 · test-exists · 추가 확인 항목

PocketBase: 공식 CrudService는 getFullList 자동 batch fetch, getFirstListItem의 first/404 계약과 decode customization을 제공한다.

PgBase: PgBase 양 SDK는 cursor iterate로 전체 순회를 제공하지만 getFullList 배열 수집/getFirstListItem first-or-404 및 model decode hook을 노출하지 않는다.

차이/남은 범위: 전체 순회 목적은 adapted이며 first-match/404 편의 helper 및 decode customization은 빠져 있다. 호출자가 list+첫 항목 검사/iterator 수집을 작성해야 한다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/CrudService.ts#L12-L136)

PgBase 근거: [sdk/typescript/src/index.ts:409](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:409) / [sdk/typescript/src/index.ts:779](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:779) / [sdk/dart/lib/src/records.dart:174](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/records.dart:174)

테스트/검증 근거: [sdk/typescript/test/sdk.test.mjs:287](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/sdk.test.mjs:287)

## SDK-X04 · JS linked external auth 관리 helpers

**미구현** · P2 · static-only · 추가 확인 항목

PocketBase: 공식 JS listExternalAuths/unlinkExternalAuth는 현재 deprecated이지만 _externalAuths collection CRUD를 통해 연결 목록/해제를 수행한다.

PgBase: PgBase TS AuthRealm은 oauthLink와 completion만 노출하며 연결 목록/해제 helper가 없다.

차이/남은 범위: JS public helper 대응이 없다. 공식 Dart도 dedicated deprecated helpers는 없으므로 Dart helper 누락으로 일반화하지 않는다. 서버에 외부 계정 관리가 있더라도 SDK 구현 증거로 계산하지 않았다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RecordService.ts#L1041-L1085)

PgBase 근거: [sdk/typescript/src/index.ts:716](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:716) / [sdk/typescript/src/index.ts:740](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:740) / [sdk/dart/lib/src/client.dart:338](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:338)

테스트/검증 근거:

## SDK-X05 · HealthService.check

**미구현** · P2 · static-only · 추가 확인 항목

PocketBase: 공식 JS/Dart health.check는 건강 상태 endpoint의 DTO를 반환한다.

PgBase: PgBase TS/Dart public client에 health service/check helper가 없다. Dart admin.system은 운영 정보 조회로 별도 목적이다.

차이/남은 범위: SDK의 고수준 건강 상태 점검 helper가 누락된다. 서버 health endpoint 존재 여부와 분리한 판정이다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/HealthService.ts#L6-L26)

PgBase 근거: [sdk/typescript/src/index.ts:229](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:229) / [sdk/typescript/src/admin.ts:110](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/admin.ts:110) / [sdk/dart/lib/src/client.dart:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/client.dart:18) / [sdk/dart/lib/src/admin.dart:440](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/admin.dart:440)

테스트/검증 근거:

## SDK-X06 · BackupService list/create/upload/delete/restore/download

**미구현** · P1 · static-only · 추가 확인 항목

PocketBase: 공식 BackupService는 backup 목록·생성·업로드·삭제·복원·다운로드 URL을 제공한다.

PgBase: PgBase SDK 관리자는 backup storage 설정/test만 노출하고 backup artifact lifecycle 메서드는 없다. Dart의 generic admin.request로 문서화된 서버 route를 직접 호출할 수는 있다.

차이/남은 범위: backupStorageSettings가 BackupService 기능 구현은 아니다. TS admin에는 공개 generic request도 없고 파일 업로드/download/restore helper가 양쪽에 없다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/BackupService.ts#L8-L131)

PgBase 근거: [sdk/typescript/src/admin.ts:168](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/admin.ts:168) / [sdk/dart/lib/src/admin.dart:331](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/admin.dart:331) / [sdk/dart/lib/src/admin.dart:76](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/admin.dart:76)

테스트/검증 근거:

## SDK-X07 · SQLService.run

**미구현** · P2 · static-only · 추가 확인 항목

PocketBase: 공식 SQLService.run은 superuser SQL 요청과 execTime/affectedRows/columns/rows DTO를 노출한다.

PgBase: PgBase 관리자 UI/서버 SQL 기능과 별개로 TS/Dart SDK에는 SQL 실행 helper/DTO가 없다.

차이/남은 범위: DB 운영 기능이 구현되어 있어도 SDK public SQL 서비스 대응은 누락이다. Dart generic request 수동 호출은 고수준 SDK parity가 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/SQLService.ts#L4-L29)

PgBase 근거: [sdk/typescript/src/admin.ts:110](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/admin.ts:110) / [sdk/dart/lib/src/admin.dart:127](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/admin.dart:127) / [sdk/dart/lib/pgbase.dart:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/pgbase.dart:1)

테스트/검증 근거:

## SDK-X08 · SettingsService 전체 설정·메일 테스트

**보완 필요** · P2 · prior-run · 추가 확인 항목

PocketBase: 공식 SettingsService는 getAll/update/testS3/testEmail/generateAppleClientSecret를 제공한다.

PgBase: PgBase TS/Dart는 파일/백업 storage 설정·test와 Apple client secret helper를 제공한다.

차이/남은 범위: 일반 설정 getAll/update와 SMTP testEmail 고수준 메서드는 없다. SDK settings 영역 전체를 완료로 볼 수 없다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/SettingsService.ts#L9-L123)

PgBase 근거: [sdk/typescript/src/admin.ts:126](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/admin.ts:126) / [sdk/typescript/src/admin.ts:165](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/admin.ts:165) / [sdk/dart/lib/src/admin.dart:130](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/admin.dart:130) / [sdk/dart/lib/src/admin.dart:314](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/admin.dart:314)

테스트/검증 근거: [sdk/typescript/test/admin.test.mjs:122](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/admin.test.mjs:122) / [sdk/typescript/test/admin.test.mjs:145](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/admin.test.mjs:145)

## SDK-X09 · LogService 단건·통계·truncate

**보완 필요** · P2 · static-only · 추가 확인 항목

PocketBase: 공식 LogService는 getList/getOne/getStats/truncate와 filter 기반 조회를 제공한다.

PgBase: PgBase 관리자는 before/statusClass cursor 로그 목록만 제공한다.

차이/남은 범위: 단건 로그·시간별 통계·truncate 및 PB filter 옵션 대응이 없다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/LogService.ts#L9-L95)

PgBase 근거: [sdk/typescript/src/admin.ts:156](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/admin.ts:156) / [sdk/dart/lib/src/admin.dart:278](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/admin.dart:278)

테스트/검증 근거:

## SDK-X10 · CollectionService metadata·scaffold typing

**보완 필요** · P1 · static-only · 추가 확인 항목

PocketBase: 공식 CollectionService는 CRUD/import 외 getScaffolds/getAllOAuth2Providers/dryRunViewQuery를 제공한다.

PgBase: PgBase 양 SDK는 schema catalog/plan/apply/import/scaffolds/truncate를 제공한다. TS CollectionScaffolds는 아직 base/auth/view 모두 CollectionChange라고 선언하지만 PgBase 서버 auth scaffold는 {collection,authCollection}이다.

차이/남은 범위: getAllOAuth2Providers/dryRunViewQuery helper가 없다. 별도로 TS scaffolds() 반환 타입은 현재 자사 서버 구조와도 불일치하며 기존 테스트 fixture는 잘못된 단순 shape만 검사한다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/CollectionService.ts#L65-L121)

PgBase 근거: [sdk/typescript/src/admin.ts:45](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/admin.ts:45) / [sdk/typescript/src/admin.ts:129](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/admin.ts:129) / [sdk/dart/lib/src/admin.dart:150](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/admin.dart:150) / [internal/schema/scaffolds.go:8](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/scaffolds.go:8)

테스트/검증 근거: [sdk/typescript/test/admin.test.mjs:122](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/admin.test.mjs:122) / [sdk/dart/test/sdk_test.dart:721](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:721)

## SDK-X11 · CronService 목록·실행

**대응 구현·설계 차이** · none · prior-run · 추가 확인 항목

PocketBase: 공식 CronService는 getFullList와 run(jobId)을 제공한다.

PgBase: PgBase TS/Dart는 jobs와 queueJob을 제공하며 configureJob 일정/활성 설정도 노출한다.

차이/남은 범위: 함수명과 job metadata/queue 실행 계약이 다르지만 목록·실행 요청 목적은 제공한다. Dart는 이번 실행 검증 대상이 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/CronService.ts#L8-L42)

PgBase 근거: [sdk/typescript/src/admin.ts:162](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/admin.ts:162) / [sdk/dart/lib/src/admin.dart:291](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/admin.dart:291)

테스트/검증 근거: [sdk/typescript/test/admin.test.mjs:34](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/admin.test.mjs:34)

## SDK-X12 · JS superuser autoRefreshThreshold

**미구현** · P2 · static-only · 추가 확인 항목

PocketBase: 공식 JS superuser authWithPassword는 autoRefreshThreshold가 설정되면 beforeSend를 통해 만료 전 refresh/재인증을 등록한다.

PgBase: PgBase SDK는 explicit realm.refresh와 별도 admin cookie session만 제공하며 만료 기준 자동 refresh 등록이 없다.

차이/남은 범위: opaque native session 및 cookie admin에 맞는 자동 갱신 정책은 별도 설계가 필요하다. 현재 공개 SDK에 대응 기능은 없다. 공식 Dart에는 동일 JS superuser 자동 refresh 옵션이 없다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/services/RecordService.ts#L374-L400)

PgBase 근거: [sdk/typescript/src/index.ts:690](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:690) / [sdk/typescript/src/admin.ts:116](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/admin.ts:116)

테스트/검증 근거:

## SDK-X13 · 정밀 JSON 데이터 표현

**대응 구현·설계 차이** · none · prior-run · 추가 확인 항목

PocketBase: 공식 JS Client.send는 response.json과 JSON.stringify를 사용하며 공식 Dart client는 jsonDecode/jsonEncode 기반 모델을 사용한다.

PgBase: PgBase TS 기본은 native JSON이며 새 listJSON/getJSON/createJSON/updateJSON 원문 경로를 추가했다. Dart는 JsonNumber.source를 보존하는 exact JSON 모델을 기본 사용한다.

차이/남은 범위: PB와 달리 정밀 JSON I/O를 의도적으로 강화했다. TS batch/auth/realtime은 여전히 native parsing이고 Dart의 JsonValue wrapper는 공식 RecordModel과 source-compatible하지 않다.

공식 근거: [소스 1](https://github.com/pocketbase/js-sdk/blob/01efa38294141f71bc60dc6359a06c29e19b93fb/src/Client.ts#L439-L472)

PgBase 근거: [sdk/typescript/src/index.ts:444](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:444) / [sdk/typescript/src/index.ts:790](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:790) / [sdk/typescript/src/index.ts:821](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/src/index.ts:821) / [sdk/dart/lib/src/exact_json.dart:32](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/exact_json.dart:32) / [sdk/dart/lib/src/transport.dart:67](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/transport.dart:67)

테스트/검증 근거: [sdk/typescript/test/records-json.test.mjs:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/typescript/test/records-json.test.mjs:11) / [sdk/dart/test/sdk_test.dart:214](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:214)

## SDK-X14 · Dart RecordModel typed-path access

**미구현** · P3 · static-only · 추가 확인 항목

PocketBase: 공식 Dart RecordModel.get<T>(path,defaultValue)는 nested path와 타입 변환/기본값을 지원하며 collectionId/collectionName getters를 제공한다.

PgBase: PgBase Dart는 immutable JsonObject/JsonValue 접근과 별도 User 타입을 제공한다.

차이/남은 범위: exact JSON 모델은 의도된 표현 차이지만 PB get<T> nested-path/casting/default helper에 해당하는 편의 기능은 없다.

공식 근거: [소스 1](https://github.com/pocketbase/dart-sdk/blob/b3f0831c3bbb2257438fd77041c09f2826d316be/lib/src/dtos/record_model.dart#L13-L105)

PgBase 근거: [sdk/dart/lib/src/exact_json.dart:68](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/exact_json.dart:68) / [sdk/dart/lib/src/types.dart:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/types.dart:1) / [sdk/dart/lib/src/records.dart:81](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/lib/src/records.dart:81)

테스트/검증 근거: [sdk/dart/test/sdk_test.dart:214](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/sdk/dart/test/sdk_test.dart:214)

## UI-01 · 최초 설치·첫 관리자·백업 초기화

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: 설치 화면에서 첫 _superusers 레코드 생성 후 로그인하거나 백업 파일을 업로드하여 복구한다.

PgBase: 설치 토큰으로 최초 관리자 생성, 백업 업로드·복구와 비동기 작업 상태 확인을 제공한다. PostgreSQL 백업 형식과 setup 인증 절차를 사용한다.

차이/남은 범위: 목적은 충족한다. PB의 ZIP 교체 방식과 동일한 포맷·복구 프로토콜은 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/auth/pageInstaller.js#L28)

PgBase 근거: [ui/admin/src/pages/setup-page.tsx:43](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/setup-page.tsx:43) / [ui/admin/src/pages/setup-page.tsx:89](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/setup-page.tsx:89)

테스트/검증 근거: [ui/admin/src/pages/setup-page.test.tsx:29](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/setup-page.test.tsx:29) / [ui/admin/src/pages/setup-page.test.tsx:134](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/setup-page.test.tsx:134)

## UI-02 · 관리자 로그인·MFA·로그아웃

**보완 필요** · P1 · prior-run · 기존 항목

PocketBase: 관리자 인증 메서드를 조회하고 password 또는 email OTP 로그인 및 MFA를 처리하며 헤더에서 로그아웃한다.

PgBase: 이메일·비밀번호, 서버가 요구하는 authenticator OTP, 로그아웃, 비밀번호 복구 요청과 수동 토큰 확인 화면이 있다.

차이/남은 범위: PB의 관리자 email OTP 단독 로그인/메서드 선택과 메일 링크의 전용 확인 라우트는 없다. TOTP 지원을 PB MFA UI 전체 동등으로 세면 안 된다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/auth/pageSuperuserLogin.js#L28)

PgBase 근거: [ui/admin/src/pages/login-page.tsx:35](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/login-page.tsx:35) / [ui/admin/src/pages/login-page.tsx:63](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/login-page.tsx:63) / [ui/admin/src/app.tsx:98](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/app.tsx:98)

테스트/검증 근거: [ui/admin/src/pages/login-page.test.tsx:68](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/login-page.test.tsx:68) / [ui/admin/src/pages/login-page.test.tsx:88](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/login-page.test.tsx:88)

## UI-03 · 라우팅·미저장 편집 이탈 보호

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: hash 라우트로 설치·로그인·컬렉션 화면을 연결하고 컬렉션/레코드 변경 상태를 추적한다.

PgBase: typed history 라우트, 리소스별 draft 식별, 앱 이동·뒤로가기·beforeunload 이탈 보호가 있다. text ID new도 생성 라우트와 구분한다.

차이/남은 범위: hash 대신 /admin history 경로를 사용하는 설계 차이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/router.js#L130)

PgBase 근거: [ui/admin/src/routing/admin-location.ts:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/routing/admin-location.ts:1) / [ui/admin/src/routing/unsaved-draft.tsx:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/routing/unsaved-draft.tsx:1)

테스트/검증 근거: [ui/admin/src/routing/admin-location.test.tsx:178](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/routing/admin-location.test.tsx:178) / [ui/admin/src/routing/unsaved-draft.test.tsx:61](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/routing/unsaved-draft.test.tsx:61) / [ui/admin/e2e/authenticated/remaining-development.spec.ts:4](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/e2e/authenticated/remaining-development.spec.ts:4)

## UI-04 · theme·responsive·a11y

**보완 필요** · P1 · prior-run · 기존 항목

PocketBase: light/dark/자동 OS 테마를 저장하고 matchMedia 변경을 반영한다. 작은 화면에 맞춰 레이아웃과 버튼을 바꾼다.

PgBase: 명시적 light/dark, accent, branding 설정과 label/ARIA, 가로 스크롤 테이블, 좁은 화면 CSS가 있다.

차이/남은 범위: system 옵션은 dataset.theme에만 저장되며 CSS는 dark만 처리한다. OS 테마 연동이 없다. 모바일 전체 흐름/스크린리더 parity는 이번 조사에서 실행 검증하지 않았다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/store.js#L297)

PgBase 근거: [ui/admin/src/pages/settings-page.tsx:84](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/settings-page.tsx:84) / [ui/admin/src/styles.css:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/styles.css:1) / [ui/admin/src/styles.css:71](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/styles.css:71) / [ui/admin/src/app.tsx:67](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/app.tsx:67)

테스트/검증 근거: [ui/admin/src/pages/settings-page.test.tsx:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/settings-page.test.tsx:14) / [ui/admin/src/relation/relation-picker.test.tsx:291](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/relation/relation-picker.test.tsx:291)

## UI-05 · admin UI extension 등록·화면

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: extensions.js를 기본 라우트 이전에 로드하고 superuserOnly/guestOnly/blank 경로, headerLinks 및 pbEvent/component 확장을 제공한다.

PgBase: 등록된 extension의 id/label/path/mount를 인증된 /admin/ext/{id} 화면과 내비게이션에 연결하고 unmount 정리를 실행한다.

차이/남은 범위: PB guest/blank 공개 extension 라우트, 기존 화면의 pbEvent/component 재정의, 임의 헤더 링크와 동등한 공개 인터페이스는 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/router.js#L44)

PgBase 근거: [ui/admin/src/extensions/admin-extensions.ts:2](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/extensions/admin-extensions.ts:2) / [ui/admin/src/extensions/extension-screen.tsx:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/extensions/extension-screen.tsx:1) / [ui/admin/src/app.tsx:45](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/app.tsx:45)

테스트/검증 근거: [pgbase/ui_extension_integration_test.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/ui_extension_integration_test.go:18)

## UI-06 · create/edit/delete

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: 컬렉션 생성/변경/삭제를 확인 모달과 서버 컬렉션 API로 수행한다.

PgBase: 별도 designer 페이지에서 변경 preview 후 apply하고 파괴적 삭제는 대상 이름·계획 확인을 거친다. 결과 미확정 시 작업 원장을 확인한다.

차이/남은 범위: PostgreSQL 스키마 계획/원장 모델로 같은 관리 목적을 충족한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/collections/collectionUpsertModal.js#L133)

PgBase 근거: [ui/admin/src/pages/collection-designer-page.tsx:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collection-designer-page.tsx:22) / [ui/admin/src/designer/collection-designer.ts:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/designer/collection-designer.ts:1)

테스트/검증 근거: [ui/admin/src/designer/collection-designer.test.tsx:165](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/designer/collection-designer.test.tsx:165) / [ui/admin/src/designer/collection-designer.test.tsx:242](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/designer/collection-designer.test.tsx:242)

## UI-07 · duplicate/truncate/scaffold

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: 컬렉션 복제 시 ID를 제거하고 이름·인덱스를 조정하며 base/auth/view scaffold와 truncate를 제공한다.

PgBase: 복제·base/auth/view scaffold·확인 후 truncate가 있다. 신규 text ID 정책과 인증 scaffold 양쪽 정책 복사도 현재 트리에 연결됐다.

차이/남은 범위: UUID 기본값과 선택적 exact text ID는 PB의 기본 text ID 모델과 의도적으로 다르다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/collections/collectionUpsertModal.js#L226)

PgBase 근거: [ui/admin/src/pages/collections-page.tsx:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collections-page.tsx:12) / [ui/admin/src/pages/collections-page.tsx:108](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collections-page.tsx:108) / [ui/admin/src/designer/collection-designer-state.ts:122](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/designer/collection-designer-state.ts:122)

테스트/검증 근거: [ui/admin/src/designer/collection-identity.test.ts:7](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/designer/collection-identity.test.ts:7) / [ui/admin/e2e/authenticated/remaining-development.spec.ts:152](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/e2e/authenticated/remaining-development.spec.ts:152)

## UI-08 · type·view SQL dry-run

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: base/auth/view 타입과 SQL view 편집기를 제공하고 SQL dry-run으로 샘플과 필드를 확인한다.

PgBase: SQL view 또는 declarative builder를 생성·편집하고 명시적 preview/apply 및 최대 10개 샘플을 제공한다. 기존 builder 수정과 큰 numeric 필터 보존이 구현됐다.

차이/남은 범위: PB의 debounce 자동 SQL 샘플 대신 명시적 preview이며 SQL dialect는 PostgreSQL이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/collections/collectionViewQueryTab.js#L20)

PgBase 근거: [ui/admin/src/pages/view-definition-editor.tsx:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/view-definition-editor.tsx:1) / [ui/admin/src/pages/view-sample-preview.tsx:8](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/view-sample-preview.tsx:8) / [ui/admin/src/designer/collection-designer-state.ts:122](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/designer/collection-designer-state.ts:122)

테스트/검증 근거: [ui/admin/src/pages/collection-designer-page.test.tsx:11](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collection-designer-page.test.tsx:11) / [ui/admin/src/pages/view-definition-editor.test.tsx:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/view-definition-editor.test.tsx:18) / [ui/admin/e2e/authenticated/remaining-development.spec.ts:104](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/e2e/authenticated/remaining-development.spec.ts:104)

## UI-09 · field type·validation editor

**보완 필요** · P1 · prior-run · 기존 항목

PocketBase: 타입별 설정 화면에 일반 제약 외에 protected file, JSON maxSize, 날짜 min/max 등 세부 옵션을 노출한다.

PgBase: 네이티브 PostgreSQL 타입 및 text/select/numeric/file/editor/geo/password/autodate 옵션, 필드 hidden/presentable을 편집한다.

차이/남은 범위: 디자이너에 파일 protected, JSON 최대 크기, 날짜 min/max 입력이 없다. backend 기능 존재 여부와 별개인 UI 노출 공백이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/fields/file/settings.js#L305)

PgBase 근거: [ui/admin/src/pages/collection-field-editor.tsx:32](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collection-field-editor.tsx:32) / [ui/admin/src/pages/field-options-editor.tsx:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/field-options-editor.tsx:14)

테스트/검증 근거: [ui/admin/src/designer/field-options.test.ts:6](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/designer/field-options.test.ts:6) / [ui/admin/src/pages/field-options-editor.test.tsx:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/field-options-editor.test.tsx:15)

## UI-10 · relation 제약·index

**보완 필요** · P2 · prior-run · 기존 항목

PocketBase: 관계 대상/선택 개수/삭제 동작과 인덱스 설정을 제공하며 인덱스 raw SQL 편집도 가능하다.

PgBase: 관계 대상·cardinality·min/max·onDelete와 이름/ordered fields/unique 구조형 인덱스를 편집한다.

차이/남은 범위: PB raw SQL 표현식/부분 인덱스에 대응하는 designer UI는 없다. PostgreSQL 관계 제약 자체는 더 명시적이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/collections/indexUpsertModal.js#L209)

PgBase 근거: [ui/admin/src/pages/collection-field-editor.tsx:60](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collection-field-editor.tsx:60) / [ui/admin/src/pages/collection-designer-page.tsx:72](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collection-designer-page.tsx:72)

테스트/검증 근거: [ui/admin/src/designer/collection-designer.test.tsx:106](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/designer/collection-designer.test.tsx:106) / [ui/admin/src/designer/collection-draft.test.ts:48](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/designer/collection-draft.test.ts:48)

## UI-11 · CRUD 권한 규칙 편집·미리보기

**보완 필요** · P1 · static-only · 기존 항목

PocketBase: list/view/create/update/delete 규칙 편집기에 필드·request 키 자동완성 및 null/공개 구분을 제공한다. authRule/manageRule도 별도 편집한다.

PgBase: 5개 CRUD action의 JSON AST와 preset, 사용자/레코드 ID 입력 및 preview 화면이 있다. 관계 컬렉션 user-access 저장은 별도 API로 실제 연결됐다. 다만 rules 저장/preview의 실제 서버 계약은 일치하지 않는다.

차이/남은 범위: 우선 수정할 자체 계약 오류: setRules가 {rules,revision}을 전송하지만 서버 strict PUT DTO는 rules만 받아 revision을 거절한다. preview는 단일 Rule 자리로 전체 5-action rules 객체를 보낸다. memoryBackend 테스트는 이 불일치를 잡지 못한다. 이와 별도로 필드/request 자동완성, auth/manage 규칙 UI, create preview가 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/collections/collectionRulesTab.js#L13)

PgBase 근거: [ui/admin/src/pages/record-rules-editor.tsx:4](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-rules-editor.tsx:4) / [ui/admin/src/pages/collections-page.tsx:64](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collections-page.tsx:64) / [ui/admin/src/backend/http-admin-ports.ts:522](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/backend/http-admin-ports.ts:522) / [internal/api/rules.go:52](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/rules.go:52) / [internal/api/admin_handlers.go:384](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/admin_handlers.go:384) / [internal/recordaccess/preview.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordaccess/preview.go:16)

테스트/검증 근거: [ui/admin/src/pages/record-rules-editor.test.tsx:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-rules-editor.test.tsx:1) / [ui/admin/src/pages/collections-page.test.tsx:54](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collections-page.test.tsx:54) / [ui/admin/e2e/authenticated/remaining-development.spec.ts:78](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/e2e/authenticated/remaining-development.spec.ts:78)

## UI-12 · schema export/import/diff

**보완 필요** · P1 · prior-run · 기존 항목

PocketBase: 컬렉션 선택 export, JSON 미리보기, import의 old/new diff와 deleteMissing 확인을 제공한다.

PgBase: 전체 schema JSON export, JSON 붙여넣기, 계획·SQL·변경 컬렉션 요약 preview와 apply/recovery가 있다. 생략한 컬렉션은 유지한다고 명시한다.

차이/남은 범위: 선택한 컬렉션만 export, 파일 선택 import, old/new 나란히 diff, deleteMissing UX는 없다. SQL 계획 표시는 구조 diff와 같지 않다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/settings/sync/importCollectionsReviewModal.js#L38)

PgBase 근거: [ui/admin/src/pages/schema-transfer-page.tsx:74](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/schema-transfer-page.tsx:74) / [ui/admin/src/pages/schema-transfer-page.tsx:133](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/schema-transfer-page.tsx:133)

테스트/검증 근거: [ui/admin/src/pages/schema-transfer-page.test.tsx:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/schema-transfer-page.test.tsx:24) / [ui/admin/src/pages/schema-transfer-page.test.tsx:79](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/schema-transfer-page.test.tsx:79)

## UI-14 · collection API preview

**보완 필요** · P2 · prior-run · 기존 항목

PocketBase: 컬렉션별 CRUD/realtime/batch와 auth methods/password/OAuth/OTP/refresh/verification/reset/email-change 예제를 탭으로 제공한다.

PgBase: collection 필드와 여러 언어·호출 audience 예제를 제공하며 view의 변경 예제 제외, Dart audience, 파일 multipart 경로가 검사된다.

차이/남은 범위: PB 수준의 인증 컬렉션별 모든 auth 메서드 예제가 없다. 관계 설명이 여전히 UUID라고 안내하여 text ID 컬렉션 설명이 부정확하다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/apiPreview/apiPreviewModal.js#L34)

PgBase 근거: [ui/admin/src/pages/collection-api-examples.tsx:115](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collection-api-examples.tsx:115)

테스트/검증 근거: [ui/admin/src/pages/collection-api-examples.test.tsx:29](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collection-api-examples.test.tsx:29) / [ui/admin/src/pages/collection-api-examples.test.tsx:35](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collection-api-examples.test.tsx:35)

## UI-15 · list/filter/sort/cursor paging

**보완 필요** · P2 · prior-run · 기존 항목

PocketBase: 레코드 목록은 40행 페이지, 일반 검색어 또는 filter 식, 보이는 관계의 expand, presentable 필드를 통한 표시/정렬을 제공한다.

PgBase: 정확한 numeric 표시, 서버가 허용하는 정렬, filter 식, cursor Next/Previous, 관계 ID 표시가 있다.

차이/남은 범위: PB의 일반 검색어 변환·검색 기록 및 목록 내 관계 presentable label 확장이 없다. offset 대신 cursor 자체는 의도적 차이이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/records/recordsList.js#L6)

PgBase 근거: [ui/admin/src/pages/records-page.tsx:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/records-page.tsx:17) / [ui/admin/src/workspace/record-workspace.ts:96](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/workspace/record-workspace.ts:96)

테스트/검증 근거: [ui/admin/src/pages/records-page.test.tsx:28](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/records-page.test.tsx:28) / [ui/admin/src/workspace/record-workspace.test.tsx:64](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/workspace/record-workspace.test.tsx:64)

## UI-16 · persisted columns

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: 컬렉션별로 목록 표시 컬럼 선택을 브라우저 저장소에 유지한다.

PgBase: 관리자/컬렉션 범위에 보이는 컬럼을 저장하고 사라진 필드를 제거하며 id는 유지한다.

차이/남은 범위: 컬럼 표시 선택의 저장 목적은 충족한다. 이 항목은 PB의 모든 테이블 레이아웃 동작을 뜻하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/records/recordsList.js#L11)

PgBase 근거: [ui/admin/src/pages/records-page.tsx:46](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/records-page.tsx:46) / [ui/admin/src/workspace/record-productivity.ts:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/workspace/record-productivity.ts:1)

테스트/검증 근거: [ui/admin/src/workspace/record-productivity.test.ts:10](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/workspace/record-productivity.test.ts:10)

## UI-17 · bulk delete·JSON export

**보완 필요** · P2 · prior-run · 기존 항목

PocketBase: 선택한 레코드를 batch 삭제하고 선택 레코드들을 JSON으로 다운로드한다.

PgBase: shift 선택·개별 ETag 기반 bulk 삭제, 일부 실패 유지, 활성 filter/sort 전체 페이지 JSON export가 있다.

차이/남은 범위: 현재 JSON export는 선택 행이 아니라 활성 쿼리 범위이다. PB의 임의 선택 레코드만 export하는 흐름은 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/records/recordsList.js#L193)

PgBase 근거: [ui/admin/src/pages/records-page.tsx:52](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/records-page.tsx:52) / [ui/admin/src/workspace/record-workspace.ts:159](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/workspace/record-workspace.ts:159) / [ui/admin/src/workspace/record-workspace.ts:180](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/workspace/record-workspace.ts:180)

테스트/검증 근거: [ui/admin/src/workspace/record-workspace.test.tsx:223](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/workspace/record-workspace.test.tsx:223) / [ui/admin/src/workspace/record-workspace.test.tsx:245](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/workspace/record-workspace.test.tsx:245)

## UI-19 · CRUD/duplicate/draft

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: 레코드를 생성/수정/삭제·복제하고 변경 draft를 localStorage에 보존/복원한다.

PgBase: 별도 레코드 편집 화면, idempotent create, 변경 필드 patch/ETag 충돌 처리, duplicate와 scoped draft 복구가 있다. text ID를 정확히 보존한다.

차이/남은 범위: PB 모달 대신 별도 화면이며 비밀번호/token draft를 저장하지 않는다. CRUD 목적은 충족한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/records/recordUpsertModal.js#L144)

PgBase 근거: [ui/admin/src/pages/record-edit-page.tsx:140](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-edit-page.tsx:140) / [ui/admin/src/workspace/record-workspace.ts:210](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/workspace/record-workspace.ts:210) / [ui/admin/src/workspace/record-productivity.ts:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/workspace/record-productivity.ts:1)

테스트/검증 근거: [ui/admin/src/pages/record-edit-page.test.tsx:67](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-edit-page.test.tsx:67) / [ui/admin/src/workspace/record-workspace.test.tsx:266](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/workspace/record-workspace.test.tsx:266) / [ui/admin/src/workspace/record-productivity.test.ts:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/workspace/record-productivity.test.ts:16) / [ui/admin/e2e/authenticated/remaining-development.spec.ts:4](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/e2e/authenticated/remaining-development.spec.ts:4)

## UI-20 · relation picker/inline target

**보완 필요** · P2 · prior-run · 기존 항목

PocketBase: 관계 대상 50행 목록을 검색/페이지 이동하며 선택 레코드들을 별도로 가져와 label과 순서를 복원한다.

PgBase: 빈 검색어로 indexed browse, cursor 이동, exact indexed label 또는 ID 검색, 선택 label hydration, inline create/edit를 제공한다. text ID의 대소문자를 보존한다.

차이/남은 범위: 일반 문자열 부분검색/임의 filter 식 대신 한 indexed text 필드의 exact 검색이다. hydration은 바이트·요청 상한을 넘으면 ID 표시로 남는다. 빈 검색어 browse는 이미 있으므로 누락으로 세지 않았다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/records/recordsPickerModal.js#L4)

PgBase 근거: [ui/admin/src/relation/relation-query.ts:39](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/relation/relation-query.ts:39) / [ui/admin/src/relation/relation-target-picker.tsx:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/relation/relation-target-picker.tsx:1)

테스트/검증 근거: [ui/admin/src/relation/relation-picker.test.tsx:42](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/relation/relation-picker.test.tsx:42) / [ui/admin/src/relation/relation-picker.test.tsx:125](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/relation/relation-picker.test.tsx:125) / [ui/admin/src/pages/record-edit-page.test.tsx:157](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-edit-page.test.tsx:157)

## UI-21 · file mapping/thumbnail/download

**보완 필요** · P2 · prior-run · 기존 항목

PocketBase: 레코드의 파일을 업로드/삭제/미리보기하고 썸네일·다운로드를 제공하며 다른 레코드의 공개 파일을 고르는 picker도 제공한다.

PgBase: 원자적 multipart 연결, 보호 토큰을 통한 이미지 preview, 서버 public URL, thumbnail 다운로드, 삭제 및 업로드 전 이미지/오디오/비디오 preview가 있다.

차이/남은 범위: 저장된 파일의 UI preview는 이미지 중심이다. PB의 다른 컬렉션/레코드 공개 파일 라이브러리 picker는 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/records/recordFilePickerModal.js#L48)

PgBase 근거: [ui/admin/src/pages/record-edit-page.tsx:82](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-edit-page.tsx:82) / [ui/admin/src/pages/record-edit-page.tsx:114](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-edit-page.tsx:114) / [ui/admin/src/pages/record-edit-page.tsx:128](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-edit-page.tsx:128)

테스트/검증 근거: [ui/admin/src/pages/file-field.test.tsx:39](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/file-field.test.tsx:39) / [ui/admin/src/pages/file-field.test.tsx:87](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/file-field.test.tsx:87) / [ui/admin/src/pages/record-edit-page.test.tsx:46](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-edit-page.test.tsx:46)

## UI-22 · auth users lifecycle actions

**보완 필요** · P1 · prior-run · 기존 항목

PocketBase: 인증 컬렉션의 일반 레코드 CRUD 화면에서 verification/reset/token reset/OAuth 연결 관리와 지정 기간 impersonation을 수행한다.

PgBase: auth 컬렉션은 일반 Records 화면에서 생성/프로필 변경/삭제 가능하다. 별도 Users 화면은 전체 realm 사용자의 활성화·verification·메일 요청·session revoke·OAuth unlink·5분 impersonation을 제공한다.

차이/남은 범위: Users 표는 authCollectionId/공개 record ID/realm filter와 해당 Records 편집 링크를 노출하지 않는다. impersonation 기간은 300초 고정이다. 인증 CRUD 전체가 없다는 판정은 부정확하다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/records/recordUpsertModal.js#L910)

PgBase 근거: [ui/admin/src/pages/users-page.tsx:90](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/users-page.tsx:90) / [ui/admin/src/pages/users-page.tsx:119](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/users-page.tsx:119) / [ui/admin/src/pages/users-page.tsx:145](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/users-page.tsx:145) / [ui/admin/src/pages/record-edit-page.tsx:163](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-edit-page.tsx:163) / [internal/userauth/lifecycle.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/lifecycle.go:13)

테스트/검증 근거: [ui/admin/src/pages/users-page.test.tsx:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/users-page.test.tsx:9) / [ui/admin/e2e/authenticated/remaining-development.spec.ts:152](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/e2e/authenticated/remaining-development.spec.ts:152)

## UI-23 · realm/options/OAuth/OTP/MFA

**보완 필요** · P1 · prior-run · 기존 항목

PocketBase: auth 컬렉션에서 password identity fields, OAuth providers, email OTP, MFA 조건/기간, 로그인 경고와 token 옵션을 설정한다.

PgBase: realm 생성·등록 허용·profile schema, password 최소 길이, TOTP/email OTP 설정, OAuth provider 설정 및 Apple secret 생성이 있다.

차이/남은 범위: 임의 password identity fields, PB식 MFA 조건·기간, login-alert, 목적별 token TTL/일괄 무효화 UI가 없다. TOTP는 PB의 두 인증수단 조합 MFA와 동일한 설정 모델이 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/collections/collectionAuthOptionsTab.js#L15)

PgBase 근거: [ui/admin/src/pages/auth-collections-page.tsx:96](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/auth-collections-page.tsx:96) / [ui/admin/src/components/auth-policy-editor.tsx:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/components/auth-policy-editor.tsx:22) / [ui/admin/src/components/auth-administration-panel.tsx:154](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/components/auth-administration-panel.tsx:154)

테스트/검증 근거: [ui/admin/src/pages/auth-collections-page.test.tsx:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/auth-collections-page.test.tsx:9) / [ui/admin/src/pages/auth-collections-page.test.tsx:57](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/auth-collections-page.test.tsx:57)

## UI-24 · mail template/SMTP test

**보완 필요** · P2 · prior-run · 기존 항목

PocketBase: 인증 컬렉션별 verify/reset/email-change/OTP/login-alert 템플릿과 test email, SMTP 설정을 제공한다.

PgBase: verify/password_reset/email_change/otp 4개 템플릿 및 realm SMTP 편집·비밀 redaction·테스트를 제공한다.

차이/남은 범위: login-alert 템플릿/활성화 UI와 PB placeholder 전체에 대응하는 편집 보조가 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/collections/collectionAuthOptionsTab.js#L49)

PgBase 근거: [ui/admin/src/components/auth-administration-panel.tsx:15](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/components/auth-administration-panel.tsx:15) / [ui/admin/src/components/auth-administration-panel.tsx:211](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/components/auth-administration-panel.tsx:211) / [ui/admin/src/components/auth-administration-panel.tsx:236](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/components/auth-administration-panel.tsx:236)

테스트/검증 근거: [ui/admin/src/pages/auth-collections-page.test.tsx:57](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/auth-collections-page.test.tsx:57) / [ui/admin/src/pages/auth-collections-page.test.tsx:81](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/auth-collections-page.test.tsx:81)

## UI-25 · service tokens/admin application settings

**보완 필요** · P2 · prior-run · 기존 항목

PocketBase: PB는 application name/URL/accent, batch, trusted proxy, rate limits, superuser IP, hideControls를 설정한다. 별도 scoped service-token 제품 개념은 아니다.

PgBase: scoped service token 발급·한 번 공개·revoke, 현재 관리자 이메일/IP, theme/accent/branding, logs·backup·rate limits·trusted proxy CIDR 설정이 있다.

차이/남은 범위: service token은 의도적인 별도 권한 모델이다. application URL, batch 설정, production hide/lock controls 및 PB trusted-header 입력에 대응하는 화면은 없다. branding은 주로 문서 title에 반영된다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/settings/application/pageApplicationSettings.js#L190)

PgBase 근거: [ui/admin/src/pages/service-tokens-page.tsx:28](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/service-tokens-page.tsx:28) / [ui/admin/src/pages/settings-page.tsx:81](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/settings-page.tsx:81) / [ui/admin/src/pages/profile-settings.tsx:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/profile-settings.tsx:21)

테스트/검증 근거: [ui/admin/src/pages/service-tokens-page.test.tsx:8](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/service-tokens-page.test.tsx:8) / [ui/admin/src/pages/settings-page.test.tsx:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/settings-page.test.tsx:14) / [ui/admin/src/pages/settings-page.test.tsx:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/settings-page.test.tsx:21)

## UI-26 · logs search/detail/statistics/truncate

**보완 필요** · P2 · prior-run · 기존 항목

PocketBase: filter/일반 검색어로 로그를 찾고 level/message/data 상세 및 시간별 histogram/zoom을 제공한다.

PgBase: HTTP status/method/route/time 범위 검색, cursor, 상세, CSV, 삭제와 상태별 집계 meter를 제공한다.

차이/남은 범위: PB 구조화 log level/message/data의 일반 탐색 UI와 시간 histogram/zoom이 없다. Pg 통계 로드는 활성 필터를 넘기지 않아 필터 결과 그래프와도 다르다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/logs/logsChart.js#L21)

PgBase 근거: [ui/admin/src/pages/operations-console.tsx:57](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/operations-console.tsx:57) / [ui/admin/src/pages/operations-console.tsx:87](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/operations-console.tsx:87)

테스트/검증 근거: [ui/admin/src/pages/operations-console.test.tsx:45](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/operations-console.test.tsx:45)

## UI-27 · scheduled jobs/cron configure/run

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: 등록된 cron job의 ID·표현식·실행 버튼을 제공한다. PB 이 화면 자체가 임의 cron handler를 만드는 편집기는 아니다.

PgBase: 등록된 job의 schedule/timezone/enabled 수정, queue/run과 lease/running 상태에 따른 제어가 있다.

차이/남은 범위: PG의 durable scheduler 설정 모델로 관리 목적을 충족하며 arbitrary code 생성은 양쪽 비교 범위가 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/settings/crons/cronsList.js#L28)

PgBase 근거: [ui/admin/src/pages/operations-console.tsx:4](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/operations-console.tsx:4) / [ui/admin/src/pages/operations-console.tsx:113](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/operations-console.tsx:113)

테스트/검증 근거: [ui/admin/src/pages/operations-console.test.tsx:19](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/operations-console.test.tsx:19) / [ui/admin/src/pages/operations-console.test.tsx:39](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/operations-console.test.tsx:39)

## UI-28 · backup lifecycle

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: 백업 생성·업로드·다운로드·삭제·확인 후 restore, 자동 cron과 retention/S3 설정을 제공한다.

PgBase: 관리 백업 생성/업로드/download/restore/delete와 비동기 job polling, 자동 schedule/timezone 및 저장소 retention 편집이 있다.

차이/남은 범위: 비동기 PG 복구 작업 모델과 archive 포맷이 다르지만 관리자 관리 흐름은 있다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/settings/backups/backupsList.js#L19)

PgBase 근거: [ui/admin/src/pages/operations-console.tsx:118](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/operations-console.tsx:118) / [ui/admin/src/pages/settings-page.tsx:234](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/settings-page.tsx:234) / [ui/admin/src/pages/backup-storage-page.tsx:60](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/backup-storage-page.tsx:60)

테스트/검증 근거: [ui/admin/src/pages/operations-console.test.tsx:65](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/operations-console.test.tsx:65) / [ui/admin/src/pages/operations-console.test.tsx:75](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/operations-console.test.tsx:75) / [ui/admin/src/pages/backup-storage-page.test.tsx:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/backup-storage-page.test.tsx:18)

## UI-29 · file/backup storage settings/test

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: 파일과 백업 저장소의 S3 endpoint/bucket/region/access/secret 및 연결 test를 제공한다.

PgBase: 파일/백업 local/S3 설정과 저장된 설정 test, 비밀 omission/redaction, 파일 저장소 migration preview·시작·재개·취소를 제공한다.

차이/남은 범위: PG 저장소 이동 job은 PB 화면보다 추가된 경로이다. 해당 차이를 미구현으로 세지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/base/s3ConfigFields.js#L91)

PgBase 근거: [ui/admin/src/pages/storage-settings-page.tsx:110](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/storage-settings-page.tsx:110) / [ui/admin/src/pages/backup-storage-page.tsx:67](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/backup-storage-page.tsx:67)

테스트/검증 근거: [ui/admin/src/pages/storage-settings-page.test.tsx:29](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/storage-settings-page.test.tsx:29) / [ui/admin/src/pages/storage-settings-page.test.tsx:61](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/storage-settings-page.test.tsx:61) / [ui/admin/src/pages/backup-storage-page.test.tsx:43](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/backup-storage-page.test.tsx:43)

## UI-30 · SQL console/history/result/CSV

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: SQL editor, 실행 확인, 로컬 history, 결과 정렬·행 표시·CSV를 제공한다.

PgBase: 읽기 기본 모드, 허용된 extension write의 명시적 확인, typed params/results, 정확한 numeric 표시, 취소·durable history/recovery·CSV가 있다.

차이/남은 범위: PB SQLite 임의 SQL console을 그대로 제공하는 것은 아니다. PG 정책에 따른 bounded read/허용 write, 서버 history는 의도적 범위 변경이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/settings/sql/pageSQLConsole.js#L5)

PgBase 근거: [ui/admin/src/pages/sql-console-page.tsx:54](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/sql-console-page.tsx:54) / [ui/admin/src/pages/sql-console-page.tsx:134](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/sql-console-page.tsx:134)

테스트/검증 근거: [ui/admin/src/pages/sql-console-page.test.tsx:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/sql-console-page.test.tsx:26) / [ui/admin/src/pages/sql-console-page.test.tsx:39](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/sql-console-page.test.tsx:39) / [ui/admin/src/pages/sql-console-page.test.tsx:239](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/sql-console-page.test.tsx:239)

## UI-32 · keyboard relation selection

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: 드롭다운에서 방향키로 결과 focus를 이동하고 키보드로 선택한다.

PgBase: ARIA combobox/listbox와 active descendant, Arrow/Home/End/Enter/Escape 선택을 제공한다.

차이/남은 범위: PB DOM focus 대신 ARIA active descendant를 사용하며 키보드 선택 목적은 충족한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/base/dropdownKeyboardNav.js#L32)

PgBase 근거: [ui/admin/src/relation/relation-target-picker.tsx:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/relation/relation-target-picker.tsx:1)

테스트/검증 근거: [ui/admin/src/relation/relation-picker.test.tsx:291](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/relation/relation-picker.test.tsx:291) / [ui/admin/src/pages/record-edit-page.test.tsx:173](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-edit-page.test.tsx:173)

## UI-33 · 리치 텍스트·HTML source 편집

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: TinyMCE에서 rich text, HTML source, 링크·이미지·표와 convertURLs를 편집한다.

PgBase: Tiptap rich text/표/link/image, HTML source 전환과 portable URL 처리를 제공한다.

차이/남은 범위: 좁은 rich text+source 목적은 충족한다. TinyMCE 플러그인 전체/미디어 라이브러리 동일성을 의미하지 않으며 source 안내도 지원 포맷 제한을 명시한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/base/tinymce.js#L146)

PgBase 근거: [ui/admin/src/pages/rich-text-field.tsx:34](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/rich-text-field.tsx:34) / [ui/admin/src/pages/rich-text-field.tsx:62](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/rich-text-field.tsx:62)

테스트/검증 근거: [ui/admin/src/pages/advanced-field-inputs.test.tsx:8](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/advanced-field-inputs.test.tsx:8) / [ui/admin/src/pages/advanced-field-inputs.test.tsx:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/advanced-field-inputs.test.tsx:12)

## UI-34 · geoPoint 좌표와 지도에서 위치 선택

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: 경위도 숫자와 OpenStreetMap/Leaflet 지도에서 클릭·드래그하여 위치를 선택한다.

PgBase: 좌표 입력, offline Natural Earth SVG 지도 클릭/방향키 선택·zoom을 제공한다.

차이/남은 범위: 외부 타일 대신 대략적인 offline 해안선 지도를 택했다. 도로/주소 수준 지도나 Leaflet drag/pan UX와 동일하지 않다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/base/leaflet.js#L72)

PgBase 근거: [ui/admin/src/pages/geo-point-field.tsx:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/geo-point-field.tsx:14) / [ui/admin/src/pages/geo-point-field.tsx:38](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/geo-point-field.tsx:38)

테스트/검증 근거: [ui/admin/src/pages/advanced-field-inputs.test.tsx:26](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/advanced-field-inputs.test.tsx:26)

## UI-35 · 날짜·시간 picker와 local timezone 표시

**보완 필요** · P2 · static-only · 기존 항목

PocketBase: datetime-local step=1로 초 단위 편집을 지원하고 local timezone을 표시하며 RFC3339로 저장한다.

PgBase: local timezone 안내와 datetime-local 입력을 통해 UTC ISO로 변환한다.

차이/남은 범위: 표시 변환이 slice(0,16)이며 step=1이 없어 PB 초 단위 편집 흐름을 제공하지 않는다. 값을 건드리지 않은 저장까지 항상 초가 사라진다는 의미는 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/fields/date/input.js#L18)

PgBase 근거: [ui/admin/src/pages/record-edit-page.tsx:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-edit-page.tsx:17) / [ui/admin/src/pages/record-edit-page.tsx:59](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-edit-page.tsx:59)

테스트/검증 근거:

## UI-X01 · 여러 관리자 계정 생성·편집·삭제

**미구현** · P1 · static-only · 추가 확인 항목

PocketBase: Manage superusers 링크가 _superusers 인증 컬렉션을 열어 같은 레코드 CRUD UI로 여러 관리자 계정을 관리한다.

PgBase: Settings에서 현재 관리자 이메일과 IP allowlist만 편집하며 최초 설치에서 첫 관리자를 만든다.

차이/남은 범위: 다른 관리자 목록·생성·삭제·비밀번호 변경을 처리하는 관리 화면이 없다. 사용자 auth 컬렉션 CRUD와 관리자 계정 관리 권한 모델을 구분해야 한다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/base/appHeader.js#L79)

PgBase 근거: [ui/admin/src/pages/profile-settings.tsx:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/profile-settings.tsx:21) / [ui/admin/src/pages/setup-page.tsx:59](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/setup-page.tsx:59) / [ui/admin/src/app.tsx:105](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/app.tsx:105)

테스트/검증 근거: [ui/admin/src/pages/settings-page.test.tsx:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/settings-page.test.tsx:21)

## UI-X02 · 필드 복제·순서 재배치

**미구현** · P2 · static-only · 추가 확인 항목

PocketBase: 필드 설정에 duplicate가 있고 collectionFieldsTab sortable로 필드 순서를 재배치한다.

PgBase: 필드 추가·이름/타입/옵션 편집·삭제와 ordinal 기반 표시가 있다.

차이/남은 범위: UI에 필드 duplicate 또는 drag/move 순서 변경 동작이 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/base/fieldSettings.js#L22)

PgBase 근거: [ui/admin/src/pages/collection-field-editor.tsx:32](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collection-field-editor.tsx:32) / [ui/admin/src/pages/collection-designer-page.tsx:58](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collection-designer-page.tsx:58)

테스트/검증 근거:

## UI-X03 · 컬렉션 관계 개요 ERD

**미구현** · P3 · static-only · 추가 확인 항목

PocketBase: 컬렉션 overview의 Fields and relations 탭이 ERD를 렌더링한다.

PgBase: 컬렉션 목록에 필드/관계 개수를 표시하고 개별 필드에서 관계 대상을 편집한다.

차이/남은 범위: 전체 컬렉션 관계 그래프/ERD 화면이 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/collections/collectionsOverviewModal.js#L119)

PgBase 근거: [ui/admin/src/pages/collections-page.tsx:118](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collections-page.tsx:118) / [ui/admin/src/pages/collection-field-editor.tsx:60](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collection-field-editor.tsx:60)

테스트/검증 근거:

## UI-X04 · 인증 허용 규칙·사용자 관리 권한 규칙 전용 편집

**미구현** · P1 · static-only · 추가 확인 항목

PocketBase: authRule은 인증 직전 조건, manageRule은 다른 인증 레코드를 관리할 수 있는 조건으로 별도 편집한다.

PgBase: CRUD JSON rule editor는 list/view/create/update/delete만 다룬다. auth 설정은 password/OTP 등의 방식 설정에 집중한다.

차이/남은 범위: auth/manage 규칙을 직접 읽고 고치는 전용 UI가 없다. raw schema import에서 규칙 문서를 전달할 수 있는 것과 일상 편집 UX를 구분했다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/collections/collectionRulesTab.js#L215)

PgBase 근거: [ui/admin/src/pages/record-rules-editor.tsx:4](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-rules-editor.tsx:4) / [ui/admin/src/components/auth-policy-editor.tsx:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/components/auth-policy-editor.tsx:22) / [ui/admin/src/backend/ports.ts:293](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/backend/ports.ts:293)

테스트/검증 근거:

## UI-X05 · 인증 컬렉션 token 기간·종류별 일괄 무효화

**미구현** · P1 · static-only · 추가 확인 항목

PocketBase: token options에서 토큰 목적별 duration을 편집하고 secret 재생성으로 해당 토큰을 invalidate한다.

PgBase: 사용자별 Revoke sessions 및 service token revoke는 있으나 auth 정책 editor에는 OTP 기간만 있다.

차이/남은 범위: auth/verification/reset/email-change 등의 collection-wide TTL 및 일괄 invalidate 조작이 없다. 사용자별 revoke와 동일하지 않다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/collections/tokenOptionsAccordion.js#L33)

PgBase 근거: [ui/admin/src/components/auth-policy-editor.tsx:66](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/components/auth-policy-editor.tsx:66) / [ui/admin/src/pages/users-page.tsx:158](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/users-page.tsx:158) / [ui/admin/src/backend/ports.ts:293](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/backend/ports.ts:293)

테스트/검증 근거:

## UI-X06 · 기존 공개 파일을 선택하는 미디어 라이브러리

**미구현** · P2 · static-only · 추가 확인 항목

PocketBase: 공개 파일 필드가 있는 다른 컬렉션을 선택하고 기존 파일을 검색/페이지 이동해 재사용한다.

PgBase: 레코드에 로컬 파일을 올리거나 rich text에서 URL을 직접 입력한다.

차이/남은 범위: 기존 파일을 collection/record 기준으로 탐색·선택하는 라이브러리 UI가 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/records/recordFilePickerModal.js#L48)

PgBase 근거: [ui/admin/src/pages/record-edit-page.tsx:128](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-edit-page.tsx:128) / [ui/admin/src/pages/rich-text-field.tsx:77](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/rich-text-field.tsx:77)

테스트/검증 근거:

## UI-X07 · 컬렉션 검색·pin·system 분리 내비게이션

**미구현** · P3 · static-only · 추가 확인 항목

PocketBase: sidebar에서 컬렉션 이름을 검색하고 pin을 저장하며 system 컬렉션을 별도 그룹으로 보여 준다.

PgBase: 전체 컬렉션을 이름순 목록으로 나열하고 기본 관리자 링크를 표시한다.

차이/남은 범위: 컬렉션 검색/pin/system 그룹을 제공하지 않아 컬렉션 수가 많을 때 탐색 UX가 다르다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/collections/collectionsSidebar.js#L7)

PgBase 근거: [ui/admin/src/pages/collections-page.tsx:118](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collections-page.tsx:118) / [ui/admin/src/app.tsx:105](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/app.tsx:105)

테스트/검증 근거: [ui/admin/src/pages/collections-page.test.tsx:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collections-page.test.tsx:14)

## UI-X08 · 운영 화면의 파괴적 제어 잠금

**미구현** · P2 · static-only · 추가 확인 항목

PocketBase: application hideControls 설정으로 collection 생성/수정 버튼을 숨기고 record 수정에는 추가 unlock을 요구한다.

PgBase: 스키마 preview와 삭제 확인, stale 상태 차단은 있으나 전체 환경의 편집 잠금 설정은 없다.

차이/남은 범위: PB hide/lock controls 운영 모드와 대응하는 전역 UI 정책이 없다. 개별 작업 확인 대화상자가 그 기능을 대신하지는 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/settings/application/pageApplicationSettings.js#L249)

PgBase 근거: [ui/admin/src/pages/settings-page.tsx:81](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/settings-page.tsx:81) / [ui/admin/src/pages/collection-designer-page.tsx:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/collection-designer-page.tsx:22) / [ui/admin/src/pages/record-edit-page.tsx:158](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/src/pages/record-edit-page.tsx:158)

테스트/검증 근거:

## OPS-01 · serve CLI/runtime

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: serve [domains]는 기본 127.0.0.1:8090과 내장 SQLite로 시작한다.

PgBase: serve → config → RunManaged; PostgreSQL URL/토큰키와 외부 DB가 필요하다.

차이/남은 범위: 같은 시작 경험/플래그가 아니다. SQLite 단일 파일 배포 대신 PostgreSQL 운영이 필요하다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/cmd/serve.go#L14)

PgBase 근거: [internal/cli/run.go:173](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/run.go:173) / [internal/config/env.go:37](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/config/env.go:37) / [pgbase/managed.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/managed.go:1)

테스트/검증 근거: [internal/cli/run_test.go](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/run_test.go)

## OPS-02 · 통합 runtime settings GET/PATCH/reload

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: Settings는 SMTP/S3/meta/logs/backups/rateLimits 등을 한 모델로 저장·조회한다.

PgBase: 암호화 runtime settings와 revision/reload/subscribe 지원. SMTP·storage는 별도 관리 API.

차이/남은 범위: PB settings JSON을 그대로 주고받을 수 없다. 기능별 설정 모델과 reload 계약이 다르다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/settings_model.go#L123)

PgBase 근거: [internal/runtimesettings/settings.go:80](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/runtimesettings/settings.go:80) / [internal/runtimesettings/settings.go:180](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/runtimesettings/settings.go:180) / [internal/runtimesettings/settings.go:232](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/runtimesettings/settings.go:232)

테스트/검증 근거: [internal/runtimesettings/settings_integration_test.go](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/runtimesettings/settings_integration_test.go)

## OPS-05 · request logs list/detail/stats/truncate

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: filter/sort/page로 id,created,level,message,data.*를 검색하고 filter를 적용한 통계를 얻는다.

PgBase: before/statusClass/method/route/from/to 검색, 상세/삭제/통계 구현.

차이/남은 범위: PB filter 언어 및 arbitrary data 검색 없음. 통계 API는 요청 filter를 전달하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/logs.go#L25)

PgBase 근거: [internal/api/operations_console.go:45](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/operations_console.go:45) / [internal/api/operations_console.go:83](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/operations_console.go:83)

테스트/검증 근거: [internal/api/operations_console_test.go:137](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/operations_console_test.go:137)

## OPS-06 · login/OTP rate limits

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: 기본 auth 경로 규칙을 전역 limiter와 IP 기준으로 적용한다.

PgBase: 계정·네트워크 로그인 실패, 등록 quota를 DB로 저장하고 재시도 제한한다.

차이/남은 범위: 보호 기능은 있으나 집계 단위/해제 시점과 설정 모델이 PB와 다르다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/middlewares_rate_limit.go#L32)

PgBase 근거: [internal/runtimesettings/settings.go:51](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/runtimesettings/settings.go:51) / [internal/adminauth/service.go:275](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/service.go:275)

테스트/검증 근거: [internal/adminauth/service_integration_test.go:262](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/adminauth/service_integration_test.go:262)

## OPS-07 · PB-style configurable global API rate rules

**보완 필요** · P1 · test-exists · 기존 항목

PocketBase: collectionName:action, *:action, 경로/접두사, audience 및 superuser 제외를 지원한다.

PgBase: DB quota, 경로/HTTP메서드/접두사·audience·IP제외 지원.

차이/남은 범위: Admit는 method+path, path, / 라벨만 조회하여 PB collection/action 태그가 적용되지 않는다. 관리자도 @auth quota 대상이고 PB superuser bypass와 다르다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/middlewares_rate_limit.go#L103)

PgBase 근거: [internal/apiratelimit/service.go:40](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/apiratelimit/service.go:40) / [internal/api/middleware.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/middleware.go:23)

테스트/검증 근거: [internal/api/api_rate_limit_integration_test.go:19](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/api_rate_limit_integration_test.go:19) / [pgbase/api_rate_limit_service_actor_integration_test.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/api_rate_limit_service_actor_integration_test.go:14)

## OPS-08 · trusted proxy/real client IP

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: 설정된 헤더 목록과 UseLeftmostIP로 실제 IP를 결정한다.

PgBase: 신뢰 CIDR에서 온 요청의 X-Forwarded-For만 오른쪽부터 검증한다.

차이/남은 범위: PB의 custom header/leftmost 옵션 없음. 안전한 CIDR 모델은 설계 차이지만 CF-Connecting-IP 등 기존 설정은 이식되지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/event_request.go#L40)

PgBase 근거: [internal/api/client_address.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/client_address.go:24) / [internal/runtimesettings/settings.go:37](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/runtimesettings/settings.go:37)

테스트/검증 근거: [internal/api/client_address_test.go](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/client_address_test.go)

## OPS-09 · built-in HTTPS/ACME serve

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: autocert, HTTP challenge/redirect, www 별칭을 제공한다.

PgBase: 정적 인증서 또는 명시적 호스트 allowlist ACME, 캐시, challenge/redirect와 managed restore 유지.

차이/남은 범위: 구성 플래그/도메인 자동 추론이 다르다. 실제 공개 CA 발급·갱신은 이번 감사에서 실행하지 않았다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/serve.go#L149)

PgBase 근거: [pgbase/tls.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/tls.go:17) / [pgbase/app_construct.go:423](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app_construct.go:423)

테스트/검증 근거: [pgbase/tls_integration_test.go:31](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/tls_integration_test.go:31) / [pgbase/acme_mock_integration_test.go:32](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/acme_mock_integration_test.go:32) / [pgbase/managed_tls_integration_test.go:86](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/managed_tls_integration_test.go:86)

## OPS-10 · backup CRUD/transfer queue

**대응 구현·설계 차이** · none · executed-now · 기존 항목

PocketBase: ZIP 백업을 생성/목록/업로드/다운로드/삭제/복원한다.

PgBase: tar.gz+manifest, 비동기 job receipt, SHA 검증, upload/download/delete/restore 구현.

차이/남은 범위: PB ZIP/파일이름·응답·라우트와 호환되지 않는다. 백업 교차 복원은 지원하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/backup.go#L17)

PgBase 근거: [internal/api/operations_console.go:173](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/operations_console.go:173) / [internal/backup/manager.go:269](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/manager.go:269)

테스트/검증 근거: [internal/backup/archive_test.go:95](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/archive_test.go:95) / [pgbase/managed_integration_test.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/managed_integration_test.go:24)

## OPS-11 · DB와 파일의 일관된 백업·복원

**대응 구현·설계 차이** · none · prior-run · 기존 항목

PocketBase: SQLite VACUUM INTO와 local pb_data 파일을 백업; 원격 S3 파일은 별도 관리한다.

PgBase: exported PG snapshot+GC gate에서 ready files/variants/extension_objects 포함, 새 DB/스토리지 대상 복원·managed activation.

차이/남은 범위: PG 백업 포맷과 복구 절차다. local/S3 source 삭제 후 확장 객체 바이트 복원을 이전 작업에서 확인. restore는 RunManaged 또는 Activate가 필요하다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/backup_create.go#L28)

PgBase 근거: [internal/backup/online.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/online.go:1) / [pgbase/backup_options.go:73](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/backup_options.go:73) / [internal/backup/extension_restore_integration_test.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/extension_restore_integration_test.go:1)

테스트/검증 근거: [internal/backup/online_integration_test.go](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/online_integration_test.go) / [internal/backup/extension_restore_integration_test.go](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/extension_restore_integration_test.go) / [pgbase/managed_integration_test.go](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/managed_integration_test.go)

## OPS-12 · backup S3 repository

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: 백업 저장소용 별도 S3 설정을 사용한다.

PgBase: 독립 S3 저장소, archive/metadata 검증 및 checksum 제공.

차이/남은 범위: PgBase의 archive/metadata 쌍으로 저장하며 PB ZIP repository와 호환되지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/backup_create.go#L109)

PgBase 근거: [internal/backup/s3_repository.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/s3_repository.go:1) / [pgbase/backup_options.go:107](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/backup_options.go:107)

테스트/검증 근거: [internal/backup/s3_repository_test.go:43](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/s3_repository_test.go:43) / [internal/backup/backup_integration_test.go](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/backup_integration_test.go)

## OPS-13 · durable scheduled jobs/cron

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: 메모리 cron 등록, 관리자 목록 및 즉시 실행 지원.

PgBase: DB durable jobs, timezone, 활성화/스케줄 수정, 실행 요청/claim 지원.

차이/남은 범위: in-process PB cron과 실행·중복·재시도 모델이 다르다. 동적 JS cron API 공백은 EXT 행에서 다룬다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/cron.go#L15)

PgBase 근거: [internal/jobs/service.go:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jobs/service.go:1) / [internal/api/operations_console.go:113](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/operations_console.go:113) / [pgbase/jobs_options.go:13](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/jobs_options.go:13)

테스트/검증 근거: [internal/jobs/management_integration_test.go](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/jobs/management_integration_test.go)

## OPS-15 · self-update

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: GitHub release 아카이브로 실행 파일을 업데이트한다.

PgBase: 서명된 manifest, artifact checksum, schema bounds, phase journal, probe/rollback, Windows helper 분기.

차이/남은 범위: PB release feed는 사용할 수 없다. updater 구현 존재와 실제 배포 feed/Windows 전체 교체 인수는 별개다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/plugins/ghupdate/ghupdate.go#L35)

PgBase 근거: [internal/selfupdate/install.go:61](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/selfupdate/install.go:61) / [internal/selfupdate/install.go:181](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/selfupdate/install.go:181)

테스트/검증 근거: [internal/selfupdate/selfupdate_test.go:162](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/selfupdate/selfupdate_test.go:162) / [internal/selfupdate/selfupdate_test.go:236](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/selfupdate/selfupdate_test.go:236)

## OPS-16 · panic 복구 후 request-log 기록

**좁은 계약 일치** · none · executed-now · 기존 항목

PocketBase: API middleware는 panic을 복구하여 서버 오류/로그로 처리한다.

PgBase: recovery 아래의 응답 상태를 request telemetry가 500으로 관측한다.

차이/남은 범위: 이 좁은 오류 처리 계약에 한해 일치. 전체 로그 wire 포맷 일치를 뜻하지 않는다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/middlewares.go#L253)

PgBase 근거: [internal/api/middleware.go:127](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/middleware.go:127) / [internal/api/router.go:305](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/router.go:305)

테스트/검증 근거: [internal/api/telemetry_test.go:250](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/telemetry_test.go:250) / [operations-tests-output.txt](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/operations-tests-output.txt)

## OPS-17 · persisted settings encryption key environment

**대응 구현·설계 차이** · none · executed-now · 기존 항목

PocketBase: 지정한 encryption env 키로 저장된 settings를 암호화/복호화한다.

PgBase: 환경변수명 선택/CLI override와 base64 키 검증, AES-GCM 설정 암호화·rotate 제공.

차이/남은 범위: 환경변수명/키 인코딩/암호문이 다르므로 기존 PB 암호화 settings를 읽는 기능은 아니다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/settings_query.go#L54)

PgBase 근거: [internal/config/env.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/config/env.go:18) / [internal/runtimesettings/settings.go:275](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/runtimesettings/settings.go:275) / [internal/runtimesettings/settings.go:315](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/runtimesettings/settings.go:315)

테스트/검증 근거: [internal/config/env_test.go:41](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/config/env_test.go:41) / [internal/config/env_test.go:57](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/config/env_test.go:57)

## OPS-18 · 내장 자동 백업 스케줄

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: backups.cron 변경을 bootstrap/settings reload에 반영한다.

PgBase: managed-backup durable job에 schedule/timezone/enabled를 반영한다.

차이/남은 범위: 명시적 enabled와 durable job 모델이다. 스케줄문법·저장형식은 PB와 동일하지 않다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/backup.go#L33)

PgBase 근거: [pgbase/backup_schedule.go:17](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/backup_schedule.go:17)

테스트/검증 근거: [internal/runtimesettings/settings_test.go](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/runtimesettings/settings_test.go) / [pgbase/managed_integration_test.go](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/managed_integration_test.go)

## OPS-19 · 백업 보관 개수 제한

**보완 필요** · P1 · executed-now · 기존 항목

PocketBase: CronMaxKeep은 @auto_pb_backup_ 접두사의 자동 백업만 정리한다.

PgBase: publish/upload 후 repository 전체를 Keep 개수로 정리한다.

차이/남은 범위: 수동·업로드 백업도 보관 한도에 포함돼 제거될 수 있다. PB 자동백업 보존 정책과 다른 데이터 보존 동작이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/backup.go#L69)

PgBase 근거: [internal/backup/manager.go:372](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/manager.go:372) / [internal/backup/manager.go:378](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/manager.go:378)

테스트/검증 근거: [internal/backup/archive_test.go:95](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/archive_test.go:95) / [operations-tests-output.txt](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/operations-tests-output.txt)

## OPS-20 · 자동 백업 실패 관리자 메일 알림

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: 자동 백업 실패를 전체 superuser에게 알린다.

PgBase: 최종 실패 managed job receipt 후 active admins에 SMTP 재시도/최소간격을 적용해 알린다.

차이/남은 범위: 시작 시 options.SMTP로 만든 발신자를 사용한다. SMTP설정 reload 일치는 별도 확인 필요; 실제 외부 메일 전달은 미실행.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/backup.go#L48)

PgBase 근거: [pgbase/backup_alert.go:29](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/backup_alert.go:29) / [pgbase/backup_alert.go:40](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/backup_alert.go:40) / [internal/backup/manager.go:269](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/manager.go:269)

테스트/검증 근거: [internal/backup/managed_failure_test.go](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/backup/managed_failure_test.go) / [pgbase/backup_alert_test.go:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/backup_alert_test.go:21) / [pgbase/tls_integration_test.go:64](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/tls_integration_test.go:64)

## OPS-21 · HTTP health 확인

**대응 구현·설계 차이** · none · executed-now · 기존 항목

PocketBase: /api/health가 code/message/data와 관리자 부가정보를 반환한다.

PgBase: /health/live,/health/ready, 관리 system health 제공; ready는 의존성 장애503.

차이/남은 범위: 라우트와 JSON 다름. PB SDK health.check를 그대로 연결할 수 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/health.go#L13)

PgBase 근거: [internal/api/health.go:14](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/health.go:14) / [internal/api/router.go:95](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/router.go:95)

테스트/검증 근거: [internal/api/health_test.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/health_test.go:16) / [internal/api/health_test.go:34](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/health_test.go:34)

## OPS-22 · 관리자 CLI create/update/upsert/delete

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: superuser 하위명령으로 관리자를 생성/수정/삭제한다.

PgBase: admin 명령과 stdin/terminal 비밀값으로 생성·upsert·암호변경·삭제 제공.

차이/남은 범위: PB _superusers 레코드가 아니라 별도 admins 저장소와 플래그 계약이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/cmd/superuser.go#L16)

PgBase 근거: [internal/cli/run.go:255](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/run.go:255)

테스트/검증 근거: [internal/cli/admin_integration_test.go:130](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/admin_integration_test.go:130)

## OPS-23 · 관리자 CLI OTP·IP 정책

**보완 필요** · P2 · test-exists · 기존 항목

PocketBase: superuser otp는 SMTP 없이 OTP를 만들고 출력; ips는 전역 allowlist 설정.

PgBase: admin otp email은 SMTP 발송 후 receipt를 출력, TOTP enable/disable도 지원. IP는 관리자별.

차이/남은 범위: 메일 서버가 없는 상태의 PB 긴급 CLI OTP 발급과 일치하지 않는다. 전역 IP 정책 API도 다르다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/cmd/superuser.go#L171)

PgBase 근거: [internal/cli/run.go:334](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/run.go:334)

테스트/검증 근거: [internal/cli/admin_integration_test.go:185](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/cli/admin_integration_test.go:185) / [pgbase/n2_n3_integration_test.go:72](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/n2_n3_integration_test.go:72)

## OPS-24 · CORS와 preflight

**대응 구현·설계 차이** · none · executed-now · 기존 항목

PocketBase: 기본 wildcard origin, HEAD 등 메서드 및 설정 가능한 headers/credentials.

PgBase: 명시적 origin allowlist, 제한된 헤더/메서드, 관리자 same-origin 권한 유지.

차이/남은 범위: wildcard는 의도적으로 거절; HEAD/임의 header preflight는 미지원. PB 기본 CORS 설정을 그대로 사용할 수 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/middlewares_cors.go#L1)

PgBase 근거: [internal/api/middleware.go:361](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/middleware.go:361)

테스트/검증 근거: [internal/api/middleware_test.go:201](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/middleware_test.go:201) / [internal/api/middleware_test.go:231](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/middleware_test.go:231)

## OPS-25 · 사용자 정적 사이트·SPA fallback

**대응 구현·설계 차이** · none · test-exists · 기존 항목

PocketBase: pb_public/커스텀 정적 filesystem 및 index fallback 제공.

PgBase: 명시적 public directory, SPA fallback, 예약 API/admin 경로 우선순위 보존.

차이/남은 범위: 기본 디렉터리 탐색과 경로 설정은 PgBase 계약이다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/base.go#L103)

PgBase 근거: [pgbase/app_construct.go:219](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/app_construct.go:219) / [internal/api/router.go:324](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/router.go:324)

테스트/검증 근거: [pgbase/n2_n3_integration_test.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/pgbase/n2_n3_integration_test.go:24)

## OPS-26 · 배포 패키징·다중 플랫폼 release 경로

**보완 필요** · P2 · static-only · 추가 확인 항목

PocketBase: 공식 release workflow와 GoReleaser로 다중 OS/아키텍처 실행파일 배포.

PgBase: CI backend/frontend/E2E/security/backup gate와 signed container workflow 존재.

차이/남은 범위: 현재 공개 image workflow는 linux/amd64다. 동일 소스의 Windows/macOS/arm64 공개 릴리스 및 업데이트 feed 인수 증거는 이번 조사에 없다.

공식 근거: [소스 1](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/.github/workflows/release.yaml#L7)

PgBase 근거: [.github/workflows/release-gate.yml:1](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/.github/workflows/release-gate.yml:1) / [.github/workflows/release-image.yml:75](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/.github/workflows/release-image.yml:75)

테스트/검증 근거: [scripts/support/validate-release-candidate.sh](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/scripts/support/validate-release-candidate.sh)
