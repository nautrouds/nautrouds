package main

import "testing"

func TestSocketNamer_NoInstanceID_DoesNotOwnOtherInstanceSockets(t *testing.T) {
	namer := newSocketNamer("")

	if got := namer.Format(0); got != "nautrouds-0.sock" {
		t.Fatalf("Format(0) = %q, want %q", got, "nautrouds-0.sock")
	}
	if !namer.Owns("nautrouds-0.sock") {
		t.Fatalf("expected to own %q", "nautrouds-0.sock")
	}
	if namer.Owns("nautrouds-abc-0.sock") {
		t.Fatalf("must not own another instance's socket %q", "nautrouds-abc-0.sock")
	}
}

func TestSocketNamer_WithInstanceID_DoesNotOwnPrefixCollisions(t *testing.T) {
	namer := newSocketNamer("abc")

	if got := namer.Format(0); got != "nautrouds-abc-0.sock" {
		t.Fatalf("Format(0) = %q, want %q", got, "nautrouds-abc-0.sock")
	}
	if !namer.Owns("nautrouds-abc-0.sock") {
		t.Fatalf("expected to own %q", "nautrouds-abc-0.sock")
	}
	if namer.Owns("nautrouds-abcd-0.sock") {
		t.Fatalf("must not own another instance's socket %q", "nautrouds-abcd-0.sock")
	}
	if namer.Owns("nautrouds-0.sock") {
		t.Fatalf("must not own no-instance-id socket %q", "nautrouds-0.sock")
	}
}
