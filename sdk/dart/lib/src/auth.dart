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

  String? get token => _token;
  int get generation => _generation;
  Stream<int> get changes => _changes.stream;

  Future<void> restore() async {
    if (storage == null) return;
    final value = await storage!.read();
    _replace(value);
  }

  Future<void> save(String token) async {
    _replace(token);
    await storage?.write(token);
  }

  Future<void> clear() async {
    _replace(null);
    await storage?.clear();
  }

  void _replace(String? value) {
    _token = value;
    _generation++;
    if (!_changes.isClosed) _changes.add(_generation);
  }

  Future<void> close() => _changes.close();
}
