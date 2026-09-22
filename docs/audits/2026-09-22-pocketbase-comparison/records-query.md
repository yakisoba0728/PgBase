# PocketBase v0.40.4 ↔ PgBase: records / query 감사

기준: PocketBase 공식 GitHub `5cec579da984436a258602a46a96302fbd31f77c`(v0.40.4); PgBase `6993744` + remaining-development worktree의 2026-09-22 미커밋 변경. 기존 35행을 재평가하고 hidden query operand 검증 누락 1행(QRY-22)을 추가했다. 과거 완료 표시는 증거로 사용하지 않았다.

36행 분류: 좁은 기능 동등 3, 계약을 바꾼 대응 구현 12, 부분 구현 20, 미구현 1. 이것은 제품 완성도 백분율이 아니다. `equivalent`도 해당 행의 기능 의미론만 가리키며 PocketBase SDK/HTTP drop-in 호환을 뜻하지 않는다.

## 우선 보완 사항

1. **P1: 숨김 필드 query 검증 누락(QRY-22).** non-admin `title = secret`에서 `secret.Hidden=true`인데 두 hidden guard가 모두 통과하고 SQL에 secret을 포함하는 것을 unit reproducer로 확인했다. 목록/건수 차이를 이용한 추론 위험이 있어 다른 호환 개선보다 우선 수정할 대상이다. 실제 DB/HTTP 누출 테스트를 수행한 것은 아니다.
2. **PocketBase HTTP adapter는 완성되지 않았다.** native CRUD/batch는 구현돼 있지만 PB 원래 URL, collection name-or-ID, PB batch의 최상위 결과 배열·요청 URL/query 문법·multipart naming을 그대로 받지 않는다. 특히 REC-09의 PB batch wire 자체는 미구현이다.
3. **기존 'filter 완료' 판정은 public query와 access-rule compiler를 혼동했다.** rule 쪽 지원이 public list filter에 연결되지 않은 사례(@request/@collection, strftime modifiers, array modifiers)가 있다. null/빈 문자열, LIKE escape, quote parsing, literal-literal 비교도 PB와 다르다.
4. **문자열/날짜 표현·응답 선택에 틈이 있다.** PB metadata는 alias 추가 수준이며 native 시간 키·RFC3339Nano가 남는다. fields=missing, 전체 list envelope projection, HTML excerpt, 빈 expand:{} 및 깊이7 expand 처리도 다르다.
5. **native 기능은 상당 부분 구현됐다.** 읽기 전용 view, 순서 보장 atomic batch, JSON CRUD, text ID, modifiers, 권한 filter/expand, offset page/count/skipTotal은 실제 구현과 회귀 테스트가 있다. 누락된 PB 호환 표면을 기능 자체의 부재로 해석하면 안 된다.

## 검증 범위

현재 감사에서 PgBase 선별 단위 테스트 9개를 재실행했다([기록](records-verification.txt)). 추가로 parser/compiler/guard/serializer의 현재 차이를 확인하는 **15개 관찰 subtest**를 실행했다([원본](query-observations_test.go.txt), [출력](query-observations-output.txt)). 여기서 PASS는 '현재 비호환이 재현됨'을 뜻하며 PB 호환 PASS가 아니다. 임시 test 파일은 실행 뒤 제거했고 production 코드는 수정하지 않았다.

PB Go1.27 런타임·공식 서버는 실행하지 않았다. PB 기대값은 pinned 공식 구현 및 upstream tests를 읽어 확인했다. DB/MinIO/HTTP 통합 테스트는 이 감사에서 재실행하지 않은 행에 `test-exists`로 표시했다. 이전 턴의 전체 단위·통합 통과가 최신 PB conformance를 입증하지는 않는다. JSON의 `verificationDetail`은 선택한 테스트의 실제 범위를 설명한다.

## 행별 판정

