package activitylog

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	p := &PendingActivity{logger: l, ctx: ctx, grant: g}
	p.reset()
	return p
}

// In starts an entry in the named log: Spatie's activity('name'). An empty
// name is the default log, as a falsy name is in Spatie.
func (l *Logger) In(ctx context.Context, g security.Grant, logName string) *PendingActivity {
	p := l.Activity(ctx, g)
	if logName != "" {
		p.InLog(logName)
	}
	return p
}

// PendingActivity is an entry being described: Spatie's PendingActivityLog.
// Every method writes into the entry at once and returns the chain, so a tap
// sees what the chain set before it, and a method after a tap overrides it,
// as in Spatie. Log writes it and starts a fresh one.
//
// It is not safe for concurrent use; it is meant to live for one chain.
type PendingActivity struct {
	logger *Logger
	ctx    context.Context
	grant  security.Grant

	activity   *Activity
	subjectRec Record
	causerRec  Record
	err        error
}

// reset starts a fresh entry: the default log, no change, no property, and the
// causer the context or the Grant names -- Spatie's getActivity.
func (p *PendingActivity) reset() {
	activity, err := Activities(p.logger.db).New()
	if err != nil {
		p.err = err
		activity = &Activity{}
	}
	activity.LogName = optional(p.logger.cfg.DefaultLogName)
	causer := p.defaultCauser()
	activity.CauserType, activity.CauserID = optional(causer.Type), optional(causer.ID)
	p.activity, p.subjectRec, p.causerRec = activity, nil, nil
}

// Entry is the entry being described, for what the chain has no method for.
func (p *PendingActivity) Entry() *Activity { return p.activity }

// InLog puts the entry in the named log; an empty name leaves the entry with
// no log, as Spatie's useLog(null). UseLog is the same method under Spatie's
// other name.
func (p *PendingActivity) InLog(logName string) *PendingActivity {
	p.activity.LogName = optional(logName)
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
	ref := RefOf(record)
	p.activity.SubjectType, p.activity.SubjectID = optional(ref.Type), optional(ref.ID)
	p.subjectRec = record
	return p
}

// PerformedOn is On.
func (p *PendingActivity) PerformedOn(record Record) *PendingActivity { return p.On(record) }

// OnRef says what the entry is about by reference, for a thing that is not a
// row of this database.
func (p *PendingActivity) OnRef(ref Ref) *PendingActivity {
	p.activity.SubjectType, p.activity.SubjectID = optional(ref.Type), optional(ref.ID)
	p.subjectRec = nil
	return p
}

// By says who caused the entry: a record, whose kind and key become the causer
// and whose attributes the :causer placeholders read. A nil record changes
// nothing, as causedBy(null) does in Spatie. CausedBy is the same method under
// Spatie's other name.
func (p *PendingActivity) By(record Record) *PendingActivity {
	if record == nil {
		return p
	}
	ref := RefOf(record)
	p.activity.CauserType, p.activity.CauserID = optional(ref.Type), optional(ref.ID)
	p.causerRec = record
	return p
}

// CausedBy is By.
func (p *PendingActivity) CausedBy(record Record) *PendingActivity { return p.By(record) }

// ByRef says who caused the entry by reference.
func (p *PendingActivity) ByRef(ref Ref) *PendingActivity {
	p.activity.CauserType, p.activity.CauserID = optional(ref.Type), optional(ref.ID)
	p.causerRec = nil
	return p
}

// ByAnonymous records the entry with no causer, whoever holds the Grant.
// CausedByAnonymous is the same method under Spatie's other name.
func (p *PendingActivity) ByAnonymous() *PendingActivity {
	p.activity.CauserType, p.activity.CauserID = nil, nil
	p.causerRec = nil
	return p
}

// CausedByAnonymous is ByAnonymous.
func (p *PendingActivity) CausedByAnonymous() *PendingActivity { return p.ByAnonymous() }

