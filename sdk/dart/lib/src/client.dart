import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;

import 'auth.dart';
import 'errors.dart';
import 'exact_json.dart';
import 'files.dart';
import 'records.dart';
import 'realtime.dart';
import 'transport.dart';
import 'types.dart';

typedef OAuthUrlLauncher = FutureOr<void> Function(Uri url);

final class OAuthBrowserAdapter {
  const OAuthBrowserAdapter({
    required this.open,
    required this.waitForCallback,
    this.close,
  });
  final OAuthUrlLauncher open;
  final Future<Uri> Function(CancellationToken? cancellation) waitForCallback;
  final void Function()? close;
}

final class PgBaseClient {
  PgBaseClient(Uri baseUri, {http.Client? httpClient, AuthStore? auth})
    : transport = PgBaseTransport(baseUri, client: httpClient),
      auth = auth ?? AuthStore();

  final PgBaseTransport transport;
  final AuthStore auth;
  late final RealtimeService realtime = RealtimeService(this);
  final Map<String, CancellationToken> _requestControllers = {};
  bool _autoCancellation = true;

  void close() => transport.close();

  /// Enables PocketBase-style cancellation of a previous request with the
  /// same key.  It is enabled by default.
  PgBaseClient autoCancellation(bool enabled) {
    _autoCancellation = enabled;
    return this;
  }

  PgBaseClient cancelRequest(String requestKey) {
    _requestControllers.remove(requestKey)?.cancel();
    return this;
  }

  PgBaseClient cancelAllRequests() {
    for (final token in _requestControllers.values) {
      token.cancel();
    }
    _requestControllers.clear();
    return this;
  }

  AuthRealm realm(String name) => AuthRealm(this, name);
  Records records(String collection) => Records(this, collection);
  Files files(String collection, String recordId) =>
      Files(this, collection, recordId);

  /// Submits the compact operation form accepted by PgBase's batch endpoint.
  Future<JsonObject> batch(
    List<BatchOperation> operations, {
    String? fields,
    CancellationToken? cancellation,
  }) => submitBatch(this, operations, fields: fields, cancellation: cancellation);

  /// Submits the HTTP-shaped batch form. Each request is an object with the
  /// server's `method`, `url`, `body`, and optional `headers` fields.
  Future<JsonObject> batchRequests(
    List<JsonObject> requests, {
    String? fields,
    CancellationToken? cancellation,
  }) async {
    final response = await request(
      'POST',
      '/api/v1/user/collections/batch',
      body: JsonObject({'requests': JsonArray(requests)}),
      query: {if (fields != null) 'fields': fields},
      cancellation: cancellation,
    );
    if (response is! JsonObject)
      throw const PgBaseProtocolError('invalid_batch_response');
    return response;
  }

  /// Submits typed HTTP-shaped batch requests, including PUT upserts.
  Future<JsonObject> batchHttpRequests(
    List<BatchHttpRequest> requests, {
    String? fields,
    CancellationToken? cancellation,
  }) => batchRequests(
    requests.map((request) => request.value).toList(growable: false),
    fields: fields,
    cancellation: cancellation,
  );

  Future<UserSession> register(
    String email,
    String password, {
    String? fields,
    CancellationToken? cancellation,
  }) => _authenticate(
    '/api/v1/auth/register',
    email,
    password,
    cancellation: cancellation,
    fields: fields,
  );
  Future<UserSession> login(
    String email,
    String password, {
    String? fields,
    CancellationToken? cancellation,
  }) => _authenticate(
    '/api/v1/auth/login',
    email,
    password,
    cancellation: cancellation,
    fields: fields,
    mfa: true,
  );
  Future<UserSession> session({CancellationToken? cancellation}) async =>
      _decodeSession(
        await request(
          'GET',
          '/api/v1/auth/session',
          cancellation: cancellation,
        ),
      );

  Future<void> logout({CancellationToken? cancellation}) async {
    try {
      await request('POST', '/api/v1/auth/logout', cancellation: cancellation);
    } finally {
      await auth.clear();
    }
  }

