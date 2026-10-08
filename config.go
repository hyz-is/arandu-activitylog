package activitylog

import (
	"context"
	"fmt"
	"net/http"

	"github.com/arandu-io/framework/security"
)

// The defaults for the optional settings. They are constants rather than
// literals inside Config.withDefaults, so the value a reader finds here is the
// value the package uses.
const (
	// DefaultLogName is the log an entry belongs to when nothing names one.
	DefaultLogName = "default"
	// DefaultCleanAfterDays is how old an entry is when the clean removes it.
	DefaultCleanAfterDays = 365
	// DefaultCauserType is the kind a Grant's subject is recorded as.
	DefaultCauserType = "user"
	// DefaultPrefix is where the read routes are mounted when Config leaves
	// Prefix empty.
	DefaultPrefix = "/activity-log"
	// DefaultPageSize is how many entries one page answers with.
	DefaultPageSize = 50
	// MaxPageSize is the ceiling PageSize is refused above.
	MaxPageSize = 200
	// DefaultCleanSpec is when the scheduled clean runs: every day at 03:10.
	DefaultCleanSpec = "10 3 * * *"
)

// Config is what the application passes when it wires this package: Spatie's
// config/activitylog.php, as a typed struct. A misspelled key in a map is a
// setting that silently keeps its default; a field that does not exist does
// not compile.
type Config struct {
	// Tenant is the customer a visitor with no session is read as, on the
	// read routes. It comes from the application's own configuration, never
	// from the request.
	Tenant string

	// Disabled turns recording off. Nothing is written while it is true, and
	// Log answers a nil entry, as Spatie's ACTIVITYLOG_ENABLED=false does.
	Disabled bool

	// DefaultLogName is the log an entry belongs to when nothing names one.
	// Empty means DefaultLogName.
	DefaultLogName string

	// CleanAfterDays is how old an entry is when the clean removes it. Zero
	// means DefaultCleanAfterDays.
	CleanAfterDays int

	// CleanSpec is the five-field cron expression the scheduled clean runs
	// on. Empty means DefaultCleanSpec; "-" schedules no clean.
	CleanSpec string

	// CleanTenants are tenants the scheduled clean also reaches, beside the
	// ones the scheduler lists for per-tenant work: the application's own
	// tenant, where the mail log lives, is usually one.
	CleanTenants []string

	// DefaultExceptAttributes are never logged from any record, whatever its
	// options say: the place for a password hash, a token, a secret.
	DefaultExceptAttributes []string

	// CauserType is the kind the Grant's subject is recorded as when an entry
	// names no causer. Empty means DefaultCauserType.
	CauserType string

	// ResolveCauser decides who caused an entry that names no causer, in
	// place of the Grant's subject -- Spatie's CauserResolver::resolveUsing.
	// It may answer the zero Ref for an anonymous entry.
	ResolveCauser func(ctx context.Context, g security.Grant) Ref

	// BeforeLogging runs on every entry, in order, right before it is
	// written -- Spatie's Activity::beforeLogging. It may change anything on
	// the entry, such as adding the request's address to its properties. An
	// error stops the entry and is answered to the caller.
	BeforeLogging []func(ctx context.Context, a *Activity) error

	// TransformChanges runs on every entry that carries a record's change,
	// before BeforeLogging -- Spatie's LogActivityAction::transformChanges,
	// the place to redact a field.
	TransformChanges func(a *Activity)

	// Policy decides who may read and clean the log. Nil is ActivityPolicy,
	// which denies everything: a wiring that says nothing about rules gets no
	// access at all, which is the state to start from. Recording needs no
	// policy: it takes the Grant the caller's own action was authorized with.
	Policy security.Policy[Activity]

	// Prefix is the path the read routes are mounted under. Empty means
	// DefaultPrefix.
	Prefix string

	// PageSize is how many entries one page answers with. Zero means
	// DefaultPageSize, and anything above MaxPageSize is refused.
	PageSize int
}

// Validate reports what the configuration cannot be used with.
func (c Config) Validate() error {
	if c.Tenant != "" && !security.ValidTenant(c.Tenant) {
		return fmt.Errorf("activitylog: Config.Tenant is %q, which cannot be a tenant: lowercase letters, digits, - and _, up to 64 characters", c.Tenant)
	}
	for _, tenant := range c.CleanTenants {
		if !security.ValidTenant(tenant) {
			return fmt.Errorf("activitylog: Config.CleanTenants holds %q, which cannot be a tenant", tenant)
		}
	}
	if c.CleanAfterDays < 0 {
		return fmt.Errorf("activitylog: Config.CleanAfterDays is %d, and has to be zero, for %d, or more", c.CleanAfterDays, DefaultCleanAfterDays)
	}
	if c.PageSize < 0 || c.PageSize > MaxPageSize {
		return fmt.Errorf("activitylog: Config.PageSize is %d, and has to be between 0 and %d, where 0 means %d", c.PageSize, MaxPageSize, DefaultPageSize)
	}
	if c.Prefix != "" {
		if c.Prefix[0] != '/' {
			return fmt.Errorf("activitylog: Config.Prefix is %q and has to start with /", c.Prefix)
		}
		if err := validateRoutePrefix(c.Prefix); err != nil {
			return err
		}
	}
	return nil
}

// withDefaults returns the configuration with the optional fields filled in.
// It runs after Validate, so a value somebody wrote is checked as written.
func (c Config) withDefaults() Config {
	if c.DefaultLogName == "" {
		c.DefaultLogName = DefaultLogName
	}
	if c.CleanAfterDays == 0 {
		c.CleanAfterDays = DefaultCleanAfterDays
	}
	if c.CleanSpec == "" {
		c.CleanSpec = DefaultCleanSpec
	}
	if c.CauserType == "" {
		c.CauserType = DefaultCauserType
	}
	if c.Prefix == "" {
		c.Prefix = DefaultPrefix
	}
	if c.PageSize == 0 {
		c.PageSize = DefaultPageSize
	}
	if c.Policy == nil {
		c.Policy = ActivityPolicy{}
	}
	return c
}

// route is one line of the table this module registers.
type route struct {
	method  string
	pattern string
	name    string
}

// routePatterns is the whole read table, built from one prefix. It is read by
// Validate, which registers it on a throwaway mux, and by Routes.
func routePatterns(prefix string) []route {
	return []route{
		{http.MethodGet, prefix, "activitylog.index"},
		{http.MethodGet, prefix + "/{id}", "activitylog.show"},
	}
}

// validateRoutePrefix asks the standard library to parse the exact patterns
// the module will register; its parser reports an invalid one by panic.
func validateRoutePrefix(prefix string) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("activitylog: Config.Prefix %q cannot be registered as a route path", prefix)
		}
	}()
	mux := http.NewServeMux()
	for _, r := range routePatterns(prefix) {
		mux.Handle(r.method+" "+r.pattern, http.NotFoundHandler())
	}
	return nil
}
