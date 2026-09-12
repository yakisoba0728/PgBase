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

  /// Reads a numbered PocketBase-compatible page. Native [list] remains
  /// cursor-based so callers must opt into this offset-pagination contract.
  Future<PocketBaseRecordPage<JsonObject>> getList([
    int page = 1,
    int perPage = 30,
    PocketBaseListOptions options = const PocketBaseListOptions(),
    CancellationToken? cancellation,
  ]) async {
    final result = await _client.request(
      'GET',
      _base,
      query: options.toParameters(page, perPage),
      cancellation: cancellation,
    );
    if (result is! JsonObject || result['items'] is! JsonArray) {
      throw const PgBaseProtocolError('invalid_pocketbase_record_page');
    }
    final items = result['items']! as JsonArray;
    final resultPage = result['page'];
    final resultPerPage = result['perPage'];
    final totalItems = result['totalItems'];
    final totalPages = result['totalPages'];
    if (items.values.any((item) => item is! JsonObject) ||
        resultPage is! JsonNumber ||
        resultPerPage is! JsonNumber ||
        totalItems is! JsonNumber ||
        totalPages is! JsonNumber) {
      throw const PgBaseProtocolError('invalid_pocketbase_record_page');
    }
    int integer(JsonNumber value) {
      final parsed = int.tryParse(value.source);
      if (parsed == null) throw const PgBaseProtocolError('invalid_pocketbase_record_page');
      return parsed;
    }
    return PocketBaseRecordPage(
      page: integer(resultPage),
      perPage: integer(resultPerPage),
      totalItems: integer(totalItems),
      totalPages: integer(totalPages),
      items: items.values.cast<JsonObject>(),
    );
  }

  Future<JsonObject> get(
    String id, {
    String? fields,
    CancellationToken? cancellation,
  }) async =>
      (await getWithEtag(id, fields: fields, cancellation: cancellation)).record;

  Future<RecordResult<JsonObject>> getWithEtag(
    String id, {
    String? fields,
    CancellationToken? cancellation,
  }) => _client.recordRequest(
    'GET',
    _path(id),
    query: {if (fields != null) 'fields': fields},
    cancellation: cancellation,
  );

  Future<JsonObject> create(
    JsonObject input,
    String idempotencyKey, {
    String? fields,
    CancellationToken? cancellation,
  }) async => (await createWithEtag(
    input,
    idempotencyKey,
    fields: fields,
    cancellation: cancellation,
  )).record;

  Future<RecordResult<JsonObject>> createWithEtag(
    JsonObject input,
    String idempotencyKey, {
    String? fields,
    CancellationToken? cancellation,
  }) {
    _idempotency(idempotencyKey);
    return _client.recordRequest(
      'POST',
      _base,
      body: input,
      query: {if (fields != null) 'fields': fields},
      headers: {'idempotency-key': idempotencyKey},
      cancellation: cancellation,
    );
  }

  Future<JsonObject> update(
    String id,
    JsonObject input,
    String etag, {
    String? fields,
    CancellationToken? cancellation,
  }) async => (await updateWithEtag(
    id,
    input,
    etag,
    fields: fields,
    cancellation: cancellation,
  )).record;

  Future<RecordResult<JsonObject>> updateWithEtag(
    String id,
    JsonObject input,
    String etag, {
    String? fields,
    CancellationToken? cancellation,
  }) {
    _strongEtag(etag);
    return _client.recordRequest(
      'PATCH',
      _path(id),
      body: input,
      query: {if (fields != null) 'fields': fields},
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
          fields: query.fields,
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
    String? fields,
    CancellationToken? cancellation,
    int maxReconnects = 5,
    Duration reconnectDelay = const Duration(milliseconds: 250),
  }) => recordEvents(
    _client,
    collection,
    lastEventId: lastEventId,
    fields: fields,
    cancellation: cancellation,
    maxReconnects: maxReconnects,
    reconnectDelay: reconnectDelay,
  );

  String _path(String id) => '$_base/${Uri.encodeComponent(id)}';
}

final class BatchOperation {
  const BatchOperation(this.value);
  final JsonObject value;

  factory BatchOperation.create({
    required String collection,
    required JsonObject input,
    required String idempotencyKey,
  }) => BatchOperation(
    JsonObject({
      'operation': const JsonString('create'),
      'collection': JsonString(collection),
      'input': input,
      'idempotencyKey': JsonString(idempotencyKey),
    }),
  );

  factory BatchOperation.update({
    required String collection,
    required String id,
    required JsonObject input,
    String? ifMatch,
  }) => _mutation('update', collection, id, input, ifMatch);

  /// Creates or replaces the supplied record through the batch upsert form.
  factory BatchOperation.upsert({
    required String collection,
    required String id,
    required JsonObject input,
    String? ifMatch,
  }) => _mutation('upsert', collection, id, input, ifMatch);

  factory BatchOperation.delete({
    required String collection,
    required String id,
    String? ifMatch,
  }) => BatchOperation(
    JsonObject({
      'operation': const JsonString('delete'),
      'collection': JsonString(collection),
      'id': JsonString(id),
      if (ifMatch != null) 'ifMatch': JsonString(ifMatch),
    }),
  );

  static BatchOperation _mutation(
    String operation,
    String collection,
    String id,
    JsonObject input,
    String? ifMatch,
  ) => BatchOperation(
    JsonObject({
      'operation': JsonString(operation),
      'collection': JsonString(collection),
      'id': JsonString(id),
      'input': input,
      if (ifMatch != null) 'ifMatch': JsonString(ifMatch),
    }),
  );
}

/// A typed request for the generic batch endpoint.
final class BatchHttpRequest {
  const BatchHttpRequest(this.value);
  final JsonObject value;

  factory BatchHttpRequest.upsert({
    required String collection,
    required String id,
    required JsonObject input,
    String? ifMatch,
  }) => BatchHttpRequest(
    JsonObject({
      'method': const JsonString('PUT'),
      'url': JsonString(
        '/api/v1/collections/${Uri.encodeComponent(collection)}/records/${Uri.encodeComponent(id)}',
      ),
      'body': input,
      if (ifMatch != null)
        'headers': JsonObject({'if-match': JsonString(ifMatch)}),
    }),
  );
}

Future<JsonObject> submitBatch(
  PgBaseClient client,
  List<BatchOperation> operations, {
  String? fields,
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
    query: {if (fields != null) 'fields': fields},
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
