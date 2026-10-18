package activitylog

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database/model"
)

// Pagination bounds for List: a request that asks for everything gets the
// maximum, never everything.
const (
	defaultLimit = 50
	maxLimit     = 200
)

// ActivityService reads and cleans the log, after its policy answers.
//
// It is the half of the package that a person reaches -- a screen, a terminal
// -- and every method authorizes before the first statement. The Logger is the
// other half, reached by code that already holds the Grant of what it records.
type ActivityService struct {
	db       *data.DB
	policy   security.Policy[Activity]
	platform security.Policy[Activity]
	cfg      Config
}

// NewActivityService wires the service over the application's database handle
// and the configured policy, ActivityPolicy when the configuration names none.
func NewActivityService(db *data.DB, cfg Config) (*ActivityService, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.withDefaults()
	return &ActivityService{db: db, policy: cfg.Policy, platform: cfg.PlatformPolicy, cfg: cfg}, nil
}

// Filter narrows a listing: Spatie's inLog, causedBy, forSubject and forEvent
// scopes, and a window of time. Every field left empty narrows nothing.
type Filter struct {
	// LogNames keeps the entries of these logs.
	LogNames []string
	// Event keeps the entries of this event.
	Event string
	// Subject keeps the entries about this thing; a Ref with only a Type
	// keeps every entry about that kind.
	Subject Ref
	// Causer keeps the entries caused by this one; a Ref with only a Type
	// keeps every entry caused by that kind.
	Causer Ref
	// Involves keeps the entries this one caused and the entries an operator
	// made inside its account -- causer_id, or on_behalf_of, is its ID: what a
	// person reads as "what was done by me or for me". Its Type narrows the
	// causer as Causer's does; on_behalf_of names an account of the kind
	// Config.CauserType records, so a Type of another kind matches only as
	// causer. A Ref with no ID narrows nothing.
	//
	// The column on_behalf_of is not backfilled: an entry made inside an
	// account before the column was added is found as the operator's, by
	// Causer, and not as the account's.
	Involves Ref
	// Since and Until bound when the entries happened, Since inclusive and
	// Until exclusive.
	Since, Until time.Time
	// Search keeps the entries whose description contains this.
	Search string
}

// Find reads one entry.
func (s *ActivityService) Find(ctx context.Context, actor security.Subject, id string) (*Activity, error) {
	g, err := security.Authorize(ctx, s.policy, actor, ActivityView, Activity{})
	if err != nil {
		return nil, err
	}
	record, err := Activities(s.db).WhereKey(strings.TrimSpace(id)).First(ctx, g)
	if err != nil {
		return nil, err
	}
	if record == nil {
		return nil, ErrNotFound
	}
	if _, err := security.Authorize(ctx, s.policy, actor, ActivityView, *record); err != nil {
		return nil, err
	}
	return record, nil
}

// List pages through the log, newest first: by when each entry happened, and
// by identifier within one instant. The cursor is the identifier of the last
// entry of the previous page.
func (s *ActivityService) List(ctx context.Context, actor security.Subject, filter Filter, q data.Query) ([]*Activity, error) {
	g, err := security.Authorize(ctx, s.policy, actor, ActivityView, Activity{})
	if err != nil {
		return nil, err
	}
	limit := q.Limit
	switch {
	case limit <= 0:
		limit = defaultLimit
	case limit > maxLimit:
		limit = maxLimit
	}
	page := filtered(Activities(s.db), filter, s.cfg.CauserType)
	if q.Cursor != "" {
		anchor, err := Activities(s.db).WhereKey(q.Cursor).Value(ctx, g, "created_at")
		if err != nil {
			return nil, err
		}
		if anchor == nil {
			return nil, nil
		}
		olderThan(page.Base(), anchor, q.Cursor)
	}
	return page.OrderByDesc("created_at").OrderByDesc("id").Limit(limit).Get(ctx, g)
}

