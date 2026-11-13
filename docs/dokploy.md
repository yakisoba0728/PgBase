# Dokploy 배포

이 구성은 **PgBase 단일 인스턴스 + PostgreSQL 17 + 로컬 파일 저장소**를 배포한다.
Dokploy의 Docker Compose 모드를 사용한다. Swarm Stack 모드는 사용하지 않는다.
DB와 파일은 각각 named volume에 보관하며 호스트 포트는 공개하지 않는다.
HTTPS는 Dokploy/Traefik이 처리하고 PgBase는 내부 8090 포트로 HTTP를 제공한다.

Compose: [`deployments/dokploy/compose.yml`](../deployments/dokploy/compose.yml).
환경 예시: [`deployments/dokploy/.env.example`](../deployments/dokploy/.env.example).

## 1. 서버와 저장소

1. Docker Compose를 지원하는 Dokploy 서버와 사용할 도메인을 준비한다.
   DNS A/AAAA 레코드는 실제 Dokploy ingress 서버를 가리켜야 한다.
2. Dokploy 프로젝트에서 **Docker Compose** 서비스를 만든다.
3. Git provider에 이 저장소와 **이번 수정이 포함된 커밋/브랜치**를 연결한다.
   로컬 변경은 push 전까지 서버에서 사용할 수 없다. 이전 `v0.2.3` 이미지에는 이번 보안 수정이 없다.
4. Compose Path를 `deployments/dokploy/compose.yml`로 지정한다.
   저장소 전체를 checkout해야 한다. 이 파일만 Raw Compose에 붙여 넣으면 Dockerfile과 소스를 찾지 못한다.
5. 첫 배포는 자동 배포를 끈 상태에서 진행한다. 이 구성은 빌드 시 Node·Go 이미지를 내려받고 UI와 서버를 빌드한다.
   서버 메모리가 부족하면 별도 빌드 서버를 사용한다.

## 2. 환경 변수

아래 명령을 **각각 따로** 실행하고 출력을 Dokploy의 Environment 탭에 입력한다.
실제 비밀값이 든 `.env`를 Git에 추가하지 않는다.

```sh
openssl rand -hex 32       # PGBASE_POSTGRES_PASSWORD
openssl rand -hex 32       # PGBASE_SETUP_TOKEN
openssl rand -base64 32    # PGBASE_TOKEN_SECRET
openssl rand -base64 32    # PGBASE_RUNTIME_SETTINGS_ENCRYPTION_KEY
```

| 변수 | 설정 |
| --- | --- |
| `PGBASE_POSTGRES_PASSWORD` | 위에서 생성한 hex 비밀번호. DB URL에 삽입되므로 hex를 사용한다 |
| `PGBASE_SETUP_TOKEN` | 최초 관리자 생성에 사용할 별도 토큰 |
| `PGBASE_TOKEN_SECRET` | 세션·커서 등에 사용하는 base64 비밀값 |
| `PGBASE_RUNTIME_SETTINGS_ENCRYPTION_KEY` | 저장된 운영 설정을 암호화하는 별도 base64 키 |
| `PGBASE_PUBLIC_URL` | 예: `https://pgbase.example.com`. 경로·마지막 `/`·query 없이 실제 외부 origin 지정 |
| `PGBASE_TRUSTED_PROXIES` | 실제 Traefik 네트워크의 CIDR. 여러 개면 쉼표로 구분 |

Dokploy 서버에서 프록시 네트워크를 확인한다.

```sh
docker network inspect dokploy-network --format '{{range .IPAM.Config}}{{println .Subnet}}{{end}}'
```

기본 네트워크 이름을 변경했다면 Compose의 외부 네트워크 이름도 실제 이름으로 바꾼다.
`0.0.0.0/0` 또는 `::/0`를 trusted proxy로 지정하지 않는다.
해당 프록시 네트워크에 접근할 수 있는 다른 컨테이너도 신뢰 경계에 포함된다.
환경 변수는 Compose에서 명시적으로 전달하므로 별도 `env_file` 설정은 필요 없다.

DB 비밀번호와 두 base64 키는 재배포 때 그대로 보존하고 별도로 안전하게 백업한다.
기존 DB volume이 있는 상태에서 환경 변수의 DB 비밀번호만 바꾸면 DB 내부 비밀번호는 바뀌지 않는다.
암호화 키를 잃으면 저장된 운영 설정을 복호화할 수 없다.

## 3. 도메인과 첫 배포

Dokploy의 **Domains → Add Domain**에서 다음과 같이 설정하고 배포한다.

| 항목 | 값 |
| --- | --- |
| Service | `pgbase` |
| Host | `PGBASE_PUBLIC_URL`과 동일한 호스트 |
| Container Port | `8090` |
| Path / Internal Path | `/` |
| HTTPS | 활성화, 유효한 인증서 사용 |

Dokploy가 routing label을 주입한다. `postgres`, `migrate`, `files-init`에는 도메인을 연결하지 않는다.
도메인 설정을 바꾼 뒤에도 재배포해야 한다.

시작 순서는 다음과 같다.

1. PostgreSQL 준비 상태를 확인한다.
2. `files-init`가 파일 volume의 소유자를 앱의 nonroot uid `65532`로 설정한다.
3. `migrate`가 `pgbase migrate up`을 실행하고 성공 종료한다.
4. `pgbase`가 `PGBASE_AUTO_MIGRATE=false`로 시작한다.

