import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:http/testing.dart';
import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

void main() {
  test(
    'schema roundtrip preserves native policy binding independently of source',
    () async {
      final rules = {
        'list': {
          'op': 'eq',
          'binding': 'catalog',
          'field': 'owner.id',
          'auth': 'id',
          'expression': 'owner.id = @request.auth.id',
        },
        'view': {
          'op': 'owner',
          'binding': 'scalar',
          'expression': 'owner annotation',
        },
        'create': {'op': 'deny'},
        'update': {'op': 'owner'},
        'delete': {'op': 'deny'},
      };
      final document = {
        'format': 'pgbase.schema/v1',
        'collections': [
          {'name': 'posts', 'userAccess': true, 'rules': rules},
        ],
      };
      final requests = <http.Request>[];
      final client = MockClient((request) async {
        requests.add(request);
        final Object response;
        switch (request.url.path) {
          case '/api/admin/schema/export':
            response = document;
          case '/api/admin/schema/import/preview':
            response = {
              'baseVersion': 3,
              'checksum': 'native-policy',
              'document': jsonDecode(request.body),
            };
          case '/api/admin/schema/import/apply':
            response = {'operationId': 'applied', 'status': 'committed'};
          default:
            throw StateError('unexpected request: ${request.url.path}');
        }
        return http.Response(
          jsonEncode(response),
          200,
          headers: {'content-type': 'application/json'},
        );
      });
      final admin = PgBaseAdminClient(
        Uri.parse('http://pgbase.test/'),
        httpClient: client,
      );
      addTearDown(admin.close);
      final exported = await admin.exportSchema();
      final plan = await admin.previewSchemaImport(exported);
      await admin.applySchemaImport(plan);
      await admin.applySchemaImport(plan);
      final preview = jsonDecode(requests[1].body) as Map<String, dynamic>;
      final applied = jsonDecode(requests[2].body) as Map<String, dynamic>;
      expect(_rules(preview), rules);
      expect(_rules(applied['document']), rules);
      expect(requests[2].body, requests[3].body);
      expect(
        requests[2].headers['pgbase-operation-id'],
        requests[3].headers['pgbase-operation-id'],
      );
      expect(
        requests[2].headers['idempotency-key'],
        requests[3].headers['idempotency-key'],
      );
      expect(
        requests.every(
          (request) => !request.url.queryParameters.containsKey('compat'),
        ),
        isTrue,
      );
    },
  );
}

Object? _rules(Object? raw) {
  final document = raw! as Map<String, Object?>;
  final collections = document['collections']! as List<Object?>;
  final collection = collections.single! as Map<String, Object?>;
  return collection['rules'];
}
