import 'package:http/http.dart' as http;

import 'client.dart';
import 'errors.dart';
import 'exact_json.dart';
import 'transport.dart';

/// Administrator traffic is deliberately separate from [PgBaseClient].
/// Supply a cookie-capable `package:http` client for the target platform.
final class PgBaseAdminClient {
  PgBaseAdminClient(
    Uri baseUri, {
    http.Client? httpClient,
    this.sendOriginHeader = true,
  }) : transport = PgBaseTransport(baseUri, client: httpClient);

  final PgBaseTransport transport;

  /// Disable for BrowserClient: fetch supplies the same-origin Origin policy
  /// and browsers prohibit application code from setting that header.
  final bool sendOriginHeader;
  String _csrf = '';
  String? _cookie;
  int _generation = 0;

  void close() => transport.close();

  Future<JsonObject> setup(
    String setupToken,
    String email,
    String password, {
    CancellationToken? cancellation,
  }) => _adopt(
    '/api/admin/setup',
    JsonObject({
      'setupToken': JsonString(setupToken),
      'email': JsonString(email),
      'password': JsonString(password),
    }),
    cancellation,
  );

  Future<JsonObject> login(
    String email,
    String password, {
    CancellationToken? cancellation,
  }) => _adopt(
    '/api/admin/auth/login',
    JsonObject({'email': JsonString(email), 'password': JsonString(password)}),
    cancellation,
  );
  Future<JsonObject> resume({CancellationToken? cancellation}) =>
      _adopt('/api/admin/session', null, cancellation, method: 'GET');

  Future<void> logout({CancellationToken? cancellation}) async {
    final generation = _generation;
    try {
      await request(
        'POST',
        '/api/admin/auth/logout',
        cancellation: cancellation,
      );
    } finally {
      if (generation == _generation) {
        _csrf = '';
        _cookie = null;
        _generation++;
      }
    }
  }

  /// Fully integrated escape hatch for every documented administrator route.
  /// It preserves the cookie/CSRF boundary while new server endpoints are added.
  Future<JsonValue> request(
    String method,
    String path, {
    JsonValue? body,
    Map<String, String>? query,
    Map<String, String>? headers,
    CancellationToken? cancellation,
  }) async {
    final normalized = method.toUpperCase();
    final requestHeaders = <String, String>{
      if (sendOriginHeader) 'origin': transport.baseUri.origin,
      if (_cookie != null) 'cookie': _cookie!,
      ...?headers,
    };
    if (normalized != 'GET' && _csrf.isNotEmpty)
      requestHeaders['x-pgbase-csrf'] = _csrf;
    final response = await transport.json(
      normalized,
      path,
      headers: requestHeaders,
      body: body,
      query: query,
      cancellation: cancellation,
    );
    if (response.status < 200 || response.status >= 300) {
      if (response.status == 401) {
        _csrf = '';
        _cookie = null;
        _generation++;
      }
      throw pgBaseError(response);
    }
    final setCookie = response.headers['set-cookie'];
    if (setCookie != null && setCookie.contains('=')) {
      _cookie = setCookie.split(';').first;
    }
    if (response.status == 204 || response.bytes.isEmpty)
      return const JsonNull();
    try {
      return decodeExactJson(response.bytes);
    } on PgBaseProtocolError {
      throw const PgBaseProtocolError('invalid_admin_success_json');
    }
  }

