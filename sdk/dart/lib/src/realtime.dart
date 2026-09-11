import 'dart:async';
import 'dart:collection';
import 'dart:convert';

import 'package:http/http.dart' as http;

import 'client.dart';
import 'errors.dart';
import 'exact_json.dart';
import 'transport.dart';
import 'types.dart';

final class RealtimeEvent {
  const RealtimeEvent({
    required this.topic,
    required this.action,
    required this.record,
  });
  final String topic;
  final String action;
  final JsonObject record;
}

/// A reserved `@` application topic event with arbitrary valid JSON payload.
final class RealtimeCustomEvent {
  const RealtimeCustomEvent({required this.topic, required this.data});
  final String topic;
  final JsonValue data;
}

/// One SSE connection shared by PB-style topic subscriptions.
final class RealtimeService {
  RealtimeService(
    this._client, {
    this.maxReconnects = 5,
    this.reconnectDelay = const Duration(milliseconds: 250),
  }) {
    if (maxReconnects < 0 || reconnectDelay.isNegative) {
      throw const PgBaseProtocolError('invalid_reconnect_options');
    }
  }
  final PgBaseClient _client;

  /// Maximum failed handshakes before a pending subscription is rejected.
  final int maxReconnects;
  final Duration reconnectDelay;
  final Map<String, Set<Function>> _listeners = {};
  CancellationToken? _stop;
  String? _clientId;
  String? _lastEventId;
  Future<void>? _running;
  Completer<void>? _ready;

  /// Subscribes to collection topics and reserved `@` application topics.
  /// Collection callbacks receive [RealtimeEvent]; application callbacks
  /// receive [RealtimeCustomEvent] with arbitrary JSON [RealtimeCustomEvent.data].
  Future<Future<void> Function()> subscribe(
    String topic,
    Function listener,
  ) async {
    if (!_validTopic(topic))
      throw const PgBaseProtocolError('invalid_realtime_topic');
    if (_stop?.isCancelled ?? false) await _running;
    (_listeners[topic] ??= {}).add(listener);
    _ensureRunning();
    if (_clientId != null)
      await _sync();
    else
      await _ready!.future;
    return () => unsubscribe(topic, listener);
  }

  /// Subscribes to an application topic while preserving its raw UTF-8 name.
  /// The reserved `@app/...` name remains an internal realtime wire detail.
  Future<Future<void> Function()> subscribeApplication(
    String topic,
    void Function(RealtimeCustomEvent) listener,
  ) {
    final wireTopic = _applicationWireTopic(topic);
    return subscribe(wireTopic, (dynamic event) {
      if (event is RealtimeCustomEvent && event.topic == wireTopic) {
        listener(RealtimeCustomEvent(topic: topic, data: event.data));
      }
    });
  }

  Future<void> unsubscribe([String? topic, Function? listener]) async {
    if (topic == null) {
      _listeners.clear();
    } else if (listener == null) {
      _listeners.remove(topic);
    } else {
      final listeners = _listeners[topic];
      listeners?.remove(listener);
      if (listeners?.isEmpty ?? false) _listeners.remove(topic);
    }
    if (_listeners.isEmpty) {
      _stop?.cancel();
      _clientId = null;
      return;
    }
    if (_clientId != null) await _sync();
  }

  void _ensureRunning() {
    if (_running != null) return;
    _ready = Completer<void>();
    _running = _run().whenComplete(() {
      _running = null;
      _ready = null;
    });
  }

  Future<void> _sync() async {
    await _client.request(
      'POST',
      '/api/realtime',
      body: JsonObject({
        'clientId': JsonString(_clientId!),
        'subscriptions': JsonArray(
          _listeners.keys.map(JsonString.new).toList(),
        ),
      }),
      cancellation: _stop,
    );
  }

  Future<void> _run() async {
    final stop = _stop = CancellationToken();
    var reconnects = 0;
    while (!stop.isCancelled && _listeners.isNotEmpty) {
      try {
        final response = await _client.transport.eventStream(
          '/api/realtime',
          headers: {
            'accept': 'text/event-stream',
            ..._client.headersFor(null),
            if (_lastEventId != null) 'last-event-id': _lastEventId!,
          },
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
          final data = decodeExactJson(utf8.encode(message.data));
          if (data is! JsonObject) continue;
          if (message.event == 'PB_CONNECT') {
            final id = data['clientId'];
            if (id is! JsonString)
              throw const PgBaseProtocolError('invalid_realtime_connect');
            _clientId = id.value;
            await _sync();
            reconnects = 0;
            if (!(_ready?.isCompleted ?? true)) _ready!.complete();
            continue;
          }
          if (message.id != null) _lastEventId = _cursor(message.id!);
          final topic = message.event;
          if (topic == null) continue;
          final dynamic event;
          if (topic.startsWith('@')) {
            event = RealtimeCustomEvent(topic: topic, data: data);
          } else {
            final action = data['action'];
            final record = data['record'];
            if (action is! JsonString || record is! JsonObject) continue;
            event = RealtimeEvent(
              topic: topic,
              action: action.value,
              record: record,
            );
          }
          final listeners = _listeners[topic];
          if (listeners != null)
            for (final listener in List.of(listeners)) {
              listener(event);
            }
        }
      } on PgBaseCancellation {
        if (!(_ready?.isCompleted ?? true)) {
          _failReady(const PgBaseCancellation());
        }
        return;
      } catch (error) {
        if (stop.isCancelled) {
          _failReady(const PgBaseCancellation());
          return;
        }
        if (error is PgBaseError && _terminalSseError(error)) {
          _failReady(error);
          return;
        }
        if (reconnects >= maxReconnects) {
          _failReady(error);
          return;
        }
        reconnects++;
        final delay = Duration(
          milliseconds:
              (reconnectDelay.inMilliseconds * (1 << (reconnects - 1))).clamp(
                0,
                10000,
              ),
        );
        await Future.any<void>([
          Future<void>.delayed(delay),
          stop.whenCancelled,
        ]);
      }
    }
    if (!(_ready?.isCompleted ?? true)) _failReady(const PgBaseCancellation());
  }

  void _failReady(Object error) {
    if (!(_ready?.isCompleted ?? true)) _ready!.completeError(error);
  }
}

bool _validTopic(String topic) {
  if (topic.isEmpty) return false;
  if (!topic.startsWith('@')) return true;
  final bytes = utf8.encode(topic).length;
  return bytes >= 2 &&
      bytes <= 256 &&
      !RegExp(r'[\s\x00-\x1f\x7f?#*]').hasMatch(topic);
}

String _applicationWireTopic(String topic) {
  final bytes = utf8.encode(topic);
  if (bytes.isEmpty || bytes.length > 188) {
    throw const PgBaseProtocolError('invalid_realtime_topic');
  }
  return '@app/${base64Url.encode(bytes).replaceAll('=', '')}';
}

Stream<RecordEvent> recordEvents(
  PgBaseClient client,
  String collection, {
  String? lastEventId,
  String? fields,
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
          query: {if (fields != null) 'fields': fields},
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
      } on http.ClientException catch (error) {
        final network = PgBaseNetworkError(error);
        if (stop.isCancelled || generation != client.auth.generation) return;
        if (reconnects >= maxReconnects) throw network;
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
