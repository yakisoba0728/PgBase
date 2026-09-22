import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

Future<void> tick() => Future<void>.delayed(Duration.zero);

void main() {
  test(
    'reentrant transitions never deliver an older state after stopped',
    () async {
      final transport = LiveRealtime();
      final client = PgBaseClient(
        Uri.parse('http://pgbase.test'),
        httpClient: transport,
      );
      final observed = <RealtimeState>[];
      client.realtime.onStatus((status) {
        if (status.state == RealtimeState.connecting)
          return client.realtime.unsubscribe();
      });
      client.realtime.onStatus((status) {
        observed.add(status.state);
      });
      await expectLater(
        client.realtime.subscribe('notes/*', (_) {}),
        throwsA(isA<PgBaseCancellation>()),
      );
      expect(observed, [RealtimeState.stopped, RealtimeState.stopped]);
      expect(client.realtime.status.state, RealtimeState.stopped);
      expect(transport.connections, isEmpty);
    },
  );

  test(
    'reset from a reconnecting observer skips an already cancelled backoff',
    () async {
      final transport = LiveRealtime()..endAfterConnect = true;
      final client = PgBaseClient(
        Uri.parse('http://pgbase.test'),
        httpClient: transport,
      );
      final realtime = RealtimeService(
        client,
        reconnectDelay: const Duration(seconds: 1),
      );
      final started = Completer<void>();
      Future<void>? reset;
      realtime.onStatus((status) {
        if (status.state == RealtimeState.reconnecting) {
          reset = realtime.resetCursor();
          started.complete();
        }
      });
      await realtime.subscribe('notes/*', (_) {});
      await started.future;
      await reset!.timeout(const Duration(milliseconds: 200));
      expect(realtime.status.reason, RealtimeStopReason.reset);
    },
  );

  test(
    'status observers can add subscriptions without a second connection',
    () async {
      final transport = LiveRealtime();
      final client = PgBaseClient(
        Uri.parse('http://pgbase.test'),
        httpClient: transport,
      );
      var adding = false;
      Future<Future<void> Function()>? added;
      client.realtime.onStatus((status) {
        if (status.state == RealtimeState.connecting && !adding) {
          adding = true;
          added = client.realtime.subscribe('notes/one', (_) {});
        }
      });
      await client.realtime.subscribe('notes/*', (_) {});
      await added;
      expect(transport.connections.length, 1);
      await client.realtime.unsubscribe();
    },
  );

  test(
    'repeated empty sessions exhaust the budget despite successful handshakes',
    () async {
      final transport = LiveRealtime()..endAfterConnect = true;
      final client = PgBaseClient(
        Uri.parse('http://pgbase.test'),
        httpClient: transport,
      );
      final realtime = RealtimeService(
        client,
        maxReconnects: 2,
        reconnectDelay: Duration.zero,
      );
      await realtime.subscribe('notes/*', (_) {});
      for (
        var i = 0;
        i < 10 && realtime.status.state != RealtimeState.stopped;
        i++
      ) {
        await tick();
      }
      expect(realtime.status.reason, RealtimeStopReason.exhausted);
      expect(realtime.status.attempt, 2);
      expect(transport.connections.length, 3);
    },
  );

  test('status reports a terminal failure after readiness and reset discards the cursor explicitly', () async {
    final transport = LiveRealtime();
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test'),
      httpClient: transport,
    );
    final statuses = <RealtimeStatus>[];
    final remove = client.realtime.onStatus(statuses.add);
    expect(client.realtime.status.state, RealtimeState.stopped);
    await client.realtime.subscribe('notes/*', (_) {});
    transport.connections[0].write(
      'id: 9007199254740993\nevent: cursor\ndata: {}\n\nevent: PB_ERROR\ndata: {"code":"cursor_expired"}\n\n',
    );
    await tick();
    expect(statuses.last.state, RealtimeState.stopped);
    expect(statuses.last.reason, RealtimeStopReason.terminal);
    expect(statuses.last.lastEventId, '9007199254740993');
    expect((statuses.last.error as PgBaseError).code, 'cursor_expired');
    await client.realtime.subscribe('notes/*', (_) {});
    expect(
      transport.connections[1].request.headers['last-event-id'],
      '9007199254740993',
    );
    await client.realtime.resetCursor();
    expect(client.realtime.status.lastEventId, isNull);
    expect(client.realtime.status.reason, RealtimeStopReason.reset);
    await client.realtime.subscribe('notes/*', (_) {});
    expect(transport.connections[2].request.headers['last-event-id'], isNull);
    await client.realtime.unsubscribe();
    expect(statuses.last.reason, RealtimeStopReason.unsubscribed);
    remove();
  });

  test('consumer and observer failures cannot poison delivery or reconnect', () async {
    final transport = LiveRealtime();
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test'),
      httpClient: transport,
    );
    final statuses = <RealtimeStatus>[];
    final delivered = <String>[];
    client.realtime.onStatus((_) => throw StateError('observer'));
    client.realtime.onStatus((_) async => throw StateError('async observer'));
    client.realtime.onStatus(statuses.add);
    await client.realtime.subscribe(
      'notes/*',
      (_) => throw StateError('consumer'),
    );
    await client.realtime.subscribe(
      'notes/*',
      (_) async => throw StateError('async consumer'),
    );
    await client.realtime.subscribe(
      'notes/*',
      (RealtimeEvent event) => delivered.add(event.action),
    );
    await client.realtime.subscribeApplication(
      'feed',
      (_) async => throw StateError('async application consumer'),
    );
    for (final action in ['create', 'update']) {
      transport.connections[0].write(
        'id: 8\nevent: notes/*\ndata: {"action":"$action","record":{"id":"one"}}\n\n',
      );
    }
    transport.connections[0].write('event: @app/ZmVlZA\ndata: {}\n\n');
    await tick();
    expect(delivered, ['create', 'update']);
    expect(transport.connections.length, 1);
    expect(
      statuses
          .where((status) => status.source == RealtimeErrorSource.listener)
          .length,
      5,
    );
    expect(client.realtime.status.state, RealtimeState.connected);
    expect(statuses.where((status) => status.topic == 'feed').length, 1);
    await client.realtime.unsubscribe();
  });

  test(
    'recoverable failures report attempts and exhaustion after readiness',
    () async {
      final transport = LiveRealtime()..failReconnect = true;
      final client = PgBaseClient(
        Uri.parse('http://pgbase.test'),
        httpClient: transport,
      );
      final realtime = RealtimeService(
        client,
        maxReconnects: 1,
        reconnectDelay: Duration.zero,
      );
      final statuses = <RealtimeStatus>[];
      realtime.onStatus(statuses.add);
      await realtime.subscribe('notes/*', (_) {});
      await transport.connections.single.body.close();
      for (
        var i = 0;
        i < 10 && realtime.status.state != RealtimeState.stopped;
        i++
      ) {
        await tick();
      }
      expect(transport.requests, 2);
      expect(
        statuses
            .firstWhere((status) => status.state == RealtimeState.reconnecting)
            .attempt,
        1,
      );
      expect(statuses.last.reason, RealtimeStopReason.exhausted);
      expect(
        (statuses.last.error as PgBaseError).code,
        'temporarily_unavailable',
      );
    },
  );
}

final class LiveRealtime extends http.BaseClient {
  final connections = <LiveConnection>[];
  int requests = 0;
  bool failReconnect = false;
  bool endAfterConnect = false;
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    if (request.method == 'POST')
      return http.StreamedResponse(const Stream.empty(), 204);
    if (++requests > 1 && failReconnect)
      return http.StreamedResponse(
        Stream.value(
          utf8.encode('{"error":{"code":"temporarily_unavailable"}}'),
        ),
        503,
      );
    final connection = LiveConnection(request);
    connections.add(connection);
    if (request is http.AbortableRequest)
      request.abortTrigger?.then((_) {
        if (!connection.body.isClosed) connection.body.close();
      });
    connection.write('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n');
    if (endAfterConnect) connection.body.close();
    return http.StreamedResponse(connection.body.stream, 200);
  }
}

final class LiveConnection {
  LiveConnection(this.request);
  final http.BaseRequest request;
  final body = StreamController<List<int>>();
  void write(String frame) => body.add(utf8.encode(frame));
}
