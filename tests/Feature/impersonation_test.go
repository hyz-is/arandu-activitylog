package feature_test

import (
	"context"
	"testing"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/auth"

	activitylog "github.com/hyz-is/arandu-activitylog"
)

// An operator acting in a customer's account: what they change is written
// with the customer's Grant, and the entry names the operator as its causer
// and the customer as the account it was done in.
func TestAnEntryMadeInsideSomebodysAccountNamesTheOperator(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	ctx := context.Background()

	operator := security.Subject{ID: "op-1", Tenant: "acme", Roles: []string{"admin"}, Verified: true}
	acting, err := auth.ImpersonateActing(operator, member("acme"))
	if err != nil {
		t.Fatal(err)
	}
	g := grantOf(t, acting)

	// The write goes through the logger, as an application saves a record.
	post := newArticle(t, db, "Hello")
	if _, err := logger.Save(ctx, g, post); err != nil {
		t.Fatalf("saving inside the account: %v", err)
	}
	entry, err := logger.Activity(ctx, g).Event("updated").On(post).WithProperties(map[string]any{"field": "title"}).Log("changed :subject.title")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Causer().ID != "op-1" {
		t.Fatalf("the causer is %q, want the operator", entry.Causer().ID)
	}
	mark, _ := entry.Properties()[activitylog.ImpersonationProperty].(map[string]any)
	if mark["by"] != "op-1" || mark["on_behalf_of"] != "user-acme" || mark["mode"] != "acting" {
		t.Fatalf("the entry records the impersonation as %v", entry.Properties())
	}
	if entry.Properties()["field"] != "title" {
		t.Fatal("the caller's own properties were lost")
	}

	// Viewing only: the mark says so.
	viewing, _ := auth.Impersonate(operator, member("acme"))
	seen, err := logger.Activity(ctx, grantOf(t, viewing)).Event("viewed").Log("looked")
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := seen.Properties()[activitylog.ImpersonationProperty].(map[string]any); m["mode"] != "viewing" || seen.Causer().ID != "op-1" {
		t.Fatalf("a viewing entry is %v by %q", seen.Properties(), seen.Causer().ID)
	}

	// An ordinary entry carries no mark, and its causer is the member.
	plain, err := logger.Activity(ctx, grantOf(t, member("acme"))).Event("viewed").Log("looked")
	if err != nil {
		t.Fatal(err)
	}
	if _, marked := plain.Properties()[activitylog.ImpersonationProperty]; marked || plain.Causer().ID != "user-acme" {
		t.Fatalf("an ordinary entry is %v by %q", plain.Properties(), plain.Causer().ID)
	}
}

// What was done by a person and what an operator did inside their account are
// one filter, read by an indexed column the logger fills from the Grant.
func TestInvolvesKeepsWhatAPersonDidAndWhatWasDoneInTheirAccount(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()

	owner := member("acme")
	operator := security.Subject{ID: "op-1", Tenant: "acme", Roles: []string{"admin"}, Verified: true}
	acting, err := auth.ImpersonateActing(operator, owner)
	if err != nil {
		t.Fatal(err)
	}
	other := security.Subject{ID: "user-other", Tenant: "acme", Verified: true}

	mine, err := logger.Activity(ctx, grantOf(t, owner)).Event("updated").Log("by the owner")
	if err != nil {
		t.Fatal(err)
	}
	inside, err := logger.Activity(ctx, grantOf(t, acting)).Event("updated").Log("by the operator, inside")
	if err != nil {
		t.Fatal(err)
	}
	// A tap that names an account is overwritten: only the Grant says so.
	forged, err := logger.Activity(ctx, grantOf(t, other)).Tap(func(a *activitylog.Activity, _ string) {
		account := owner.ID
		a.OnBehalfOf = &account
	}).Log("by somebody else")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logger.Activity(ctx, grantOf(t, operator)).Event("updated").Log("by the operator, at home"); err != nil {
		t.Fatal(err)
	}

	if inside.OnBehalfOf == nil || *inside.OnBehalfOf != owner.ID {
		t.Fatalf("the entry made inside the account has on_behalf_of %v", inside.OnBehalfOf)
	}
	if mine.OnBehalfOf != nil || forged.OnBehalfOf != nil {
		t.Fatalf("an ordinary entry carries on_behalf_of: %v, %v", mine.OnBehalfOf, forged.OnBehalfOf)
	}

	ids := func(filter activitylog.Filter) map[string]bool {
		out := map[string]bool{}
		for _, e := range entries(t, svc, "acme", filter) {
			out[e.ID] = true
		}
		return out
	}
	got := ids(activitylog.Filter{Involves: activitylog.Ref{ID: owner.ID}})
	if len(got) != 2 || !got[mine.ID] || !got[inside.ID] {
		t.Fatalf("Involves the owner kept %v, want their own entry and the operator's inside", got)
	}
	got = ids(activitylog.Filter{Involves: activitylog.Ref{Type: activitylog.DefaultCauserType, ID: owner.ID}})
	if len(got) != 2 || !got[mine.ID] || !got[inside.ID] {
		t.Fatalf("Involves the owner by kind kept %v", got)
	}
	got = ids(activitylog.Filter{Involves: activitylog.Ref{Type: "author", ID: owner.ID}})
	if len(got) != 0 {
		t.Fatalf("Involves of another kind matched the account: %v", got)
	}
	// The other filters still narrow it, and another tenant sees none of it.
	got = ids(activitylog.Filter{Involves: activitylog.Ref{ID: owner.ID}, Causer: activitylog.Ref{ID: operator.ID}})
	if len(got) != 1 || !got[inside.ID] {
		t.Fatalf("Involves with Causer kept %v", got)
	}
	if foreign := entries(t, svc, "globex", activitylog.Filter{Involves: activitylog.Ref{ID: owner.ID}}); len(foreign) != 0 {
		t.Fatalf("another tenant read %d entries of this account", len(foreign))
	}
}
