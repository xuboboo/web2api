package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ---------------------------------------------------------------------------
// Engine resolution
// ---------------------------------------------------------------------------

func TestResolveEngines(t *testing.T) {
	if got, ok := ResolveEngines(nil); !ok || strings.Join(got, ",") != "baidu,sogou,so360,bing" {
		t.Fatalf("default engines: got %v ok=%v", got, ok)
	}
	if got, ok := ResolveEngines([]string{"360"}); !ok || strings.Join(got, ",") != "so360" {
		t.Fatalf("alias 360: got %v ok=%v", got, ok)
	}
	if got, ok := ResolveEngines([]string{"bing", "BAIDU", "bing", " "}); !ok || strings.Join(got, ",") != "bing,baidu" {
		t.Fatalf("dedupe/lowercase: got %v ok=%v", got, ok)
	}
	if got, ok := ResolveEngines([]string{"google"}); ok || got != nil {
		t.Fatalf("unknown engine must fail: got %v ok=%v", got, ok)
	}
	if got, ok := ResolveEngines([]string{""}); ok || got != nil {
		t.Fatalf("blank-only list must fail: got %v ok=%v", got, ok)
	}
}

// ---------------------------------------------------------------------------
// JWT sign/verify
// ---------------------------------------------------------------------------

func TestJWTSignVerify(t *testing.T) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	token, err := signJWT(secret, "admin", time.Now().Add(time.Hour), 0)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	claims, err := verifyJWT(secret, token)
	if err != nil || claims.Sub != "admin" {
		t.Fatalf("verify: claims=%+v err=%v", claims, err)
	}

	if _, err := verifyJWT(secret, token+"x"); err == nil {
		t.Fatal("tampered signature must fail")
	}
	if _, err := verifyJWT([]byte("another-secret-another-secret"), token); err == nil {
		t.Fatal("wrong secret must fail")
	}
	expired, err := signJWT(secret, "admin", time.Now().Add(-time.Minute), 0)
	if err != nil {
		t.Fatalf("sign expired: %v", err)
	}
	if _, err := verifyJWT(secret, expired); err != ErrJWTExpired {
		t.Fatalf("expired token: want ErrJWTExpired got %v", err)
	}
	if _, err := verifyJWT(secret, "not-a-jwt"); err == nil {
		t.Fatal("garbage token must fail")
	}

	// Token version round-trips (契约 B: 改密码后旧 token 全失效).
	versioned, err := signJWT(secret, "admin", time.Now().Add(time.Hour), 7)
	if err != nil {
		t.Fatalf("sign versioned: %v", err)
	}
	claims, err = verifyJWT(secret, versioned)
	if err != nil || claims.TV != 7 {
		t.Fatalf("versioned token: claims=%+v err=%v", claims, err)
	}
}

// ---------------------------------------------------------------------------
// Stores: key lifecycle + usage counters
// ---------------------------------------------------------------------------

func testKey(id, name string, quota int64, createdAt time.Time) *APIKey {
	full, hash := generateAPIKeyMaterial()
	return &APIKey{
		ID:         id,
		Name:       name,
		KeyHash:    hash,
		Prefix:     full[:keyPrefixDisplayLen],
		Status:     KeyStatusActive,
		DailyQuota: quota,
		CreatedAt:  createdAt,
	}
}

