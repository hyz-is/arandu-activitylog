# Changelog

Everything worth knowing about a release of Arandu Activity Log is recorded here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the versions follow [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

A published module version is immutable: Go serves it from the proxy forever, so
a release is corrected by another release and never by moving a tag.

## [Unreleased]

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
