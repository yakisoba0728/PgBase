# PocketBase schema/fields 대조 — 2026-09-22

대상은 공식 PocketBase **v0.40.4 / `5cec579da984436a258602a46a96302fbd31f77c`**와 현재 PgBase `remaining-development` 작업 트리다. 이전 턴의 미커밋 custom ID·auth policy·builder UI 수정도 포함했다. 2026-09-11 표는 항목 목록으로만 사용했고, 기존 완료 판정을 근거로 사용하지 않았다. 이 조사에서는 프로덕션 코드를 수정하지 않았다.

원래 범위 **40개(SCH-01..24, FLD-01..16)**를 모두 다시 판정했다. 원래 범위 결과는 **equivalent 2 / adapted 11 / partial 27 / missing 0 / unverified 0**이다. 별도 추가한 **4개(SCH-25..27, FLD-17)**는 partial 1 / missing 3이다. 합계 44개로 기존 232개 분모와 추가 항목을 섞어 완료율을 계산하면 안 된다.

`equivalent`는 행에서 좁게 정의한 PB 동작과 assertion이 대응하는 경우, `adapted`는 사용자 기능이 존재하되 의도된 PostgreSQL/native API 계약인 경우다. `partial`은 핵심 기능이 있어도 관찰 가능한 PB 동작이 빠진 경우다. `missing`은 해당 좁은 작업 자체가 없는 경우다. adapted는 drop-in PB wire 호환을 뜻하지 않으며 일부 P2는 PB 호환을 약속할 때의 우선순위다. 상세 계약·복수 소스·테스트·검증 수준은 [schema-fields.json](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/docs/audits/2026-09-22-pocketbase-comparison/schema-fields.json)에 있다.

## 우선 확인할 차이

