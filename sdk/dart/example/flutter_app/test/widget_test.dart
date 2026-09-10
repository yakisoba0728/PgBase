import 'package:flutter_test/flutter_test.dart';
import 'package:pgbase_flutter_example/main.dart';

void main() {
  testWidgets('shows the exact numeric SDK example', (tester) async {
    await tester.pumpWidget(const PgBaseExampleApp());
    expect(find.textContaining('900719925474099312345.0001'), findsOneWidget);
  });
}
