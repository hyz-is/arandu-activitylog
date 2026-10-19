# Changelog

Everything worth knowing about a release of Arandu Activity Log is recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

A published module version is immutable: Go serves it from the proxy forever, so
a release is corrected by another release and never by moving a tag.

## [Unreleased]

## [0.5.0] - 2026-10-19

### Added

- The migration `20261019_0001_widen_activity_log_time_precision`: the log's
  `created_at` and `updated_at`, which an earlier framework default created in
  whole seconds -- PostgreSQL rounds a value to the second there, MySQL
  truncates it -- are raised to microseconds through Hesape's
  `migrations.WidenTimePrecision`, so entries written within one second are
  read newest first by the instant and not by the identifier alone. A column
  already in microseconds is left alone, SQLite has nothing to widen, and the
  rollback keeps the columns wide.

### Changed

- Requires Framework v0.58.0 and Hesape v0.69.0. Hesape declares a date-time
  column in microseconds by default, and its `NewOrderedID` -- the identifier
  of every entry, and the tie-break of every newest-first read -- is strictly
  increasing within a process. The manifest's framework floor is `>= 0.58`.
  The suite requires the PostgreSQL connector v0.11.0, and CI runs it against
  a PostgreSQL service.

## [0.4.2] - 2026-10-19

### Added

- `ActivityService.ReadOwn` and `ReadOwnEntry`: a person reads their own
  entries in every tenant -- what they caused and what an operator did inside
  their account -- newest first, by the keyset `ReadAll` pages by, and one of
  them by its identifier. Every signed-in subject may, over its own entries
  only: the read is narrowed to `Filter.Involves` naming the subject whatever
  the query says, and an entry of somebody else answers `ErrNotFound`. A
  guest is refused. The rule is the package's, under the new action
  `ActivityViewOwn` (`activity.view_own`), which nobody grants.
- `OwnQuery`.

## [0.4.1] - 2026-10-19

### Added

- `ActivityService.ReadEntry`: one entry of any tenant's log by its
  identifier, the platform administrator's read of a single entry as
  `ReadAll` is of a page. It asks `Config.PlatformPolicy` for
  `ActivityViewAll`, refuses a subject inside somebody else's account, and
  answers a `PlatformEntry` snapshot or `ErrNotFound`.

## [0.4.0] - 2026-10-18

### Added

- `ActivityService.ReadAll`: the platform administrator reads every tenant's
  log, or the tenants `PlatformQuery.Tenants` names, newest first, by the same
  keyset as `List`, with every `Filter`. It asks `Config.PlatformPolicy` -- a
  new field, and the only policy it asks -- for the new action
  `ActivityViewAll` (`activity.view_all`). Nil refuses everybody, and
  `ActivityPolicy` and `PermissionPolicy` never allow it, so a `Config.Policy`
  that opens an organization's log opens nothing across organizations. A
  subject inside somebody else's account is refused. It answers
  `PlatformEntry` snapshots, with the entry's tenant, that hold no connection.
- `PlatformActions()`, declared apart from `Actions()` so a catalogue an
  organization grants from never holds the platform read.
- `Filter.Involves`: the entries a subject caused **or** that an operator made
  inside its account -- "caused by or done on behalf of", what a person reads
  as their own history. The read route takes it as `?involves_type=` and
  `?involves_id=`.
- `Activity.OnBehalfOf` and the `on_behalf_of` column, indexed with the tenant
  (`activity_log_tenant_on_behalf_of_index`). The logger fills it from the
  Grant whenever an operator views or acts in somebody's account, and clears
  whatever a tap set on any other entry. The JSON resource answers it.
- The migration `20261018_0001_add_on_behalf_of_to_activity_log_table`. Run
  `aru migrate` before the new version serves: the logger writes the column.
  It backfills nothing: entries recorded before it keep the account only in
  their `impersonation` property.
- `ActivityService.SimplePaginate`, Eloquent's simplePaginate: one numbered
  page without counting the log, reading one entry past it to say
  `Page.HasMore`. `Paginate` counted every entry the filter keeps on every
  page, 11 to 40 ms on 500 thousand entries of one tenant, growing with the
  log.
- `Page.HasMore`, filled by `Paginate` and `SimplePaginate`.
- `Config.SimplePagination`: the screens page with `SimplePaginate` -- Previous
  and Next, no total. `IndexPageData.Counted` says which, and the published
  index view draws the page count only when it is set.

### Changed

- `PermissionPolicy` refuses `ActivityViewAll`, which did not exist before.
  Every action it allowed it still allows.

## [0.3.0] - 2026-10-13

### Added

- An entry recorded while an operator views or acts in somebody's account
  (hesape `auth.Impersonate` / `auth.ImpersonateActing`) names the operator as
  its causer, and carries the `impersonation` property (`ImpersonationProperty`):
  `by` the operator, `on_behalf_of` the account, `mode` `viewing` or `acting`.
  An explicit causer (`By`, `WithCauser`, `Config.ResolveCauser`) still wins.

### Fixed

- An entry recorded with the Grant of somebody only viewing an account was
  refused, because that Grant writes nothing: the record that the account was
  looked at was lost. The entry is now written in the same tenant with a Grant
  to record only.

### Changed

- Requires hesape v0.56.0.

## [0.2.3] - 2026-10-09

### Changed

- Requires Framework v0.56.0 and Hesape v0.54.0. The manifest's framework
  floor is `>= 0.56`. No symbol, route, migration, action or policy decision
  changes: `apidiff` against v0.2.2 reports nothing.
