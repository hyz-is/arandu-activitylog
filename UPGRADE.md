# Upgrade Guide

## v0.7.0

Go 1.26.10 is the floor: `go.mod` declares `go 1.26.10`, because every
vulnerability an Arandu application reaches is in the standard library and is
fixed there (crypto/x509, net/http HTTP/2 and CONNECT, html/template,
net/textproto, crypto/tls). A build on an older toolchain fetches 1.26.10 or
fails when `GOTOOLCHAIN=local`.

This release selects Framework v0.66.0 and Hesape v0.85.0; read their upgrade
guides from the versions the application required before (Framework v0.58.0
and Hesape v0.69.0 in v0.6.0). Hesape refuses to start a session for a subject
that carries roles or actions unless the application installed a
`SubjectResolver` with `ResolveSubjects`, and Framework reads an unset
`APP_ENV` as production.

`apidiff` reports `New` and `(*Module).Routes` as changed. Their signatures
read as before; what moved is underneath the names they take: Framework
v0.63.0 declares `security.SessionStore` as an alias of hesape's
`http.SessionStore` and `fhttp.Router` as an alias of hesape's
`controllers.Router`, where v0.58.0 declared structs of its own. An application
on Framework v0.66.0 passes the store and the router it already builds and
changes nothing; one that stays on an older Framework cannot take this release.

`PermissionPolicy` and `ActivityPolicy` implement `auth.Writes`: only
`ActivityClean` writes, and a Grant `security.Authorize` issues for
`ActivityView`, `ActivityViewAll` or `ActivityViewOwn` only reads, so the
database refuses any insert, update or delete made with it
(`auth.ErrReadGrant`). Nothing in this package writes with such a Grant. An
application whose own `Config.Policy` or `Config.PlatformPolicy` reads the log
gets the same protection by implementing `auth.Writes` on it.

## v0.5.0

Run `aru migrate`: `20261019_0001_widen_activity_log_time_precision` raises the
log's two instants from whole seconds to microseconds where a release before
Hesape v0.67.0 created them so. On PostgreSQL it changes the type modifier
only and rewrites neither the table nor its indexes; on MySQL the columns are
restated with `MODIFY`, which may copy the table -- on a large log, run it
when a copy is acceptable. No entry changes. A process still running the
previous binary may see PostgreSQL refuse a cached plan over the table once per
connection ("cached plan must not change result type"): migrate before the new
binary starts.

This release selects Framework v0.58.0 and Hesape v0.69.0; follow their upgrade
notes between the versions you had and these. No symbol of this package
changes.

## v0.4.2

Nothing to change: `ReadOwn`, `ReadOwnEntry`, `OwnQuery` and
`ActivityViewOwn` are new. An application that showed a person their history
one organization at a time -- a `List` with `Filter.Involves` per tenant the
person belongs to -- reads it in one page across all of them:

```go
page, err := activity.Service().ReadOwn(ctx, subject, activitylog.OwnQuery{Cursor: cursor})
```

`ActivityViewOwn` is not declared in any catalogue: nobody grants a person
their own history.

## v0.4.1

Nothing to change: `ActivityService.ReadEntry` is new. An application that
opened one entry of an organization's log from its platform console by
reading it as somebody of that organization reads it with the platform read
instead:

```go
entry, err := activity.Service().ReadEntry(ctx, actor, id) // PlatformEntry, its Tenant set
```

## v0.4.0

Nothing to change in code that calls this package: every addition is a new
field, method or option, and leaving each one out keeps what v0.3.0 did.

### Run `aru migrate` before the new version serves

The release adds the `on_behalf_of` column to `activity_log`, and the logger
writes it on every entry. An application that deploys it without running
`aru migrate` first fails every entry with "no such column" (or "column does
not exist"). The migration adds a nullable column and one index; it does not
rewrite rows.

Nothing is backfilled. An entry an operator made inside somebody's account
before the migration keeps the account in its `impersonation` property, so
`Filter.Involves` finds it as the operator's (by causer) and not as the
account's. An application that needs the old entries there copies the
property into the column itself, at a time it chooses:

```sql
-- PostgreSQL; SQLite spells it json_extract(properties, '$.impersonation.on_behalf_of')
update activity_log
   set on_behalf_of = (properties::jsonb -> 'impersonation' ->> 'on_behalf_of')
 where on_behalf_of is null and properties like '%"impersonation"%';
```

### The platform administrator reads every tenant

An application that read the platform's view of the log one tenant at a time,
with a Grant per tenant, writes the rule for its platform administrators and
reads them all at once:

```go
type PlatformActivity struct{}

func (PlatformActivity) Can(ctx context.Context, s security.Subject, a security.Action, _ activitylog.Activity) error {
	if a == activitylog.ActivityViewAll && isPlatformManager(s) {
		return nil
	}
	return errors.New("not a platform manager")
}

activitylog.Config{PlatformPolicy: PlatformActivity{}}
```

then `activity.Service().ReadAll(ctx, actor, activitylog.PlatformQuery{...})`.
Declare `activitylog.PlatformActions()` only where platform administrators are
granted, never in the catalogue an organization grants from. Nothing changes
for an application that does not set `PlatformPolicy`: `ReadAll` refuses
everybody.

### "Mine" in one filter

An application that read a person's own history with two queries -- caused by
them, and the impersonation property naming them -- reads it with one:

```go
activitylog.Filter{Involves: activitylog.Ref{Type: "user", ID: subject.ID}}
```

### Paging the screens without a count

`Config.SimplePagination: true` draws the log screen from `SimplePaginate`:
Previous and Next, and no page count or total, so no `COUNT(*)` over the
tenant's log on every page. Publish the views again (`aru vendor:publish
--apply`, then `aru view:build`) when turning it on: an index view published
from an earlier version prints "of 0 · 0" without the count. Code that pages
by itself may call `SimplePaginate` in place of `Paginate`, or `List` with a
cursor for a log of any size.

## v0.3.0

An entry recorded inside somebody's account names the operator as its causer
and carries the `impersonation` property. Requires hesape v0.56.0.
