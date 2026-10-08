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
)

// Actions are the actions this package's policy answers about, for an
// application to declare beside its own -- in arandu-permission's catalogue,
// so an organization grants reading or cleaning its log to whom it decides,
// as permission.Actions() is declared.
func Actions() []security.Action { return []security.Action{ActivityView, ActivityClean} }

// PermissionPolicy decides by the subject's own actions, which
// arandu-permission fills: it allows an action the subject was granted, on an
// entry of the subject's tenant. It is the policy for an application that
// administers its permissions there.
type PermissionPolicy struct{}

var _ security.Policy[Activity] = PermissionPolicy{}

// Can allows a granted action on an entry of the subject's tenant.
func (PermissionPolicy) Can(_ context.Context, s security.Subject, a security.Action, record Activity) error {
	if record.ID != "" && record.TenantID != s.Tenant {
		return fmt.Errorf("activity belongs to another tenant")
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
