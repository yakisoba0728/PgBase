import 'auth.dart';
import 'errors.dart';
import 'types.dart';

/// Internal lifetime boundary shared by JSON, multipart and file operations.
/// An AuthStore may be shared by clients, so the store owns the generation.
final class AuthScope {
  AuthScope(this._auth) : _generation = _auth.generation;

  final AuthStore _auth;
  int _generation;

  void check() {
    if (_generation != _auth.generation) {
      throw const PgBaseError(403, 'session_changed');
    }
  }

  /// Accept exactly this operation's save, including its asynchronous storage.
  /// A later change while persistence is pending belongs to a different scope.
  Future<void> adopt(String token, {User? record}) async {
    check();
    _generation++;
    try {
      await _auth.save(token, record: record);
      check();
    } catch (_) {
      check();
      rethrow;
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
