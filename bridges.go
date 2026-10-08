package activitylog

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/arandu-io/framework/events"
	"github.com/arandu-io/framework/mail"
	"github.com/arandu-io/framework/security"
)

// ActivityRecord is the action of the Grants this package records under on
// its own: the events it is handed and the mail it watches leave. An entry an
// application records takes the Grant of the act it describes instead.
const ActivityRecord security.Action = "activity.record"

// The logs the two bridges write to by default.
const (
	// EventsLogName is where EventRecorder writes the domain events.
	EventsLogName = "events"
	// MailLogName is where MailTransport writes the mail.
	MailLogName = "mail"
)

// EventRecorder writes every committed domain event of the outbox into the
// activity log, so everything an application already records as an event --
// in its own transaction, with the tenant, the subject whose Grant authorized
// it and the action -- is also in the log, read by Spatie's scopes.
//
// It is an events.Publisher: the relay hands it each event once the write that
// produced it committed. The relay delivers at least once, so the entry takes
// the event's identifier and a second delivery finds it written and does
// nothing.
type EventRecorder struct {
	logger *Logger
	// LogName decides the log of an event; nil writes every event to
	// EventsLogName.
	LogName func(e events.Stored) string
	// Describe decides the description of an event; nil uses its name.
	Describe func(e events.Stored) string
	// Payload keeps the event's payload in the entry's properties, under
	// "payload". It is off by default: a payload is whatever the producer
	// stored, and the log is read by more people than the outbox is.
	Payload bool
}

// NewEventRecorder returns the recorder over the logger.
func NewEventRecorder(logger *Logger) *EventRecorder { return &EventRecorder{logger: logger} }

var _ events.Publisher = (*EventRecorder)(nil)

// Publish writes one committed event as an entry of its tenant: the event's
// name, its aggregate as the subject, the subject that authorized it as the
// causer, and when it occurred.
func (r *EventRecorder) Publish(ctx context.Context, e events.Stored) error {
	if e.TenantID == "" || !security.ValidTenant(e.TenantID) {
		return nil
	}
	//arandu:system-grant the event was authorized and committed in this tenant; its row carries the tenant, and this writes the log entry about it there.
	g := security.SystemGrant(ActivityRecord, e.TenantID)
	if e.ID != "" {
		found, err := Activities(r.logger.db).WhereKey(e.ID).Exists(ctx, g)
		if err != nil {
			return err
		}
		if found {
			return nil
		}
	}
	logName := EventsLogName
	if r.LogName != nil {
		logName = r.LogName(e)
	}
	description := e.Name
	if r.Describe != nil {
		description = r.Describe(e)
	}
	chain := r.logger.In(ctx, g, logName).Event(e.Name).
		OnRef(Ref{Type: e.Aggregate, ID: e.AggregateID}).
		WithProperty("action", e.Action).
		CreatedAt(e.OccurredAt)
	if e.AuthorizedBy != "" {
		chain = chain.ByRef(Ref{Type: r.logger.cfg.CauserType, ID: e.AuthorizedBy})
	} else {
		chain = chain.ByAnonymous()
	}
	if r.Payload && e.Payload != "" {
		var payload any
		if json.Unmarshal([]byte(e.Payload), &payload) == nil {
			chain = chain.WithProperty("payload", payload)
		}
	}
	if e.ID != "" {
		id := e.ID
		chain = chain.Tap(func(a *Activity, _ string) { a.ID = id })
	}
	_, err := chain.Log(description)
	return err
}

// MailTransport is a mail transport that writes every message it is handed
// into the activity log of one tenant -- the mail log -- and hands it on.
// A message the next transport refused is logged as failed, with the reason,
// and the refusal is answered to the sender as it would have been.
type MailTransport struct {
	next   mail.Transport
	logger *Logger
	tenant string
}

// NewMailTransport watches next, writing to the log of tenant -- the
// application's own, from its configuration, never from a message.
func NewMailTransport(next mail.Transport, logger *Logger, tenant string) (*MailTransport, error) {
	if next == nil || logger == nil {
		return nil, errors.New("activitylog: NewMailTransport needs the transport it watches and a logger")
	}
	if !security.ValidTenant(tenant) {
		return nil, errors.New("activitylog: NewMailTransport needs the tenant its log belongs to")
	}
	return &MailTransport{next: next, logger: logger, tenant: tenant}, nil
}

var _ mail.Transport = (*MailTransport)(nil)

// Name is the watched transport's.
func (t *MailTransport) Name() string { return t.next.Name() }

// Unwrap is the watched transport.
func (t *MailTransport) Unwrap() mail.Transport { return t.next }

// Send hands the message on, then writes the entry. A failure to write the
// entry is not the message's failure: the message is already gone.
func (t *MailTransport) Send(ctx context.Context, m mail.Message) error {
	sendErr := t.next.Send(ctx, m)
	event, description := "sent", "Sent: "+m.Subject
	if sendErr != nil {
		event, description = "failed", "Not sent: "+m.Subject
	}
	//arandu:system-grant the mail log belongs to the application's own tenant, named in its configuration.
	g := security.SystemGrant(ActivityRecord, t.tenant)
	chain := t.logger.In(ctx, g, MailLogName).Event(event).ByAnonymous().WithProperties(map[string]any{
		"to":        addresses(m.To),
		"cc":        addresses(m.CC),
		"subject":   m.Subject,
		"tags":      m.Tags,
		"transport": t.next.Name(),
	})
	if sendErr != nil {
		chain = chain.WithProperty("error", sendErr.Error())
	}
	_, _ = chain.Log(description)
	return sendErr
}

func addresses(list []mail.Address) []string {
	out := make([]string, 0, len(list))
	for _, address := range list {
		out = append(out, strings.TrimSpace(address.Email))
	}
	return out
}
