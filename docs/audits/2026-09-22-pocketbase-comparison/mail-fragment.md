# PocketBase 메일·이메일 인증 계약 감사

기준일: 2026-09-22. PocketBase v0.40.4, commit `5cec579da984436a258602a46a96302fbd31f77c`를 로컬 clone의 `git rev-parse HEAD`로 확인했다. PgBase는 작업 중인 `remaining-development/PgBase` worktree의 현재 소스다. 이전 비교 JSON의 상태는 증거로 사용하지 않았다.

범위는 기존 MAIL-01..05와 AUTH-20..25, 추가 MAIL-06..07 및 AUTH-35..36의 총 15행이다. 이번 작업에서는 소스와 테스트를 읽었으며 테스트·서버·Docker를 실행하거나 재시작하지 않았다. `test-exists`는 관련 테스트 코드가 존재한다는 뜻이며 통과 판정이 아니다. `static-only`는 정적 비교뿐이고, 특히 AUTH-35는 실행 재현이 없는 의심으로 `unverified`를 유지한다. 프로덕션 코드는 수정하지 않았다.

상태 분포: partial 11, adapted 2, missing 1, unverified 1. equivalent 0. partial은 핵심 기능이 있어도 확인된 PB 계약 일부가 빠졌다는 뜻이며, adapted는 목적은 제공하지만 네이티브 인터페이스·흐름이 다른 경우다.

## 먼저 확인할 차이

