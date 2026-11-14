//go:build integration
// +build integration

package baidu

import (
	"testing"

	"github.com/xuboboo/web2api/core"
	"github.com/xuboboo/web2api/testutil/ithelper"
)

func TestSearchBaidu(t *testing.T) {
	ithelper.RunEngineTests(t, func(b *core.Browser) core.SearchEngine {
		return New(*b, ithelper.EngineOptions())
	})
}
