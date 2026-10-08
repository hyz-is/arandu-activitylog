// Package activitylog is an Arandu module: the activity log of an application,
// on the pattern of spatie/laravel-activitylog v5, adapted to Arandu.
//
// The files are laid out by role:
//
//	module.go   -> registration, read routes, migration and scheduled clean
//	config.go   -> what the application passes in
//	model.go    -> the entry, its changes and its properties
//	logger.go   -> recording: the fluent chain, placeholders, context scopes
//	record.go   -> logging a record's changes, and the options that shape it
//	values.go   -> what a value becomes in the log
//	policy.go   -> who may read and clean
//	service.go  -> reading and cleaning, after the policy
//	commands.go -> what an operator runs from a terminal
//	bridges.go  -> the domain events and the mail an application already has
//
// # What is Spatie's, and what is Arandu's
//
// Everything Spatie v5 records, this records, in the same shape: a log name, a
// description with :subject, :causer and :properties placeholders, a subject
// and a causer, an event, the record's change as attribute_changes
// ({attributes, old}) and free properties. LogOptions are Spatie's: logAll,
// logOnly with relation and JSON paths, logExcept, logOnlyDirty,
// dontLogIfAttributesChangedOnly, dontLogEmptyChanges, useLogName,
// setDescriptionForEvent and useAttributeRawValues; a record may also choose
// its events, adjust each entry about it and turn its own logging off. The
// beforeLogging hooks, transformChanges, withoutLogging, defaultCauser, the
// causer resolver, the buffer and the clean are here too.
//
// Three things differ, each because Arandu asks it:
//
//   - The tenant. Every entry carries the tenant of the Grant it was recorded
//     under, data.Tenant(g), and every read is scoped by the Grant the policy
//     issued. Nothing a caller names chooses whose log an entry lands in.
//   - The trigger. Spatie hooks Eloquent's events; a Hesape model event hands a
//     callback the row and neither the context nor the Grant of the write, so a
//     hook could not write in the same transaction and tenant without hidden
//     state. A record's change is logged by saving it through the Logger --
//     logger.Save(ctx, g, record) in place of record.Save(ctx, g) -- which
//     writes the row and its entry in one transaction.
//   - The scopes. Spatie's request-scoped switches and default causer are, in
//     Go, values the context carries: WithoutLogging, WithCauser, Buffered.
package activitylog

import (
	"context"
	"errors"
	stdhttp "net/http"
	"strings"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database/migrations"
	"github.com/arandu-io/hesape/database/schema"
)

// Module is what the application registers.
type Module struct {
	cfg      Config
	logger   *Logger
	svc      *ActivityService
	sessions *security.SessionStore
}

// Compile-time proof that the module honors the contracts it claims.
var (
	_ foundation.Module      = (*Module)(nil)
	_ foundation.Migratable  = (*Module)(nil)
	_ foundation.Schedulable = (*Module)(nil)
)

// New returns the module, or the reason it cannot be built. sessions is where
// the read routes take their subject from; it may be nil for an application
// that mounts no route of this package.
func New(cfg Config, db *data.DB, sessions *security.SessionStore) (*Module, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if db == nil {
		return nil, errors.New("activitylog: New needs a database handle: this package owns a table")
	}
	logger, err := NewLogger(db, cfg)
	if err != nil {
		return nil, err
	}
	svc, err := NewActivityService(db, cfg)
	if err != nil {
		return nil, err
	}
	return &Module{cfg: cfg.withDefaults(), logger: logger, svc: svc, sessions: sessions}, nil
}

// Logger records entries. It is the one Logger of the module, so an entry
// recorded by code and one read through a route are the same rows.
func (m *Module) Logger() *Logger { return m.logger }

// Service reads and cleans the log.
func (m *Module) Service() *ActivityService { return m.svc }

// Name is the module identifier.
func (m *Module) Name() string { return "activitylog" }

