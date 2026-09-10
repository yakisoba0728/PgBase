import 'errors.dart';
import 'exact_json.dart';

typedef RecordData = Map<String, JsonValue>;

final class PgBaseUuid {
  PgBaseUuid(String value) : value = _parse(value);

  final String value;

  static String _parse(String source) {
    final normalized = source.toLowerCase();
    // PostgreSQL UUID accepts every canonical 128-bit value.  In particular
    // the nil and max sentinels have no RFC version/variant bits.
    if (!RegExp(
      r'^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$',
    ).hasMatch(normalized)) {
      throw const PgBaseProtocolError('invalid_uuid');
    }
    return normalized;
  }

  @override
  String toString() => value;
}

final class RecordPage<T> {
  const RecordPage({required this.items, this.nextCursor});

  final List<T> items;
  final String? nextCursor;
}

final class RecordResult<T> {
  const RecordResult({required this.record, this.etag});

  final T record;
  final String? etag;
}

final class User {
  const User({
    required this.id,
    required this.email,
    required this.createdAt,
    this.verified,
    this.authCollection,
    this.authCollectionId,
  });

  final String id;
  final String email;
  final String createdAt;
  final bool? verified;
  final String? authCollection;
  final String? authCollectionId;
}

final class UserSession {
  const UserSession({
    this.token,
    required this.user,
    required this.expiresAt,
    this.impersonated = false,
    this.impersonatingAdministratorId,
  });

  final String? token;
  final User user;
  final String expiresAt;
  final bool impersonated;
  final String? impersonatingAdministratorId;
}

/// Enabled sign-in methods for one explicit public auth collection.
final class AuthMethods {
  const AuthMethods({
    required this.passwordEnabled,
    required this.otpEnabled,
    required this.mfaEnabled,
    required this.oauthProviders,
  });

  final bool passwordEnabled;
  final bool otpEnabled;
  final bool mfaEnabled;
  final List<String> oauthProviders;
}

final class RecordEvent {
  const RecordEvent({
    required this.eventId,
    required this.deliveryId,
    required this.collection,
    required this.recordId,
    required this.operation,
    required this.version,
  });

  final String eventId;
  final String deliveryId;
  final String collection;
  final String recordId;
  final String operation;
  final int version;
}

final class Query {
  const Query({
    this.filter,
    this.sort,
    this.expand,
    this.fields,
    this.cursor,
    this.limit,
  });

  final String? filter;
  final String? sort;
  final String? expand;
  /// Comma-separated record fields to include in each response item.
  final String? fields;
  final String? cursor;
  final int? limit;

  Map<String, String> toParameters() => {
    if (filter != null) 'filter': filter!,
    if (sort != null) 'sort': sort!,
    if (expand != null) 'expand': expand!,
    if (fields != null) 'fields': fields!,
    if (cursor != null) 'cursor': cursor!,
    if (limit != null) 'limit': '$limit',
  };
}
