package feature_test

import (
	"context"
	"errors"
	"strings"
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
	if got[0].GetProperty("html", nil) != nil || got[0].GetProperty("text", nil) != nil {
		t.Error("a transport without a redactor kept the body")
	}
}

// The body is kept only as the application's redactor returns it: the code
// the message carried never reaches the log, and a message the redactor
// declines keeps the envelope alone.
func TestTheMailLogKeepsTheRedactedBody(t *testing.T) {
	db := openDatabase(t)
	logger := newLogger(t, db, activitylog.Config{})
	svc := newService(t, db, activitylog.Config{})
	ctx := context.Background()
	outbox := &mail.Array{}
	watched, err := activitylog.NewMailTransport(outbox, logger, "platform")
	if err != nil {
		t.Fatal(err)
	}
	watched.WithBody(func(m mail.Message) (activitylog.MailBody, bool) {
		if len(m.Tags) > 0 && m.Tags[0] == "private" {
			return activitylog.MailBody{}, false
		}
		return activitylog.MailBody{HTML: strings.ReplaceAll(m.HTML, "123456", "[redacted]"), Text: strings.ReplaceAll(m.Text, "123456", "[redacted]")}, true
	})
	coded := mail.Message{Envelope: mail.Envelope{To: []mail.Address{{Email: "ana@example.test"}}, Subject: "Seu código", Tags: []string{"code"}},
		HTML: "<p>Seu código é <b>123456</b></p>", Text: "Seu código é 123456"}
	if err := watched.Send(ctx, coded); err != nil {
		t.Fatal(err)
	}
	private := coded
	private.Subject, private.Tags = "Particular", []string{"private"}
	if err := watched.Send(ctx, private); err != nil {
		t.Fatal(err)
	}
	if sent := outbox.Sent(); len(sent) != 2 || !strings.Contains(sent[0].HTML, "123456") {
		t.Fatal("the redaction reached the message handed on")
	}
	got := entries(t, svc, "platform", activitylog.Filter{LogNames: []string{activitylog.MailLogName}})
	if len(got) != 2 {
		t.Fatalf("logged %d messages, want 2", len(got))
	}
	byTag := map[string]*activitylog.Activity{}
	for _, entry := range got {
		subject, _ := entry.GetProperty("subject", "").(string)
		byTag[subject] = entry
	}
	kept := byTag["Seu código"]
	if html, _ := kept.GetProperty("html", "").(string); html != "<p>Seu código é <b>[redacted]</b></p>" {
		t.Errorf("html = %q", html)
	}
	if text, _ := kept.GetProperty("text", "").(string); text != "Seu código é [redacted]" || kept.GetProperty("body_redacted", nil) != true {
		t.Errorf("text = %q, redacted = %v", text, kept.GetProperty("body_redacted", nil))
	}
	if byTag["Particular"].GetProperty("html", nil) != nil {
		t.Error("a message the redactor declined kept its body")
	}
}
