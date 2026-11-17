import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

void main() {
  test('a draining subscription cannot adopt another identity', () async {
    final transport = _RealtimeTransport(holdFirstAbort: true);
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test'),
      httpClient: transport,
      auth: AuthStore(initialToken: 'account-A'),
    );
    try {
      await client.realtime.subscribe('notes/*', (_) {});
      await client.realtime.unsubscribe();
      final pending = client.realtime.subscribe('notes/second', (_) {});
      final rejected = expectLater(pending, throwsA(isA<PgBaseCancellation>()));
      await client.auth.save('account-B');
      await transport.connections.first.close();
      await rejected;
      expect(transport.connections.length, 1);
    } finally {
      await client.realtime.unsubscribe();
      for (final connection in transport.connections) {
        await connection.close();
      }
    }
  });

  test('an old disposer cannot stop a replacement session listener', () async {
    final transport = _RealtimeTransport();
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test'),
      httpClient: transport,
      auth: AuthStore(initialToken: 'account-A'),
    );
    final received = Completer<String>();
    void listener(RealtimeEvent event) {
      received.complete(event.record['id']!.toDart() as String);
    }

    try {
      final oldDispose = await client.realtime.subscribe('notes/*', listener);
      await client.auth.save('account-B');
      await client.realtime.subscribe('notes/*', listener);
      await oldDispose();
      expect(client.realtime.status.state, RealtimeState.connected);
      expect(transport.connections.last.request.headers['authorization'], 'Bearer account-B');
      transport.connections.last.write(
        'event: notes/*\ndata: {"action":"create","record":{"id":"B-record"}}\n\n',
      );
      expect(await received.future, 'B-record');
    } finally {
      await client.realtime.unsubscribe();
    }
  });

  for (final application in [false, true]) {
    test('an old ${application ? "application" : "collection"} listener error cannot enter a replacement session status', () async {
      final transport = _RealtimeTransport();
      final client = PgBaseClient(
        Uri.parse('http://pgbase.test'),
        httpClient: transport,
        auth: AuthStore(initialToken: 'account-A'),
      );
      final statuses = <RealtimeStatus>[];
      final stopObserving = client.realtime.onStatus(statuses.add);
      final started = Completer<void>();
      final oldResult = Completer<void>();
      Future<void> listener(dynamic _) {
        started.complete();
        return oldResult.future;
      }

      Future<Future<void> Function()> subscribe(FutureOr<void> Function(dynamic) callback) => application
          ? client.realtime.subscribeApplication('private', callback)
          : client.realtime.subscribe('notes/*', callback);
      try {
        await subscribe(listener);
        transport.connections.first.write(application
            ? 'event: @app/cHJpdmF0ZQ\ndata: {"value":"account-A"}\n\n'
            : 'event: notes/*\ndata: {"action":"create","record":{"id":"A-record"}}\n\n');
        await started.future;
        await client.auth.save('account-B');
        await subscribe((_) {});
        final observed = statuses.length;
        oldResult.completeError(StateError('account-A private listener failure'));
        await Future<void>.delayed(Duration.zero);
        expect(client.realtime.status.state, RealtimeState.connected);
        expect(client.realtime.status.error, isNull);
        expect(statuses.length, observed);
      } finally {
        stopObserving();
        await client.realtime.unsubscribe();
      }
    });
  }
}

final class _RealtimeTransport extends http.BaseClient {
  _RealtimeTransport({this.holdFirstAbort = false});
  final bool holdFirstAbort;
  final connections = <_Connection>[];

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    if (request.method == 'POST') return http.StreamedResponse(const Stream.empty(), 204);
    final connection = _Connection(request);
    connections.add(connection);
    if ((!holdFirstAbort || connections.length > 1) && request is http.AbortableRequest) {
      request.abortTrigger?.then((_) => connection.close());
    }
    connection.write('event: PB_CONNECT\ndata: {"clientId":"session"}\n\n');
    return http.StreamedResponse(connection.body.stream, 200);
  }
}

final class _Connection {
  _Connection(this.request);
  final http.BaseRequest request;
  final body = StreamController<List<int>>();
  void write(String frame) => body.add(utf8.encode(frame));
  Future<void> close() async {
    if (!body.isClosed) await body.close();
  }
}
