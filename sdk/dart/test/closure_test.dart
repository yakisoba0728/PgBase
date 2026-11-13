import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

final class Frames extends http.BaseClient {
  final body = StreamController<List<int>>();
  List<dynamic> remote = [];
  int active = 0, maximum = 0;
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    if (request.method == 'POST') {
      active++;
      if (active > maximum) maximum = active;
      final topics =
          (jsonDecode(utf8.decode(await request.finalize().toBytes()))
                  as Map)['subscriptions']
              as List;
      await Future<void>.delayed(const Duration(milliseconds: 5));
      active--;
      if (topics.contains('invalid/topic/extra')) {
        return http.StreamedResponse(
          Stream.value(
            utf8.encode('{"error":{"code":"invalid_subscription"}}'),
          ),
          400,
        );
      }
      remote = topics;
      return http.StreamedResponse(const Stream.empty(), 204);
    }
    if (request is http.Abortable)
      request.abortTrigger?.then((_) => body.close());
    body.add(
      utf8.encode('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n'),
    );
    return http.StreamedResponse(body.stream, 200);
  }
}

final class BufferingClient extends http.BaseClient {
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    await request.finalize().toBytes();
    return http.StreamedResponse(const Stream.empty(), 204);
  }
}

final class PausedClient extends http.BaseClient {
  StreamSubscription<List<int>>? body;
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final done = Completer<void>();
    body = request.finalize().listen(
      (_) {},
      onDone: done.complete,
      onError: done.completeError,
    )..pause();
    await done.future;
    return http.StreamedResponse(const Stream.empty(), 204);
  }
}

