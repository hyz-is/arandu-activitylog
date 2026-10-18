# Upgrade Guide

## Unreleased

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

### "Mine" in one filter

An application that read a person's own history with two queries -- caused by
them, and the impersonation property naming them -- reads it with one:

```go
activitylog.Filter{Involves: activitylog.Ref{Type: "user", ID: subject.ID}}
```

## v0.3.0

An entry recorded inside somebody's account names the operator as its causer
and carries the `impersonation` property. Requires hesape v0.56.0.
