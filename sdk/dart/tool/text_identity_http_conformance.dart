import 'dart:io';

import 'package:pgbase/pgbase.dart';

Future<void> main() async {
  final client = PgBaseClient(Uri.parse(Platform.environment['PGBASE_SDK_URL']!));
  final realmName = Platform.environment['PGBASE_TEXT_AUTH_REALM']!;
  try {
    await client.register(
      'dart-text-records@example.test',
      'dart text records password 0123456789',
    );
    String id(JsonObject value) => (value['id']! as JsonString).value;
    final tags = client.records('text_tags');
    final records = client.records('text_notes');
    final tag = await tags.create(
      JsonObject({'id': const JsonString('DaRtTag01'), 'label': const JsonString('dart')}),
      'dart-text-tag-create-0001',
    );
    final note = await records.create(
      JsonObject({
        'id': const JsonString('DaRtMiXeD01'),
        'title': const JsonString('created'),
        'tags': JsonArray([JsonString(id(tag))]),
      }),
      'dart-text-note-create-0001',
    );
    if (id(note) != 'DaRtMiXeD01' ||
        ((note['tags']! as JsonArray).values.single as JsonString).value != id(tag)) {
      throw StateError('Dart text identity or relation did not round trip on create');
    }
    final loaded = await records.get('DaRtMiXeD01');
    if (id(loaded) != 'DaRtMiXeD01') {
      throw StateError('Dart text identity did not round trip through get');
    }
    try {
      await records.create(
        JsonObject({'id': const JsonString('DaRtMiXeD01'), 'title': const JsonString('duplicate')}),
        'dart-text-note-duplicate-0001',
      );
      throw StateError('Dart duplicate text identity unexpectedly succeeded');
    } on PgBaseError {
      if (((await records.get('DaRtMiXeD01'))['title']! as JsonString).value != 'created') {
        throw StateError('Dart text identity collision changed the original record');
      }
    }
    var current = await records.getWithEtag('DaRtMiXeD01');
    current = await records.updateWithEtag(
      'DaRtMiXeD01',
      JsonObject({'title': const JsonString('first update')}),
      current.etag!,
    );
    try {
      await records.update(
        'DaRtMiXeD01',
        JsonObject({'title': const JsonString('stale')}),
        '"stale"',
      );
      throw StateError('Dart stale text identity update unexpectedly succeeded');
    } on PgBaseError {
      if (((await records.get('DaRtMiXeD01'))['title']! as JsonString).value != 'first update') {
        throw StateError('Dart stale text identity update changed persisted data');
      }
    }
    current = await records.getWithEtag('DaRtMiXeD01');
    final retried = await records.updateWithEtag(
      'DaRtMiXeD01',
      JsonObject({'title': const JsonString('retried')}),
      current.etag!,
    );
    if (id(retried.record) != 'DaRtMiXeD01') {
      throw StateError('Dart text identity update retry lost id');
    }
    final filtered = await records.getList(
      1,
      30,
      PocketBaseListOptions(
        filter: client.filter('id = {:id}', {'id': 'DaRtMiXeD01'}),
        sort: 'id',
      ),
    );
    if (filtered.page != 1 ||
        filtered.perPage != 30 ||
        filtered.totalItems != 1 ||
        filtered.totalPages != 1 ||
        filtered.items.length != 1 ||
        id(filtered.items.single) != 'DaRtMiXeD01') {
      throw StateError('Dart text identity PocketBase page/filter mismatch');
    }
    final native = client.records('uuid_notes');
    final uuidRecord = await native.create(
      JsonObject({'title': const JsonString('dart uuid')}),
      'dart-native-uuid-create-0001',
    );
    if (!RegExp(r'^[0-9a-f]{8}-[0-9a-f-]{27}$', caseSensitive: false).hasMatch(id(uuidRecord))) {
      throw StateError('Dart native UUID collection regressed');
    }
    final authClient = PgBaseClient(Uri.parse(Platform.environment['PGBASE_SDK_URL']!));
    try {
      final realm = authClient.realm(realmName);
      final registered = await realm.register(
        'dart-text-target@example.test',
        'dart text target password 0123456789',
      );
      if (!RegExp(r'^[a-z0-9]{15}$').hasMatch(registered.user.id) ||
          registered.user.emailVisibility != false) {
        throw StateError('Dart text auth registration lost id or email visibility');
      }
      final loggedIn = await realm.login(
        'dart-text-target@example.test',
        'dart text target password 0123456789',
      );
      if (loggedIn.user.id != registered.user.id ||
          loggedIn.user.emailVisibility != false) {
        throw StateError('Dart text auth login lost exact id or visibility');
      }
      if ((await realm.session()).user.emailVisibility != false) {
        throw StateError('Dart text auth session lost email visibility');
      }
      if ((await realm.refresh()).user.emailVisibility != false) {
        throw StateError('Dart text auth refresh lost email visibility');
      }
      final managerClient = PgBaseClient(
        Uri.parse(Platform.environment['PGBASE_SDK_URL']!),
      );
      try {
        final manager = await managerClient.realm(realmName).login(
          'sdk-text-manager@example.test',
          'user-one-fixture-passphrase-7281',
        );
        if (manager.user.id != 'ManagerCase01') {
          throw StateError('Dart text auth manager session lost mixed-case id');
        }
        final managed = await managerClient.request(
          'PATCH',
          '/api/v1/auth/${Uri.encodeComponent(realmName)}/records/${Uri.encodeComponent(registered.user.id)}',
          body: JsonObject({'emailVisibility': const JsonBool(true)}),
        );
        if (managed is! JsonObject ||
            (managed['emailVisibility']! as JsonBool).value != true) {
          throw StateError('Dart manager did not persist email visibility');
        }
        final visibleLogin = await realm.login(
          'dart-text-target@example.test',
          'dart text target password 0123456789',
        );
        if (visibleLogin.user.emailVisibility != true ||
            (await realm.session()).user.emailVisibility != true ||
            (await realm.refresh()).user.emailVisibility != true) {
          throw StateError('Dart persisted email visibility was lost by auth session responses');
        }
        await managerClient.realm(realmName).logout();
      } finally {
        managerClient.close();
      }
      await realm.logout();
    } finally {
      authClient.close();
    }
    await native.remove(id(uuidRecord), (await native.getWithEtag(id(uuidRecord))).etag!);
    await records.remove('DaRtMiXeD01', (await records.getWithEtag('DaRtMiXeD01')).etag!);
    try {
      await records.get('DaRtMiXeD01');
      throw StateError('Dart text identity delete did not remove record');
    } on PgBaseError {
      // Exact public text identity no longer resolves after delete.
    }
  } finally {
    client.close();
  }
}
