part of 'client.dart';

final class AuthRealm {
  const AuthRealm(this.client, this.name);
  final PgBaseClient client;
  final String name;
  String get _path => '/api/v1/auth/${Uri.encodeComponent(name)}';
  Future<UserSession> register(
    String email,
    String password, {
    String? fields,
    JsonObject? profile,
    CancellationToken? cancellation,
  }) => client._authenticate(
    '$_path/register',
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
  Future<UserSession> refresh({
    String? fields,
    CancellationToken? cancellation,
  }) async {
    final generation = client.auth.generation;
    final session = _decodeRefreshSession(
      await client.request(
        'POST',
        '$_path/refresh',
        query: {if (fields != null) 'fields': fields},
        cancellation: cancellation,
      ),
    );
    if (generation == client.auth.generation)
      await client.auth.save(session.token!, record: session.user);
    return session;
  }

  Future<UserSession> session({
    String? fields,
    CancellationToken? cancellation,
  }) async => _decodeSession(
    await client.request(
      'GET',
      '$_path/session',
      query: {if (fields != null) 'fields': fields},
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
    String? fields,
    CancellationToken? cancellation,
  }) async {
    final value = await client.request(
      'POST',
      '$_path/otp/request',
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
  }) => client._completeSession(
    '$_path/otp/complete',
    {'otpId': JsonString(otpId), 'code': JsonString(code)},
    cancellation,
    mfa: true,
    fields: fields,
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
  }) => _oauthStartAndOpen(
    () => oauthStart(provider, redirectUri, cancellation: cancellation),
    open,
  );

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
    (state, code) => client.oauthComplete(
      provider,
      state,
      code,
      cancellation: cancellation,
      profile: profile,
      fields: fields,
    ),
  );

  Future<UserSession> oauthCompleteFromUri(
    String provider,
    Uri callback, {
    String? fields,
    JsonObject? profile,
    CancellationToken? cancellation,
  }) => client.oauthCompleteFromUri(
    provider,
    callback,
    fields: fields,
    profile: profile,
    cancellation: cancellation,
  );

  Future<JsonObject> profile({
    String? fields,
    CancellationToken? cancellation,
  }) async {
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
      emailVisibility: (user['emailVisibility'] as JsonBool?)?.value,
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

Future<JsonObject> _oauthStartAndOpen(
  Future<JsonObject> Function() start,
  OAuthUrlLauncher open,
) async {
  final started = await start();
  final authorizationUrl = started['authorizationUrl'];
  if (authorizationUrl is! JsonString) {
    throw const PgBaseProtocolError('invalid_oauth_start_response');
  }
  await open(Uri.parse(authorizationUrl.value));
  return started;
}

Future<UserSession> _oauthWithBrowser(
  Future<JsonObject> Function() start,
  String redirectUri,
  OAuthBrowserAdapter adapter,
  CancellationToken? cancellation,
  Future<UserSession> Function(String state, String code) complete,
) async {
  try {
    final started = await start();
    final authorizationUrl = started['authorizationUrl'];
    if (authorizationUrl is! JsonString) {
      throw const OAuthCallbackError('invalid_start');
    }
    final expectedState = Uri.parse(authorizationUrl.value)
        .queryParameters['state'];
    if (expectedState == null || expectedState.isEmpty) {
      throw const OAuthCallbackError('invalid_start');
    }
    await adapter.open(Uri.parse(authorizationUrl.value));
    final callback = await adapter.waitForCallback(cancellation);
    final redirect = Uri.parse(redirectUri);
    if (callback.scheme != redirect.scheme ||
        callback.authority != redirect.authority) {
      throw const OAuthCallbackError('invalid_callback_origin');
    }
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
      throw const OAuthCallbackError('invalid_callback');
    }
    if (state != expectedState) throw const OAuthCallbackError('invalid_state');
    return await complete(state, code);
  } finally {
    adapter.close?.call();
  }
}

PgBaseError pgBaseError(PgBaseResponse response) {
  try {
    final body = decodeExactJson(response.bytes);
    final envelope = body is JsonObject ? body['error'] : null;
    if (envelope is JsonObject) {
      final codeValue = envelope['code'];
      final code = codeValue is JsonString ? codeValue.value : 'request_failed';
      final requestIdValue = envelope['requestId'];
      final requestId = requestIdValue is JsonString
          ? requestIdValue.value
          : null;
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
