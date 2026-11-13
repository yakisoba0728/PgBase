# 수정 후보 이미지 최종 검증

독립 검토 후 수정된 작업 트리로 production Dockerfile을 다시 빌드했다. 검증 중 소스 재빌드로 대체하지 않고 동일 digest를 로컬 레지스트리에서 가져와 E2E와 복원 검사를 실행했다. 레지스트리는 검증 후 제거했으며 외부 레지스트리에 게시하지 않았다.

- 로컬 후보: `pgbase-rewrite-candidate:local` (동일 이미지에 `pgbase-rewrite-candidate:independent` 태그도 유지)
- 이미지 digest/ID: `sha256:a1e185f58e6297d86be07b74b50b2728acfd4e8482908fcb79f54e5a76c2d039`
- 플랫폼: `linux/arm64`
- 검증용 버전: `v0.0.0-rc.1` (릴리스 게시를 의미하지 않음)
- 빌드 입력 manifest SHA256: `7b0d95a3822b13fd2f0f83e3726149b0e2ed340317009e85bcf80f615447191b`
- 이미지 내 실행 파일 SHA256: `13604f87d9d763c54ad4c968261ccf8a01123c64df94d59b0a4a9ea67800a5a1`

미커밋 수정과 새 파일을 포함한 빌드 입력을 동결했다. 실행 파일의 commit metadata에는 기존 HEAD 대신 `worktree-<빌드 입력 manifest SHA256>`을 넣어 수정되지 않은 과거 commit의 산출물로 오인하지 않도록 했다. 기존 HEAD도 별도 metadata에 기록한다. Dockerfile에 고정된 Node·Go·distroless 이미지와 lockfile을 그대로 사용했다.

| 검사 | 결과 |
| --- | --- |
| Production 이미지 빌드·version metadata | 통과, schema min/max 66·소스 manifest·버전·시각 일치 |
| Digest 고정 Docker E2E | 14개 top-level 테스트를 shuffle하여 2회 통과 |
| 동일 이미지의 백업·복원 | PostgreSQL 16.13/17.9/18.3 전체 통과 |
| 내장 UI | 실행 중 이미지가 제공하는 144개 파일이 현재 관리자 UI build와 byte 단위 일치, 보안 header 확인 |
| 런타임 이미지 설정 | nonroot·entrypoint·Linux/arm64 확인, shell 실행 불가 확인 |
| 이미지 취약점 gate | Trivy 0.70.0의 OS·Go binary 검사 HIGH/CRITICAL 0건, 예외·ignore-unfixed 사용 없음 |
| SBOM | SPDX 2.3 생성, package 57개 |
| 소스 대응·정리 | 빌드 입력 파일별 SHA256 재대조, 검증용 앱·DB·레지스트리·volume 제거 |

로그·이미지 archive·추출 실행 파일·SBOM·해시 metadata는 무시된 `.local/redesign/candidate-final/`에 보관한다. `candidate.json`이 소스·이미지·실행 파일·archive 해시와 최종 결과를 연결한다. `build-inputs.json`, `build.log`, `e2e.log`, `restore.log`, `embedded-ui.json`, `security-image.json`, `sbom.spdx.json`이 세부 근거다. `image.tar`를 `docker load`하면 `pgbase-rewrite-candidate:independent` 태그를 복구할 수 있다.

소스 수준 Go 전체 unit/race·영향 package integration/race·관리자 UI 184개 및 브라우저 18개 검증은 [독립 검토 결과](backend-independent-review.md)에 있다. 이번 이미지 검증에서는 변경되지 않은 소스 검사를 반복하지 않았다.

운영 배포·외부 레지스트리 게시·서명된 릴리스와 다른 CPU 아키텍처에서의 실행은 수행하지 않았다. 이 결과는 로컬 Linux/arm64 후보의 검증이다.
