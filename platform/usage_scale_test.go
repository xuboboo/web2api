package platform

// P2 scale tests: with a 10k-record usage journal, /api/stats/overview and
// /api/usage must stay fast (store-layer aggregate/page push-down, no full
// materialization per request) and the retention cap must bound the journal.

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

// fillUsageJournal appends n usage records through the store, alternating
// engines and statuses. Used by the bbolt heavy test where each RecordUsage
// commits one transaction (insert throughput is part of the evidence there).
func fillUsageJournal(t *testing.T, s Store, n int, baseTime time.Time) {
	t.Helper()
	for i := 0; i < n; i++ {
		rec := scaleRecord(i, baseTime)
		if err := s.RecordUsage(rec, "scale-key", 0, false, rec.Time); err != nil {
			t.Fatalf("fill record %d: %v", i, err)
		}
	}
}

// scaleRecord builds fixture record i.
func scaleRecord(i int, baseTime time.Time) *UsageRecord {
	engineStat := map[string]EngineStat{
		"baidu": {Calls: 2, OK: 1},
		"bing":  {Calls: 2, OK: 2},
	}
	status := fiber.StatusOK
	if i%10 == 0 {
		status = fiber.StatusBadGateway
	}
	return &UsageRecord{
		Time:        baseTime.Add(time.Duration(i) * time.Second),
		KeyID:       "scale-key",
		KeyName:     "scale",
		Endpoint:    "/v1/index/check",
		Engines:     []string{"baidu", "bing"},
		URLs:        1,
		Units:       4,
		Status:      status,
		TookMS:      20,
		EngineStats: engineStat,
	}
}

// newBulkJSONStore builds a JSON store holding n usage records in ONE save —
// 10k sequential RecordUsage calls would re-marshal the whole document per
// insert (O(n²) fixture cost) while the subject under test is the read path.
func newBulkJSONStore(t *testing.T, path string, n int, baseTime time.Time) *jsonStore {
	t.Helper()
	s := &jsonStore{
		path: path,
		data: jsonStoreData{
			Keys:     map[string]*APIKey{},
			Usage:    make([]UsageRecord, 0, n),
			Settings: map[string]string{},
		},
	}
	for i := 0; i < n; i++ {
		rec := *scaleRecord(i, baseTime)
		s.data.Seq++
		rec.Seq = int64(s.data.Seq)
		s.data.Usage = append(s.data.Usage, rec)
	}
	if err := s.saveLocked(); err != nil {
		t.Fatalf("save bulk store: %v", err)
	}
	return s
}

// TestUsageScaleQueryPushdown runs the admin read paths against a 10k-record
// JSON-store journal: page size stays bounded and the aggregate matches the
// fixture. It also prints the measured latencies so regressions are visible.
func TestUsageScaleQueryPushdown(t *testing.T) {
	dir := t.TempDir()
	s := newBulkJSONStore(t, filepath.Join(dir, "web2api.json"), 10000, time.Now().Add(-time.Hour))
	defer s.Close()

	const total = 10000

	started := time.Now()
	agg, err := s.UsageAggregate()
	if err != nil {
		t.Fatalf("UsageAggregate: %v", err)
	}
	aggregateTook := time.Since(started)
	if agg.TotalCalls != total || agg.TotalUnits != int64(total*4) {
		t.Fatalf("aggregate totals: calls=%d units=%d, want %d/%d", agg.TotalCalls, agg.TotalUnits, total, total*4)
	}
	// Every record carries baidu{2,1} and bing{2,2} in its engine stats.
	if agg.ByEngine["baidu"].Calls != int64(total*2) || agg.ByEngine["baidu"].OK != int64(total) {
		t.Fatalf("aggregate baidu: %+v", agg.ByEngine["baidu"])
	}
	if agg.ByEngine["bing"].Calls != int64(total*2) || agg.ByEngine["bing"].OK != int64(total*2) {
		t.Fatalf("aggregate bing: %+v", agg.ByEngine["bing"])
	}

	started = time.Now()
	page, got, err := s.ListUsagePage(UsagePageFilter{Offset: 5000, Limit: 50})
	if err != nil {
		t.Fatalf("ListUsagePage: %v", err)
	}
	pageTook := time.Since(started)
	if got != total || len(page) != 50 {
		t.Fatalf("page: got=%d len=%d, want %d/50", got, len(page), total)
	}
	// Newest first: the last inserted record has the highest seq.
	if page[0].Seq < page[1].Seq {
		t.Fatalf("page order: %d then %d", page[0].Seq, page[1].Seq)
	}

	started = time.Now()
	series, err := s.UsageTimeseries(14)
	if err != nil {
		t.Fatalf("UsageTimeseries: %v", err)
	}
	timeseriesTook := time.Since(started)
	var seriesCalls int
	for _, day := range series {
		seriesCalls += day.Calls
	}
	if seriesCalls != total {
		t.Fatalf("timeseries calls=%d, want %d", seriesCalls, total)
	}

	t.Logf("scale=%d aggregate=%s page(50,offset=5000)=%s timeseries(14d)=%s",
		total, aggregateTook, pageTook, timeseriesTook)

	// Generous guardrails: a regression to full materialization + sort per
	// request shows up here long before it shows up for users.
	if aggregateTook > 2*time.Second || pageTook > 2*time.Second || timeseriesTook > 2*time.Second {
		t.Fatalf("scale regression: aggregate=%s page=%s timeseries=%s", aggregateTook, pageTook, timeseriesTook)
	}
}

