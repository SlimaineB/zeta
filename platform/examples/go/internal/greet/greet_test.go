package greet

import "testing"

func TestMessage(t *testing.T) {
	if got := Message("Go"); got != "hello from Go" {
		t.Fatalf("Message() = %q", got)
	}
}
