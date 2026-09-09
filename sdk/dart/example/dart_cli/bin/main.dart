import 'dart:io';

import 'package:pgbase/pgbase.dart';

Future<void> main(List<String> arguments) async {
  final base = Uri.parse(Platform.environment['PGBASE_URL'] ?? 'http://127.0.0.1:8091/');
  final email = Platform.environment['PGBASE_EMAIL'];
  final password = Platform.environment['PGBASE_PASSWORD'];
  if (email == null || password == null) {
    stderr.writeln('Set PGBASE_URL, PGBASE_EMAIL, and PGBASE_PASSWORD to run this example.');
    exitCode = 64;
    return;
  }
  final client = PgBaseClient(base);
  try {
    final session = await client.login(email, password);
    final page = await client.records('notes').list();
    stdout.writeln('Signed in as ${session.user.email}; ${page.items.length} notes visible.');
  } finally {
    client.close();
  }
}
