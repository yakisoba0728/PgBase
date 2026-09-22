import 'dart:async';

import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

void main() {
  test('legacy storage persists the opaque token only', () async {
    final storage = MemoryTokenStorage();
    final auth = AuthStore(storage: storage);
    await auth.save(
      'opaque-session',
      record: const User(id: 'one', email: 'a@test', createdAt: 'now'),
    );
    final restored = AuthStore(storage: storage);
    await restored.restore();
    expect(restored.token, 'opaque-session');
    expect(restored.record, isNull);
    expect(restored.isValid, isTrue);
    await auth.close();
    await restored.close();
  });

  test('state writes serialize before clear and restore cannot resurrect stale auth', () async {
    final storage = DelayedStateStorage();
    final auth = AuthStore(stateStorage: storage);
    final saving = auth.save(
      'old',
      record: const User(id: 'one', email: 'a@test', createdAt: 'now'),
    );
    await storage.started.future;
    final clearing = auth.clear();
    storage.allowWrite.complete();
    await Future.wait([saving, clearing]);
    expect(storage.value, isNull);
    storage.value = const AuthState(token: 'stale');
    final restoring = auth.restore();
    await storage.readStarted.future;
    final cleared = auth.clear();
    storage.allowRead.complete();
    await Future.wait([restoring, cleared]);
    expect(auth.token, isNull);
    expect(auth.record, isNull);
    expect(storage.value, isNull);
    await auth.close();
  });

  test('ambiguous storage configuration is rejected', () {
    expect(
      () => AuthStore(
        storage: MemoryTokenStorage(),
        stateStorage: MemoryStateStorage(),
      ),
      throwsArgumentError,
    );
  });
  test('opt-in state storage restores the token and record together', () async {
    final storage = MemoryStateStorage();
    final first = AuthStore(stateStorage: storage);
    await first.save(
      'opaque-session',
      record: const User(
        id: 'CaseSensitive01',
        email: 'member@example.test',
        createdAt: '2026-01-01T00:00:00Z',
        verified: true,
      ),
    );
    final restored = AuthStore(stateStorage: storage);
    await restored.restore();
    expect(restored.token, 'opaque-session');
    expect(restored.record?.id, 'CaseSensitive01');
    expect(restored.record?.verified, isTrue);
    expect(restored.isValid, isTrue);
    await restored.clear();
    final cleared = AuthStore(stateStorage: storage);
    await cleared.restore();
    expect(cleared.token, isNull);
    expect(cleared.record, isNull);
    await first.close();
    await restored.close();
    await cleared.close();
  });
}

final class MemoryTokenStorage implements AuthStorage {
  String? value;
  @override
  Future<String?> read() async => value;
  @override
  Future<void> write(String token) async {
    value = token;
  }

  @override
  Future<void> clear() async {
    value = null;
  }
}

final class DelayedStateStorage implements AuthStateStorage {
  AuthState? value;
  final started = Completer<void>();
  final allowWrite = Completer<void>();
  final readStarted = Completer<void>();
  final allowRead = Completer<void>();
  @override
  Future<AuthState?> read() async {
    final snapshot = value;
    readStarted.complete();
    await allowRead.future;
    return snapshot;
  }

  @override
  Future<void> write(AuthState state) async {
    started.complete();
    await allowWrite.future;
    value = state;
  }

  @override
  Future<void> clear() async {
    value = null;
  }
}

final class MemoryStateStorage implements AuthStateStorage {
  AuthState? value;
  @override
  Future<AuthState?> read() async => value;
  @override
  Future<void> write(AuthState state) async {
    value = state;
  }

  @override
  Future<void> clear() async {
    value = null;
  }
}
