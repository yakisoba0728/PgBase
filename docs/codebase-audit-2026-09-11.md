# PgBase 전체 코드 감사 — 2026-09-11

기준 SHA는 `0ea07ff91dabf49d0f0e5f4c55aff53ed7ea291b`이다. 1,385개 tracked path의 고정 HEAD blob·범위 증거와 232개 기존 기능 ID를 전수 대조했다. 기능 수용 표의 과거 `구현 232/부분 0/미구현 0`은 수용 기록으로 보존하지만, 현재 품질 판단은 이 독립 감사의 20개 제품 결함과 2개 문서 결함이다.

## 핵심 결과

- 제품 결함: **20건** — P1 **9**, P2 **11**.
- 문서 결함: **2건**, 모두 P3. 제품 결함 집계와 분리했다.
- coverage: 1,385/1,385 path가 complete evidence를 가졌고 missing·incomplete·blob mismatch는 0이다. `full_read` **1361**, `source_provenance` **24**이다.
- 기능 행 raw 판정(232개, finding 중복 제거 전): contract_gap 3, defect 22, intentional_difference 51, static_reviewed 28, verified 128. unique 제품 finding 집계는 feature 행 수가 아니라 finding ID로 계산했다.

## 4단계 수행 범위

| Phase | 범위 | 기능 행 | 수행 내용 |
| --- | --- | ---: | --- |
| 1 | schema·fields·migrations·records·query/rules·auth/mail | 128 | 기능별 구현 trace·검증·권한·storage 경로 읽기 |
| 2 | files·realtime·extensions·operations | 60 | 파일·확장·운영 lifecycle과 rollback·cleanup 경계 읽기 |
| 3 | SDK·UI·repository delivery | 44 | client/UI/repository surface와 기존 assertion 읽기 |
| 4 | 교차검증·coverage closure·통합 보고 | — | 독립 finding 우선 판정과 ID·blob·range 합집합 검증 |

## 10개 그룹 파일 전수 완료

| 그룹 | assigned path | 전수 증거 |
| --- | ---: | --- |
| `schema_fields_migrations` | 224 | fixed-head blob·전체 read range 또는 허용 provenance |
| `records_query_rules` | 184 | fixed-head blob·전체 read range 또는 허용 provenance |
| `auth_mail` | 136 | fixed-head blob·전체 read range 또는 허용 provenance |
| `files_realtime` | 55 | fixed-head blob·전체 read range 또는 허용 provenance |
| `extensions` | 99 | fixed-head blob·전체 read range 또는 허용 provenance |
| `operations` | 248 | fixed-head blob·전체 read range 또는 허용 provenance |
| `sdk` | 48 | fixed-head blob·전체 read range 또는 허용 provenance |
| `ui` | 135 | fixed-head blob·전체 read range 또는 허용 provenance |
| `repository_delivery` | 138 | fixed-head blob·전체 read range 또는 허용 provenance |
| `documentation` | 118 | fixed-head blob·전체 read range 또는 허용 provenance |

`full_read`는 source를 실제 범위로 읽은 증거이고, `source_provenance`는 generated/config/asset 또는 구조화 생성물의 source·generator·integrity 근거다. 두 방법은 같은 의미로 세지 않으며 cross-read는 coverage 합계에서 제외했다.

## 우선순위 결함

