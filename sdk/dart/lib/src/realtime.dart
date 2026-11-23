import 'dart:async';
import 'dart:collection';
import 'dart:convert';

import 'package:http/http.dart' as http;

import 'client.dart';
import 'errors.dart';
import 'exact_json.dart';
import 'transport.dart';
import 'types.dart';

enum RealtimeState { connecting, connected, reconnecting, stopped }

enum RealtimeStopReason { unsubscribed, terminal, exhausted, reset }

enum RealtimeErrorSource { transport, listener }

/// Immutable shared-session lifecycle snapshot; cursors retain their exact text.
final class RealtimeStatus {
  const RealtimeStatus({
    required this.state,
    this.attempt = 0,
    this.lastEventId,
    this.reason,
    this.error,
    this.source,
    this.topic,
  });
  final RealtimeState state;
  final int attempt;
  final String? lastEventId;
  final RealtimeStopReason? reason;
  final Object? error;
  final RealtimeErrorSource? source;
  final String? topic;
}

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

typedef _Registration = ({
  String topic,
  Set<Function> listeners,
  Function listener,
  bool added,
});

typedef _SubscriptionOutcome = ({
  Future<void> Function()? dispose,
  Object? error,
});

final class _QueuedSubscription {
  _QueuedSubscription(this.topics, this.listener, this.generation);
  final List<String> topics;
  final Function listener;
  final int generation;
  final completer = Completer<Future<void> Function()>();

  void settle(_SubscriptionOutcome outcome) {
    final dispose = outcome.dispose;
    if (dispose != null) {
      completer.complete(dispose);
    } else {
      completer.completeError(outcome.error!);
    }
  }
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
  final _queued = <_QueuedSubscription>[];
  bool _draining = false;
  Future<void> _syncing = Future<void>.value();
  Completer<void>? _ready;
  final _observers = <FutureOr<void> Function(RealtimeStatus)>{};
  RealtimeStatus _status = const RealtimeStatus(state: RealtimeState.stopped);

  RealtimeStatus get status => RealtimeStatus(
    state: _status.state,
    attempt: _status.attempt,
    lastEventId: _lastEventId,
    reason: _status.reason,
    error: _status.error,
    source: _status.source,
    topic: _status.topic,
  );

  /// Observes the shared session, starting with its current snapshot.
  void Function() onStatus(FutureOr<void> Function(RealtimeStatus) observer) {
    _observers.add(observer);
    _invokeObserver(observer, status);
    return () {
      _observers.remove(observer);
    };
  }

  /// Stops subscriptions and forgets the replay cursor for an explicit resync.
  Future<void> resetCursor() async {
    await unsubscribe();
    await _running;
    _lastEventId = null;
    _emit(RealtimeState.stopped, reason: RealtimeStopReason.reset);
  }

  /// Subscribes to collection topics and reserved `@` application topics.
  /// Collection callbacks receive [RealtimeEvent]; application callbacks
  /// receive [RealtimeCustomEvent] with arbitrary JSON [RealtimeCustomEvent.data].
  Future<Future<void> Function()> subscribe(String topic, Function listener) =>
      _subscribeTopics([topic], listener);

  /// Registers all topics with one atomic server replacement.
  Future<Future<void> Function()> subscribeMany(
    Iterable<String> topics,
    Function listener,
  ) => _subscribeTopics(topics.toSet().toList(), listener);

  Future<Future<void> Function()> _subscribeTopics(
    List<String> topics,
    Function listener,
  ) {
    final request = _QueuedSubscription(
      topics,
      listener,
      _client.auth.generation,
    );
    _queued.add(request);
    if (!_draining) unawaited(_drain());
    return request.completer.future;
  }

