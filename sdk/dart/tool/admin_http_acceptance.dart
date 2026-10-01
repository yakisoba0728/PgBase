import 'dart:io';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:http/io_client.dart';
import 'package:pgbase/pgbase.dart';

Future<void> main() async {
  final environment = Platform.environment;
  final baseUrl = environment['PGBASE_ACCEPTANCE_URL'];
  final email = environment['PGBASE_ACCEPTANCE_EMAIL'];
  final password = environment['PGBASE_ACCEPTANCE_PASSWORD'];
  final archivePath = environment['PGBASE_ACCEPTANCE_ARCHIVE'];
  final origin = environment['PGBASE_ACCEPTANCE_ORIGIN'];
  if (baseUrl == null ||
      email == null ||
      password == null ||
      archivePath == null ||
      origin == null) {
    throw StateError('missing acceptance environment');
  }

  final client = PgBaseAdminClient(
    Uri.parse(baseUrl),
    httpClient: _OriginClient(origin),
    sendOriginHeader: false,
  );
  try {
    await client.login(email, password);

    final suffix = '$pid-${DateTime.now().microsecondsSinceEpoch}';
    final accountEmail = 'sdk-dart-$suffix@example.test';
    final account = await client.createAdministrator(
      accountEmail,
      '${password}A1!',
    );
    _expect(
      _string(account, 'email') == accountEmail,
      'created administrator email',
    );
    final accountId = _string(account, 'id');
    final accounts = await client.administratorAccounts(limit: 100);
    _expect(
      _objects(
        accounts,
        'items',
      ).any((item) => _string(item, 'id') == accountId),
      'administrator listing',
    );
    _expect(
      !_bool(await client.setAdministratorActive(accountId, false), 'active'),
      'administrator deactivation',
    );
    await client.setAdministratorPassword(accountId, '${password}B2!');
    await client.revokeAdministratorSessions(accountId);

    final exported = await client.exportSchema();
    final approved = await client.previewSchemaImport(exported);
    final appliedValue = await client.applySchemaImport(approved);
    final applied = _object(appliedValue, 'schema import apply');
    final operationId = _string(applied, 'operationId');
    final operation = await client.schemaImportOperation(operationId);
    _expect(
      _string(operation, 'operationId') == operationId,
      'schema import operation',
    );
    _expect(
      _string(operation, 'status') == 'committed',
      'schema import committed',
    );

    final createJob = await client.createBackup();
    _expect(
      _string(createJob, 'operation') == 'create',
      'backup create operation',
    );
    final createReceipt = await _terminalBackup(client, createJob);
    _expect(
      _string(createReceipt, 'status') == 'completed',
      'backup creation completed: ${createReceipt.toDart()}',
    );
    final createdBackupId = _string(createReceipt, 'backupId');
    _expect(
      _objects(
        await client.backups(),
        'items',
      ).any((item) => _string(item, 'id') == createdBackupId),
      'created backup listing',
    );
    await client.deleteBackup(createdBackupId);

    final archive = File(archivePath);
    final uploaded = await client.uploadBackup(
      UploadSource(
        open: archive.openRead,
        length: await archive.length(),
        filename: 'sdk-acceptance.tar.gz',
        contentType: 'application/gzip',
      ),
    );
    final uploadedId = _string(uploaded, 'id');
    final downloaded = await client.downloadBackup(uploadedId);
    _expect(
      _equalBytes(downloaded, await archive.readAsBytes()),
      'backup download bytes',
    );
    _expect(
      _objects(
        await client.backups(),
        'items',
      ).any((item) => _string(item, 'id') == uploadedId),
      'uploaded backup listing',
    );
    await client.deleteBackup(uploadedId);
    _expect(
      !_objects(
        await client.backups(),
        'items',
      ).any((item) => _string(item, 'id') == uploadedId),
      'backup deletion',
    );

    await client.logout();
    stdout.write('sdk-admin-http-acceptance: ok\n');
  } finally {
    client.close();
  }
}

Future<JsonObject> _terminalBackup(
  PgBaseAdminClient client,
  JsonObject initial,
) async {
  var receipt = initial;
  final deadline = DateTime.now().add(const Duration(seconds: 20));
  while (_string(receipt, 'status') == 'running' &&
      DateTime.now().isBefore(deadline)) {
    await Future<void>.delayed(const Duration(milliseconds: 50));
    receipt = await client.backupJob(_string(initial, 'id'));
  }
  return receipt;
}

JsonObject _object(JsonValue value, String label) {
  if (value is! JsonObject) throw StateError('$label was not an object');
  return value;
}

String _string(JsonObject object, String key) {
  final value = object[key];
  if (value is! JsonString) throw StateError('$key was not a string');
  return value.value;
}

bool _bool(JsonObject object, String key) {
  final value = object[key];
  if (value is! JsonBool) throw StateError('$key was not a bool');
  return value.value;
}

List<JsonObject> _objects(JsonObject object, String key) {
  final value = object[key];
  if (value is! JsonArray || value.values.any((item) => item is! JsonObject)) {
    throw StateError('$key was not an object array');
  }
  return value.values.cast<JsonObject>();
}

bool _equalBytes(Uint8List left, Uint8List right) {
  if (left.length != right.length) return false;
  for (var index = 0; index < left.length; index++) {
    if (left[index] != right[index]) return false;
  }
  return true;
}

void _expect(bool condition, String label) {
  if (!condition) throw StateError('acceptance failed: $label');
}

final class _OriginClient extends http.BaseClient {
  _OriginClient(this.origin) : _inner = IOClient(HttpClient());

  final String origin;
  final http.Client _inner;

  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) {
    request.headers['origin'] = origin;
    return _inner.send(request);
  }

  @override
  void close() => _inner.close();
}
