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
