# Field contracts — complete source reading

PocketBase v0.40.3 (`5684ee24f1e88f71a2ede73d20cf547fbd509e16`)의 `core/field*.go` 32개(구현과 테스트)를 전체 본문 판독했다. 파일별 근거는 [core-fields-ledger.json](core-fields-ledger.json)에 있다. 비교 대상은 PgBase `35af64c`; 아래는 소스에서 확인한 계약 차이이며 런타임 재현 결과나 결함 판정이 아니다.

## 타입 이름보다 중요한 차이

| 영역 | PocketBase의 실제 계약 | PgBase의 실제 계약 | 판정 |
| --- | --- | --- | --- |
| 기본 타입 | bool, number, text, date, autodate, email, URL, editor, geoPoint, JSON, password, select, relation, file의 14종. 각 타입이 정규화·값 검증·설정 검증·선택적 setter/getter/저장 인터셉터를 제공한다. | boolean, integer, bigint, numeric, text, timestamptz, UUID, JSONB, relation, file의 10종. DB 타입과 명시적 입력 codec 중심이다. | 일부 대응. 타입 개수 비율을 제품 진행률로 사용하면 안 된다. |
| 필수 값 | bool은 `true`, number는 0이 아닌 값이 필수다. 선택적인 number의 0은 min/max 검사를 건너뛴다. text는 빈 문자열을 거부한다. | 일반 scalar의 Required는 SQL NULL 금지다. 명시적으로 보낸 `false`, `0`, 빈 문자열은 허용한다. | 의미가 다름. PB 스키마를 그대로 옮기면 검증 결과가 달라진다. |
| 숫자 | float64 기반 number에 min/max/onlyInt와 `field+`, `field-` 수정자를 제공한다. | integer는 JSON 숫자, bigint/numeric은 정밀도 보존용 JSON 문자열이다. 알 수 없는 필드 이름을 거부하므로 같은 수정자 문법은 없다. | 정밀도·전송 형식·갱신 문법 모두 다름. |
| 문자열 계열 | text는 rune 단위 길이, 정규식, 자동 생성 패턴을 지원한다. email/URL은 형식과 도메인 허용/차단 목록을 갖는다. editor는 HTML 문자열과 별도 최대 크기·URL 변환 설정을 갖는다. | text의 공통 byte 제한과 DB 인덱스 관련 제한을 적용한다. 공개 FieldInput에는 사용자 지정 길이·패턴·도메인·editor 설정이 없다. | 전용 필드 및 설정 공백. 앱에서 문자열로 저장 가능하다는 것과 동일한 필드 기능은 다르다. |
| 날짜 | date는 파싱한 DateTime과 min/max, autodate는 OnCreate/OnUpdate 및 저장 재시도 상태 추적을 제공한다. | timestamptz는 오프셋이 있는 RFC3339 입력을 UTC로 정규화한다. 시스템 created_at/updated_at이 있으나 사용자 지정 autodate 필드는 없다. | 기본 저장 대응, 설정·자동 갱신 계약 차이. |
| JSON·위치 | JSON은 입력 경로에 따른 문자열 해석, 최대 크기, Required일 때 null/빈 문자열/빈 배열/빈 객체 거부를 제공한다. geoPoint는 lat/lon 경계와 Required일 때 (0,0) 거부를 제공한다. | JSONB 저장이 있으며 JSON null과 SQL NULL을 구분한다. geoPoint 전용 스키마·검증은 없다. | JSON 일부 대응, 위치 필드 공백. |
| select·password | select는 허용 값, 최대 선택 수, 중복 제거, append/prepend/subtract 수정자를 갖는다. password는 bcrypt 저장과 길이·패턴·비용 설정, hash getter를 갖는 일반 필드다. | enum/select 및 일반 레코드 password 타입은 없다. 사용자 인증 암호 기능은 별도 userauth 서비스에 있다. | 필드 공백. 인증 자체가 없다는 뜻은 아니다. |
| relation | 단일/다중 ID, 최소/최대 선택, 중복 정규화, 대상 존재 확인, 수정자와 삭제 정책을 제공한다. | 단일 FK/다중 순서 edge, 최소/최대 선택, restrict/cascade/set_null, 대상 UUID 검증과 권한 검사가 있다. 입력 중복은 거부한다. | 상당 부분 제공하나 ID·중복·삭제 계약이 다름. 선택 수 제한은 이미 구현되어 있다. |
| file | 레코드 필드 값으로 파일명 목록을 저장하며 필드별 MIME·크기·개수·Required·Protected·썸네일 설정을 갖는다. 업로드/실패 정리/커밋 후 이전 파일 삭제를 레코드 저장에 연결한다. | 별도 첨부 metadata와 업로드 API, pending/ready/deleted 수명주기, 다운로드 권한/토큰, local/S3 저장소가 있다. 크기·MIME·썸네일 한도는 FileOptions 전역 설정이며 file 필드의 Required/Unique/Indexed/Default는 거부한다. | 첨부 기능 제공, 필드별 설정 및 레코드 저장 계약 차이. |
| 썸네일 | WxH center/top/bottom crop, fit, 한 축 크기 등 복수 변환을 필드별로 지정한다. | JPEG/PNG를 전역 최대 크기 안에 비율 유지 축소하고 작은 이미지는 확대하지 않는다. | 이미 구현되어 있음. PB와 같은 변환 메뉴·URL 계약은 아니다. |
| 필드 메타데이터·식별 | ID 기반 변경, Hidden/System/Presentable/Help, 순서 이동과 polymorphic JSON 설정을 지원한다. FieldsList는 ID를 우선하고 ID가 없으면 이름으로 교체한다. | 저장된 필드 UUID와 순서가 있다. 공개 FieldInput에는 ID 기반 rename 입력 및 위 PB 메타데이터가 없다. 변경 계획은 이름으로 기존 필드를 대응한다. | 저장 ID 부재가 아니라 공개 편집 계약의 차이. 이름 변경을 데이터 보존 rename으로 가정하면 안 된다. |

