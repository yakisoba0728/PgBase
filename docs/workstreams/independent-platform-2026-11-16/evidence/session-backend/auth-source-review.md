# Independent source review: auth request policy context

Reviewer: `/root/backend`, separate from the Security agent.
Reviewed runtime change: `86dbfa822ea69e0c98867a66e52efdc634377c3a`,
exact tree `9a76dd6550d8b91a0a527ad82edea4d3aec05272`.

No blocking issue was found in the auth request-context change.

Read `internal/api/router.go`, the request-context wrapper/snapshot helper,
native/public auth collection and admin-user route declarations, session
admission/issuance callers, `internal/userauth/auth_rule_resolver.go`, the query
parser's request-operand mapping, the rule AST, both new regression files and
the upgrade contract.

- The transport wrapper runs inside admission and snapshots the request used
  by the handler. Current native login/session issuance, collection-specific
  auth, public auth-record and admin-user impersonation route families match
  the added route selection. This does not grant a new principal.
- The auth resolver copies only method, headers, query and context. It builds
  body/presence/change data from the operation and reloads the active actor from
  the same auth collection in PostgreSQL. Transport-supplied `Auth` cannot
  override stored verification state.
- Missing transport metadata or method causes a request-dependent rule to
  deny before compilation. Dependency scanning includes nested rule arguments,
  both comparison operands and function fields. Body/actor-only embedded calls
  keep their existing service authority. Denial of the entire expression when
  transport data is absent is an intentional documented compatibility change.
- Tests include negative and positive method/header/query cases for
  anonymous/authenticated list/view and password login, missing request
  metadata, nested/RHS dependencies, spoofed actor metadata and operation body.
  Literal false/empty-rule controls separate transport semantics from fixture
  registration/access mistakes.

This is independent **source review**, not execution. No tests were started
during the Performance agent's exclusive measurement window. The Security
agent's before/after runs retain their own ownership. Full OAuth/MFA callback
and extension-adapter execution, recovered avatar/file changes, and final
integrated qualification are outside this focused review.