  Future<void> changePassword(
    String currentPassword,
    String newPassword, {
    CancellationToken? cancellation,
  }) => _clearAfter(
    '/api/v1/auth/change-password',
    JsonObject({
      'currentPassword': JsonString(currentPassword),
      'newPassword': JsonString(newPassword),
    }),
    cancellation,
  );
  Future<void> requestPasswordReset(
    String email, {
    CancellationToken? cancellation,
  }) => _voidPost('/api/v1/auth/request-password-reset', {
    'email': JsonString(email),
  }, cancellation);
  Future<void> confirmPasswordReset(
    String token,
    String newPassword, {
    CancellationToken? cancellation,
  }) => _voidPost('/api/v1/auth/confirm-password-reset', {
    'token': JsonString(token),
    'newPassword': JsonString(newPassword),
  }, cancellation);
  Future<void> requestVerification({CancellationToken? cancellation}) async =>
      request(
        'POST',
        '/api/v1/auth/request-verification',
        cancellation: cancellation,
      );
  Future<void> confirmVerification(
    String token, {
    CancellationToken? cancellation,
  }) => _voidPost('/api/v1/auth/confirm-verification', {
    'token': JsonString(token),
  }, cancellation);
  Future<void> requestEmailChange(
    String currentPassword,
    String newEmail, {
    CancellationToken? cancellation,
  }) => _voidPost('/api/v1/auth/request-email-change', {
    'currentPassword': JsonString(currentPassword),
    'newEmail': JsonString(newEmail),
  }, cancellation);
  Future<void> confirmEmailChange(
    String token, {
    CancellationToken? cancellation,
  }) => _clearAfter(
    '/api/v1/auth/confirm-email-change',
    JsonObject({'token': JsonString(token)}),
    cancellation,
  );

  Future<JsonObject> requestOtp(
    String email, {
    CancellationToken? cancellation,
  }) => _objectRequest(
    'POST',
    '/api/v1/auth/otp/request',
    JsonObject({'email': JsonString(email)}),
    cancellation,
  );
  Future<UserSession> completeOtp(
    String otpId,
    String code, {
    CancellationToken? cancellation,
  }) => _completeSession(
    '/api/v1/auth/otp/complete',
    {'otpId': JsonString(otpId), 'code': JsonString(code)},
    cancellation,
    mfa: true,
  );
  Future<UserSession> completeMfa(
    String challengeToken,
    String code, {
    CancellationToken? cancellation,
  }) => _completeSession('/api/v1/auth/mfa/complete', {
    'challengeToken': JsonString(challengeToken),
    'code': JsonString(code),
  }, cancellation);
  Future<JsonObject> beginTotpEnrollment(
    String currentPassword, {
    CancellationToken? cancellation,
  }) => _objectRequest(
    'POST',
    '/api/v1/auth/totp/enroll',
    JsonObject({'currentPassword': JsonString(currentPassword)}),
    cancellation,
  );
  Future<JsonObject> confirmTotpEnrollment(
    String code, {
    CancellationToken? cancellation,
  }) => _objectRequest(
    'POST',
    '/api/v1/auth/totp/confirm',
    JsonObject({'code': JsonString(code)}),
    cancellation,
  );

  Future<JsonObject> oauthStart(
    String provider,
    String redirectUri, {
    CancellationToken? cancellation,
  }) => _objectRequest(
    'POST',
    '/api/v1/auth/oauth/start',
    JsonObject({
      'provider': JsonString(provider),
      'redirectUri': JsonString(redirectUri),
    }),
    cancellation,
  );

  /// Starts OAuth and lets the host open a browser popup, redirect, or deep link.
  Future<JsonObject> oauthStartAndOpen(
    String provider,
    String redirectUri,
    OAuthUrlLauncher open, {
    CancellationToken? cancellation,
  }) async {
    final started = await oauthStart(provider, redirectUri, cancellation: cancellation);
    final authorizationUrl = started['authorizationUrl'];
    if (authorizationUrl is! JsonString) {
      throw const PgBaseProtocolError('invalid_oauth_start_response');
    }
    await open(Uri.parse(authorizationUrl.value));
    return started;
  }

