import 'client.dart';
import 'errors.dart';
import 'exact_json.dart';
import 'realtime.dart';
import 'transport.dart';
import 'types.dart';

final class Records {
  const Records(this._client, this.collection);

  final PgBaseClient _client;
  final String collection;

  String get _base =>
      '/api/v1/user/collections/${Uri.encodeComponent(collection)}/records';

  Future<RecordPage<JsonObject>> list([
    Query query = const Query(),
    CancellationToken? cancellation,
  ]) async {
    final result = await _client.request(
      'GET',
      _base,
      query: query.toParameters(),
      cancellation: cancellation,
    );
    if (result is! JsonObject || result['items'] is! JsonArray)
      throw const PgBaseProtocolError('invalid_record_page');
    final items = result['items']! as JsonArray;
    if (items.values.any((item) => item is! JsonObject))
      throw const PgBaseProtocolError('invalid_record_page');
    return RecordPage(
      items: items.values.cast<JsonObject>(),
      nextCursor: (result['nextCursor'] as JsonString?)?.value,
    );
  }

  Future<JsonObject> get(String id, {CancellationToken? cancellation}) async =>
      (await getWithEtag(id, cancellation: cancellation)).record;

  Future<RecordResult<JsonObject>> getWithEtag(
    String id, {
    CancellationToken? cancellation,
  }) => _client.recordRequest('GET', _path(id), cancellation: cancellation);

  Future<JsonObject> create(
    JsonObject input,
    String idempotencyKey, {
    CancellationToken? cancellation,
  }) async => (await createWithEtag(
    input,
    idempotencyKey,
    cancellation: cancellation,
  )).record;

  Future<RecordResult<JsonObject>> createWithEtag(
    JsonObject input,
    String idempotencyKey, {
    CancellationToken? cancellation,
  }) {
    _idempotency(idempotencyKey);
    return _client.recordRequest(
      'POST',
      _base,
      body: input,
      headers: {'idempotency-key': idempotencyKey},
      cancellation: cancellation,
    );
  }

  Future<JsonObject> update(
    String id,
    JsonObject input,
    String etag, {
    CancellationToken? cancellation,
  }) async => (await updateWithEtag(
    id,
    input,
    etag,
    cancellation: cancellation,
  )).record;

  Future<RecordResult<JsonObject>> updateWithEtag(
    String id,
    JsonObject input,
    String etag, {
    CancellationToken? cancellation,
  }) {
    _strongEtag(etag);
    return _client.recordRequest(
      'PATCH',
      _path(id),
      body: input,
      headers: {'if-match': etag},
      cancellation: cancellation,
    );
  }

  Future<void> remove(
    String id,
    String etag, {
    CancellationToken? cancellation,
  }) async {
    _strongEtag(etag);
    await _client.request(
      'DELETE',
      _path(id),
      headers: {'if-match': etag},
      cancellation: cancellation,
    );
  }

  Stream<JsonObject> iterate([
    Query query = const Query(),
    CancellationToken? cancellation,
  ]) async* {
    var next = query.cursor;
    do {
      final page = await list(
        Query(
          filter: query.filter,
          sort: query.sort,
          expand: query.expand,
          cursor: next,
          limit: query.limit,
        ),
        cancellation,
      );
      yield* Stream.fromIterable(page.items);
      next = page.nextCursor;
    } while (next != null);
  }

  Stream<RecordEvent> events({
    String? lastEventId,
    CancellationToken? cancellation,
    int maxReconnects = 5,
    Duration reconnectDelay = const Duration(milliseconds: 250),
  }) => recordEvents(
    _client,
    collection,
    lastEventId: lastEventId,
    cancellation: cancellation,
    maxReconnects: maxReconnects,
    reconnectDelay: reconnectDelay,
  );

  String _path(String id) => '$_base/${Uri.encodeComponent(id)}';
}

final class BatchOperation {
  const BatchOperation(this.value);
  final JsonObject value;
}

Future<JsonObject> batch(
  PgBaseClient client,
  List<BatchOperation> operations, {
  CancellationToken? cancellation,
}) async {
  final response = await client.request(
    'POST',
    '/api/v1/user/collections/batch',
    body: JsonObject({
      'operations': JsonArray(
        operations.map((operation) => operation.value).toList(growable: false),
      ),
    }),
    cancellation: cancellation,
  );
  if (response is! JsonObject)
    throw const PgBaseProtocolError('invalid_batch_response');
  return response;
}

void _idempotency(String value) {
  if (!RegExp(r'^[A-Za-z0-9._~-]{16,128}$').hasMatch(value))
    throw const PgBaseProtocolError('invalid_idempotency_key');
}

void _strongEtag(String value) {
  if (value.startsWith('W/') || !RegExp(r'^"[^"\\]*"$').hasMatch(value))
    throw const PgBaseProtocolError('invalid_etag');
}
