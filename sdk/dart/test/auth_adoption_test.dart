import 'dart:async';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

final _changed = throwsA(
  isA<PgBaseError>()
      .having((error) => error.status, 'status', 403)
      .having((error) => error.code, 'code', 'session_changed'),
);

final _completions = <String, Future<UserSession> Function(PgBaseClient)>{
  'public login': (client) => client.login('a@example.test', 'password'),
  'public registration': (client) =>
      client.register('a@example.test', 'password'),
  'realm login': (client) =>
      client.realm('members').login('a@example.test', 'password'),
  'realm registration': (client) =>
      client.realm('members').register('a@example.test', 'password'),
  'public OTP': (client) => client.completeOtp('otp', '123456'),
  'realm OTP': (client) => client.realm('members').completeOtp('otp', '123456'),
  'MFA': (client) => client.completeMfa('challenge', '123456'),
  'OAuth': (client) => client.oauthComplete('provider', 'state', 'code'),
  'refresh': (client) => client.realm('members').refresh(),
};

void main() {
  for (final operation in _completions.entries) {
    for (final failStorage in [false, true]) {
      test(
        '${operation.key} rejects replacement during persistence (failure=$failStorage)',
        () async {
          final storage = DelayedStore(failStorage);
          final auth = AuthStore(storage: storage, initialToken: 'initial');
          final client = sessionClient(auth);
          final rejected = expectLater(operation.value(client), _changed);
          await storage.started.future;
          final replacement = auth.save('replacement');
          storage.release.complete();
          await Future.wait([rejected, replacement]);
          expect(auth.token, 'replacement');
          client.close();
          await auth.close();
        },
      );
    }
  }

  for (final name in ['public login', 'refresh']) {
    test('$name retains current self-adopted persistence errors', () async {
      final storage = DelayedStore(true);
      final auth = AuthStore(storage: storage, initialToken: 'initial');
      final client = sessionClient(auth);
      final rejected = expectLater(
        _completions[name]!(client),
        throwsA(same(storage.failure)),
      );
      await storage.started.future;
      storage.release.complete();
      await rejected;
      expect(auth.token, 'token-a');
      client.close();
      await auth.close();
    });
  }
}

PgBaseClient sessionClient(AuthStore auth) => PgBaseClient(
  Uri.parse('http://pgbase.test'),
  auth: auth,
  httpClient: MockClient(
    (_) async => http.Response(
      '{"token":"token-a","user":{"id":"a","email":"a@example.test","createdAt":"then"},"expiresAt":"later"}',
      200,
    ),
  ),
);

final class DelayedStore implements AuthStorage {
  DelayedStore(this.fail);
  final bool fail;
  final failure = StateError('old persistence failure');
  final started = Completer<void>();
  final release = Completer<void>();
  @override
  Future<String?> read() async => null;
  @override
  Future<void> clear() async {}
  @override
  Future<void> write(String token) async {
    if (token != 'token-a') return;
    started.complete();
    await release.future;
    if (fail) throw failure;
  }
}