  /// Runs OAuth start, launch, callback validation, and completion in one
  /// cancellable operation. The adapter owns platform popup/deep-link I/O.
  Future<UserSession> oauthWithBrowser(
    String provider,
    String redirectUri,
    OAuthBrowserAdapter adapter, {
    CancellationToken? cancellation,
  }) async {
    try {
      final started = await oauthStart(provider, redirectUri, cancellation: cancellation);
      final authorizationUrl = started['authorizationUrl'];
      if (authorizationUrl is! JsonString) throw const OAuthCallbackError('invalid_start');
      final expectedState = Uri.parse(authorizationUrl.value).queryParameters['state'];
      if (expectedState == null || expectedState.isEmpty) throw const OAuthCallbackError('invalid_start');
      await adapter.open(Uri.parse(authorizationUrl.value));
      final callback = await adapter.waitForCallback(cancellation);
      final redirect = Uri.parse(redirectUri);
      if (callback.scheme != redirect.scheme || callback.authority != redirect.authority) {
        throw const OAuthCallbackError('invalid_callback_origin');
      }
      final error = callback.queryParameters['error'];
      if (error != null) throw OAuthCallbackError(error, callback.queryParameters['error_description']);
      final state = callback.queryParameters['state'];
      final code = callback.queryParameters['code'];
      if (state == null || state.isEmpty || code == null || code.isEmpty) throw const OAuthCallbackError('invalid_callback');
      if (state != expectedState) throw const OAuthCallbackError('invalid_state');
      return await oauthComplete(provider, state, code, cancellation: cancellation);
    } finally {
      adapter.close?.call();
    }
  }

  Future<JsonObject> oauthLink(
    String currentPassword,
    String provider,
    String redirectUri, {
    CancellationToken? cancellation,
  }) => _objectRequest(
    'POST',
    '/api/v1/auth/oauth/link',
    JsonObject({
      'currentPassword': JsonString(currentPassword),
      'provider': JsonString(provider),
      'redirectUri': JsonString(redirectUri),
    }),
    cancellation,
  );

  Future<UserSession> oauthComplete(
    String provider,
    String state,
    String code, {
    CancellationToken? cancellation,
  }) => _completeSession('/api/v1/auth/oauth/complete', {
    'provider': JsonString(provider),
    'state': JsonString(state),
    'code': JsonString(code),
  }, cancellation);

  /// Parses a browser redirect or mobile deep link then completes OAuth.
  Future<UserSession> oauthCompleteFromUri(
    String provider,
    Uri callback, {
    CancellationToken? cancellation,
  }) {
    final error = callback.queryParameters['error'];
    if (error != null) {
      throw OAuthCallbackError(error, callback.queryParameters['error_description']);
    }
    final state = callback.queryParameters['state'];
    final code = callback.queryParameters['code'];
    if (state == null || state.isEmpty || code == null || code.isEmpty) {
      throw const OAuthCallbackError('invalid_callback', 'The OAuth callback is missing state or code.');
    }
    return oauthComplete(provider, state, code, cancellation: cancellation);
  }

  Future<JsonValue> request(
    String method,
    String path, {
    JsonValue? body,
    Map<String, String>? headers,
    Map<String, String>? query,
    CancellationToken? cancellation,
    String? requestKey,
  }) async {
    final effectiveCancellation = _requestCancellation(
      '$method $path',
      requestKey,
      cancellation,
    );
    try {
      final response = await transport.json(
      method,
      path,
      headers: headersFor(headers),
      body: body,
      query: query,
      cancellation: effectiveCancellation.token,
    );
    if (response.status < 200 || response.status >= 300)
      throw pgBaseError(response);
    if (response.status == 204 || response.bytes.isEmpty)
      return const JsonNull();
      try {
      return decodeExactJson(response.bytes);
    } on PgBaseProtocolError {
        throw const PgBaseProtocolError('invalid_success_json');
      }
    } finally {
      effectiveCancellation.dispose();
    }
  }

  _RequestCancellation _requestCancellation(
    String fallbackKey,
    String? key,
    CancellationToken? external,
  ) {
    if (!_autoCancellation || key == null) {
      return _RequestCancellation(external, () {});
    }
    final requestKey = key.isEmpty ? fallbackKey : key;
    cancelRequest(requestKey);
    final token = CancellationToken();
    _requestControllers[requestKey] = token;
    if (external?.isCancelled ?? false) {
      token.cancel();
    } else {
      external?.whenCancelled.then((_) => token.cancel());
    }
    return _RequestCancellation(token, () {
      if (identical(_requestControllers[requestKey], token)) {
        _requestControllers.remove(requestKey);
      }
    });
  }

