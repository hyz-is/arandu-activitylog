package activitylog

import (
	"context"
	"fmt"

	"github.com/arandu-io/framework/security"
)

// The actions the policy answers about. Recording is not one of them: an entry
// is recorded under the Grant of the act it describes.
const (
	// ActivityView is reading one entry, or paging through the log.
	ActivityView security.Action = "activity.view"
	// ActivityClean is removing old entries.
	ActivityClean security.Action = "activity.clean"
	// ActivityViewAll is reading the log of every tenant at once: the platform
	// administrator's, through ActivityService.ReadAll. It is asked of
	// Config.PlatformPolicy and of no other policy, and neither policy this
	// package ships allows it.
	ActivityViewAll security.Action = "activity.view_all"
	// ActivityViewOwn is a person reading their own entries, of every tenant:
	// what they caused, and what an operator did inside their account --
	// ActivityService.ReadOwn. Nobody grants it: every signed-in subject
	// holds it over its own entries, and over nobody else's.
	ActivityViewOwn security.Action = "activity.view_own"
)

// Actions are the actions this package's policy answers about, for an
// application to declare beside its own -- in arandu-permission's catalogue,
// so an organization grants reading or cleaning its log to whom it decides,
// as permission.Actions() is declared.
func Actions() []security.Action { return []security.Action{ActivityView, ActivityClean} }

// PlatformActions are the actions of the platform rather than of an
// organization: ActivityViewAll. They are declared apart from Actions so a
// catalogue an organization grants from never holds them; an application gives
// them to its platform administrators only, and its Config.PlatformPolicy
// decides.
func PlatformActions() []security.Action { return []security.Action{ActivityViewAll} }

// PermissionPolicy decides by the subject's own actions, which
// arandu-permission fills: it allows an action the subject was granted, on an
// entry of the subject's tenant. It is the policy for an application that
// administers its permissions there.
type PermissionPolicy struct{}

var _ security.Policy[Activity] = PermissionPolicy{}

// Can allows a granted action on an entry of the subject's tenant. It never
// allows ActivityViewAll: an action an organization's group could be given is
// not one that reads every organization, and the platform read is opened only
// by a policy the application writes for its platform administrators.
func (PermissionPolicy) Can(_ context.Context, s security.Subject, a security.Action, record Activity) error {
	if record.ID != "" && record.TenantID != s.Tenant {
		return fmt.Errorf("activity belongs to another tenant")
	}
	if a == ActivityViewAll {
		return fmt.Errorf("%s is decided by the application's platform policy", a)
	}
	if s.Can(a) {
		return nil
	}
	return fmt.Errorf("the subject was not granted %s", a)
}

// ActivityPolicy is the policy this package ships, and it denies everything.
//
// An application opens the log by passing its own policy in Config.Policy,
// beside the rest of its authorization; until it does, nobody reads or cleans
// the log through this package, which is the state to start from.
type ActivityPolicy struct{}

var _ security.Policy[Activity] = ActivityPolicy{}

// Can refuses every action. A record of another tenant is refused first, so a
// policy written by copying this one keeps the line that matters most.
func (ActivityPolicy) Can(_ context.Context, s security.Subject, a security.Action, record Activity) error {
	if record.ID != "" && record.TenantID != s.Tenant {
		return fmt.Errorf("activity belongs to another tenant")
	}
	return fmt.Errorf("no rule allows %s on activity", a)
}

// ownPolicy is the rule of ActivityViewOwn: a signed-in subject reads its own
// entries. Which entries are its own is not the policy's to say -- the read
// itself is narrowed to them, and to nothing else -- so the rule is only that
// there is somebody to read them for.
type ownPolicy struct{}

var _ security.Policy[Activity] = ownPolicy{}

func (ownPolicy) Can(_ context.Context, s security.Subject, a security.Action, _ Activity) error {
	if a != ActivityViewOwn {
		return fmt.Errorf("no rule allows %s on one's own activity", a)
	}
	if s.ID == "" || s.IsGuest() {
		return fmt.Errorf("a guest has no activity of its own")
	}
	return nil
}
