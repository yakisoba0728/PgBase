import type { AuthMailTemplate, AuthSMTPSettings, OAuthProviderSettings } from "./ports";

// Path identifiers and configuration-status flags are response metadata, not
// writable fields. The server deliberately rejects unknown request members.
export function providerInput(value: OAuthProviderSettings) {
  return {
    enabled: value.enabled, authorizationUrl: value.authorizationUrl, tokenUrl: value.tokenUrl,
    userInfoUrl: value.userInfoUrl, issuer: value.issuer, jwksUrl: value.jwksUrl, clientId: value.clientId, clientSecret: value.clientSecret,
    scopes: value.scopes, redirectUris: value.redirectUris, subjectField: value.subjectField,
    emailField: value.emailField, emailVerifiedField: value.emailVerifiedField, avatarField: value.avatarField,
    allowInsecureLoopback: value.allowInsecureLoopback,
  };
}

export function templateInput(value: AuthMailTemplate) {
  return { subject: value.subject, body: value.body, htmlBody: value.htmlBody };
}

export function smtpInput(value: AuthSMTPSettings) {
  return {
    address: value.address, from: value.from, username: value.username, password: value.password,
    serverName: value.serverName, implicitTls: value.implicitTls, allowInsecureLoopback: value.allowInsecureLoopback,
  };
}
