package feature_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/foundation"
	fhttp "github.com/arandu-io/framework/http"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database"
	"github.com/arandu-io/hesape/database/migrations"

	activitylog "github.com/hyz-is/arandu-activitylog"
)

// The shipped policy refuses every read and every clean: the log is opened by
// the application's own policy, not by installing the package.
func TestTheShippedPolicyRefusesEveryAction(t *testing.T) {
	db := openDatabase(t)
	svc, err := activitylog.NewActivityService(db, activitylog.Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	actor := security.Subject{ID: "root", Tenant: "acme", Roles: []string{"admin"}, Verified: true}
	if _, err := svc.List(ctx, actor, activitylog.Filter{}, data.Query{}); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("List: %v", err)
	}
	if _, err := svc.Find(ctx, actor, "x"); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("Find: %v", err)
	}
	if _, err := svc.Paginate(ctx, actor, activitylog.Filter{}, 1, 10); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("Paginate: %v", err)
	}
	if _, err := svc.SimplePaginate(ctx, actor, activitylog.Filter{}, 1, 10); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("SimplePaginate: %v", err)
	}
	if _, err := svc.Count(ctx, actor, activitylog.Filter{}); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("Count: %v", err)
	}
	if _, err := svc.Clean(ctx, actor, 1, ""); !errors.Is(err, security.ErrForbidden) {
		t.Errorf("Clean: %v", err)
	}
}

// The filters are Spatie's scopes -- inLog, causedBy, forSubject, forEvent --
// and a window of time; pages run newest first by cursor; another tenant's
// entry is not found.
func TestTheLogIsReadByItsScopesAndPagedNewestFirst(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	g := grantOf(t, member("acme"))
	old := time.Now().Add(-48 * time.Hour)

	for i, spec := range []struct{ log, event, subject string }{
		{"default", "created", "a1"}, {"billing", "paid", "a2"}, {"billing", "refunded", "a2"}, {"default", "updated", "a1"},
	} {
		chain := logger.In(ctx, g, spec.log).Event(spec.event).OnRef(activitylog.Ref{Type: "invoice", ID: spec.subject}).WithProperty("n", i)
		if i == 0 {
			chain = chain.CreatedAt(old)
		}
		if _, err := chain.Log("entry " + spec.event); err != nil {
			t.Fatal(err)
		}
	}
	foreign, err := logger.Activity(ctx, grantOf(t, member("globex"))).Log("foreign")
	if err != nil {
		t.Fatal(err)
	}

	check := func(name string, filter activitylog.Filter, want int) {
		t.Helper()
		if got := entries(t, svc, "acme", filter); len(got) != want {
			t.Errorf("%s kept %d entries, want %d", name, len(got), want)
		}
	}
	check("everything", activitylog.Filter{}, 4)
	check("inLog", activitylog.Filter{LogNames: []string{"billing"}}, 2)
	check("forEvent", activitylog.Filter{Event: "paid"}, 1)
	check("forSubject", activitylog.Filter{Subject: activitylog.Ref{Type: "invoice", ID: "a1"}}, 2)
	check("causedBy", activitylog.Filter{Causer: activitylog.Ref{Type: "user", ID: "user-acme"}}, 4)
	check("since", activitylog.Filter{Since: time.Now().Add(-time.Hour)}, 3)
	check("search", activitylog.Filter{Search: "refund"}, 1)

	first, err := svc.List(ctx, member("acme"), activitylog.Filter{}, data.Query{Limit: 2})
	if err != nil || len(first) != 2 {
		t.Fatalf("first page: %v %v", len(first), err)
	}
	second, err := svc.List(ctx, member("acme"), activitylog.Filter{}, data.Query{Limit: 2, Cursor: first[1].ID})
	if err != nil || len(second) != 2 || second[0].ID >= first[1].ID {
		t.Fatalf("second page does not follow the first")
	}
	if *first[0].Event != "updated" {
		t.Errorf("the newest entry does not come first: %s", *first[0].Event)
	}

	numbered, err := svc.Paginate(ctx, member("acme"), activitylog.Filter{}, 2, 3)
	if err != nil || numbered.Total != 4 || numbered.Pages != 2 || numbered.Page != 2 || len(numbered.Items) != 1 || *numbered.Items[0].Event != "created" {
		t.Errorf("numbered page = %+v (%v)", numbered, err)
	}
	if past, _ := svc.Paginate(ctx, member("acme"), activitylog.Filter{}, 9, 3); past.Page != 2 {
		t.Errorf("a page past the last answered page %d", past.Page)
	}
	if numbered.HasMore {
		t.Errorf("the last numbered page says HasMore %v", numbered.HasMore)
	}
	if head, _ := svc.Paginate(ctx, member("acme"), activitylog.Filter{}, 1, 3); !head.HasMore {
		t.Error("the first of two numbered pages says nothing follows")
	}

	// Without the count: the same entries, and only whether a page follows.
	simple, err := svc.SimplePaginate(ctx, member("acme"), activitylog.Filter{}, 1, 3)
	if err != nil || len(simple.Items) != 3 || !simple.HasMore || simple.Total != 0 || simple.Pages != 0 || simple.Page != 1 {
		t.Errorf("simple first page = %+v (%v)", simple, err)
	}
	last, err := svc.SimplePaginate(ctx, member("acme"), activitylog.Filter{}, 2, 3)
	if err != nil || len(last.Items) != 1 || last.HasMore || last.Items[0].ID != numbered.Items[0].ID {
		t.Errorf("simple last page = %+v (%v)", last, err)
	}
	if exact, _ := svc.SimplePaginate(ctx, member("acme"), activitylog.Filter{}, 1, 4); exact.HasMore || len(exact.Items) != 4 {
		t.Errorf("a page holding exactly the rest says HasMore %v with %d", exact.HasMore, len(exact.Items))
	}
	if past, _ := svc.SimplePaginate(ctx, member("acme"), activitylog.Filter{}, 9, 3); len(past.Items) != 0 || past.HasMore {
		t.Errorf("a simple page past the last = %+v", past)
	}
	if other, _ := svc.SimplePaginate(ctx, member("globex"), activitylog.Filter{}, 1, 50); len(other.Items) != 1 {
		t.Errorf("another tenant's simple page holds %d entries", len(other.Items))
	}
	if _, err := svc.Find(ctx, member("acme"), foreign.ID); !errors.Is(err, activitylog.ErrNotFound) {
		t.Errorf("another tenant's entry: %v, want ErrNotFound", err)
	}
	if found, err := svc.Find(ctx, member("globex"), foreign.ID); err != nil || found.Description != "foreign" {
		t.Errorf("own entry: %v", err)
	}
}

