import 'auth.dart';
import 'errors.dart';

/// Internal lifetime boundary shared by JSON, multipart and file operations.
/// An AuthStore may be shared by clients, so the store owns the generation.
final class AuthScope {
  AuthScope(this._auth) : _generation = _auth.generation;

  final AuthStore _auth;
  final int _generation;

  void check() {
    if (_generation != _auth.generation) {
      throw const PgBaseError(403, 'session_changed');
    }
  }

  Future<T> run<T>(Future<T> Function() action) async {
    check();
    try {
      final result = await action();
      check();
      return result;
    } catch (_) {
      check();
      rethrow;
    }
  }
}