  /// Installs queued subscriptions in call order. Requests that queue behind
  /// an in-flight one share a single replacement request. A failed shared
  /// replacement leaves the server unchanged, so each request is then retried
  /// alone and only the failing one completes with an error, as if they had
  /// run serially.
  Future<void> _drain() async {
    _draining = true;
    try {
      while (_queued.isNotEmpty) {
        // A connection handshake replaces with its own request's topics only:
        // a denied topic there would stop the whole shared stream.
        final count = _clientId == null ? 1 : _queued.length;
        final taken = _queued.sublist(0, count);
        _queued.removeRange(0, count);
        final batch = <_QueuedSubscription>[];
        for (final request in taken) {
          final error = _requestError(request);
          if (error == null) {
            batch.add(request);
          } else {
            request.completer.completeError(error);
          }
        }
        if (batch.length > 1) {
          List<_SubscriptionOutcome>? outcomes;
          try {
            outcomes = await _subscribe(batch, stopWhenEmpty: false);
          } catch (_) {
            // Retried one at a time below.
          }
          if (outcomes != null) {
            for (var index = 0; index < batch.length; index++) {
              batch[index].settle(outcomes[index]);
            }
            continue;
          }
        }
        for (final request in batch) {
          try {
            request.settle(
              (await _subscribe([request], stopWhenEmpty: true)).single,
            );
          } catch (error, stack) {
            request.completer.completeError(error, stack);
          }
        }
      }
    } finally {
      _draining = false;
    }
  }

  Object? _requestError(_QueuedSubscription request) {
    if (request.generation != _client.auth.generation)
      return const PgBaseCancellation();
    if (request.topics.isEmpty ||
        request.topics.any((topic) => !_validTopic(topic)))
      return const PgBaseProtocolError('invalid_realtime_topic');
    return null;
  }

  /// Registers the requests' listeners and replaces the server topic list
  /// once. A shared failure rolls every request back; [stopWhenEmpty] is false
  /// when the caller retries them, so the stream is not torn down in between.
  Future<List<_SubscriptionOutcome>> _subscribe(
    List<_QueuedSubscription> requests, {
    required bool stopWhenEmpty,
  }) async {
    final generation = requests.first.generation;
    if (_stop?.isCancelled ?? false) await _running;
    // Reinstall established topics before attempting to add new ones.
    final previousStop = _stop;
    while (_running != null &&
        !(_ready?.isCompleted ?? true) &&
        !(previousStop?.isCancelled ?? true)) {
      await _ready!.future;
    }
    if (generation != _client.auth.generation ||
        (_running != null && (previousStop?.isCancelled ?? false)))
      throw const PgBaseCancellation();
    final registrations = [
      for (final request in requests)
        <_Registration>[
          for (final topic in request.topics)
            _register(topic, request.listener),
        ],
    ];
    void rollBack(List<_Registration> items) {
      for (final item in items) {
        if (item.added) {
          _removeListener(item.topic, item.listeners, item.listener);
        }
      }
    }

    _ensureRunning();
    final stop = _stop;
    try {
      if (_clientId != null) {
        if (!await _sync())
          throw const PgBaseProtocolError('realtime_connection_changed');
      } else {
        await _ready!.future;
      }
      if (generation != _client.auth.generation || (stop?.isCancelled ?? true))
        throw const PgBaseCancellation();
    } catch (_) {
      registrations.forEach(rollBack);
      if (stopWhenEmpty && _listeners.isEmpty && !(_stop?.isCancelled ?? true))
        await unsubscribe();
      rethrow;
    }
    final outcomes = <_SubscriptionOutcome>[];
    for (final items in registrations) {
      if (items.any(
        (item) =>
            !identical(_listeners[item.topic], item.listeners) ||
            !item.listeners.contains(item.listener),
      )) {
        rollBack(items);
        outcomes.add((dispose: null, error: const PgBaseCancellation()));
        continue;
      }
      outcomes.add((
        dispose: () async {
          if (!identical(_stop, stop)) return;
          for (final item in items) {
            _removeListener(item.topic, item.listeners, item.listener);
          }
          await _updateSubscriptions();
        },
        error: null,
      ));
    }
    if (outcomes.any((outcome) => outcome.error != null) &&
        _listeners.isEmpty &&
        !(_stop?.isCancelled ?? true))
      await unsubscribe();
    return outcomes;
  }

  _Registration _register(String topic, Function listener) {
    final listeners = _listeners[topic] ??= {};
    return (
      topic: topic,
      listeners: listeners,
      listener: listener,
      added: listeners.add(listener),
    );
  }

  void _removeListener(
    String topic,
    Set<Function> listeners,
    Function listener,
  ) {
    if (!identical(_listeners[topic], listeners)) return;
    listeners.remove(listener);
    if (listeners.isEmpty) _listeners.remove(topic);
  }

