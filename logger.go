package activitylog

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/arandu-io/framework/data"
	"github.com/arandu-io/framework/security"
	"github.com/arandu-io/hesape/database"
)

// Logger records entries: Spatie's ActivityLogger, reached by a value the
// application holds rather than by a global helper.
//
// An entry is recorded under the Grant the caller's own action was authorized
// with. That is the whole of its authorization, and it is enough: the entry is
// a side effect of an act the policy already allowed, it lands in that act's
// tenant -- data.Tenant(g), never a tenant somebody names -- and in that act's
// transaction when the context carries one. Reading and cleaning the log are
// separate acts with their own policy; see ActivityService.
type Logger struct {
	db  *data.DB
	cfg Config
}

// NewLogger wires the logger over the application's database handle.
func NewLogger(db *data.DB, cfg Config) (*Logger, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if db == nil {
		return nil, errors.New("activitylog: NewLogger needs a database handle: entries are rows, and there is no in-memory mode")
	}
	return &Logger{db: db, cfg: cfg.withDefaults()}, nil
}

// Enabled says whether an entry recorded with this context would be written.
func (l *Logger) Enabled(ctx context.Context) bool {
	return !l.cfg.Disabled && !loggingDisabled(ctx)
}

// Activity starts an entry in the default log, recorded under g: Spatie's
// activity(). Nothing is written until Log.
func (l *Logger) Activity(ctx context.Context, g security.Grant) *PendingActivity {
	return &PendingActivity{logger: l, ctx: ctx, grant: g, logName: l.cfg.DefaultLogName}
}

// In starts an entry in the named log: Spatie's activity('name').
func (l *Logger) In(ctx context.Context, g security.Grant, logName string) *PendingActivity {
	return l.Activity(ctx, g).InLog(logName)
}

// PendingActivity is an entry being described. Every method returns it, so a
// description reads as one chain ending in Log.
//
// It is not safe for concurrent use; it is meant to live for one chain.
type PendingActivity struct {
	logger *Logger
	ctx    context.Context
	grant  security.Grant

	logName    string
	event      string
	subject    Ref
	subjectRec Record
	causer     Ref
	causerRec  Record
	causerSet  bool
	properties map[string]any
	changes    Changes
	createdAt  time.Time
	taps       []func(a *Activity, event string)
}

// InLog puts the entry in the named log. An empty name keeps the default.
// UseLog is the same method under Spatie's other name.
func (p *PendingActivity) InLog(logName string) *PendingActivity {
	if logName != "" {
		p.logName = logName
	}
	return p
}

// UseLog is InLog.
func (p *PendingActivity) UseLog(logName string) *PendingActivity { return p.InLog(logName) }

// On says what the entry is about: a record, whose kind and key become the
// subject and whose attributes the :subject placeholders read. PerformedOn is
// the same method under Spatie's other name.
func (p *PendingActivity) On(record Record) *PendingActivity {
	if record == nil {
		return p
	}
	p.subject, p.subjectRec = refOf(record), record
	return p
}

// PerformedOn is On.
func (p *PendingActivity) PerformedOn(record Record) *PendingActivity { return p.On(record) }

// OnRef says what the entry is about by reference, for a thing that is not a
// row of this database.
func (p *PendingActivity) OnRef(ref Ref) *PendingActivity {
	p.subject, p.subjectRec = ref, nil
	return p
}

// By says who caused the entry: a record, whose kind and key become the causer
// and whose attributes the :causer placeholders read. It wins over the Grant's
// subject and over a default causer the context carries. CausedBy is the same
// method under Spatie's other name.
func (p *PendingActivity) By(record Record) *PendingActivity {
	if record == nil {
		return p
	}
	p.causer, p.causerRec, p.causerSet = refOf(record), record, true
	return p
}

// CausedBy is By.
func (p *PendingActivity) CausedBy(record Record) *PendingActivity { return p.By(record) }

// ByRef says who caused the entry by reference.
func (p *PendingActivity) ByRef(ref Ref) *PendingActivity {
	p.causer, p.causerRec, p.causerSet = ref, nil, true
	return p
}

// ByAnonymous records the entry with no causer, whoever holds the Grant.
// CausedByAnonymous is the same method under Spatie's other name.
func (p *PendingActivity) ByAnonymous() *PendingActivity {
	p.causer, p.causerRec, p.causerSet = Ref{}, nil, true
	return p
}

// CausedByAnonymous is ByAnonymous.
func (p *PendingActivity) CausedByAnonymous() *PendingActivity { return p.ByAnonymous() }

// Event names the kind of change the entry records.
func (p *PendingActivity) Event(event string) *PendingActivity {
	p.event = event
	return p
}

