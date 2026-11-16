import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

final _sessionChanged = throwsA(
  isA<PgBaseError>()
      .having((error) => error.status, 'status', 403)
      .having((error) => error.code, 'code', 'session_changed'),
);

void main() {
  for (final boundary in ['buffered records', 'next page']) {
    for (final transition in ['replacement', 'logout']) {
      test('record iteration rejects $boundary after $transition', () async {
        final auth = AuthStore(initialToken: 'old-user');
        final requests = <http.Request>[];
        final client = PgBaseClient(
          Uri.parse('http://pgbase.test'),
          auth: auth,
          httpClient: MockClient((request) async {
            requests.add(request);
            return http.Response(
              jsonEncode({
                'items': boundary == 'buffered records'
                    ? [
                        {'id': 'private-first'},
                        {'id': 'private-buffered'},
                      ]
                    : [
                        {'id': 'private-first'},
                      ],
                'nextCursor': 'old-user-cursor',
              }),
              200,
            );
          }),
        );
        final iterator = StreamIterator(client.records('notes').iterate());
        try {
          expect(await iterator.moveNext(), isTrue);
          expect(iterator.current['id']!.toDart(), 'private-first');
          if (transition == 'logout') {
            await auth.clear();
          } else {
            await auth.save('new-user');
          }
          await expectLater(iterator.moveNext(), _sessionChanged);
          expect(
            requests,
            hasLength(1),
            reason:
                'an old cursor must never be submitted under another session',
          );
          expect(requests.single.headers['authorization'], 'Bearer old-user');
          expect(auth.token, transition == 'logout' ? null : 'new-user');
        } finally {
          await iterator.cancel();
          client.close();
          await auth.close();
        }
      });
    }
  }

  for (final kind in ['json', 'record', 'multipart', 'upload', 'download']) {
    for (final outcome in ['success', 'HTTP error', 'transport error']) {
      test(
        'user $kind rejects an old $outcome body after shared auth changes',
        () async {
          final transport = DelayedUserClient(
            outcome == 'HTTP error' ? 401 : 200,
          );
          final auth = AuthStore(initialToken: 'old-user');
          final client = PgBaseClient(
            Uri.parse('http://pgbase.test'),
            httpClient: transport,
            auth: auth,
          );
          final other = PgBaseClient(
            Uri.parse('http://pgbase.test'),
            auth: auth,
          );
          final source = UploadSource(
            open: () => Stream.value(utf8.encode('private')),
            length: 7,
            filename: 'private.txt',
          );
          final Future<Object?> pending;
          switch (kind) {
            case 'record':
              pending = client.records('notes').get('one');
            case 'multipart':
              pending = client.batchMultipart(JsonObject({}), {
                'attachment': source,
              });
            case 'upload':
              pending = client
                  .files('notes', 'one')
                  .upload('attachment', source);
            case 'download':
              pending = client
                  .files('notes', 'one')
                  .download('file', 'download-token');
            default:
              pending = client.request('GET', '/private');
          }
          final rejected = expectLater(pending, _sessionChanged);
          await transport.started.future;
          await other.auth.save('new-user');
          if (outcome == 'transport error') {
            transport.body.addError(
              http.ClientException('old connection lost'),
            );
          } else {
            transport.body.add(
              utf8.encode(
                outcome == 'success'
                    ? '{"id":"old-user-private","field":"attachment","filename":"private.txt","contentType":"text/plain","size":7,"thumbnail":false}'
                    : '{"error":{"code":"unauthorized"}}',
              ),
            );
          }
          await transport.body.close();
          await rejected;
          expect(transport.sentToken, 'Bearer old-user');
          expect(auth.token, 'new-user');
          client.close();
          other.close();
          await auth.close();
        },
      );
    }
  }

  test(
    'file preparation cannot submit under a replacement user token',
    () async {
      final started = Completer<void>();
      final bytes = StreamController<List<int>>(onListen: started.complete);
      var calls = 0;
      var opens = 0;
      final client = PgBaseClient(
        Uri.parse('http://pgbase.test'),
        auth: AuthStore(initialToken: 'old-user'),
        httpClient: MockClient((_) async {
          calls++;
          return http.Response(
            '{"id":"file","field":"attachment","filename":"private.txt","contentType":"text/plain","size":7,"thumbnail":false}',
            200,
          );
        }),
      );
      final rejected = expectLater(
        client
            .files('notes', 'one')
            .upload(
              'attachment',
              UploadSource(
                open: () => opens++ == 0
                    ? bytes.stream
                    : Stream.value(utf8.encode('private')),
                length: 7,
                filename: 'private.txt',
              ),
            ),
        _sessionChanged,
      );
      await started.future;
      await client.auth.save('new-user');
      bytes.add(utf8.encode('private'));
      await bytes.close();
      await rejected;
      expect(calls, 0);
      client.close();
      await client.auth.close();
    },
  );

  for (final realm in [false, true]) {
    test(
      'stale login cannot return its obsolete session: realm=$realm',
      () async {
        final started = Completer<void>();
        final response = Completer<http.Response>();
        final client = PgBaseClient(
          Uri.parse('http://pgbase.test'),
          httpClient: MockClient((_) {
            started.complete();
            return response.future;
          }),
        );
        final pending = realm
            ? client.realm('members').login('old@example.test', 'password')
            : client.login('old@example.test', 'password');
        final rejected = expectLater(pending, _sessionChanged);
        await started.future;
        await client.auth.save('new-user');
        response.complete(
          http.Response(
            '{"token":"old-token","user":{"id":"old-user","email":"old@example.test","createdAt":"then"},"expiresAt":"later"}',
            200,
          ),
        );
        await rejected;
        expect(client.auth.token, 'new-user');
        client.close();
        await client.auth.close();
      },
    );
  }
}

final class DelayedUserClient extends http.BaseClient {
  DelayedUserClient(this.status) {
    body.onListen = started.complete;
  }
  final int status;
  final started = Completer<void>();
  final body = StreamController<List<int>>();
  String? sentToken;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    sentToken = request.headers['authorization'];
    await request.finalize().drain<void>();
    return http.StreamedResponse(body.stream, status);
  }
}
