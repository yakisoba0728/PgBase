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

part 'auth_protocol.dart';

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

  /// Cancel older reads with the same URL or requests with an explicit key.
  /// Enabled by default; mutations require an explicit key to participate.
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

  /// Constructs a PocketBase filter expression with safe placeholder values.
  ///
  /// Placeholders use `{:name}`. Strings, dates, lists, and maps are quoted
  /// filter literals, while bool, num, and null remain literal values. A
  /// placeholder absent from [params] is left unchanged, matching PocketBase's
  /// SDK and allowing the server to reject an incomplete expression.
  String filter(String expression, [Map<String, dynamic> params = const {}]) {
    if (params.isEmpty) return expression;

    String stringify(dynamic value) {
      if (value == null || value is num || value is bool) {
        return value.toString();
      }
      if (value is DateTime) {
        return jsonEncode(
          value.toUtc().toIso8601String().replaceFirst('T', ' '),
        );
      }
      if (value is String) return jsonEncode(value);

      final serialized = jsonEncode(value);
      if (serialized.startsWith('[') || serialized.startsWith('{')) {
        return jsonEncode(serialized);
      }
      return serialized;
    }

    return expression.replaceAllMapped(RegExp(r'\{:([\w.-]+)\}'), (match) {
      final key = match.group(1)!;
      return params.containsKey(key) ? stringify(params[key]) : match.group(0)!;
    });
  }

  /// Submits the compact operation form accepted by PgBase's batch endpoint.
  Future<JsonObject> batch(
    List<BatchOperation> operations, {
    String? fields,
    CancellationToken? cancellation,
  }) =>
      submitBatch(this, operations, fields: fields, cancellation: cancellation);

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
    JsonObject? profile,
    CancellationToken? cancellation,
  }) => _authenticate(
    '/api/v1/auth/register',
    email,
    password,
    cancellation: cancellation,
    fields: fields,
    profile: profile,
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
  Future<UserSession> session({
    String? fields,
    CancellationToken? cancellation,
  }) async => _decodeSession(
    await request(
      'GET',
      '/api/v1/auth/session',
      query: {if (fields != null) 'fields': fields},
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
    String? fields,
    CancellationToken? cancellation,
  }) async {
    final value = await request(
      'POST',
      '/api/v1/auth/otp/request',
      body: JsonObject({'email': JsonString(email)}),
      query: {if (fields != null) 'fields': fields},
      cancellation: cancellation,
    );
    if (value is! JsonObject)
      throw const PgBaseProtocolError('invalid_otp_response');
    return value;
  }

  Future<UserSession> completeOtp(
    String otpId,
    String code, {
    String? fields,
    CancellationToken? cancellation,
  }) => _completeSession(
    '/api/v1/auth/otp/complete',
    {'otpId': JsonString(otpId), 'code': JsonString(code)},
    cancellation,
    mfa: true,
    fields: fields,
  );
  Future<UserSession> completeMfa(
    String challengeToken,
    String code, {
    String? fields,
    CancellationToken? cancellation,
  }) => _completeSession(
    '/api/v1/auth/mfa/complete',
    {'challengeToken': JsonString(challengeToken), 'code': JsonString(code)},
    cancellation,
    fields: fields,
  );
  Future<JsonObject> beginTotpEnrollment(
    String currentPassword, {
    CancellationToken? cancellation,
  }) => _objectRequest(
    'POST',
    '/api/v1/auth/totp/enroll',
    JsonObject({'currentPassword': JsonString(currentPassword)}),
    cancellation,
  );

  /// Confirms enrollment and returns the server-authored recovery codes.
  ///
  /// [fields] predates the native confirmation contract and is ignored. It is
  /// retained temporarily for source compatibility; confirmation does not
  /// support response projection.
  Future<JsonObject> confirmTotpEnrollment(
    String code, {
    @Deprecated('TOTP confirmation does not support response projection.')
    String? fields,
    CancellationToken? cancellation,
  }) => _objectRequest(
    'POST',
    '/api/v1/auth/totp/confirm',
    JsonObject({'code': JsonString(code)}),
    cancellation,
  );
  Future<void> disableTOTP(
    String currentPassword,
    String code, {
    CancellationToken? cancellation,
  }) async => request(
    'POST',
    '/api/v1/auth/totp/disable',
    body: JsonObject({
      'currentPassword': JsonString(currentPassword),
      'code': JsonString(code),
    }),
    cancellation: cancellation,
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
  }) => _oauthStartAndOpen(
    () => oauthStart(provider, redirectUri, cancellation: cancellation),
    open,
  );

  /// Runs OAuth start, launch, callback validation, and completion in one
  /// cancellable operation. The adapter owns platform popup/deep-link I/O.
  Future<UserSession> oauthWithBrowser(
    String provider,
    String redirectUri,
    OAuthBrowserAdapter adapter, {
    CancellationToken? cancellation,
    JsonObject? profile,
    String? fields,
  }) => _oauthWithBrowser(
    () => oauthStart(provider, redirectUri, cancellation: cancellation),
    redirectUri,
    adapter,
    cancellation,
    (state, code) => oauthComplete(
      provider,
      state,
      code,
      cancellation: cancellation,
      profile: profile,
      fields: fields,
    ),
  );

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
    String? fields,
    JsonObject? profile,
    CancellationToken? cancellation,
  }) => _completeSession(
    '/api/v1/auth/oauth/complete',
    {
      'provider': JsonString(provider),
      'state': JsonString(state),
      'code': JsonString(code),
      if (profile != null) 'fields': profile,
    },
    cancellation,
    fields: fields,
  );

  /// Parses a browser redirect or mobile deep link then completes OAuth.
  Future<UserSession> oauthCompleteFromUri(
    String provider,
    Uri callback, {
    String? fields,
    JsonObject? profile,
    CancellationToken? cancellation,
  }) {
    final error = callback.queryParameters['error'];
    if (error != null) {
      throw OAuthCallbackError(
        error,
        callback.queryParameters['error_description'],
      );
    }
    final state = callback.queryParameters['state'];
    final code = callback.queryParameters['code'];
    if (state == null || state.isEmpty || code == null || code.isEmpty) {
      throw const OAuthCallbackError(
        'invalid_callback',
        'The OAuth callback is missing state or code.',
      );
    }
    return oauthComplete(
      provider,
      state,
      code,
      fields: fields,
      profile: profile,
      cancellation: cancellation,
    );
  }

  Future<JsonValue> request(
    String method,
    String path, {
    JsonValue? body,
    Map<String, String>? headers,
    Map<String, String>? query,
    CancellationToken? cancellation,
    String? requestKey,
    bool autoCancel = true,
  }) async {
    final effectiveCancellation = _requestCancellation(
      method,
      path,
      query,
      requestKey,
      cancellation,
      autoCancel,
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
    String method,
    String path,
    Map<String, String>? query,
    String? key,
    CancellationToken? external,
    bool autoCancel,
  ) {
    final read =
        method.toUpperCase() == 'GET' || method.toUpperCase() == 'HEAD';
    if (!_autoCancellation || !autoCancel || (key == null && !read)) {
      return _RequestCancellation(external, () {});
    }
    final requestKey = key == null || key.isEmpty
        ? '${method.toUpperCase()} ${transport.uri(path, query)}'
        : key;
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
    String? requestKey,
    bool autoCancel = true,
  }) async {
    final effective = _requestCancellation(
      method,
      path,
      query,
      requestKey,
      cancellation,
      autoCancel,
    );
    try {
      final response = await transport.json(
        method,
        path,
        headers: headersFor(headers),
        body: body,
        query: query,
        cancellation: effective.token,
      );
      if (response.status < 200 || response.status >= 300)
        throw pgBaseError(response);
      final decoded = decodeExactJson(response.bytes);
      if (decoded is! JsonObject)
        throw const PgBaseProtocolError('invalid_record_response');
      return RecordResult(record: decoded, etag: response.etag);
    } finally {
      effective.dispose();
    }
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
    JsonObject? profile,
  }) => _completeSession(
    path,
    {
      'email': JsonString(email),
      'password': JsonString(password),
      if (profile != null) 'fields': profile,
    },
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
