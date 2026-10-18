package activitylog

import (
	"context"
	"fmt"
	"time"

	"github.com/arandu-io/framework/security"

	"github.com/hyz-is/arandu-activitylog/internal/everytenant"
)

// entryOf is a row of any tenant as ReadAll answers it.
func entryOf(r *everytenant.Entry) PlatformEntry {
	carried := Activity{AttributeChanges: r.AttributeChanges, PropertiesJSON: r.PropertiesJSON}
	return PlatformEntry{
		Tenant: r.TenantID, ID: r.ID, LogName: text(r.LogName), Description: r.Description, Event: text(r.Event),
		Subject:    Ref{Type: text(r.SubjectType), ID: text(r.SubjectID)},
		Causer:     Ref{Type: text(r.CauserType), ID: text(r.CauserID)},
		OnBehalfOf: text(r.OnBehalfOf), Changes: carried.Changes(), Properties: carried.Properties(),
		CreatedAt: r.CreatedAt.UTC(),
	}
}

// PlatformQuery is what ReadAll reads: the narrowing every listing takes, the
// tenants to keep, and the page.
type PlatformQuery struct {
	Filter
	// Tenants keeps the entries of these tenants. Empty keeps every tenant's.
	Tenants []string
	// Cursor is the NextCursor of the previous page; empty is the first.
	Cursor string
	// Limit is how many entries a page holds: zero means 50, and more than 200
	// is 200.
	Limit int
}

// PlatformEntry is one entry of any tenant's log as ReadAll answers it.
//
// It is a snapshot and not an Activity: an Activity carries the connection it
// was read with, and one read without the tenant filter could be saved or
// deleted through it in a tenant nobody authorized. A PlatformEntry holds no
// connection, so the platform read stays a read.
type PlatformEntry struct {
	// Tenant is the customer the entry belongs to -- what a platform screen
	// shows beside it, and the one field no tenant's own listing answers.
	Tenant      string
	ID          string
	LogName     string
	Description string
	Event       string
	Subject     Ref
	Causer      Ref
	// OnBehalfOf is whose account an operator made the entry in, or empty.
	OnBehalfOf string
	Changes    Changes
	Properties map[string]any
	CreatedAt  time.Time
}

// PlatformPage is one page of ReadAll, newest first, and the cursor of the
// next. NextCursor is empty on the last page.
type PlatformPage struct {
	Entries    []PlatformEntry
	NextCursor string
}

// ReadAll pages through the log of every tenant, or of the tenants q names,
// newest first: the platform administrator's read, who reads every entry
// where a user reads only their own.
//
// It asks Config.PlatformPolicy, and no other policy, for ActivityViewAll.
// Nil there refuses everybody, as the two policies this package ships do, so
// the read exists only once the application wrote the rule that opens it for
// its platform administrators. A subject inside somebody else's account is
// refused before any policy: an operator acts there as the account, and the
// account reads no other tenant.
//
// It pages by a keyset on (created_at, id), the order List pages in, and
// counts nothing. The cursor is the identifier of the last entry of the
// previous page; a page that ends on an entry removed since then is the last.
func (s *ActivityService) ReadAll(ctx context.Context, actor security.Subject, q PlatformQuery) (PlatformPage, error) {
	if actor.Impersonator != nil {
		return PlatformPage{}, fmt.Errorf("%w: a subject inside somebody else's account reads no other tenant", security.ErrForbidden)
	}
	for _, tenant := range q.Tenants {
		if !security.ValidTenant(tenant) {
			return PlatformPage{}, fmt.Errorf("activitylog: PlatformQuery.Tenants holds %q, which cannot be a tenant", tenant)
		}
	}
	g, err := security.Authorize(ctx, s.platform, actor, ActivityViewAll, Activity{})
	if err != nil {
		return PlatformPage{}, err
	}
	limit := q.Limit
	switch {
	case limit <= 0:
		limit = defaultLimit
	case limit > maxLimit:
		limit = maxLimit
	}
	page := everytenant.Entries(s.db)
	narrow(page.Base(), q.Filter, s.cfg.CauserType)
	if len(q.Tenants) > 0 {
		tenants := make([]any, 0, len(q.Tenants))
		for _, tenant := range q.Tenants {
			tenants = append(tenants, tenant)
		}
		page.WhereIn("tenant_id", tenants)
	}
	if q.Cursor != "" {
		anchor, err := everytenant.Entries(s.db).WhereKey(q.Cursor).Value(ctx, g, "created_at")
		if err != nil {
			return PlatformPage{}, err
		}
		if anchor == nil {
			return PlatformPage{}, nil
		}
		olderThan(page.Base(), anchor, q.Cursor)
	}
	rows, err := page.OrderByDesc("created_at").OrderByDesc("id").Limit(limit+1).Get(ctx, g)
	if err != nil {
		return PlatformPage{}, err
	}
	out := PlatformPage{Entries: make([]PlatformEntry, 0, min(len(rows), limit))}
	for i, row := range rows {
		if i == limit {
			out.NextCursor = out.Entries[limit-1].ID
			break
		}
		out.Entries = append(out.Entries, entryOf(row))
	}
	return out, nil
}
