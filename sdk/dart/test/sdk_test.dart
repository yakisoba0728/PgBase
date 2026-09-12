import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

void main() {
  test(
    'filter placeholder values are deterministic and never parsed twice',
    () {
      final pgbase = PgBaseClient(Uri.parse('http://pgbase.test/'));
      expect(
        pgbase.filter('title = {:title} || summary = {:title}', {
          'title': 'quoted "value"\\next\nline',
        }),
        'title = "quoted \\"value\\"\\\\next\\nline" || summary = "quoted \\"value\\"\\\\next\\nline"',
      );
      expect(
        pgbase.filter('created >= {:when}', {
          'when': DateTime.utc(2026, 1, 2, 3, 4, 5),
        }),
        'created >= "2026-01-02 03:04:05.000Z"',
      );
      expect(
        pgbase.filter('flags = {:value}', {
          'value': [true, 'two'],
        }),
        'flags = "[true,\\"two\\"]"',
      );
      expect(
        pgbase.filter('meta = {:value}', {
          'value': {'flag': true},
        }),
        'meta = "{\\"flag\\":true}"',
      );
      expect(
        pgbase.filter('unset = {:value}', {'value': null}),
        'unset = null',
      );
      expect(
        pgbase.filter('number = {:value}', {'value': double.nan}),
        'number = NaN',
      );
      expect(
        pgbase.filter('number = {:value}', {'value': double.infinity}),
        'number = Infinity',
      );
      expect(
        pgbase.filter('known = {:known} && missing = {:missing}', {
          'known': false,
        }),
        'known = false && missing = {:missing}',
      );
      expect(
        pgbase.filter('value = {:text}', {
          'text': '{:text} || title != "safe"',
        }),
        'value = "{:text} || title != \\"safe\\""',
      );
    },
  );

  test(
    'application realtime topics preserve the raw name and payload',
    () async {
      final client = _ApplicationTopicClient();
      final pgbase = PgBaseClient(
        Uri.parse('http://pgbase.test/'),
        httpClient: client,
      );
      final received = Completer<RealtimeCustomEvent>();
      final stop = await pgbase.realtime.subscribeApplication(
        'custom-topic',
        received.complete,
      );
      final event = await received.future;
      expect(event.topic, 'custom-topic');
      expect(((event.data as JsonObject)['ok'] as JsonBool).value, isTrue);
      expect(client.subscriptions, ['@app/Y3VzdG9tLXRvcGlj']);
      await stop();
      expect(
        () => pgbase.realtime.subscribeApplication('', (_) {}),
        throwsA(isA<PgBaseProtocolError>()),
      );
    },
  );

  test(
    'application realtime topics preserve every valid JSON payload shape',
    () async {
      const payloads = <String>[
        '{"ok":true}',
        '[1,2]',
        '"text"',
        '7',
        'false',
        'null',
      ];
      for (final payload in payloads) {
        final client = _ApplicationPayloadClient(payload);
        final pgbase = PgBaseClient(
          Uri.parse('http://pgbase.test/'),
          httpClient: client,
        );
        final received = Completer<RealtimeCustomEvent>();
        final stop = await pgbase.realtime.subscribeApplication(
          'payload',
          received.complete,
        );
        final event = await received.future.timeout(
          const Duration(milliseconds: 50),
        );
        expect(event.topic, 'payload');
        expect(utf8.decode(encodeExactJson(event.data)), payload);
        await stop();
      }
    },
  );

  test('clean shared realtime EOF consumes the reconnect budget', () async {
    final httpClient = _CleanEofThenTerminalClient();
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test/'),
      httpClient: httpClient,
    );
    final realtime = RealtimeService(
      client,
      maxReconnects: 0,
      reconnectDelay: Duration.zero,
    );
    final stop = await realtime.subscribe('notes', (_) {});
    await Future<void>.delayed(const Duration(milliseconds: 20));
    expect(httpClient.getCalls, 1);
    await stop();
  });

  test(
    'malformed optional HTTP error fields retain stable error normalization',
    () async {
      for (final body in <String>[
        '{"error":{"code":7}}',
        '{"error":{"code":[]}}',
        '{"error":{"requestId":{}}}',
      ]) {
        final pgbase = PgBaseClient(
          Uri.parse('http://pgbase.test/'),
          httpClient: _MalformedErrorClient(body),
        );
        await expectLater(
          pgbase.session(),
          throwsA(
            isA<PgBaseError>()
                .having((error) => error.status, 'status', 500)
                .having((error) => error.code, 'code', 'request_failed')
                .having((error) => error.requestId, 'requestId', isNull)
                .having((error) => error.etag, 'etag', '"pgb1.error"'),
          ),
        );
      }
    },
  );

  test('shared realtime rejects a terminal HTTP handshake without retrying', () async {
    final httpClient = _TerminalSseClient(401, code: 'unauthorized');
    final client = PgBaseClient(Uri.parse('http://pgbase.test/'), httpClient: httpClient);
    final realtime = RealtimeService(client, maxReconnects: 3);

    await expectLater(
      realtime.subscribe('notes', (_) {}),
      throwsA(isA<PgBaseError>().having((error) => error.status, 'status', 401)),
    );
    expect(httpClient.calls, 1);
  });

  test('shared realtime bounds recoverable handshake retries', () async {
    final httpClient = _TerminalSseClient(500, code: 'server_error');
    final client = PgBaseClient(Uri.parse('http://pgbase.test/'), httpClient: httpClient);
    final realtime = RealtimeService(
      client,
      maxReconnects: 1,
      reconnectDelay: Duration.zero,
    );

    await expectLater(realtime.subscribe('notes', (_) {}), throwsA(isA<PgBaseError>()));
    expect(httpClient.calls, 2);
  });

  test('unsubscribing during a pending shared handshake settles readiness', () async {
    final httpClient = _BlockingSseClient();
    final client = PgBaseClient(Uri.parse('http://pgbase.test/'), httpClient: httpClient);
    final realtime = RealtimeService(client);
    final pending = realtime.subscribe('notes', (_) {});
    await httpClient.started.future;
    await realtime.unsubscribe();

    await expectLater(pending, throwsA(isA<PgBaseCancellation>()));
    await httpClient.aborted.future;
  });

  test('a non-cancellation body error after unsubscribe settles readiness', () async {
    final httpClient = _BodyAbortErrorSseClient();
    final client = PgBaseClient(Uri.parse('http://pgbase.test/'), httpClient: httpClient);
    final realtime = RealtimeService(client);
    final pending = realtime.subscribe('notes', (_) {});
    await httpClient.started.future;
    await realtime.unsubscribe();

    await expectLater(pending, throwsA(isA<PgBaseCancellation>()));
    await httpClient.aborted.future;
  });

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
      expect(
        PgBaseUuid('00000000-0000-0000-0000-000000000000').value,
        '00000000-0000-0000-0000-000000000000',
      );
      expect(
        PgBaseUuid('FFFFFFFF-FFFF-FFFF-FFFF-FFFFFFFFFFFF').value,
        'ffffffff-ffff-ffff-ffff-ffffffffffff',
      );
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
      final members = pgbase.realm('members');
      final methods = await members.methods();
      expect(methods.passwordEnabled, isTrue);
      expect(methods.otpEnabled, isTrue);
      expect(methods.oauthProviders, ['github']);
      expect(client.requests[1].path, '/api/v1/auth/members/methods');
      final refreshed = await members.refresh();
      expect(refreshed.token, 'rotated-token');
      expect(pgbase.auth.token, 'rotated-token');
      expect(client.requests[2].headers['authorization'], 'Bearer user-token');
      await pgbase.auth.save('still-valid-locally');
      await expectLater(
        pgbase.realm('invalid').refresh(),
        throwsA(isA<PgBaseProtocolError>()),
      );
      expect(pgbase.auth.token, 'still-valid-locally');
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
      expect(pgbase.auth.token, 'still-valid-locally');
      expect(created.etag, '"pgb1.created"');
      expect(
        client.requests[4].headers['authorization'],
        'Bearer still-valid-locally',
      );
      expect(client.requests[4].headers['idempotency-key'], 'create-note-0001');
      expect(client.requests[5].headers['if-match'], '"pgb1.created"');
      expect(batch['results'], isA<JsonArray>());
      expect(
        () => pgbase.records('notes').create(JsonObject({}), 'short'),
        throwsA(isA<PgBaseProtocolError>()),
      );
    },
  );

  test('batch upserts use both compact and generic PUT forms', () async {
    final client = _ScriptedClient();
    final pgbase = PgBaseClient(
      Uri.parse('http://pgbase.test/'),
      httpClient: client,
    );
    const id = '01900000-0000-7000-8000-000000000001';
    final input = JsonObject({'title': const JsonString('upserted')});

    await pgbase.batch([
      BatchOperation.upsert(collection: 'notes', id: id, input: input),
    ]);
    await pgbase.batchHttpRequests([
      BatchHttpRequest.upsert(
        collection: 'notes',
        id: id,
        input: input,
        ifMatch: '"pgb1.current"',
      ),
    ]);

    final compact = decodeExactJson(client.requests[0].body) as JsonObject;
    final operation =
        ((compact['operations'] as JsonArray).values.single as JsonObject);
    expect((operation['operation'] as JsonString).value, 'upsert');
    expect((operation['id'] as JsonString).value, id);

    final generic = decodeExactJson(client.requests[1].body) as JsonObject;
    final request =
        ((generic['requests'] as JsonArray).values.single as JsonObject);
    expect((request['method'] as JsonString).value, 'PUT');
    expect(
      (request['url'] as JsonString).value,
      '/api/v1/collections/notes/records/$id',
    );
    final headers = request['headers'] as JsonObject;
    expect((headers['if-match'] as JsonString).value, '"pgb1.current"');
  });

  test(
    'record fields projections reach list and get and preserve responses',
    () async {
      final client = _ProjectionClient();
      final pgbase = PgBaseClient(
        Uri.parse('http://pgbase.test/'),
        httpClient: client,
      );

      final page = await pgbase
          .records('notes')
          .list(const Query(fields: 'title,summary'));
      final record = await pgbase
          .records('notes')
          .get('01900000-0000-7000-8000-000000000001', fields: 'title,summary');

      expect((page.items.single['title'] as JsonString).value, 'listed');
      expect(page.items.single.values.containsKey('private'), isFalse);
      expect((record['title'] as JsonString).value, 'viewed');
      expect(record.values.containsKey('private'), isFalse);
      expect(client.urls, hasLength(2));
      for (final url in client.urls) {
        expect(url.query, 'fields=title%2Csummary');
        expect(url.queryParameters['fields'], 'title,summary');
      }
    },
  );

  test(
    'PocketBase numbered list sends its explicit compatibility contract',
    () async {
      final client = _ProjectionClient();
      final pgbase = PgBaseClient(
        Uri.parse('http://pgbase.test/'),
        httpClient: client,
      );
      final page = await pgbase
          .records('notes')
          .getList(
            2,
            25,
            const PocketBaseListOptions(
              filter: 'title = "paged"',
              sort: '@random',
              skipTotal: true,
            ),
          );
      expect(page.page, 2);
      expect(page.perPage, 25);
      expect(page.totalItems, -1);
      expect(page.totalPages, -1);
      expect((page.items.single['title'] as JsonString).value, 'paged');
      final url = client.urls.single;
      expect(url.queryParameters['compat'], 'pocketbase');
      expect(url.queryParameters['page'], '2');
      expect(url.queryParameters['perPage'], '25');
      expect(url.queryParameters['skipTotal'], 'true');
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
    'concurrent admin logins keep the newest cookie and CSRF paired',
    () async {
      final client = _RacingAdminLoginClient();
      final admin = PgBaseAdminClient(
        Uri.parse('http://pgbase.test/'),
        httpClient: client,
      );
      final older = admin.login('old@example.test', 'password');
      await client.oldLoginStarted.future;
      final newer = admin.login('new@example.test', 'password');
      await newer;
      client.oldLoginResponse.complete(_adminLoginResponse('old'));
      await older;
      await admin.configureJob('daily', '0 0 * * *', true);

      final mutation = client.requests.last;
      expect(mutation.headers['cookie'], 'admin-session=new');
      expect(mutation.headers['x-pgbase-csrf'], 'csrf-new');
    },
  );

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

  test('request keys cancel the prior Dart request and cancelAll cancels active keys', () async {
    final httpClient = _KeyedAbortClient();
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test/'),
      httpClient: httpClient,
    );
    final first = client.request('GET', '/one', requestKey: 'same');
    await httpClient.firstStarted.future;
    final second = client.request('GET', '/two', requestKey: 'same');
    await expectLater(first, throwsA(isA<PgBaseCancellation>()));
    expect(await second, isA<JsonObject>());
    expect(httpClient.aborted, 1);
    final third = client.request('GET', '/three', requestKey: 'three');
    await httpClient.secondStarted.future;
    client.cancelAllRequests();
    await expectLater(third, throwsA(isA<PgBaseCancellation>()));
    expect(httpClient.aborted, 2);
  });

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

  test(
    'auth store retains its record across an SSR cookie round trip',
    () async {
      final source = AuthStore();
      await source.save(
        'server-token',
        record: const User(
          id: '01900000-0000-7000-8000-000000000001',
          email: 'person@example.test',
          createdAt: '2026-01-01T00:00:00Z',
        ),
      );
      final restored = AuthStore();
      await restored.loadFromCookie(source.exportToCookie());
      expect(restored.isValid, isTrue);
      expect(restored.record?.email, 'person@example.test');
    },
  );

  test(
    'OAuth launcher receives the provider URL before callback completion',
    () async {
      final oauth = _OAuthClient();
      final client = PgBaseClient(
        Uri.parse('http://pgbase.test/'),
        httpClient: oauth,
      );
      Uri? opened;
      await client.realm('members').oauthStartAndOpen(
        'github',
        'app://callback',
        (url) {
          opened = url;
        },
      );
      expect(
        opened,
        Uri.parse('https://provider.test/authorize?state=expected-state'),
      );
      expect(oauth.requests, ['/api/v1/auth/members/oauth/start']);
    },
  );

  test(
    'OAuth browser flow validates callback state and always closes its adapter',
    () async {
      final oauth = _OAuthClient();
      final client = PgBaseClient(
        Uri.parse('http://pgbase.test/'),
        httpClient: oauth,
      );
      var closed = 0;
      final session = await client
          .realm('members')
          .oauthWithBrowser(
            'github',
            'app://callback',
            OAuthBrowserAdapter(
              open: (_) {},
              waitForCallback: (_) async =>
                  Uri.parse('app://callback?state=expected-state&code=code'),
              close: () => closed++,
            ),
          );
      expect(session.token, 'oauth-token');
      expect(oauth.requests.take(2).toList(), [
        '/api/v1/auth/members/oauth/start',
        '/api/v1/auth/oauth/complete',
      ]);
      expect(closed, 1);
      final globalSession = await client.oauthWithBrowser(
        'github',
        'app://callback',
        OAuthBrowserAdapter(
          open: (_) {},
          waitForCallback: (_) async =>
              Uri.parse('app://callback?state=expected-state&code=code'),
          close: () => closed++,
        ),
      );
      expect(globalSession.token, 'oauth-token');
      expect(oauth.requests.skip(2).take(2).toList(), [
        '/api/v1/auth/oauth/start',
        '/api/v1/auth/oauth/complete',
      ]);
      expect(closed, 2);
      await expectLater(
        client
            .realm('members')
            .oauthWithBrowser(
              'github',
              'app://callback',
              OAuthBrowserAdapter(
                open: (_) {},
                waitForCallback: (_) async =>
                    Uri.parse('app://callback?state=wrong&code=code'),
                close: () => closed++,
              ),
            ),
        throwsA(isA<OAuthCallbackError>()),
      );
      expect(closed, 3);
      await expectLater(
        client
            .realm('members')
            .oauthWithBrowser(
              'github',
              'app://callback',
              OAuthBrowserAdapter(
                open: (_) {},
                waitForCallback: (_) async => Uri.parse(
                  'app://callback?state=expected-state&error=access_denied&error_description=cancelled',
                ),
                close: () => closed++,
              ),
            ),
        throwsA(
          isA<OAuthCallbackError>().having(
            (error) => error.code,
            'code',
            'access_denied',
          ),
        ),
      );
      expect(closed, 4);
    },
  );

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

  test('an SSE body network failure has the stable SDK error type', () async {
    final pgbase = PgBaseClient(
      Uri.parse('http://pgbase.test/'),
      httpClient: _SseBodyNetworkClient(),
      auth: AuthStore(initialToken: 'user-token'),
    );

    await expectLater(
      pgbase.records('notes').events(maxReconnects: 0).drain<void>(),
      throwsA(isA<PgBaseNetworkError>()),
    );
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

final class _ProjectionClient extends http.BaseClient {
  final List<Uri> urls = [];

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    await request.finalize().drain<void>();
    urls.add(request.url);
    final isList = request.url.path.endsWith('/records');
    if (isList && request.url.queryParameters['compat'] == 'pocketbase') {
      return _jsonResponse(200, {
        'page': 2,
        'perPage': 25,
        'totalItems': -1,
        'totalPages': -1,
        'items': [
          {'id': '01900000-0000-7000-8000-000000000001', 'title': 'paged'},
        ],
      });
    }
    return _jsonResponse(
      200,
      isList
          ? {
              'items': [
                {
                  'id': '01900000-0000-7000-8000-000000000001',
                  'title': 'listed',
                },
              ],
            }
          : {'id': '01900000-0000-7000-8000-000000000001', 'title': 'viewed'},
    );
  }
}

final class _ApplicationTopicClient extends http.BaseClient {
  final List<String> subscriptions = [];

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final body = await request.finalize().toBytes();
    if (request.method == 'GET' && request.url.path == '/api/realtime') {
      return http.StreamedResponse(
        http.ByteStream.fromBytes(
          utf8.encode(
            'event: PB_CONNECT\ndata: {"clientId":"application-test"}\n\n'
            'event: @app/Y3VzdG9tLXRvcGlj\ndata: {"ok":true}\n\n',
          ),
        ),
        200,
        headers: const {'content-type': 'text/event-stream'},
      );
    }
    if (request.method == 'POST' && request.url.path == '/api/realtime') {
      final value = decodeExactJson(body) as JsonObject;
      final values = value['subscriptions'] as JsonArray;
      subscriptions.addAll(
        values.values.cast<JsonString>().map((value) => value.value),
      );
      return _jsonResponse(204, null);
    }
    throw StateError('unexpected application realtime request ${request.url}');
  }
}

