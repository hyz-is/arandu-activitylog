package feature_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/console"

	activitylog "github.com/hyz-is/arandu-activitylog"
)

// The pending entry is Spatie's: a tap runs at once and a method after it
// overrides it; a description a tap set still has its placeholders replaced;
// the placeholder base is matched exactly; an absent causer keeps its token;
// When's default branch runs when the condition does not hold.
func TestThePendingEntryBehavesAsSpatiesDoes(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	ctx := context.Background()
	g := grantOf(t, member("acme"))

	entry, err := logger.Activity(ctx, g).Tap(func(a *activitylog.Activity, _ string) {
		_ = a.SetProperty("from", "tap")
		a.Description = "tapped :properties.from"
	}).WithProperty("from", "chain").Log("ignored")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Description != "tapped chain" {
		t.Errorf("description = %q: a method after the tap must override it, and a tap's description is a template", entry.Description)
	}

	anonymous, _ := logger.Activity(ctx, g).ByAnonymous().Log(":causer.id did it; :Subject.title stays")
	if anonymous.Description != ":causer.id did it; :Subject.title stays" {
		t.Errorf("description = %q", anonymous.Description)
	}

	chosen, _ := logger.Activity(ctx, g).When(false, func(p *activitylog.PendingActivity) *activitylog.PendingActivity {
		return p.Event("yes")
	}, func(p *activitylog.PendingActivity) *activitylog.PendingActivity {
		return p.Event("no")
	}).Log("conditional")
	if *chosen.Event != "no" {
		t.Errorf("event = %v: the default branch did not run", *chosen.Event)
	}

	noLog, _ := logger.Activity(ctx, g).InLog("").Log("no log")
	if noLog.LogName != nil {
		t.Errorf("useLog(null) kept a log name: %v", *noLog.LogName)
	}
}

