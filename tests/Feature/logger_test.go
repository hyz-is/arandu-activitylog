package feature_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/arandu-io/framework/security"

	activitylog "github.com/hyz-is/arandu-activitylog"
)

// A manual entry is Spatie's activity()->...->log(): a log, an event, a
// subject, a causer, properties, and a description whose placeholders read
// them. Its tenant is the Grant's, and its causer defaults to the Grant's
// subject.
func TestAManualEntryRecordsWhatItIsToldInTheGrantsTenant(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	g := grantOf(t, member("acme"))

	post := newArticle(t, db, "Hello")
	if _, err := post.Save(ctx, g); err != nil {
		t.Fatal(err)
	}
	writer := authorTable.New(db).(*author)
	writer.Name = "Ana"
	if _, err := writer.Save(ctx, g); err != nil {
		t.Fatal(err)
	}

	entry, err := logger.Activity(ctx, g).InLog("editorial").Event("published").On(post).By(writer).
		WithProperties(map[string]any{"channel": "web"}).WithProperty("reach", map[string]any{"people": 42}).
		Log(":causer.name published :subject.title on :properties.channel to :properties.reach.people people; :unknown.thing stays; :subject.missing stays.")
	if err != nil {
		t.Fatal(err)
	}
	if entry == nil || entry.ID == "" {
		t.Fatal("no entry was answered")
	}
	want := "Ana published Hello on web to 42 people; :unknown.thing stays; :subject.missing stays."
	if entry.Description != want {
		t.Errorf("description = %q, want %q", entry.Description, want)
	}

	stored := entries(t, svc, "acme", activitylog.Filter{})
	if len(stored) != 1 {
		t.Fatalf("stored %d entries, want 1", len(stored))
	}
	got := stored[0]
	if got.TenantID != "acme" {
		t.Errorf("tenant = %q, want the Grant's", got.TenantID)
	}
	if got.Subject() != (activitylog.Ref{Type: "articles", ID: post.ID}) {
		t.Errorf("subject = %+v", got.Subject())
	}
	if got.Causer() != (activitylog.Ref{Type: "author", ID: writer.ID}) {
		t.Errorf("causer = %+v", got.Causer())
	}
	if got.GetProperty("reach.people", nil) != float64(42) || got.GetProperty("missing", "fallback") != "fallback" {
		t.Errorf("properties = %+v", got.Properties())
	}
	if *got.LogName != "editorial" || *got.Event != "published" {
		t.Errorf("log %v, event %v", *got.LogName, *got.Event)
	}

	// With no causer named, the Grant's subject caused it; anonymous is
	// nobody; a context default and the configured resolver come in between.
	plain, _ := logger.Activity(ctx, g).Log("plain")
	if plain.Causer() != (activitylog.Ref{Type: activitylog.DefaultCauserType, ID: "user-acme"}) {
		t.Errorf("default causer = %+v", plain.Causer())
	}
	anonymous, _ := logger.Activity(ctx, g).ByAnonymous().Log("anonymous")
	if !anonymous.Causer().IsZero() {
		t.Errorf("anonymous causer = %+v", anonymous.Causer())
	}
	scoped, _ := logger.Activity(activitylog.WithCauser(ctx, activitylog.Ref{Type: "robot", ID: "r1"}), g).Log("scoped")
	if scoped.Causer() != (activitylog.Ref{Type: "robot", ID: "r1"}) {
		t.Errorf("context causer = %+v", scoped.Causer())
	}
	resolving := newLogger(t, db, activitylog.Config{ResolveCauser: func(context.Context, security.Grant) activitylog.Ref {
		return activitylog.Ref{Type: "service", ID: "billing"}
	}})
	resolved, _ := resolving.Activity(ctx, g).Log("resolved")
	if resolved.Causer() != (activitylog.Ref{Type: "service", ID: "billing"}) {
		t.Errorf("resolved causer = %+v", resolved.Causer())
	}

	// A tap sets anything, and a description it set wins over Log's.
	when := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	tapped, _ := logger.Activity(ctx, g).CreatedAt(when).Tap(func(a *activitylog.Activity, event string) {
		a.Description = "from the tap"
	}).Log("from log")
	if tapped.Description != "from the tap" || !tapped.CreatedAt.Equal(when) {
		t.Errorf("tap: %q at %v", tapped.Description, tapped.CreatedAt)
	}

	// Another tenant reads none of it.
	if other := entries(t, svc, "globex", activitylog.Filter{}); len(other) != 0 {
		t.Errorf("another tenant read %d entries", len(other))
	}
}

