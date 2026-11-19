import 'dart:async';
import 'dart:typed_data';

import 'package:http/http.dart' as http;
import 'package:http_parser/http_parser.dart';

import 'errors.dart';
import 'exact_json.dart';

final class CancellationToken {
  final Completer<void> _abort = Completer<void>();
  final Set<void Function()> _listeners = {};

  bool get isCancelled => _abort.isCompleted;
  Future<void> get whenCancelled => _abort.future;

  /// Registers a callback and returns a function that detaches it.
  void Function() onCancel(void Function() listener) {
    if (isCancelled) {
      listener();
      return () {};
    }
    _listeners.add(listener);
    return () => _listeners.remove(listener);
  }

  void cancel() {
    if (isCancelled) return;
    _abort.complete();
    final listeners = List.of(_listeners);
    _listeners.clear();
    for (final listener in listeners) {
      listener();
    }
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
  final Set<CancellationToken> _active = {};
  bool _closed = false;

  void close() {
    if (_closed) return;
    _closed = true;
    for (final token in List.of(_active)) {
      token.cancel();
    }
    if (_ownsClient) _client.close();
  }

  ({CancellationToken token, void Function() dispose}) _track(
    CancellationToken? external,
  ) {
    if (_closed || (external?.isCancelled ?? false)) {
      throw const PgBaseCancellation();
    }
    final token = CancellationToken();
    final detach = external?.onCancel(token.cancel);
    _active.add(token);
    return (
      token: token,
      dispose: () {
        detach?.call();
        _active.remove(token);
      },
    );
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
  }) => _collect(cancellation, (stop) {
    final request = http.AbortableRequest(
      method,
      uri(path, query),
      abortTrigger: stop.whenCancelled,
    );
    request.headers.addAll({'accept': 'application/json', ...headers});
    if (body != null) {
      request.headers['content-type'] = 'application/json; charset=utf-8';
      request.bodyBytes = encodeExactJson(body);
    }
    return request;
  });

