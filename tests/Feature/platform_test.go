package feature_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/auth"

	activitylog "github.com/hyz-is/arandu-activitylog"
)

// platformPolicy is the rule an application writes for its platform
// administrators: the read of every tenant, for a subject the platform made a
// manager, and nothing else.
type platformPolicy struct{}

func (platformPolicy) Can(_ context.Context, s security.Subject, a security.Action, _ activitylog.Activity) error {
	if a == activitylog.ActivityViewAll && s.HasRole("platform-manager") {
		return nil
	}
	return fmt.Errorf("no platform rule allows %s", a)
}

// manager is the platform's administrator, signed in to the platform's own
// tenant.
func manager() security.Subject {
	return security.Subject{ID: "manager-1", Tenant: "platform", Roles: []string{"platform-manager"}, Verified: true}
}

// seedTenants records n entries in each tenant, alternating between them, and
// answers how many there are.
func seedTenants(t *testing.T, logger *activitylog.Logger, n int, tenants ...string) int {
	t.Helper()
	for i := range n {
		for _, tenant := range tenants {
			if _, err := logger.Activity(context.Background(), grantOf(t, member(tenant))).InLog("billing").Event("paid").
				Log(fmt.Sprintf("%s paid %d", tenant, i)); err != nil {
				t.Fatal(err)
			}
		}
	}
	return n * len(tenants)
}

