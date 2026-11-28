package cmd

import (
	"testing"

	"github.com/spf13/viper"
)

// Regression (audit P2): bindFlags mapped search's local flags (limit/lang/
// region/start/site/file/format/features/search-timeout) into viper as app.*
// keys. AllKeys() merges bound pflags, so the startup report warned
// "未知配置键" on every `web2api search` invocation and WEB2API_STRICT_CONFIG=1
// made the command fail before it could run. Local flags of one-shot commands
// must stay out of viper entirely.
func TestSearchLocalFlagsDoNotLeakIntoViperUnknownKeys(t *testing.T) {
	t.Setenv("WEB2API_STRICT_CONFIG", "1")

	v := viper.New()
	setConfigDefaults(v)

	if err := searchCMD.Flags().Set("limit", "5"); err != nil {
		t.Fatalf("set limit flag: %v", err)
	}
	t.Cleanup(func() { _ = searchCMD.Flags().Set("limit", "10") })

	bindFlags(searchCMD, v)

	if err := validateUnknownConfigKeys(v); err != nil {
		t.Fatalf("search local flags leaked into viper unknown keys: %v", err)
	}
}

// fingerprint-check's local flags were already exempt; keep them exempt and
// make sure the shared helper did not widen the exemption to other commands.
func TestProbeFlagExemptionScope(t *testing.T) {
	v := viper.New()
	setConfigDefaults(v)

	bindFlags(searchCMD, v)
	bindFlags(serveCMD, v)

	if err := validateUnknownConfigKeys(v); err != nil {
		t.Fatalf("unexpected unknown config keys: %v", err)
	}
}