  Future<RecordResult<JsonObject>> recordRequest(
    String method,
    String path, {
    JsonValue? body,
    Map<String, String>? headers,
    Map<String, String>? query,
    CancellationToken? cancellation,
  }) async {
    final response = await transport.json(
      method,
      path,
      headers: headersFor(headers),
      body: body,
      query: query,
      cancellation: cancellation,
    );
    if (response.status < 200 || response.status >= 300)
      throw pgBaseError(response);
    final decoded = decodeExactJson(response.bytes);
    if (decoded is! JsonObject)
      throw const PgBaseProtocolError('invalid_record_response');
    return RecordResult(record: decoded, etag: response.etag);
  }

  Future<JsonObject> batchMultipart(
    JsonObject requests,
    Map<String, UploadSource> files, {
    CancellationToken? cancellation,
  }) async {
    if (files.keys.any((key) => key.isEmpty || key == 'requests'))
      throw const PgBaseProtocolError('invalid_batch_file');
    final response = await transport.multipart(
      'POST',
      '/api/v1/user/collections/batch',
      fields: {'requests': utf8.decode(encodeExactJson(requests))},
      files: files,
      headers: headersFor(null),
      cancellation: cancellation,
    );
    if (response.status < 200 || response.status >= 300)
      throw pgBaseError(response);
    final decoded = decodeExactJson(response.bytes);
    if (decoded is! JsonObject)
      throw const PgBaseProtocolError('invalid_batch_response');
    return decoded;
  }

  Map<String, String> headersFor(Map<String, String>? extra) => {
    if (auth.token != null) 'authorization': 'Bearer ${auth.token}',
    ...?extra,
  };

  Future<UserSession> _authenticate(
    String path,
    String email,
    String password, {
    CancellationToken? cancellation,
    bool mfa = false,
    String? fields,
  }) => _completeSession(
    path,
    {'email': JsonString(email), 'password': JsonString(password)},
    cancellation,
    mfa: mfa,
    fields: fields,
  );

  Future<UserSession> _completeSession(
    String path,
    Map<String, JsonValue> body,
    CancellationToken? cancellation, {
    bool mfa = false,
    String? fields,
  }) async {
    final generation = auth.generation;
    try {
      final session = _decodeSession(
        await request(
          'POST',
          path,
          body: JsonObject(body),
          query: {if (fields != null) 'fields': fields},
          cancellation: cancellation,
        ),
      );
      if (session.token != null && generation == auth.generation)
        await auth.save(session.token!, record: session.user);
      return session;
    } on PgBaseError catch (error) {
      if (mfa && error.code == 'mfa_required') {
        final token = error.details['challengeToken']?.firstOrNull;
        final expiresAt = error.details['expiresAt']?.firstOrNull;
        if (token != null && expiresAt != null)
          throw MfaChallengeError(
            error.status,
            error.code,
            MfaChallenge(token: token, expiresAt: expiresAt),
            details: error.details,
            etag: error.etag,
          );
      }
      rethrow;
    }
  }

  Future<void> _voidPost(
    String path,
    Map<String, JsonValue> body,
    CancellationToken? cancellation,
  ) async =>
      request('POST', path, body: JsonObject(body), cancellation: cancellation);
  Future<void> _clearAfter(
    String path,
    JsonObject body,
    CancellationToken? cancellation,
  ) async {
    try {
      await request('POST', path, body: body, cancellation: cancellation);
    } finally {
      await auth.clear();
    }
  }

  Future<JsonObject> _objectRequest(
    String method,
    String path,
    JsonObject body,
    CancellationToken? cancellation,
  ) async {
    final result = await request(
      method,
      path,
      body: body,
      cancellation: cancellation,
    );
    if (result is! JsonObject)
      throw const PgBaseProtocolError('invalid_object_response');
    return result;
  }
}

final class AuthRealm {
  const AuthRealm(this.client, this.name);
  final PgBaseClient client;
  final String name;
  String get _path => '/api/v1/auth/${Uri.encodeComponent(name)}';
  Future<UserSession> register(
    String email,
    String password, {
    String? fields,
    CancellationToken? cancellation,
  }) => client._authenticate(
    '$_path/register',
    email,
    password,
    cancellation: cancellation,
    fields: fields,
  );
  Future<UserSession> login(
    String email,
    String password, {
    String? fields,
    CancellationToken? cancellation,
  }) => client._authenticate(
    '$_path/login',
    email,
    password,
    cancellation: cancellation,
    fields: fields,
    mfa: true,
  );

