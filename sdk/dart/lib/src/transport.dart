import 'dart:async';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:http_parser/http_parser.dart';

import 'errors.dart';
import 'exact_json.dart';

final class CancellationToken {
  final Completer<void> _abort = Completer<void>();

  bool get isCancelled => _abort.isCompleted;
  Future<void> get whenCancelled => _abort.future;
  void cancel() {
    if (!_abort.isCompleted) _abort.complete();
  }
}

final class PgBaseResponse {
  const PgBaseResponse({
    required this.status,
    required this.headers,
    required this.bytes,
  });

  final int status;
  final Map<String, String> headers;
  final Uint8List bytes;

  String? get etag => headers['etag'];
}

/// Creates an upload stream afresh for hashing and for sending; this avoids a second full-file copy.
final class UploadSource {
  const UploadSource({
    required this.open,
    required this.length,
    required this.filename,
    this.contentType,
  });

  final Stream<List<int>> Function() open;
  final int length;
  final String filename;
  final String? contentType;
}

final class PgBaseTransport {
  PgBaseTransport(this.baseUri, {http.Client? client})
    : _client = client ?? http.Client(),
      _ownsClient = client == null;

  final Uri baseUri;
  final http.Client _client;
  final bool _ownsClient;

  void close() {
    if (_ownsClient) _client.close();
  }

  Uri uri(String path, [Map<String, String>? query]) {
    if (!path.startsWith('/')) throw const PgBaseProtocolError('invalid_path');
    return baseUri.resolveUri(Uri(path: path, queryParameters: query));
  }

  Future<PgBaseResponse> json(
    String method,
    String path, {
    Map<String, String> headers = const {},
    JsonValue? body,
    Map<String, String>? query,
    CancellationToken? cancellation,
  }) async {
    final request = http.AbortableRequest(
      method,
      uri(path, query),
      abortTrigger: cancellation?.whenCancelled,
    );
    request.headers.addAll({'accept': 'application/json', ...headers});
    if (body != null) {
      request.headers['content-type'] = 'application/json; charset=utf-8';
      request.bodyBytes = encodeExactJson(body);
    }
    return _collect(request, cancellation);
  }

  Future<PgBaseResponse> multipart(
    String method,
    String path, {
    required Map<String, String> fields,
    required Map<String, UploadSource> files,
    Map<String, String> headers = const {},
    CancellationToken? cancellation,
  }) async {
    final request = http.AbortableMultipartRequest(
      method,
      uri(path),
      abortTrigger: cancellation?.whenCancelled,
    );
    request.headers.addAll({'accept': 'application/json', ...headers});
    request.fields.addAll(fields);
    for (final entry in files.entries) {
      final file = entry.value;
      request.files.add(
        http.MultipartFile(
          entry.key,
          file.open(),
          file.length,
          filename: file.filename,
          contentType: _mediaType(file.contentType),
        ),
      );
    }
    return _collect(request, cancellation);
  }

  Future<PgBaseResponse> download(
    String path, {
    Map<String, String> headers = const {},
    Map<String, String>? query,
    CancellationToken? cancellation,
  }) {
    final request = http.AbortableRequest(
      'GET',
      uri(path, query),
      abortTrigger: cancellation?.whenCancelled,
    );
    request.headers.addAll(headers);
    return _collect(request, cancellation);
  }

  /// Streams an exact-length request body without taking ownership of [source].
  Future<PgBaseResponse> upload(
    String method,
    String path, {
    required UploadSource source,
    required String contentType,
    Map<String, String> headers = const {},
    Map<String, String>? query,
    CancellationToken? cancellation,
  }) async {
    final request = http.AbortableStreamedRequest(
      method,
      uri(path, query),
      abortTrigger: cancellation?.whenCancelled,
    );
    request.headers.addAll({
      'accept': 'application/json',
      'content-type': contentType,
      ...headers,
    });
    request.contentLength = source.length;
    final uploadStream = source.open();
    final sending = _client.send(request);
    try {
      final results = await Future.wait<Object?>([
        sending,
        _pipeUpload(request.sink, uploadStream, cancellation),
      ], eagerError: false);
      final response = results.first as http.StreamedResponse;
      final bytes = await response.stream.toBytes();
      return PgBaseResponse(
        status: response.statusCode,
        headers: _headers(response.headers),
        bytes: Uint8List.fromList(bytes),
      );
    } on PgBaseCancellation {
      rethrow;
    } on http.RequestAbortedException {
      throw const PgBaseCancellation();
    } on http.ClientException catch (error) {
      throw PgBaseNetworkError(error);
    }
  }

  Future<http.StreamedResponse> eventStream(
    String path, {
    required Map<String, String> headers,
    Map<String, String>? query,
    CancellationToken? cancellation,
  }) async {
    final request = http.AbortableRequest(
      'GET',
      uri(path, query),
      abortTrigger: cancellation?.whenCancelled,
    );
    request.headers.addAll(headers);
    try {
      return await _client.send(request);
    } on http.RequestAbortedException {
      throw const PgBaseCancellation();
    } on http.ClientException catch (error) {
      throw PgBaseNetworkError(error);
    }
  }

  Future<PgBaseResponse> _collect(
    http.BaseRequest request,
    CancellationToken? cancellation,
  ) async {
    try {
      final response = await _client.send(request);
      final bytes = await response.stream.toBytes();
      return PgBaseResponse(
        status: response.statusCode,
        headers: _headers(response.headers),
        bytes: Uint8List.fromList(bytes),
      );
    } on http.RequestAbortedException {
      throw const PgBaseCancellation();
    } on http.ClientException catch (error) {
      throw PgBaseNetworkError(error);
    }
  }

  Map<String, String> _headers(Map<String, String> headers) => Map.unmodifiable(
    {for (final entry in headers.entries) entry.key.toLowerCase(): entry.value},
  );
}

Future<void> _pipeUpload(
  StreamSink<List<int>> sink,
  Stream<List<int>> source,
  CancellationToken? cancellation,
) {
  final done = Completer<void>();
  late final StreamSubscription<List<int>> subscription;
  subscription = source.listen(
    sink.add,
    onError: (Object error, StackTrace stackTrace) async {
      await sink.close();
      if (!done.isCompleted) done.completeError(error, stackTrace);
    },
    onDone: () async {
      await sink.close();
      if (!done.isCompleted) done.complete();
    },
    cancelOnError: true,
  );
  cancellation?.whenCancelled.then((_) async {
    await subscription.cancel();
    unawaited(sink.close());
    if (!done.isCompleted) done.completeError(const PgBaseCancellation());
  });
  return done.future;
}

MediaType? _mediaType(String? source) {
  if (source == null) return null;
  final pieces = source.split('/');
  if (pieces.length != 2 || pieces.any((piece) => piece.isEmpty))
    throw const PgBaseProtocolError('invalid_content_type');
  return MediaType(pieces.first, pieces.last);
}
