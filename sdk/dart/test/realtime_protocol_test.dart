import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

void main() {
  test('a pre-cancelled native record stream makes no request', () async {
    final transport = RealtimeFrames('');
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test'),
      httpClient: transport,
    );
    final cancellation = CancellationToken()..cancel();
    expect(
      await client
          .records('notes')
          .events(cancellation: cancellation, maxReconnects: 0)
          .toList(),
      isEmpty,
    );
    expect(transport.connections, 0);
  });

  test('native record streams exhaust reconnects after clean EOF', () async {
    for (final maxReconnects in [0, 2]) {
      final transport = RealtimeFrames('');
      final client = PgBaseClient(
        Uri.parse('http://pgbase.test'),
        httpClient: transport,
      );
      await expectLater(
        client
            .records('notes')
            .events(maxReconnects: maxReconnects, reconnectDelay: Duration.zero)
            .drain<void>()
            .timeout(const Duration(seconds: 1)),
        throwsA(
          isA<PgBaseProtocolError>().having(
            (error) => error.code,
            'code',
            'realtime_stream_ended',
          ),
        ),
      );
      expect(transport.connections, maxReconnects + 1);
    }
  });

  test(
    'PB_ERROR preserves terminal cursor failure codes before readiness',
    () async {
      for (final code in ['cursor_expired', 'invalid_cursor']) {
        final transport = RealtimeFrames(
          'event: PB_ERROR\ndata: {"code":"$code"}\n\n',
        );
        final client = PgBaseClient(
          Uri.parse('http://pgbase.test'),
          httpClient: transport,
        );
        final realtime = RealtimeService(client, maxReconnects: 0);
        await expectLater(
          realtime.subscribe('notes/*', (_) {}),
          throwsA(isA<PgBaseError>().having((e) => e.code, 'code', code)),
        );
        expect(transport.connections, 1);
      }
    },
  );

  test('PB_ERROR after readiness ends retries, preserving repeated numeric checkpoint events', () async {
    final transport = RealtimeFrames(
      'event: PB_CONNECT\ndata: {"clientId":"session"}\n\n'
      'id: 9007199254740993\nevent: cursor\ndata: {}\n\n'
      'id: 9007199254740993\nevent: notes/*\ndata: {"action":"create","record":{"id":"one"}}\n\n'
      'id: 9007199254740993\nevent: notes/*\ndata: {"action":"update","record":{"id":"one"}}\n\n'
      'event: PB_ERROR\ndata: {"code":"cursor_expired"}\n\n',
    );
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test'),
      httpClient: transport,
    );
    final realtime = RealtimeService(
      client,
      maxReconnects: 1,
      reconnectDelay: const Duration(milliseconds: 1),
    );
    final events = <RealtimeEvent>[];
    try {
      await realtime.subscribe(
        'notes/*',
        (RealtimeEvent event) => events.add(event),
      );
      await Future<void>.delayed(const Duration(milliseconds: 20));
      expect(transport.connections, 1);
      expect(events.map((event) => event.action), ['create', 'update']);
    } finally {
      await realtime.unsubscribe();
    }
  });

  test('reconnect restores existing topics before rejecting new ones', () async {
    final streams = <StreamController<List<int>>>[];
    final checkpoints = <String?>[];
    final syncs = <List<dynamic>>[];
    final reconnecting = Completer<RealtimeStatus>();
    final transport = MockClient.streaming((request, body) async {
      if (request.method == 'POST') {
        final topics =
            (jsonDecode(await body.transform(utf8.decoder).join())
                    as Map)['subscriptions']
                as List;
        syncs.add(topics);
        if (topics.contains('denied/*')) {
          return http.StreamedResponse(
            Stream.value(utf8.encode('{"error":{"code":"access_denied"}}')),
            403,
          );
        }
        return http.StreamedResponse(const Stream.empty(), 204);
      }
      final controller = StreamController<List<int>>();
      streams.add(controller);
      checkpoints.add(request.headers['last-event-id']);
      controller.add(
        utf8.encode(
          'event: PB_CONNECT\ndata: {"clientId":"session-${streams.length}"}\n\n',
        ),
      );
      return http.StreamedResponse(controller.stream, 200);
    });
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test'),
      httpClient: transport,
    );
    final realtime = RealtimeService(
      client,
      maxReconnects: 1,
      reconnectDelay: const Duration(milliseconds: 10),
    );
    realtime.onStatus((status) {
      if (status.state == RealtimeState.reconnecting)
        reconnecting.complete(status);
    });
    final topics = List.generate(100, (i) => 'notes/$i');
    try {
      await realtime.subscribeMany(topics, (_) {});
      expect(syncs, [topics]);
      streams.first.add(
        utf8.encode(
          'id: 9007199254740993\nevent: cursor\ndata: {}\n\nevent: PB_ERROR\ndata: {"code":"subscription_failed","retryable":true}\n\n',
        ),
      );
      expect(
        (await reconnecting.future).error,
        isA<PgBaseError>().having(
          (error) => error.code,
          'code',
          'subscription_failed',
        ),
      );
      await expectLater(
        realtime.subscribe('denied/*', (_) {}),
        throwsA(
          isA<PgBaseError>().having(
            (error) => error.code,
            'code',
            'access_denied',
          ),
        ),
      );
      expect(checkpoints, [null, '9007199254740993']);
      expect(syncs, [
        topics,
        topics,
        [...topics, 'denied/*'],
      ]);
      expect(realtime.status.state, RealtimeState.connected);
    } finally {
      await realtime.unsubscribe();
      for (final stream in streams) {
        await stream.close();
      }
    }
  });

  test('concurrent subscriptions share one replacement and a denied topic rejects only its own call', () async {
    final syncs = <List<dynamic>>[];
    final held = Completer<void>();
    final release = Completer<void>();
    final stream = StreamController<List<int>>();
    final transport = MockClient.streaming((request, body) async {
      if (request.method == 'POST') {
        final topics =
            (jsonDecode(await body.transform(utf8.decoder).join())
                    as Map)['subscriptions']
                as List;
        syncs.add(topics);
        if (!held.isCompleted) {
          held.complete();
          await release.future;
        }
        if (topics.contains('denied/*')) {
          return http.StreamedResponse(
            Stream.value(utf8.encode('{"error":{"code":"access_denied"}}')),
            403,
          );
        }
        return http.StreamedResponse(const Stream.empty(), 204);
      }
      stream.add(
        utf8.encode('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n'),
      );
      return http.StreamedResponse(stream.stream, 200);
    });
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test'),
      httpClient: transport,
    );
    final realtime = RealtimeService(client);
    try {
      // The handshake installs only the first call's topic; the 20 calls
      // queued behind it share the next replacement instead of 20 serial ones.
      final first = realtime.subscribe('notes/*', (_) {});
      final queued = [
        for (var i = 0; i < 20; i++) realtime.subscribe('notes/$i', (_) {}),
      ];
      await held.future;
      release.complete();
      await Future.wait([first, ...queued]);
      final all = ['notes/*', for (var i = 0; i < 20; i++) 'notes/$i'];
      expect(syncs, [
        ['notes/*'],
        all,
      ]);

      // A denied topic fails the shared replacement without changing the
      // server, so the batch is retried call by call and only that call fails.
      syncs.clear();
      final outcomes = await Future.wait([
        for (final topic in ['notes/a', 'notes/b', 'denied/*', 'notes/c'])
          realtime
              .subscribe(topic, (_) {})
              .then<Object?>((_) => null, onError: (Object error) => error),
      ]);
      expect(outcomes[0], isNull);
      expect(outcomes[1], isNull);
      expect(
        outcomes[2],
        isA<PgBaseError>().having(
          (error) => error.code,
          'code',
          'access_denied',
        ),
      );
      expect(outcomes[3], isNull);
      expect(syncs, [
        [...all, 'notes/a'],
        [...all, 'notes/a', 'notes/b', 'denied/*', 'notes/c'],
        [...all, 'notes/a', 'notes/b'],
        [...all, 'notes/a', 'notes/b', 'denied/*'],
        [...all, 'notes/a', 'notes/b', 'notes/c'],
      ]);
      expect(realtime.status.state, RealtimeState.connected);
    } finally {
      await realtime.unsubscribe();
      await stream.close();
    }
  });
}

final class RealtimeFrames extends http.BaseClient {
  RealtimeFrames(this.frames);
  final String frames;
  int connections = 0;
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    if (request.method == 'POST')
      return http.StreamedResponse(const Stream.empty(), 204);
    connections++;
    return http.StreamedResponse(Stream.value(utf8.encode(frames)), 200);
  }
}
