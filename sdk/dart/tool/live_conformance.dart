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
    await _adminCookieCsrfConformance(base, adminEmail, adminPassword);
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
        JsonObject({
          'name': const JsonString('metadata'),
          'type': const JsonString('jsonb'),
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
      JsonObject({
        'title': const JsonString('exact'),
        'metadata': JsonObject({
          'large': JsonNumber('900719925474099312345.0001'),
        }),
      }),
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
    final exact =
        ((await records.get(recordId))['metadata']! as JsonObject)['large'];
    if (exact is! JsonNumber || exact.source != '900719925474099312345.0001')
      throw StateError('JSONB number was not lossless');
    try {
      await records.update(
        recordId,
        JsonObject({'title': const JsonString('stale')}),
        created.etag!,
      );
      throw StateError('stale ETag unexpectedly updated a record');
    } on PgBaseError catch (error) {
      if (error.status != 412 || error.etag == null)
        throw StateError('stale ETag did not return 412 with current ETag');
    }
    final replayKey = 'replay-create-key-$suffix';
    final replayInput = JsonObject({'title': const JsonString('replay')});
    final replayOne = await records.createWithEtag(replayInput, replayKey);
    final replayTwo = await records.createWithEtag(replayInput, replayKey);
    final replayOneID = (replayOne.record['id']! as JsonString).value;
    final replayTwoID = (replayTwo.record['id']! as JsonString).value;
    if (replayOneID != replayTwoID)
      throw StateError(
        'idempotent replay created $replayOneID and $replayTwoID',
      );
    try {
      await records.create(
        JsonObject({'title': const JsonString('replay conflict')}),
        replayKey,
      );
      throw StateError('idempotency conflict unexpectedly succeeded');
    } on PgBaseError catch (error) {
      if (error.status != 409 || error.code != 'idempotency_conflict')
        throw StateError('wrong idempotency conflict result');
    }
    final firstPage = await records.list(const Query(limit: 1));
    final iterated = await records.iterate(const Query(limit: 1)).toList();
    if (firstPage.nextCursor == null ||
        !iterated.any(
          (record) =>
              record['id'] is JsonString &&
              (record['id']! as JsonString).value == recordId,
        )) {
      throw StateError('cursor iteration did not traverse the collection');
    }
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
    final beforeFailedBatch = (await records.list()).items.length;
    try {
      await user.batch([
        BatchOperation(
          JsonObject({
            'operation': const JsonString('create'),
            'collection': JsonString(name),
            'input': JsonObject({'title': const JsonString('rollback')}),
            'idempotencyKey': JsonString('rollback-create-key-$suffix'),
          }),
        ),
        BatchOperation(
          JsonObject({
            'operation': const JsonString('create'),
            'collection': const JsonString('missing_collection'),
            'input': JsonObject({'title': const JsonString('invalid')}),
            'idempotencyKey': JsonString('rollback-invalid-key-$suffix'),
          }),
        ),
      ]);
      throw StateError('invalid batch unexpectedly succeeded');
    } on PgBaseError {
      if ((await records.list()).items.length != beforeFailedBatch)
        throw StateError('failed batch partially committed');
    }
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
    final revoked = PgBaseClient(base);
    try {
      final second = await revoked.register(
        'n7-revoked-$suffix@example.test',
        'correct-horse-battery-staple-N7',
      );
      await admin.revokeUserSessions(second.user.id);
      await _expectUnauthorized(revoked.session());
    } finally {
      revoked.close();
    }
    await user.logout();
    await _expectUnauthorized(user.session());
    stdout.writeln('N7 live conformance passed for $name');
  } finally {
    user.close();
    admin.close();
  }
}

Future<void> _expectUnauthorized(Future<Object?> request) async {
  try {
    await request;
    throw StateError('revoked session unexpectedly remained valid');
  } on PgBaseError catch (error) {
    if (error.status != 401) rethrow;
  }
}

String _required(String name) {
  final value = Platform.environment[name];
  if (value == null || value.isEmpty) throw StateError('$name is required');
  return value;
}

Future<void> _adminCookieCsrfConformance(
  Uri base,
  String email,
  String password,
) async {
  final client = HttpClient();
  try {
    final unauthenticated = await _rawAdmin(
      client,
      base,
      'GET',
      '/api/admin/session',
    );
    if (unauthenticated.status != 401)
      throw StateError('admin unauthenticated route was not 401');
    final login = await _rawAdmin(
      client,
      base,
      'POST',
      '/api/admin/auth/login',
      body: jsonEncode({'email': email, 'password': password}),
    );
    final cookie = login.headers
        .value(HttpHeaders.setCookieHeader)
        ?.split(';')
        .first;
    final decoded =
        jsonDecode(utf8.decode(login.bytes)) as Map<String, dynamic>;
    final csrf = decoded['csrfToken'] as String?;
    if (login.status != 200 || cookie == null || csrf == null)
      throw StateError('admin cookie login did not return session material');
    final resumed = await _rawAdmin(
      client,
      base,
      'GET',
      '/api/admin/session',
      cookie: cookie,
    );
    if (resumed.status != 200) throw StateError('admin cookie resume failed');
    for (final value in [null, 'wrong-csrf']) {
      final rejected = await _rawAdmin(
        client,
        base,
        'POST',
        '/api/admin/auth/logout',
        cookie: cookie,
        csrf: value,
      );
      if (rejected.status != 403) {
        throw StateError(
          'admin CSRF rejection was ${rejected.status}: ${utf8.decode(rejected.bytes)}',
        );
      }
    }
  } finally {
    client.close(force: true);
  }
}

Future<_RawResponse> _rawAdmin(
  HttpClient client,
  Uri base,
  String method,
  String path, {
  String? body,
  String? cookie,
  String? csrf,
}) async {
  final request = await client.openUrl(method, base.resolve(path));
  request.headers.set(HttpHeaders.acceptHeader, 'application/json');
  request.headers.set('Origin', base.origin);
  if (cookie != null) request.headers.set(HttpHeaders.cookieHeader, cookie);
  if (csrf != null) request.headers.set('X-PgBase-CSRF', csrf);
  if (body != null) {
    request.headers.contentType = ContentType.json;
    request.write(body);
  }
  final response = await request.close();
  return _RawResponse(
    response.statusCode,
    response.headers,
    await response.fold<List<int>>([], (out, chunk) => out..addAll(chunk)),
  );
}

final class _RawResponse {
  const _RawResponse(this.status, this.headers, this.bytes);
  final int status;
  final HttpHeaders headers;
  final List<int> bytes;
}