// Event names the kind of change the entry records. SetEvent is the same
// method under Spatie's other name.
func (p *PendingActivity) Event(event string) *PendingActivity {
	p.activity.Event = optional(event)
	return p
}

// SetEvent is Event.
func (p *PendingActivity) SetEvent(event string) *PendingActivity { return p.Event(event) }

// WithProperties replaces what the entry carries.
func (p *PendingActivity) WithProperties(properties map[string]any) *PendingActivity {
	if err := p.activity.SetProperties(properties); err != nil && p.err == nil {
		p.err = err
	}
	return p
}

// WithProperty adds one property to what the entry carries.
func (p *PendingActivity) WithProperty(key string, value any) *PendingActivity {
	if err := p.activity.SetProperty(key, value); err != nil && p.err == nil {
		p.err = err
	}
	return p
}

// WithChanges sets the record's change the entry carries.
func (p *PendingActivity) WithChanges(changes Changes) *PendingActivity {
	if err := p.activity.SetChanges(Changes{Attributes: changes.Attributes, Old: changes.Old}); err != nil && p.err == nil {
		p.err = err
	}
	return p
}

// CreatedAt dates the entry; the default is now.
func (p *PendingActivity) CreatedAt(at time.Time) *PendingActivity {
	p.activity.CreatedAt = at.UTC()
	return p
}

// Tap runs fn on the entry now, with the chain's event -- the place to set
// anything the chain has no method for. As in Spatie, a method called after
// the tap overrides what the tap set.
func (p *PendingActivity) Tap(fn func(a *Activity, event string)) *PendingActivity {
	if fn != nil {
		fn(p.activity, text(p.activity.Event))
	}
	return p
}

// When applies fn to the chain when condition holds, and otherwise the
// optional default -- Spatie's Conditionable.
func (p *PendingActivity) When(condition bool, fn func(*PendingActivity) *PendingActivity, otherwise ...func(*PendingActivity) *PendingActivity) *PendingActivity {
	if condition {
		if fn != nil {
			return fn(p)
		}
		return p
	}
	for _, fallback := range otherwise {
		if fallback != nil {
			return fallback(p)
		}
	}
	return p
}

// Unless applies fn to the chain when condition does not hold, and otherwise
// the optional default.
func (p *PendingActivity) Unless(condition bool, fn func(*PendingActivity) *PendingActivity, otherwise ...func(*PendingActivity) *PendingActivity) *PendingActivity {
	return p.When(!condition, fn, otherwise...)
}

// Log writes the entry and answers it. Its description is one a tap already
// set or else description, with its placeholders replaced. While logging is
// off -- by configuration or by WithoutLogging -- nothing is written, the
// answer is nil with no error, and the entry being described is kept, as in
// Spatie.
//
// Inside a context made by Buffered the entry is kept and written when the
// buffer is flushed; its identifier is already set.
func (p *PendingActivity) Log(description string) (*Activity, error) {
	l := p.logger
	if !l.Enabled(p.ctx) {
		return nil, nil
	}
	if p.err != nil {
		err := p.err
		p.err = nil
		p.reset()
		return nil, err
	}
	if security.Tenant(p.grant) == "" {
		return nil, ErrNoGrant
	}
	activity := p.activity
	if activity.ID == "" {
		id, err := database.NewOrderedID()
		if err != nil {
			return nil, err
		}
		activity.ID = id
	}
	now := time.Now().UTC()
	if activity.CreatedAt.IsZero() {
		activity.CreatedAt = now
	}
	activity.UpdatedAt = now
	if activity.Description == "" {
		activity.Description = description
	}
	activity.Description = p.replacePlaceholders(activity.Description, activity)
	if l.cfg.TransformChanges != nil {
		l.cfg.TransformChanges(activity)
	}
	if hook, ok := p.subjectRec.(BeforeActivityLogged); ok {
		hook.BeforeActivityLogged(activity, text(activity.Event))
	}
	for _, before := range l.cfg.BeforeLogging {
		if err := before(p.ctx, activity); err != nil {
			p.reset()
			return nil, err
		}
	}
	p.reset()
	if buffer := bufferOf(p.ctx); buffer != nil {
		buffer.add(p.grant, activity)
		return activity, nil
	}
	if _, err := activity.Save(p.ctx, p.grant); err != nil {
		return nil, fmt.Errorf("activitylog: writing the entry: %w", err)
	}
	return activity, nil
}