func exerciseStore(t *testing.T, s Store) {
	t.Helper()

	k1 := testKey("id-1", "ci", 100, time.Now().Add(-2*time.Hour))
	k2 := testKey("id-2", "zzz", 0, time.Now().Add(-time.Hour))
	for _, k := range []*APIKey{k1, k2} {
		if err := s.PutKey(k); err != nil {
			t.Fatalf("PutKey: %v", err)
		}
	}

	got, err := s.GetKeyByID("id-1")
	if err != nil || got == nil || got.Name != "ci" {
		t.Fatalf("GetKeyByID: got=%+v err=%v", got, err)
	}
	if missing, err := s.GetKeyByID("nope"); err != nil || missing != nil {
		t.Fatalf("GetKeyByID missing: got=%v err=%v", missing, err)
	}
	byHash, err := s.GetKeyByHash(k2.KeyHash)
	if err != nil || byHash == nil || byHash.ID != "id-2" {
		t.Fatalf("GetKeyByHash: got=%+v err=%v", byHash, err)
	}

	keys, err := s.ListKeys()
	if err != nil || len(keys) != 2 {
		t.Fatalf("ListKeys: len=%d err=%v", len(keys), err)
	}
	if keys[0].ID != "id-2" || keys[1].ID != "id-1" {
		t.Fatalf("ListKeys order (newest first): %v", keys)
	}

	// RecordUsage: consume 2 units for id-1 → UsedToday=2, UsedTotal=2.
	at := time.Now()
	rec := &UsageRecord{Time: at, KeyID: "id-1", KeyName: "ci", Endpoint: "/v1/index/check", URLs: 2, Units: 2, Status: 200}
	if err := s.RecordUsage(rec, "id-1", 2, true, at); err != nil {
		t.Fatalf("RecordUsage: %v", err)
	}
	got, _ = s.GetKeyByID("id-1")
	if got.UsedToday != 2 || got.UsedTotal != 2 || got.LastUsedAt == nil {
		t.Fatalf("counters after consume: %+v", got)
	}

	// Non-consuming record (400 response) must not bump counters.
	rec2 := &UsageRecord{Time: at, KeyID: "id-1", Endpoint: "/v1/index/check", Status: 400}
	if err := s.RecordUsage(rec2, "id-1", 0, false, at); err != nil {
		t.Fatalf("RecordUsage non-consume: %v", err)
	}
	got, _ = s.GetKeyByID("id-1")
	if got.UsedToday != 2 || got.UsedTotal != 2 {
		t.Fatalf("counters after non-consume: %+v", got)
	}

	// Date rollover: pretend the key's counter belongs to yesterday.
	got.TodayDate = localDate(at.AddDate(0, 0, -1))
	got.UsedToday = 9
	if err := s.PutKey(got); err != nil {
		t.Fatalf("PutKey rollover setup: %v", err)
	}
	rec3 := &UsageRecord{Time: at, KeyID: "id-1", Endpoint: "/v1/index/check", Units: 3, Status: 200}
	if err := s.RecordUsage(rec3, "id-1", 3, true, at); err != nil {
		t.Fatalf("RecordUsage rollover: %v", err)
	}
	got, _ = s.GetKeyByID("id-1")
	if got.UsedToday != 3 || got.UsedTotal != 5 {
		t.Fatalf("counters after rollover: UsedToday=%d UsedTotal=%d", got.UsedToday, got.UsedTotal)
	}

	// Usage records ascending by seq; deleting a missing key reports false.
	all, err := s.AllUsage()
	if err != nil || len(all) != 3 || all[0].Seq >= all[2].Seq {
		t.Fatalf("AllUsage: len=%d err=%v", len(all), err)
	}

	// Aggregate push-down: 3 calls, 2+0+3 = 5 units, all today; the two 200
	// calls are the OK rows of the last 24h.
	agg, err := s.UsageAggregate()
	if err != nil {
		t.Fatalf("UsageAggregate: %v", err)
	}
	if agg.TotalCalls != 3 || agg.TotalUnits != 5 || agg.TodayCalls != 3 || agg.TodayUnits != 5 || agg.Calls24h != 3 || agg.OK24h != 2 {
		t.Fatalf("UsageAggregate: %+v", agg)
	}

	// Timeseries: today's bucket carries all calls; older days stay zero-filled.
	series, err := s.UsageTimeseries(3)
	if err != nil || len(series) != 3 {
		t.Fatalf("UsageTimeseries: len=%d err=%v", len(series), err)
	}
	last := series[len(series)-1]
	if last.Date != localDate(at) || last.Calls != 3 || last.Units != 5 || last.OK != 2 {
		t.Fatalf("UsageTimeseries today: %+v (want date=%s)", last, localDate(at))
	}
	if series[0].Calls != 0 {
		t.Fatalf("UsageTimeseries first day zero-filled: %+v", series[0])
	}

	// Pagination push-down: newest first, filtered total independent of limit.
	page, total, err := s.ListUsagePage(UsagePageFilter{Limit: 2})
	if err != nil || total != 3 || len(page) != 2 {
		t.Fatalf("ListUsagePage: len=%d total=%d err=%v", len(page), total, err)
	}
	if page[0].Seq != all[2].Seq || page[1].Seq != all[1].Seq {
		t.Fatalf("ListUsagePage order: %v", page)
	}
	page, total, err = s.ListUsagePage(UsagePageFilter{KeyID: "id-1", Offset: 1, Limit: 10})
	if err != nil || total != 3 || len(page) != 2 {
		t.Fatalf("ListUsagePage key filter: len=%d total=%d err=%v", len(page), total, err)
	}
	page, total, err = s.ListUsagePage(UsagePageFilter{KeyID: "id-2"})
	if err != nil || total != 0 || len(page) != 0 {
		t.Fatalf("ListUsagePage absent key: total=%d err=%v", total, err)
	}

	// Pruning removes only records older than the cutoff (滚动清理上限).
	oldAt := at.Add(-48 * time.Hour)
	oldRec := &UsageRecord{Time: oldAt, KeyID: "id-1", Endpoint: "/v1/index/check", Units: 1, Status: 200}
	if err := s.RecordUsage(oldRec, "id-1", 0, false, oldAt); err != nil {
		t.Fatalf("RecordUsage old: %v", err)
	}
	removed, err := s.PruneUsageBefore(at.Add(-24 * time.Hour))
	if err != nil || removed != 1 {
		t.Fatalf("PruneUsageBefore: removed=%d err=%v", removed, err)
	}
	if all, err := s.AllUsage(); err != nil || len(all) != 3 {
		t.Fatalf("AllUsage after prune: len=%d err=%v (4 records before, 1 removed)", len(all), err)
	}

	if existed, err := s.DeleteKey("id-2"); err != nil || !existed {
		t.Fatalf("DeleteKey: existed=%v err=%v", existed, err)
	}
	if existed, err := s.DeleteKey("id-2"); err != nil || existed {
		t.Fatalf("DeleteKey twice: existed=%v err=%v", existed, err)
	}

	// Settings roundtrip.
	if err := s.PutSetting(settingAdminHash, []byte("hash-bytes")); err != nil {
		t.Fatalf("PutSetting: %v", err)
	}
	if v, err := s.GetSetting(settingAdminHash); err != nil || string(v) != "hash-bytes" {
		t.Fatalf("GetSetting: got=%q err=%v", v, err)
	}
	if v, err := s.GetSetting("absent"); err != nil || v != nil {
		t.Fatalf("GetSetting absent: got=%q err=%v", v, err)
	}
}

