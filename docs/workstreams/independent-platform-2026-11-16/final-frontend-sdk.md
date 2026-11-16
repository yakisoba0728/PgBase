# Final continuation: frontend and SDK audit

Base: published PR head `b376e36697674544be821be0290e24916d81da7d`.
This is an independent continuation; prior successful CI is historical evidence,
not qualification of the changes recorded here.

## Reproduced and corrected: Realtime subscription lifetimes

The TypeScript and Dart shared Realtime services checked authentication only
before waiting for a previous, aborted connection to drain. A pending subscription
could survive an account change during that wait, open a connection using the new
account's bearer, and install the old account's callback. Both also returned
cleanup functions that referred only to the topic and callback: an old screen's
cleanup could remove a replacement account's subscription when the same callback
was reused.

The added regression files reproduce both schedules using controlled in-memory
transports. The old transport's close is explicitly held until after the account
changes; there is no timing-based delay to guess the ordering. Against the
unmodified baseline, both TypeScript cases failed (`Missing expected rejection`
and `stopped` instead of `connected`), and both Dart cases failed for the same
reasons. The actual TypeScript reproduction dispatched `Bearer account-B` for the
subscription started under account A.

Subscriptions now retain the initiating authentication generation through drain
and readiness. Cleanup is bound to its original connection and listener set.
The TypeScript connect path also checks for a stop after asynchronous topic sync,
so an obsolete connection cannot report itself connected after it was stopped.
Neither SDK automatically transfers existing subscriptions between identities.

Validation after the patch:

- TypeScript targeted Realtime protocol/status/lifetime suites: **14 passed**.
- Dart targeted Realtime protocol/status/lifetime suites: **15 passed**.
- Complete TypeScript command (`npm test`), including build and public type
  contract: **111 passed**, zero failures/skips in the Node test-runner suite;
  the additional existing `sdk.test.mjs` script also passed.
- Complete Dart suite: **132 passed**; `dart analyze`: no issues.
- Generated TypeScript `dist/realtime.js` rebuilt from source. No UI design or
  public request/response schema changed.

Tests use Node 24 and Dart 3.13.3, disposable in-memory HTTP streams and synthetic
tokens, without network servers or production credentials. These are client
lifetime regressions, not server authorization bypass demonstrations.

## Coverage and remaining work

Directly traced in this pass: shared Realtime subscribe/start/readiness/topic
sync/event dispatch/stop/disposer boundaries in both SDKs, their authentication
stores and cancellation/transport callers; administrator session transport and
query-cache recovery; managed-backup browser exclusion and its console/API/job
callers. This does not yet constitute a complete screen-by-screen, cross-browser,
accessibility, entire SDK or server security audit. Further work is recorded in
subsequent sections when implemented and executed.
