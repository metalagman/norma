package session

import "testing"

func TestTopicSessionGetSessionIDReturnsTransportSessionID(t *testing.T) {
	ts := &TopicSession{
		sessionID: "tg-2317500-0",
		userID:    "tg-2317500",
	}

	if got := ts.GetSessionID(); got != "tg-2317500-0" {
		t.Fatalf("GetSessionID() = %q, want tg-2317500-0", got)
	}
	if got := ts.GetUserID(); got != "tg-2317500" {
		t.Fatalf("GetUserID() = %q, want tg-2317500", got)
	}
}
