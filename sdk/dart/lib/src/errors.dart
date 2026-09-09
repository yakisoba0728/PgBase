final class PgBaseError implements Exception {
  const PgBaseError(
    this.status,
    this.code, {
    this.details = const {},
    this.etag,
    this.requestId,
  });

  final int status;
  final String code;
  final Map<String, List<String>> details;
  final String? etag;
  final String? requestId;

  @override
  String toString() => 'PgBaseError($status, $code)';
}

final class PgBaseCancellation implements Exception {
  const PgBaseCancellation();

  @override
  String toString() => 'PgBaseCancellation()';
}

/// A stable SDK error for a transport failure that was not caller cancellation.
final class PgBaseNetworkError implements Exception {
  const PgBaseNetworkError(this.cause);

  final Object cause;

  @override
  String toString() => 'PgBaseNetworkError($cause)';
}

final class PgBaseProtocolError implements Exception {
  const PgBaseProtocolError(this.code);

  final String code;

  @override
  String toString() => 'PgBaseProtocolError($code)';
}

final class MfaChallengeError extends PgBaseError {
  const MfaChallengeError(
    super.status,
    super.code,
    this.challenge, {
    super.details,
    super.etag,
  });

  final MfaChallenge challenge;
}

final class MfaChallenge {
  const MfaChallenge({required this.token, required this.expiresAt});

  final String token;
  final String expiresAt;
}
