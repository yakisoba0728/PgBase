import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

void main() {
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
