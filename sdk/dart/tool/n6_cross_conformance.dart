import 'dart:convert';
import 'dart:io';

import 'package:pgbase/pgbase.dart';

Future<void> main() async {
  final base = Uri.parse(_required('N6_URL'));
  final email = _required('N6_EMAIL');
  final password = _required('N6_PASSWORD');
  const collection = 'n6_tasks';
  const recordId = '01a087e8-8169-7c5c-a071-fa2ba40577ff';
  const fileId = '8f32b70a-b0a8-4b78-8840-825bc892e7ef';
  final client = PgBaseClient(base);
  try {
    await client.login(email, password);
    final record = await client.records(collection).getWithEtag(recordId);
    final large = ((record.record['metadata']! as JsonObject)['large'] as JsonNumber).source;
    if (large != '900719925474099312345.0001' || record.etag == null)
      throw StateError('exact value or strong ETag missing');
    final files = client.files(collection, recordId);
    final token = await files.mintDownloadToken(fileId);
    final bytes = await files.download(fileId, token);
    if (utf8.decode(bytes) != 'N6 external module file payload')
      throw StateError('cross-flow file bytes differed');
    final event = client.records(collection).events(maxReconnects: 0).first;
    await Future<void>.delayed(const Duration(milliseconds: 100));
    await client.records(collection).create(
      JsonObject({
        'title': const JsonString('N7 final cross-flow'),
        'metadata': JsonObject({'large': JsonNumber('900719925474099312345.0001')}),
      }),
      'n7-final-cross-${DateTime.now().microsecondsSinceEpoch}',
    );
    final received = await event.timeout(const Duration(seconds: 10));
    if (received.collection != collection) throw StateError('SSE collection differed');
    stdout.writeln('N6 cross conformance passed etag=${record.etag} event=${received.eventId}');
  } finally {
    client.close();
  }
}

String _required(String name) {
  final value = Platform.environment[name];
  if (value == null || value.isEmpty) throw StateError('$name is required');
  return value;
}