  Future<JsonObject> catalog({CancellationToken? cancellation}) =>
      _object('GET', '/api/admin/collections', cancellation: cancellation);
  Future<JsonObject> schemaPlan(
    JsonObject change, {
    CancellationToken? cancellation,
  }) => _object(
    'POST',
    '/api/admin/collections/plan',
    body: change,
    cancellation: cancellation,
  );
  Future<JsonValue> applySchema(
    JsonObject approvedBody,
    String operationId,
    String idempotencyKey, {
    CancellationToken? cancellation,
  }) => request(
    'POST',
    '/api/admin/collections/apply',
    body: approvedBody,
    headers: {
      'pgbase-operation-id': operationId,
      'idempotency-key': idempotencyKey,
    },
    cancellation: cancellation,
  );
  Future<JsonObject> schemaOperation(
    String id, {
    CancellationToken? cancellation,
  }) => _object(
    'GET',
    '/api/admin/schema-operations/${Uri.encodeComponent(id)}',
    cancellation: cancellation,
  );
  Future<JsonObject> exportSchema({CancellationToken? cancellation}) =>
      _object('GET', '/api/admin/schema/export', cancellation: cancellation);
  Future<JsonObject> previewSchemaImport(
    JsonObject document, {
    CancellationToken? cancellation,
  }) => _object(
    'POST',
    '/api/admin/schema/import/preview',
    body: document,
    cancellation: cancellation,
  );
  Future<JsonValue> applySchemaImport(
    JsonObject approvedBody,
    String operationId,
    String idempotencyKey, {
    CancellationToken? cancellation,
  }) => request(
    'POST',
    '/api/admin/schema/import/apply',
    body: approvedBody,
    headers: {
      'pgbase-operation-id': operationId,
      'idempotency-key': idempotencyKey,
    },
    cancellation: cancellation,
  );
  Future<JsonObject> schemaImportOperation(
    String id, {
    CancellationToken? cancellation,
  }) => _object(
    'GET',
    '/api/admin/schema/import/operations/${Uri.encodeComponent(id)}',
    cancellation: cancellation,
  );

  Future<JsonObject> logs({
    int? before,
    int? statusClass,
    CancellationToken? cancellation,
  }) => _object(
    'GET',
    '/api/admin/logs',
    query: {
      if (before != null) 'before': '$before',
      if (statusClass != null) 'statusClass': '$statusClass',
    },
    cancellation: cancellation,
  );
  Future<JsonObject> jobs({CancellationToken? cancellation}) =>
      _object('GET', '/api/admin/jobs', cancellation: cancellation);
  Future<void> configureJob(
    String id,
    String schedule,
    bool enabled, {
    CancellationToken? cancellation,
  }) async => request(
    'PUT',
    '/api/admin/jobs/${Uri.encodeComponent(id)}',
    body: JsonObject({
      'schedule': JsonString(schedule),
      'enabled': JsonBool(enabled),
    }),
    cancellation: cancellation,
  );
  Future<void> queueJob(String id, {CancellationToken? cancellation}) async =>
      request(
        'POST',
        '/api/admin/jobs/${Uri.encodeComponent(id)}/run',
        cancellation: cancellation,
      );

  Future<JsonObject> fileStorageSettings({CancellationToken? cancellation}) =>
      _object('GET', '/api/admin/storage/files', cancellation: cancellation);
  Future<JsonObject> updateFileStorage(
    JsonObject input, {
    CancellationToken? cancellation,
  }) => _object(
    'PUT',
    '/api/admin/storage/files',
    body: input,
    cancellation: cancellation,
  );
  Future<void> testFileStorage({CancellationToken? cancellation}) async =>
      request(
        'POST',
        '/api/admin/storage/files/test',
        cancellation: cancellation,
      );
  Future<JsonObject> backupStorageSettings({CancellationToken? cancellation}) =>
      _object('GET', '/api/admin/storage/backups', cancellation: cancellation);
  Future<JsonObject> updateBackupStorage(
    JsonObject input, {
    CancellationToken? cancellation,
  }) => _object(
    'PUT',
    '/api/admin/storage/backups',
    body: input,
    cancellation: cancellation,
  );
  Future<void> testBackupStorage({CancellationToken? cancellation}) async =>
      request(
        'POST',
        '/api/admin/storage/backups/test',
        cancellation: cancellation,
      );
  Future<JsonObject> previewFileStorageMigration(
    JsonObject input, {
    CancellationToken? cancellation,
  }) => _object(
    'POST',
    '/api/admin/storage/files/migrations/preview',
    body: input,
    cancellation: cancellation,
  );
  Future<JsonObject> startFileStorageMigration(
    JsonObject input,
    String operationId, {
    CancellationToken? cancellation,
  }) => _object(
    'POST',
    '/api/admin/storage/files/migrations',
    body: input,
    headers: {'pgbase-operation-id': operationId},
    cancellation: cancellation,
  );
  Future<JsonObject> fileStorageMigration(
    String id, {
    CancellationToken? cancellation,
  }) => _object(
    'GET',
    '/api/admin/storage/files/migrations/${Uri.encodeComponent(id)}',
    cancellation: cancellation,
  );
  Future<JsonObject> latestFileStorageMigration({
    CancellationToken? cancellation,
  }) => _object(
    'GET',
    '/api/admin/storage/files/migrations/latest',
    cancellation: cancellation,
  );
  Future<void> resumeFileStorageMigration(
    String id, {
    CancellationToken? cancellation,
  }) async => request(
    'POST',
    '/api/admin/storage/files/migrations/${Uri.encodeComponent(id)}/resume',
    cancellation: cancellation,
  );
  Future<void> cancelFileStorageMigration(
    String id, {
    CancellationToken? cancellation,
  }) async => request(
    'POST',
    '/api/admin/storage/files/migrations/${Uri.encodeComponent(id)}/cancel',
    cancellation: cancellation,
  );