| ID | 기능 | 판정 | 남은 차이 / 범위 |
|---|---|---|---|
| REC-01 | 레코드 목록 조회 | 대응 구현·계약 차이 | compat=pocketbase와 page/perPage/skipTotal 중 하나를 함께 지정해야 PB envelope가 활성화된다. compat만으로는 cursor envelope를 유지한다. PB 원래 경로와 collection ID selector는 없다. |
| REC-02 | 단일 레코드 조회 | 대응 구현·계약 차이 | 과거 문서의 UUID-only 설명은 틀렸다. text ID는 구현되었으나 PB 원래 경로·collection ID selector·기본 PB metadata envelope는 그대로 호환되지 않는다. |
| REC-03 | 레코드 생성(JSON/multipart) | 부분 구현 | 일반 create는 application/json만 수용하고 multipart를 거절한다. PB 200과 달리 Pg는201+Location/ETag를 반환한다. PB CRUD alias가 없다. |
| REC-04 | 레코드 부분 갱신과 빈 PATCH | 부분 구현 | body가 전혀 없으면 strict JSON decode EOF로400, JSON null은 input==nil로400. 기존 REC-04 완료 표시는 HTTP nil-body 계약까지 증명하지 못했다. 직접 multipart PATCH도 미지원. |
| REC-05 | 레코드 삭제 | 대응 구현·계약 차이 | 핵심 삭제 능력은 대응하지만 권한별 URL·ID 정책·에러 envelope가 다르다. 조건부 삭제는 Pg의 추가 기능. |
| REC-06 | 뷰 컬렉션 레코드 읽기 전용 | 동등(좁은 기능) | 이 행의 좁은 사용자 능력은 대응한다. 응답 오류 코드·경로까지 동일하다는 뜻은 아니다. |
| REC-07 | 레코드 JSON metadata·가시성 | 부분 구현 | compat projection은 native keys를 보존한 shallow copy이며 RFC3339Nano 값을 단순 alias한다. PB 기본 JSON과 date 문자열이 완전히 같지 않다. create/update는 PocketBaseCompatibility flag를 서비스에 전달하지 않는다. 숨긴 입력도 PB는 비관리자 입력에서 제거하지만 Pg는 hidden_field로거절한다. |
| REC-08 | 순서 보장 원자적 batch | 동등(좁은 기능) | 원자성의 핵심 능력은 대응. PB hook middleware recursion 구조와 wire 계약 동일성은 별도 행에서 다룬다. |
| REC-09 | PocketBase batch 요청·응답 wire | 미구현 | PB batch wire adapter 자체가 없다. Pg URL regexp는 /api/(v1/admin)/collections/{name}/records 경로만, per-operation ?fields/?expand도거절. 원래 PB SDK batch 요청을 그대로 처리할 수 없다. |
| REC-10 | PUT upsert와 조건부 갱신 | 대응 구현·계약 차이 | Native upsert는 구현되었지만 PB의 body.id/ID 생략 생성 wire와 다르다. Pg는 URL id필수이고 optional If-Match는 추가 계약이다. |
| REC-11 | multipart batch와 파일 참조 | 대응 구현·계약 차이 | 파일 포함 batch 기능은 있지만 multipart part 이름/참조 방식과 결과 envelope가 달라 PB multipart batch는 직접 미호환. |
| REC-12 | batch enable·한도·timeout | 대응 구현·계약 차이 | 능력은 대응하지만 설정명·단위·기본값·상한이 다르다. Disabled의 PB403 대비 Pg503 차이도 있다. |
| REC-13 | 숫자 증가·감소 modifier | 대응 구현·계약 차이 | 더 엄격하고 정밀한 native 숫자 정책이다. PB의 float/coercion 허용과 Pg typed/string 입력은 동일하지 않다. 동시 equal-length modifier key 순서도 PB는 길이만 정렬, Pg는 길이+lexical이다. |
| REC-14 | select·relation·file prepend/append/remove modifier | 대응 구현·계약 차이 | 개별 연산 능력은 구현. PB 동일길이 modifier tie순서와 완전히 동등하다고 주장할 수 없고, 파일 modifier HTTP 입구는 PB direct multipart가 아닌 native batch이다. 각 modifier 입력의 PB coercion과 Pg string-only validation도 차이가 있다. |
| QRY-01 | 불리언 문법과 괄호 | 대응 구현·계약 차이 | 문법의 핵심 능력은 대응한다. PB보다 큰 byte 한도/더 작은 node 한도라 허용하는 긴 식의 집합은 다르다. |
| QRY-02 | 스칼라 비교 연산자와 null 의미론 | 부분 구현 | title=null에 빈 문자열이 매칭되지 않고 nullable field != nonempty에서 NULL이 제외된다. PB의 text null/empty 및 NULL-safe inequality와 불일치. |
| QRY-03 | contains / not-contains | 부분 구현 | title ~ 'a\%b'를 PB처럼 부분 문자열 패턴으로 감싸지 않는다. public SQL LIKE와 별도 in-memory scalarContains 구현도 다르다. SQLite/PG LIKE의 기본 case 처리 역시 동등성 검증이 필요하다. |
| QRY-04 | any-match 비교 연산자 | 부분 구현 | 기존 테스트는 title/large_count 같은 scalar의 토큰 컴파일 중심이다. PB select/file/JSON multi value와 일반 relation comparison의 all-match 대 any-match 의미론까지 완성됐다고 할 수 없다. |
| QRY-05 | 피연산자 배치·field-to-field 비교 | 부분 구현 | PB 테스트가 허용하는 2 < 5도 거절한다. root field=relation.field 조합 및 function-on-right 등 피연산자 조합을 PB oracle로 확대 검증해야 한다. |
| QRY-06 | 문자열·숫자·불리언·null literal | 부분 구현 | title = 'a "b" c'와 backslash apostrophe 문자열을 거절한다. double quote 정규화가 single-quoted literal 내부까지 바꾸는 문제가 있다. 날짜는 RFC3339 timezone을 요구해 PB 날짜 문자열/coercion과도 다르다. |
| QRY-07 | 정방향 relation filter | 부분 구현 | PB가 허용하는 4~6 hop은 거절한다. 일반 relation field 비교가 기본 EXISTS로 컴파일되어 PB multi-match 제약과 세부 결과 차이가 남는다. |
| QRY-08 | JSON 객체 속성·배열 index filter | 부분 구현 | 잘 형성된 동종 scalar path는 지원하지만 mixed JSON 값에 무조건 ::numeric/::boolean cast하므로 PB JSON 비교와 다르다. path-to-path 비교·heterogeneous 값/배열에 대한 public HTTP differential 검증이 없다. |
| QRY-09 | 역방향 relation filter | 부분 구현 | 역방향 filter 자체는 구현됐다. PB collection ID 기반 _via_ selector, 4~6 hop 및 전체 multi-match 조합은 제공되지 않는다. |
| QRY-10 | lower·length·each·isset·changed modifier | 부분 구현 | rule compiler가 풍부해도 public filter 경로와 같지 않다. select/file JSON 배열 length/each는 public list에서 지원되지 않는다. 두 compiler의 modifier/case/coercion 일치 검증 필요. |
| QRY-11 | geoDistance·strftime 함수 | 부분 구현 | strftime('…',date,'start of year') public filter 거절. %w를 D로 바꾸어 weekday 기준이 0→1 달라진다. rule %s도 epoch 아닌 SSSS로 치환한다. 모든 SQLite substitutions/time-value/modifiers 및 relation args는 미완성. |
| QRY-12 | 시간 macro와 @request/@collection 식별자 | 부분 구현 | 시간 macro 부분과 rule 특수값은 구현됐다. PB superuser가 쓸 수 있는 public list @request/@collection filter까지는 연결되지 않았다. PB는 일반 사용자에게도 허용한다는 뜻이 아님. |
| QRY-13 | 다중 스칼라 sort·안정 tie-break | 부분 구현 | PB가 허용하는 unindexed text field sort를 unindexed_field로 거절한다. 입력 whitespace/중복 처리와 기본 ordering도 다르다. PG 성능 정책을 유지할지 compat에서 완화할지 결정 필요. |
| QRY-14 | relation sort | 부분 구현 | 핵심 relation sort는 있으나 PB가 허용하는 3~6 hop, unindexed terminal, 일부 JSON/다중값 sort는 제한된다. |
| QRY-15 | @random·@rowid sort | 대응 구현·계약 차이 | 의도적인 엔진 차이다. @rowid가 SQLite 정수 삽입 순서와 같다는 보장은 없다. text public ID lexical 순서도 아니다. |
| QRY-16 | page 번호 기반 임의 페이지 이동 | 대응 구현·계약 차이 | 페이지 기능은 구현. /api/v1/...에서 compat=pocketbase와 page/perPage/skipTotal 중 하나를 지정해야 활성화되며 PB 기본 route/drop-in은 아니다. |
| QRY-17 | totalItems·totalPages·skipTotal | 동등(좁은 기능) | 활성화된 PB page adapter의 이 좁은 계약은 대응한다. PB 전체 route·serializer 동일성을 의미하지 않으며 latest upstream을 상대로 이번 감사에서 DB differential 재실행은 하지 않았다. |
| QRY-18 | fields 응답 projection | 부분 구현 | fields=missing은 PB의 {} 대신 오류. list envelope의 items.id/page 선택과 record-field 선택을 동일하게 지원하지 않는다. inline HTML 'Hello<b>world</b>'가 'Hello world'로 바뀌어 PB DOM text 처리와 다르다. |
| QRY-19 | 정방향·중첩 expand | 대응 구현·계약 차이 | 주요 기능은 구현됐고 PB보다 추가적인 자원 한도가 있다. budget 초과/empty expand 응답 차이는 QRY-21에서 따로 분류한다. |
| QRY-20 | 역방향 _via_ expand | 부분 구현 | 현행 name_via_field 핵심은 구현됐다. collection ID selector/아직 PB가 받는 legacy syntax는 없고, PB root당 limit와 Pg job-wide 예산·에러 처리도 다르다. PB unique back-relation은 단일 object지만 Pg는 항상 배열이다. |
| QRY-21 | expand JSON·invalid path 처리 | 부분 구현 | PB는 7단계 요청을6단계까지 처리하지만 Pg는 expand_budget_exceeded로 전체 오류. 정상 empty 관계를 요청한 경우 PB expand:{}와 Pg key omission이 다르다. |
| QRY-22 | 숨김 필드의 모든 query 피연산자 검증 (추가 감사 항목) | 부분 구현 | 실제 unit repro에서 non-admin title = secret(Hidden:true)이 두 guard를 모두 통과하고 SQL에 secret을 포함했다. 응답 자체에서 hidden을 제거해도 필터 결과/건수로 추론될 여지가 있어 우선 수정 필요. DB/HTTP 공격은 실행하지 않았으며 원격 누출 확정으로 과장하지 않는다. |

