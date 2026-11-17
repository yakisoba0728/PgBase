import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

final _sessionChanged = throwsA(
  isA<PgBaseError>()
      .having((error) => error.status, 'status', 403)
      .having((error) => error.code, 'code', 'session_changed'),
);

void main() {
  for (final kind in ['json', 'download', 'upload']) {
    for (final status in [200, 401]) {
      test('administrator $kind rejects stale $status body', () async {
        final transport = DelayedAdminClient(status);
        final client = PgBaseAdminClient(
          Uri.parse('http://pgbase.test'),
          httpClient: transport,
        );
        await client.resume();
        final Future<Object?> pending;
        switch (kind) {
          case 'download':
            pending = client.downloadBackup('backup');
          case 'upload':
            pending = client.uploadBackup(
              UploadSource(
                open: () => Stream.value([1, 2, 3]),
                length: 3,
                filename: 'backup.tar.gz',
              ),
            );
          default:
            pending = client.catalog();
        }
        final rejected = expectLater(pending, _sessionChanged);
        await transport.started.future;
        await client.resume();
        transport.body.add(
          utf8.encode(
            status == 200
                ? '{"private":"old administrator"}'
                : '{"error":{"code":"unauthorized"}}',
          ),
        );
        await transport.body.close();
        await rejected;
        await client.configureJob('daily', '0 0 * * *', true);
        expect(transport.lastHeaders['cookie'], 'admin-session=2');
        expect(transport.lastHeaders['x-pgbase-csrf'], 'csrf-2');
      });
    }
  }

  test('an old transport failure belongs to the old session', () async {
    final transport = DelayedAdminClient(200);
    final client = PgBaseAdminClient(
      Uri.parse('http://pgbase.test'),
      httpClient: transport,
    );
    await client.resume();
    final rejected = expectLater(client.catalog(), _sessionChanged);
    await transport.started.future;
    await client.resume();
    transport.body.addError(http.ClientException('old connection lost'));
    await transport.body.close();
    await rejected;
  });
}

final class DelayedAdminClient extends http.BaseClient {
  DelayedAdminClient(this.status);
  final int status;
  int sessions = 0;
  final started = Completer<void>();
  final body = StreamController<List<int>>();
  Map<String, String> lastHeaders = {};

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    lastHeaders = Map.of(request.headers);
    if (request.url.path == '/api/admin/session') {
      sessions++;
      return http.StreamedResponse(
        Stream.value(utf8.encode('{"csrfToken":"csrf-$sessions"}')),
        200,
        headers: {'set-cookie': 'admin-session=$sessions; HttpOnly'},
      );
    }
    if (started.isCompleted) {
      return http.StreamedResponse(const Stream.empty(), 204);
    }
    await request.finalize().toBytes();
    started.complete();
    return http.StreamedResponse(body.stream, status);
  }
}
