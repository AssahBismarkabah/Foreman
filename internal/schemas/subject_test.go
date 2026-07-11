package schemas

import (
	"testing"
)

func TestSubject(t *testing.T) {
	tests := []struct {
		parts []string
		want  string
	}{
		{[]string{}, "foreman"},
		{[]string{"session"}, "foreman.session"},
		{[]string{"session", "ses_1"}, "foreman.session.ses_1"},
		{[]string{"session", "ses_1", "created"}, "foreman.session.ses_1.created"},
		{[]string{"agent", "heartbeat"}, "foreman.agent.heartbeat"},
		{[]string{"plugin", "slack", "message"}, "foreman.plugin.slack.message"},
	}
	for _, tt := range tests {
		got := Subject(tt.parts...)
		if got != tt.want {
			t.Errorf("Subject(%v) = %q, want %q", tt.parts, got, tt.want)
		}
	}
}

func TestSessionSubject(t *testing.T) {
	got := SessionSubject("ses_abc")
	want := "foreman.session.ses_abc"
	if got != want {
		t.Errorf("SessionSubject = %q, want %q", got, want)
	}
}

func TestSessionEventSubject(t *testing.T) {
	got := SessionEventSubject("ses_1", EvSessionCreated)
	want := "foreman.session.ses_1.session.created"
	if got != want {
		t.Errorf("SessionEventSubject = %q, want %q", got, want)
	}
}
