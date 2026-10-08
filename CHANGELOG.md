# Changelog

Everything worth knowing about a release of Arandu Activity Log is recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

A published module version is immutable: Go serves it from the proxy forever, so
a release is corrected by another release and never by moving a tag.

## [Unreleased]

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
  `Record` requires `Fresh`, which every Hesape model has.
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
