package platform

import "testing"

func TestLimiterTableCapsCardinality(t *testing.T) {
	table := newLimiterTable()
	for i := 0; i < maxLimiterEntries+100; i++ {
		table.allow("client-"+newUserID(), 60, 1)
	}
	if got := len(table.m); got != maxLimiterEntries {
		t.Fatalf("limiter table cardinality = %d, want <= %d", got, maxLimiterEntries)
	}
	if table.overflow == nil {
		t.Fatal("saturated table did not create a shared overflow limiter")
	}
}