// The clean removes what is older than the days given, of one log or of all,
// in the cleaning tenant only.
func TestTheCleanRemovesOldEntries(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{CleanAfterDays: 30})
	ctx := context.Background()
	acme, globex := grantOf(t, member("acme")), grantOf(t, member("globex"))
	ancient := time.Now().AddDate(0, 0, -400)
	for _, entry := range []struct {
		g   security.Grant
		log string
		at  time.Time
	}{
		{acme, "default", ancient}, {acme, "billing", ancient}, {acme, "default", time.Now().AddDate(0, 0, -40)},
		{acme, "default", time.Now()}, {globex, "default", ancient},
	} {
		if _, err := logger.In(ctx, entry.g, entry.log).CreatedAt(entry.at).Log("old"); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := svc.Clean(ctx, member("acme"), 365, "billing")
	if err != nil || removed != 1 {
		t.Fatalf("cleaning one log removed %d (%v), want 1", removed, err)
	}
	removed, err = svc.Clean(ctx, member("acme"), 0, "")
	if err != nil || removed != 2 {
		t.Fatalf("cleaning by the configured 30 days removed %d (%v), want 2", removed, err)
	}
	if left := entries(t, svc, "acme", activitylog.Filter{}); len(left) != 1 {
		t.Errorf("%d entries left, want the recent one", len(left))
	}
	if left := entries(t, svc, "globex", activitylog.Filter{}); len(left) != 1 {
		t.Errorf("the clean reached another tenant")
	}
	if _, err := svc.Clean(ctx, member("acme"), -1, ""); err == nil {
		t.Error("a negative number of days was accepted")
	}
}

