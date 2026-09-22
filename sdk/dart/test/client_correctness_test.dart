import 'dart:async';
import 'dart:convert';

import 'package:http/http.dart' as http;
import 'package:pgbase/pgbase.dart';
import 'package:test/test.dart';

void main() {
  test(
    'record reads cancel duplicates through body consumption, including query',
    () async {
      final transport = ControlledClient();
      final client = PgBaseClient(
        Uri.parse('http://pgbase.test'),
        httpClient: transport,
      );
      final first = client
          .records('notes')
          .list(const Query(filter: 'score > 1'));
      final failed = expectLater(first, throwsA(isA<PgBaseCancellation>()));
      final other = client
          .records('notes')
          .list(const Query(filter: 'score > 2'));
      final latest = client
          .records('notes')
          .list(const Query(filter: 'score > 1'));
      await Future<void>.delayed(Duration.zero);
      expect(transport.calls[0].aborted, isTrue);
      expect(transport.calls[1].aborted, isFalse);
      transport.calls[1].complete();
      transport.calls[2].complete();
      await Future.wait([failed, other, latest]);
    },
  );

  test('record reads support an explicit key and individual opt-out', () async {
    final transport = ControlledClient();
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test'),
      httpClient: transport,
    );
    final keyed = client.records('notes').get('one', requestKey: 'editor');
    final failed = expectLater(keyed, throwsA(isA<PgBaseCancellation>()));
    client.cancelRequest('editor');
    final first = client.records('notes').list(const Query(autoCancel: false));
    final second = client.records('notes').list(const Query(autoCancel: false));
    await Future<void>.delayed(Duration.zero);
    expect(transport.calls[1].aborted, isFalse);
    transport.calls[1].complete();
    transport.calls[2].complete();
    expect(
      transport.calls.any(
        (call) => call.request.url.query.contains('requestKey'),
      ),
      isFalse,
    );
    await Future.wait([failed, first, second]);
  });

  test('writes and globally disabled reads are independent', () async {
    final transport = ControlledClient();
    final client = PgBaseClient(
      Uri.parse('http://pgbase.test'),
      httpClient: transport,
    );
    final one = client
        .records('notes')
        .create(JsonObject({}), 'create-note-0001');
    final two = client
        .records('notes')
        .create(JsonObject({}), 'create-note-0002');
    client.autoCancellation(false);
    final three = client.records('notes').get('one');
    final four = client.records('notes').get('one');
    await Future<void>.delayed(Duration.zero);
    expect(transport.calls.every((call) => !call.aborted), isTrue);
    for (final call in transport.calls) {
      call.complete();
    }
    await Future.wait([one, two, three, four]);
  });

  test('response body network and abort failures remain errors', () async {
    for (final failure in [
      http.ClientException('lost'),
      http.RequestAbortedException(),
    ]) {
      final transport = ControlledClient();
      final client = PgBaseClient(
        Uri.parse('http://pgbase.test'),
        httpClient: transport,
      );
      final result = client.records('notes').get('one');
      final failed = expectLater(
        result,
        throwsA(
          failure is http.RequestAbortedException
              ? isA<PgBaseCancellation>()
              : isA<PgBaseNetworkError>(),
        ),
      );
      transport.calls.single.fail(failure);
      await failed;
    }
  });
}

final class ControlledClient extends http.BaseClient {
  final calls = <PendingRequest>[];
  @override
  Future<http.StreamedResponse> send(http.BaseRequest request) async {
    final call = PendingRequest(request);
    calls.add(call);
    if (request is http.AbortableRequest) {
      request.abortTrigger?.then((_) {
        call.aborted = true;
        call.fail(http.RequestAbortedException());
      });
    }
    return http.StreamedResponse(call.body.stream, 200);
  }
}

final class PendingRequest {
  PendingRequest(this.request);
  final http.BaseRequest request;
  final body = StreamController<List<int>>();
  bool aborted = false;
  void complete() {
    body.add(utf8.encode('{"id":"one","items":[]}'));
    body.close();
  }

  void fail(Object error) {
    if (!body.isClosed) {
      body.addError(error);
      body.close();
    }
  }
}