// defaultCauser answers who caused an entry that names nobody: the default the
// context carries, else the configured resolver, else the Grant's subject when
// it is somebody -- Spatie's CauserResolver.
func (p *PendingActivity) defaultCauser() Ref {
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
// what they name. The base is matched exactly, as Spatie's in_array does. A
// token whose base is anything else, whose base is absent -- an anonymous
// causer, an entry about nothing -- or whose path leads nowhere is left as
// written.
func (p *PendingActivity) replacePlaceholders(description string, activity *Activity) string {
	return placeholder.ReplaceAllStringFunc(description, func(token string) string {
		trailing := ""
		for strings.HasSuffix(token, ".") {
			token, trailing = token[:len(token)-1], "."+trailing
		}
		base, path, _ := strings.Cut(token[1:], ".")
		var values map[string]any
		switch base {
		case "subject":
			values = recordValues(p.subjectRec, activity.Subject())
		case "causer":
			values = recordValues(p.causerRec, activity.Causer())
		case "properties":
			values = activity.Properties()
		default:
			return token + trailing
		}
		if values == nil {
			return token + trailing
		}
		value, ok := lookup(values, path)
		if !ok && base != "properties" {
			// A loaded relation of the subject or the causer: :subject.author.name.
			value, ok = relationPath(values, path)
		}
		if !ok || value == nil {
			return token + trailing
		}
		return fmt.Sprint(normalizeValue(value)) + trailing
	})
}

// recordValues is what a placeholder can read: a record's array, with its kind
// and key under "type" and "id" when it has no column so named; a reference's
// kind and key; nothing for the zero reference.
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
// was recorded with, and empties the buffer. When the write fails, the entries
// are kept for the next flush, as Spatie's buffer keeps them. ctx should not
// be the buffered context: the writes are the flush's own.
func (b *Buffer) Flush(ctx context.Context) error {
	b.mu.Lock()
	entries := b.entries
	b.entries = nil
	b.mu.Unlock()
	if len(entries) == 0 {
		return nil
	}
	err := data.Transaction(ctx, b.logger.db, func(ctx context.Context) error {
		for _, entry := range entries {
			if _, err := entry.activity.Save(ctx, entry.grant); err != nil {
				return fmt.Errorf("activitylog: flushing the buffer: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		b.mu.Lock()
		b.entries = append(entries, b.entries...)
		b.mu.Unlock()
	}
	return err
}

// WithBuffer runs fn under a buffered context and flushes what it recorded
// when fn returns, whether or not it failed -- Spatie's flush after a job,
// processed or failed. The flush's error is answered when fn's is nil.
func (l *Logger) WithBuffer(ctx context.Context, fn func(ctx context.Context) error) error {
	buffered, buffer := l.Buffered(ctx)
	err := fn(buffered)
	if flushErr := buffer.Flush(context.WithoutCancel(ctx)); err == nil {
		err = flushErr
	}
	return err
}

// BufferRequests is a middleware that buffers what each request records and
// flushes it once the response is written -- Spatie's flush on terminate. A
// failed flush is reported to onError, which may be nil.
func (l *Logger) BufferRequests(onError func(r *http.Request, err error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			buffered, buffer := l.Buffered(r.Context())
			next.ServeHTTP(w, r.WithContext(buffered))
			if err := buffer.Flush(context.WithoutCancel(r.Context())); err != nil && onError != nil {
				onError(r, err)
			}
		})
	}
}