void main() {
  for (final realmLogout in [false, true]) {
    test(
      'logout waits for replacement and sends it: realm=$realmLogout',
      () async {
        final response = Completer<http.Response>();
        String? logoutToken;
        final client = PgBaseClient(
          Uri.parse('http://pgbase.test'),
          auth: AuthStore(initialToken: 'old'),
          httpClient: MockClient((request) async {
            if (request.url.path.endsWith('/refresh')) return response.future;
            logoutToken = request.headers['authorization'];
            return http.Response('', 204);
          }),
        );
        final refresh = client.realm('members').refresh();
        final logout = realmLogout
            ? client.realm('members').logout()
            : client.logout();
        expect(logoutToken, isNull);
        response.complete(
          http.Response(
            '{"token":"replacement","expiresAt":"later","user":{"id":"one","email":"a@example.test","createdAt":"then"}}',
            200,
          ),
        );
        await Future.wait([refresh, logout]);
        expect(logoutToken, 'Bearer replacement');
        expect(client.auth.token, isNull);
        await client.auth.close();
      },
    );
    test(
      'new explicit login survives pending refresh/logout: realm=$realmLogout',
      () async {
        final response = Completer<http.Response>();
        var logouts = 0;
        final client = PgBaseClient(
          Uri.parse('http://pgbase.test'),
          auth: AuthStore(initialToken: 'old'),
          httpClient: MockClient((request) async {
            if (request.url.path.endsWith('/refresh')) return response.future;
            logouts++;
            return http.Response('', 204);
          }),
        );
        final refresh = client.realm('members').refresh();
        final logout = realmLogout
            ? client.realm('members').logout()
            : client.logout();
        await client.auth.save('explicit-new-login');
        response.complete(
          http.Response(
            '{"token":"replacement","expiresAt":"later","user":{"id":"one","email":"a@example.test","createdAt":"then"}}',
            200,
          ),
        );
        await Future.wait([refresh, logout]);
        expect(logouts, 0);
        expect(client.auth.token, 'explicit-new-login');
        await client.auth.close();
      },
    );
  }
  test('unsupported projections reject locally without token rotation and verification sends JSON', () async {
    final calls = <http.Request>[];
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test'),
      auth: AuthStore(initialToken: 'old'),
      httpClient: MockClient((request) async {
        calls.add(request);
        return http.Response('', 204);
      }),
    );
    await expectLater(
      client.realm('members').refresh(fields: 'user.id'),
      throwsA(isA<PgBaseProtocolError>()),
    );
    await expectLater(
      client.records('notes').events(fields: 'id,title').first,
      throwsA(isA<PgBaseProtocolError>()),
    );
    expect(calls, isEmpty);
    expect(client.auth.token, 'old');
    await client.requestVerification();
    expect(calls.single.body, '{}');
    expect(calls.single.headers['content-type'], contains('application/json'));
    await client.auth.close();
  });
  test('malformed optional auth fields use stable protocol errors', () async {
    for (final key in [
      'token',
      'emailVisibility',
      'authCollection',
      'authCollectionId',
      'impersonatingAdministratorId',
    ]) {
      final data = <String, Object?>{
        'token': 'replacement',
        'expiresAt': 'later',
        'user': <String, Object?>{
          'id': 'one',
          'email': 'a@example.test',
          'createdAt': 'then',
        },
      };
      if ([
        'emailVisibility',
        'authCollection',
        'authCollectionId',
      ].contains(key)) {
        (data['user']! as Map<String, Object?>)[key] = [];
      } else {
        data[key] = [];
      }
      final client = PgBaseClient(
        Uri.parse('http://pgbase.test'),
        auth: AuthStore(initialToken: 'old'),
        httpClient: MockClient(
          (_) async => http.Response(jsonEncode(data), 200),
        ),
      );
      await expectLater(
        client.realm('members').refresh(),
        throwsA(isA<PgBaseProtocolError>()),
      );
      expect(client.auth.token, 'old');
      await client.auth.close();
    }
  });

  test('shared sync serializes and rejected subscriptions roll back', () async {
    final transport = Frames();
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test'),
      httpClient: transport,
    );
    try {
      await client.realtime.subscribe('notes/*', (_) {});
      final invalid = client.realtime.subscribe('invalid/topic/extra', (_) {});
      await Future.wait([
        expectLater(invalid, throwsA(isA<PgBaseError>())),
        client.realtime.subscribe('notes/one', (_) {}),
        client.realtime.subscribe('notes/two', (_) {}),
      ]);
      expect(transport.maximum, 1);
      expect(transport.remote, ['notes/*', 'notes/one', 'notes/two']);
    } finally {
      await client.realtime.unsubscribe();
      await client.auth.close();
      client.close();
    }
  });
  test('raw upload reports refused connection promptly', () async {
    final transport = PgBaseTransport(Uri.parse('http://127.0.0.1:1'));
    try {
      await expectLater(
        transport
            .upload(
              'POST',
              '/upload',
              source: UploadSource(
                open: () => Stream.value([1]),
                length: 1,
                filename: 'x',
              ),
              contentType: 'application/gzip',
            )
            .timeout(const Duration(seconds: 2)),
        throwsA(isA<PgBaseNetworkError>()),
      );
    } finally {
      transport.close();
    }
  });
  test('raw upload respects downstream pause', () async {
    var produced = 0;
    Stream<List<int>> source() async* {
      for (var i = 0; i < 100; i++) {
        produced++;
        yield [1];
      }
    }

    final client = PausedClient();
    final transport = PgBaseTransport(
      Uri.parse('http://pgbase.test'),
      client: client,
    );
    final sending = transport.upload(
      'POST',
      '/upload',
      source: UploadSource(open: source, length: 100, filename: 'x'),
      contentType: 'application/gzip',
    );
    await Future<void>.delayed(const Duration(milliseconds: 20));
    expect(produced, lessThanOrEqualTo(1));
    client.body!.resume();
    await sending;
    expect(produced, 100);
  });
  test('raw upload cancels during browser style prebuffering', () async {
    final source = StreamController<List<int>>();
    final cancellation = CancellationToken();
    final transport = PgBaseTransport(
      Uri.parse('http://pgbase.test'),
      client: BufferingClient(),
    );
    final sending = transport.upload(
      'POST',
      '/upload',
      source: UploadSource(open: () => source.stream, length: 1, filename: 'x'),
      contentType: 'application/gzip',
      cancellation: cancellation,
    );
    final result = expectLater(sending, throwsA(isA<PgBaseCancellation>()));
    await Future<void>.delayed(Duration.zero);
    cancellation.cancel();
    await result.timeout(const Duration(seconds: 2));
    expect(source.hasListener, isFalse);
    await source.close();
  });
}