  /// Returns the sign-in methods enabled for this auth collection.
  Future<AuthMethods> methods({CancellationToken? cancellation}) async =>
      _decodeAuthMethods(
        await client.request(
          'GET',
          '$_path/methods',
          cancellation: cancellation,
        ),
      );

  /// Rotates the current bearer token after validating its replacement session.
  Future<UserSession> refresh({CancellationToken? cancellation}) async {
    final generation = client.auth.generation;
    final session = _decodeRefreshSession(
      await client.request(
        'POST',
        '$_path/refresh',
        cancellation: cancellation,
      ),
    );
    if (generation == client.auth.generation) await client.auth.save(session.token!, record: session.user);
    return session;
  }

  Future<UserSession> session({CancellationToken? cancellation}) async =>
      _decodeSession(
        await client.request(
          'GET',
          '$_path/session',
          cancellation: cancellation,
        ),
      );
  Future<void> logout({CancellationToken? cancellation}) async {
    try {
      await client.request('POST', '$_path/logout', cancellation: cancellation);
    } finally {
      await client.auth.clear();
    }
  }

  Future<void> requestPasswordReset(
    String email, {
    CancellationToken? cancellation,
  }) async {
    await client.request(
      'POST',
      '$_path/request-password-reset',
      body: JsonObject({'email': JsonString(email)}),
      cancellation: cancellation,
    );
  }

  Future<JsonObject> requestOtp(
    String email, {
    CancellationToken? cancellation,
  }) async {
    final value = await client.request(
      'POST',
      '$_path/otp/request',
      body: JsonObject({'email': JsonString(email)}),
      cancellation: cancellation,
    );
    if (value is! JsonObject)
      throw const PgBaseProtocolError('invalid_otp_response');
    return value;
  }

  Future<UserSession> completeOtp(
    String otpId,
    String code, {
    CancellationToken? cancellation,
  }) => client._completeSession(
    '$_path/otp/complete',
    {'otpId': JsonString(otpId), 'code': JsonString(code)},
    cancellation,
    mfa: true,
  );

  Future<JsonObject> oauthStart(
    String provider,
    String redirectUri, {
    CancellationToken? cancellation,
  }) => client._objectRequest(
    'POST',
    '$_path/oauth/start',
    JsonObject({
      'provider': JsonString(provider),
      'redirectUri': JsonString(redirectUri),
    }),
    cancellation,
  );

  Future<JsonObject> oauthStartAndOpen(
    String provider,
    String redirectUri,
    OAuthUrlLauncher open, {
    CancellationToken? cancellation,
  }) => client.oauthStartAndOpen(
    provider,
    redirectUri,
    open,
    cancellation: cancellation,
  );

  Future<UserSession> oauthWithBrowser(
    String provider,
    String redirectUri,
    OAuthBrowserAdapter adapter, {
    CancellationToken? cancellation,
  }) => client.oauthWithBrowser(
    provider,
    redirectUri,
    adapter,
    cancellation: cancellation,
  );

  Future<UserSession> oauthCompleteFromUri(
    String provider,
    Uri callback, {
    CancellationToken? cancellation,
  }) => client.oauthCompleteFromUri(provider, callback, cancellation: cancellation);

  Future<JsonObject> profile({String? fields, CancellationToken? cancellation}) async {
    final value = await client.request(
      'GET',
      '$_path/profile',
      query: {if (fields != null) 'fields': fields},
      cancellation: cancellation,
    );
    return _profileAttributes(value);
  }

  Future<JsonObject> updateProfile(
    JsonObject value, {
    String? fields,
    CancellationToken? cancellation,
  }) async {
    final response = await client.request(
      'PATCH',
      '$_path/profile',
      body: value,
      query: {if (fields != null) 'fields': fields},
      cancellation: cancellation,
    );
    return _profileAttributes(response);
  }
}

final class _RequestCancellation {
  const _RequestCancellation(this.token, this.dispose);
  final CancellationToken? token;
  final void Function() dispose;
}