// Page is one numbered page of the log and where it sits among the rest --
// what a screen with page numbers draws.
//
// Paginate fills every field. SimplePaginate counts nothing, so it leaves
// Pages and Total at zero and says only whether a page follows.
type Page struct {
	Items              []*Activity
	Page, Pages, Total int
	// HasMore says a page follows this one.
	HasMore bool
}

// Paginate reads one numbered page of the log, newest first: Eloquent's
// paginate, which Spatie's activity queries end in. A page past the last is
// the last.
func (s *ActivityService) Paginate(ctx context.Context, actor security.Subject, filter Filter, page, perPage int) (Page, error) {
	g, err := security.Authorize(ctx, s.policy, actor, ActivityView, Activity{})
	if err != nil {
		return Page{}, err
	}
	switch {
	case perPage <= 0:
		perPage = defaultLimit
	case perPage > maxLimit:
		perPage = maxLimit
	}
	total, err := filtered(Activities(s.db), filter, s.cfg.CauserType).Count(ctx, g)
	if err != nil {
		return Page{}, err
	}
	pages := int((total + int64(perPage) - 1) / int64(perPage))
	if pages < 1 {
		pages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	items, err := filtered(Activities(s.db), filter, s.cfg.CauserType).OrderByDesc("created_at").OrderByDesc("id").
		Offset((page-1)*perPage).Limit(perPage).Get(ctx, g)
	if err != nil {
		return Page{}, err
	}
	return Page{Items: items, Page: page, Pages: pages, Total: int(total), HasMore: page < pages}, nil
}

// SimplePaginate reads one numbered page of the log, newest first, without
// counting the log: Eloquent's simplePaginate. It reads one entry past the
// page to say whether another follows, and leaves Pages and Total at zero.
//
// Paginate counts every entry the filter keeps, on every page, and that count
// grows with the log; this one costs what the page costs. A page past the last
// is empty rather than the last, since nothing says where the last is.
// List, which pages by a cursor rather than an offset, costs the same on the
// thousandth page as on the first.
func (s *ActivityService) SimplePaginate(ctx context.Context, actor security.Subject, filter Filter, page, perPage int) (Page, error) {
	g, err := security.Authorize(ctx, s.policy, actor, ActivityView, Activity{})
	if err != nil {
		return Page{}, err
	}
	switch {
	case perPage <= 0:
		perPage = defaultLimit
	case perPage > maxLimit:
		perPage = maxLimit
	}
	if page < 1 {
		page = 1
	}
	items, err := filtered(Activities(s.db), filter, s.cfg.CauserType).OrderByDesc("created_at").OrderByDesc("id").
		Offset((page-1)*perPage).Limit(perPage+1).Get(ctx, g)
	if err != nil {
		return Page{}, err
	}
	more := len(items) > perPage
	if more {
		items = items[:perPage]
	}
	return Page{Items: items, Page: page, HasMore: more}, nil
}

// ForSubject pages through the entries about a record, newest first --
// Spatie's activitiesAsSubject.
func (s *ActivityService) ForSubject(ctx context.Context, actor security.Subject, record Record, q data.Query) ([]*Activity, error) {
	return s.List(ctx, actor, Filter{Subject: RefOf(record)}, q)
}

// CausedBy pages through the entries a record caused, newest first --
// Spatie's activitiesAsCauser.
func (s *ActivityService) CausedBy(ctx context.Context, actor security.Subject, record Record, q data.Query) ([]*Activity, error) {
	return s.List(ctx, actor, Filter{Causer: RefOf(record)}, q)
}

// Count is how many entries a filter keeps.
func (s *ActivityService) Count(ctx context.Context, actor security.Subject, filter Filter) (int64, error) {
	g, err := security.Authorize(ctx, s.policy, actor, ActivityView, Activity{})
	if err != nil {
		return 0, err
	}
	return filtered(Activities(s.db), filter, s.cfg.CauserType).Count(ctx, g)
}

func filtered(query *ActivityQuery, filter Filter, causerType string) *ActivityQuery {
	narrow(query.Base(), filter, causerType)
	return query
}

// narrow adds what a Filter keeps to a query's core builder: a tenant's own
// listing, or ReadAll's. It is written on the builder so both read one filter.
func narrow(query *model.Builder, filter Filter, causerType string) {
	if len(filter.LogNames) > 0 {
		names := make([]any, 0, len(filter.LogNames))
		for _, name := range filter.LogNames {
			names = append(names, name)
		}
		query.WhereIn("log_name", names)
	}
	if filter.Event != "" {
		query.Where("event", "=", filter.Event)
	}
	if filter.Subject.Type != "" {
		query.Where("subject_type", "=", filter.Subject.Type)
	}
	if filter.Subject.ID != "" {
		query.Where("subject_id", "=", filter.Subject.ID)
	}
	if filter.Causer.Type != "" {
		query.Where("causer_type", "=", filter.Causer.Type)
	}
	if filter.Causer.ID != "" {
		query.Where("causer_id", "=", filter.Causer.ID)
	}
	if involves := filter.Involves; involves.ID != "" {
		query.Where(func(either *model.Builder) {
			either.Where(func(caused *model.Builder) {
				if involves.Type != "" {
					caused.Where("causer_type", "=", involves.Type)
				}
				caused.Where("causer_id", "=", involves.ID)
			})
			if involves.Type == "" || involves.Type == causerType {
				either.OrWhere("on_behalf_of", "=", involves.ID)
			}
		})
	}
	if !filter.Since.IsZero() {
		query.Where("created_at", ">=", filter.Since.UTC())
	}
	if !filter.Until.IsZero() {
		query.Where("created_at", "<", filter.Until.UTC())
	}
	if search := strings.TrimSpace(filter.Search); search != "" {
		query.Where("description", "like", "%"+escapeLike(search)+"%")
	}
}

// olderThan keeps what comes after the entry at anchor, id in the order every
// listing pages in: newest first, by when, then by identifier.
func olderThan(query *model.Builder, anchor any, id string) {
	query.Where(func(before *model.Builder) {
		before.Where("created_at", "<", anchor).
			OrWhere(func(same *model.Builder) {
				same.Where("created_at", "=", anchor).Where("id", "<", id)
			})
	})
}

// escapeLike keeps a search for a literal % or _ from matching everything.
func escapeLike(value string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(value)
}

// Clean removes the entries older than days -- the configured CleanAfterDays
// when days is zero -- from one log, or from every log when logName is empty:
// Spatie's activitylog:clean. It answers how many were removed.
func (s *ActivityService) Clean(ctx context.Context, actor security.Subject, days int, logName string) (int64, error) {
	g, err := security.Authorize(ctx, s.policy, actor, ActivityClean, Activity{})
	if err != nil {
		return 0, err
	}
	return s.clean(ctx, g, days, logName, time.Now())
}

// cutoffFor is the instant before which Clean removes entries.
func cutoffFor(s *ActivityService, days int) time.Time {
	if days == 0 {
		days = s.cfg.CleanAfterDays
	}
	return time.Now().UTC().AddDate(0, 0, -days)
}

// clean is Clean under a Grant already obtained -- the scheduler's, for the
// scheduled run.
func (s *ActivityService) clean(ctx context.Context, g security.Grant, days int, logName string, now time.Time) (int64, error) {
	if days == 0 {
		days = s.cfg.CleanAfterDays
	}
	if days < 1 {
		return 0, fmt.Errorf("activitylog: the days option must be a positive integer")
	}
	cutoff := now.UTC().AddDate(0, 0, -days)
	query := Activities(s.db).Where("created_at", "<", cutoff)
	if logName != "" {
		query = query.Where("log_name", "=", logName)
	}
	return query.Delete(ctx, g)
}