- **P1 / FLD-12 — multi-relation cascade:** source가 [A,B]를 참조할 때 A만 삭제하면 PB는 B 연결을 남긴다. PgBase cascade graph는 A를 참조한 source 전체를 삭제 closure에 넣는다. PB는 남은 ID가 0개일 때만 cascade하며, PG 분기는 남은 target 수를 확인하지 않는다. 이는 소스 분기 대조로 확인한 데이터 삭제 의미 차이이며 이 조사에서 새 DB 차등 재현 테스트를 실행하지는 않았다. [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_model.go#L1613-L1643), [internal/relation/delete.go:339](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/relation/delete.go:339).
- **P1 / SCH-24 — deleteMissing 필드 삭제:** PB는 누락 non-system fields도 삭제한다. PgBase `importedFields`는 deleteMissing 여부와 관계없이 누락 기존 필드를 다시 붙인다. auth realm/system collection 누락은 자동 보존/삭제 대신 요청 차단이다. [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_import.go#L34), [internal/schema/schema_document.go:498](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:498), [internal/schema/schema_delete_missing.go:53](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_delete_missing.go:53).
- **P1 / SCH-02,22 — auth 설정 이동 범위:** 새 text recordId authority/export 경로는 구현되어 실제 브라우저에서 검증했다. 그러나 portable auth DTO에는 OAuth2 enabled/mappedFields, authAlert, 메일 template, OTP length 등이 없어 PB 전체 collection 옵션을 export/import하지는 않는다. 다른 settings API의 존재와 schema 이동 능력을 혼동하지 않았다. [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L99), [internal/schema/auth_schema_document.go:21](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/auth_schema_document.go:21).
- **P1 / SCH-19 — SQL view ID:** builder view는 source text policy를 상속하지만 SQL view는 id의 SQL 타입이 uuid여야 한다. PB가 허용하는 text/ROW_NUMBER id view와 다르다. [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/view.go#L117), [internal/schema/sql_view_inspect.go:151](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/sql_view_inspect.go:151).
- **P1/P2 / SCH-12 및 FLD 행 — coercion/defaults:** PB zero default, optional empty 및 입력 coercion을 native strict codec이 모두 재현하지 않는다. PB validationMode의 required zero 검사 구현만으로 전체 field wire 호환을 선언할 수 없다. [PB bool](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_bool.go#L99), [internal/query/codec.go:145](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:145).
- **P2 / SCH-25,26,27, FLD-17 — 이전 분모에서 빠진 작업:** 기존 두 field 이름의 atomic swap, builder view 정의 변경의 schema import, view relation/file semantics, text `:autogenerate` modifier를 추가 행으로 기록했다. 직접 builder 편집이 통과했다는 증거는 schema import 편집 지원의 증거가 아니다. [PB swap](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync.go#L97), [internal/schema/planner.go:288](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:288), [internal/schema/schema_document.go:345](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:345).
- **이전 설명 수정:** PB는 date를 space-separated millisecond UTC layout으로 내보내며 현재 PG는 RFC3339Nano다. PB GeoPoint는 nil/empty/partial 값을 zero-fill하므로 PG의 exact-two-key 검사와 같지 않다. [PB date](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/types/datetime.go#L96-L110), [PB GeoPoint test](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_geo_point_test.go#L31-L50), [internal/query/codec.go:463](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:463).

## 항목별 판정

| ID | 상태 | 좁은 판정 근거 | 소스 시작점 |
| --- | --- | --- | --- |
| SCH-01 | adapted | native preview/apply CRUD 구현 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L18); [internal/api/schema_handlers.go:209](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/schema_handlers.go:209) |
| SCH-02 | partial | auth aggregate/schema 문서에서 일부 옵션 누락 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L99); [internal/userauth/collections.go:23](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/userauth/collections.go:23) |
| SCH-03 | partial | updated/skipTotal·page size 계약 차이 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L34); [internal/api/schema_handlers.go:421](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/schema_handlers.go:421) |
| SCH-04 | partial | system 규칙 변경 보호 차이 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L100); [internal/schema/planner.go:18](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:18) |
| SCH-05 | partial | rename+DDL 변경 동시 적용 거절 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync.go#L50); [internal/schema/collection_rename.go:48](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/collection_rename.go:48) |
| SCH-06 | equivalent | incoming relation/view 삭제 보호 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model.go#L687); [internal/schema/planner.go:465](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:465) |
| SCH-07 | partial | system truncate 거절·전체 ID snapshot | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L183); [internal/recordops/truncate.go:27](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/truncate.go:27) |
| SCH-08 | adapted | 세 collection 타입 native scaffold | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L201); [internal/schema/scaffolds.go:22](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/scaffolds.go:22) |
| SCH-09 | adapted | 5개 규칙 저장/변환 기능 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model.go#L358); [internal/api/rules.go:20](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/api/rules.go:20) |
| SCH-10 | adapted | field metadata와 system field 보호 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field.go#L69); [internal/schema/types.go:33](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/types.go:33) |
| SCH-11 | equivalent | PB mode의 required zero/blank 검사 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_bool.go#L115); [internal/query/codec.go:145](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:145) |
| SCH-12 | partial | 일반 optional scalar implicit zero default 없음 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_bool.go#L99); [internal/schema/planner.go:606](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:606) |
| SCH-13 | adapted | 일반 scalar index/unique lifecycle | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L525); [internal/schema/planner.go:673](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:673) |
| SCH-14 | partial | 복합 key 최대 8개 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L538); [internal/schema/planner_indexes.go:41](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_indexes.go:41) |
| SCH-15 | partial | 함수/collation/type index 범위 제한 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L538); [internal/schema/planner_indexes.go:41](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner_indexes.go:41) |
| SCH-16 | adapted | rename·옛 이름 재사용·drop 값 보존 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync.go#L94); [internal/schema/planner.go:281](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:281) |
| SCH-17 | adapted | rename된 multi-relation 저장소 삭제 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync.go#L111); [internal/schema/planner.go:490](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:490) |
| SCH-18 | partial | required scalar 추가에 explicit default 필수 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go#L220); [internal/schema/planner.go:348](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:348) |
| SCH-19 | partial | SQL view ID가 UUID로 제한 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/view.go#L82); [internal/schema/view.go:16](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view.go:16) |
| SCH-20 | adapted | 제한된 SQL view sample 제공 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L262); [internal/schema/view_sample.go:9](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view_sample.go:9) |
| SCH-21 | adapted | SQL/builder view 직접 수정 구현 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L118); [internal/schema/view.go:101](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view.go:101) |
| SCH-22 | partial | 일부 auth 설정을 schema로 이동할 수 없음 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/ui/src/settings/sync/pageExportCollections.js#L24); [internal/schema/schema_document.go:19](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:19) |
| SCH-23 | partial | name merge로 collection identity rename 불가 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_import.go#L31); [internal/schema/schema_document.go:156](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:156) |
| SCH-24 | partial | deleteMissing이 누락 field를 삭제하지 않음 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_import.go#L34); [internal/schema/schema_delete_missing.go:10](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_delete_missing.go:10) |
| FLD-01 | partial | bool coercion/default 계약 차이 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_bool.go#L99-L119); [internal/query/codec.go:145](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:145) |
| FLD-02 | partial | number wire/OnlyInt 계약 차이 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_number.go#L129-L169); [internal/query/codec.go:175](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:175) |
| FLD-03 | partial | optional empty·기본길이·empty autogenerate 차이 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_text.go#L173-L175); [internal/fieldvalue/options.go:100](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options.go:100) |
| FLD-04 | adapted | exact-case text alias ID라는 native 변경 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_text.go#L161-L175); [internal/schema/record_id.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/record_id.go:12) |
| FLD-05 | partial | email exact domain/optional empty 차이 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_email.go#L117-L170); [internal/fieldvalue/options.go:56](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options.go:56) |
| FLD-06 | partial | URL Host·port·subdomain/empty 차이 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_url.go#L117-L172); [internal/fieldvalue/options.go:128](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/fieldvalue/options.go:128) |
| FLD-07 | partial | PB 날짜 wire layout과 RFC3339Nano 차이 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_date.go#L113-L146); [internal/query/codec.go:252](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:252) |
| FLD-08 | partial | select normalization·scalar/multi 전환 제한 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_select.go#L136-L207); [internal/query/codec.go:219](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:219) |
| FLD-09 | partial | JSON 기본 maxSize/coercion 차이 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_json.go#L25-L25); [internal/query/codec.go:288](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:288) |
| FLD-10 | partial | EXIF·thumbnail format/fallback 차이 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_file.go#L226-L307); [internal/schema/file_options.go:12](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/file_options.go:12) |
| FLD-11 | adapted | 보호 기본값·token/session 정책의 native 변경 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_file.go#L128-L135); [internal/schema/types.go:78](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/types.go:78) |
| FLD-12 | partial | multi-relation cascade가 너무 넓음 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_relation.go#L169-L214); [internal/schema/relation_types.go:130](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/relation_types.go:130) |
| FLD-13 | partial | bcrypt Cost/raw-hash 호환 없음 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_password.go#L89-L94); [internal/schema/types.go:51](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/types.go:51) |
| FLD-14 | partial | 사용자 autodate 값이 no-op 대신 오류 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_autodate.go#L123-L150); [internal/recordops/generated_fields.go:51](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/generated_fields.go:51) |
| FLD-15 | partial | editor coercion/size budget 차이 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_editor.go#L115-L166); [internal/query/codec.go:219](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:219) |
| FLD-16 | partial | GeoPoint empty/partial normalization 차이 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_geo_point.go#L109-L143); [internal/query/codec.go:463](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/query/codec.go:463) |