각 행의 고정 GitHub URL, PgBase 구현/test 파일과 줄 번호는 [records-query.json](records-query.json)에 있다.

## 직접 읽은 소스·테스트

PocketBase: `apis/record_crud.go`, `apis/record_crud_test.go`, `apis/batch.go`, `apis/record_helpers.go`, `core/record_model.go`, `core/field_number.go`, `core/field_select.go`, `core/record_field_resolver.go`, `core/record_field_resolver_runner.go`, `core/record_query_expand.go`, `tools/search/{filter,provider,sort,token_functions,identifier_macros,multi_match_subquery}.go`, `tools/search/filter_test.go`, `tools/picker/{pick,excerpt_modifier}.go`, `tools/picker/pick_test.go`, `tools/router/event.go`, `tools/types/datetime.go`.

PgBase: `internal/api/{router,record_handlers,admin_handlers,batch_handlers,batch_file_modifiers}.go`; `internal/recordops/{service,read,batch,modifiers,field_visibility}.go`; `internal/runtimesettings/settings.go`; `internal/query/{filter,compiler,predicate,service,scalar_predicate,relation_filter_sort,access_rule,field_visibility,expand,expand_types,expand_plan,expand_reverse,projection,record_identity_sql,codec}.go`; `internal/accessrule/{pocketbase_resolver,pocketbase}.go`; `internal/responseprojection/{projection,record}.go`.

읽은 대응 테스트: `internal/api/record_handlers_test.go`, `internal/recordops/{modifiers_test,modifiers_integration_test,batch_integration_test,text_identity_integration_test,field_visibility_test}.go`, `internal/query/{compiler_test,pocketbase_page_test,pocketbase_page_integration_test,pocketbase_compat_live_test,pocketbase_filter_completion_test,pocketbase_rule_conformance_test,relation_filter_sort_test,relation_filter_sort_live_test,expand_test,expand_nested_live_test,projection_test,field_visibility_test,service_surface_test,access_rule_test,text_identity_integration_test}.go`, `internal/responseprojection/record_test.go`, `pgbase/{batch_integration_test,sql_view_integration_test,runtime_settings_http_integration_test,field_visibility_integration_test}.go`. 링크된 일부 통합 테스트는 해당 행의 구현/핵심 assertion 중심으로 확인했으며 파일 전체 실행과 구분한다.