`files-init`와 `migrate`의 **Exited (0)는 정상**이다. 앱과 DB는 실행 중이어야 한다.
마이그레이션이 실패하면 앱은 시작하지 않는다. 로그를 확인하고 원인을 해결한 뒤 다시 배포한다.

```sh
curl --fail https://pgbase.example.com/health/live
curl --fail https://pgbase.example.com/health/ready
```

두 요청의 HTTP 200을 확인하고 `https://pgbase.example.com/admin/`을 연다.
setup token을 입력해 첫 관리자를 만든다. 사용자 공개 가입은 기본적으로 꺼져 있다.
컬렉션·레코드 생성, 파일 업로드/다운로드, 로그아웃/로그인을 확인한 뒤
재배포하고 데이터와 업로드 파일이 그대로 남는지 확인한다.

## 4. 업데이트

이 구성은 유지보수 중단을 허용하는 단일 인스턴스용이다.
기존 writer가 실행 중인 채 새 DB migration을 적용하지 않는다.

1. 아래 절차로 앱을 멈추고 DB·파일·환경을 백업한다.
2. Dokploy에서 스택을 **Stop**한다. **Delete나 volume 제거는 하지 않는다.**
3. 배포할 커밋을 선택하고 Deploy한다. 새로운 이미지의 `migrate`가 먼저 성공해야 앱이 시작된다.
4. readiness·관리자 로그인·CRUD·파일 다운로드를 확인한 뒤 사용을 재개한다.

DB 스키마가 바뀐 릴리스에서 이전 이미지로만 되돌리는 것은 안전한 롤백이 아니다.
스키마 호환성을 확인하거나 배포 전 DB·파일·키 묶음을 함께 복원한다.
자세한 절차는 [업그레이드](upgrades.md)와 [백업·복원](backup-restore.md)을 따른다.

## 5. 백업과 복구

현재 distroless 앱 이미지에는 `pg_dump`/`pg_restore`가 없다.
이 프로필은 웹 관리형 백업을 활성화하지 않는다. 외부에서 백업을 수행한다.
실행 중인 PostgreSQL 데이터 volume을 단순 파일 복사한 결과를 일관된 DB 백업으로 취급하지 않는다.

Dokploy 서버의 checkout 루트에서, 실제 서비스의 **Compose project 이름과 Environment 파일 경로**를 사용한다.
다른 project 이름을 쓰면 기존 데이터 대신 새 volume이 생길 수 있다.

```sh
export PGBASE_PROJECT='실제-Dokploy-Compose-project-이름'
export PGBASE_ENV_FILE='/실제/Dokploy/환경파일/경로/.env'
pgcompose() {
  docker compose --env-file "$PGBASE_ENV_FILE" -p "$PGBASE_PROJECT" \
    -f deployments/dokploy/compose.yml "$@"
}
```

Dokploy의 실제 실행 설정/컨테이너 label(`com.docker.compose.project`)에서 이름을 확인한다.
아래 파일은 서버의 안전한 백업 디렉터리에 기록하고 서버 밖에도 보관한다.

```sh
umask 077
pgcompose stop pgbase
pgcompose exec -T postgres pg_dump -U pgbase -d pgbase \
  --format=custom --no-owner --no-acl \
  --schema=pgbase_meta --schema=app --schema=pgbase_relation --extension='*' > database.dump
pgcompose run --rm --no-deps -T --entrypoint /bin/tar files-init -C /files -czf - . > files.tar.gz
```

각 명령의 성공과 백업 파일을 확인한다. 두 명령을 수행하는 동안 앱을 계속 정지해 둔다.
대상 커밋 SHA·Compose·환경 비밀값도 함께 보관한다. 백업만 수행했다면 `pgcompose start pgbase`로 재개한다.

복구는 앱을 정지한 상태에서 같은 PostgreSQL major의 **비어 있는 새 DB와 새 파일 volume**에 수행한다.

```sh
pgcompose exec -T postgres pg_restore -U pgbase -d pgbase \
  --exit-on-error --no-owner --no-acl < database.dump
pgcompose run --rm --no-deps -T --entrypoint /bin/sh files-init -ec \
  'tar -xzf - -C /files && chown -R 65532:65532 /files && chmod 700 /files' < files.tar.gz
```

원본 volume은 복구 검증이 끝날 때까지 보존한다. 원래 암호화 키와 token secret을 사용하고,
복원 DB에 맞는 소스 버전으로 시작한 뒤 readiness·레코드·파일 바이트를 확인한다.
백업 archive는 신뢰할 수 있는 자체 생성 파일만 사용한다.

## 범위와 참고

실제 메일·OAuth·S3 연동, 여러 서버 배포, 운영 부하 및 복구 목표 검증은 별도다.
Dokploy 서버의 자격 증명이나 도메인을 요구하는 실제 원격 배포는 이 문서 작성에 포함되지 않는다.

- [Dokploy Docker Compose](https://docs.dokploy.com/docs/core/docker-compose)
- [Dokploy Compose domains](https://docs.dokploy.com/docs/core/docker-compose/domains)
- [PgBase 운영 설정](deployment.md)