  /// Subscribes to an application topic while preserving its raw UTF-8 name.
  /// The reserved `@app/...` name remains an internal realtime wire detail.
  Future<Future<void> Function()> subscribeApplication(
    String topic,
    FutureOr<void> Function(RealtimeCustomEvent) listener,
  ) {
    final wireTopic = _applicationWireTopic(topic);
    return subscribe(wireTopic, (dynamic event) {
      if (event is RealtimeCustomEvent && event.topic == wireTopic) {
        _deliver(
          listener,
          RealtimeCustomEvent(topic: topic, data: event.data),
          topic,
        );
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
    await _updateSubscriptions();
  }

  Future<void> _updateSubscriptions() async {
    if (_listeners.isEmpty) {
      _stop?.cancel();
      _clientId = null;
      _failReady(const PgBaseCancellation());
      _emit(RealtimeState.stopped, reason: RealtimeStopReason.unsubscribed);
      return;
    }
    if (_clientId != null) await _sync();
  }

  void _ensureRunning() {
    if (_running != null) return;
    _newReadiness();
    final stop = _stop = CancellationToken();
    final generation = _client.auth.generation;
    final authChanges = _client.auth.changes.listen((next) {
      if (next != generation && identical(_stop, stop))
        _finish(const PgBaseCancellation(), RealtimeStopReason.unsubscribed);
    });
    _running = Future<void>.microtask(() => _run(stop, generation))
        .whenComplete(() async {
          await authChanges.cancel();
          _running = null;
          _ready = null;
        });
  }

  void _newReadiness() {
    _ready = Completer<void>();
    // A reconnect can stop with nobody waiting for its readiness.
    unawaited(
      _ready!.future.then<void>((_) {}, onError: (Object _, StackTrace _) {}),
    );
  }

  Future<bool> _sync() {
    final stop = _stop;
    final clientId = _clientId;
    final result = _syncing.then((_) async {
      if (!identical(stop, _stop) ||
          (stop?.isCancelled ?? true) ||
          clientId == null ||
          clientId != _clientId)
        return false;
      await _client.request(
        'POST',
        '/api/realtime',
        body: JsonObject({
          'clientId': JsonString(clientId),
          'subscriptions': JsonArray(
            _listeners.keys.map(JsonString.new).toList(),
          ),
        }),
        cancellation: stop,
      );
      return true;
    });
    _syncing = result.then<void>((_) {}, onError: (Object _, StackTrace _) {});
    return result;
  }

  Future<void> _run(CancellationToken stop, int generation) async {
    if (generation != _client.auth.generation) {
      _finish(const PgBaseCancellation(), RealtimeStopReason.unsubscribed);
      return;
    }
    if (_listeners.isEmpty) {
      _failReady(const PgBaseCancellation());
      return;
    }
    var reconnects = 0;
    _emit(RealtimeState.connecting);
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
          if (generation != _client.auth.generation) {
            _finish(
              const PgBaseCancellation(),
              RealtimeStopReason.unsubscribed,
            );
            return;
          }
          if (stop.isCancelled) return;
          final data = decodeExactJson(utf8.encode(message.data));
          if (message.event == 'PB_ERROR') {
            final rawCode = data is JsonObject ? data['code'] : null;
            final code = rawCode is JsonString
                ? rawCode.value
                : 'subscription_failed';
            final error = PgBaseError(
              code == 'cursor_expired'
                  ? 410
                  : code == 'invalid_cursor'
                  ? 400
                  : 0,
              code,
            );
            final retryable = data is JsonObject ? data['retryable'] : null;
            if (code == 'subscription_failed' &&
                retryable is JsonBool &&
                retryable.value)
              throw error;
            _finish(error, RealtimeStopReason.terminal);
            return;
          }
          if (message.event == 'PB_CONNECT') {
            if (data is! JsonObject)
              throw const PgBaseProtocolError('invalid_realtime_connect');
            final id = data['clientId'];
            if (id is! JsonString)
              throw const PgBaseProtocolError('invalid_realtime_connect');
            _clientId = id.value;
            await _sync();
            if (generation != _client.auth.generation) {
              _finish(
                const PgBaseCancellation(),
                RealtimeStopReason.unsubscribed,
              );
              return;
            }
            if (stop.isCancelled) return;
            if (!(_ready?.isCompleted ?? true)) _ready!.complete();
            _emit(RealtimeState.connected, attempt: reconnects);
            continue;
          }
          if (message.id != null) _lastEventId = _cursor(message.id!);
          final topic = message.event;
          if (topic == null) continue;
          final dynamic event;
          if (topic.startsWith('@')) {
            event = RealtimeCustomEvent(topic: topic, data: data);
          } else {
            if (data is! JsonObject) continue;
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
            for (final listener in List<Function>.of(listeners)) {
              if (generation != _client.auth.generation) {
                _finish(
                  const PgBaseCancellation(),
                  RealtimeStopReason.unsubscribed,
                );
                return;
              }
              _deliver(listener, event, topic);
            }
          if (listeners != null) reconnects = 0;
        }
        if (!stop.isCancelled)
          throw const PgBaseProtocolError('realtime_stream_ended');
      } on PgBaseCancellation {
        _failReady(const PgBaseCancellation());
        if (_status.state != RealtimeState.stopped)
          _finish(const PgBaseCancellation(), RealtimeStopReason.unsubscribed);
        return;
      } catch (error) {
        if (stop.isCancelled) {
          _failReady(const PgBaseCancellation());
          if (_status.state != RealtimeState.stopped)
            _finish(
              const PgBaseCancellation(),
              RealtimeStopReason.unsubscribed,
            );
          return;
        }
        if (error is PgBaseError && _terminalSseError(error)) {
          _finish(error, RealtimeStopReason.terminal);
          return;
        }
        if (reconnects >= maxReconnects) {
          _finish(error, RealtimeStopReason.exhausted);
          return;
        }
        reconnects++;
        _clientId = null;
        if (_ready?.isCompleted ?? false) _newReadiness();
        _emit(
          RealtimeState.reconnecting,
          attempt: reconnects,
          error: error,
          source: RealtimeErrorSource.transport,
        );
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

  void _finish(Object error, RealtimeStopReason reason) {
    _clientId = null;
    _listeners.clear();
    _stop?.cancel();
    _failReady(error);
    _emit(
      RealtimeState.stopped,
      attempt: _status.attempt,
      reason: reason,
      error: reason == RealtimeStopReason.unsubscribed ? null : error,
      source: reason == RealtimeStopReason.unsubscribed
          ? null
          : RealtimeErrorSource.transport,
    );
  }

  void _listenerFailure(Object error, String topic) => _emit(
    _status.state,
    attempt: _status.attempt,
    reason: _status.reason,
    error: error,
    source: RealtimeErrorSource.listener,
    topic: topic,
  );

  void _deliver(Function listener, Object event, String topic) {
    final stop = _stop;
    final generation = _client.auth.generation;
    // Listener futures can outlive the connection and its authenticated owner.
    void failed(Object error) {
      if (identical(_stop, stop) &&
          stop != null &&
          !stop.isCancelled &&
          generation == _client.auth.generation) {
        _listenerFailure(error, topic);
      }
    }

    try {
      final result = Function.apply(listener, [event]);
      if (result is Future)
        result.then<void>(
          (_) {},
          onError: (Object error, StackTrace stack) {
            failed(error);
          },
        );
    } catch (error) {
      failed(error);
    }
  }

  void _emit(
    RealtimeState state, {
    int attempt = 0,
    RealtimeStopReason? reason,
    Object? error,
    RealtimeErrorSource? source,
    String? topic,
  }) {
    final snapshot = _status = RealtimeStatus(
      state: state,
      attempt: attempt,
      lastEventId: _lastEventId,
      reason: reason,
      error: error,
      source: source,
      topic: topic,
    );
    for (final observer in List.of(_observers)) {
      if (!identical(_status, snapshot)) break;
      _invokeObserver(observer, snapshot);
    }
  }

  void _invokeObserver(
    FutureOr<void> Function(RealtimeStatus) observer,
    RealtimeStatus snapshot,
  ) {
    try {
      final result = observer(snapshot);
      if (result is Future)
        result.then<void>((_) {}, onError: (Object error, StackTrace stack) {});
    } catch (_) {
      /* Observer failures never affect delivery or report recursively. */
    }
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
  if (fields != null && fields.isNotEmpty)
    throw const PgBaseProtocolError(
      'unsupported_event_projection: metadata events have no records; use realtime.subscribe',
    );
  if (maxReconnects < 0 || reconnectDelay.isNegative)
    throw const PgBaseProtocolError('invalid_reconnect_options');
  if (cancellation?.isCancelled ?? false) return;
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
        if (!stop.isCancelled)
          throw const PgBaseProtocolError('realtime_stream_ended');
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
