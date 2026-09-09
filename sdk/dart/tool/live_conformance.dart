import 'dart:convert';
import 'dart:io';

import 'package:http/io_client.dart';
import 'package:pgbase/pgbase.dart';

Future<void> main() async {
  final base = Uri.parse(_required('PGBASE_N7_URL'));
  final setupToken = _required('PGBASE_N7_SETUP_TOKEN');
  final suffix = DateTime.now().microsecondsSinceEpoch.toString();
  final admin = PgBaseAdminClient(base, httpClient: IOClient(HttpClient()));
  final user = PgBaseClient(base);
  try {
    const adminEmail = 'n7-admin@example.test';
    const adminPassword = 'correct-horse-battery-staple-N7';
    try {
      await admin.setup(setupToken, adminEmail, adminPassword);
    } on PgBaseError catch (error) {
      if (error.code != 'setup_unavailable') rethrow;
      await admin.login(adminEmail, adminPassword);
    }
    final name = 'n7_notes_$suffix';
    final change = JsonObject({
      'operation': const JsonString('create'),
      'name': JsonString(name),
      'fields': JsonArray([
        JsonObject({
          'name': const JsonString('title'),
          'type': const JsonString('text'),
        }),
        JsonObject({
          'name': const JsonString('attachment'),
          'type': const JsonString('file'),
          'options': JsonObject({
            'maxFileSize': JsonNumber('1048576'),
            'mimeTypes': JsonArray([const JsonString('text/plain')]),
          }),
        }),
      ]),
    });
    final plan = await admin.schemaPlan(change);
    await admin.applySchema(plan);
    await admin.request(
      'PUT',
      '/api/admin/collections/${Uri.encodeComponent(name)}/user-access',
      body: JsonObject({'enabled': const JsonBool(true)}),
    );
    await user.register(
      'n7-user-$suffix@example.test',
      'correct-horse-battery-staple-N7',
    );
    final records = user.records(name);
    final created = await records.createWithEtag(
      JsonObject({'title': const JsonString('exact')}),
      'create-record-key-$suffix',
    );
    final updated = await records.updateWithEtag(
      (created.record['id']! as JsonString).value,
      JsonObject({'title': const JsonString('updated')}),
      created.etag!,
    );
    if ((updated.record['title']! as JsonString).value != 'updated')
      throw StateError('record update did not round trip');
    final recordId = (updated.record['id']! as JsonString).value;
    final event = records
        .events(maxReconnects: 0)
        .first
        .timeout(const Duration(seconds: 10));
    // Give the subscription one event-loop turn to establish its stream before
    // the batch commits the next record event.
    await Future<void>.delayed(const Duration(milliseconds: 100));
    final batchResult = await user.batch([
      BatchOperation(
        JsonObject({
          'operation': const JsonString('create'),
          'collection': JsonString(name),
          'input': JsonObject({'title': const JsonString('batched')}),
          'idempotencyKey': JsonString('batch-create-key-$suffix'),
        }),
      ),
    ]);
    if (batchResult['results'] is! JsonArray)
      throw StateError('batch did not return results');
    final received = await event;
    if (received.collection != name || received.operation != 'create')
      throw StateError('SSE did not deliver the batch create');
    const payload = 'N7 file payload';
    final uploaded = await user
        .files(name, recordId)
        .upload(
          'attachment',
          UploadSource(
            open: () => Stream<List<int>>.value(utf8.encode(payload)),
            length: utf8.encode(payload).length,
            filename: 'n7.txt',
            contentType: 'text/plain',
          ),
        );
    final files = await user.files(name, recordId).list('attachment');
    if (files.length != 1 || files.single.id != uploaded.id)
      throw StateError('file upload did not round trip');
    final token = await user
        .files(name, recordId)
        .mintDownloadToken(uploaded.id);
    if (token.isEmpty ||
        !user.files(name, recordId).downloadUri(uploaded.id, token).hasQuery)
      throw StateError('file download token was not usable');
    final downloaded = await user
        .files(name, recordId)
        .download(uploaded.id, token);
    if (utf8.decode(downloaded) != payload)
      throw StateError('file download did not preserve bytes');
    await user.files(name, recordId).delete(uploaded.id);
    if ((await user.files(name, recordId).list('attachment')).isNotEmpty)
      throw StateError('file delete did not clean up');
    stdout.writeln('N7 live conformance passed for $name');
  } finally {
    user.close();
    admin.close();
  }
}

String _required(String name) {
  final value = Platform.environment[name];
  if (value == null || value.isEmpty) throw StateError('$name is required');
  return value;
}
