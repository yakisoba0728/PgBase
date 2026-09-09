import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

void main() {
  group('exact JSON', () {
    test(
      'retains arbitrary JSON number spelling and rejects duplicate keys',
      () {
        final value = decodeExactJson(
          utf8.encode('{"n":900719925474099312345,"x":1.2300e-4}'),
        ) as JsonObject;
        expect((value['n'] as JsonNumber).source, '900719925474099312345');
        expect((value['x'] as JsonNumber).source, '1.2300e-4');
        expect(
          () => decodeExactJson(utf8.encode('{"x":1,"x":2}')),
          throwsA(isA<PgBaseProtocolError>()),
        );
      },
    );

    test('UUID and decimal are textual exact values', () {
      expect(
        PgBaseUuid('018f4ea0-2e31-7c5f-a8f7-3d850b3f6b3d').value,
        '018f4ea0-2e31-7c5f-a8f7-3d850b3f6b3d',
      );
      expect(
        ExactDecimal('99999999999999999999999.0001').value,
        '99999999999999999999999.0001',
      );
      expect(() => ExactDecimal('1e3'), throwsA(isA<PgBaseProtocolError>()));
    });
  });

  test(
    'auth, ETag and idempotency preserve the PgBase wire contract',
    () async {
      final client = _ScriptedClient();
      final pgbase = PgBaseClient(
        Uri.parse('http://pgbase.test/'),
        httpClient: client,
      );
      await pgbase.login('person@example.test', 'password');
      final created = await pgbase
          .records('notes')
          .createWithEtag(
            JsonObject({'title': const JsonString('one')}),
            'create-note-0001',
          );
      await pgbase
          .records('notes')
          .updateWithEtag(
            '01900000-0000-7000-8000-000000000001',
            JsonObject({'title': const JsonString('two')}),
            '"pgb1.created"',
          );
      final batch = await pgbase.batch([
        BatchOperation(
          JsonObject({
            'operation': const JsonString('create'),
            'collection': const JsonString('notes'),
            'input': JsonObject({'title': const JsonString('batched')}),
            'idempotencyKey': const JsonString('create-batch-0001'),
          }),
        ),
      ]);
      expect(pgbase.auth.token, 'user-token');
      expect(created.etag, '"pgb1.created"');
      expect(client.requests[1].headers['authorization'], 'Bearer user-token');
      expect(client.requests[1].headers['idempotency-key'], 'create-note-0001');
      expect(client.requests[2].headers['if-match'], '"pgb1.created"');
      expect(batch['results'], isA<JsonArray>());
      expect(
        () => pgbase.records('notes').create(JsonObject({}), 'short'),
        throwsA(isA<PgBaseProtocolError>()),
      );
    },
  );

  test('admin client keeps CSRF apart from bearer authentication', () async {
    final client = _ScriptedClient();
    final admin = PgBaseAdminClient(
      Uri.parse('http://pgbase.test/'),
      httpClient: client,
    );
    await admin.login('admin@example.test', 'password');
    await admin.configureJob('daily', '0 0 * * *', true);
    expect(client.requests.last.headers['x-pgbase-csrf'], 'csrf-token');
    expect(client.requests.last.headers.containsKey('authorization'), isFalse);
  });

  test(
    'file upload streams a multipart part with its verified digest',
    () async {
      final client = _ScriptedClient();
      final pgbase = PgBaseClient(
        Uri.parse('http://pgbase.test/'),
        httpClient: client,
        auth: AuthStore(initialToken: 'user-token'),
      );
      final source = UploadSource(
        open: () => Stream<List<int>>.value(utf8.encode('N7 file payload')),
        length: utf8.encode('N7 file payload').length,
        filename: 'n7.txt',
        contentType: 'text/plain',
      );
      final files = pgbase.files(
        'notes',
        '01900000-0000-7000-8000-000000000001',
      );
      final file = await files.upload('attachment', source);
      final request = client.requests.single;
      expect(file.filename, 'n7.txt');
      expect(request.headers['x-pgbase-file-size'], '15');
      expect(
        request.headers['x-pgbase-file-sha256'],
        'a9bb9324beb099ff2565a182f26b9db2d184b652b5a7cc664cb5fa8357d84190',
      );
      expect(utf8.decode(request.body), contains('N7 file payload'));
      expect(
        utf8.decode(await files.download(file.id, 'download-token')),
        'N7 file payload',
      );
    },
  );

  test(
    'SSE reconnects from an exact int64 cursor and de-duplicates event IDs',
    () async {
      final client = _ScriptedClient();
      final pgbase = PgBaseClient(
        Uri.parse('http://pgbase.test/'),
        httpClient: client,
        auth: AuthStore(initialToken: 'user-token'),
      );
      final events = pgbase
          .records('notes')
          .events(lastEventId: '9007199254740992', maxReconnects: 0);
      final event = await events.first;
      expect(event.deliveryId, '9007199254740993');
      expect(client.requests.last.headers['last-event-id'], '9007199254740992');
    },
  );

  test('auth generation aborts an open SSE subscription', () async {
    final client = _BlockingSseClient();
    final pgbase = PgBaseClient(
      Uri.parse('http://pgbase.test/'),
      httpClient: client,
      auth: AuthStore(initialToken: 'user-token'),
    );
    final subscription = pgbase.records('notes').events().listen((_) {});
    await client.started.future;
    await pgbase.auth.clear();
    await client.aborted.future.timeout(const Duration(seconds: 1));
    await subscription.asFuture<void>();
  });
}

