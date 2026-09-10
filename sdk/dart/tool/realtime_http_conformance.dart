import 'dart:async';
import 'dart:io';
import 'package:pgbase/pgbase.dart';

Future<void> main() async {
  final client = PgBaseClient(Uri.parse(_required('PGBASE_RT_URL')));
  await client.auth.save(_required('PGBASE_RT_TOKEN'));
  final collection = _required('PGBASE_RT_COLLECTION');
  final suffix = DateTime.now().microsecondsSinceEpoch.toString();
  final topic = "$collection?filter=title%3D'wanted-$suffix'&fields=id%2Ctitle";
  final received = <RealtimeEvent>[];
  final stopOne = await client.realtime.subscribe(topic, received.add);
  final stopAll = await client.realtime.subscribe('$collection/*', received.add);
  await client.records(collection).create(JsonObject({'title': JsonString('wanted-$suffix'), 'ignored': const JsonString('not requested')}), 'rt-conformance-$suffix'.substring(0, 32));
  for (var i = 0; i < 100 && received.length < 2; i++) await Future<void>.delayed(const Duration(milliseconds: 25));
  if (received.length != 2 || received.first.record['ignored'] != null) throw StateError('realtime multiplex/filter/fields conformance failed');
  await stopOne(); await stopAll(); client.close();
  stdout.writeln('realtime HTTP conformance passed');
}
String _required(String name) => Platform.environment[name] ?? (throw StateError('$name is required'));