func TestThePlatformAdministratorReadsEveryTenantsLog(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	total := seedTenants(t, logger, 3, "acme", "globex")
	if _, err := logger.Activity(context.Background(), grantOf(t, member("acme"))).InLog("auth").Event("signed-in").Log("signed in"); err != nil {
		t.Fatal(err)
	}
	total++
	svc := newService(t, db, activitylog.Config{PlatformPolicy: platformPolicy{}})
	ctx := context.Background()

	page, err := svc.ReadAll(ctx, manager(), activitylog.PlatformQuery{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Entries) != total || page.NextCursor != "" {
		t.Fatalf("the platform read %d entries and cursor %q, want all %d and none", len(page.Entries), page.NextCursor, total)
	}
	seen := map[string]int{}
	for i, entry := range page.Entries {
		seen[entry.Tenant]++
		if i > 0 {
			before := page.Entries[i-1]
			if entry.CreatedAt.After(before.CreatedAt) || (entry.CreatedAt.Equal(before.CreatedAt) && entry.ID > before.ID) {
				t.Fatalf("entry %d is newer than the one before it", i)
			}
		}
	}
	if seen["acme"] != 4 || seen["globex"] != 3 || len(seen) != 2 {
		t.Fatalf("the entries by tenant are %v", seen)
	}

	// The tenants and the filter narrow it.
	only, err := svc.ReadAll(ctx, manager(), activitylog.PlatformQuery{Tenants: []string{"globex"}})
	if err != nil || len(only.Entries) != 3 || only.Entries[0].Tenant != "globex" {
		t.Fatalf("Tenants kept %d entries (%v)", len(only.Entries), err)
	}
	logged, err := svc.ReadAll(ctx, manager(), activitylog.PlatformQuery{Filter: activitylog.Filter{LogNames: []string{"auth"}}})
	if err != nil || len(logged.Entries) != 1 || logged.Entries[0].Tenant != "acme" || logged.Entries[0].Event != "signed-in" {
		t.Fatalf("the filter kept %+v (%v)", logged.Entries, err)
	}

	// The cursor walks every entry once, in order, across tenants.
	var walked []string
	cursor := ""
	for range total {
		next, err := svc.ReadAll(ctx, manager(), activitylog.PlatformQuery{Cursor: cursor, Limit: 3})
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range next.Entries {
			walked = append(walked, entry.ID)
		}
		if cursor = next.NextCursor; cursor == "" {
			break
		}
	}
	if len(walked) != total {
		t.Fatalf("the cursor walked %d entries, want %d", len(walked), total)
	}
	for i := range walked {
		if walked[i] != page.Entries[i].ID {
			t.Fatalf("entry %d of the walk is %s, want %s", i, walked[i], page.Entries[i].ID)
		}
	}

	if _, err := svc.ReadAll(ctx, manager(), activitylog.PlatformQuery{Tenants: []string{"Not A Tenant"}}); err == nil {
		t.Fatal("a tenant that cannot be one was accepted")
	}
}

// A Grant of the ordinary read never reaches another tenant, however widely the
// application's Policy opens it, and the platform read is refused to whoever
// the platform policy did not allow.
func TestNoOrdinaryGrantReadsAcrossTenants(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	seedTenants(t, logger, 2, "acme", "globex")
	ctx := context.Background()

	// openPolicy allows every action, ActivityViewAll included, as Policy.
	// ReadAll does not ask it.
	open := newService(t, db, activitylog.Config{})
	if _, err := open.ReadAll(ctx, manager(), activitylog.PlatformQuery{}); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("with no PlatformPolicy the platform read answered %v", err)
	}
	// The ordinary read of a platform manager is their own tenant's, and the
	// entries of the customers are not in it.
	own, err := open.List(ctx, manager(), activitylog.Filter{}, data.Query{Limit: 200})
	if err != nil || len(own) != 0 {
		t.Fatalf("the ordinary read of the platform tenant held %d entries (%v)", len(own), err)
	}
	if acme := entries(t, open, "acme", activitylog.Filter{}); len(acme) != 2 {
		t.Fatalf("the ordinary read of acme held %d entries", len(acme))
	}

	// Neither shipped policy opens it, even to a subject holding the action.
	holder := security.Subject{ID: "user-acme", Tenant: "acme", Verified: true,
		Actions: []security.Action{activitylog.ActivityView, activitylog.ActivityViewAll}}
	for name, policy := range map[string]security.Policy[activitylog.Activity]{
		"ActivityPolicy":   activitylog.ActivityPolicy{},
		"PermissionPolicy": activitylog.PermissionPolicy{},
	} {
		svc := newService(t, db, activitylog.Config{Policy: activitylog.PermissionPolicy{}, PlatformPolicy: policy})
		if _, err := svc.ReadAll(ctx, holder, activitylog.PlatformQuery{}); !errors.Is(err, security.ErrForbidden) {
			t.Errorf("%s as the platform policy opened the platform read: %v", name, err)
		}
		// PermissionPolicy still opens the organization's own read.
		if name == "PermissionPolicy" {
			if err := (activitylog.PermissionPolicy{}).Can(ctx, holder, activitylog.ActivityView, activitylog.Activity{}); err != nil {
				t.Errorf("PermissionPolicy refused the organization's read: %v", err)
			}
		}
	}

	// The platform policy refuses whoever it did not make a manager.
	platform := newService(t, db, activitylog.Config{PlatformPolicy: platformPolicy{}})
	strangers := []security.Subject{member("acme"), holder, security.Guest("acme")}
	for _, who := range strangers {
		if _, err := platform.ReadAll(ctx, who, activitylog.PlatformQuery{}); !errors.Is(err, security.ErrForbidden) {
			t.Errorf("%q read every tenant: %v", who.ID, err)
		}
	}

	// A manager inside somebody's account reads no other tenant, even when the
	// account's subject carries the manager's role.
	account := member("acme")
	account.Roles = append(account.Roles, "platform-manager")
	inside, err := auth.ImpersonateActing(manager(), account)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := platform.ReadAll(ctx, inside, activitylog.PlatformQuery{}); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("a subject inside an account read every tenant: %v", err)
	}
}

func TestThePlatformActionsAreNotAnOrganizationsToGrant(t *testing.T) {
	for _, action := range activitylog.Actions() {
		if action == activitylog.ActivityViewAll {
			t.Fatal("Actions(), which an organization grants from, holds the platform read")
		}
	}
	platform := activitylog.PlatformActions()
	if len(platform) != 1 || platform[0] != activitylog.ActivityViewAll {
		t.Fatalf("PlatformActions() = %v", platform)
	}
}

