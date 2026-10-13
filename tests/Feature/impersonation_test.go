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
