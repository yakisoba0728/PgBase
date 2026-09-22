# PocketBase 인증·규칙·메일 1:1 비교

2026-09-22 기준, 공식 PocketBase v0.40.4의 commit `5cec579da984436a258602a46a96302fbd31f77c`와 PgBase `remaining-development/PgBase` 현재 worktree(HEAD `6993744a8f36db48ddfeaa8dae017656752d24c7` 및 미커밋 변경 포함)를 비교했다. PB 근거는 로컬 공식 clone에서 읽었으며 각 행은 변경되지 않는 GitHub commit 링크를 가진다.

기존 비교표의 auth 33개, rules 7개, mail 5개를 모두 다시 확인했다. 기존 45개 ID를 유지하고 AUTH-34..36, RULE-08..13, MAIL-06..07의 11개 누락 항목을 추가하여 총 56개다. 이전 표의 상태나 테스트 이름만으로 동일성을 판정하지 않았다. 상세 계약·구현·테스트 근거는 [auth-rules-mail.json](auth-rules-mail.json)에 있다.

결과는 **adapted 9, partial 45, missing 1, unverified 1**이다. 이번 범위에서 complete 1:1임을 입증한 equivalent 행은 없다. 이는 기능 전체가 없다는 뜻이 아니다. 기본 인증·세션·realm·메일·규칙 기능이 상당히 구현되어 있지만, PB의 세부 행동·범위·wire 계약까지 동일하다는 주장을 지지하지 않는다는 뜻이다.

검증은 소스와 관련 테스트 assertion 읽기다. **이번 비교에서는 테스트 실행·서버 실행·Docker 재시작·production 코드 수정이 없었다.** `test-exists` 49행은 관련 테스트가 읽힌 것이며 PB 전체 계약이 검증되었다는 뜻이 아니다. `static-only` 7행은 소스 비교다. 부모 작업에서 이미 통과한 PG17 schema/auth-policy race, UI 590, SDK 16, browser 6 결과는 이전 실행이며 이 56개 계약 전체의 새 실행 증거로 확장하지 않았다.

## 먼저 확인할 차이

1. **규칙의 문법 지원과 실제 요청 경로는 별개다.** request 변수를 쓰는 base rule은 일반 저장 validation에서 nil/빈 request 환경으로 검사된다. 넓은 conformance fixture가 DB에 직접 규칙을 넣으므로 실제 설정 API 경로를 입증하지 않는다. PgBase authRule는 원래 HTTP 호출자 대신 로그인 대상자를 principal로 넣는다. PB는 [원래 requestInfo로 authRule를 검사한 뒤](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L53) 응답용 auth를 바꾼다. RULE-03/05/09에 요청 metadata, scalar context와 auth projection 차이를 기록했다.

2. **규칙 저장 UI와 backend 계약이 맞지 않는 정적 경로가 있다.** UI save는 `{rules, revision}`을 보내지만 native backend는 strict decode로 rules만 받는다. preview는 다섯 action의 draft를 single-rule 입력에 전달한다. backend 자체의 relation/request preview는 구현되어 있으므로 “preview 없음”으로 판정하지 않았다. RULE-07은 실제 브라우저 재현 없이 현재 송수신 코드를 대조한 결과다.