func TestBoltStore(t *testing.T) {
	dir := t.TempDir()
	s, err := newBoltStore(filepath.Join(dir, "web2api.db"))
	if err != nil {
		t.Fatalf("open bolt store: %v", err)
	}
	defer s.Close()
	exerciseStore(t, s)
}

func TestJSONStore(t *testing.T) {
	dir := t.TempDir()
	s, err := newJSONStore(filepath.Join(dir, "web2api.json"))
	if err != nil {
		t.Fatalf("open json store: %v", err)
	}
	exerciseStore(t, s)
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Reopen: data must persist across restarts.
	s2, err := newJSONStore(filepath.Join(dir, "web2api.json"))
	if err != nil {
		t.Fatalf("reopen json store: %v", err)
	}
	defer s2.Close()
	keys, err := s2.ListKeys()
	if err != nil || len(keys) != 1 || keys[0].ID != "id-1" || keys[0].UsedTotal != 5 {
		t.Fatalf("reopen persistence: keys=%+v err=%v", keys, err)
	}
}

// ---------------------------------------------------------------------------
// Platform construction: secret reuse, admin hash, JSON fallback
// ---------------------------------------------------------------------------

func TestNewReusesSecretAndHash(t *testing.T) {
	dir := t.TempDir()
	cfg := PlatformConfig{Enabled: true, DataDir: dir, AdminUsername: "admin", AdminPassword: "pw-123", JWTExpireHours: 24}

	p1, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p1.storeKind != "bbolt" {
		t.Fatalf("store kind: %s", p1.storeKind)
	}
	token, err := p1.issueToken("admin")
	if err != nil {
		t.Fatalf("issueToken: %v", err)
	}
	if err := p1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "secret.key")); err != nil {
		t.Fatalf("secret.key must exist: %v", err)
	}

	p2, err := New(cfg, nil)
	if err != nil {
		t.Fatalf("New again: %v", err)
	}
	defer p2.Close()
	if _, err := verifyJWT(p2.secret, token); err != nil {
		t.Fatalf("token from p1 must verify under p2 (secret reuse): %v", err)
	}
	// Wrong admin password still rejected after restart.
	if bcryptErr := compareAdminPassword(p2, "wrong"); bcryptErr == nil {
		t.Fatal("wrong password must not compare equal")
	}
	if bcryptErr := compareAdminPassword(p2, "pw-123"); bcryptErr != nil {
		t.Fatalf("configured password must verify: %v", bcryptErr)
	}
}

func compareAdminPassword(p *Platform, password string) error {
	return bcrypt.CompareHashAndPassword(p.adminHash, []byte(password))
}

// TestNewFallsBackToJSONWhenBoltBlocked verifies the contract fallback: when
// web2api.db cannot be opened (here: it is a directory), New switches to the
// equivalent JSON store with the same interface.
func TestNewFallsBackToJSONWhenBoltBlocked(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "web2api.db"), 0o755); err != nil {
		t.Fatalf("block bbolt path: %v", err)
	}

	p, err := New(PlatformConfig{Enabled: true, DataDir: dir, AdminUsername: "admin", AdminPassword: "pw-123", JWTExpireHours: 24}, nil)
	if err != nil {
		t.Fatalf("New with blocked bbolt: %v", err)
	}
	defer p.Close()
	if p.storeKind != "json" {
		t.Fatalf("store kind: want json got %s", p.storeKind)
	}

	// The fallback store must be fully functional.
	full, hash := generateAPIKeyMaterial()
	key := &APIKey{ID: "k1", Name: "n", KeyHash: hash, Prefix: full[:keyPrefixDisplayLen], Status: KeyStatusActive, CreatedAt: time.Now()}
	if err := p.store.PutKey(key); err != nil {
		t.Fatalf("PutKey on fallback: %v", err)
	}
	if got, _ := p.store.GetKeyByHash(hash); got == nil {
		t.Fatal("GetKeyByHash on fallback failed")
	}
}