- AUTH-35 (P1, **정적 의심/미재현**): verification token 발급 후 관리자 또는 허용된 manager가 비밀번호를 바꾸지 않고 email만 수정하는 경우, 발급 당시 주소를 다시 확인하거나 pending token을 폐기하는 경로가 보이지 않는다. 영향 확정 전에 이 정확한 순서의 회귀 테스트가 필요하다. [internal/userauth/email_actions.go:75](../../../internal/userauth/email_actions.go#L75), [internal/userauth/email_actions.go:364](../../../internal/userauth/email_actions.go#L364), [internal/userauth/auth_records.go:469](../../../internal/userauth/auth_records.go#L469); [PB apis/record_auth_verification_confirm.go#L89](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm.go#L89).
- AUTH-36 (P1, 정적 계약 차이): PB는 verified 승격에서 disabled-password-auth 비밀번호를 무작위화하고 기존 external auth 링크를 제거한다. PgBase confirm은 verified 상태만 변경한다. 계정 탈취나 우회가 재현됐다는 주장은 하지 않는다. [PB apis/record_auth_verification_confirm.go#L44](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm.go#L44), [PB core/external_auth_model.go#L141](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/external_auth_model.go#L141), [internal/userauth/email_actions.go:82](../../../internal/userauth/email_actions.go#L82).
- AUTH-20/21/22: PB의 password-auth-disabled reset 거절, reset 후 email 일치 시 verified 승격, public email-address verification request 계약이 각각 일치하지 않는다. [PB apis/record_auth_password_reset_request.go#L22](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_request.go#L22), [PB apis/record_auth_password_reset_confirm.go#L42](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_confirm.go#L42), [internal/api/email_actions.go:28](../../../internal/api/email_actions.go#L28), [internal/userauth/email_actions.go:93](../../../internal/userauth/email_actions.go#L93).
- MAIL-03/04/05: PB record/app/subject placeholders, 선택한 template의 test-email 전송, Record/Mailer/token 메타데이터가 있는 mail hook 계약을 네이티브 구현이 모두 제공하지는 않는다. 아래 행은 이름 차이와 실제 누락을 구분한다.
- MAIL-06/07: SMTP 지원만으로 sendmail fallback, Cc/Bcc/multiple recipients, arbitrary headers, inline attachments, HTML-only 메시지까지 지원한다고 볼 수 없다. [PB core/base.go#L653](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go#L653), [PB tools/mailer/mailer.go#L12](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/mailer.go#L12), [internal/userauth/auth_admin.go:472](../../../internal/userauth/auth_admin.go#L472), [internal/mailer/mailer.go:6](../../../internal/mailer/mailer.go#L6).
- 기존 보고서의 'delegated manageRule 없음', '사용자 origin 목록 없음'은 현재 코드와 맞지 않는다. 해당 기능은 존재하며 범위·세부 계약 차이를 AUTH-24/25에 반영했다. [internal/userauth/auth_records.go:144](../../../internal/userauth/auth_records.go#L144), [internal/api/auth_collections.go:77](../../../internal/api/auth_collections.go#L77).

## 행별 근거

### AUTH-20 — password reset request

- 판단: **partial**, 우선순위 P2, 검증 test-exists.
- PocketBase 계약: Public POST /api/collections/{collection}/request-password-reset accepts a validated email only when collection PasswordAuth.Enabled is true. Unknown accounts and recently sent requests receive 204; successful sends establish an in-memory two-minute resend suppression. Delivery runs in the background. [PB apis/record_auth_password_reset_request.go#L16](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_request.go#L16); [PB apis/record_auth_password_reset_request.go#L41](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_request.go#L41).
- PgBase 구현: Public default/named-realm request-password-reset endpoints normalize email, apply account/network buckets, issue a hashed opaque action token, attempt mail synchronously, and suppress unknown-account/delivery failure disclosure. [internal/api/email_actions.go:28](../../../internal/api/email_actions.go#L28); [internal/api/auth_collections.go:68](../../../internal/api/auth_collections.go#L68); [internal/userauth/email_actions.go:93](../../../internal/userauth/email_actions.go#L93); [internal/userauth/service.go:408](../../../internal/userauth/service.go#L408).
- 차이/한계: Collection path and error/throttling behavior differ. The reviewed PgBase request path has no collection PasswordEnabled check; unlike PB it can issue a reset while password authentication is disabled. Invalid email is nondisclosing success rather than PB validation error. PB's two-minute resend suppression and asynchronous delivery are absent from this path.
- 읽은 테스트: [internal/userauth/email_actions_integration_test.go:219](../../../internal/userauth/email_actions_integration_test.go#L219); [internal/userauth/email_actions_integration_test.go:287](../../../internal/userauth/email_actions_integration_test.go#L287); [pgbase/email_actions_integration_test.go:126](../../../pgbase/email_actions_integration_test.go#L126); [PB apis/record_auth_password_reset_request_test.go#L45](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_request_test.go#L45).

### AUTH-21 — password reset confirmation

- 판단: **partial**, 우선순위 P2, 검증 test-exists.
- PocketBase 계약: Public collection-scoped confirmation requires token, password and matching passwordConfirm; validates token purpose and collection, saves the new password, and marks an unverified record verified only if its current email still matches the token email claim. Saving password rotates the record token key. [PB apis/record_auth_password_reset_confirm.go#L39](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_confirm.go#L39); [PB apis/record_auth_password_reset_confirm.go#L65](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_confirm.go#L65); [PB core/record_model.go#L1443](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_model.go#L1443).
- PgBase 구현: Confirmation accepts token/newPassword, validates the realm password policy, consumes a purpose-bound, expiring, password-hash-bound database token once, updates password_hash and revokes sessions. [internal/api/email_actions.go:42](../../../internal/api/email_actions.go#L42); [internal/userauth/email_actions.go:137](../../../internal/userauth/email_actions.go#L137); [internal/userauth/email_actions.go:341](../../../internal/userauth/email_actions.go#L341).
- 차이/한계: PgBase does not set verified=true after a successful reset, so PB workflows that use password reset as email proof remain unverified. It has no passwordConfirm field or collection-scoped confirm URL, and opaque database tokens are not PB JWTs.
- 읽은 테스트: [internal/userauth/email_actions_integration_test.go:219](../../../internal/userauth/email_actions_integration_test.go#L219); [pgbase/email_actions_integration_test.go:129](../../../pgbase/email_actions_integration_test.go#L129); [PB apis/record_auth_password_reset_confirm_test.go#L102](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_confirm_test.go#L102).

### AUTH-22 — verification request/confirm

- 판단: **partial**, 우선순위 P1, 검증 test-exists.
- PocketBase 계약: Public collection-scoped request accepts email, validates it, conceals missing accounts with 204, suppresses repeat sends for two minutes and skips already-verified records; public confirmation requires a collection-bound verification token, checks its email claim, and returns 204. Superuser verification routes reject because superusers are already verified. [PB apis/record_auth_verification_request.go#L16](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_request.go#L16); [PB apis/record_auth_verification_request.go#L34](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_request.go#L34); [PB apis/record_auth_verification_confirm.go#L83](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm.go#L83).
- PgBase 구현: Request requires an authenticated non-impersonated user session; public confirmation consumes an opaque single-use token and sets that user verified. Already verified users do not receive another verification mail. Native action page supports explicit submit. [internal/api/email_actions.go:20](../../../internal/api/email_actions.go#L20); [internal/api/email_actions.go:54](../../../internal/api/email_actions.go#L54); [internal/userauth/email_actions.go:55](../../../internal/userauth/email_actions.go#L55).
- 차이/한계: No public email-address verification-request contract or collection-scoped confirmation path. Reusing a consumed token fails, while PB accepts a still-valid verification token for an already-verified record. Additional verification trust-transition and email-binding checks are isolated in AUTH-35/36 below; their PgBase consequences are static observations, not executed reproductions.
- 읽은 테스트: [internal/userauth/email_actions_integration_test.go:144](../../../internal/userauth/email_actions_integration_test.go#L144); [internal/userauth/email_actions_integration_test.go:177](../../../internal/userauth/email_actions_integration_test.go#L177); [pgbase/email_actions_integration_test.go:117](../../../pgbase/email_actions_integration_test.go#L117); [PB apis/record_auth_verification_confirm_test.go#L90](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm_test.go#L90).

### AUTH-23 — email change request/confirm

- 판단: **adapted**, 우선순위 P2, 검증 test-exists.
- PocketBase 계약: Authenticated record requests a unique different newEmail without a password; public confirmation requires token plus current password, verifies token collection and newEmail, then saves the new email and verified=true. Email changes rotate the record token key. [PB apis/record_auth_email_change_request.go#L22](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_email_change_request.go#L22); [PB apis/record_auth_email_change_request.go#L61](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_email_change_request.go#L61); [PB apis/record_auth_email_change_confirm.go#L63](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_email_change_confirm.go#L63); [PB core/record_model.go#L1451](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_model.go#L1451).
- PgBase 구현: Authenticated non-impersonated user supplies currentPassword and newEmail at request time; the new email receives a purpose-bound, password-hash-bound token. Public token-only confirmation updates email/verified and revokes sessions. [internal/api/email_actions.go:54](../../../internal/api/email_actions.go#L54); [internal/userauth/email_actions.go:173](../../../internal/userauth/email_actions.go#L173); [internal/userauth/email_actions.go:289](../../../internal/userauth/email_actions.go#L289).
- 차이/한계: Password proof is moved from confirmation to request, so PB request/confirmation payloads and user flow cannot be used unchanged. PgBase checks collisions with other users but does not reject requesting the user's same current email as PB does. Confirm endpoints and token formats differ.
- 읽은 테스트: [internal/userauth/email_actions_integration_test.go:258](../../../internal/userauth/email_actions_integration_test.go#L258); [pgbase/email_actions_integration_test.go:133](../../../pgbase/email_actions_integration_test.go#L133); [PB apis/record_auth_email_change_confirm_test.go#L12](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_email_change_confirm_test.go#L12).

### AUTH-24 — verified/active account management

- 판단: **partial**, 우선순위 P2, 검증 test-exists.
- PocketBase 계약: PB record CRUD restricts direct verified/email changes to superusers or callers allowed by a nonempty manageRule, in addition to normal record access. A matching manageRule is evaluated against any authenticated record; PB's built-in auth fields do not include a separate active boolean. [PB forms/record_upsert.go#L148](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/record_upsert.go#L148); [PB apis/record_crud.go#L729](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go#L729); [PB core/collection_model_auth_options.go#L108](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L108); [PB core/collection_model.go#L941](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model.go#L941).
- PgBase 구현: PgBase provides administrator verified/active/session-revoke operations and typed auth-record CRUD. Sensitive user-driven updates require both update rule and a nonempty manage rule evaluated with distinct actor/target; tests reject a manager from another realm. Native active=false revokes sessions. [internal/userauth/auth_admin.go:428](../../../internal/userauth/auth_admin.go#L428); [internal/userauth/lifecycle.go:51](../../../internal/userauth/lifecycle.go#L51); [internal/userauth/auth_records.go:144](../../../internal/userauth/auth_records.go#L144); [internal/userauth/managed_authorization.go:65](../../../internal/userauth/managed_authorization.go#L65).
- 차이/한계: The old report's claim that delegated manageRule was absent is obsolete. Current PgBase has it, but same-realm actor policy is narrower than PB's authenticated-record rule context; native active/session administration is an adaptation rather than a PB built-in active-field equivalent.
- 읽은 테스트: [internal/userauth/auth_record_model_integration_test.go:184](../../../internal/userauth/auth_record_model_integration_test.go#L184); [pgbase/user_management_integration_test.go:25](../../../pgbase/user_management_integration_test.go#L25).

### AUTH-25 — new-device auth alert/auth origins

- 판단: **partial**, 우선순위 P2, 검증 test-exists.
- PocketBase 계약: When collection AuthAlert.Enabled and a real auth method is present, PB fingerprints real IP plus truncated User-Agent, skips alert on first origin, alerts subsequent new origins, records or updates origin only after the helper succeeds, and keeps up to five origins. Alert delivery failure is logged without failing authentication. [PB apis/record_helpers.go#L121](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L121); [PB apis/record_helpers.go#L587](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go#L587); [PB core/collection_model_auth_options.go#L56](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L56).
- PgBase 구현: Successful auth flows record an HMAC fingerprint of network address, atomically deduplicate concurrent origins, update last-seen, and send best-effort mail when an auth_alert template exists. User auxiliary list/get/delete routes and administrator extension queries exist. [internal/userauth/auth_auxiliary.go:38](../../../internal/userauth/auth_auxiliary.go#L38); [internal/userauth/auth_auxiliary.go:55](../../../internal/userauth/auth_auxiliary.go#L55); [internal/userauth/email_actions.go:430](../../../internal/userauth/email_actions.go#L430); [internal/userauth/service.go:109](../../../internal/userauth/service.go#L109); [internal/api/auth_collections.go:77](../../../internal/api/auth_collections.go#L77).
- 차이/한계: Same-IP devices with different User-Agents are not distinguished. Template presence replaces PB's enabled flag/default template. The reviewed insertion path has no first-origin suppression or five-origin pruning, records an origin even when mail fails, and exposes a different auxiliary record/route schema. The old statement that users cannot list origins is obsolete.
- 읽은 테스트: [internal/userauth/email_actions_integration_test.go:63](../../../internal/userauth/email_actions_integration_test.go#L63); [internal/userauth/auth_record_model_integration_test.go:22](../../../internal/userauth/auth_record_model_integration_test.go#L22); [pgbase/extension_app_auth_exact_integration_test.go:23](../../../pgbase/extension_app_auth_exact_integration_test.go#L23).

### MAIL-01 — SMTP configuration

- 판단: **partial**, 우선순위 P2, 검증 test-exists.
- PocketBase 계약: Global SMTP settings expose enabled, host, port, username/password, TLS, AUTH PLAIN or LOGIN, and optional EHLO/HELO localName; sender display name/address and app metadata are global settings. SMTP-disabled fallback is assessed separately in MAIL-06. [PB core/settings_model.go#L356](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/settings_model.go#L356); [PB core/settings_model.go#L521](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/settings_model.go#L521); [PB tools/mailer/smtp.go#L64](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/smtp.go#L64).
- PgBase 구현: Process-wide SMTP options and per-auth-collection encrypted SMTP credentials support address/from, username/password, serverName, implicit TLS, timeout and explicitly local insecure development mode. Blank submitted passwords preserve existing stored secrets; API reads redact them. [pgbase/email_options.go:12](../../../pgbase/email_options.go#L12); [internal/userauth/auth_admin.go:390](../../../internal/userauth/auth_admin.go#L390); [internal/userauth/auth_admin.go:472](../../../internal/userauth/auth_admin.go#L472); [internal/mailer/smtp.go:20](../../../internal/mailer/smtp.go#L20).
- 차이/한계: The transport implements smtp.PlainAuth only and exposes no AUTH LOGIN selector or custom EHLO localName. TLS policy is deliberately stricter off-loopback. Global settings JSON, sender display-name metadata and realm configuration are not interchangeable with PB.
- 읽은 테스트: [pgbase/email_actions_integration_test.go:151](../../../pgbase/email_actions_integration_test.go#L151); [internal/mailer/smtp_test.go:12](../../../internal/mailer/smtp_test.go#L12); [internal/mailer/smtp_test.go:28](../../../internal/mailer/smtp_test.go#L28).

### MAIL-02 — auth mail templates

- 판단: **adapted**, 우선순위 P2, 검증 test-exists.
- PocketBase 계약: Each auth collection stores five configurable HTML email templates: verification, password reset, email change, OTP and auth alert, with subject/body and built-in defaults. [PB core/collection_model_auth_options.go#L121](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L121); [PB core/collection_model_auth_options.go#L145](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L145); [PB core/collection_model_auth_templates.go#L13](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_templates.go#L13).
- PgBase 구현: Per-auth-collection mail templates accept verify/password_reset/email_change/otp/auth_alert, each with subject, required text body and optional HTML body. All five have active service send paths; HTML is wrapped and ordinary attachments can be supplied through hooks. [internal/userauth/auth_admin.go:95](../../../internal/userauth/auth_admin.go#L95); [internal/userauth/auth_admin.go:341](../../../internal/userauth/auth_admin.go#L341); [internal/userauth/email_actions.go:388](../../../internal/userauth/email_actions.go#L388); [internal/userauth/email_actions.go:437](../../../internal/userauth/email_actions.go#L437); [internal/userauth/email_otp.go:191](../../../internal/userauth/email_otp.go#L191).
- 차이/한계: PB template fields/default HTML/layout are not drop-in compatible with PgBase's purpose identifiers and required text/placeholder validation. No-template actions use simple native text fallbacks; auth alerts require an explicitly stored template. Placeholder capability is separately partial in MAIL-03.
- 읽은 테스트: [internal/userauth/auth_admin_integration_test.go:15](../../../internal/userauth/auth_admin_integration_test.go#L15); [internal/userauth/email_actions_integration_test.go:100](../../../internal/userauth/email_actions_integration_test.go#L100); [pgbase/email_actions_integration_test.go:133](../../../pgbase/email_actions_integration_test.go#L133).

### MAIL-03 — placeholders/action links

- 판단: **partial**, 우선순위 P2, 검증 test-exists.
- PocketBase 계약: PB resolves {APP_NAME}, {APP_URL}, {TOKEN}, {OTP}, {OTP_ID}, {ALERT_INFO} and nonhidden {RECORD:field} values in both subject and HTML body; string record values are escaped. Default action links target /_/#/auth/confirm-*/<token>. [PB mails/record.go#L251](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record.go#L251); [PB core/collection_model_auth_options.go#L257](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go#L257); [PB core/collection_model_auth_templates.go#L3](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_templates.go#L3).
- PgBase 구현: Templates interpolate native {{action_url}}, {{otp}}, {{otp_id}} or {{alert}} into body/HTML. Generated action links point to /auth/action#action=...&token=...; page clears the fragment and sends confirmation only on explicit submission. [internal/userauth/auth_admin.go:341](../../../internal/userauth/auth_admin.go#L341); [internal/userauth/auth_admin.go:369](../../../internal/userauth/auth_admin.go#L369); [internal/userauth/email_actions.go:393](../../../internal/userauth/email_actions.go#L393); [internal/userauth/email_otp.go:191](../../../internal/userauth/email_otp.go#L191); [internal/api/email_actions.go:88](../../../internal/api/email_actions.go#L88).
- 차이/한계: App metadata, arbitrary nonhidden record placeholders, raw token placeholder and all subject substitution are absent from reviewed rendering paths. Native placeholder/action-link/token syntax differs; copying a PB template does not produce an equivalent rendered message.
- 읽은 테스트: [internal/userauth/email_actions_integration_test.go:100](../../../internal/userauth/email_actions_integration_test.go#L100); [pgbase/email_actions_integration_test.go:133](../../../pgbase/email_actions_integration_test.go#L133); [PB mails/record_test.go#L48](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record_test.go#L48).

### MAIL-04 — template preview/SMTP test

- 판단: **partial**, 우선순위 P2, 검증 test-exists.
- PocketBase 계약: The settings test-email form takes recipient, a selected one of five auth templates, and optional auth collection (default _superusers), builds a synthetic record, and sends the chosen real rendered template through the normal mail lifecycle. [PB forms/test_email_send.go#L21](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/test_email_send.go#L21); [PB forms/test_email_send.go#L77](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/test_email_send.go#L77).
- PgBase 구현: Separate administrator endpoints preview a submitted native template using a supplied action URL/sample OTP/alert, and send a fixed 'PgBase SMTP test' message through the collection transport. [internal/api/auth_admin.go:50](../../../internal/api/auth_admin.go#L50); [internal/userauth/auth_admin.go:369](../../../internal/userauth/auth_admin.go#L369); [internal/userauth/auth_admin.go:416](../../../internal/userauth/auth_admin.go#L416).
- 차이/한계: SMTP test does not accept a template selector or exercise stored auth-template rendering/record placeholders. Preview and transport smoke test therefore do not implement PB's selected-template test-email contract. A preview-specific regression test was not located in the inspected tests.
- 읽은 테스트: [pgbase/email_actions_integration_test.go:151](../../../pgbase/email_actions_integration_test.go#L151); [internal/api/auth_admin_test.go:25](../../../internal/api/auth_admin_test.go#L25); [PB forms/test_email_send_test.go#L13](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/test_email_send_test.go#L13).

### MAIL-05 — auth mail hooks

- 판단: **partial**, 우선순위 P2, 검증 test-exists.
- PocketBase 계약: Each of five auth mails raises MailerRecordEvent with App, Record, replaceable Mailer, mutable Message and action metadata (token/newEmail, otpId/password, info); the generic OnMailerSend hook runs inside the selected client's Send call after the purpose event. [PB mails/record.go#L37](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record.go#L37); [PB mails/record.go#L75](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record.go#L75); [PB mails/record.go#L153](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record.go#L153); [PB mails/record.go#L229](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record.go#L229); [PB core/base.go#L673](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go#L673).
- PgBase 구현: Generic mail.send and purpose-specific hooks can veto or mutate delivery and receive paired outcomes; MailEvent provides Purpose, Message and Meta. OTP and alert services supply metadata, and the decorator wraps both configured SMTP and injected senders. [pgbase/lifecycle_hooks.go:190](../../../pgbase/lifecycle_hooks.go#L190); [pgbase/lifecycle_hooks.go:228](../../../pgbase/lifecycle_hooks.go#L228); [internal/userauth/email_actions.go:423](../../../internal/userauth/email_actions.go#L423); [internal/userauth/email_actions.go:455](../../../internal/userauth/email_actions.go#L455); [internal/userauth/email_otp.go:211](../../../internal/userauth/email_otp.go#L211); [internal/userauth/auth_admin.go:472](../../../internal/userauth/auth_admin.go#L472).
- 차이/한계: Purpose events do not expose PB Record or replaceable Mailer/App. Verification/reset/email-change sends do not populate token/newEmail Meta. Generic hook precedes purpose hooks, reversing PB order. These are behavioral hook-contract gaps beyond naming differences.
- 읽은 테스트: [pgbase/lifecycle_hooks_test.go:39](../../../pgbase/lifecycle_hooks_test.go#L39); [pgbase/extension_runtime_regression_test.go:149](../../../pgbase/extension_runtime_regression_test.go#L149); [internal/userauth/email_actions_integration_test.go:100](../../../internal/userauth/email_actions_integration_test.go#L100).

### MAIL-06 — automatic sendmail fallback when SMTP is disabled

- 판단: **missing**, 우선순위 P3, 검증 static-only.
- PocketBase 계약: App.NewMailClient returns the built-in Sendmail transport when global SMTP.Enabled is false; Sendmail locates and invokes the system sendmail executable. [PB core/base.go#L653](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go#L653); [PB tools/mailer/sendmail.go#L43](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/sendmail.go#L43).
- PgBase 구현: Mailer selection uses a per-realm SMTP configuration or an explicitly configured process sender; absence of both returns 'user email actions unavailable'. No built-in sendmail selection/transport was found in internal/mailer, email options or sender setup. [internal/userauth/auth_admin.go:472](../../../internal/userauth/auth_admin.go#L472); [pgbase/email_options.go:27](../../../pgbase/email_options.go#L27); [internal/mailer/mailer.go:26](../../../internal/mailer/mailer.go#L26).
- 차이/한계: Deployments relying on the host sendmail command require custom sender integration or SMTP configuration. This omitted transport feature cannot be inferred from generic SMTP support.
- 읽은 테스트: [PB core/base_test.go#L180](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base_test.go#L180).

### MAIL-07 — generic mail envelope, inline attachments and HTML-only messages

- 판단: **partial**, 우선순위 P2, 검증 test-exists.
- PocketBase 계약: PB Message supports per-message named From, multiple To/Cc/Bcc, arbitrary headers, regular and inline attachment readers, text and/or HTML; SMTP derives plain text from HTML when Text is empty. [PB tools/mailer/mailer.go#L12](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/mailer.go#L12); [PB tools/mailer/smtp.go#L93](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/smtp.go#L93).
- PgBase 구현: Native Message and script mail bridge support one recipient, subject, required text, optional HTML and bounded ordinary attachments; sender is transport-owned. SMTP encodes multipart alternative/mixed and limits total attachment bytes to 8 MiB. [internal/mailer/mailer.go:6](../../../internal/mailer/mailer.go#L6); [internal/mailer/smtp.go:62](../../../internal/mailer/smtp.go#L62); [internal/mailer/smtp.go:139](../../../internal/mailer/smtp.go#L139); [pgbase/extension_bridge_mail.go:12](../../../pgbase/extension_bridge_mail.go#L12).
- 차이/한계: No native Cc/Bcc/multiple recipients, per-message From, custom headers or inline attachment fields. HTML-only messages are rejected because Text is mandatory, instead of PB's HTML-to-text fallback. Ordinary attachment tests do not establish these missing envelope capabilities.
- 읽은 테스트: [internal/mailer/smtp_test.go:55](../../../internal/mailer/smtp_test.go#L55); [internal/mailer/smtp_test.go:76](../../../internal/mailer/smtp_test.go#L76); [pgbase/javascript_file_mail_integration_test.go:22](../../../pgbase/javascript_file_mail_integration_test.go#L22).

### AUTH-35 — verification token invalidation after an email-only account update

- 판단: **unverified**, 우선순위 P1, 검증 static-only.
- PocketBase 계약: PB verification confirmation rejects a token whose email claim no longer matches the record; record saves also rotate tokenKey on email changes, invalidating existing signed record tokens. [PB apis/record_auth_verification_confirm.go#L89](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm.go#L89); [PB core/record_model.go#L1451](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_model.go#L1451).
- PgBase 구현: Verification token issuance stores no email payload; confirmation matches active user, purpose, expiry, unused state and a digest of current password hash, then sets verified. Email-only typed auth-record updates delete sessions but the reviewed path does not invalidate pending email-action rows. [internal/userauth/email_actions.go:75](../../../internal/userauth/email_actions.go#L75); [internal/userauth/email_actions.go:82](../../../internal/userauth/email_actions.go#L82); [internal/userauth/email_actions.go:364](../../../internal/userauth/email_actions.go#L364); [internal/userauth/auth_records.go:469](../../../internal/userauth/auth_records.go#L469); [migrations/system/000012_user_email_actions.sql:4](../../../migrations/system/000012_user_email_actions.sql#L4).
- 차이/한계: Static concern, not an executed reproduction or confirmed vulnerability: after issuing a still-valid verification token, an administrator/authorized manager changes only the email without changing the password; old-token confirmation appears able to verify the new address because no original-email comparison/invalidation is visible in the reviewed path. No regression test for that exact sequence was located. Reproduce under controlled fixtures before treating impact as established.
- 읽은 테스트: [internal/userauth/email_actions_integration_test.go:144](../../../internal/userauth/email_actions_integration_test.go#L144); [internal/userauth/email_actions_integration_test.go:177](../../../internal/userauth/email_actions_integration_test.go#L177).

### AUTH-36 — verification trust-transition cleanup of old password and OAuth links

- 판단: **partial**, 우선순위 P1, 검증 static-only.
- PocketBase 계약: When verification changes an existing record from unverified to verified, PB randomizes the password if password auth is disabled. Its verified-upgrade model hook deletes pre-existing external auth links transactionally and rotates tokenKey when links existed. [PB apis/record_auth_verification_confirm.go#L44](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm.go#L44); [PB core/external_auth_model.go#L141](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/external_auth_model.go#L141).
- PgBase 구현: ConfirmVerification updates only verified and updated_at inside email-token consumption. No password randomization, pre-existing OAuth identity cleanup or session revocation is visible in that operation; the existing successful verification test intentionally retains the session. [internal/userauth/email_actions.go:82](../../../internal/userauth/email_actions.go#L82); [internal/userauth/email_actions.go:341](../../../internal/userauth/email_actions.go#L341); [internal/userauth/auth_admin.go:428](../../../internal/userauth/auth_admin.go#L428).
- 차이/한계: The reviewed verification transition does not implement PB's password and pre-existing external-auth cleanup. This is a static contract difference; exploitability or resulting account compromise has not been reproduced. PgBase tests cover verified state and single-use token handling, not these cleanup conditions.
- 읽은 테스트: [PB apis/record_auth_verification_confirm_test.go#L156](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm_test.go#L156); [internal/userauth/email_actions_integration_test.go:144](../../../internal/userauth/email_actions_integration_test.go#L144).

## 읽은 파일

이번 비교에서 직접 읽거나 관련 영역을 검색·확인한 파일 목록이다. 실행 검증 목록은 아니다. research 스킬의 독립 보조 에이전트 생성은 동시 thread 한도 때문에 거절되어 현재 에이전트가 이 범위를 직접 확인했다.

### PgBase

- `internal/api/auth_admin.go`
- `internal/api/auth_admin_test.go`
- `internal/api/auth_collections.go`
- `internal/api/email_actions.go`
- `internal/mailer/mailer.go`
- `internal/mailer/smtp.go`
- `internal/mailer/smtp_test.go`
- `internal/userauth/auth_admin.go`
- `internal/userauth/auth_admin_integration_test.go`
- `internal/userauth/auth_auxiliary.go`
- `internal/userauth/auth_record_model_integration_test.go`
- `internal/userauth/auth_records.go`
- `internal/userauth/email_actions.go`
- `internal/userauth/email_actions_integration_test.go`
- `internal/userauth/email_otp.go`
- `internal/userauth/lifecycle.go`
- `internal/userauth/managed_authorization.go`
- `internal/userauth/service.go`
- `migrations/system/000012_user_email_actions.sql`
- `pgbase/email_actions_integration_test.go`
- `pgbase/email_options.go`
- `pgbase/extension_app_auth_exact_integration_test.go`
- `pgbase/extension_bridge_mail.go`
- `pgbase/extension_runtime_regression_test.go`
- `pgbase/javascript_file_mail_integration_test.go`
- `pgbase/lifecycle_hooks.go`
- `pgbase/lifecycle_hooks_test.go`
- `pgbase/user_management_integration_test.go`

추가로 `internal/userauth/token_policy.go`의 action token 발급 및 `pgbase/auth_lifecycle_hooks.go`의 이메일 action wrapper를 확인했다.

### PocketBase (고정 commit)

- [apis/record_auth_email_change_confirm.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_email_change_confirm.go)
- [apis/record_auth_email_change_confirm_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_email_change_confirm_test.go)
- [apis/record_auth_email_change_request.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_email_change_request.go)
- [apis/record_auth_password_reset_confirm.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_confirm.go)
- [apis/record_auth_password_reset_confirm_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_confirm_test.go)
- [apis/record_auth_password_reset_request.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_request.go)
- [apis/record_auth_password_reset_request_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_password_reset_request_test.go)
- [apis/record_auth_verification_confirm.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm.go)
- [apis/record_auth_verification_confirm_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_confirm_test.go)
- [apis/record_auth_verification_request.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_auth_verification_request.go)
- [apis/record_crud.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_crud.go)
- [apis/record_helpers.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/apis/record_helpers.go)
- [core/base.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base.go)
- [core/base_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/base_test.go)
- [core/collection_model.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model.go)
- [core/collection_model_auth_options.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_options.go)
- [core/collection_model_auth_templates.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/collection_model_auth_templates.go)
- [core/external_auth_model.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/external_auth_model.go)
- [core/record_model.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/record_model.go)
- [core/settings_model.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/core/settings_model.go)
- [forms/record_upsert.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/record_upsert.go)
- [forms/test_email_send.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/test_email_send.go)
- [forms/test_email_send_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/forms/test_email_send_test.go)
- [mails/record.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record.go)
- [mails/record_test.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/mails/record_test.go)
- [tools/mailer/mailer.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/mailer.go)
- [tools/mailer/sendmail.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/sendmail.go)
- [tools/mailer/smtp.go](https://github.com/pocketbase/pocketbase/blob/5cec579da984436a258602a46a96302fbd31f77c/tools/mailer/smtp.go)

기계 병합용 전체 행: [mail-fragment.json](mail-fragment.json).