// WithProperties replaces what the entry carries.
func (p *PendingActivity) WithProperties(properties map[string]any) *PendingActivity {
	p.properties = make(map[string]any, len(properties))
	for key, value := range properties {
		p.properties[key] = value
	}
	return p
}

// WithProperty adds one property to what the entry carries.
func (p *PendingActivity) WithProperty(key string, value any) *PendingActivity {
	if p.properties == nil {
		p.properties = map[string]any{}
	}
	p.properties[key] = value
	return p
}

// WithChanges sets the record's change the entry carries.
func (p *PendingActivity) WithChanges(changes Changes) *PendingActivity {
	p.changes = changes
	return p
}

// CreatedAt dates the entry; the default is now.
func (p *PendingActivity) CreatedAt(at time.Time) *PendingActivity {
	p.createdAt = at
	return p
}

// Tap runs fn on the entry right before it is written, with its event -- the
// place to set anything the chain has no method for.
func (p *PendingActivity) Tap(fn func(a *Activity, event string)) *PendingActivity {
	if fn != nil {
		p.taps = append(p.taps, fn)
	}
	return p
}

// When applies fn to the chain when condition holds -- Spatie's Conditionable.
func (p *PendingActivity) When(condition bool, fn func(*PendingActivity) *PendingActivity) *PendingActivity {
	if condition && fn != nil {
		return fn(p)
	}
	return p
}

// Unless applies fn to the chain when condition does not hold.
func (p *PendingActivity) Unless(condition bool, fn func(*PendingActivity) *PendingActivity) *PendingActivity {
	return p.When(!condition, fn)
}

// Log writes the entry, described by description once its placeholders are
// replaced, and answers it. While logging is off -- by configuration or by
// WithoutLogging -- nothing is written and the answer is nil with no error.
//
// Inside a context made by Buffered the entry is kept and written when the
// buffer is flushed; its identifier is already set.
func (p *PendingActivity) Log(description string) (*Activity, error) {
	l := p.logger
	if !l.Enabled(p.ctx) {
		return nil, nil
	}
	if security.Tenant(p.grant) == "" {
		return nil, ErrNoGrant
	}
	activity, err := Activities(l.db).New()
	if err != nil {
		return nil, err
	}
	if activity.ID, err = database.NewOrderedID(); err != nil {
		return nil, err
	}
	activity.LogName = optional(p.logName)
	activity.Event = optional(p.event)
	activity.SubjectType, activity.SubjectID = optional(p.subject.Type), optional(p.subject.ID)
	causer := p.resolveCauser()
	activity.CauserType, activity.CauserID = optional(causer.Type), optional(causer.ID)
	if err := activity.SetProperties(p.properties); err != nil {
		return nil, err
	}
	if err := activity.SetChanges(Changes{Attributes: normalizeMap(p.changes.Attributes), Old: normalizeMap(p.changes.Old)}); err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	activity.CreatedAt, activity.UpdatedAt = now, now
	if !p.createdAt.IsZero() {
		activity.CreatedAt = p.createdAt.UTC()
	}
	for _, tap := range p.taps {
		tap(activity, p.event)
	}
	// A description a tap already set wins over the one Log was given, as it
	// does in Spatie.
	if activity.Description == "" {
		activity.Description = p.replacePlaceholders(description, activity)
	}
	if activity.AttributeChanges != nil && l.cfg.TransformChanges != nil {
		l.cfg.TransformChanges(activity)
	}
	if hook, ok := p.subjectRec.(BeforeActivityLogged); ok {
		hook.BeforeActivityLogged(activity, p.event)
	}
	for _, before := range l.cfg.BeforeLogging {
		if err := before(p.ctx, activity); err != nil {
			return nil, err
		}
	}
	if buffer := bufferOf(p.ctx); buffer != nil {
		buffer.add(p.grant, activity)
		return activity, nil
	}
	if _, err := activity.Save(p.ctx, p.grant); err != nil {
		return nil, fmt.Errorf("activitylog: writing the entry: %w", err)
	}
	return activity, nil
}

// resolveCauser answers who caused the entry: the one the chain named, else
// the default the context carries, else the configured resolver, else the
// Grant's subject when it is somebody.
func (p *PendingActivity) resolveCauser() Ref {
	if p.causerSet {
		return p.causer
	}
	if ref, ok := defaultCauser(p.ctx); ok {
		return ref
	}
	if p.logger.cfg.ResolveCauser != nil {
		return p.logger.cfg.ResolveCauser(p.ctx, p.grant)
	}
	subject := p.grant.Subject()
	if subject.ID == "" || subject.IsGuest() {
		return Ref{}
	}
	return Ref{Type: p.logger.cfg.CauserType, ID: subject.ID}
}

// placeholder is Spatie's pattern: a colon, then letters, digits, dot,
// underscore and hyphen, never ending in a dot.
var placeholder = regexp.MustCompile(`(?i):[a-z0-9._-]+`)