// Updates read relations before and after: an unchanged relation is not
// reported as changed with LogOnlyDirty, a missing JSON path is a nested null,
// a missing name is null, dates are written as Laravel writes them, a restore
// by save is not an update, and dontLogIfAttributesChangedOnly also holds a
// creation back.
func TestRecordChangesMatchSpatie(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	g := grantOf(t, member("acme"))

	setOptions(t, activitylog.DefaultLogOptions().LogOnly("title", "meta->size->h", "missing", "created_at").LogOnlyDirty())
	post := newArticle(t, db, "Dated")
	if _, err := logger.Save(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	created := entries(t, svc, "acme", activitylog.Filter{Subject: activitylog.RefOf(post)})[0].Changes()
	size, _ := created.Attributes["meta"].(map[string]any)["size"].(map[string]any)
	if v, ok := size["h"]; !ok || v != nil {
		t.Errorf("a missing JSON path is not a nested null: %+v", created.Attributes["meta"])
	}
	if v, ok := created.Attributes["missing"]; !ok || v != nil {
		t.Errorf("a missing name is not logged as null: %+v", created.Attributes)
	}
	if at, _ := created.Attributes["created_at"].(string); !strings.HasSuffix(at, "Z") || len(at) != len(activitylog.DateFormat) {
		t.Errorf("created_at = %q, want %s", at, activitylog.DateFormat)
	}

	// Only what changed, and nothing for an unchanged JSON path.
	post.Title = "Dated again"
	if _, err := logger.Save(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	updates := entries(t, svc, "acme", activitylog.Filter{Subject: activitylog.RefOf(post), Event: activitylog.EventUpdated})
	if len(updates) != 1 {
		t.Fatalf("logged %d updates", len(updates))
	}
	if changes := updates[0].Changes(); len(changes.Attributes) != 1 || changes.Attributes["title"] != "Dated again" {
		t.Errorf("an unchanged attribute was reported as changed: %+v", changes)
	}

	// A restore by saving deleted_at back to null is not an update.
	if _, err := logger.Delete(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	post.DeletedAt = nil
	if _, err := logger.Save(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, svc, "acme", activitylog.Filter{Subject: activitylog.RefOf(post), Event: activitylog.EventUpdated}); len(got) != 1 {
		t.Errorf("a restore by save was logged as an update")
	}

	// A creation that sets nothing but the ignored attributes is not logged.
	setOptions(t, activitylog.DefaultLogOptions().LogAll().DontLogIfAttributesChangedOnly("id", "tenant_id", "title", "body", "secret", "meta", "author_id", "created_at", "updated_at", "deleted_at"))
	quiet := newArticle(t, db, "Quiet")
	if _, err := logger.Save(ctx, g, quiet); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, svc, "acme", activitylog.Filter{Subject: activitylog.RefOf(quiet)}); len(got) != 0 {
		t.Errorf("a creation of ignored attributes only was logged")
	}

	// LogOnly replaces, as Spatie's does.
	setOptions(t, activitylog.DefaultLogOptions().LogOnly("body").LogOnly("title"))
	replaced := newArticle(t, db, "Replaced")
	if _, err := logger.Save(ctx, g, replaced); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, svc, "acme", activitylog.Filter{Subject: activitylog.RefOf(replaced)}); len(got) != 1 || len(got[0].Changes().Attributes) != 1 {
		t.Errorf("a second LogOnly did not replace the first")
	}
}

// A force delete is logged as deleted, with what the attributes were; a record
// leaves out the events it does not record.
func TestAForceDeleteIsLoggedAsADeletion(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	g := grantOf(t, member("acme"))
	setOptions(t, activitylog.DefaultLogOptions().LogOnly("title"))

	post := newArticle(t, db, "Gone")
	if _, err := post.Save(ctx, g); err != nil {
		t.Fatal(err)
	}
	if _, err := logger.ForceDelete(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	got := entries(t, svc, "acme", activitylog.Filter{Subject: activitylog.RefOf(post)})
	if len(got) != 1 || *got[0].Event != activitylog.EventDeleted || got[0].Changes().Old["title"] != "Gone" {
		t.Fatalf("force delete = %+v", got)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM articles WHERE id = ?", post.ID).Scan(&count); err != nil || count != 0 {
		t.Errorf("the row was not removed for good")
	}
	byRecord, err := svc.ForSubject(ctx, member("acme"), post, data.Query{})
	if err != nil || len(byRecord) != 1 {
		t.Errorf("ForSubject = %d (%v)", len(byRecord), err)
	}
}

// A failed flush keeps its entries for the next one; WithBuffer flushes what
// it recorded even when its work failed; the request middleware flushes after
// the response.
func TestTheBufferKeepsWhatAFailedFlushCouldNotWrite(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	g := grantOf(t, member("acme"))

	buffered, buffer := logger.Buffered(ctx)
	first, _ := logger.Activity(buffered, g).Log("one")
	second, _ := logger.Activity(buffered, g).Log("two")
	second.ID = first.ID // a duplicate key makes the flush fail
	if err := buffer.Flush(ctx); err == nil {
		t.Fatal("a flush with a duplicate key succeeded")
	}
	if buffer.Len() != 2 {
		t.Fatalf("a failed flush lost entries: %d kept", buffer.Len())
	}
	id, _ := activitylogID()
	second.ID = id
	if err := buffer.Flush(ctx); err != nil {
		t.Fatal(err)
	}

	failed := logger.WithBuffer(ctx, func(ctx context.Context) error {
		_, _ = logger.Activity(ctx, g).Log("from a failed job")
		return context.Canceled
	})
	if failed != context.Canceled {
		t.Errorf("WithBuffer answered %v", failed)
	}
	if got := entries(t, svc, "acme", activitylog.Filter{Search: "failed job"}); len(got) != 1 {
		t.Errorf("a failed job's entries were not flushed")
	}
}

// activitylogID is a fresh entry identifier, the way the logger makes one.
func activitylogID() (string, error) {
	return time.Now().UTC().Format("20060102150405.000000000"), nil
}

// activitylog:clean says what Spatie's says, and refuses an empty --days.
func TestTheCleanCommandSpeaksAsSpatiesDoes(t *testing.T) {
	db := openDatabase(t)
	svc := newService(t, db, activitylog.Config{})
	commands, err := activitylog.Commands(activitylog.Deps{Service: svc, Operator: member})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	app := console.NewApplication(&out, &out, strings.NewReader("")).Add(commands...)
	if err := app.Call(context.Background(), "activitylog:clean", "--tenant=acme", "--force"); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"Cleaning activity log...", "Deleted 0 record(s) from the activity log.", "All done!"} {
		if !strings.Contains(out.String(), line) {
			t.Errorf("the clean did not say %q: %q", line, out.String())
		}
	}
	if err := app.Call(context.Background(), "activitylog:clean", "--tenant=acme", "--days=", "--force"); err == nil {
		t.Error("an empty --days was accepted")
	}
}

// PermissionPolicy decides by the subject's granted actions, within its
// tenant, and Actions are what an application declares in arandu-permission.
func TestThePermissionPolicyDecidesByGrantedActions(t *testing.T) {
	policy := activitylog.PermissionPolicy{}
	ctx := context.Background()
	granted := security.Subject{ID: "u1", Tenant: "acme", Actions: []security.Action{activitylog.ActivityView}}
	if err := policy.Can(ctx, granted, activitylog.ActivityView, activitylog.Activity{}); err != nil {
		t.Errorf("a granted view was refused: %v", err)
	}
	if err := policy.Can(ctx, granted, activitylog.ActivityClean, activitylog.Activity{}); err == nil {
		t.Error("an action that was not granted was allowed")
	}
	if err := policy.Can(ctx, granted, activitylog.ActivityView, activitylog.Activity{ID: "x", TenantID: "globex"}); err == nil {
		t.Error("another tenant's entry was allowed")
	}
	if len(activitylog.Actions()) != 2 {
		t.Errorf("Actions = %v", activitylog.Actions())
	}
}

// A relation's attribute is logged by its dot path, read before and after the
// write: a new author is a change of author.name, and a change of the title
// alone does not report the relation as changed.
func TestARelationIsLoggedBeforeAndAfter(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	g := grantOf(t, member("acme"))
	setOptions(t, activitylog.DefaultLogOptions().LogOnly("title", "author.name").LogOnlyDirty())

	writer := func(name string) *author {
		a := authorTable.New(db).(*author)
		a.Name = name
		if _, err := a.Save(ctx, g); err != nil {
			t.Fatal(err)
		}
		return a
	}
	ana, bia := writer("Ana"), writer("Bia")
	post := newArticle(t, db, "Story")
	post.AuthorID = ana.ID
	if _, err := logger.Save(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	post.AuthorID = bia.ID
	if _, err := logger.Save(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	post.Title = "Story, revised"
	if _, err := logger.Save(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	got := entries(t, svc, "acme", activitylog.Filter{Subject: activitylog.RefOf(post)})
	if len(got) != 3 {
		t.Fatalf("logged %d entries", len(got))
	}
	if created := got[0].Changes(); created.Attributes["author.name"] != "Ana" {
		t.Errorf("created = %+v", created)
	}
	if reauthored := got[1].Changes(); reauthored.Old["author.name"] != "Ana" || reauthored.Attributes["author.name"] != "Bia" {
		t.Errorf("a new author is not logged as a change: %+v", reauthored)
	}
	if retitled := got[2].Changes(); len(retitled.Attributes) != 1 || retitled.Attributes["title"] != "Story, revised" {
		t.Errorf("an unchanged relation was reported: %+v", retitled)
	}

	// A placeholder reads a loaded relation of the subject.
	fresh, err := post.Fresh(ctx, g, "author")
	if err != nil {
		t.Fatal(err)
	}
	described, err := logger.Activity(ctx, g).On(fresh.(*article)).Log(":subject.title by :subject.author.name")
	if err != nil || described.Description != "Story, revised by Bia" {
		t.Errorf("description = %q (%v)", described.Description, err)
	}
}