final class _ApplicationPayloadClient extends http.BaseClient {
  _ApplicationPayloadClient(this.payload);
  final String payload;
  var getCalls = 0;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    await request.finalize().drain<void>();
    if (request.method == 'POST') return _jsonResponse(204, null);
    getCalls++;
    if (getCalls > 1)
      return _jsonResponse(410, {
        'error': {'code': 'gone'},
      });
    return http.StreamedResponse(
      http.ByteStream.fromBytes(
        utf8.encode(
          'event: PB_CONNECT\ndata: {"clientId":"payload-test"}\n\n'
          'event: @app/cGF5bG9hZA\ndata: $payload\n\n',
        ),
      ),
      200,
      headers: const {'content-type': 'text/event-stream'},
    );
  }
}

final class _CleanEofThenTerminalClient extends http.BaseClient {
  var getCalls = 0;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    await request.finalize().drain<void>();
    if (request.method == 'POST') return _jsonResponse(204, null);
    getCalls++;
    if (getCalls > 1)
      return _jsonResponse(410, {
        'error': {'code': 'gone'},
      });
    return http.StreamedResponse(
      http.ByteStream.fromBytes(
        utf8.encode('event: PB_CONNECT\ndata: {"clientId":"eof-test"}\n\n'),
      ),
      200,
      headers: const {'content-type': 'text/event-stream'},
    );
  }
}

