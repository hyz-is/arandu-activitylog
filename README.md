# Arandu Activity Log

The activity log of [spatie/laravel-activitylog](https://github.com/spatie/laravel-activitylog)
v5, adapted to [Arandu](https://github.com/arandu-io/framework): record what
happened, to what, by whom, with what changed — in the tenant of the Grant the
act was authorized with.

```sh
go get github.com/hyz-is/arandu-activitylog@latest
aru migrate
```

## Wiring

In `bootstrap/app.go`, beside everything else the application builds:

```go
activity, err := activitylog.New(activitylog.Config{
	Tenant:                  cfg.Auth.Tenant,
	DefaultExceptAttributes: []string{"password", "token_ciphertext"},
	Policy:                  policies.ActivityPolicy{}, // the application's rules
}, db, sessions)
if err != nil {
	return err
}
kernel.Register(activity) // the table, the read routes and the daily clean

commands, _ := activitylog.Commands(activitylog.Deps{
	Service:  activity.Service(),
	Operator: operatorFor, // who a terminal runs as, per tenant
})
```

The shipped `ActivityPolicy` denies every read and every clean. Recording needs
no policy: an entry is recorded under the Grant of the act it describes.

## Logging activity

```go
logger := activity.Logger()

logger.Activity(ctx, g).
	InLog("billing").
	Event("refunded").
	On(invoice).                      // any Hesape model, or OnRef(activitylog.Ref{...})
	By(operator).                     // default: the Grant's subject
	WithProperties(map[string]any{"amount": 1990}).
	Log(":causer.name refunded :subject.number (:properties.amount)")
```

| Spatie | Arandu |
| --- | --- |
| `activity('log')` | `logger.In(ctx, g, "log")` |
| `performedOn` / `on` | `On(record)`, `OnRef(ref)` |
| `causedBy` / `by` | `By(record)`, `ByRef(ref)` |
| `causedByAnonymous` | `ByAnonymous()` |
| `event`, `withProperties`, `withProperty`, `withChanges`, `createdAt`, `tap` | the same names |
| `when` / `unless` | `When`, `Unless` |
| `Activity::defaultCauser($m, fn)` | `activitylog.WithCauser(ctx, ref)` |
| `withoutLogging(fn)` | `activitylog.WithoutLogging(ctx)` |
| `ACTIVITYLOG_ENABLED=false` | `Config.Disabled` |
| `Activity::beforeLogging` | `Config.BeforeLogging` |
| `LogActivityAction::transformChanges` | `Config.TransformChanges` |
| `CauserResolver::resolveUsing` | `Config.ResolveCauser` |
| buffer | `ctx, buffer := logger.Buffered(ctx)` … `buffer.Flush(ctx)` |

## Logging a record's changes

A record says how it is logged by implementing `ActivityLogOptions`, and is
saved through the logger, which writes the row and its entry in one
transaction:

```go
func (c *Customer) ActivityLogOptions() activitylog.LogOptions {
	return activitylog.DefaultLogOptions().
		LogOnly("name", "email", "address->city").
		LogOnlyDirty().
		DontLogIfAttributesChangedOnly("updated_at").
		UseLogName("customers")
}

logger.Save(ctx, g, customer)    // created or updated
logger.Delete(ctx, g, customer)  // deleted
logger.Restore(ctx, g, customer) // restored, for soft deletes
```

The entry's `attribute_changes` is `{attributes, old}`, as in v5. A record may
also implement `ActivityEvents() []string` (Spatie's `$recordEvents`),
`BeforeActivityLogged(a, event)` and `ActivityLoggingDisabled() bool`, and
`ActivityType() string` when its kind is not its table's name.

Spatie hooks Eloquent's events. A Hesape model event hands a callback the row,
but not the context or the Grant of the write, so a hook could not write the
entry in the same transaction and tenant without hidden state. Saving through
the logger is that hook, written where it can be read.

## Everything the application already records

Two bridges log what an Arandu application already does, with no change to the
code that does it:

```go
// Every committed domain event of the outbox, as an entry of its tenant: its
// name, its aggregate, who authorized it, when. Delivered at least once,
// recorded once.
// recorder is an events.Publisher: hand it to the relay, alone or beside the
// application's other publishers.
recorder := activitylog.NewEventRecorder(activity.Logger())
relay := events.NewRelay(events.NewOutbox(db), recorder, events.RelayOptions{Locker: locker})

// Every message the mailer sends, sent or refused, in the "mail" log of the
// application's own tenant.
transport, _ := activitylog.NewMailTransport(mailTransport(cfg.Mail), activity.Logger(), cfg.Auth.Tenant)
mailer := mail.New(transport, renderer, from)
```

## Permissions

`activitylog.Actions()` are the log's actions; declare them in
arandu-permission's catalogue beside the application's own, and pass
`activitylog.PermissionPolicy{}` as `Config.Policy` so an organization grants
reading or cleaning its log to whom it decides.

### The platform administrator reads every tenant's log

A user reads their own entries through `Config.Policy`, in their tenant. The
platform's administrator reads every entry of every tenant through
`ActivityService.ReadAll`, which asks `Config.PlatformPolicy` -- and no other
policy -- for `activitylog.ActivityViewAll`:

```go
activity, err := activitylog.New(activitylog.Config{
	Policy:         activitylog.PermissionPolicy{}, // an organization's own log
	PlatformPolicy: policies.PlatformActivity{},    // the application's rule for its platform managers
}, db, sessions)

page, err := activity.Service().ReadAll(ctx, actor, activitylog.PlatformQuery{
	Tenants: []string{"acme"},                     // empty: every tenant
	Filter:  activitylog.Filter{LogNames: []string{"auth"}},
	Cursor:  cursor,                               // page.NextCursor of the page before
	Limit:   50,
})
// page.Entries[i].Tenant names whose entry it is.

entry, err := activity.Service().ReadEntry(ctx, actor, id) // one entry, of any tenant
```

### A person reads their own entries

The other half of the rule: a person reads what they caused, and what an
operator did inside their account, in every tenant, and nobody else's entries.
`ReadOwn` needs no policy and no grant -- every signed-in subject holds it over
its own entries -- and narrows the read to them whatever the query says:

```go
page, err := activity.Service().ReadOwn(ctx, subject, activitylog.OwnQuery{
	Filter: activitylog.Filter{LogNames: []string{"billing"}},
	Cursor: cursor,
})
entry, err := activity.Service().ReadOwnEntry(ctx, subject, id) // ErrNotFound when it is not theirs
```

Nil `PlatformPolicy` refuses everybody, and so do `ActivityPolicy` and
`PermissionPolicy`: the read exists once the application writes the rule for
its platform administrators. `activitylog.PlatformActions()` are declared apart
from `Actions()` so no organization's group is ever offered them. A subject
inside somebody else's account is refused before any policy. `ReadAll` answers
`PlatformEntry` snapshots, which hold no connection, so nothing read across
tenants can be saved or deleted through them; it pages by the same keyset as
`List` and counts nothing. There is no route for it: the application mounts the
platform screen where its platform administrators sign in.

## Screens

With `Config.Screens`, the read routes draw the log -- filtered, paged, with
what each entry is about and who caused it -- and one entry with what changed
before and after. Publish and compile them, then import the compiled package:

```sh
aru vendor:publish --apply
aru view:build
```

`Config.Locale` picks the words ("en", "pt-BR"), and `Config.Name` names a
subject or a causer -- a user by their name rather than their identifier.

The screens number their pages with `Paginate`, which counts every entry the
filter keeps on every page drawn: the count grows with the log. With
`Config.SimplePagination` they page with `SimplePaginate` instead -- Previous and
Next, no total -- and cost what the page costs. Publish the views again when
turning it on, or an older index view prints "of 0 · 0".

## Reading and cleaning

```go
entries, err := activity.Service().List(ctx, actor, activitylog.Filter{
	LogNames: []string{"billing"},           // inLog
	Subject:  activitylog.Ref{Type: "invoices", ID: id}, // forSubject
	Causer:   activitylog.Ref{Type: "user", ID: userID}, // causedBy
	Event:    "refunded",                    // forEvent
}, data.Query{Limit: 50})

// What a person did, and what an operator did inside their account: the
// "mine" a person reads. causer_id or on_behalf_of is the ID.
mine, err := activity.Service().List(ctx, actor, activitylog.Filter{
	Involves: activitylog.Ref{Type: "user", ID: actor.ID},
}, data.Query{Limit: 50})

// Pages for a screen: Paginate counts the log (Page.Pages, Page.Total);
// SimplePaginate does not, and says only Page.HasMore. List pages by a cursor
// -- keyset on (created_at, id) -- and costs the same on any page.
page, err := activity.Service().SimplePaginate(ctx, actor, activitylog.Filter{}, 2, 50)

removed, err := activity.Service().Clean(ctx, actor, 365, "") // activitylog:clean
```

`activitylog:clean {log?} --tenant= --days= --force` does the same from a
terminal, and the module schedules it daily per tenant (`Config.CleanSpec`,
`"-"` for none, `Config.CleanAfterDays`, 365 by default).

An entry recorded while an operator views or acts in somebody's account names
the operator as its causer and the account in its `on_behalf_of` column (and,
as before, in the `impersonation` property). The column was added after
v0.3.0 and is not backfilled: older entries are found by `Causer`, as the
operator's, and not by `Involves` as the account's.

`GET /activity-log` and `GET /activity-log/{id}` answer the same reads as JSON,
under `Config.Prefix`. `?involves_type=` and `?involves_id=` are `Filter.Involves`.

## License

MIT. See [LICENSE.md](LICENSE.md).