// Routes registers the read routes under the configured prefix: the log, one
// entry. They answer JSON, and the policy decides who may read.
func (m *Module) Routes(r *fhttp.Router) {
	if m.sessions == nil {
		return
	}
	handlers := map[string]func(*fhttp.Context) error{
		"activitylog.index": m.index,
		"activitylog.show":  m.show,
	}
	for _, route := range routePatterns(m.cfg.Prefix) {
		r.Action(route.method, route.pattern, handlers[route.name]).Name(route.name)
	}
}

func (m *Module) index(ctx *fhttp.Context) error {
	filter := Filter{
		Event:   ctx.Query("event"),
		Subject: Ref{Type: ctx.Query("subject_type"), ID: ctx.Query("subject_id")},
		Causer:  Ref{Type: ctx.Query("causer_type"), ID: ctx.Query("causer_id")},
		Search:  ctx.Query("q"),
	}
	if names := strings.TrimSpace(ctx.Query("log")); names != "" {
		filter.LogNames = strings.Split(names, ",")
	}
	records, err := m.svc.List(ctx.Ctx(), m.subject(ctx.Request), filter, data.Query{Cursor: ctx.Query("cursor"), Limit: m.cfg.PageSize})
	if err != nil {
		return m.answer(ctx, err)
	}
	cursor := ""
	if len(records) == m.cfg.PageSize {
		cursor = records[len(records)-1].ID
	}
	return ctx.JSON(stdhttp.StatusOK, newCollection(records, cursor))
}

func (m *Module) show(ctx *fhttp.Context) error {
	record, err := m.svc.Find(ctx.Ctx(), m.subject(ctx.Request), ctx.Param("id"))
	if err != nil {
		return m.answer(ctx, err)
	}
	return ctx.JSON(stdhttp.StatusOK, NewResource(record))
}

// subject is who is reading: the session's subject, or a guest of the
// configured tenant when there is none.
func (m *Module) subject(r *stdhttp.Request) security.Subject {
	sub, err := m.sessions.Load(r.Context(), r)
	if err != nil || sub.ID == "" {
		return security.Guest(m.cfg.Tenant)
	}
	return sub
}

// answer turns a refusal into a status with no detail; anything else is
// returned for the framework to answer.
func (m *Module) answer(ctx *fhttp.Context, err error) error {
	switch {
	case errors.Is(err, security.ErrForbidden):
		fhttp.Refuse(ctx.Response, ctx.Request, stdhttp.StatusForbidden, "forbidden")
		return nil
	case errors.Is(err, ErrNotFound):
		fhttp.Refuse(ctx.Response, ctx.Request, stdhttp.StatusNotFound, "not found")
		return nil
	}
	return err
}

// Resource is the list of fields one entry answers with. The tenant is not one
// of them: it names a customer and belongs in no response.
type Resource struct {
	activity *Activity
}

// NewResource snapshots one entry for the response.
func NewResource(record *Activity) Resource { return Resource{activity: record} }

