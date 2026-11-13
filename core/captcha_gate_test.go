package core

import (
	"testing"
	"time"
)

func TestCaptchaGateTripsAndExpires(t *testing.T) {
	var gate CaptchaGate
	if gate.Blocked() {
		t.Fatal("a fresh gate must not be blocked")
	}

	gate.Trip(30 * time.Millisecond)
	if !gate.Blocked() {
		t.Fatal("gate must be blocked right after Trip")
	}

	time.Sleep(40 * time.Millisecond)
	if gate.Blocked() {
		t.Fatal("gate must expire after its TTL")
	}
}

func TestCaptchaGateTripNeverShortens(t *testing.T) {
	var gate CaptchaGate
	gate.Trip(time.Minute)
	gate.Trip(10 * time.Millisecond)
	if !gate.Blocked() {
		t.Fatal("a shorter Trip must not shorten the existing gate window")
	}
}

func TestCaptchaGateClear(t *testing.T) {
	var gate CaptchaGate
	gate.Trip(time.Minute)
	gate.Clear()
	if gate.Blocked() {
		t.Fatal("Clear must release the gate")
	}
}

func TestCaptchaGateNilReceiver(t *testing.T) {
	var gate *CaptchaGate
	if gate.Blocked() {
		t.Fatal("nil gate must report unblocked")
	}
	gate.Trip(time.Second)
	gate.Clear()
}

func TestCaptchaGateZeroTTLUsesDefault(t *testing.T) {
	var gate CaptchaGate
	gate.Trip(0)
	if !gate.Blocked() {
		t.Fatal("Trip(0) must fall back to the default TTL")
	}
	gate.Clear()
}