  Future<JsonObject> users({CancellationToken? cancellation}) =>
      _object('GET', '/api/admin/users', cancellation: cancellation);
  Future<JsonValue> setUserActive(
    String id,
    bool active, {
    CancellationToken? cancellation,
  }) => request(
    'PATCH',
    '/api/admin/users/${Uri.encodeComponent(id)}',
    body: JsonObject({'active': JsonBool(active)}),
    cancellation: cancellation,
  );
  Future<void> revokeUserSessions(
    String id, {
    CancellationToken? cancellation,
  }) async => request(
    'POST',
    '/api/admin/users/${Uri.encodeComponent(id)}/revoke-sessions',
    cancellation: cancellation,
  );
  Future<JsonObject> impersonateUser(
    String id, {
    int durationSeconds = 300,
    CancellationToken? cancellation,
  }) => _object(
    'POST',
    '/api/admin/users/${Uri.encodeComponent(id)}/impersonate',
    body: JsonObject({'durationSeconds': JsonNumber('$durationSeconds')}),
    cancellation: cancellation,
  );
  Future<JsonObject> realtimeStatus({CancellationToken? cancellation}) =>
      _object('GET', '/api/admin/realtime/status', cancellation: cancellation);
  Future<void> retryRealtimeEvent(
    String eventId, {
    CancellationToken? cancellation,
  }) async => request(
    'POST',
    '/api/admin/realtime/${Uri.encodeComponent(eventId)}/retry',
    cancellation: cancellation,
  );
  Future<JsonObject> system({CancellationToken? cancellation}) =>
      _object('GET', '/api/admin/system', cancellation: cancellation);

  Future<JsonObject> _adopt(
    String path,
    JsonObject? body,
    CancellationToken? cancellation, {
    String method = 'POST',
  }) async {
    final generation = ++_generation;
    final value = await request(
      method,
      path,
      body: body,
      cancellation: cancellation,
    );
    if (value is! JsonObject || value['csrfToken'] is! JsonString)
      throw const PgBaseProtocolError('invalid_admin_session_response');
    if (generation == _generation)
      _csrf = (value['csrfToken']! as JsonString).value;
    return value;
  }

  Future<JsonObject> _object(
    String method,
    String path, {
    JsonObject? body,
    Map<String, String>? query,
    Map<String, String>? headers,
    CancellationToken? cancellation,
  }) async {
    final value = await request(
      method,
      path,
      body: body,
      query: query,
      headers: headers,
      cancellation: cancellation,
    );
    if (value is! JsonObject)
      throw const PgBaseProtocolError('invalid_admin_object_response');
    return value;
  }
}
