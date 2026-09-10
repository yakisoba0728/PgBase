import 'dart:convert';

import 'errors.dart';

sealed class JsonValue {
  const JsonValue();

  Object? toDart();
}

final class JsonNull extends JsonValue {
  const JsonNull();
  @override
  Object? toDart() => null;
}

final class JsonBool extends JsonValue {
  const JsonBool(this.value);
  final bool value;
  @override
  Object toDart() => value;
}

final class JsonString extends JsonValue {
  const JsonString(this.value);
  final String value;
  @override
  Object toDart() => value;
}

/// Raw JSON numeric token. Keep this for JSONB rather than coercing to double.
final class JsonNumber extends JsonValue {
  JsonNumber(this.source) {
    if (!RegExp(r'^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$')
        .hasMatch(source)) {
      throw const PgBaseProtocolError('invalid_json_number');
    }
  }

  final String source;
  @override
  Object toDart() => source;
}

/// A PostgreSQL numeric representation. It is intentionally text, never a double.
final class ExactDecimal {
  ExactDecimal(String value) : value = _parse(value);
  final String value;
  static String _parse(String source) {
    if (!RegExp(r'^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$').hasMatch(source)) {
      throw const PgBaseProtocolError('invalid_exact_decimal');
    }
    return source;
  }

  @override
  String toString() => value;
}

final class JsonArray extends JsonValue {
  const JsonArray(this.values);
  final List<JsonValue> values;
  @override
  Object toDart() =>
      values.map((value) => value.toDart()).toList(growable: false);
}

final class JsonObject extends JsonValue {
  JsonObject(Map<String, JsonValue> values) : values = Map.unmodifiable(values);
  final Map<String, JsonValue> values;
  JsonValue? operator [](String key) => values[key];
  @override
  Object toDart() => values.map((key, value) => MapEntry(key, value.toDart()));
}

JsonValue decodeExactJson(List<int> bytes) =>
    _ExactJsonParser(utf8.decode(bytes, allowMalformed: false)).parse();

List<int> encodeExactJson(JsonValue value) => utf8.encode(_encode(value));

String _encode(JsonValue value) => switch (value) {
  JsonNull() => 'null',
  JsonBool(:final value) => value ? 'true' : 'false',
  JsonString(:final value) => jsonEncode(value),
  JsonNumber(:final source) => source,
  JsonArray(:final values) => '[${values.map(_encode).join(',')}]',
  JsonObject(:final values) =>
    '{${values.entries.map((entry) => '${jsonEncode(entry.key)}:${_encode(entry.value)}').join(',')}}',
};

JsonValue jsonValue(Object? value) {
  if (value == null) return const JsonNull();
  if (value is JsonValue) return value;
  if (value is bool) return JsonBool(value);
  if (value is String) return JsonString(value);
  if (value is int || value is double) return JsonNumber('$value');
  if (value is BigInt) return JsonString('$value');
  if (value is ExactDecimal) return JsonString(value.value);
  if (value is List<Object?>)
    return JsonArray(value.map(jsonValue).toList(growable: false));
  if (value is Map<String, Object?>)
    return JsonObject(value.map((key, item) => MapEntry(key, jsonValue(item))));
  throw PgBaseProtocolError('unsupported_json_value_${value.runtimeType}');
}

final class _ExactJsonParser {
  _ExactJsonParser(this.source);
  final String source;
  int _position = 0;

  JsonValue parse() {
    _whitespace();
    final value = _value();
    _whitespace();
    if (_position != source.length) _invalid();
    return value;
  }

  JsonValue _value() {
    if (_position == source.length) _invalid();
    return switch (source[_position]) {
      'n' => _literal('null', const JsonNull()),
      't' => _literal('true', const JsonBool(true)),
      'f' => _literal('false', const JsonBool(false)),
      '"' => JsonString(_string()),
      '[' => _array(),
      '{' => _object(),
      '-' => _number(),
      _ when _digit(source[_position]) => _number(),
      _ => _invalid(),
    };
  }

  JsonValue _literal(String expected, JsonValue value) {
    if (!source.startsWith(expected, _position)) _invalid();
    _position += expected.length;
    return value;
  }

  JsonArray _array() {
    _position++;
    _whitespace();
    final values = <JsonValue>[];
    if (_take(']')) return JsonArray(values);
    while (true) {
      _whitespace();
      values.add(_value());
      _whitespace();
      if (_take(']')) return JsonArray(values);
      _expect(',');
    }
  }

  JsonObject _object() {
    _position++;
    _whitespace();
    final values = <String, JsonValue>{};
    if (_take('}')) return JsonObject(values);
    while (true) {
      _whitespace();
      if (_position == source.length || source[_position] != '"') _invalid();
      final key = _string();
      if (values.containsKey(key))
        throw const PgBaseProtocolError('duplicate_json_key');
      _whitespace();
      _expect(':');
      _whitespace();
      values[key] = _value();
      _whitespace();
      if (_take('}')) return JsonObject(values);
      _expect(',');
    }
  }

  JsonNumber _number() {
    final start = _position;
    if (_take('-') && _position == source.length) _invalid();
    if (_take('0')) {
      if (_position < source.length && _digit(source[_position])) _invalid();
    } else {
      _digits();
    }
    if (_take('.')) _digits();
    if (_position < source.length &&
        (source[_position] == 'e' || source[_position] == 'E')) {
      _position++;
      if (_position < source.length &&
          (source[_position] == '+' || source[_position] == '-'))
        _position++;
      _digits();
    }
    return JsonNumber(source.substring(start, _position));
  }

  String _string() {
    final start = _position;
    _expect('"');
    var escaped = false;
    while (_position < source.length) {
      final current = source[_position++];
      if (current == '"' && !escaped)
        return jsonDecode(source.substring(start, _position)) as String;
      if (current.codeUnitAt(0) < 0x20 && !escaped) _invalid();
      if (current == '\\' && !escaped) {
        escaped = true;
      } else {
        escaped = false;
      }
    }
    _invalid();
  }

  void _digits() {
    final start = _position;
    while (_position < source.length && _digit(source[_position])) _position++;
    if (start == _position) _invalid();
  }

  bool _take(String expected) {
    if (_position < source.length && source[_position] == expected) {
      _position++;
      return true;
    }
    return false;
  }

  void _expect(String expected) {
    if (!_take(expected)) _invalid();
  }

  void _whitespace() {
    while (_position < source.length && ' \n\r\t'.contains(source[_position]))
      _position++;
  }

  bool _digit(String value) =>
      value.codeUnitAt(0) >= 48 && value.codeUnitAt(0) <= 57;

  Never _invalid() => throw const PgBaseProtocolError('invalid_json');
}
