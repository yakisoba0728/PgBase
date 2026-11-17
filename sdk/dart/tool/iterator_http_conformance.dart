import 'dart:async';
import 'dart:io';

import 'package:http/http.dart' as http;
import 'package:pgbase/pgbase.dart';

Future<void> main() async {
  final transport = _CountingClient();
  final client = PgBaseClient(
    Uri.parse(Platform.environment['PGBASE_SDK_URL']!),
    httpClient: transport,
  );
  final token = Platform.environment['PGBASE_SDK_TOKEN']!;
  final other = Platform.environment['PGBASE_SDK_OTHER_TOKEN']!;
  try {
    for (final limit in [1, 2]) {
      await client.auth.save(token);
      final before = transport.requests;
      final iterator = StreamIterator(
        client.records('iterator_notes').iterate(Query(limit: limit)),
      );
      try {
        if (!await iterator.moveNext()) throw StateError('missing owner row');
        await client.auth.save(other);
        try {
          await iterator.moveNext();
          throw StateError('stale iterator did not reject');
        } on PgBaseError catch (error) {
          if (error.status != 403 || error.code != 'session_changed') rethrow;
        }
        if (transport.requests != before + 1) {
          throw StateError('stale iterator made another HTTP request');
        }
      } finally {
        await iterator.cancel();
      }
    }
    await client.auth.save(token);
    final rows = await client
        .records('iterator_notes')
        .iterate(const Query(limit: 1))
        .toList();
    final titles = rows.map((row) => row['title']!.toDart() as String).toList()
      ..sort();
    if (titles.join(',') != 'private-first,private-second') {
      throw StateError('stable pagination lost owner rows');
    }
    await client.auth.save(other);
    final otherRows = await client
        .records('iterator_notes')
        .iterate(const Query(limit: 1))
        .toList();
    if (otherRows.isNotEmpty)
      throw StateError('fresh replacement iterator exposed owner rows');
    print(
      'Dart iterator HTTP conformance passed: buffered/page boundaries, stable pagination, fresh-user isolation',
    );
  } finally {
    client.close();
    await client.auth.close();
  }
}

final class _CountingClient extends http.BaseClient {
  final http.Client _inner = http.Client();
  int requests = 0;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) {
    requests++;
    return _inner.send(request);
  }

  @override
  void close() => _inner.close();
}
