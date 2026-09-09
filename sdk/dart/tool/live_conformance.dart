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
      ]),
    });
    final plan = await admin.schemaPlan(change);
    await admin.applySchema(
      JsonObject({
        ...change.values,
        'checksum': plan['checksum']!,
        'baseVersion': plan['baseVersion']!,
      }),
      '01900000-0000-7000-8000-${suffix.substring(suffix.length - 12)}',
      'schema-apply-key-$suffix',
    );
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
