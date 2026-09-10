import 'dart:math';

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
    final generation = _generation;
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
      if (response.status == 401 && generation == _generation) {
        _csrf = '';
        _cookie = null;
        _generation++;
      }
      throw pgBaseError(response);
    }
    // An auth operation increments the generation before it is dispatched.
    // Do not let an older, slower login replace the cookie that belongs to
    // the CSRF token adopted by the newer login.
    final setCookie = response.headers['set-cookie'];
    if (generation == _generation &&
        setCookie != null &&
        setCookie.contains('=')) {
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

  /// Freezes one preview's exact apply body and recovery identities.
  Future<ApprovedSchemaPlan> schemaPlan(
    JsonObject change, {
    CancellationToken? cancellation,
  }) async {
    final changeSnapshot = _snapshot(change);
    final preview = await _object(
      'POST',
      '/api/admin/collections/plan',
      body: changeSnapshot,
      cancellation: cancellation,
    );
    final checksum = preview['checksum'];
    final baseVersion = preview['baseVersion'];
    if (checksum is! JsonString || baseVersion is! JsonNumber)
      throw const PgBaseProtocolError('invalid_schema_plan');
    return ApprovedSchemaPlan._(
      preview: _snapshot(preview),
      applyBody: _snapshot(
        JsonObject({
          ...changeSnapshot.values,
          'checksum': checksum,
          'baseVersion': baseVersion,
        }),
      ),
      operationId: _operationId(),
      idempotencyKey: _idempotencyKey('schema-apply'),
    );
  }

  Future<JsonValue> applySchema(
    ApprovedSchemaPlan plan, {
    CancellationToken? cancellation,
  }) => request(
    'POST',
    '/api/admin/collections/apply',
    body: plan._applyBody,
    headers: {
      'pgbase-operation-id': plan.operationId,
      'idempotency-key': plan.idempotencyKey,
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

  /// Freezes the server-approved document and one replay-safe import identity.
  Future<ApprovedSchemaImportPlan> previewSchemaImport(
    JsonObject document, {
    CancellationToken? cancellation,
  }) async {
    final documentSnapshot = _snapshot(document);
    final preview = await _object(
      'POST',
      '/api/admin/schema/import/preview',
      body: documentSnapshot,
      cancellation: cancellation,
    );
    final approvedDocument = preview['document'];
    final checksum = preview['checksum'];
    final baseVersion = preview['baseVersion'];
    if (approvedDocument is! JsonObject ||
        checksum is! JsonString ||
        baseVersion is! JsonNumber) {
      throw const PgBaseProtocolError('invalid_schema_import_plan');
    }
    return ApprovedSchemaImportPlan._(
      preview: _snapshot(preview),
      applyBody: _snapshot(
        JsonObject({
          'document': approvedDocument,
          'checksum': checksum,
          'baseVersion': baseVersion,
        }),
      ),
      operationId: _operationId(),
      idempotencyKey: _idempotencyKey('schema-import'),
    );
  }

  Future<JsonValue> applySchemaImport(
    ApprovedSchemaImportPlan plan, {
    CancellationToken? cancellation,
  }) => request(
    'POST',
    '/api/admin/schema/import/apply',
    body: plan._applyBody,
    headers: {
      'pgbase-operation-id': plan.operationId,
      'idempotency-key': plan.idempotencyKey,
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

/// A schema preview and its immutable, replay-safe apply request.
final class ApprovedSchemaPlan {
  const ApprovedSchemaPlan._({
    required this._preview,
    required this._applyBody,
    required this.operationId,
    required this.idempotencyKey,
  });

  final JsonObject _preview;
  final JsonObject _applyBody;
  final String operationId;
  final String idempotencyKey;

  /// A defensive snapshot of the server preview.
  JsonObject get preview => _snapshot(_preview);
}

/// A schema-import preview and its immutable, replay-safe apply request.
final class ApprovedSchemaImportPlan {
  const ApprovedSchemaImportPlan._({
    required this._preview,
    required this._applyBody,
    required this.operationId,
    required this.idempotencyKey,
  });

  final JsonObject _preview;
  final JsonObject _applyBody;
  final String operationId;
  final String idempotencyKey;

  /// A defensive snapshot of the server preview.
  JsonObject get preview => _snapshot(_preview);
}

final Random _random = Random.secure();

String _operationId() {
  final bytes = List<int>.generate(16, (_) => _random.nextInt(256));
  bytes[6] = (bytes[6] & 0x0f) | 0x40;
  bytes[8] = (bytes[8] & 0x3f) | 0x80;
  final hex = bytes
      .map((value) => value.toRadixString(16).padLeft(2, '0'))
      .join();
  return '${hex.substring(0, 8)}-${hex.substring(8, 12)}-${hex.substring(12, 16)}-${hex.substring(16, 20)}-${hex.substring(20)}';
}

String _idempotencyKey(String prefix) => '$prefix-${_operationId()}';

JsonObject _snapshot(JsonObject value) =>
    decodeExactJson(encodeExactJson(value)) as JsonObject;