final class _CapturedRequest {
  const _CapturedRequest(this.method, this.path, this.headers, this.body);
  final String method;
  final String path;
  final Map<String, String> headers;
  final List<int> body;
}

final class _ScriptedClient extends http.BaseClient {
  final List<_CapturedRequest> requests = [];

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final body = await request.finalize().toBytes();
    requests.add(
      _CapturedRequest(
        request.method,
        request.url.path,
        Map.unmodifiable(request.headers),
        body,
      ),
    );
    final path = request.url.path;
    if (path == '/api/v1/auth/login')
      return _json(200, {
        'token': 'user-token',
        'user': {
          'id': '01900000-0000-7000-8000-000000000001',
          'email': 'person@example.test',
          'createdAt': '2026-01-01T00:00:00Z',
        },
        'expiresAt': '2026-01-02T00:00:00Z',
      });
    if (path == '/api/admin/auth/login')
      return _json(200, {
        'csrfToken': 'csrf-token',
        'administrator': {'id': 'admin'},
        'expiresAt': '2026-01-02T00:00:00Z',
      });
    if (path.endsWith('/records') && request.method == 'POST')
      return _json(
        201,
        {'id': '01900000-0000-7000-8000-000000000002', 'title': 'one'},
        headers: {'etag': '"pgb1.created"'},
      );
    if (path.contains('/records/') && request.method == 'PATCH')
      return _json(
        200,
        {'id': '01900000-0000-7000-8000-000000000001', 'title': 'two'},
        headers: {'etag': '"pgb1.updated"'},
      );
    if (path == '/api/v1/user/collections/batch')
      return _json(200, {'results': []});
    if (path.contains('/files/attachment') && request.method == 'POST')
      return _json(201, {
        'id': '01900000-0000-7000-8000-000000000004',
        'field': 'attachment',
        'filename': 'n7.txt',
        'contentType': 'text/plain',
        'size': 15,
        'thumbnail': false,
      });
    if (path.startsWith('/api/v1/user/files/') && request.method == 'GET')
      return http.StreamedResponse(
        http.ByteStream.fromBytes(utf8.encode('N7 file payload')),
        200,
        headers: const {'content-type': 'text/plain'},
      );
    if (path.endsWith('/events')) {
      return http.StreamedResponse(
        http.ByteStream.fromBytes(
          utf8.encode(
            'event: record\nid: 9007199254740993\ndata: {"eventId":"01900000-0000-7000-8000-000000000003","collection":"notes","recordId":"01900000-0000-7000-8000-000000000001","operation":"update","version":2}\n\n',
          ),
        ),
        200,
        headers: const {'content-type': 'text/event-stream'},
      );
    }
    return _json(204, null);
  }

  http.StreamedResponse _json(
    int status,
    Object? body, {
    Map<String, String> headers = const {},
  }) => http.StreamedResponse(
    http.ByteStream.fromBytes(
      body == null ? const [] : utf8.encode(jsonEncode(body)),
    ),
    status,
    headers: {'content-type': 'application/json', ...headers},
  );
}

final class _BlockingSseClient extends http.BaseClient {
  final started = Completer<void>();
  final aborted = Completer<void>();

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    if (!request.url.path.endsWith('/events')) {
      throw StateError('unexpected request ${request.url}');
    }
    final stream = StreamController<List<int>>();
    final abortable = request as http.Abortable;
    abortable.abortTrigger!.then((_) async {
      if (!aborted.isCompleted) aborted.complete();
      await stream.close();
    });
    started.complete();
    return http.StreamedResponse(stream.stream, 200);
  }
}
