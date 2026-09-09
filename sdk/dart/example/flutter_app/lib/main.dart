import 'package:flutter/material.dart';
import 'package:pgbase/pgbase.dart';

void main() => runApp(const PgBaseExampleApp());

class PgBaseExampleApp extends StatelessWidget {
  const PgBaseExampleApp({super.key});

  @override
  Widget build(BuildContext context) {
    // The real app chooses a secure AuthStorage implementation at composition.
    final client = PgBaseClient(Uri.parse('http://127.0.0.1:8091/'));
    final amount = ExactDecimal('900719925474099312345.0001');
    return MaterialApp(
      home: Scaffold(
        appBar: AppBar(title: const Text('PgBase Flutter example')),
        body: Padding(
          padding: const EdgeInsets.all(24),
          child: Column(
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              const Text('Connect this app to PgBase with an in-memory AuthStore by default.'),
              Text('Exact numeric display: $amount'),
              FilledButton(
                onPressed: client.close,
                child: const Text('Close client'),
              ),
            ],
          ),
        ),
      ),
    );
  }
}