UserSession _decodeSession(JsonValue value) {
  if (value is! JsonObject || value['user'] is! JsonObject)
    throw const PgBaseProtocolError('invalid_session_response');
  final user = value['user']! as JsonObject;
  String string(String key, JsonObject from) {
    final field = from[key];
    if (field is! JsonString)
      throw const PgBaseProtocolError('invalid_session_response');
    return field.value;
  }

  final verified = user['verified'];
  final impersonated = value['impersonated'];
  return UserSession(
    token: (value['token'] as JsonString?)?.value,
    user: User(
      id: string('id', user),
      email: string('email', user),
      createdAt: string('createdAt', user),
      verified: verified is JsonBool ? verified.value : null,
      authCollection: (user['authCollection'] as JsonString?)?.value,
      authCollectionId: (user['authCollectionId'] as JsonString?)?.value,
    ),
    expiresAt: string('expiresAt', value),
    impersonated: impersonated is JsonBool && impersonated.value,
    impersonatingAdministratorId:
        (value['impersonatingAdministratorId'] as JsonString?)?.value,
  );
}

AuthMethods _decodeAuthMethods(JsonValue value) {
  if (value is! JsonObject ||
      value['passwordEnabled'] is! JsonBool ||
      value['otpEnabled'] is! JsonBool ||
      value['mfaEnabled'] is! JsonBool ||
      value['oauthProviders'] is! JsonArray) {
    throw const PgBaseProtocolError('invalid_auth_methods_response');
  }
  final providers = (value['oauthProviders']! as JsonArray).values;
  if (!providers.every((provider) => provider is JsonString)) {
    throw const PgBaseProtocolError('invalid_auth_methods_response');
  }
  var oauth2Enabled = false;
  var oauth2Providers = <OAuthMethodProvider>[];
  final oauth2 = value['oauth2'];
  if (oauth2 != null) {
    if (oauth2 is! JsonObject ||
        oauth2['enabled'] is! JsonBool ||
        oauth2['providers'] is! JsonArray) {
      throw const PgBaseProtocolError('invalid_auth_methods_response');
    }
    oauth2Enabled = (oauth2['enabled']! as JsonBool).value;
    for (final provider in (oauth2['providers']! as JsonArray).values) {
      if (provider is! JsonObject ||
          provider['name'] is! JsonString ||
          provider['startEndpoint'] is! JsonString) {
        throw const PgBaseProtocolError('invalid_auth_methods_response');
      }
      oauth2Providers.add(
        OAuthMethodProvider(
          name: (provider['name']! as JsonString).value,
          startEndpoint: (provider['startEndpoint']! as JsonString).value,
        ),
      );
    }
  }
  return AuthMethods(
    passwordEnabled: (value['passwordEnabled']! as JsonBool).value,
    otpEnabled: (value['otpEnabled']! as JsonBool).value,
    mfaEnabled: (value['mfaEnabled']! as JsonBool).value,
    oauthProviders: providers
        .cast<JsonString>()
        .map((item) => item.value)
        .toList(growable: false),
    oauth2Enabled: oauth2Enabled,
    oauth2Providers: List.unmodifiable(oauth2Providers),
  );
}

/// Refresh responses must include a usable replacement token before storage.
UserSession _decodeRefreshSession(JsonValue value) {
  final session = _decodeSession(value);
  if (session.token == null || session.token!.isEmpty) {
    throw const PgBaseProtocolError('invalid_session_response');
  }
  return session;
}

PgBaseError pgBaseError(PgBaseResponse response) {
  try {
    final body = decodeExactJson(response.bytes);
    final envelope = body is JsonObject ? body['error'] : null;
    if (envelope is JsonObject) {
      final code = (envelope['code'] as JsonString?)?.value ?? 'request_failed';
      final requestId = (envelope['requestId'] as JsonString?)?.value;
      final detailsValue = envelope['details'];
      final details = <String, List<String>>{};
      if (detailsValue is JsonObject) {
        for (final entry in detailsValue.values.entries) {
          final list = entry.value;
          if (list is JsonArray &&
              list.values.every((item) => item is JsonString))
            details[entry.key] = list.values
                .cast<JsonString>()
                .map((item) => item.value)
                .toList(growable: false);
        }
      }
      return PgBaseError(
        response.status,
        code,
        details: details,
        etag: response.etag,
        requestId: requestId,
      );
    }
  } on PgBaseProtocolError {
    // Fall through to a stable transport error.
  }
  return PgBaseError(response.status, 'request_failed', etag: response.etag);
}

JsonObject _profileAttributes(JsonValue value) {
  if (value is! JsonObject || value['attributes'] is! JsonObject)
    throw const PgBaseProtocolError('invalid_profile_response');
  return value['attributes']! as JsonObject;
}
