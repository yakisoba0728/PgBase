import 'dart:async';
import 'dart:typed_data';

import 'package:convert/convert.dart';
import 'package:crypto/crypto.dart';

import 'client.dart';
import 'errors.dart';
import 'exact_json.dart';
import 'transport.dart';

final class FileInfo {
  const FileInfo({
    required this.id,
    required this.field,
    required this.filename,
    required this.contentType,
    required this.size,
    required this.thumbnail,
  });
  final String id;
  final String field;
  final String filename;
  final String contentType;
  final int size;
  final bool thumbnail;
}

final class Files {
  const Files(this._client, this.collection, this.recordId);
  final PgBaseClient _client;
  final String collection;
  final String recordId;

  String get _base =>
      '/api/v1/user/collections/${Uri.encodeComponent(collection)}/records/${Uri.encodeComponent(recordId)}/files';

  Future<List<FileInfo>> list(
    String field, {
    CancellationToken? cancellation,
  }) async {
    final response = await _client.request(
      'GET',
      '$_base/${Uri.encodeComponent(field)}',
      cancellation: cancellation,
    );
    if (response is! JsonObject || response['items'] is! JsonArray)
      throw const PgBaseProtocolError('invalid_file_list');
    return (response['items']! as JsonArray).values
        .map(_fileInfo)
        .toList(growable: false);
  }

  Future<FileInfo> upload(
    String field,
    UploadSource source, {
    CancellationToken? cancellation,
  }) async {
    if (source.length < 0 || source.filename.isEmpty)
      throw const PgBaseProtocolError('invalid_file_input');
    final digest = await _sha256(source, cancellation);
    final response = await _client.transport.multipart(
      'POST',
      '$_base/${Uri.encodeComponent(field)}',
      fields: const {},
      files: {'file': source},
      headers: _client.headersFor({
        'x-pgbase-file-size': '${source.length}',
        'x-pgbase-file-sha256': digest,
      }),
      cancellation: cancellation,
    );
    if (response.status < 200 || response.status >= 300)
      throw pgBaseError(response);
    final decoded = decodeExactJson(response.bytes);
    return _fileInfo(decoded);
  }

  Future<void> delete(String fileId, {CancellationToken? cancellation}) async {
    await _client.request(
      'DELETE',
      '/api/v1/user/files/${Uri.encodeComponent(fileId)}',
      cancellation: cancellation,
    );
  }

  Future<String> mintDownloadToken(
    String fileId, {
    String variant = 'original',
    CancellationToken? cancellation,
  }) async {
    final response = await _client.request(
      'POST',
      '/api/v1/user/files/${Uri.encodeComponent(fileId)}/token',
      body: JsonObject({'variant': JsonString(variant)}),
      cancellation: cancellation,
    );
    if (response is! JsonObject || response['token'] is! JsonString)
      throw const PgBaseProtocolError('invalid_file_token');
    return (response['token']! as JsonString).value;
  }

  Uri downloadUri(String fileId, String token, {String variant = 'original'}) =>
      _client.transport.uri(
        '/api/v1/user/files/${Uri.encodeComponent(fileId)}',
        {'token': token, if (variant != 'original') 'variant': variant},
      );

  Future<Uint8List> download(
    String fileId,
    String token, {
    String variant = 'original',
    CancellationToken? cancellation,
  }) async {
    final response = await _client.transport.download(
      '/api/v1/user/files/${Uri.encodeComponent(fileId)}',
      query: {'token': token, if (variant != 'original') 'variant': variant},
      headers: _client.headersFor(null),
      cancellation: cancellation,
    );
    if (response.status < 200 || response.status >= 300)
      throw pgBaseError(response);
    return response.bytes;
  }
}

FileInfo _fileInfo(JsonValue value) {
  if (value is! JsonObject)
    throw const PgBaseProtocolError('invalid_file_response');
  String string(String key) {
    final item = value[key];
    if (item is! JsonString)
      throw const PgBaseProtocolError('invalid_file_response');
    return item.value;
  }

  final size = value['size'];
  final thumbnail = value['thumbnail'];
  if (size is! JsonNumber || thumbnail is! JsonBool)
    throw const PgBaseProtocolError('invalid_file_response');
  final parsed = int.tryParse(size.source);
  if (parsed == null) throw const PgBaseProtocolError('invalid_file_response');
  return FileInfo(
    id: string('id'),
    field: string('field'),
    filename: string('filename'),
    contentType: string('contentType'),
    size: parsed,
    thumbnail: thumbnail.value,
  );
}

Future<String> _sha256(
  UploadSource source,
  CancellationToken? cancellation,
) async {
  if (cancellation?.isCancelled ?? false) throw const PgBaseCancellation();
  final sink = AccumulatorSink<Digest>();
  final converter = sha256.startChunkedConversion(sink);
  final result = Completer<String>();
  late final StreamSubscription<List<int>> subscription;
  StreamSubscription<void>? cancellationSubscription;

  void finishError(Object error, [StackTrace? stackTrace]) {
    if (result.isCompleted) return;
    converter.close();
    result.completeError(error, stackTrace);
  }

  void abort() {
    if (result.isCompleted) return;
    unawaited(subscription.cancel());
    finishError(const PgBaseCancellation());
  }

  void sourceError(Object error, StackTrace stackTrace) {
    unawaited(subscription.cancel());
    finishError(error, stackTrace);
  }

  subscription = source.open().listen(
    (chunk) {
      if (cancellation?.isCancelled ?? false) {
        abort();
      } else {
        converter.add(chunk);
      }
    },
    onError: sourceError,
    onDone: () {
      if (result.isCompleted) return;
      converter.close();
      result.complete(sink.events.single.toString());
    },
  );
  cancellationSubscription = cancellation?.whenCancelled.asStream().listen(
    (_) => abort(),
  );
  if (cancellation?.isCancelled ?? false) abort();
  try {
    return await result.future;
  } finally {
    await cancellationSubscription?.cancel();
    await subscription.cancel();
  }
}
