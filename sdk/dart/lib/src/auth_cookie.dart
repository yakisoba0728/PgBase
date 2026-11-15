import 'dart:convert';

import 'errors.dart';

void validateAuthCookieName(String key) {
  if (key.isEmpty || RegExp(r"[^!#\$%&'*+.^_`|~0-9A-Za-z-]").hasMatch(key)) {
    throw const PgBaseProtocolError('invalid_cookie_option');
  }
}

String serializeAuthCookie(
  Map<String, Object?> state, {
  String key = 'pgbase_auth',
  String path = '/',
  bool httpOnly = true,
  bool secure = false,
  String sameSite = 'Lax',
}) {
  validateAuthCookieName(key);
  if (!path.startsWith('/') ||
      RegExp(r'[^\x20-\x3a\x3c-\x7e]').hasMatch(path) ||
      !const ['Lax', 'Strict', 'None'].contains(sameSite) ||
      (sameSite == 'None' && !secure)) {
    throw const PgBaseProtocolError('invalid_cookie_option');
  }
  final prefix = key.toLowerCase();
  final host = prefix.startsWith('__host-');
  final http = prefix.startsWith('__http-') || prefix.startsWith('__host-http-');
  if (((host || http || prefix.startsWith('__secure-')) && !secure) ||
      (host && path != '/') ||
      (http && !httpOnly)) {
    throw const PgBaseProtocolError('invalid_cookie_option');
  }
  final payload = Uri.encodeComponent(jsonEncode(state));
  return [
    '$key=$payload',
    'Path=$path',
    if (httpOnly) 'HttpOnly',
    if (secure) 'Secure',
    'SameSite=$sameSite',
  ].join('; ');
}
