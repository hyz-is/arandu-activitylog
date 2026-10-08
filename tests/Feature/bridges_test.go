package feature_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/arandu-io/framework/events"
	"github.com/arandu-io/framework/mail"

	activitylog "github.com/hyz-is/arandu-activitylog"
)

// A committed domain event becomes an entry of its tenant -- its name, its
// aggregate, who authorized it and when -- once, however often the relay
// delivers it.
func TestEveryCommittedEventIsRecordedOnce(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	recorder := activitylog.NewEventRecorder(logger)
	recorder.Payload = true
	when := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	event := events.Stored{
		ID: "0199c7b2-1111-7000-8000-000000000001", TenantID: "acme", Name: "customer.created",
		Aggregate: "customer", AggregateID: "c1", AuthorizedBy: "user-acme", Action: "customer.create",
		OccurredAt: when, Payload: `{"email":"a@example.test"}`,
	}
	for i := 0; i < 2; i++ {
		if err := recorder.Publish(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	got := entries(t, svc, "acme", activitylog.Filter{LogNames: []string{activitylog.EventsLogName}})
	if len(got) != 1 {
		t.Fatalf("recorded %d entries for one event, want 1", len(got))
	}
	entry := got[0]
	if entry.ID != event.ID || entry.Description != "customer.created" || *entry.Event != "customer.created" {
		t.Errorf("entry = %+v", entry)
	}
	if entry.Subject() != (activitylog.Ref{Type: "customer", ID: "c1"}) || entry.Causer() != (activitylog.Ref{Type: "user", ID: "user-acme"}) {
		t.Errorf("subject %+v causer %+v", entry.Subject(), entry.Causer())
	}
	if !entry.CreatedAt.Equal(when) || entry.GetProperty("action", nil) != "customer.create" || entry.GetProperty("payload.email", nil) != "a@example.test" {
		t.Errorf("when %v properties %+v", entry.CreatedAt, entry.Properties())
	}
	if other := entries(t, svc, "globex", activitylog.Filter{}); len(other) != 0 {
		t.Error("the event reached another tenant's log")
	}
}

type refusingTransport struct{}

func (refusingTransport) Send(context.Context, mail.Message) error { return errors.New("mailbox full") }
func (refusingTransport) Name() string                             { return "refusing" }

// Every message handed to the watched transport is in the mail log: sent, or
// not sent with the reason, which the sender still gets.
func TestTheMailLogRecordsEveryMessage(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	outbox := &mail.Array{}
	watched, err := activitylog.NewMailTransport(outbox, logger, "platform")
	if err != nil {
		t.Fatal(err)
	}
	if watched.Unwrap() != outbox || watched.Name() != outbox.Name() {
		t.Error("the watched transport is not the one handed in")
	}
	message := mail.Message{Envelope: mail.Envelope{To: []mail.Address{{Email: "ana@example.test"}}, Subject: "Bem-vinda", Tags: []string{"welcome"}}}
	if err := watched.Send(ctx, message); err != nil {
		t.Fatal(err)
	}
	if len(outbox.Sent()) != 1 {
		t.Fatal("the message was not handed on")
	}
	failing, _ := activitylog.NewMailTransport(refusingTransport{}, logger, "platform")
	if err := failing.Send(ctx, message); err == nil || err.Error() != "mailbox full" {
		t.Errorf("the refusal was not answered to the sender: %v", err)
	}
	got := entries(t, svc, "platform", activitylog.Filter{LogNames: []string{activitylog.MailLogName}})
	if len(got) != 2 {
		t.Fatalf("logged %d messages, want 2", len(got))
	}
	if *got[0].Event != "sent" || got[0].Description != "Sent: Bem-vinda" || got[0].GetProperty("subject", nil) != "Bem-vinda" {
		t.Errorf("sent entry = %+v %+v", got[0], got[0].Properties())
	}
	if to, _ := got[0].GetProperty("to", nil).([]any); len(to) != 1 || to[0] != "ana@example.test" {
		t.Errorf("recipients = %v", got[0].GetProperty("to", nil))
	}
	if *got[1].Event != "failed" || got[1].GetProperty("error", nil) != "mailbox full" {
		t.Errorf("failed entry = %+v", got[1].Properties())
	}
	if _, err := activitylog.NewMailTransport(outbox, logger, ""); err == nil {
		t.Error("a mail log with no tenant was accepted")
	}
}
