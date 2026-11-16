# Final bounded performance protocol

Registered before focused execution on starting source
`23b6c4090e453587325d9dfad8fec6d854f3bee2`, 2026-11-16.

## Three hypotheses, no expanded campaign

1. The schema catalog listener reserves one of the four pooled connections for
   its lifetime, leaving three available request/background slots. This is
   historically documented behavior, not an alleged new leak. The latest task
   explicitly permits separating the listener from a four-connection request
   pool, with the extra backend disclosed. A deterministic four-acquisition
   test must establish the capacity distinction; reconnect, cancellation,
   backend disappearance and exclusive backup-gate recovery must still pass.
2. WAL/COMMIT stalls can exhaust request workers and the bounded admission queue.
   The historical 547-drop interval overlaps pool/WAL pressure. This is
   contributor-level evidence, not proof of a unique disk or runtime cause.
3. Generator/scheduler delay can cause admission backlog. The historical small
   producer delays and GC pauses do not support them as the sole cause. Current
   raw generator/queue/service and runtime observations remain separate.

The historical evidence is preserved in the Phase A artifact for run
43820007603, artifact 14204951142, ZIP SHA-256
`d2411a4837ffd38a5fd3c2b67423240f07de4eed9ed0f5d4038af59ca628cf6e`.
Historical source: `a1a652289f0bc817f18db257282c758e66b4a672`; tested merge
`f5295856078d06a0e68a00365e0223a48b1a206e`; tree
`7c1fc6ed40fc8d908156702d8a3fe4a13c8930d1`. It completed 119,453 of
120,000 scheduled arrivals, dropped 547 and failed despite whole-run
end-to-end p99 209.489023 ms. No old result is promoted to a current pass.

## Focused change and verification boundary

The proposed minimal ownership transfer is `pgxpool.Conn.Hijack` after normal
pool acquisition and initialization, followed by bounded explicit close.
This preserves TLS configuration, pinned search path, statement timeout, UTC,
application name and the shared backup-writer gate. It changes the budget from
four pooled sessions including the listener to four pooled request/background
sessions plus one dedicated listener. The final diagnostic observer is another
separate existing connection. Neither request pool maximum nor PostgreSQL
durability settings change. Other existing application coordination sessions
must be reported separately from this listener delta.

The RED test uses the unchanged runtime. GREEN checks will cover schema
listener/readiness/writer-lease/reconnect, database session/backup-gate safety
and affected race tests. No claimed causal repair of historical WAL stalls or
547 drops follows from this ownership change alone.

The coordinator fixes the integrated source, obtains independent review, and
launches one hosted final run: 200 RPS for 600 seconds, 120,000 arrivals,
pool maximum four, 32 workers and admission capacity 256, zero drops/errors/
incomplete arrivals and end-to-end p99 at most 250 ms, with existing durable
owner/CRUD/ETag/relations/cursor checks. A second final run requires a new
reproduced cause, validated code change and independent acceptance. No repeated
same-code attempts, durability relaxation or threshold changes are authorized.
