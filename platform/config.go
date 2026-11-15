// Package platform implements the Web2API 收录查询平台 back end: API key
// storage (bbolt, JSON fallback), JWT admin login, per-key daily quotas and
// usage statistics, plus the /v1/* public and /api/* admin route registration
// described in the product contract.
package platform

import "strings"

//
// Integration contract (for the integrator):
//
//	cfg := platform.PlatformConfig{...}        // mapped from config.yaml `platform:` node
//	p, err := platform.New(cfg, engineStatus)  // engineStatus func() any supplies engine/circuit-breaker state
//	p.SetSecurity(securityCfg)                 // security node (契约 B): rate limit + login lockout
//	app.Get("/", ...static web/admin...)
//	p.RegisterAdmin(app, indexCheck, engineControl) // mounts /api/* + playground + cooldown admin (engineControl = *core.Server)
//	p.RegisterPublic(app, indexCheckHandler)   // mounts /v1/* (index/check GET+POST, quota, engines)
//	mcp.Register(app.Group(base), p.AuthAny(), mcp.Deps{...}) // agent plane (契约 C)
//	defer p.Close()

// PlatformConfig maps the fixed `platform:` section of config.yaml.
// Field names must not drift from the product contract:
//
//	platform:
//	  enabled: true
//	  data_dir: "./data"
//	  admin_username: "admin"
//	  admin_password: "admin123"
//	  jwt_expire_hours: 24
//	  usage_retention_days: 30
type PlatformConfig struct {
	Enabled        bool   `mapstructure:"enabled"`
	DataDir        string `mapstructure:"data_dir"`
	AdminUsername  string `mapstructure:"admin_username"`
	AdminPassword  string `mapstructure:"admin_password"`
	JWTExpireHours int    `mapstructure:"jwt_expire_hours"`
	// UsageRetentionDays caps the usage journal: records older than this many
	// days are pruned at startup and periodically as usage is written.
	// 0 = unlimited (keep everything).
	UsageRetentionDays int `mapstructure:"usage_retention_days"`
}

// DefaultPlatformConfig returns the contract defaults for the platform section.
func DefaultPlatformConfig() PlatformConfig {
	return PlatformConfig{
		Enabled:            true,
		DataDir:            "./data",
		AdminUsername:      "admin",
		AdminPassword:      "admin123",
		JWTExpireHours:     24,
		UsageRetentionDays: 30,
	}
}

// normalized fills empty/invalid fields with the contract defaults so the
// platform keeps working when the integrator omits part of the config node.
func (c PlatformConfig) normalized() PlatformConfig {
	if strings.TrimSpace(c.DataDir) == "" {
		c.DataDir = DefaultPlatformConfig().DataDir
	}
	c.DataDir = strings.TrimSpace(c.DataDir)
	if strings.TrimSpace(c.AdminUsername) == "" {
		c.AdminUsername = DefaultPlatformConfig().AdminUsername
	}
	c.AdminUsername = strings.TrimSpace(c.AdminUsername)
	if c.AdminPassword == "" {
		c.AdminPassword = DefaultPlatformConfig().AdminPassword
	}
	if c.JWTExpireHours <= 0 {
		c.JWTExpireHours = DefaultPlatformConfig().JWTExpireHours
	}
	if c.UsageRetentionDays < 0 {
		c.UsageRetentionDays = DefaultPlatformConfig().UsageRetentionDays
	}
	return c
}
