# 다음 작업과 완료 기준

2026-11-22 기준: main은 `56823891`(schema 71)이고 v0.4.0 이후 tag 없이 20 commit 앞서 있다. 2026-11-19 기록 뒤 문서 기록([PR #167](https://github.com/yakisoba0728/PgBase/pull/167)), setup-dart 1.8.2([PR #169](https://github.com/yakisoba0728/PgBase/pull/169)), 데이터 무결성·백업 복구([PR #182](https://github.com/yakisoba0728/PgBase/pull/182)), Go·관리자 UI 의존성 통합([PR #183](https://github.com/yakisoba0728/PgBase/pull/183))을 병합했다. 네 PR 모두 migration이 없다. 운영 배포 여부는 확인하지 않음. 이 기록 시점에 보안 수정 [PR #184](https://github.com/yakisoba0728/PgBase/pull/184)(migration `000072`, schema 72)는 열려 있고 병합되지 않았다. 범위와 검증은 [현재 상태](project-status.md) §19를 따른다.

2026-11-19 기준: main은 `64512f78`(schema 71)이다. 요청 로그 정리([PR #159](https://github.com/yakisoba0728/PgBase/pull/159)), jobs 예약 시각([PR #160](https://github.com/yakisoba0728/PgBase/pull/160)), batch·복원·로그 보호([PR #161](https://github.com/yakisoba0728/PgBase/pull/161)), 감사 지적 일괄 수정과 migration `000070`·`000071`([PR #162](https://github.com/yakisoba0728/PgBase/pull/162)), 그 후속 test 수정([PR #163](https://github.com/yakisoba0728/PgBase/pull/163)), 브랜드 마크([PR #164](https://github.com/yakisoba0728/PgBase/pull/164)), 컬렉션 API 탭 서비스 토큰 예시([PR #166](https://github.com/yakisoba0728/PgBase/pull/166))를 병합했다. 운영 배포 여부는 확인하지 않음. 범위와 검증은 [현재 상태](project-status.md) §18을 따른다.

2026-11-18 기준: main과 운영의 runtime은 `df18bfe3`(schema 69)이다. 미룬 UI 항목 4개([PR #157](https://github.com/yakisoba0728/PgBase/pull/157))와 간헐 실패 test 두 건([PR #155](https://github.com/yakisoba0728/PgBase/pull/155)·[PR #156](https://github.com/yakisoba0728/PgBase/pull/156))을 처리해 배포했다. 운영에는 첫 관리자가 만들어져 있다. 범위와 검증은 [현재 상태](project-status.md) §17을 따른다.

2026-11-17 기준: main은 `d64c729f`(schema 69)이고 운영은 main `57507741`을 빈 데이터로 새로 설치한 상태다. 플랫폼 강화([PR #148](https://github.com/yakisoba0728/PgBase/pull/148))를 병합하고 같은 날 배포했으며, main `57507741`을 [v0.4.0](https://github.com/yakisoba0728/PgBase/releases/tag/v0.4.0)으로 게시했다. 범위와 검증은 [현재 상태](project-status.md) §15를 따른다.

2026-11-15 기준: main은 `5cb9ea0d`(schema 67)이고 같은 commit이 운영에 배포돼 있다. 최신 릴리스 tag는 v0.3.1(schema 66)이라 그 뒤의 백엔드 재작성(PR #135–#137, schema 67), PR #138–#141, 관리자 UI 개편([PR #142](https://github.com/yakisoba0728/PgBase/pull/142))과 크기·모션 후속([PR #144](https://github.com/yakisoba0728/PgBase/pull/144))은 tag 없이 main에만 있다. 이번 UI 개편의 범위와 검증은 [현재 상태](project-status.md) §13을 따른다. 아래 2026-11-13 문단과 표는 당시 기록에 이번 항목을 더한 것이다.

2026-11-13 보완은 PR [#125](https://github.com/yakisoba0728/PgBase/pull/125)·[#133](https://github.com/yakisoba0728/PgBase/pull/133)으로 병합하고, 전체 CI·릴리스 gate 통과 후 [v0.3.1](https://github.com/yakisoba0728/PgBase/releases/tag/v0.3.1)과 운영 서버에 반영했다(schema 66). [구현·검증 근거](production-readiness-2026-11-13.md), [배포 기록](deployment-2026-11-13.md)을 따른다. 이번 최종 점검의 필수 코드 수정·문서·병합·배포 항목은 완료했다.

| 구분 | 항목 | 현재 상태 / 다음 조건 |
| --- | --- | --- |
| 소유자 요청으로 제외 | 운영 백업·복원 | 운영 백업 생성·외부 저장소·예약 설정을 하지 않음. 서버 밖 DB·파일·키 사본과 실제 복원 인수는 미완료. 소유자가 재개를 요청할 때 진행 |
| 완료 | SQL view·auth snapshot 일관성 | catalog·whole-row·기존 view 검사, realm 최종 diff 보존과 삭제 정책 회귀 통과 |
| 완료 | 서비스 토큰 로그·검색·파일 metadata·오류 field | schema 66·UI 구현, migration/복원·HTTP·UI·CI 통과, 운영 반영 |
| 완료 | 검증·문서·릴리스 | PostgreSQL matrix·race·Dart·E2E·restore·security·서명 gate 통과, v0.3.1 게시, 운영 schema 66/66·health 정상 |
| 완료 | 관리자 UI 개편([PR #142](https://github.com/yakisoba0728/PgBase/pull/142)) | 공용 컴포넌트·디자인 토큰·모션·결함 수정, unit 381·browser 23·필수 CI 통과, main 병합과 운영 배포(schema 67 그대로) |
| 완료 | UI 크기 확대·모션 강화([PR #144](https://github.com/yakisoba0728/PgBase/pull/144)) | PocketBase v0.40.6을 크기 기준으로만 참고해 글자·컨트롤·표 행 확대, 모션 재조정. unit 386·browser 23·필수 CI 통과, 운영 배포 |
| 완료 | 로딩·토스트 통일과 조회 최적화([PR #146](https://github.com/yakisoba0728/PgBase/pull/146)) | 토스트 하단 중앙·3 tone, 재조회 시 이전 내용 유지, cache 정책과 prefetch, count 생략·hydration 배치·`LIKE` 컴파일·gzip. unit 403·browser 23·필수 CI 통과, 운영 배포(schema 67 그대로) |
| 완료 | 플랫폼 강화 병합([PR #148](https://github.com/yakisoba0728/PgBase/pull/148)) | schema 68·69, 읽기 commit의 WAL flush 대기 제거, 보안·저장소·realtime·SDK 결함 수정. 전체 qualification 15개 job 통과, 부하 gate p99 13.2ms(기준 250ms, 1회). main 병합, 운영 배포(schema 69/69) |
| 완료 | 운영 초기화(소유자 지시) | DB·파일 volume과 서버 백업을 지우고 main `57507741`을 빈 상태로 재배포. 69/69, doctor 통과, setup 대기 |
| 완료 | 관리자 UI 전체 화면 점검([PR #151](https://github.com/yakisoba0728/PgBase/pull/151)·[PR #153](https://github.com/yakisoba0728/PgBase/pull/153)) | 173장 캡처로 찾은 깨짐 약 45건 수정, 회귀 6건 수정 후 재확인. unit 459·필수 CI 통과, 운영 배포(schema 69 그대로) |
| 완료(소유자 결정) | frontend audit gate 범위([PR #152](https://github.com/yakisoba0728/PgBase/pull/152)) | 배포 의존성만 막고 개발 의존성은 경고. picomatch GHSA-fc5q-39xc-vjpf 패치가 나오면 재검토 |
| 완료 | 간헐 실패 test 두 건([PR #155](https://github.com/yakisoba0728/PgBase/pull/155)·[PR #156](https://github.com/yakisoba0728/PgBase/pull/156)) | `internal/filestore`는 250ms 대기가 1초 정리 예산보다 짧았고, `internal/cli` watch는 상태 줄 출력 전에 stdout을 읽었다. 둘 다 test만 수정, 제품 경합 아님 |
| 완료 | UI 점검에서 미룬 항목([PR #157](https://github.com/yakisoba0728/PgBase/pull/157)) | 화면 쪽 email·URL·필수 검증, 휴대폰 사용자·logs 목록의 보조 열, jobs pane 옆 목록, 비활성 danger 버튼. 로컬 서버 렌더링으로 확인, unit 460·필수 CI 통과, 운영 배포 |
| 완료 | 요청 로그 잡음 제거([PR #159](https://github.com/yakisoba0728/PgBase/pull/159)) | 성공한 health probe와 관리자 콘솔 자체 `GET`/`HEAD`를 저장 로그에서 제외. 실패·로그인·변경·내보내기 읽기는 계속 저장. 관리자가 어떤 화면을 열었는지는 더 이상 남지 않는다 |
| 완료 | jobs 예약 시각([PR #160](https://github.com/yakisoba0728/PgBase/pull/160)) | interval·cron 발생 시각을 PostgreSQL 시각으로 계산. jobs 통합 package race 검사 PostgreSQL 17.9·18.6 통과(PR 본문) |
| 완료 | batch·복원·저장소 정리·로그 정리 보호([PR #161](https://github.com/yakisoba0728/PgBase/pull/161)) | 초과 batch 응답은 commit 전 400으로 rollback, 불확실한 복원 commit에서 파일 보존, S3 endpoint 정규화, 로그 기간 정리를 settings revision으로 fence |
| 완료 | 감사 지적 일괄 수정([PR #162](https://github.com/yakisoba0728/PgBase/pull/162)) | migration `000070`·`000071`(index만), schema 69 → 71. 서비스 토큰 목록 `{items,nextAfter?}`·규칙 PUT `revision`(오래되면 412)·offset 응답 budget 초과 시 검증 오류·realtime `retryable` 표시 등 계약 변경. 다중 instance realtime은 load-balancer affinity 필요 |
| 완료 | #162 후속 test 수정([PR #163](https://github.com/yakisoba0728/PgBase/pull/163)) | #162 병합 뒤 main push Backend의 integration vet 실패를 해소. test만 변경 |
| 완료 | 브랜드 마크([PR #164](https://github.com/yakisoba0728/PgBase/pull/164))·서비스 토큰 API 예시([PR #166](https://github.com/yakisoba0728/PgBase/pull/166)) | favicon·워드마크 아이콘·README 로고, 컬렉션 API 탭의 서비스 토큰 호출자 모드. UI unit 473(#164)·475(#166) 통과(PR 본문) |
| 완료 | 데이터 무결성·백업 복구([PR #182](https://github.com/yakisoba0728/PgBase/pull/182)) | migration 없음(schema 71). live 복원 활성화·백업 보존 정리·파일 정리 fence, S3 저장소 장애 중 가용성과 중단된 예약 백업의 같은 발생 재시도, transaction 안의 필터 삭제·첨부 교체 검사. 계약 추가: 관리자 `matchFilter` 삭제·batch `$replaces`(불일치 412), SDK `enrichmentFailed`, 일정 편집 시 명시적 IANA 시간대. 번들 Compose에 파일 이전 scratch volume. 통합 PostgreSQL 18.6+MinIO 51 package·UI 483·Chromium 26·Firefox/WebKit 12·SDK test 통과(PR 본문) |
| 완료 | 의존성 통합([PR #183](https://github.com/yakisoba0728/PgBase/pull/183))·setup-dart 1.8.2([PR #169](https://github.com/yakisoba0728/PgBase/pull/169)) | Dependabot #171–#177·#180·#181을 묶어 Node 24.22.3·Tiptap 3.34.0·Playwright 1.65.1·jsdom 31.0.1·`@vitejs/plugin-react` 6.3.0·user-event 14.6.12·AWS SDK·Prometheus common 0.74.0, 관리자 bundle 재빌드. 원래 PR #168·#170–#181은 병합 없이 닫힘 |
| 열린 PR(이 기록 시점) | 보안 수정([PR #184](https://github.com/yakisoba0728/PgBase/pull/184)) | relation 표시 권한·재설정 token 검사 순서·인증 메일 admission·관리자 TOTP counter 1회 소비·저장소 작업 admission. migration `000072`(`admin_totp.last_counter`)로 schema 72, 병합 후 배포에는 stop–migrate–start와 로그인 처리 instance 전체 교체가 필요. 최신 commit `8666bbd3`의 PR CI(Backend·Frontend·Security) 통과, Full·Nonperformance source qualification은 PR에서 skipped. 병합 여부는 이 문서 밖에서 확인한다 |
| 남음 | 운영 배포(schema 71) | 2026-11-22에도 확인하지 않음. 69 → 71은 stop–migrate–start 절차. S3 정리 worker와 로그 정리 replica를 모두 새 build로 올린 뒤에 #161 보호를 전제로 한다. #182 웹 복원 전에는 이전 build를 포함한 다른 instance를 멈춘다 |
| 다음 유지보수 | 반영하지 않은 이미지 갱신 | Dependabot #168(golang 1.27.4)과 #170(postgres16·17 client 단계를 18.6으로)은 병합 없이 닫혔고 main은 golang 1.26.11, postgres 16.16·17.12 그대로다. 닫은 사유는 PR에 없다 |
| 남음 | #162 통합 검증 결과 | PR 본문 작성 시점에 통합 Go·PostgreSQL+MinIO·Chromium 실행이 진행 중이었다. 필수 검사 5개 통과는 확인했고 그 밖의 결과는 확인하지 않음 |
| 남음 | CI PostgreSQL 재초기화 | PR #157 통합 검사에서 backend가 `exit code 2`로 끝나 서버가 재초기화됐다(재실행 통과). 원인 test 미확인 |
| 남음 | UI 한계 | 날짜·시간 입력 표기(자체 위젯 필요), 휴대폰 logs 요청 칸, pane을 닫은 jobs 목록의 긴 결과 문구. Safari·Firefox·setup 화면·모바일 overlay 미확인 |
| 소유자 결정 | v0.4.0 이후 릴리스 | v0.4.0(schema 69) 이후 #151, #153, #157(main과 운영)과 #159–#164, #166, #167, #169, #182, #183(main, 운영 확인하지 않음)이 tag 없이 있다(2026-11-22 main `56823891`, 20 commit). #162로 source가 schema 71을 요구하므로 schema 69 그대로인 패치 릴리스가 아니다. #184가 병합되면 schema 72가 된다 |
| 소유자 작업 | 운영 설정 | 첫 관리자는 만들어졌다. SMTP 등 운영 설정을 다시 넣는다 |
| 소유자 작업 | Dokploy API key 교체 | 작업 세션 기록에 key가 노출됐다 |
| 남음 | 운영 사후 확인 | 로그인 후 화면·CRUD·realtime·jobs·파일 동작은 운영에서 확인하지 않았다. 운영 백업은 없다 |
| 소유자 결정 | 쓰기 경로 WAL flush | 쓰기 요청당 약 2.5회. outbox 전달 commit을 완화하면 줄지만 전달·내구성 계약이 바뀐다 |
| 남음 | 보안 검토 대기 | CodeQL 25건의 조치 불필요 분류 재확인, 이미지 의존성 30건 검토. 2026-11-22 code-scanning API는 "no analysis found"를 돌려줘 25건을 이 방법으로 재확인하지 못했다. 같은 날 열린 Dependabot 경고 0, 열린 issue 0 |
| 확인 | `main` 보호(2026-11-22) | classic branch protection은 없다(API 404). repository ruleset "Reviewed main with required CI"가 active로 PR, 필수 검사 5개(Go unit·Observability artifacts·PostgreSQL integration / 17.9·test·audit), 삭제·non-fast-forward 금지를 요구한다 |
| 소유자 결정 | 운영 요청 지연 | 한국 접속이 Cloudflare LAX로 가서 요청당 약 0.5초. proxy 해제(DNS only)·유료 plan·서버 이전 중 선택. 코드로는 줄일 수 없다 |
| 소유자 결정 | 대량 텍스트 검색 | 컬렉션 텍스트 필드 trigram index가 없어 50만 행 검색 1.3초. migration·쓰기 비용·disk 증가가 따른다 |
| 남음 | UI 개편 사후 확인 | 운영 로그인 후 화면 확인, 서버 `doctor`·binary 일치 job, 초기 setup·login OTP·다크/좁은 폭 화면의 browser 확인. 배포 전 서버 dump는 뜨지 않았다(UI만 변경) |
| 완료 | 릴리스 [v0.4.0](https://github.com/yakisoba0728/PgBase/releases/tag/v0.4.0) | main `57507741`, schema 69. 릴리스 gate 통과, 이미지 `v0.4.0`·`latest` 승격, GitHub 릴리스와 첨부 8개 게시 |
| 남음 | 관리자 UI 작업 실행 이력 | 개별 작업의 전체 실행 이력은 서버 endpoint가 없어 미구현 |
| 구성 완료 | 브라우저 E2E | [기본 E2E](../.github/workflows/e2e.yml)는 Chromium을 실행한다. [전체 검사](../.github/workflows/full-qualification.yml)와 [기능 검사](../.github/workflows/nonperformance-qualification.yml)는 Firefox·WebKit도 점검한다. [릴리스 검사](../.github/workflows/release-gate.yml)도 E2E를 호출한다. |
| 다음 유지보수 | 선택적 의존성 갱신 | #126–#132는 보류 사유와 함께 닫음. 이번 릴리스의 필수 보안 패치는 포함. 새 버전은 다음 주기에 재검증 |
| 별도 소유자 결정 | npm/pub.dev 공개 패키지 | 이름·scope·공개 여부·token 미정. SDK source/conformance는 검증했으며 registry 배포는 하지 않음 |

과거 N/M/P/O 단계와 날짜별 미완료 표시는 [구현 이력](project-status.md)과 [TODO 이력](../TODO.md)에 당시 상태로 보존한다. 현재 열린 작업의 합계로 해석하지 않는다. schema 66 적용 뒤 이전 binary만 다시 배포하는 롤백은 지원하지 않는다.