추가한 항목은 아래와 같다.

| ID | 상태 | 좁은 판정 근거 | 소스 시작점 |
| --- | --- | --- | --- |
| SCH-25 | missing | 두 기존 field 이름의 atomic swap 없음 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_record_table_sync.go#L97); [internal/schema/planner.go:281](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/planner.go:281) |
| SCH-26 | partial | builder view 변경을 import로 적용하지 못함 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_import.go#L63); [internal/schema/schema_document.go:339](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/schema_document.go:339) |
| SCH-27 | missing | view relation/file 의미 보존 없음 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/view.go#L202); [internal/schema/view.go:38](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/schema/view.go:38) |
| FLD-17 | missing | 명시 text :autogenerate modifier 없음 | [PB](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_text.go#L376-L393); [internal/recordops/modifiers.go:24](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/internal/recordops/modifiers.go:24) |

## 검증 기록

현재 조사에서 아래 기존 단위 테스트를 실행했고 모두 통과했다. `executed-now`는 관련 native 단위 테스트가 실행되었다는 뜻이며 PB와 PG의 전체 계약이 차등 실행으로 통과했다는 뜻이 아니다. 실제 DB integration/PB suites는 소스와 assertion을 읽었으며 이번 조사에서 다시 실행하지 않았다. `static-only`는 구현 경로로 확인했지만 해당 누락 사례의 새 실행 검증은 하지 않았다는 의미다.

```powershell
go test ./internal/schema -run 'Test(Plan|AdvancedIndex|CompoundIndex|CollectionScaffolds|AuthCollectionScaffold|NormalizeRelation|RecordID|FieldPresentation)' -count=1
go test ./internal/fieldvalue ./internal/query ./internal/schema ./internal/recordidentity -run 'Test(TextNumericAndSelectionContracts|DomainNormalizationAndDenyPrecedence|RandomStringByPatternIsBoundedAndMatching|DecodeInputAcceptsEveryFieldTypeWithoutPrecisionLoss|DecodeInputAcceptsNullForOptionalNonJSONBFields|PocketBaseModeRejectsRequiredZeroAndBlankValues|TimestampBoundsAndJSONMaximumSize|DecodeInputRejectsUnknownFieldsAndTypeMismatches|DecodeInputEnforcesTextAndMultiSelectOptions|GeoPointCodecBoundaries|EditorCodecUsesByteLimitAndNonemptyRequired|RecordIDPolicyDefaultsAndExactValidation|RecordIDPolicyRejectsUnsafeOrUnboundedDefinitions)' -count=1
go test ./internal/relation ./internal/recordops ./internal/filestore ./internal/schema -run 'TestDecodeStableValidationCodes|TestNormalizeRelationFieldRejectsInvalidCombinations|TestPreparePasswordValidatesAndHashes|TestPreparePasswordOptionalClearAndDefaultMaximum|TestFileFieldOptions|TestRenderVariantDimensionsAndVerticalCrop|TestPolicyCallbackMustExplicitlyOptIntoPublicFiles' -count=1
```

`internal/recordidentity`에는 untagged test가 없어 해당 실행은 integration 검증이 아니다. 이전 턴의 실제 Chromium+실제 API 회귀는 [ui/admin/e2e/authenticated/remaining-development.spec.ts:4](C:/Users/yakihyuk0728/.codex/worktrees/remaining-development/PgBase/ui/admin/e2e/authenticated/remaining-development.spec.ts:4)의 exact-case ID/`new`/relation hydration, :104의 builder projection/filter preview/apply, :152의 auth scaffold→authority policy→uppercase auth record→양쪽 export를 통과했다. 이 증거를 해당 좁은 경로에만 `prior-run`으로 사용했다.

System-rule 변경 보호(SCH-04)는 `RuleService.Set`와 `ValidateRules` 전체 경로에서 system guard가 없는 것을 확인한 정적 차이다. 새 HTTP 재현이나 보안 취약점 주장은 하지 않았다. view SQL/field limits, index AST allowlist 등도 의도된 제한을 명시했으며 일반 기능 전체를 없는 것으로 취급하지 않았다.

## 읽은 범위

아래는 판정에 사용하여 직접 읽은 구현/테스트 및 주요 assertion의 파일 목록이다. 이 목록은 해당 파일 전체를 실행 검증했다는 뜻이 아니다. records/query 및 auth-runtime 전용 동작은 다른 audit 범위이며, 이 문서에서는 field contract/schema 보존과 필요한 실행 경계만 확인했다.

PocketBase:
- `apis/collection.go`
- `apis/file.go`
- `core/collection_import.go`
- `core/collection_model.go`
- `core/collection_model_auth_options.go`
- `core/collection_query.go`
- `core/collection_record_table_sync.go`
- `core/collection_record_table_sync_test.go`
- `core/collection_validate.go`
- `core/field.go`
- `core/field_autodate.go`
- `core/field_bool.go`
- `core/field_bool_test.go`
- `core/field_date.go`
- `core/field_editor.go`
- `core/field_email.go`
- `core/field_file.go`
- `core/field_geo_point.go`
- `core/field_geo_point_test.go`
- `core/field_json.go`
- `core/field_number.go`
- `core/field_number_test.go`
- `core/field_password.go`
- `core/field_relation.go`
- `core/field_select.go`
- `core/field_text.go`
- `core/field_text_test.go`
- `core/field_url.go`
- `core/record_model.go`
- `core/view.go`
- `tools/filesystem/filesystem.go`
- `tools/search/provider.go`
- `tools/types/datetime.go`
- `tools/types/geo_point.go`
- `ui/src/settings/sync/pageExportCollections.js`

PgBase schema/API/UI evidence:
- `internal/accessrule/pocketbase.go`
- `internal/api/rules.go`
- `internal/api/schema_handlers.go`
- `internal/api/schema_handlers_test.go`
- `internal/api/schema_operations_live_test.go`
- `internal/query/codec.go`
- `internal/query/codec_test.go`
- `internal/recordaccess/pocketbase_rule_integration_test.go`
- `internal/recordaccess/rules.go`
- `internal/recordaccess/rules_validation_test.go`
- `internal/recordops/truncate.go`
- `internal/recordops/truncate_concurrency_integration_test.go`
- `internal/recordops/truncate_integration_test.go`
- `internal/schema/advanced_fields_integration_test.go`
- `internal/schema/advanced_index_integration_test.go`
- `internal/schema/advanced_index_test.go`
- `internal/schema/auth_collection_document_integration_test.go`
- `internal/schema/auth_record_id_document_integration_test.go`
- `internal/schema/auth_schema_document.go`
- `internal/schema/collection_reference_rename.go`
- `internal/schema/collection_rename.go`
- `internal/schema/collection_rename_integration_test.go`
- `internal/schema/compatibility_integration_test.go`
- `internal/schema/compound_index_integration_test.go`
- `internal/schema/compound_index_test.go`
- `internal/schema/defaults.go`
- `internal/schema/defaults_test.go`
- `internal/schema/delete_missing_integration_test.go`
- `internal/schema/field_contract_integration_test.go`
- `internal/schema/field_options.go`
- `internal/schema/field_preflight.go`
- `internal/schema/field_presentation_test.go`
- `internal/schema/names.go`
- `internal/schema/operation_apply.go`
- `internal/schema/planner.go`
- `internal/schema/planner_indexes.go`
- `internal/schema/planner_test.go`
- `internal/schema/relation_planner_test.go`
- `internal/schema/relation_types.go`
- `internal/schema/scaffolds.go`
- `internal/schema/scaffolds_integration_test.go`
- `internal/schema/scaffolds_test.go`
- `internal/schema/schema_delete_missing.go`
- `internal/schema/schema_document.go`
- `internal/schema/schema_document_integration_test.go`
- `internal/schema/schema_document_ledger_integration_test.go`
- `internal/schema/service.go`
- `internal/schema/service_integration_test.go`
- `internal/schema/sql_view_inspect.go`
- `internal/schema/sql_view_inspect_integration_test.go`
- `internal/schema/sql_view_service_integration_test.go`
- `internal/schema/type_changes.go`
- `internal/schema/types.go`
- `internal/schema/view.go`
- `internal/schema/view_integration_test.go`
- `internal/schema/view_sample.go`
- `internal/userauth/collections.go`
- `ui/admin/e2e/authenticated/remaining-development.spec.ts`
- `ui/admin/src/designer/collection-designer-state.ts`

PgBase fields/runtime evidence:
- `internal/api/file_handlers.go`
- `internal/fieldvalue/options.go`
- `internal/fieldvalue/options_test.go`
- `internal/fieldvalue/random_pattern.go`
- `internal/filestore/image.go`
- `internal/filestore/local_test.go`
- `internal/filestore/policy_test.go`
- `internal/filestore/service.go`
- `internal/filestore/variants.go`
- `internal/filestore/variants_test.go`
- `internal/jsonvalue/json.go`
- `internal/passwordfield/password.go`
- `internal/query/advanced_fields_integration_test.go`
- `internal/query/advanced_fields_test.go`
- `internal/query/codec.go`
- `internal/query/codec_test.go`
- `internal/query/field_options_test.go`
- `internal/recordidentity/service.go`
- `internal/recordidentity/service_integration_test.go`
- `internal/recordops/generated_fields.go`
- `internal/recordops/generated_fields_integration_test.go`
- `internal/recordops/generated_fields_test.go`
- `internal/recordops/modifiers.go`
- `internal/recordops/modifiers_test.go`
- `internal/recordops/text_identity_integration_test.go`
- `internal/relation/codec.go`
- `internal/relation/codec_test.go`
- `internal/relation/delete.go`
- `internal/relation/delete_live_test.go`
- `internal/schema/advanced_fields_integration_test.go`
- `internal/schema/advanced_fields_test.go`
- `internal/schema/compatibility_integration_test.go`
- `internal/schema/field_contract_integration_test.go`
- `internal/schema/field_options.go`
- `internal/schema/field_preflight.go`
- `internal/schema/file_options.go`
- `internal/schema/file_options_test.go`
- `internal/schema/planner.go`
- `internal/schema/record_id.go`
- `internal/schema/record_id_test.go`
- `internal/schema/record_size.go`
- `internal/schema/relation_types.go`
- `internal/schema/relation_types_test.go`
- `internal/schema/type_changes.go`
- `internal/schema/types.go`
- `internal/userauth/record_identity.go`
- `internal/userauth/text_record_identity_integration_test.go`
- `pgbase/file_integration_test.go`
- `pgbase/file_services.go`
- `ui/admin/e2e/authenticated/remaining-development.spec.ts`
