package feature_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/database/model"
	"github.com/arandu-io/hesape/database/schema"

	activitylog "github.com/hyz-is/arandu-activitylog"

	_ "modernc.org/sqlite"
)

// article is a row of the application's, the subject the tests log about.
type article struct {
	model.Model

	ID        string     `db:"id"`
	TenantID  string     `db:"tenant_id"`
	Title     string     `db:"title"`
	Body      string     `db:"body"`
	Secret    string     `db:"secret"`
	Meta      string     `db:"meta"`
	AuthorID  string     `db:"author_id"`
	CreatedAt time.Time  `db:"created_at"`
	UpdatedAt time.Time  `db:"updated_at"`
	DeletedAt *time.Time `db:"deleted_at"`
}

var articleTable = model.NewTable(model.TableSpec{
	Name:        "articles",
	New:         func() model.Entity { return new(article) },
	UniqueIDs:   true,
	SoftDeletes: true,
})

// articleOptions is what every article answers ActivityLogOptions with; a
// test sets it and the cleanup puts the default back.
var (
	articleOptions = activitylog.DefaultLogOptions().LogAll()
	articleEvents  []string
	articleHook    func(a *activitylog.Activity, event string)
	articleMuted   bool
)

func (a *article) ActivityLogOptions() activitylog.LogOptions { return articleOptions }

// Fresh is narrowed to the model's own type, as aru model:build generates it:
// the logger must not depend on the promoted one.
func (a *article) Fresh(ctx context.Context, g security.Grant, with ...string) (*article, error) {
	fresh, err := a.Model.Fresh(ctx, g, with...)
	if err != nil || fresh == nil {
		return nil, err
	}
	return fresh.(*article), nil
}

func (a *article) ActivityEvents() []string {
	if articleEvents == nil {
		return activitylog.DefaultEvents
	}
	return articleEvents
}

func (a *article) BeforeActivityLogged(activity *activitylog.Activity, event string) {
	if articleHook != nil {
		articleHook(activity, event)
	}
}

func (a *article) ActivityLoggingDisabled() bool { return articleMuted }

// author is a causer with a name the placeholders read.
type author struct {
	model.Model

	ID       string `db:"id"`
	TenantID string `db:"tenant_id"`
	Name     string `db:"name"`
}

var authorTable = model.NewTable(model.TableSpec{
	Name:         "authors",
	New:          func() model.Entity { return new(author) },
	UniqueIDs:    true,
	NoTimestamps: true,
})

func init() {
	articleTable.Relate("author", func(m *model.Model) model.Relation {
		return model.BelongsTo(m, authorTable, "author_id", "id", "author")
	})
}

func (a *author) ActivityType() string { return "author" }

// openPolicy allows everything, so the tests reach the rows; the tenant filter
// the statements carry is what keeps tenants apart.
type openPolicy struct{}

func (openPolicy) Can(context.Context, security.Subject, security.Action, activitylog.Activity) error {
	return nil
}

type articlePolicy struct{}

func (articlePolicy) Can(context.Context, security.Subject, security.Action, article) error {
	return nil
}

func setOptions(t *testing.T, options activitylog.LogOptions) {
	t.Helper()
	articleOptions = options
	t.Cleanup(func() {
		articleOptions = activitylog.DefaultLogOptions().LogAll()
		articleEvents, articleHook, articleMuted = nil, nil, false
	})
}

// openDatabase returns a migrated, empty database under its own name.
func openDatabase(t *testing.T) *data.DB {
	t.Helper()
	name := t.Name()
	raw, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	conn := database.ForMigrations(database.NewConnection(raw, "", "", map[string]any{"driver": string(database.DialectSQLite), "name": name}))
	ctx := context.Background()
	for _, migration := range (&activitylog.Module{}).Migrations() {
		if err := migration.Up(ctx, conn); err != nil {
			t.Fatalf("applying %s: %v", migration.GetName(), err)
		}
	}
	if err := conn.Schema().Create(ctx, "articles", func(table *schema.Blueprint) {
		table.String("id").Primary()
		table.String("tenant_id")
		table.String("title")
		table.Text("body")
		table.String("secret")
		table.Text("meta")
		table.String("author_id").Default("")
		table.Timestamp("created_at")
		table.Timestamp("updated_at")
		table.Timestamp("deleted_at").Nullable()
	}); err != nil {
		t.Fatal(err)
	}
	if err := conn.Schema().Create(ctx, "authors", func(table *schema.Blueprint) {
		table.String("id").Primary()
		table.String("tenant_id")
		table.String("name")
	}); err != nil {
		t.Fatal(err)
	}
	return data.Wrap(raw, data.DialectSQLite)
}

// member is a subject of one customer.
func member(tenant string) security.Subject {
	return security.Subject{ID: "user-" + tenant, Tenant: tenant, Roles: []string{"member"}, Verified: true}
}

// grantOf is the Grant an application's own action was authorized with.
func grantOf(t *testing.T, subject security.Subject) security.Grant {
	t.Helper()
	g, err := security.Authorize(context.Background(), articlePolicy{}, subject, "article.update", article{})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func newLogger(t *testing.T, db *data.DB, cfg activitylog.Config) *activitylog.Logger {
	t.Helper()
	logger, err := activitylog.NewLogger(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return logger
}

func newService(t *testing.T, db *data.DB, cfg activitylog.Config) *activitylog.ActivityService {
	t.Helper()
	if cfg.Policy == nil {
		cfg.Policy = openPolicy{}
	}
	svc, err := activitylog.NewActivityService(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func newArticle(t *testing.T, db *data.DB, title string) *article {
	t.Helper()
	return articleTable.New(db).(*article).with(title)
}

func (a *article) with(title string) *article {
	a.Title, a.Body, a.Secret, a.Meta = title, "body of "+title, "s3cr3t", `{"color":"red","size":{"w":2}}`
	return a
}

// entries reads every entry of a tenant, oldest first.
func entries(t *testing.T, svc *activitylog.ActivityService, tenant string, filter activitylog.Filter) []*activitylog.Activity {
	t.Helper()
	records, err := svc.List(context.Background(), member(tenant), filter, data.Query{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for i, j := 0, len(records)-1; i < j; i, j = i+1, j-1 {
		records[i], records[j] = records[j], records[i]
	}
	return records
}