final class _MalformedErrorClient extends http.BaseClient {
  _MalformedErrorClient(this.body);
  final String body;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    await request.finalize().drain<void>();
    return http.StreamedResponse(
      http.ByteStream.fromBytes(utf8.encode(body)),
      500,
      headers: const {
        'content-type': 'application/json',
        'etag': '"pgb1.error"',
      },
    );
  }
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
    if (path == '/api/v1/auth/members/methods')
      return _json(200, {
        'passwordEnabled': true,
        'otpEnabled': true,
        'mfaEnabled': false,
        'oauthProviders': ['github'],
      });
    if (path == '/api/v1/auth/members/refresh')
      return _json(200, {
        'token': 'rotated-token',
        'user': {
          'id': '01900000-0000-7000-8000-000000000001',
          'email': 'person@example.test',
          'createdAt': '2026-01-01T00:00:00Z',
        },
        'expiresAt': '2026-01-02T00:00:00Z',
      });
    if (path == '/api/v1/auth/invalid/refresh')
      return _json(200, {'token': 'must-not-save'});
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
    if (!request.url.path.endsWith('/events') && request.url.path != '/api/realtime') {
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

final class _BodyAbortErrorSseClient extends http.BaseClient {
  final started = Completer<void>();
  final aborted = Completer<void>();

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    if (request.url.path != '/api/realtime') {
      throw StateError('unexpected request ${request.url}');
    }
    final stream = StreamController<List<int>>();
    final abortable = request as http.Abortable;
    abortable.abortTrigger!.then((_) async {
      if (!aborted.isCompleted) aborted.complete();
      stream.addError(http.ClientException('body aborted', request.url));
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

final class _OAuthClient extends http.BaseClient {
  final List<String> requests = [];
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    await request.finalize().drain<void>();
    requests.add(request.url.path);
    if (request.url.path.endsWith('/oauth/start')) {
      return _jsonResponse(200, {
        'authorizationUrl':
            'https://provider.test/authorize?state=expected-state',
        'expiresAt': '2026-01-02T00:00:00Z',
      });
    }
    if (request.url.path.endsWith('/oauth/complete')) {
      return _jsonResponse(200, {
        'token': 'oauth-token',
        'user': {
          'id': 'oauth',
          'email': 'oauth@example.test',
          'createdAt': '2026-01-01T00:00:00Z',
        },
        'expiresAt': '2026-01-02T00:00:00Z',
      });
    }
    throw StateError('unexpected request');
  }
}

final class _KeyedAbortClient extends http.BaseClient {
  final firstStarted = Completer<void>();
  final secondStarted = Completer<void>();
  int calls = 0;
  int aborted = 0;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) {
    calls++;
    final abortable = request as http.Abortable;
    if (calls == 2) return Future.value(_jsonResponse(200, {'ok': true}));
    if (calls == 1) {
      firstStarted.complete();
    } else {
      secondStarted.complete();
    }
    final response = Completer<http.StreamedResponse>();
    abortable.abortTrigger!.then((_) {
      aborted++;
      response.completeError(http.RequestAbortedException(request.url));
    });
    return response.future;
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
  _TerminalSseClient(this.status, {this.code = 'cursor_expired'});
  final int status;
  final String code;
  int calls = 0;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    calls++;
    await request.finalize().drain<void>();
    return http.StreamedResponse(
      http.ByteStream.fromBytes(
        utf8.encode('{"error":{"code":"$code"}}'),
      ),
      status,
      headers: const {'content-type': 'application/json'},
    );
  }
}

final class _SseBodyNetworkClient extends http.BaseClient {
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    await request.finalize().drain<void>();
    return http.StreamedResponse(
      Stream<List<int>>.error(http.ClientException('stream lost', request.url)),
      200,
      headers: const {'content-type': 'text/event-stream'},
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

final class _RacingAdminLoginClient extends http.BaseClient {
  final List<_CapturedRequest> requests = [];
  final oldLoginStarted = Completer<void>();
  final oldLoginResponse = Completer<http.StreamedResponse>();

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
    if (request.url.path == '/api/admin/auth/login') {
      final email =
          (jsonDecode(utf8.decode(body)) as Map<String, dynamic>)['email'];
      if (email == 'old@example.test') {
        oldLoginStarted.complete();
        return oldLoginResponse.future;
      }
      return _adminLoginResponse('new');
    }
    return _jsonResponse(204, null);
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
