# Auth rules and request metadata

Auth collection rules evaluate `@request.method`, `@request.headers`,
`@request.query` and `@request.context` against the originating HTTP request.
The native authentication routes, public/authenticated auth-record routes and
administrator user routes capture that metadata after route admission. The
snapshot remains attached to the service call and its database transaction.
Header names use lowercase with hyphens replaced by underscores, as in
`@request.headers.x_scope`; query operands use their original parameter names.

The transport snapshot does not supply the actor or replace the operation body.
The auth-rule service builds `@request.body`, `:isset` and `:changed` from its
validated operation input, and reloads an active user actor from the applicable
auth collection in PostgreSQL before evaluating `@request.auth`. Request headers
or a stale caller snapshot cannot make an unverified stored user verified.

An `@request.auth.<key>` the actor does not have (a profile field the actor's
record lacks, or for an anonymous caller any key other than `id` and `email`,
which auth rules always supply) evaluates as NULL rather
than failing the request. `= null` is then true, `!= null` false, and every
other comparison false, so a bare `@request.auth.<key> = null` also matches
anonymous callers; guard it with `@request.auth.id != ''` when it should apply
only to signed-in actors.

An embedded/background call without request metadata cannot satisfy a rule
that depends on HTTP metadata. In particular, a missing method/header/query
must not make a negative comparison such as `@request.method != 'GET'` grant
access. This applies to the entire rule, including nested alternatives and
operands on either side of a comparison. Body-only and database-actor rules
continue to work for embedded calls.

## Upgrade behavior

Earlier versions could evaluate the supported method/header/query operands as
empty values in auth collection rules. Positive predicates could deny valid
requests; negative predicates could allow requests that the configured rule
should reject, including reads through public auth-record routes. Review any
such rules when upgrading. Their intended HTTP values now determine access.
Embedded callers that relied on an empty HTTP value must use body/actor rules
appropriate to their execution context. No database migration or token-format
change is required.

Regression evidence covers anonymous/authenticated auth-record list/view,
password login/session issuance, request-less calls, and preservation of
database actor and operation-body authority. Those cases do not by themselves
certify every OAuth/MFA callback, extension adapter or custom policy expression.
