import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

const session =
    '{"token":"opaque-session","user":{"id":"one","email":"a@example.test","createdAt":"now"},"expiresAt":"later"}';

void main() {
  test('default and realm registration carry exact profile values independently from projection', () async {
    final calls = <http.Request>[];
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test'),
      httpClient: MockClient((request) async {
        calls.add(request);
        return http.Response(session, 200);
      }),
    );
    final profile = JsonObject({
      'displayName': const JsonString('Ada'),
      'counter': JsonNumber('9007199254740993'),
    });
    await client.register(
      'a@example.test',
      'password',
      profile: profile,
      fields: 'id,email',
    );
    await client
        .realm('members')
        .register(
          'a@example.test',
          'password',
          profile: profile,
          fields: 'id,email',
        );
    for (final call in calls) {
      final body = decodeExactJson(utf8.encode(call.body)) as JsonObject;
      expect(
        ((body['fields'] as JsonObject)['counter'] as JsonNumber).source,
        '9007199254740993',
      );
      expect(call.url.queryParameters['fields'], 'id,email');
    }
    await client.register('a@example.test', 'password');
    expect((jsonDecode(calls.last.body) as Map).containsKey('fields'), isFalse);
    await client.realm('members').login('a@example.test', 'password');
    expect((jsonDecode(calls.last.body) as Map).containsKey('fields'), isFalse);
    expect(client.auth.token, 'opaque-session');
  });

  test('OAuth helpers send profile only on completion and preserve validation failures', () async {
    final calls = <http.Request>[];
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test'),
      httpClient: MockClient((request) async {
        calls.add(request);
        if (request.url.path.endsWith('/start'))
          return http.Response(
            '{"authorizationUrl":"https://provider.test?state=expected","expiresAt":"later"}',
            200,
          );
        final body = jsonDecode(request.body) as Map;
        if ((body['fields'] as Map?)?['displayName'] == 'invalid')
          return http.Response(
            '{"error":{"code":"validation_failed","details":{"displayName":["required"]}}}',
            400,
          );
        return http.Response(session, 200);
      }),
    );
    final profile = JsonObject({'displayName': const JsonString('Ada')});
    await client.oauthComplete(
      'github',
      'expected',
      'code',
      profile: profile,
      fields: 'id,email',
    );
    expect((jsonDecode(calls.last.body) as Map)['fields'], {
      'displayName': 'Ada',
    });
    expect(calls.last.url.queryParameters['fields'], 'id,email');
    await client
        .realm('members')
        .oauthCompleteFromUri(
          'github',
          Uri.parse('https://app.test/callback?state=expected&code=code'),
          profile: profile,
        );
    expect((jsonDecode(calls.last.body) as Map)['fields'], {
      'displayName': 'Ada',
    });
    var closed = false;
    final adapter = OAuthBrowserAdapter(
      open: (_) {},
      waitForCallback: (_) async =>
          Uri.parse('https://app.test/callback?state=expected&code=code'),
      close: () {
        closed = true;
      },
    );
    await client
        .realm('members')
        .oauthWithBrowser(
          'github',
          'https://app.test/callback',
          adapter,
          profile: profile,
        );
    expect(closed, isTrue);
    expect(
      (jsonDecode(calls[calls.length - 2].body) as Map).containsKey('fields'),
      isFalse,
    );
    expect((jsonDecode(calls.last.body) as Map)['fields'], {
      'displayName': 'Ada',
    });
    await expectLater(
      client.oauthComplete(
        'github',
        'expected',
        'code',
        profile: JsonObject({'displayName': const JsonString('invalid')}),
      ),
      throwsA(
        isA<PgBaseError>()
            .having((error) => error.code, 'code', 'validation_failed')
            .having((error) => error.details['displayName'], 'details', [
              'required',
            ]),
      ),
    );
    expect(client.auth.token, 'opaque-session');
  });
}
