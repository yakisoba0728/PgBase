# 2026-11-13 새 관리자 UI 병합과 운영 배포

최신 배포는 아래 네 번째 절의 `043dc2b3`·schema 66이다. 앞선 절은 당시 결과를 보존한다.

- 병합: [PR #108](https://github.com/yakisoba0728/PgBase/pull/108)을 merge commit `2912e399b6b1f45cfad8ae8431c1ab5d94c9557e`로 main에 병합했다.
  PR의 필수 CI 5개(Go unit, Observability artifacts, PostgreSQL integration / 17.9, Frontend test, Security audit)가 통과했다.
- 대상: `https://pgbase.yaki.kr/admin/` (Dokploy Compose 서비스 `PgBase`, 소스 `main`, 자동 배포는 꺼진 상태 유지).
- 이전 배포: main `7fbf1db39fdee02d9f52c1a928591ea8797870f6`.
- 배포: Dokploy API `compose.deploy`로 시작했고, 2026-11-12T20:08:45Z에 생성된 배포가 `done`으로 끝났다.
  비밀값·도메인·DB와 파일 volume·환경 변수는 바꾸지 않았다. schema 65 그대로이며 migration은 없다.
- 배포 전 DB dump는 만들지 않았다. 이번 변경은 관리자 UI와 그 빌드 산출물뿐이고 schema와 서버 Go 코드는 바뀌지 않았다.
  2026-11-12 기록의 dump(`pgbase-pre-f7de2f5`)는 같은 서버 volume에 남아 있으며 서버 밖 백업은 여전히 없다.
- 확인:
  - `/health/live`, `/health/ready` 200.
  - `/admin/`의 `index-CJNNe1BC.js`, `index-BkL_04hT.css`가 commit된 `ui/admin/dist`와 같다.
    이 둘과 lazy chunk 3개, 글꼴 파일 1개를 내려받아 SHA256이 commit본과 일치함을 확인했다.
  - CSP header는 기존 값 그대로다. `/admin/collections` deep link 200, 알 수 없는 경로 404, 세션 없는 `/api/admin/session` 401.
  - 브라우저에서 로그인 화면이 한국어로 표시됐다. `/api/admin/status`는 `setupRequired: false`를 돌려준다(관리자가 이미 존재).
  - 로그인 이후 화면은 운영 서버에서 확인하지 않았다. 계정과 비밀번호는 소유자만 가진다.
- 관찰: 브라우저 console에 Cloudflare가 주입한 Web Analytics beacon script와 inline script가 CSP(`script-src 'self'`)에 막혔다는 오류가 남는다.
  PgBase가 제공한 자산이 아니며 동작에는 영향이 없다. 없애려면 Cloudflare 쪽 자동 주입을 끄면 된다.
- 되돌리기: main을 PR #108 이전으로 되돌리는 revert commit을 병합한 뒤 같은 방식으로 다시 배포한다. 이번에는 필요하지 않았다.
- 릴리스 tag와 이미지는 만들지 않았다. 최신 릴리스는 v0.2.3이며 운영 서버는 source build다.

## 같은 날 두 번째 배포: 백엔드 수정

- 병합: [PR #122](https://github.com/yakisoba0728/PgBase/pull/122)를 merge commit `3bec8b3a35e5ae94bbf7ee0d11b1dc2b9c3c0487`로 main에 병합했다.
  필수 CI 5개가 통과했다. `PostgreSQL integration / 17.9`는 첫 실행에서 `internal/filestore`의
  `TestStageCleanupIntentCannotDrainBeforeCompletion` 하나가 실패했고(이 PR이 바꾸지 않은 package), 실패 job 재실행에서 통과했다.
  원인은 조사하지 않았으며 시간 의존 test 후보로 남긴다.
- 배포: 같은 Dokploy Compose 서비스에 `compose.deploy`로 배포했고 2026-11-13T03:25:29Z에 생성된 배포가 `done`으로 끝났다.
  migration은 없고 schema 65 그대로다. 비밀값·도메인·volume·환경 변수는 바꾸지 않았다. 배포 전 DB dump는 만들지 않았다.
- 확인:
  - `/health/live`, `/health/ready` 200. `/admin/`의 `index-DYpDBvTV.js`, `index-BkL_04hT.css`가 commit된 `ui/admin/dist`와 SHA256이 같다.
  - 새 경로 `/api/admin/realtime/dead`, `/api/admin/users/{id}`가 세션 없이 401을 돌려준다(404가 아니므로 등록됨).
  - `/api/admin/backups`, `/api/admin/backup-jobs`, `/api/admin/storage/backups`, `/api/admin/sql/queries`는 404다.
    이 배포에는 managed backup 디렉터리와 SQL 콘솔 설정이 없어 해당 경로가 등록되지 않는다(이전 배포와 같은 동작).
    따라서 이 서버에는 managed backup이 구성돼 있지 않다.
  - 로그인이 필요한 동작(현재 비밀번호 확인, IP allowlist 거부, 표시되는 호출자 주소)은 운영 서버에서 확인하지 않았다.
    Cloudflare·Traefik 뒤에서 프로필 화면에 표시되는 주소가 실제 접속 주소인지 소유자가 확인해야 하며,
    다르면 `PGBASE_TRUSTED_PROXIES` 설정을 먼저 바로잡고 그 전에는 allowlist를 저장하지 않는다.
- 운영에서 달라지는 점: 프로필 변경, 관리자 생성·활성 전환·비밀번호 재설정, IP allowlist 변경에 현재 비밀번호가 필요하다.

## 같은 날 세 번째 배포: 의존성 통합

PR [#124](https://github.com/yakisoba0728/PgBase/pull/124) main `8b078a8c71304600bf68b2831c0d8a27be974768`을 전체 main CI 성공 후 같은 서비스에 배포했다. deployment `YFTDjQE4C1y6BKLugqHNq`는 `2026-11-13T04:08:07.669Z` 완료. schema 65 유지, 설정·volume 보존, health·인증 경계·UI 자산 SHA256 확인. 운영 백업 생성 없음.

## 같은 날 네 번째 배포: schema 66과 v0.3.1

- PR [#125](https://github.com/yakisoba0728/PgBase/pull/125)와 [#133](https://github.com/yakisoba0728/PgBase/pull/133), source `043dc2b325af3bbb0f1caf69568f239fbed7e0c6`. main [Backend](https://github.com/yakisoba0728/PgBase/actions/runs/43371117456)·[Frontend](https://github.com/yakisoba0728/PgBase/actions/runs/43371117457)·[Security](https://github.com/yakisoba0728/PgBase/actions/runs/43371117507)와 전체 [signed release gate](https://github.com/yakisoba0728/PgBase/actions/runs/43371132129) 성공 후 진행.
- 기존 writer를 `compose.stop`으로 정지하고 앱·DB exit 0을 확인했다. 기존 환경·도메인·DB/파일 volume·autoDeploy(false)를 보존한 `compose.deploy`가 명시적 migration 후 새 앱을 시작했다. 소유자 지시로 운영 백업은 만들거나 구성하지 않았다.
- 첫 deployment `2kbTR_NuU8bG7cy7_rwjk`는 Go 빌드 중 Dokploy API가 잠시 502를 반환한 뒤 cancelled로 확인됐다. 원인은 확정하지 못했다. 설정 변경 없이 재시도한 `305sEP7cz1zb8PFjgyHcv`는 `2026-11-13T08:08:29.923Z` 시작, `08:08:59.796Z` 완료(done).
- `/health/live`, `/health/ready`, `/admin/`, `/admin/collections` 200. 미등록 경로 404, 세션 없는 admin session·dead-letter·user 경로 401, setupRequired false. `script-src 'self'` 유지.
- `index-MEuasBQ7.js`·`index-BkL_04hT.css` 및 Button·collections·CollectionPage·RichText chunk가 저장소 dist와 SHA256 일치. 앱·PostgreSQL healthy, migrate·files-init exit 0.
- 기존 관리자 브라우저 세션에서 읽기 전용 확인: migration **66/66**, DB 연결·listener 준비, schema health 정상·finding **0**, 기존 레코드 수 유지. 사용자 값·ID·계정·token은 기록하지 않는다. 운영 CRUD·파일 변경·IP allowlist 저장이나 외부 호출자 IP 일치 검증은 하지 않았다.
- [v0.3.1](https://github.com/yakisoba0728/PgBase/releases/tag/v0.3.1)과 `latest` 이미지 digest는 `sha256:07bbcff43b2644069acb3a1ae10d854ff2eb4ddcea1501243b104ee53d5bc7b6`. GHCR private 유지. 운영은 이 이미지의 pull이 아닌 같은 source의 Dokploy build다(`dev` 버전 표시). 실패한 v0.3.0 candidate는 promotion하지 않았고 immutable 태그를 보존했다.
- schema 65→66 이후 이전 binary만 재배포하는 롤백은 지원하지 않는다. 외부 복구 사본은 미확보이며 운영 백업 제외 지시는 유지된다. 최종 문서 PR은 제품 코드·migration·UI 자산을 바꾸지 않아 위 배포 source와 런타임이 같다.
