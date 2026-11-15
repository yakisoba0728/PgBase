import 'dart:convert';

import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

void main() {
  AuthStore store() {
    final value = AuthStore();
    addTearDown(value.close);
    return value;
  }

  test('native cookie namespace and defaults round-trip opaque credentials', () async {
    final source = store();
    await source.save('opaque;token=한글');
    final cookie = source.exportToCookie();
    expect(cookie, startsWith('pgbase_auth='));
    expect(cookie, endsWith('; Path=/; HttpOnly; SameSite=Lax'));
    final restored = store();
    await restored.loadFromCookie(cookie);
    expect(restored.token, source.token);
  });

  test('legacy namespace requires an explicit key', () async {
    final source = store();
    await source.save('legacy');
    final cookie = source.exportToCookie(key: 'pb_auth');
    final target = store();
    await target.loadFromCookie(cookie);
    expect(target.token, isNull);
    await target.loadFromCookie(cookie, key: 'pb_auth');
    expect(target.token, 'legacy');
  });

  test('control characters and attribute injection are rejected', () async {
    final auth = store();
    for (final code in [...List.generate(32, (i) => i), 127]) {
      final control = String.fromCharCode(code);
      expect(() => auth.exportToCookie(key: 'name$control'), throwsA(isA<PgBaseProtocolError>()));
      expect(() => auth.exportToCookie(path: '/app$control'), throwsA(isA<PgBaseProtocolError>()));
      await expectLater(auth.loadFromCookie('', key: 'name$control'), throwsA(isA<PgBaseProtocolError>()));
    }
    for (final key in ['', 'a=b', 'a;b', 'a b', '한글']) {
      expect(() => auth.exportToCookie(key: key), throwsA(isA<PgBaseProtocolError>()));
    }
    for (final path in ['', 'relative', '/; Secure', '/한글']) {
      expect(() => auth.exportToCookie(path: path), throwsA(isA<PgBaseProtocolError>()));
    }
  });

  test('cross-site and protected-prefix policies match TypeScript', () {
    final auth = store();
    for (final sameSite in ['None', 'lax', 'Lax; Secure', 'Lax\r\nInjected: yes']) {
      expect(() => auth.exportToCookie(sameSite: sameSite), throwsA(isA<PgBaseProtocolError>()));
    }
    expect(auth.exportToCookie(secure: true, sameSite: 'None'), endsWith('HttpOnly; Secure; SameSite=None'));
    expect(auth.exportToCookie(httpOnly: false, path: '/app', sameSite: 'Strict'), endsWith('Path=/app; SameSite=Strict'));
    for (final key in ['__Secure-session', '__Host-session', '__Http-session', '__Host-Http-session']) {
      expect(() => auth.exportToCookie(key: key), throwsA(isA<PgBaseProtocolError>()));
      expect(auth.exportToCookie(key: key, secure: true), startsWith('$key='));
    }
    expect(() => auth.exportToCookie(key: '__Host-session', secure: true, path: '/app'), throwsA(isA<PgBaseProtocolError>()));
    expect(() => auth.exportToCookie(key: '__Http-session', secure: true, httpOnly: false), throwsA(isA<PgBaseProtocolError>()));
    expect(() => auth.exportToCookie(key: '__Host-Http-session', secure: true, httpOnly: false), throwsA(isA<PgBaseProtocolError>()));
  });

  test('malformed cached state clears the current identity', () async {
    final auth = store();
    await auth.save('before');
    await auth.loadFromCookie('pgbase_auth=%invalid');
    expect(auth.isValid, isFalse);
    final state = Uri.encodeComponent(jsonEncode({'token': null, 'record': {'id': 'u', 'email': 'u@example.test', 'createdAt': '2026-11-16T00:00:00Z'}}));
    await auth.loadFromCookie('pgbase_auth=$state');
    expect(auth.token, isNull);
    expect(auth.record, isNull);
  });
}
