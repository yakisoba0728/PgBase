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

  test('structured HTTP errors retain their ETag and details', () async {
    final pgbase = PgBaseClient(
      Uri.parse('http://pgbase.test/'),
      httpClient: _ConflictClient(),
      auth: AuthStore(initialToken: 'user-token'),
    );

    try {
      await pgbase
          .records('notes')
          .update(
            '01900000-0000-7000-8000-000000000001',
            JsonObject({'title': const JsonString('two')}),
            '"pgb1.stale"',
          );
      fail('the stale ETag request must fail');
    } on PgBaseError catch (error) {
      expect(error.status, 412);
      expect(error.code, 'etag_conflict');
      expect(error.details['currentEtag'], ['pgb1.current']);
      expect(error.etag, '"pgb1.current"');
      expect(error.requestId, 'request-123');
    }
  });

  test(
    'request aborts remain cancellation rather than server errors',
    () async {
      final pgbase = PgBaseClient(
        Uri.parse('http://pgbase.test/'),
        httpClient: _AbortedClient(),
      );

      await expectLater(pgbase.session(), throwsA(isA<PgBaseCancellation>()));
    },
  );

  test('network failures have a stable SDK error type', () async {
    final pgbase = PgBaseClient(
      Uri.parse('http://pgbase.test/'),
      httpClient: _NetworkClient(),
    );

    await expectLater(pgbase.session(), throwsA(isA<PgBaseNetworkError>()));
  });

  test('storage clears after a delayed older token write', () async {
    final storage = _DelayedAuthStorage();
    final auth = AuthStore(storage: storage);
    final save = auth.save('stale-token');
    await storage.writeStarted.future;
    final clear = auth.clear();
    storage.finishWrite.complete();
    await Future.wait([save, clear]);

    expect(storage.value, isNull);
  });

  test('a delayed restore cannot replace a newer clear', () async {
    final storage = _DelayedAuthStorage()..value = 'stale-token';
    final auth = AuthStore(storage: storage);
    final restore = auth.restore();
    await storage.readStarted.future;
    final clear = auth.clear();
    storage.finishRead.complete();
    await Future.wait([restore, clear]);

    expect(auth.token, isNull);
  });

  test('restore waits behind a clear already writing storage', () async {
    final storage = _ClearThenRestoreStorage();
    final auth = AuthStore(storage: storage);
    final clear = auth.clear();
    await storage.clearStarted.future;
    final restore = auth.restore();
    storage.finishClear.complete();
    await Future.wait([clear, restore]);

    expect(auth.token, isNull);
  });

  test(
    'administrator 401 clears the in-memory cookie and CSRF state',
    () async {
      final client = _AdminUnauthorizedClient();
      final admin = PgBaseAdminClient(
        Uri.parse('http://pgbase.test/'),
        httpClient: client,
      );
      await admin.login('admin@example.test', 'password');

      await expectLater(
        admin.configureJob('daily', '0 0 * * *', true),
        throwsA(isA<PgBaseError>()),
      );
      await admin.configureJob('daily', '0 0 * * *', true);

      expect(client.requests[1].headers['cookie'], 'admin-session=one');
      expect(client.requests[1].headers['x-pgbase-csrf'], 'csrf-token');
      expect(client.requests[2].headers.containsKey('cookie'), isFalse);
      expect(client.requests[2].headers.containsKey('x-pgbase-csrf'), isFalse);
    },
  );

  test('an old administrator 401 cannot clear a newer login', () async {
    final client = _AdminStale401Client();
    final admin = PgBaseAdminClient(
      Uri.parse('http://pgbase.test/'),
      httpClient: client,
    );
    await admin.login('admin@example.test', 'password');
    final oldMutation = admin.configureJob('daily', '0 0 * * *', true);
    await client.oldMutationStarted.future;
    await admin.login('admin@example.test', 'password');
    client.oldMutationResponse.complete(
      _jsonResponse(401, {
        'error': {'code': 'unauthorized'},
      }),
    );
    await expectLater(oldMutation, throwsA(isA<PgBaseError>()));
    await admin.configureJob('daily', '0 0 * * *', true);

    expect(client.requests.last.headers['cookie'], 'admin-session=new');
    expect(client.requests.last.headers['x-pgbase-csrf'], 'csrf-new');
  });

  test('schema plans freeze their exact replay body and identities', () async {
    final client = _SchemaPlanClient();
    final admin = PgBaseAdminClient(
      Uri.parse('http://pgbase.test/'),
      httpClient: client,
    );
    final change = JsonObject({
      'operation': const JsonString('create'),
      'name': const JsonString('notes'),
      'fields': const JsonArray([]),
    });
    final plan = await admin.schemaPlan(change);
    await admin.applySchema(plan);
    await admin.applySchema(plan);

    expect((plan.preview['checksum'] as JsonString).value, 'sealed');
    expect(client.requests[1].body, client.requests[2].body);
    expect(
      client.requests[1].headers['pgbase-operation-id'],
      client.requests[2].headers['pgbase-operation-id'],
    );
    expect(
      client.requests[1].headers['idempotency-key'],
      client.requests[2].headers['idempotency-key'],
    );
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

  test('a stalled upload hash stops when cancellation fires', () async {
    final client = _ScriptedClient();
    final pgbase = PgBaseClient(
      Uri.parse('http://pgbase.test/'),
      httpClient: client,
      auth: AuthStore(initialToken: 'user-token'),
    );
    final stalled = StreamController<List<int>>();
    final cancellation = CancellationToken();
    final upload = pgbase
        .files('notes', 'record')
        .upload(
          'attachment',
          UploadSource(
            open: () => stalled.stream,
            length: 1,
            filename: 'stalled.txt',
          ),
          cancellation: cancellation,
        );
    cancellation.cancel();

    await expectLater(upload, throwsA(isA<PgBaseCancellation>()));
    expect(client.requests, isEmpty);
    await stalled.close();
  });

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

  test('terminal SSE auth and replay errors do not reconnect', () async {
    for (final status in [401, 410]) {
      final client = _TerminalSseClient(status);
      final pgbase = PgBaseClient(
        Uri.parse('http://pgbase.test/'),
        httpClient: client,
        auth: AuthStore(initialToken: 'user-token'),
      );
      await expectLater(
        pgbase.records('notes').events(maxReconnects: 5).drain<void>(),
        throwsA(isA<PgBaseError>()),
      );
      expect(client.calls, 1);
    }
  });

  test('realm profile returns the attributes object', () async {
    final pgbase = PgBaseClient(
      Uri.parse('http://pgbase.test/'),
      httpClient: _ProfileClient(),
    );
    final attributes = await pgbase.realm('members').profile();
    expect((attributes['displayName'] as JsonString).value, 'Ada');
  });

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

final class _ConflictClient extends http.BaseClient {
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    await request.finalize().drain<void>();
    return http.StreamedResponse(
      http.ByteStream.fromBytes(
        utf8.encode(
          r'{"error":{"code":"etag_conflict","requestId":"request-123","details":{"currentEtag":["pgb1.current"]}}}',
        ),
      ),
      412,
      headers: const {
        'content-type': 'application/json',
        'etag': '"pgb1.current"',
      },
    );
  }
}

final class _AbortedClient extends http.BaseClient {
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    await request.finalize().drain<void>();
    throw http.RequestAbortedException(request.url);
  }
}