  Future<PgBaseResponse> multipart(
    String method,
    String path, {
    required Map<String, String> fields,
    required Map<String, UploadSource> files,
    Map<String, String> headers = const {},
    CancellationToken? cancellation,
  }) => _collect(cancellation, (stop) {
    final request = http.AbortableMultipartRequest(
      method,
      uri(path),
      abortTrigger: stop.whenCancelled,
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
    return request;
  });

  Future<PgBaseResponse> download(
    String path, {
    Map<String, String> headers = const {},
    Map<String, String>? query,
    CancellationToken? cancellation,
  }) => _collect(
    cancellation,
    (stop) => http.AbortableRequest(
      'GET',
      uri(path, query),
      abortTrigger: stop.whenCancelled,
    )..headers.addAll(headers),
  );

  /// Streams an exact-length request body without taking ownership of [source].
  Future<PgBaseResponse> upload(
    String method,
    String path, {
    required UploadSource source,
    required String contentType,
    Map<String, String> headers = const {},
    Map<String, String>? query,
    CancellationToken? cancellation,
  }) => _collect(cancellation, (stop) {
    final request = _UploadRequest(method, uri(path, query), source, stop);
    request.headers.addAll({
      'accept': 'application/json',
      'content-type': contentType,
      ...headers,
    });
    request.contentLength = source.length;
    return request;
  });

  Future<http.StreamedResponse> eventStream(
    String path, {
    required Map<String, String> headers,
    Map<String, String>? query,
    CancellationToken? cancellation,
  }) => downloadStream(
    path,
    headers: headers,
    query: query,
    cancellation: cancellation,
  );

  Future<http.StreamedResponse> downloadStream(
    String path, {
    required Map<String, String> headers,
    Map<String, String>? query,
    CancellationToken? cancellation,
  }) async {
    final scope = _track(cancellation);
    try {
      final request = http.AbortableRequest(
        'GET',
        uri(path, query),
        abortTrigger: scope.token.whenCancelled,
      )..headers.addAll(headers);
      final response = await _send(request);
      return http.StreamedResponse(
        _trackedBody(response.stream, scope.token, scope.dispose),
        response.statusCode,
        headers: response.headers,
        contentLength: response.contentLength,
        request: response.request,
        isRedirect: response.isRedirect,
        persistentConnection: response.persistentConnection,
        reasonPhrase: response.reasonPhrase,
      );
    } catch (_) {
      scope.dispose();
      rethrow;
    }
  }

  Future<http.StreamedResponse> _send(http.BaseRequest request) async {
    try {
      return await _client.send(request);
    } on http.RequestAbortedException {
      throw const PgBaseCancellation();
    } on http.ClientException catch (error) {
      throw PgBaseNetworkError(error);
    }
  }

  Future<PgBaseResponse> _collect(
    CancellationToken? external,
    http.BaseRequest Function(CancellationToken) request,
  ) async {
    final scope = _track(external);
    try {
      final response = await _send(request(scope.token));
      final bytes = await http.ByteStream(
        _trackedBody(response.stream, scope.token, scope.dispose),
      ).toBytes();
      return PgBaseResponse(
        status: response.statusCode,
        headers: _headers(response.headers),
        bytes: bytes,
      );
    } on http.RequestAbortedException {
      throw const PgBaseCancellation();
    } on http.ClientException catch (error) {
      throw PgBaseNetworkError(error);
    } finally {
      scope.dispose();
    }
  }

  Stream<List<int>> _trackedBody(
    Stream<List<int>> body,
    CancellationToken stop,
    void Function() dispose,
  ) {
    final output = StreamController<List<int>>();
    late StreamSubscription<List<int>> input;
    void Function()? detach;
    output.onListen = () {
      input = body.listen(
        output.add,
        onError: output.addError,
        onDone: () {
          detach?.call();
          dispose();
          if (!output.isClosed) unawaited(output.close());
        },
      );
      output.onPause = input.pause;
      output.onResume = input.resume;
      detach = stop.onCancel(() {
        if (output.isClosed) return;
        unawaited(input.cancel());
        output.addError(const PgBaseCancellation());
        unawaited(output.close());
      });
    };
    output.onCancel = () {
      detach?.call();
      dispose();
      return input.cancel();
    };
    return output.stream;
  }

  Map<String, String> _headers(Map<String, String> headers) => Map.unmodifiable(
    {for (final entry in headers.entries) entry.key.toLowerCase(): entry.value},
  );
}

final class _UploadRequest extends http.BaseRequest with http.Abortable {
  _UploadRequest(super.method, super.url, this.source, this.cancellation);
  final UploadSource source;
  final CancellationToken? cancellation;
  @override
  Future<void>? get abortTrigger => cancellation?.whenCancelled;

  @override
  http.ByteStream finalize() {
    super.finalize();
    return http.ByteStream(
      Stream<List<int>>.multi((controller) {
        StreamSubscription<void>? abort;
        final subscription = source.open().listen(
          controller.addSync,
          onError: controller.addErrorSync,
          onDone: () {
            abort?.cancel();
            controller.closeSync();
          },
        );
        controller.onPause = subscription.pause;
        controller.onResume = subscription.resume;
        controller.onCancel = () async {
          await abort?.cancel();
          await subscription.cancel();
        };
        void cancel() {
          subscription.cancel();
          controller.addErrorSync(http.RequestAbortedException(url));
          controller.closeSync();
        }

        abort = cancellation?.whenCancelled.asStream().listen((_) => cancel());
        if (cancellation?.isCancelled ?? false) cancel();
      }),
    );
  }
}

MediaType? _mediaType(String? source) {
  if (source == null) return null;
  final pieces = source.split('/');
  if (pieces.length != 2 || pieces.any((piece) => piece.isEmpty))
    throw const PgBaseProtocolError('invalid_content_type');
  return MediaType(pieces.first, pieces.last);
}