// Logging turned off writes nothing and answers nothing, whether by
// configuration or by the context.
func TestNothingIsRecordedWhileLoggingIsOff(t *testing.T) {
	db := openDatabase(t)
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	g := grantOf(t, member("acme"))

	off := newLogger(t, db, activitylog.Config{Disabled: true})
	if entry, err := off.Activity(ctx, g).Log("never"); entry != nil || err != nil {
		t.Errorf("disabled logger answered %v, %v", entry, err)
	}
	on := newLogger(t, db, activitylog.Config{})
	quiet := activitylog.WithoutLogging(ctx)
	if entry, _ := on.Activity(quiet, g).Log("never"); entry != nil {
		t.Error("WithoutLogging recorded an entry")
	}
	if _, err := on.Save(quiet, g, newArticle(t, db, "quiet")); err != nil {
		t.Fatal(err)
	}
	if entry, _ := on.Activity(activitylog.WithLogging(quiet), g).Log("again"); entry == nil {
		t.Error("WithLogging did not turn logging back on")
	}
	if got := entries(t, svc, "acme", activitylog.Filter{}); len(got) != 1 {
		t.Errorf("stored %d entries, want only the one recorded with logging on", len(got))
	}
}

// Saving through the logger writes the row and its entry together: created
// with what the attributes became, updated with what they were, deleted with
// what they were, restored with what they are. Excluded attributes never
// appear.
func TestARecordsChangesAreLoggedAsSpatieLogsThem(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{DefaultExceptAttributes: []string{"secret"}})
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	g := grantOf(t, member("acme"))
	setOptions(t, activitylog.DefaultLogOptions().LogOnly("title", "body", "secret", "meta->color").UseLogName("articles").
		SetDescriptionForEvent(func(event string) string { return "article " + event }))

	post := newArticle(t, db, "Draft")
	if _, err := logger.Save(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	post.Title = "Final"
	if _, err := logger.Save(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	if _, err := logger.Delete(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	if _, err := logger.Restore(ctx, g, post); err != nil {
		t.Fatal(err)
	}

	got := entries(t, svc, "acme", activitylog.Filter{LogNames: []string{"articles"}})
	if len(got) != 4 {
		t.Fatalf("logged %d entries, want created, updated, deleted, restored", len(got))
	}
	for i, event := range []string{"created", "updated", "deleted", "restored"} {
		if *got[i].Event != event || got[i].Description != "article "+event {
			t.Errorf("entry %d: event %q description %q", i, *got[i].Event, got[i].Description)
		}
		if got[i].Subject().ID != post.ID {
			t.Errorf("entry %d is about %+v", i, got[i].Subject())
		}
		changes := got[i].Changes()
		for _, side := range []map[string]any{changes.Attributes, changes.Old} {
			if _, leaked := side["secret"]; leaked {
				t.Errorf("entry %d logged an excluded attribute", i)
			}
		}
	}
	created := got[0].Changes()
	if created.Attributes["title"] != "Draft" || created.Old != nil {
		t.Errorf("created = %+v", created)
	}
	if color, _ := created.Attributes["meta"].(map[string]any); color["color"] != "red" {
		t.Errorf("a JSON path was not kept nested: %+v", created.Attributes["meta"])
	}
	updated := got[1].Changes()
	if updated.Attributes["title"] != "Final" || updated.Old["title"] != "Draft" {
		t.Errorf("updated = %+v", updated)
	}
	if deleted := got[2].Changes(); deleted.Old["title"] != "Final" || deleted.Attributes != nil {
		t.Errorf("deleted = %+v", deleted)
	}
	if restored := got[3].Changes(); restored.Attributes["title"] != "Final" {
		t.Errorf("restored = %+v", restored)
	}
}

// LogOnlyDirty keeps what changed; an update of nothing but the ignored
// attributes is not logged; an empty change is skipped when asked; a record's
// own events, switch and hook are honored.
func TestTheLogOptionsShapeWhatIsLogged(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	g := grantOf(t, member("acme"))
	setOptions(t, activitylog.DefaultLogOptions().LogOnly("title", "body").LogOnlyDirty().DontLogIfAttributesChangedOnly("body", "updated_at"))

	post := newArticle(t, db, "One")
	if _, err := logger.Save(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	post.Title = "Two"
	if _, err := logger.Save(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	post.Body = "only the body"
	if _, err := logger.Save(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	got := entries(t, svc, "acme", activitylog.Filter{Event: activitylog.EventUpdated})
	if len(got) != 1 {
		t.Fatalf("logged %d updates, want 1: a change of the body alone is not logged", len(got))
	}
	if changes := got[0].Changes(); len(changes.Attributes) != 1 || changes.Attributes["title"] != "Two" || changes.Old["title"] != "One" {
		t.Errorf("only the dirty attribute should be logged: %+v", changes)
	}

	// No attribute logged and empty changes refused: nothing is written.
	articleOptions = activitylog.DefaultLogOptions().DontLogEmptyChanges()
	quiet := newArticle(t, db, "empty")
	if _, err := logger.Save(ctx, g, quiet); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, svc, "acme", activitylog.Filter{Subject: activitylog.Ref{ID: quiet.ID}}); len(got) != 0 {
		t.Errorf("an empty change was logged %d times", len(got))
	}

	// The record chooses its events, mutes itself, and adjusts its entries.
	articleOptions = activitylog.DefaultLogOptions().LogAll()
	articleEvents = []string{activitylog.EventDeleted}
	chosen := newArticle(t, db, "chosen")
	if _, err := logger.Save(ctx, g, chosen); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, svc, "acme", activitylog.Filter{Subject: activitylog.Ref{ID: chosen.ID}}); len(got) != 0 {
		t.Errorf("an event the record does not record was logged")
	}
	articleEvents = nil
	articleMuted = true
	muted := newArticle(t, db, "muted")
	if _, err := logger.Save(ctx, g, muted); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, svc, "acme", activitylog.Filter{Subject: activitylog.Ref{ID: muted.ID}}); len(got) != 0 {
		t.Errorf("a muted record was logged")
	}
	articleMuted = false
	articleHook = func(a *activitylog.Activity, event string) { _ = a.SetProperty("hooked", event) }
	hooked := newArticle(t, db, "hooked")
	if _, err := logger.Save(ctx, g, hooked); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, svc, "acme", activitylog.Filter{Subject: activitylog.Ref{ID: hooked.ID}}); len(got) != 1 || got[0].GetProperty("hooked", nil) != "created" {
		t.Errorf("the record's hook did not run")
	}
}

// The hooks run on every entry: transformChanges redacts, beforeLogging adds,
// and a beforeLogging error stops the entry and, with it, the record's save.
func TestTheHooksRunAndAFailureRollsTheRecordBack(t *testing.T) {
	db := openDatabase(t)
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	g := grantOf(t, member("acme"))
	setOptions(t, activitylog.DefaultLogOptions().LogOnly("title", "body"))

	logger := newLogger(t, db, activitylog.Config{
		TransformChanges: func(a *activitylog.Activity) {
			changes := a.Changes()
			changes.Attributes["body"] = "[redacted]"
			_ = a.SetChanges(changes)
		},
		BeforeLogging: []func(context.Context, *activitylog.Activity) error{
			func(_ context.Context, a *activitylog.Activity) error { return a.SetProperty("ip", "203.0.113.7") },
		},
	})
	post := newArticle(t, db, "Hooks")
	if _, err := logger.Save(ctx, g, post); err != nil {
		t.Fatal(err)
	}
	got := entries(t, svc, "acme", activitylog.Filter{})
	if len(got) != 1 || got[0].Changes().Attributes["body"] != "[redacted]" || got[0].GetProperty("ip", nil) != "203.0.113.7" {
		t.Fatalf("hooks did not shape the entry: %+v", got)
	}

	refusing := newLogger(t, db, activitylog.Config{BeforeLogging: []func(context.Context, *activitylog.Activity) error{
		func(context.Context, *activitylog.Activity) error { return errors.New("refused") },
	}})
	doomed := newArticle(t, db, "Doomed")
	if _, err := refusing.Save(ctx, g, doomed); err == nil {
		t.Fatal("a refused entry did not fail the save")
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM articles WHERE title = 'Doomed'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Error("the record was written although its entry was refused")
	}
}

// A buffered context keeps its entries until the buffer is flushed, and then
// writes them together.
func TestABufferWritesItsEntriesWhenFlushed(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	g := grantOf(t, member("acme"))

	buffered, buffer := logger.Buffered(ctx)
	for _, text := range []string{"one", "two", "three"} {
		entry, err := logger.Activity(buffered, g).Log(text)
		if err != nil || entry == nil || entry.ID == "" {
			t.Fatalf("buffered entry: %v %v", entry, err)
		}
	}
	if buffer.Len() != 3 || len(entries(t, svc, "acme", activitylog.Filter{})) != 0 {
		t.Fatal("a buffered entry was written before the flush")
	}
	if err := buffer.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if got := entries(t, svc, "acme", activitylog.Filter{}); len(got) != 3 || got[0].Description != "one" {
		t.Errorf("flushed %d entries", len(got))
	}
	if buffer.Len() != 0 {
		t.Error("the buffer was not emptied")
	}
}

// An entry recorded under a Grant with no tenant is refused: an entry belongs
// to somebody.
func TestAnEntryNeedsAGrantWithATenant(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	if _, err := logger.Activity(context.Background(), security.Grant{}).Log("nobody's"); !errors.Is(err, activitylog.ErrNoGrant) {
		t.Errorf("err = %v, want ErrNoGrant", err)
	}
}
