import 'dart:convert';

import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

void main() {
  test('missing cookies preserve state; empty credentials clear identity', () async {
    final auth = AuthStore();
    addTearDown(auth.close);
    await auth.save('before');
    await auth.loadFromCookie('unrelated=value');
    expect(auth.token, 'before');
    for (final cookie in [
      'pgbase_auth=',
      'pgbase_auth=%invalid',
      for (final token in [null, ''])
        'pgbase_auth=${Uri.encodeComponent(jsonEncode({
          'token': token,
          'record': {
            'id': 'u',
            'email': 'u@example.test',
            'createdAt': '2026-11-16T00:00:00Z',
          },
        }))}',
    ]) {
      await auth.save('before');
      await auth.loadFromCookie(cookie);
      expect(auth.token, isNull);
      expect(auth.record, isNull);
      expect(auth.isValid, isFalse);
    }
  });
}