| 등급 | ID | 요약 | 영향 | 권장 보완 |
| --- | --- | --- | --- | --- |
| P1 | [EXT-AUD-005](audits/2026-09-11/features.md#finding-ext-aud-005) | extension object 덮어쓰기 실패가 기존 blob을 지움 | 기존 metadata가 없는 object를 가리켜 확장 상태가 사라진다. | 새 blob을 확정하기 전 metadata 성공을 보장하고 이전 blob을 보존한다. |
| P1 | [SFM-001](audits/2026-09-11/features.md#finding-sfm-001) | migration snapshot이 호환 이름·검증 모드를 유실함 | 재생성 migration이 PocketBase 호환 조회와 입력 검증 의미를 바꿀 수 있다. | snapshot/replay에 CompatibilityName·ValidationMode를 포함한다. |
| P1 | [SFM-002](audits/2026-09-11/features.md#finding-sfm-002) | migration snapshot이 고급 index 의미를 평탄화함 | partial·expression index가 일반 index로 바뀌어 제약과 계획이 달라질 수 있다. | index의 keys·where·raw SQL을 snapshot에 보존한다. |
| P1 | [OPS-F-001](audits/2026-09-11/features.md#finding-ops-f-001) | runtime settings 반환값이 rate-limit slice를 공유함 | 호출자가 검증·저장 없이 live rate-limit 설정을 바꾸거나 경합을 만들 수 있다. | 모든 Get·Update·Reload 경계에서 slice를 깊게 복사한다. |
| P1 | [RQR-003](audits/2026-09-11/features.md#finding-rqr-003) | 다중 관계 collection truncate가 잘못된 schema의 edge table을 조회함 | 일반 DDL의 다중 관계 source·target collection은 비어 있지 않으면 truncate할 수 없다. | 관계 개수 조회를 pgbase_relation schema로 고정한다. |
| P1 | [RQR-001](audits/2026-09-11/features.md#finding-rqr-001) | 유효한 PocketBase 관계 규칙을 저장할 수 없음 | 관리자가 호환 API로 관계 규칙을 발행하지 못한다. | 저장 단계에서 PocketBase catalog compiler를 선택한다. |
| P1 | [RQR-002](audits/2026-09-11/features.md#finding-rqr-002) | 역방향 관계 filter가 잘못된 join 조건을 생성함 | 역관계 조회가 SQL 오류 또는 다른 행을 반환할 수 있다. | reverse hop의 단일·다중 relation predicate를 분기한다. |
| P1 | [EXT-AUD-002](audits/2026-09-11/features.md#finding-ext-aud-002) | 확장 OTP·인증 알림 helper가 caller payload를 버림 | 확장 코드가 만든 수신자·본문 정보가 전송 경로에 반영되지 않는다. | 검증한 payload를 실제 mail helper까지 전달한다. |
| P1 | [EXT-AUD-001](audits/2026-09-11/features.md#finding-ext-aud-001) | 확장 ApiError가 항상 정리된 500으로 평탄화됨 | 호출자가 원래 status와 error data를 받아 분기할 수 없다. | 호환 ApiError의 status·data를 보존해 응답한다. |
| P2 | [AUTHMAIL-001](audits/2026-09-11/features.md#finding-authmail-001) | email 없는 OAuth 가입이 첫 계정만 만들고 다음 계정을 거절함 | 동일 realm의 서로 다른 provider subject가 불일치하게 가입 실패한다. | 빈 email을 일관되게 거절하거나 별도 identity key로 저장한다. |
| P2 | [AUTHMAIL-002](audits/2026-09-11/features.md#finding-authmail-002) | 존재하지 않는 auth identity field를 정책으로 저장함 | 클라이언트에 보이지만 어떤 사용자도 만족할 수 없는 로그인 방법이 생긴다. | realm schema와 unique 조건을 정책 저장 전에 검증한다. |
| P2 | [EXT-AUD-003](audits/2026-09-11/features.md#finding-ext-aud-003) | $app.runInTransaction이 transaction-bound app과 중첩을 제공하지 않음 | 호환 extension transaction 안에서 model 동작과 nesting이 기대와 달라진다. | transaction-bound app facade와 nesting 정책을 구현한다. |
| P2 | [EXT-AUD-004](audits/2026-09-11/features.md#finding-ext-aud-004) | 일반 record CRUD 실패가 model error hook을 내보내지 않음 | 확장 hook이 실패 lifecycle을 관찰하거나 보완하지 못한다. | 일반 persistence 오류에서도 선언한 error phase를 emit한다. |
| P2 | [EXT-AUD-006](audits/2026-09-11/features.md#finding-ext-aud-006) | stream·multipart·copy object write가 object.write hook을 우회함 | 확장 검증·감사 hook이 일부 쓰기 경로에서 실행되지 않는다. | 모든 public write entry point를 hook pipeline으로 통합한다. |
| P2 | [EXT-AUD-007](audits/2026-09-11/features.md#finding-ext-aud-007) | object prefix의 %와 _가 SQL wildcard로 해석됨 | list와 deletePrefix가 같은 namespace의 무관한 object를 선택할 수 있다. | LIKE escape 또는 prefix-safe comparison을 사용한다. |
| P2 | [EXT-AUD-008](audits/2026-09-11/features.md#finding-ext-aud-008) | 생성 JavaScript declaration이 호환 API·byte type을 누락함 | 유효한 확장 코드가 editor에서 거절되고 잘못된 타입 가정을 허용한다. | runtime surface에서 declaration을 생성·검증한다. |
| P2 | [OPS-F-002](audits/2026-09-11/features.md#finding-ops-f-002) | managed restore가 open 실패 뒤 provisioned target을 폐기하지 않음 | 재시도할 때 복원된 DB·file target이 누적될 수 있다. | target 소유 직후 cleanup defer를 등록한다. |
| P2 | [SDK-AUDIT-001](audits/2026-09-11/features.md#finding-sdk-audit-001) | TypeScript realtime이 terminal error도 무한 재시도함 | 권한·cursor 오류에서 subscription이 완료되지 않고 요청을 반복한다. | terminal PgBaseError를 종료하고 caller에 전달한다. |
| P2 | [SDK-AUDIT-003](audits/2026-09-11/features.md#finding-sdk-audit-003) | Dart realm OAuth browser helper가 기본 realm으로 시작함 | 선택 realm의 provider 설정 대신 기본 realm 흐름으로 진입할 수 있다. | realm-specific OAuth start helper를 사용한다. |
| P2 | [SFM-003](audits/2026-09-11/features.md#finding-sfm-003) | native name과 compatibility name이 대소문자 무시 충돌 가능 | 관리자 collection 조회가 map 순서에 따라 다른 schema를 반환할 수 있다. | 두 이름 공간을 함께 case-fold uniqueness 검증한다. |
| P3 | [DOC-001](audits/2026-09-11/features.md#finding-doc-001) | 비교표 source pointer가 workstation 절대 경로를 사용함 | 다른 checkout의 검토자가 증거를 열 수 없다. | tracked source pointer를 repo-relative path로 유지한다. |
| P3 | [DOC-RT-001](audits/2026-09-11/features.md#finding-doc-rt-001) | 현재 비교표가 realtime handshake 부재를 잘못 기록함 | 현재 기능과 문서 상태가 어긋난다. | PB_CONNECT 구현과 확인 근거로 비교표를 정정한다. |

## 선택 실행과 실제 재현

RQR-001·RQR-002의 격리 integration 재현은 결함을 확인했고, RQR-003은 다중 relation의 source/target 모두에서 expected-success truncate가 `42P01`로 실패하는 것을 재현했다. 반대로 기존 scalar `TestTruncate*`는 통과했지만 해당 relation branch를 다루지 않았다.

SFM-001·SFM-002는 PostgreSQL migration snapshot의 export→replay→store 경로를 끝까지 대조해 호환 이름·검증 모드와 고급 index 정의가 실제 replay 입력에서 빠지는 효과를 확인했다. SFM-002는 이 교차검증에서 물리 index를 새로 변경하지 않았으므로 그 한계를 유지한다.

FIL-RT-001은 invalid multipart 요청 뒤 ready row·object가 `1 → 1`로 보존되고 새 part가 정리되는 focused 검증으로 철회했다. FIL-RT-002도 durable deletion queue와 `DrainOne`이 metadata 삭제 뒤 physical object를 제거하는 격리 검증으로 철회했다. 환경 파일, 자격정보, 미실행 TOTP 테스트와 임시 DB 식별자는 출판물에서 제외했다.

## 판정과 한계

`verified`와 `static_reviewed`는 읽은 source/assertion 및 기록된 실행 범위에서 결함을 찾지 못했다는 뜻이다. 모든 경로가 동적으로 실행됐다는 뜻은 아니다. 정적 trace, 선택된 focused test, 기존 assertion 읽기를 구분했으며 외부 OAuth/SMTP/production 서비스는 사용하지 않았다.

RQR-003은 정상 DDL로 만든 다중 relation에 참여하는 비어 있지 않은 collection의 truncate에만 한정했다. SCH-07과 UI-07에 반영했지만 일반 REC-05 delete나 FLD-12 relation 전체로 확장하지 않았다. UI-001은 RQR-001의 UI 영향 alias라 unique 결함으로 중복 집계하지 않았다.

FIL-RT-001, FIL-RT-002, AUTHMAIL-003, SDK-AUDIT-002는 철회 또는 명시 차이로 남기고 제품 결함에서 제외했다. SFM-001은 compatibilityName/validationMode 손실 범위로 좁혔으며 update Type/System 보존을 손실로 보고하지 않았다.

## 전체 증거

- [232개 기능 표](audits/2026-09-11/features.md)
- [구조화 기능 데이터](audits/2026-09-11/features.json)
- [구조화 finding 데이터](audits/2026-09-11/findings.json)
- [1,385개 coverage 증거](audits/2026-09-11/coverage.json)
- [범위와 방법](audits/2026-09-11/scope.json)