## 소스 근거

- 공통 인터페이스: [PocketBase field.go](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/core/field.go#L77), [PgBase FieldInput](../../internal/schema/types.go#L29).
- 필수 값과 숫자: [bool](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/core/field_bool.go#L109), [number](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/core/field_number.go#L139), [PgBase codec](../../internal/query/codec.go#L36), [SQL NULL 설정](../../internal/schema/planner.go#L483).
- 문자열·자동 날짜·JSON: [text](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/core/field_text.go#L179), [autodate](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/core/field_autodate.go#L154), [JSON](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/core/field_json.go#L121).
- 관계: [PB relation](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/core/field_relation.go#L165), [PgBase 설정](../../internal/schema/relation_types.go#L45), [입력 검증](../../internal/query/codec.go#L162).
- 파일: [PB field lifecycle](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/core/field_file.go#L241), [PgBase FileOptions](../../pgbase/file_options.go#L8), [업로드](../../internal/filestore/service.go#L149), [썸네일](../../internal/filestore/service.go#L517).
- 필드 식별: [PB FieldsList](https://github.com/pocketbase/pocketbase/blob/5684ee24f1e88f71a2ede73d20cf547fbd509e16/core/fields_list.go#L118), [PgBase 이름 기반 계획](../../internal/schema/planner.go#L175).

## 후속 구현을 위한 완료 기준

1. 원하는 필드별 검증 설정을 스키마·API·UI·SDK에 함께 추가하고 기존 데이터 적용 여부를 정의한다.
2. `false`/`0`/빈 값, 숫자 정밀도, ID 형식 및 relation 중복의 호환 정책을 명시한다. PB 동작을 자동으로 정답 취급하지 않는다.
3. 파일별 제한과 여러 썸네일 변환이 필요하면 새 설정과 저장 비용·기존 파일 처리 정책을 함께 설계한다.
4. 데이터 보존 필드 rename은 기존 필드 ID를 입력받는 독립 변경 계약과 충돌·관계·view·index 검증으로 구현한다.

이번 감사에서는 제품 코드를 수정하거나 이 항목을 구현 완료로 처리하지 않았다.