- An application below those releases upgrades them first, following their
  upgrade guides. From Framework v0.56.0 the configuration bridge no longer
  reads `SESSION_TTL` and `config.Config.SessionTTL` is gone; an application
  that boots with `bootstrap.LoadConfiguration` is unaffected.

### Fixed

- The screens draw their chrome with `view.New`, so the log and an entry
  show the application name the framework puts on the request, and the
  navigation of the routes the application registered. Before, they drew no
  brand in the application's layout.

## [0.2.2] - 2026-10-09

### Changed

- Requires Framework v0.55.1 and Hesape v0.52.0. The manifest's framework
  floor is `>= 0.55`. No symbol, route, migration, action or policy decision
  changes: `apidiff` against v0.2.1 reports nothing.
- An application below those releases upgrades them first, following their
  upgrade guides from the versions it required before. From Framework v0.55.0
  the session is configured only by what the session store reads and an
  unread `SESSION_*` setting stops the boot; from v0.54.0 a boolean setting
  that does not read as one stops the boot. Hesape v0.52.0 removed the names
  it had deprecated.

## [0.2.1] - 2026-10-08

### Fixed

- A record's change is read back through its table rather than its own
  `Fresh`, which a model generated by `aru model:build` narrows to its own
  type: such a model did not satisfy `Record`, so v0.2.0 could not log it.
  `Record` no longer requires `Fresh`, and the test model now has a narrowed
  one, as the generated models do.

## [0.2.0] - 2026-10-08

### Added

- Screens: the log and one entry, with what each is about and who caused
  it, and what changed before and after. `Config.Screens` draws them for the
  read routes, `Publishes` hands their sources to `aru vendor:publish`, and
  `Boot` refuses to start without them compiled. `Config.Locale` ("en",
  "pt-BR") and `Config.Name`, which names subjects and causers.
- `Actions()`, for an application to declare the log's actions in
  arandu-permission's catalogue, and `PermissionPolicy`, which decides by
  the subject's granted actions within its tenant.
- `Logger.ForceDelete`, logged as deleted. `RefOf`, `ActivityService.ForSubject`
  and `ActivityService.CausedBy`. `DoesNotRecordEvents`. `PendingActivity.Entry`
  and `SetEvent`. `When` and `Unless` take a default branch.
- `Logger.WithBuffer` and `Logger.BufferRequests`, which flush what a job or a
  request buffered once it ends.

### Changed

- **Breaking.** A pending entry is Spatie's: every method writes into it at
  once, a tap runs immediately and a method after it overrides it, and a
  description a tap set has its placeholders replaced too. `InLog("")` leaves
  the entry with no log, as `useLog(null)`; `In` with an empty name keeps the
  default.
- **Breaking.** `LogOnly`, `LogExcept`, `DontLogIfAttributesChangedOnly` and
  `UseAttributeRawValues` replace what was named before, as Spatie's do.
- A record's change is read from the row as stored, before and after the
  write, with the relations its options name: a relation path ("author.name")
  is logged before and after, and an unchanged value is no longer reported
  as changed because of the precision it was kept in.
- A name the record does not hold, and a JSON path that leads nowhere, are
  logged as null. Times are written as Laravel writes them
  (`2006-01-02T15:04:05.000000Z`). Clearing deleted_at through Save is not
  logged as an update. `DontLogIfAttributesChangedOnly` holds a creation back
  too. `TransformChanges` runs on every entry.
- Placeholders match their base exactly, keep their token for an absent
  causer, and read the loaded relations of the subject and the causer.
- A failed `Buffer.Flush` keeps its entries for the next one.
- `activitylog:clean` says "Cleaning activity log..." and "All done!", and
  refuses an empty `--days`.

## [0.1.0] - 2026-10-08

### Added

- The activity log of spatie/laravel-activitylog v5, adapted to Arandu: the
  `activity_log` table with the tenant of the Grant every entry is recorded
  under; `Logger.Activity` with `InLog`, `On`, `OnRef`, `By`, `ByRef`,
  `ByAnonymous`, `Event`, `WithProperties`, `WithProperty`, `WithChanges`,
  `CreatedAt`, `Tap`, `When`, `Unless` and `Log`, and the `:subject`, `:causer`
  and `:properties` placeholders.
- A record's changes, logged by `Logger.Save`, `Logger.Delete` and
  `Logger.Restore` in the record's own transaction, shaped by `LogOptions`
  (`LogAll`, `LogOnly` with relation and JSON paths, `LogExcept`,
  `LogOnlyDirty`, `DontLogIfAttributesChangedOnly`, `DontLogEmptyChanges`,
  `UseLogName`, `SetDescriptionForEvent`, `UseAttributeRawValues`) and by the
  record's own `ActivityEvents`, `BeforeActivityLogged` and
  `ActivityLoggingDisabled`.
- `Config.BeforeLogging`, `Config.TransformChanges`, `Config.ResolveCauser`, `Config.CleanTenants`,
  `Config.DefaultExceptAttributes`, `WithoutLogging`, `WithCauser` and
  `Logger.Buffered`.
- `EventRecorder`, an `events.Publisher` that writes every committed domain
  event of the outbox as an entry of its tenant, once however often it is
  delivered, and `MailTransport`, which writes every message a mailer sends,
  sent or refused, into the `mail` log.
- `ActivityService` to read the log, by cursor or by numbered page (`Paginate`), by Spatie's scopes and to clean it, behind
  `ActivityPolicy`, which denies everything until the application passes its
  own; the read routes; the `activitylog:clean` command; and a daily clean per
  tenant.
