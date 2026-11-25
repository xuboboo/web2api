package cmd

import (
	"io"
	"testing"
)

func closeTestResource(t *testing.T, closer io.Closer) {
	t.Helper()
	if err := closer.Close(); err != nil {
		t.Errorf("close test resource: %v", err)
	}
}
