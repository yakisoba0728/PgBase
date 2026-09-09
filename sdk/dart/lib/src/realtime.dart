import 'dart:async';
import 'dart:collection';
import 'dart:convert';

import 'client.dart';
import 'errors.dart';
import 'exact_json.dart';
import 'transport.dart';
import 'types.dart';

Stream<RecordEvent> recordEvents(
  PgBaseClient client,
  String collection, {
  String? lastEventId,
  CancellationToken? cancellation,
  int maxReconnects = 5,
  Duration reconnectDelay = const Duration(milliseconds: 250),
}) async* {
  if (maxReconnects < 0 || reconnectDelay.isNegative)
    throw const PgBaseProtocolError('invalid_reconnect_options');
  var cursor = lastEventId == null ? null : _cursor(lastEventId);
  var reconnects = 0;
  final generation = client.auth.generation;
  final seen = LinkedHashSet<String>();
  final stop = CancellationToken();
  final listeners = <StreamSubscription<void>>[];
  final path =
      '/api/v1/user/collections/${Uri.encodeComponent(collection)}/events';

  if (cancellation != null) {
    listeners.add(
      cancellation.whenCancelled.asStream().listen((_) => stop.cancel()),
    );
  }
  listeners.add(
    client.auth.changes
        .where((next) => next != generation)
        .listen((_) => stop.cancel()),
  );

  try {
    while (!stop.isCancelled && generation == client.auth.generation) {
      try {
        final headers = <String, String>{
          'accept': 'text/event-stream',
          ...client.headersFor(null),
          if (cursor != null) 'last-event-id': cursor,
        };
        final response = await client.transport.eventStream(
          path,
          headers: headers,
          cancellation: stop,
        );
        if (response.statusCode < 200 || response.statusCode >= 300) {
          final bytes = await response.stream.toBytes();
          throw pgBaseError(
            PgBaseResponse(
              status: response.statusCode,
              headers: response.headers.map(
                (key, value) => MapEntry(key.toLowerCase(), value),
              ),
              bytes: bytes,
            ),
          );
        }
        await for (final message in _messages(response.stream)) {
          if (stop.isCancelled || generation != client.auth.generation) return;
          final id = message.id == null ? null : _cursor(message.id!);
          if (id == null ||
              (cursor != null && BigInt.parse(id) <= BigInt.parse(cursor)))
            continue;
          cursor = id;
          if (message.event == 'cursor') continue;
          if (message.event != 'record') continue;
          final event = _event(message.data, id);
          if (!seen.add(event.eventId)) continue;
          if (seen.length > 1000) seen.remove(seen.first);
          reconnects = 0;
          yield event;
        }
      } on PgBaseCancellation {
        return;
      } on PgBaseError catch (error) {
        if (_terminalSseError(error)) rethrow;
        if (stop.isCancelled || generation != client.auth.generation) return;
        if (reconnects >= maxReconnects) rethrow;
      } catch (_) {
        if (stop.isCancelled || generation != client.auth.generation) return;
        if (reconnects >= maxReconnects) rethrow;
      }
      reconnects++;
      final delay = Duration(
        milliseconds: (reconnectDelay.inMilliseconds * (1 << (reconnects - 1)))
            .clamp(0, 10000),
      );
      await Future.any<void>([Future<void>.delayed(delay), stop.whenCancelled]);
    }
  } finally {
    for (final listener in listeners) {
      await listener.cancel();
    }
  }
}

bool _terminalSseError(PgBaseError error) =>
    error.status == 400 ||
    error.status == 401 ||
    error.status == 403 ||
    error.status == 410 ||
    error.code == 'cursor_expired';

String _cursor(String value) {
  if (!RegExp(r'^\d+$').hasMatch(value))
    throw const PgBaseProtocolError('invalid_cursor');
  final parsed = BigInt.parse(value);
  if (parsed > BigInt.parse('9223372036854775807'))
    throw const PgBaseProtocolError('invalid_cursor');
  return '$parsed';
}

final class _SseMessage {
  const _SseMessage({this.event, this.id, required this.data});
  final String? event;
  final String? id;
  final String data;
}

Stream<_SseMessage> _messages(Stream<List<int>> stream) async* {
  var buffered = '';
  await for (final piece in stream.transform(utf8.decoder)) {
    buffered += piece;
    final frames = buffered.split(RegExp(r'\r?\n\r?\n'));
    buffered = frames.removeLast();
    for (final frame in frames) {
      final message = _parseFrame(frame);
      if (message != null) yield message;
    }
  }
  if (buffered.isNotEmpty) {
    final message = _parseFrame(buffered);
    if (message != null) yield message;
  }
}

_SseMessage? _parseFrame(String frame) {
  String? event;
  String? id;
  final data = <String>[];
  for (final line in const LineSplitter().convert(frame)) {
    if (line.isEmpty || line.startsWith(':')) continue;
    final colon = line.indexOf(':');
    final field = colon < 0 ? line : line.substring(0, colon);
    final value = colon < 0
        ? ''
        : line.substring(colon + 1).replaceFirst(RegExp(r'^ '), '');
    switch (field) {
      case 'event':
        event = value;
      case 'id':
        if (!value.contains('\u0000')) id = value;
      case 'data':
        data.add(value);
    }
  }
  if (event == null && id == null && data.isEmpty) return null;
  return _SseMessage(event: event, id: id, data: data.join('\n'));
}

RecordEvent _event(String data, String deliveryId) {
  final decoded = decodeExactJson(utf8.encode(data));
  if (decoded is! JsonObject)
    throw const PgBaseProtocolError('invalid_realtime_event');
  String string(String key) {
    final item = decoded[key];
    if (item is! JsonString)
      throw const PgBaseProtocolError('invalid_realtime_event');
    return item.value;
  }

  final version = decoded['version'];
  if (version is! JsonNumber || int.tryParse(version.source) == null)
    throw const PgBaseProtocolError('invalid_realtime_event');
  return RecordEvent(
    eventId: string('eventId'),
    deliveryId: deliveryId,
    collection: string('collection'),
    recordId: string('recordId'),
    operation: string('operation'),
    version: int.parse(version.source),
  );
}