3. **규칙 의미도 PB와 전부 같지 않다.** PB의 hidden-field stored rule, collection alias, NULL/빈문자열 동등성, 명시적 LIKE wildcard 위치, locked-rule HTTP 상태를 모두 같은 방식으로 구현한 것은 아니다. 예를 들어 PB의 [LIKE 처리](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/filter.go#L492)와 달리 PgBase는 양쪽에 percent를 붙여 prefix 필터가 substring처럼 될 수 있다. RULE-08/11/12/13은 별도 실행 비교 테스트가 필요한 계약을 구분한다.

4. **관리자·token 모델은 의도적으로 다르다.** PB의 [_superusers auth collection](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/migrations/1640988000_init.go#L291)과 [record tokenKey 기반 JWT](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_tokens.go#L48)를 PgBase는 별도 admins 및 DB session으로 대체한다. PgBase refresh는 같은 row의 token을 회전시켜 이전 token을 즉시 폐기한다. PB refresh는 기존 JWT를 개별 폐기하지 않고 static token은 그대로 반환한다. DB에서 record/key를 확인하는 PB의 검증을 “DB를 전혀 쓰지 않는 인증”으로 해석해서는 안 된다.

5. **MFA는 다른 사용자 기능이다.** PB는 [password/OAuth2/OTP 중 다른 두 방식](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L182)을 mfaId로 결합한다. PgBase는 활성 TOTP enrollment가 있을 때 TOTP/recovery challenge를 사용한다. MFAEnabled이지만 활성 factor가 없는 경우 challenge가 생기지 않는 것은 현재 PgBase의 명시적 등록-factor 정책이다. 이는 PB식 전체 사용자 강제 MFA와 같지 않으며, 무권한 우회가 실행 재현되었다는 주장은 아니다. 조건부 MFA도 PB filter 대신 단일 JSON scalar equality다.

6. **OAuth provider·mapping 계약은 일부만 맞는다.** metadata helper는 google/github/microsoft 세 starter 설정만 반환하며 PB의 [전체 provider displayName/logo inventory](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go#L217)가 아니다. 여러 provider profile normalizer가 존재하지만 이를 각 provider token exchange의 완전 지원으로 간주하지 않았다. PB의 기존 email 자동 연결, current-session OAuth link, createData 및 id/name/username/avatar custom field mapping이 동일하지 않다. mandatory issuer/JWKS/nonce와 RS256-only OIDC fallback은 제한된 안전한 지원이며 PB의 더 넓은 설정 범위와 구별했다.

7. **OAuth 신규 가입의 createRule/required-field 적용은 확인이 필요하다.** PB는 OAuth 신규 사용자를 [내부 record-create 요청](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_oauth2.go#L412)으로 만든다. PgBase는 registration flags를 검사한 뒤 users/identity를 직접 insert하고 authRule를 검사한다. AUTH-34는 `registrationEnabled=true`, 허용 authRule, 거부 createRule 또는 필수 custom field가 있는 조건에서 PB와 달라 보이는 **정적 경로**다. 해당 조합의 실행 재현·영향 확정은 하지 않았다.

8. **메일 검증은 credential 생명주기와 연결된다.** PB reset/OTP/verification에는 email 일치 확인과 verified 전환, 조건부 password 무작위화 및 기존 OAuth 링크 정리가 있다. PgBase의 단회 action-token 처리는 구현되어 있지만 모든 전환을 재현하지 않는다. 특히 AUTH-35는 “검증 token 발급 → 관리자/허용 manager가 password 변경 없이 email-only 수정 → 기존 token confirm” 순서에서 새 주소를 검증할 수 있어 보이는 **정적 의심이며 unverified**다. 확정 취약점이나 계정 탈취 재현으로 표현하지 않았다. AUTH-36도 cleanup 계약 차이와 미재현 영향을 분리했다.

9. **메일 전송 성공만으로 PB mail feature 전체가 충족되지는 않는다.** template의 app/record/subject placeholder, template별 test-email, Record/Mailer/token metadata와 hook 순서, sendmail fallback, Cc/Bcc/multiple recipients/custom headers/inline attachments/HTML-only 메시지를 별도로 확인했다. MAIL-06의 자동 sendmail fallback은 missing이며 다른 메일 핵심 기능은 존재하되 범위 차이가 있다.

## 판정 기준과 주의할 경계

- equivalent: 좁게 정의된 행의 PB 계약 전체와 관련 테스트 근거가 맞는다.
- adapted: 사용자 기능은 제공하되 wire/storage/algorithm을 의도적으로 바꾸었다. 다른 행에 기록한 추가 범위까지 동일하다는 뜻은 아니다.
- partial: 구현은 있지만 PB 행동·입력·범위 일부가 없다.
- missing: 해당 operation 또는 기본 동작이 없다.
- unverified: 정적 의심의 실행 결과를 확정하지 않았다.

서로 다른 위험을 중복 집계하지 않도록 AUTH-14는 연결 기능, AUTH-34는 신규 가입 validation, AUTH-17은 OTP 전환, AUTH-35/36은 verification token/전환 cleanup으로 나눴다. RULE-05는 authRule의 호출자/request 전달, RULE-06은 manager 허용 범위, AUTH-32는 email visibility projection을 다룬다. PgBase에는 익명 auth record 전용 public 경로가 실제 있으므로 그 기능을 누락으로 적지 않았다.

## 행별 상태


| ID | Feature | Status | Priority | Verification |
| --- | --- | --- | --- | --- |
| AUTH-01 | superuser/administrator | partial | P2 | test-exists |
| AUTH-02 | administrator MFA | partial | P2 | test-exists |
| AUTH-03 | named auth collection/realm | adapted | none | test-exists |
| AUTH-04 | arbitrary auth record fields | partial | P2 | test-exists |
| AUTH-05 | self registration | partial | P1 | test-exists |
| AUTH-06 | password identity fields | partial | P2 | test-exists |
| AUTH-07 | 비밀번호 최소 길이 정책 | partial | P2 | test-exists |
| AUTH-08 | password login | adapted | none | test-exists |
| AUTH-09 | session refresh | adapted | none | test-exists |
| AUTH-10 | logout and revoke | adapted | none | test-exists |
| AUTH-11 | auth methods | partial | P2 | test-exists |
| AUTH-12 | provider catalog/config | partial | P1 | test-exists |
| AUTH-13 | realm routing | partial | P2 | test-exists |
| AUTH-14 | signup and link | partial | P1 | test-exists |
| AUTH-15 | OAuth unlink | partial | P2 | test-exists |
| AUTH-16 | profile mapping/avatar | partial | P2 | test-exists |
| AUTH-17 | email OTP login | partial | P1 | test-exists |
| AUTH-18 | MFA challenge completion | partial | P1 | test-exists |
| AUTH-19 | MFA conditional policy | partial | P2 | test-exists |
| AUTH-20 | password reset request | partial | P2 | test-exists |
| AUTH-21 | password reset confirmation | partial | P2 | test-exists |
| AUTH-22 | verification request/confirm | partial | P1 | test-exists |
| AUTH-23 | email change request/confirm | adapted | P2 | test-exists |
| AUTH-24 | verified/active account management | partial | P2 | test-exists |
| AUTH-25 | new-device auth alert/auth origins | partial | P2 | test-exists |
| AUTH-26 | impersonation | partial | P2 | test-exists |
| AUTH-27 | OAuth provider inventory helper | partial | P2 | static-only |
| AUTH-28 | Apple client-secret helper | adapted | none | test-exists |
| AUTH-29 | PB auth auxiliary system record handling | partial | P2 | test-exists |
| AUTH-30 | 사용자 암호의 최대 길이·pattern·hash cost 옵션 | partial | P2 | test-exists |
| AUTH-31 | 목적별 token secret·duration 설정 | partial | P2 | test-exists |
| AUTH-32 | 인증 레코드 emailVisibility 공개 제어 | partial | P2 | test-exists |
| AUTH-33 | OIDC userInfo 없는 id_token 기반 로그인 | partial | P2 | test-exists |
| AUTH-34 | OAuth 신규 가입 createRule·required field 적용 | partial | P1 | static-only |
| AUTH-35 | verification token invalidation after an email-only account update | unverified | P1 | static-only |
| AUTH-36 | verification trust-transition cleanup of old password and OAuth links | partial | P1 | static-only |
| MAIL-01 | SMTP configuration | partial | P2 | test-exists |
| MAIL-02 | auth mail templates | adapted | P2 | test-exists |
| MAIL-03 | placeholders/action links | partial | P2 | test-exists |
| MAIL-04 | template preview/SMTP test | partial | P2 | test-exists |
| MAIL-05 | auth mail hooks | partial | P2 | test-exists |
| MAIL-06 | automatic sendmail fallback when SMTP is disabled | missing | P3 | static-only |
| MAIL-07 | generic mail envelope, inline attachments and HTML-only messages | partial | P2 | test-exists |
| RULE-01 | five CRUD actions | adapted | none | test-exists |
| RULE-02 | owner/authenticated/comparison | partial | P1 | test-exists |
| RULE-03 | PocketBase filter syntax/variables | partial | P1 | test-exists |
| RULE-04 | rule null/empty semantics | partial | P1 | test-exists |
| RULE-05 | authRule | partial | P1 | test-exists |
| RULE-06 | manageRule | partial | P1 | test-exists |
| RULE-07 | rule administration/preview | partial | P1 | test-exists |
| RULE-08 | rule denial HTTP status and result contract (omission) | partial | P1 | test-exists |
| RULE-09 | actual request environment and auth projection (omission) | partial | P1 | test-exists |
| RULE-10 | privileged subject rule bypass (omission) | adapted | none | static-only |
| RULE-11 | hidden fields in stored API rules (omission) | partial | P1 | static-only |
| RULE-12 | collection aliases and shared-row correlation (omission) | partial | P1 | test-exists |
| RULE-13 | predicate NULL/empty and explicit LIKE wildcard semantics (omission) | partial | P1 | test-exists |

## 읽은 코드와 테스트 범위

아래는 이번 조사에서 인용·검토한 파일 목록이다. 파일 전체의 모든 줄을 감사했다는 뜻은 아니며, 해당 계약의 함수·호출 경로와 테스트 assertion을 중심으로 읽었다. 세부 규칙 및 메일 조사 기록은 [rules-fragment.md](rules-fragment.md), [mail-fragment.md](mail-fragment.md)에 있다.

### PgBase 구현
- internal/accessrule/catalog_resolver.go
- internal/accessrule/pocketbase_resolver.go
- internal/accessrule/pocketbase.go
- internal/accessrule/rule.go
- internal/adminauth/email_otp.go
- internal/adminauth/mfa.go
- internal/adminauth/password.go
- internal/adminauth/service.go
- internal/api/auth_admin.go
- internal/api/auth_collections.go
- internal/api/auth_records.go
- internal/api/email_actions.go
- internal/api/record_handlers.go
- internal/api/rules.go
- internal/api/user_handlers.go
- internal/mailer/mailer.go
- internal/mailer/smtp.go
- internal/query/access_rule.go
- internal/query/filter.go
- internal/recordaccess/preview.go
- internal/recordaccess/rule_compile.go
- internal/recordaccess/rules.go
- internal/recordaccess/subject.go
- internal/recordops/access_context.go
- internal/userauth/apple_client_secret.go
- internal/userauth/auth_admin.go
- internal/userauth/auth_auxiliary.go
- internal/userauth/auth_methods.go
- internal/userauth/auth_record_model.go
- internal/userauth/auth_records.go
- internal/userauth/auth_rule_resolver.go
- internal/userauth/collections.go
- internal/userauth/email_actions.go
- internal/userauth/email_otp.go
- internal/userauth/impersonation.go
- internal/userauth/lifecycle.go
- internal/userauth/managed_authorization.go
- internal/userauth/mfa.go
- internal/userauth/oauth_avatar.go
- internal/userauth/oauth_http.go
- internal/userauth/oauth_oidc.go
- internal/userauth/oauth_profiles.go
- internal/userauth/oauth.go
- internal/userauth/service.go
- internal/userauth/session_refresh.go
- internal/userauth/token_policy.go
- migrations/system/000012_user_email_actions.sql
- pgbase/email_options.go
- pgbase/extension_bridge_mail.go
- pgbase/lifecycle_hooks.go
- ui/admin/src/backend/http-admin-ports.ts
- ui/admin/src/pages/record-rules-editor.tsx

### PgBase 테스트
- internal/accessrule/pocketbase_resolver_test.go
- internal/accessrule/pocketbase_test.go
- internal/adminauth/email_otp_integration_test.go
- internal/adminauth/mfa_integration_test.go
- internal/adminauth/password_test.go
- internal/adminauth/service_integration_test.go
- internal/api/auth_admin_test.go
- internal/api/auth_apple_client_secret_test.go
- internal/api/auth_collections_methods_test.go
- internal/mailer/smtp_test.go
- internal/query/access_rule_test.go
- internal/query/pocketbase_rule_conformance_test.go
- internal/recordaccess/pocketbase_conformance_integration_test.go
- internal/recordaccess/pocketbase_rule_integration_test.go
- internal/recordaccess/preview_regression_integration_test.go
- internal/recordaccess/rules_validation_test.go
- internal/schema/auth_record_id_document_integration_test.go
- internal/userauth/apple_client_secret_test.go
- internal/userauth/auth_admin_integration_test.go
- internal/userauth/auth_fix_regression_integration_test.go
- internal/userauth/auth_methods_integration_test.go
- internal/userauth/auth_record_model_integration_test.go
- internal/userauth/auth_rule_enforcement_integration_test.go
- internal/userauth/collections_integration_test.go
- internal/userauth/email_actions_integration_test.go
- internal/userauth/email_otp_integration_test.go
- internal/userauth/identity_policy_schema_integration_test.go
- internal/userauth/impersonation_integration_test.go
- internal/userauth/lifecycle_integration_test.go
- internal/userauth/mfa_integration_test.go
- internal/userauth/nullable_rules_integration_test.go
- internal/userauth/oauth_apple_integration_test.go
- internal/userauth/oauth_avatar_test.go
- internal/userauth/oauth_empty_email_integration_test.go
- internal/userauth/oauth_http_test.go
- internal/userauth/oauth_profiles_test.go
- internal/userauth/service_integration_test.go
- internal/userauth/text_record_identity_integration_test.go
- pgbase/email_actions_integration_test.go
- pgbase/extension_app_auth_exact_integration_test.go
- pgbase/extension_runtime_regression_test.go
- pgbase/javascript_file_mail_integration_test.go
- pgbase/lifecycle_hooks_test.go
- pgbase/record_rules_integration_test.go
- pgbase/user_management_integration_test.go
- ui/admin/src/pages/record-rules-editor.test.tsx

### Pinned PocketBase 구현·테스트
- [apis/collection.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/collection.go)
- [apis/record_auth_email_change_confirm_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_email_change_confirm_test.go)
- [apis/record_auth_email_change_confirm.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_email_change_confirm.go)
- [apis/record_auth_email_change_request.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_email_change_request.go)
- [apis/record_auth_impersonate.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_impersonate.go)
- [apis/record_auth_methods.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_methods.go)
- [apis/record_auth_otp_request.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_otp_request.go)
- [apis/record_auth_password_reset_confirm_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_confirm_test.go)
- [apis/record_auth_password_reset_confirm.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_confirm.go)
- [apis/record_auth_password_reset_request_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_request_test.go)
- [apis/record_auth_password_reset_request.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_request.go)
- [apis/record_auth_refresh_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_refresh_test.go)
- [apis/record_auth_refresh.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_refresh.go)
- [apis/record_auth_verification_confirm_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm_test.go)
- [apis/record_auth_verification_confirm.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm.go)
- [apis/record_auth_verification_request.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_request.go)
- [apis/record_auth_with_oauth2.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_oauth2.go)
- [apis/record_auth_with_otp.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_otp.go)
- [apis/record_auth_with_password_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_password_test.go)
- [apis/record_auth_with_password.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_with_password.go)
- [apis/record_auth.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth.go)
- [apis/record_crud_external_auth_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud_external_auth_test.go)
- [apis/record_crud.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go)
- [apis/record_helpers_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers_test.go)
- [apis/record_helpers.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go)
- [apis/settings.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/settings.go)
- [core/auth_origin_model.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/auth_origin_model.go)
- [core/base_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base_test.go)
- [core/base.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go)
- [core/collection_model_auth_options.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go)
- [core/collection_model_auth_templates.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_templates.go)
- [core/collection_model.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model.go)
- [core/collection_validate.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_validate.go)
- [core/event_request.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/event_request.go)
- [core/external_auth_model.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/external_auth_model.go)
- [core/field_password.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/field_password.go)
- [core/mfa_model.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/mfa_model.go)
- [core/otp_model.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/otp_model.go)
- [core/record_field_resolver_runner.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver_runner.go)
- [core/record_field_resolver.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_field_resolver.go)
- [core/record_model.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_model.go)
- [core/record_query.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_query.go)
- [core/record_tokens.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_tokens.go)
- [core/settings_model.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/settings_model.go)
- [forms/apple_client_secret_create.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/apple_client_secret_create.go)
- [forms/record_upsert.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/record_upsert.go)
- [forms/test_email_send_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/test_email_send_test.go)
- [forms/test_email_send.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/test_email_send.go)
- [mails/record_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record_test.go)
- [mails/record.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record.go)
- [migrations/1640988000_init.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/migrations/1640988000_init.go)
- [tools/auth/auth.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/auth/auth.go)
- [tools/auth/oidc.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/auth/oidc.go)
- [tools/mailer/mailer.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/mailer.go)
- [tools/mailer/sendmail.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/sendmail.go)
- [tools/mailer/smtp.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/smtp.go)
- [tools/search/filter.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/search/filter.go)

추가로 PB password/MFA identityField·case-sensitivity scenario와 refresh static-token assertion, PgBase provider profile 및 OIDC claim/signature, realm 격리, session 회전, typed auth CRUD/visibility, TOTP enrollment/recovery 및 impersonation actor-revoke assertion을 읽었다. 외부 OAuth provider에 대한 실접속 검증은 하지 않았다.
