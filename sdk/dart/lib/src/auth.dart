import 'dart:async';

abstract interface class AuthStorage {
  Future<String?> read();
  Future<void> write(String token);
  Future<void> clear();
}

/// Token persistence is deliberately opt-in. The application selects secure storage.
final class AuthStore {
  AuthStore({this.storage, String? initialToken}) : _token = initialToken;

  final AuthStorage? storage;
  final StreamController<int> _changes = StreamController.broadcast();
  String? _token;
  int _generation = 0;
  Future<void> _storageTail = Future<void>.value();

  String? get token => _token;
  int get generation => _generation;
  Stream<int> get changes => _changes.stream;

  Future<void> restore() async {
    if (storage == null) return;
    final generation = _generation;
    final value = await storage!.read();
    if (generation == _generation) _replace(value);
  }

  Future<void> save(String token) async {
    _replace(token);
    await _persist((storage) => storage.write(token));
  }

  Future<void> clear() async {
    _replace(null);
    await _persist((storage) => storage.clear());
  }

  void _replace(String? value) {
    _token = value;
    _generation++;
    if (!_changes.isClosed) _changes.add(_generation);
  }

  /// Serialize durable changes so a slow old write cannot finish after clear.
  Future<void> _persist(Future<void> Function(AuthStorage storage) action) {
    final configured = storage;
    if (configured == null) return Future<void>.value();
    final operation = _storageTail.then((_) => action(configured));
    _storageTail = operation.catchError((_) {});
    return operation;
  }

  Future<void> close() => _changes.close();
}
