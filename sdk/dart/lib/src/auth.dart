import 'dart:async';
import 'dart:convert';

import 'types.dart';

abstract interface class AuthStorage {
  Future<String?> read();
  Future<void> write(String token);
  Future<void> clear();
}

/// Token persistence is deliberately opt-in. The application selects secure storage.
final class AuthStore {
  AuthStore({this.storage, String? initialToken, User? initialRecord})
    : _token = initialToken,
      _record = initialRecord;

  final AuthStorage? storage;
  final StreamController<int> _changes = StreamController.broadcast();
  String? _token;
  User? _record;
  int _generation = 0;
  Future<void> _storageTail = Future<void>.value();

  String? get token => _token;
  User? get record => _record;
  bool get isValid => _token != null && _token!.isNotEmpty;
  int get generation => _generation;
  Stream<int> get changes => _changes.stream;

  Future<void> restore() async {
    if (storage == null) return;
    final generation = _generation;
    final value = await _serialized(() => storage!.read());
    if (generation == _generation) _replace(value);
  }

  Future<void> save(String token, {User? record}) async {
    _replace(token, record: record);
    await _persist((storage) => storage.write(token));
  }

  Future<void> clear() async {
    _replace(null);
    await _persist((storage) => storage.clear());
  }

  void _replace(String? value, {User? record}) {
    _token = value;
    _record = value == null ? null : record ?? _record;
    _generation++;
    if (!_changes.isClosed) _changes.add(_generation);
  }

  /// Serialize durable changes so a slow old write cannot finish after clear.
  Future<void> _persist(Future<void> Function(AuthStorage storage) action) {
    final configured = storage;
    if (configured == null) return Future<void>.value();
    return _serialized(() => action(configured));
  }

  Future<T> _serialized<T>(Future<T> Function() action) {
    final operation = _storageTail.then((_) => action());
    _storageTail = operation.then<void>((_) {}, onError: (_, _) {});
    return operation;
  }

  Future<void> close() => _changes.close();

  /// Cookie helpers use JSON URI encoding so SSR can hand an auth state to a
  /// browser without relying on a platform-specific storage implementation.
  String exportToCookie({String key = 'pb_auth', String path = '/'}) {
    final record = _record;
    final payload = jsonEncode({
      'token': _token,
      'record': record == null ? null : {
        'id': record.id,
        'email': record.email,
        'createdAt': record.createdAt,
        if (record.verified != null) 'verified': record.verified,
        if (record.authCollection != null) 'authCollection': record.authCollection,
        if (record.authCollectionId != null) 'authCollectionId': record.authCollectionId,
      },
    });
    return '$key=${Uri.encodeComponent(payload)}; Path=$path; HttpOnly';
  }

  Future<void> loadFromCookie(String cookie, {String key = 'pb_auth'}) async {
    final encoded = cookie.split(';').map((part) => part.trim()).cast<String?>().firstWhere(
      (part) => part!.startsWith('$key='),
      orElse: () => null,
    );
    if (encoded == null) return;
    try {
      final value = jsonDecode(Uri.decodeComponent(encoded.substring(key.length + 1)));
      if (value is! Map || (value['token'] != null && value['token'] is! String)) throw const FormatException();
      User? record;
      final rawRecord = value['record'];
      if (rawRecord != null) {
        if (rawRecord is! Map || rawRecord['id'] is! String || rawRecord['email'] is! String || rawRecord['createdAt'] is! String) throw const FormatException();
        record = User(id: rawRecord['id'] as String, email: rawRecord['email'] as String, createdAt: rawRecord['createdAt'] as String, verified: rawRecord['verified'] as bool?, authCollection: rawRecord['authCollection'] as String?, authCollectionId: rawRecord['authCollectionId'] as String?);
      }
      _replace(value['token'] as String?, record: record);
    } catch (_) { _replace(null); }
  }
}