// replacePlaceholders replaces :subject.x, :causer.x and :properties.x with
// what they name. A token whose base is anything else, or whose path leads
// nowhere, is left as written.
func (p *PendingActivity) replacePlaceholders(description string, activity *Activity) string {
	return placeholder.ReplaceAllStringFunc(description, func(token string) string {
		trailing := ""
		for strings.HasSuffix(token, ".") {
			token, trailing = token[:len(token)-1], "."+trailing
		}
		base, path, _ := strings.Cut(token[1:], ".")
		var values map[string]any
		switch strings.ToLower(base) {
		case "subject":
			values = recordValues(p.subjectRec, p.subject)
		case "causer":
			causer := activity.Causer()
			if p.causerRec != nil {
				values = recordValues(p.causerRec, causer)
			} else {
				values = map[string]any{"id": causer.ID, "type": causer.Type}
			}
		case "properties":
			values = activity.Properties()
		default:
			return token + trailing
		}
		if values == nil {
			return token + trailing
		}
		value, ok := lookup(values, path)
		if !ok || value == nil {
			return token + trailing
		}
		return fmt.Sprint(normalizeValue(value)) + trailing
	})
}

// recordValues is what a placeholder can read from a record: its attributes,
// and its kind and key under "type" and "id" when it has no column so named.
func recordValues(record Record, ref Ref) map[string]any {
	if record == nil {
		if ref.IsZero() {
			return nil
		}
		return map[string]any{"id": ref.ID, "type": ref.Type}
	}
	values := record.ToArray()
	if _, ok := values["id"]; !ok {
		values["id"] = ref.ID
	}
	if _, ok := values["type"]; !ok {
		values["type"] = ref.Type
	}
	return values
}

type contextKey int

const (
	keyWithoutLogging contextKey = iota
	keyCauser
	keyBuffer
)

// WithoutLogging returns a context under which nothing is recorded -- neither
// a manual entry nor a record's change: Spatie's withoutLogging, scoped to
// what the context reaches rather than to a process-wide switch.
func WithoutLogging(ctx context.Context) context.Context {
	return context.WithValue(ctx, keyWithoutLogging, true)
}

// WithLogging undoes WithoutLogging for what the returned context reaches.
func WithLogging(ctx context.Context) context.Context {
	return context.WithValue(ctx, keyWithoutLogging, false)
}

func loggingDisabled(ctx context.Context) bool {
	disabled, _ := ctx.Value(keyWithoutLogging).(bool)
	return disabled
}

// WithCauser returns a context whose entries are caused by ref unless a chain
// names another: Spatie's Activity::defaultCauser. The zero Ref makes them
// anonymous.
func WithCauser(ctx context.Context, ref Ref) context.Context {
	return context.WithValue(ctx, keyCauser, ref)
}

func defaultCauser(ctx context.Context) (Ref, bool) {
	ref, ok := ctx.Value(keyCauser).(Ref)
	return ref, ok
}

// Buffer holds the entries recorded under a context made by Buffered, and
// writes them together when flushed: Spatie's buffer, scoped to the context
// rather than to the request lifecycle.
type Buffer struct {
	mu      sync.Mutex
	logger  *Logger
	entries []bufferedEntry
}

type bufferedEntry struct {
	grant    security.Grant
	activity *Activity
}

// Buffered returns a context whose entries are kept rather than written, and
// the buffer that keeps them. Flush writes them; an entry kept and never
// flushed is never written.
func (l *Logger) Buffered(ctx context.Context) (context.Context, *Buffer) {
	buffer := &Buffer{logger: l}
	return context.WithValue(ctx, keyBuffer, buffer), buffer
}

func bufferOf(ctx context.Context) *Buffer {
	buffer, _ := ctx.Value(keyBuffer).(*Buffer)
	return buffer
}

func (b *Buffer) add(g security.Grant, activity *Activity) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.entries = append(b.entries, bufferedEntry{grant: g, activity: activity})
}

// Len is how many entries are waiting.
func (b *Buffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.entries)
}

// Flush writes every waiting entry in one transaction, each under the Grant it
// was recorded with, and empties the buffer. ctx should not be the buffered
// context: the writes are the flush's own.
func (b *Buffer) Flush(ctx context.Context) error {
	b.mu.Lock()
	entries := b.entries
	b.entries = nil
	b.mu.Unlock()
	if len(entries) == 0 {
		return nil
	}
	return data.Transaction(ctx, b.logger.db, func(ctx context.Context) error {
		for _, entry := range entries {
			if _, err := entry.activity.Save(ctx, entry.grant); err != nil {
				return fmt.Errorf("activitylog: flushing the buffer: %w", err)
			}
		}
		return nil
	})
}