// ToArray returns the fields that may leave, by name.
func (r Resource) ToArray() map[string]any {
	a := r.activity
	if a == nil {
		return map[string]any{}
	}
	changes := a.Changes()
	return map[string]any{
		"id":           a.ID,
		"log_name":     text(a.LogName),
		"description":  a.Description,
		"event":        text(a.Event),
		"subject_type": text(a.SubjectType),
		"subject_id":   text(a.SubjectID),
		"causer_type":  text(a.CauserType),
		"causer_id":    text(a.CauserID),
		"attribute_changes": map[string]any{
			"attributes": changes.Attributes,
			"old":        changes.Old,
		},
		"properties": a.Properties(),
		"created_at": a.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// With is the framework's envelope hook; a resource adds nothing to it.
func (r Resource) With() map[string]any { return nil }

// Collection is one page of entries, and the cursor of the next page.
type Collection struct {
	items  []Resource
	cursor string
}

func newCollection(records []*Activity, cursor string) Collection {
	items := make([]Resource, 0, len(records))
	for _, record := range records {
		items = append(items, NewResource(record))
	}
	return Collection{items: items, cursor: cursor}
}

// ToArray returns the page.
func (c Collection) ToArray() map[string]any {
	items := make([]map[string]any, 0, len(c.items))
	for _, item := range c.items {
		items = append(items, item.ToArray())
	}
	return map[string]any{"items": items, "next_cursor": c.cursor}
}

// With is the framework's envelope hook; a collection adds nothing to it.
func (c Collection) With() map[string]any { return nil }

// Schedule declares the daily clean, per tenant, under the clean action:
// Spatie's scheduled activitylog:clean. CleanSpec "-" declares none. The
// tenants in CleanTenants are cleaned by a second task, once each.
func (m *Module) Schedule() []foundation.Task {
	if m.cfg.CleanSpec == "-" {
		return nil
	}
	tasks := []foundation.Task{{
		ID:        "activitylog.clean",
		Spec:      m.cfg.CleanSpec,
		Scope:     foundation.PerTenant,
		Timeout:   10 * time.Minute,
		Singleton: true,
		Action:    ActivityClean,
		Run: func(ctx context.Context, g security.Grant) error {
			_, err := m.svc.clean(ctx, g, 0, "", time.Now())
			return err
		},
	}}
	if len(m.cfg.CleanTenants) > 0 {
		tenants := append([]string(nil), m.cfg.CleanTenants...)
		tasks = append(tasks, foundation.Task{
			ID:        "activitylog.clean.fixed",
			Spec:      m.cfg.CleanSpec,
			Scope:     foundation.Global,
			Timeout:   10 * time.Minute,
			Singleton: true,
			Action:    ActivityClean,
			Run: func(ctx context.Context, _ security.Grant) error {
				for _, tenant := range tenants {
					//arandu:system-grant the application named this tenant in Config.CleanTenants; the clean removes only its old entries.
					g := security.SystemGrant(ActivityClean, tenant)
					if _, err := m.svc.clean(ctx, g, 0, "", time.Now()); err != nil {
						return err
					}
				}
				return nil
			},
		})
	}
	return tasks
}

// Migrations declares the schema this module owns.
func (m *Module) Migrations() []foundation.Migration {
	return []foundation.Migration{createActivityLogTable{}}
}

var _ migrations.ReversibleMigration = createActivityLogTable{}

// createActivityLogTable is Spatie v5's activity_log, with the tenant every
// Arandu table carries. The identifiers are text, because Arandu's keys are
// application-generated UUIDs; the two JSON columns are text, which every
// engine stores the same way.
type createActivityLogTable struct{ migrations.BaseMigration }

// GetName is the migration's identity, and it carries the order. It is fixed
// once the package is published.
func (createActivityLogTable) GetName() string { return "20261008_0001_create_activity_log_table" }

// Up creates the table and the indexes its scopes read by.
func (createActivityLogTable) Up(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Create(ctx, ActivityTable, func(table *schema.Blueprint) {
		table.String("id").Primary()
		table.String("tenant_id")
		table.String("log_name").Nullable()
		table.Text("description")
		table.String("subject_type").Nullable()
		table.String("subject_id").Nullable()
		table.String("event").Nullable()
		table.String("causer_type").Nullable()
		table.String("causer_id").Nullable()
		table.Text("attribute_changes").Nullable()
		table.Text("properties").Nullable()
		table.Timestamp("created_at")
		table.Timestamp("updated_at")
		table.Index([]string{"tenant_id", "log_name"}, "activity_log_tenant_log_index")
		table.Index([]string{"tenant_id", "subject_type", "subject_id"}, "activity_log_tenant_subject_index")
		table.Index([]string{"tenant_id", "causer_type", "causer_id"}, "activity_log_tenant_causer_index")
		table.Index([]string{"tenant_id", "created_at"}, "activity_log_tenant_created_index")
	})
}

// Down drops it.
func (createActivityLogTable) Down(ctx context.Context, conn migrations.Connection) error {
	return conn.Schema().Drop(ctx, ActivityTable)
}
