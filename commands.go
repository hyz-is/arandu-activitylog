package activitylog

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/console"
)

// CommandPrefix is what every command of this package is called under.
const CommandPrefix = "activitylog:"

// Deps is what the commands need, built by the application where it wires
// everything else.
type Deps struct {
	// Service is the same service the routes call, so a clean from a terminal
	// passes the same policy as one from anywhere else.
	Service *ActivityService

	// Operator says who a command runs as, for the customer it names. A
	// command has no session; who it runs as is the application's answer.
	Operator func(tenant string) security.Subject
}

// Validate reports what the dependencies cannot be used with.
func (d Deps) Validate() error {
	if d.Service == nil {
		return errors.New("activitylog: Deps.Service is required")
	}
	if d.Operator == nil {
		return errors.New("activitylog: Deps.Operator is required: a command runs as somebody, and who belongs to the application")
	}
	return nil
}

// Commands builds every command of this package.
func Commands(deps Deps) ([]console.Command, error) {
	if err := deps.Validate(); err != nil {
		return nil, err
	}
	return []console.Command{cleanCommand(deps)}, nil
}

// cleanCommand is Spatie's activitylog:clean: it removes the entries older
// than --days (the configured CleanAfterDays by default), of one log or of
// all of them, for one customer. Without --force it says what it would remove
// and removes nothing.
func cleanCommand(deps Deps) console.Command {
	return console.Command{
		Signature: CommandPrefix + "clean {log? : The log to clean, empty for every log}" +
			" {--tenant= : The customer whose log is cleaned}" +
			" {--days= : Remove the entries older than this many days}" +
			" {--force : Remove them, rather than count them}",
		Description: "remove old entries from the activity log",
		Run: func(ctx context.Context, o *console.IO) error {
			tenant := strings.TrimSpace(o.Option("tenant").String())
			if tenant == "" || !security.ValidTenant(tenant) {
				return console.Exit(1, "--tenant is required, and has to be a tenant: lowercase letters, digits, - and _")
			}
			actor := deps.Operator(tenant)
			if actor.Tenant != tenant {
				return console.Exit(1, "the operator for %q belongs to %q", tenant, actor.Tenant)
			}
			days := 0
			if option := o.Option("days"); option.Present() {
				parsed, err := strconv.Atoi(strings.TrimSpace(option.String()))
				if err != nil || parsed < 1 {
					return console.Exit(1, "The days option must be a positive integer.")
				}
				days = parsed
			}
			o.Comment("Cleaning activity log...")
			logName := o.Argument("log").String()
			if !o.Option("force").Bool() {
				filter := Filter{Until: cutoffFor(deps.Service, days)}
				if logName != "" {
					filter.LogNames = []string{logName}
				}
				count, err := deps.Service.Count(ctx, actor, filter)
				if err != nil {
					return fail(err)
				}
				o.Comment("%d record(s) would be deleted from the activity log. Run again with --force to delete them.", count)
				return nil
			}
			deleted, err := deps.Service.Clean(ctx, actor, days, logName)
			if err != nil {
				return fail(err)
			}
			o.Info("Deleted %d record(s) from the activity log.", deleted)
			o.Comment("All done!")
			return nil
		},
	}
}

func fail(err error) error {
	if errors.Is(err, security.ErrForbidden) {
		return console.Exit(1, "the policy refused this: pass a policy that opens %s in Config.Policy", ActivityClean)
	}
	return console.Exit(1, "%v", err)
}
