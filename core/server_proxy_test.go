package core

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
)

func TestTrustedProxyClientIP(t *testing.T) {
	opts := DefaultServerOptions()
	opts.TrustProxyHeaders = true
	opts.TrustedProxies = []string{"0.0.0.0"}
	serv := NewServerWithOptions("127.0.0.1", 0, opts)
	serv.app.Get("/remote-ip", func(c *fiber.Ctx) error {
		return c.SendString(c.IP())
	})

	req := httptest.NewRequest("GET", "/remote-ip", nil)
	req.Header.Set("X-Real-IP", "198.51.100.42")
	req.Header.Set("X-Forwarded-For", "203.0.113.99")
	resp, err := serv.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(body)); got != "198.51.100.42" {
		t.Fatalf("trusted proxy client IP = %q", got)
	}
}

func TestUntrustedPeerCannotSpoofClientIP(t *testing.T) {
	opts := DefaultServerOptions()
	opts.TrustProxyHeaders = true
	opts.TrustedProxies = []string{"192.0.2.1"}
	serv := NewServerWithOptions("127.0.0.1", 0, opts)
	serv.app.Get("/remote-ip", func(c *fiber.Ctx) error {
		return c.SendString(c.IP())
	})

	req := httptest.NewRequest("GET", "/remote-ip", nil)
	req.Header.Set("X-Real-IP", "198.51.100.99")
	resp, err := serv.app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer closeTestResource(t, resp.Body)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(body)); got == "198.51.100.99" {
		t.Fatal("untrusted peer spoofed the client IP")
	}
}