func TestThePlatformAdministratorReadsOneEntryOfAnyTenant(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	seedTenants(t, logger, 1, "acme", "globex")
	ctx := context.Background()
	svc := newService(t, db, activitylog.Config{PlatformPolicy: platformPolicy{}})

	page, err := svc.ReadAll(ctx, manager(), activitylog.PlatformQuery{Tenants: []string{"globex"}})
	if err != nil || len(page.Entries) != 1 {
		t.Fatalf("the globex page held %d entries (%v)", len(page.Entries), err)
	}
	entry, err := svc.ReadEntry(ctx, manager(), page.Entries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Tenant != "globex" || entry.ID != page.Entries[0].ID || entry.Description != "globex paid 0" {
		t.Fatalf("the entry read is %+v", entry)
	}

	if _, err := svc.ReadEntry(ctx, manager(), "no-such-entry"); !errors.Is(err, activitylog.ErrNotFound) {
		t.Fatalf("an entry nobody holds answered %v", err)
	}
	if _, err := svc.ReadEntry(ctx, member("globex"), page.Entries[0].ID); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("a member of the tenant read it through the platform read: %v", err)
	}
	if _, err := newService(t, db, activitylog.Config{}).ReadEntry(ctx, manager(), page.Entries[0].ID); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("with no PlatformPolicy the entry was read: %v", err)
	}
	inside, err := auth.ImpersonateActing(manager(), member("acme"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReadEntry(ctx, inside, page.Entries[0].ID); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("a subject inside an account read another tenant's entry: %v", err)
	}
}

// A person reads their own entries in every tenant -- what they caused and
// what an operator did inside their account -- and nobody else's.
func TestAPersonReadsTheirOwnEntriesInEveryTenant(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	ctx := context.Background()
	ana := security.Subject{ID: "ana", Tenant: "acme", Verified: true}
	// Ana acts in two organizations; Bruno acts in one.
	for _, tenant := range []string{"acme", "globex"} {
		who := ana
		who.Tenant = tenant
		if _, err := logger.Activity(ctx, grantOf(t, who)).InLog("billing").Event("paid").Log("ana paid in " + tenant); err != nil {
			t.Fatal(err)
		}
	}
	bruno := security.Subject{ID: "bruno", Tenant: "acme", Verified: true}
	if _, err := logger.Activity(ctx, grantOf(t, bruno)).InLog("billing").Event("paid").Log("bruno paid"); err != nil {
		t.Fatal(err)
	}
	// An operator inside Ana's account.
	inside, err := auth.ImpersonateActing(manager(), ana)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := logger.Activity(ctx, grantOf(t, inside)).InLog("billing").Event("refunded").Log("refunded for ana"); err != nil {
		t.Fatal(err)
	}
	svc := newService(t, db, activitylog.Config{})

	page, err := svc.ReadOwn(ctx, ana, activitylog.OwnQuery{})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, entry := range page.Entries {
		got[entry.Description] = entry.Tenant
	}
	if len(got) != 3 || got["ana paid in acme"] != "acme" || got["ana paid in globex"] != "globex" || got["refunded for ana"] != "acme" {
		t.Fatalf("Ana's own entries are %v", got)
	}

	// The filter narrows, and Involves cannot be pointed at somebody else.
	paid, err := svc.ReadOwn(ctx, ana, activitylog.OwnQuery{Filter: activitylog.Filter{Event: "paid", Involves: activitylog.Ref{Type: "user", ID: "bruno"}}})
	if err != nil || len(paid.Entries) != 2 {
		t.Fatalf("Ana's paid entries are %d (%v)", len(paid.Entries), err)
	}
	for _, entry := range paid.Entries {
		if entry.Description == "bruno paid" {
			t.Fatal("Involves read somebody else's entries")
		}
	}

	// One entry: hers, or not found.
	if entry, err := svc.ReadOwnEntry(ctx, ana, page.Entries[0].ID); err != nil || entry.ID != page.Entries[0].ID {
		t.Fatalf("Ana's own entry: %+v (%v)", entry, err)
	}
	bruns, err := svc.ReadOwn(ctx, bruno, activitylog.OwnQuery{})
	if err != nil || len(bruns.Entries) != 1 {
		t.Fatalf("Bruno's own entries are %d (%v)", len(bruns.Entries), err)
	}
	if _, err := svc.ReadOwnEntry(ctx, ana, bruns.Entries[0].ID); !errors.Is(err, activitylog.ErrNotFound) {
		t.Fatalf("Ana read Bruno's entry: %v", err)
	}

	// A guest has nothing of its own.
	if _, err := svc.ReadOwn(ctx, security.Guest("acme"), activitylog.OwnQuery{}); !errors.Is(err, security.ErrForbidden) {
		t.Fatalf("a guest read: %v", err)
	}
}
