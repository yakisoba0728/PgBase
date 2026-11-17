import 'dart:async';
import 'dart:convert';

import 'auth_cookie.dart';
import 'types.dart';

abstract interface class AuthStorage {
  Future<String?> read();
  Future<void> write(String token);
  Future<void> clear();
}

/// Immutable authentication snapshot. Tokens remain opaque session credentials.
final class AuthState {
  const AuthState({required this.token, this.record});
  final String? token;
  final User? record;
}

/// Optional persistence of both token and user; legacy [AuthStorage] is token-only.
abstract interface class AuthStateStorage {
  Future<AuthState?> read();
  Future<void> write(AuthState state);
  Future<void> clear();
}

/// Persistence is deliberately opt-in. The application selects secure storage.
final class AuthStore {
  AuthStore({
    this.storage,
    this.stateStorage,
    String? initialToken,
    User? initialRecord,
  }) : _token = initialToken,
       _record = initialToken == null ? null : initialRecord {
    if (storage != null && stateStorage != null) {
      throw ArgumentError('Configure storage or stateStorage, not both.');
    }
  }

  final AuthStorage? storage;
  final AuthStateStorage? stateStorage;
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
    if (storage == null && stateStorage == null) return;
    final generation = _generation;
    final value = await _serialized(
      () async => stateStorage != null
          ? await stateStorage!.read()
          : AuthState(token: await storage!.read()),
    );
    if (generation == _generation) {
      _replace(value?.token, record: value?.record);
    }
  }

  Future<void> save(String token, {User? record}) async {
    _replace(token, record: record);
    await _serialized(() async {
      if (stateStorage != null) {
        await stateStorage!.write(AuthState(token: token, record: record));
      } else {
        await storage?.write(token);
      }
    });
  }

  Future<void> clear() async {
    _replace(null);
    await _serialized(() async {
      if (stateStorage != null) {
        await stateStorage!.clear();
      } else {
        await storage?.clear();
      }
    });
  }

  void _replace(String? value, {User? record}) {
    _token = value;
    _record = value == null ? null : record;
    _generation++;
    if (!_changes.isClosed) _changes.add(_generation);
  }

  /// Serialize durable changes so a slow old write cannot finish after clear.
  Future<T> _serialized<T>(Future<T> Function() action) {
    final operation = _storageTail.then((_) => action());
    _storageTail = operation.then<void>((_) {}, onError: (_, _) {});
    return operation;
  }

  Future<void> close() => _changes.close();

  /// Encodes cached state, not proof of authentication. Set secure for HTTPS.
  String exportToCookie({
    String key = 'pgbase_auth',
    String path = '/',
    bool httpOnly = true,
    bool secure = false,
    String sameSite = 'Lax',
  }) {
    final record = _record;
    return serializeAuthCookie(
      {
        'token': _token,
        'record': record == null
            ? null
            : {
                'id': record.id,
                'email': record.email,
                'createdAt': record.createdAt,
                if (record.verified != null) 'verified': record.verified,
                if (record.emailVisibility != null)
                  'emailVisibility': record.emailVisibility,
                if (record.authCollection != null)
                  'authCollection': record.authCollection,
                if (record.authCollectionId != null)
                  'authCollectionId': record.authCollectionId,
              },
      },
      key: key,
      path: path,
      httpOnly: httpOnly,
      secure: secure,
      sameSite: sameSite,
    );
  }

  /// Loads untrusted cached state. The server must still validate its token.
  Future<void> loadFromCookie(String cookie, {String key = 'pgbase_auth'}) async {
    validateAuthCookieName(key);
    final encoded = cookie
        .split(';')
        .map((part) => part.trim())
        .cast<String?>()
        .firstWhere((part) => part!.startsWith('$key='), orElse: () => null);
    if (encoded == null) return;
    try {
      final value = jsonDecode(
        Uri.decodeComponent(encoded.substring(key.length + 1)),
      );
      if (value is! Map ||
          (value['token'] != null && value['token'] is! String)) {
        throw const FormatException();
      }
      User? record;
      final rawRecord = value['record'];
      if (rawRecord != null) {
        if (rawRecord is! Map ||
            rawRecord['id'] is! String ||
            rawRecord['email'] is! String ||
            rawRecord['createdAt'] is! String) {
          throw const FormatException();
        }
        record = User(
          id: rawRecord['id'] as String,
          email: rawRecord['email'] as String,
          createdAt: rawRecord['createdAt'] as String,
          verified: rawRecord['verified'] as bool?,
          emailVisibility: rawRecord['emailVisibility'] as bool?,
          authCollection: rawRecord['authCollection'] as String?,
          authCollectionId: rawRecord['authCollectionId'] as String?,
        );
      }
      final token = value['token'] as String?;
      _replace(token == null || token.isEmpty ? null : token, record: record);
    } catch (_) {
      _replace(null);
    }
  }
}