// TestBoltUsageScale10k is the bbolt variant of the scale check (10k sequential
// inserts each commit one transaction, so it is too slow for every CI run).
// Run it explicitly with WEB2API_HEAVY_SCALE=1 for the acceptance evidence:
//
//	WEB2API_HEAVY_SCALE=1 go test ./platform -run TestBoltUsageScale10k -v
func TestBoltUsageScale10k(t *testing.T) {
	if os.Getenv("WEB2API_HEAVY_SCALE") != "1" {
		t.Skip("set WEB2API_HEAVY_SCALE=1 to run the 10k-record bbolt scale test")
	}
	dir := t.TempDir()
	s, err := newBoltStore(filepath.Join(dir, "web2api.db"))
	if err != nil {
		t.Fatalf("open bolt: %v", err)
	}
	defer s.Close()

	baseTime := time.Now().Add(-time.Hour)
	fillStart := time.Now()
	fillUsageJournal(t, s, 10000, baseTime)
	t.Logf("fill 10k bolt records took %s", time.Since(fillStart))

	started := time.Now()
	agg, err := s.UsageAggregate()
	if err != nil {
		t.Fatalf("UsageAggregate: %v", err)
	}
	aggregateTook := time.Since(started)
	if agg.TotalCalls != 10000 || agg.TotalUnits != 40000 {
		t.Fatalf("aggregate: calls=%d units=%d", agg.TotalCalls, agg.TotalUnits)
	}

	started = time.Now()
	page, got, err := s.ListUsagePage(UsagePageFilter{Offset: 0, Limit: 50})
	if err != nil {
		t.Fatalf("ListUsagePage: %v", err)
	}
	pageTook := time.Since(started)
	if got != 10000 || len(page) != 50 {
		t.Fatalf("page: got=%d len=%d", got, len(page))
	}

	started = time.Now()
	series, err := s.UsageTimeseries(14)
	if err != nil {
		t.Fatalf("UsageTimeseries: %v", err)
	}
	timeseriesTook := time.Since(started)
	_ = series

	// The retention cap bounds the journal: exactly the fixture records older
	// than the cutoff disappear. The fixture spreads 10k records over ~2.8h
	// (1s apart), so compute the expected count from the SAME baseTime the
	// fixture used instead of hardcoding it — the fill loop's real duration
	// shifts `now` between fixture and cutoff.
	cutoff := time.Now().Add(-30 * time.Minute)
	wantRemoved := 0
	for i := 0; i < 10000; i++ {
		if baseTime.Add(time.Duration(i) * time.Second).Before(cutoff) {
			wantRemoved++
		}
	}
	removed, err := s.PruneUsageBefore(cutoff)
	if err != nil {
		t.Fatalf("PruneUsageBefore: %v", err)
	}
	if removed != int64(wantRemoved) {
		t.Fatalf("pruned=%d, want %d (records older than cutoff)", removed, wantRemoved)
	}

	t.Logf("bolt scale=10000 aggregate=%s page=%s timeseries=%s", aggregateTook, pageTook, timeseriesTook)
}

// TestAdminEndpointsStayFastWithLargeJournal walks the actual HTTP endpoints
// against the 10k JSON journal and decodes the envelopes end to end.
func TestAdminEndpointsStayFastWithLargeJournal(t *testing.T) {
	dir := t.TempDir()
	p, err := New(PlatformConfig{
		Enabled: true, DataDir: dir, AdminUsername: "admin", AdminPassword: "pw-123",
		JWTExpireHours: 24, UsageRetentionDays: 30,
	}, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer p.Close()

	// Swap in a store pre-filled with 10k records (built in one save so the
	// fixture itself stays fast; the subject is the read path). The store the
	// platform opened at construction is closed explicitly — p.Close() at test
	// end only sees the replacement, and a leaked bolt handle would block the
	// TempDir cleanup on Windows.
	bulkPath := filepath.Join(dir, "bulk.json")
	newBulkJSONStore(t, bulkPath, 10000, time.Now().Add(-time.Hour))
	originalStore := p.store
	if err := originalStore.Close(); err != nil {
		t.Fatalf("close original store: %v", err)
	}
	p.store, err = newJSONStore(bulkPath)
	if err != nil {
		t.Fatalf("reopen bulk store: %v", err)
	}

	app := fiber.New()
	p.RegisterAdmin(app, nil, nil)
	token := login(t, app)

	// overview
	started := time.Now()
	status, body := doJSON(t, app, http.MethodGet, "/api/stats/overview", nil, adminHeaders(token))
	overviewTook := time.Since(started)
	if status != http.StatusOK {
		t.Fatalf("overview: status=%d body=%v", status, body)
	}
	if body["total_calls"] != float64(10000) || body["total_units"] != float64(40000) {
		t.Fatalf("overview totals: %v", body)
	}

	// usage page
	started = time.Now()
	status, body = doJSON(t, app, http.MethodGet, "/api/usage?limit=50&offset=0", nil, adminHeaders(token))
	usageTook := time.Since(started)
	if status != http.StatusOK {
		t.Fatalf("usage: status=%d", status)
	}
	if body["total"] != float64(10000) {
		t.Fatalf("usage total: %v", body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 50 {
		t.Fatalf("usage items: %d", len(items))
	}

	// timeseries
	started = time.Now()
	status, _ = doJSON(t, app, http.MethodGet, "/api/stats/timeseries?days=14", nil, adminHeaders(token))
	timeseriesTook := time.Since(started)
	if status != http.StatusOK {
		t.Fatalf("timeseries: status=%d", status)
	}

	t.Logf("endpoints at 10k records: overview=%s usage=%s timeseries=%s", overviewTook, usageTook, timeseriesTook)
	if overviewTook > 2*time.Second || usageTook > 2*time.Second || timeseriesTook > 2*time.Second {
		t.Fatalf("endpoint scale regression: overview=%s usage=%s timeseries=%s", overviewTook, usageTook, timeseriesTook)
	}
}