final class _NetworkClient extends http.BaseClient {
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    await request.finalize().drain<void>();
    throw http.ClientException('offline', request.url);
  }
}

final class _AdminUnauthorizedClient extends http.BaseClient {
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
    return switch (requests.length) {
      1 => http.StreamedResponse(
        http.ByteStream.fromBytes(utf8.encode('{"csrfToken":"csrf-token"}')),
        200,
        headers: const {
          'content-type': 'application/json',
          'set-cookie': 'admin-session=one; HttpOnly',
        },
      ),
      2 => http.StreamedResponse(
        http.ByteStream.fromBytes(
          utf8.encode('{"error":{"code":"unauthorized"}}'),
        ),
        401,
        headers: const {'content-type': 'application/json'},
      ),
      _ => http.StreamedResponse(http.ByteStream.fromBytes(const []), 204),
    };
  }
}

final class _DelayedAuthStorage implements AuthStorage {
  String? value;
  final readStarted = Completer<void>();
  final finishRead = Completer<void>();
  final writeStarted = Completer<void>();
  final finishWrite = Completer<void>();

  @override
  Future<void> clear() async {
    value = null;
  }

  @override
  Future<String?> read() async {
    readStarted.complete();
    await finishRead.future;
    return value;
  }

  @override
  Future<void> write(String token) async {
    writeStarted.complete();
    await finishWrite.future;
    value = token;
  }
}

final class _ClearThenRestoreStorage implements AuthStorage {
  String? value = 'stale-token';
  final clearStarted = Completer<void>();
  final finishClear = Completer<void>();

  @override
  Future<void> clear() async {
    clearStarted.complete();
    await finishClear.future;
    value = null;
  }

  @override
  Future<String?> read() async => value;

  @override
  Future<void> write(String token) async {
    value = token;
  }
}

final class _TerminalSseClient extends http.BaseClient {
  _TerminalSseClient(this.status);
  final int status;
  int calls = 0;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    calls++;
    await request.finalize().drain<void>();
    return http.StreamedResponse(
      http.ByteStream.fromBytes(
        utf8.encode('{"error":{"code":"cursor_expired"}}'),
      ),
      status,
      headers: const {'content-type': 'application/json'},
    );
  }
}

final class _ProfileClient extends http.BaseClient {
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    await request.finalize().drain<void>();
    return http.StreamedResponse(
      http.ByteStream.fromBytes(
        utf8.encode('{"attributes":{"displayName":"Ada"}}'),
      ),
      200,
      headers: const {'content-type': 'application/json'},
    );
  }
}

http.StreamedResponse _jsonResponse(int status, Object? body) =>
    http.StreamedResponse(
      http.ByteStream.fromBytes(
        body == null ? const [] : utf8.encode(jsonEncode(body)),
      ),
      status,
      headers: const {'content-type': 'application/json'},
    );

final class _AdminStale401Client extends http.BaseClient {
  final List<_CapturedRequest> requests = [];
  final oldMutationStarted = Completer<void>();
  final oldMutationResponse = Completer<http.StreamedResponse>();

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
    return switch (requests.length) {
      1 => _adminLoginResponse('old'),
      2 => () async {
        oldMutationStarted.complete();
        return oldMutationResponse.future;
      }(),
      3 => _adminLoginResponse('new'),
      _ => _jsonResponse(204, null),
    };
  }
}

final class _SchemaPlanClient extends http.BaseClient {
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
    if (request.url.path.endsWith('/plan')) {
      return _jsonResponse(200, {'checksum': 'sealed', 'baseVersion': 3});
    }
    return _jsonResponse(204, null);
  }
}

http.StreamedResponse _adminLoginResponse(String suffix) =>
    http.StreamedResponse(
      http.ByteStream.fromBytes(
        utf8.encode(jsonEncode({'csrfToken': 'csrf-$suffix'})),
      ),
      200,
      headers: {
        'content-type': 'application/json',
        'set-cookie': 'admin-session=$suffix; HttpOnly',
      },
    );
