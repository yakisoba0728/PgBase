import 'dart:io';

import 'package:http/io_client.dart';
import 'package:pgbase/pgbase.dart';

/// Creates a disposable principal and collection for the external Flutter web
/// consumer. It is intentionally separate from a developer's local install.
Future<void> main() async {
  final base = Uri.parse(_required('PGBASE_N7_URL'));
  final setupToken = _required('PGBASE_N7_SETUP_TOKEN');
  final suffix = DateTime.now().microsecondsSinceEpoch.toString();
  final admin = PgBaseAdminClient(base, httpClient: IOClient(HttpClient()));
  final user = PgBaseClient(base);
  const password = 'correct-horse-browser-N7';
  try {
    try {
      await admin.setup(setupToken, 'n7-admin@example.test', password);
    } on PgBaseError catch (error) {
      if (error.code != 'setup_unavailable') rethrow;
      await admin.login(
        'n7-admin@example.test',
        'correct-horse-battery-staple-N7',
      );
    }
    final collection = 'n7_browser_$suffix';
    final change = JsonObject({
      'operation': const JsonString('create'),
      'name': JsonString(collection),
      'fields': JsonArray([
        JsonObject({
          'name': const JsonString('title'),
          'type': const JsonString('text'),
        }),
        JsonObject({
          'name': const JsonString('metadata'),
          'type': const JsonString('jsonb'),
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
      '/api/admin/collections/${Uri.encodeComponent(collection)}/user-access',
      body: JsonObject({'enabled': const JsonBool(true)}),
    );
    final email = 'n7-browser-$suffix@example.test';
    await user.register(email, password);
    stdout.writeln('PGBASE_COLLECTION=$collection');
    stdout.writeln('PGBASE_EMAIL=$email');
    stdout.writeln('PGBASE_PASSWORD=$password');
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
