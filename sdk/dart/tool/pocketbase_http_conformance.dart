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
    final tags = client.records('modifier_tags');
    final modifierRecords = client.records('modifier_notes');
    final firstTag = await tags.create(
      JsonObject({'name': const JsonString('dart-first')}),
      'dart-modifier-tag-first-0001',
    );
    final secondTag = await tags.create(
      JsonObject({'name': const JsonString('dart-second')}),
      'dart-modifier-tag-second-0001',
    );
    final thirdTag = await tags.create(
      JsonObject({'name': const JsonString('dart-third')}),
      'dart-modifier-tag-third-0001',
    );
    String id(JsonObject value) => (value['id']! as JsonString).value;
    final modifier = await modifierRecords.createWithEtag(
      JsonObject({
        'title': const JsonString('dart modifier'),
        'count': JsonNumber('10'),
        'roles': JsonArray([const JsonString('b'), const JsonString('d')]),
        'tags': JsonArray([JsonString(id(firstTag)), JsonString(id(secondTag))]),
      }),
      'dart-modifier-record-0001',
    );
    final modifierID = id(modifier.record);
    var updated = await modifierRecords.updateWithEtag(
      modifierID,
      JsonObject({'count+': JsonNumber('5')}),
      modifier.etag!,
    );
    if ((updated.record['count']! as JsonNumber).source != '15') {
      throw StateError('Dart numeric increment modifier mismatch');
    }
    updated = await modifierRecords.updateWithEtag(
      modifierID,
      JsonObject({'count-': JsonNumber('3')}),
      updated.etag!,
    );
    if ((updated.record['count']! as JsonNumber).source != '12') {
      throw StateError('Dart numeric decrement modifier mismatch');
    }
    updated = await modifierRecords.updateWithEtag(
      modifierID,
      JsonObject({
        'count': JsonNumber('100'),
        'count-': JsonNumber('50'),
        '+roles': JsonArray([const JsonString('a'), const JsonString('b')]),
        'roles+': JsonArray([const JsonString('c'), const JsonString('a')]),
        'roles-': const JsonString('b'),
        '+tags': JsonArray([JsonString(id(thirdTag))]),
        'tags-': JsonArray([JsonString(id(secondTag))]),
      }),
      updated.etag!,
    );
    final roles = (updated.record['roles']! as JsonArray).values
        .cast<JsonString>()
        .map((value) => value.value)
        .toList();
    final tagIDs = (updated.record['tags']! as JsonArray).values
        .cast<JsonString>()
        .map((value) => value.value)
        .toList();
    if ((updated.record['count']! as JsonNumber).source != '50' ||
        roles.join(',') != 'a,d,c' ||
        tagIDs.join(',') != '${id(thirdTag)},${id(firstTag)}') {
      throw StateError('Dart mixed modifier final values mismatch');
    }
    try {
      await modifierRecords.updateWithEtag(
        modifierID,
        JsonObject({
          'count+': JsonNumber('20'),
          'tags+': JsonArray([const JsonString('not-a-uuid')]),
        }),
        updated.etag!,
      );
      throw StateError('Dart invalid modifier unexpectedly succeeded');
    } on PgBaseError {
      // The server rejected the whole modifier transaction as required.
    }
    final afterRejected = await modifierRecords.get(modifierID);
    if ((afterRejected['count']! as JsonNumber).source != '50') {
      throw StateError('Dart invalid modifier changed persisted value');
    }
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