// The module declares its table, its daily clean per tenant, and two named
// read routes under its prefix.
func TestTheModuleDeclaresItsTableScheduleAndRoutes(t *testing.T) {
	db := openDatabase(t)
	sessions := security.NewSessionStore([]byte("0123456789abcdef0123456789abcdef"), time.Hour, false, security.NewMemoryBackend())
	module, err := activitylog.New(activitylog.Config{Tenant: "acme", Prefix: "/admin/activity"}, db, sessions)
	if err != nil {
		t.Fatal(err)
	}
	if got := module.Migrations(); len(got) != 2 || got[0].GetName() >= got[1].GetName() {
		t.Error("the module does not declare its table and the on_behalf_of column, in order")
	}
	tasks := module.Schedule()
	if len(tasks) != 1 || tasks[0].Scope != foundation.PerTenant || tasks[0].Action != activitylog.ActivityClean || tasks[0].Spec != activitylog.DefaultCleanSpec {
		t.Errorf("schedule = %+v", tasks)
	}
	fixed, _ := activitylog.New(activitylog.Config{CleanTenants: []string{"platform"}}, db, nil)
	if tasks := fixed.Schedule(); len(tasks) != 2 || tasks[1].Scope != foundation.Global {
		t.Errorf("CleanTenants did not add the fixed clean: %+v", tasks)
	} else {
		logger := newLogger(t, db, activitylog.Config{})
		if _, err := logger.Activity(context.Background(), grantOf(t, member("platform"))).CreatedAt(time.Now().AddDate(-2, 0, 0)).Log("old mail"); err != nil {
			t.Fatal(err)
		}
		if err := tasks[1].Run(context.Background(), security.Grant{}); err != nil {
			t.Fatal(err)
		}
		if left := entries(t, newService(t, db, activitylog.Config{}), "platform", activitylog.Filter{}); len(left) != 0 {
			t.Errorf("the fixed clean left %d old entries", len(left))
		}
	}
	if _, err := activitylog.New(activitylog.Config{CleanTenants: []string{"Bad Tenant"}}, db, nil); err == nil {
		t.Error("an invalid clean tenant was accepted")
	}
	none, _ := activitylog.New(activitylog.Config{CleanSpec: "-"}, db, nil)
	if len(none.Schedule()) != 0 {
		t.Error("CleanSpec \"-\" still scheduled a clean")
	}

	router := fhttp.NewRouter()
	module.Routes(router.ForModule(module.Name()))
	got := map[string]bool{}
	for _, route := range router.Routes() {
		if route.Module != "activitylog" {
			t.Errorf("the route %s %s is not tagged with the module name", route.Method, route.Pattern)
		}
		got[route.Method+" "+route.Pattern] = true
	}
	if len(got) != 2 || !got["GET /admin/activity"] || !got["GET /admin/activity/{id}"] {
		t.Errorf("routes = %v", got)
	}

	if _, err := activitylog.New(activitylog.Config{PageSize: activitylog.MaxPageSize + 1}, db, nil); err == nil {
		t.Error("a page size above the ceiling was accepted")
	}
	if _, err := activitylog.New(activitylog.Config{Prefix: "no-slash"}, db, nil); err == nil {
		t.Error("a prefix without a slash was accepted")
	}
}

// The on_behalf_of migration goes down and up again over a table holding
// entries, and the entries survive it.
func TestTheOnBehalfOfColumnMigratesDownAndUp(t *testing.T) {
	name := t.Name()
	raw, err := sql.Open("sqlite", "file:"+name+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	raw.SetMaxOpenConns(1)
	conn := database.ForMigrations(database.NewConnection(raw, "", "", map[string]any{"driver": string(database.DialectSQLite), "name": name}))
	ctx := context.Background()
	all := (&activitylog.Module{}).Migrations()
	if err := all[0].Up(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`insert into activity_log (id, tenant_id, description, created_at, updated_at) values ('a-1', 'acme', 'before', '2026-01-01 00:00:00', '2026-01-01 00:00:00')`); err != nil {
		t.Fatal(err)
	}
	column := all[1].(migrations.ReversibleMigration)
	for _, step := range []func(context.Context, migrations.Connection) error{column.Up, column.Down, column.Up} {
		if err := step(ctx, conn); err != nil {
			t.Fatal(err)
		}
	}
	if has, err := conn.Schema().HasColumn(ctx, activitylog.ActivityTable, "on_behalf_of"); err != nil || !has {
		t.Fatalf("the column is missing after up: %v, %v", has, err)
	}
	var kept int
	if err := raw.QueryRow(`select count(*) from activity_log where on_behalf_of is null`).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("the entry written before the column holds %d, %v", kept, err)
	}
}
