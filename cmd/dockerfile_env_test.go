package cmd

import (
	"os"
	"testing"
)

// TestDockerfileEnvContract pins the environment variables the Dockerfile sets.
//
// The image relies on Viper auto-binding for WEB2API_<KEY> (see the BindEnv
// loop in initializeConfig), and nothing else verifies that contract: a key
// that loses its SetDefault silently stops being bindable, and the container
// would then start with an empty browser_path and let Rod download Chromium at
// runtime (which fails in this network-restricted, non-root image).
func TestDockerfileEnvContract(t *testing.T) {
	isolateConfig(t)
	// Setting server.host off-loopback also exercises the F-26 guard's input.
	t.Setenv("WEB2API_APP_BROWSER_PATH", "/headless-shell/headless-shell")
	t.Setenv("WEB2API_SERVER_HOST", "0.0.0.0")
	t.Setenv("WEB2API_SERVER_PORT", "7000")

	if err := initializeConfig(configCommand(t, "")); err != nil {
		t.Fatalf("initializeConfig: %v", err)
	}

	if got, want := config.App.BrowserPath, "/headless-shell/headless-shell"; got != want {
		t.Errorf("app.browser_path = %q, want %q (WEB2API_APP_BROWSER_PATH must bind)", got, want)
	}
	if got, want := config.Server.Host, "0.0.0.0"; got != want {
		t.Errorf("server.host = %q, want %q", got, want)
	}
	if got, want := config.Server.Port, 7000; got != want {
		t.Errorf("server.port = %d, want %d", got, want)
	}

	// The Dockerfile's default password is the shipped one; the container is
	// expected to be started with WEB2API_PLATFORM_ADMIN_PASSWORD. Verify that
	// binding too, so a rename cannot silently reopen the default-password hole.
	t.Setenv("WEB2API_PLATFORM_ADMIN_PASSWORD", "container-strong-password")
	if err := initializeConfig(configCommand(t, "")); err != nil {
		t.Fatalf("initializeConfig with admin password: %v", err)
	}
	if got, want := config.Platform.AdminPassword, "container-strong-password"; got != want {
		t.Errorf("platform.admin_password = %q, want %q", got, want)
	}
}

// TestContainerConfigShipsDefaults guards the config.yaml that the image copies
// in: it must stay readable and must not silently gain a non-loopback host that
// would trip the default-password refusal during `docker run` without envs.
func TestContainerConfigShipsDefaults(t *testing.T) {
	raw, err := os.ReadFile("../config.yaml")
	if err != nil {
		t.Fatalf("config.yaml is COPY'd into the image and must exist: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("config.yaml is empty")
	}
	for _, mustHave := range []string{"server:", "platform:", "app:"} {
		if !containsLine(raw, mustHave) {
			t.Errorf("config.yaml lost its %q section; the image depends on it", mustHave)
		}
	}
}

func containsLine(raw []byte, needle string) bool {
	for i := 0; i+len(needle) <= len(raw); i++ {
		if string(raw[i:i+len(needle)]) == needle {
			return true
		}
	}
	return false
}
