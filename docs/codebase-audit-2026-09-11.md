# PgBase 전체 코드 감사 — 2026-09-11

기준 SHA는 `0ea07ff91dabf49d0f0e5f4c55aff53ed7ea291b`이다. 1,385개 tracked path의 고정 HEAD blob·범위 증거와 232개 기존 기능 ID를 전수 대조했다. 기능 수용 표의 과거 `구현 232/부분 0/미구현 0`은 수용 기록으로 보존하지만, 현재 품질 판단은 이 독립 감사의 20개 제품 결함과 2개 문서 결함이다.

## 핵심 결과

- 제품 결함: **20건** — P1 **9**, P2 **11**.
- 문서 결함: **2건**, 모두 P3. 제품 결함 집계와 분리했다.
- 범위: schema/fields/migrations·records/query/rules·auth/mail·files/realtime/extensions/operations·SDK/UI/repository delivery/documentation의 4 phase에서 그룹별 assigned path를 전수 읽기 증거로 대조했다.
- coverage: 1,385/1,385 path가 complete evidence를 가졌고 missing·incomplete·blob mismatch는 0이다. `full_read` **1361**, `source_provenance` **24**이다.
- 기능 행 raw 판정(232개, finding 중복 제거 전): contract_gap 3, defect 23, intentional_difference 51, static_reviewed 27, verified 128. unique 제품 finding 집계는 feature 행 수가 아니라 finding ID로 계산했다.

## 우선순위 결함

| 등급 | ID | 요약 |
| --- | --- | --- |
| P2 | [AUTHMAIL-001](audits/2026-09-11/features.md#finding-authmail-001) | OAuth signup admits one empty-email account and rejects the next unrelated identity |
| P2 | [AUTHMAIL-002](audits/2026-09-11/features.md#finding-authmail-002) | Auth identity policy persists fields absent from the realm schema |
| P1 | [EXT-AUD-001](audits/2026-09-11/features.md#finding-ext-aud-001) | Thrown compatibility ApiError values are flattened to a sanitized 500 |
| P1 | [EXT-AUD-002](audits/2026-09-11/features.md#finding-ext-aud-002) | OTP and auth-alert helpers validate and then discard caller payloads |
| P2 | [EXT-AUD-003](audits/2026-09-11/features.md#finding-ext-aud-003) | $app.runInTransaction supplies a DB facade and rejects nested runInTransaction calls |
| P2 | [EXT-AUD-004](audits/2026-09-11/features.md#finding-ext-aud-004) | Ordinary record CRUD never emits advertised record model error phases |
| P1 | [EXT-AUD-005](audits/2026-09-11/features.md#finding-ext-aud-005) | A metadata failure after extension-object overwrite can delete the last committed object |
| P2 | [EXT-AUD-006](audits/2026-09-11/features.md#finding-ext-aud-006) | Stream, multipart, and copy writes bypass object.write hooks |
| P2 | [EXT-AUD-007](audits/2026-09-11/features.md#finding-ext-aud-007) | Object prefix listing interprets legal percent and underscore characters as LIKE wildcards |
| P2 | [EXT-AUD-008](audits/2026-09-11/features.md#finding-ext-aud-008) | Generated JavaScript declarations omit compatibility APIs and mis-type byte values |
| P1 | [OPS-F-001](audits/2026-09-11/features.md#finding-ops-f-001) | Runtime settings snapshots retain mutable rate-limit slice aliases |
| P2 | [OPS-F-002](audits/2026-09-11/features.md#finding-ops-f-002) | Managed restore does not discard a provisioned target when opening it fails |
| P1 | [RQR-001](audits/2026-09-11/features.md#finding-rqr-001) | Valid PocketBase relation rules cannot be saved through the compatibility endpoint |
| P1 | [RQR-002](audits/2026-09-11/features.md#finding-rqr-002) | Reverse relation filters render forward correlation predicates |
| P1 | [RQR-003](audits/2026-09-11/features.md#finding-rqr-003) | Multiple-relation truncate counts the edge table in app instead of pgbase_relation |
| P2 | [SDK-AUDIT-001](audits/2026-09-11/features.md#finding-sdk-audit-001) | Multiplex TypeScript realtime retries terminal failures forever |
| P2 | [SDK-AUDIT-003](audits/2026-09-11/features.md#finding-sdk-audit-003) | Dart realm browser OAuth starts the default-realm flow |
| P1 | [SFM-001](audits/2026-09-11/features.md#finding-sfm-001) | Migration snapshots cannot preserve collection compatibility metadata |
| P1 | [SFM-002](audits/2026-09-11/features.md#finding-sfm-002) | Migration snapshot export flattens advanced indexes |
| P2 | [SFM-003](audits/2026-09-11/features.md#finding-sfm-003) | Native and compatibility collection names can collide case-insensitively |
| P3 | [DOC-001](audits/2026-09-11/features.md#finding-doc-001) | Comparison source pointers use machine-specific absolute paths |
| P3 | [DOC-RT-001](audits/2026-09-11/features.md#finding-doc-rt-001) | Current comparison matrix incorrectly says the PB_CONNECT handshake is absent |

## 선택 실행과 실제 재현

기존 선택 테스트와 focused 재현을 구분해 읽었다. RQR-001·RQR-002의 격리 integration 재현은 결함을 확인했고, RQR-003은 다중 relation의 source/target 모두에서 expected-success truncate가 `42P01`로 실패하는 것을 재현했다. 반대로 기존 scalar `TestTruncate*`는 통과했지만 해당 relation branch를 다루지 않았다. 환경 파일, 자격정보, 미실행 TOTP 테스트와 임시 DB 식별자는 출판물에서 제외했다.

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
