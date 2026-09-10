import 'dart:io';

import 'package:pgbase/pgbase.dart';

Future<void> main() async {
  final base = Uri.parse(Platform.environment['PGBASE_SDK_URL']!);
  final token = Platform.environment['PGBASE_SDK_TOKEN']!;
  final client = PgBaseClient(base);
  try {
    await client.auth.save(token);
    final records = client.records('notes');
    await records.create(
      JsonObject({'title': const JsonString('dart safe filter')}),
      'dart-safe-filter-conformance-0001',
    );
    final literal = client.filter(
      'title = {:title}',
      {'title': 'dart safe filter'},
    );
    final bound = await records.getList(
      1,
      30,
      PocketBaseListOptions(filter: literal, sort: '@rowid'),
    );
    if (bound.totalItems != 1 || bound.totalPages != 1 || bound.items.length != 1) {
      throw StateError('Dart PocketBase numbered page did not bind one record');
    }
    final attack = client.filter(
      'title = {:title}',
      {'title': 'dart safe filter" || title != "dart safe filter'},
    );
    final noMatch = await records.getList(1, 30, PocketBaseListOptions(filter: attack));
    if (noMatch.items.isNotEmpty) throw StateError('Dart filter-looking input widened query');
    final skipped = await records.getList(
      1,
      30,
      PocketBaseListOptions(filter: literal, skipTotal: true),
    );
    if (skipped.totalItems != -1 || skipped.totalPages != -1) {
      throw StateError('Dart PocketBase skipTotal envelope mismatch');
    }
  } finally {
    client.close();
  }
}
