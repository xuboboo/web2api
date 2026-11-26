package core

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
)

func TestValidatePublicHTTPURLRejectsPrivateTargets(t *testing.T) {
	tests := []string{
		"http://127.0.0.1/",
		"http://[::1]/",
		"http://10.0.0.1/",
		"http://172.16.0.1/",
		"http://192.168.1.1/",
		"http://169.254.169.254/",
		"http://100.64.0.1/",
	}

	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			err := ValidatePublicHTTPURL(context.Background(), rawURL)
			if !errors.Is(err, ErrTargetNotAllowed) {
				t.Fatalf("expected ErrTargetNotAllowed, got %v", err)
			}
			if !strings.Contains(strings.ToLower(err.Error()), "non-public") {
				t.Fatalf("expected non-public error, got %v", err)
			}
		})
	}
}

func TestIsPublicIPRejectsSpecialPurposeAndTranslationAddresses(t *testing.T) {
	blocked := []string{
		"0.1.2.3", "192.0.2.1", "198.18.0.1", "198.51.100.1",
		"203.0.113.1", "240.0.0.1", "2001:db8::1", "2001:2::1",
		"64:ff9b::a9fe:a9fe", "64:ff9b:1::a9fe:a9fe", "2002:a9fe:a9fe::1",
		"::ffff:169.254.169.254",
	}
	for _, raw := range blocked {
		t.Run(raw, func(t *testing.T) {
			ip := netip.MustParseAddr(raw)
			if isPublicIP(ip) {
				t.Fatalf("special-purpose address %s was allowed", ip)
			}
		})
	}
	for _, raw := range []string{"1.1.1.1", "2606:4700:4700::1111"} {
		t.Run("public_"+raw, func(t *testing.T) {
			ip := netip.MustParseAddr(raw)
			if !isPublicIP(ip) {
				t.Fatalf("public address %s was rejected", ip)
			}
		})
	}
}

func TestValidatePublicHTTPURLRejectsUnsupportedScheme(t *testing.T) {
	err := ValidatePublicHTTPURL(context.Background(), "file:///etc/passwd")
	if !errors.Is(err, ErrTargetNotAllowed) {
		t.Fatalf("expected ErrTargetNotAllowed, got %v", err)
	}
	if !strings.Contains(err.Error(), "only http and https") {
		t.Fatalf("unexpected error: %v", err)
	}
}
