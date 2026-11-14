//go:build integration
// +build integration

package duckduckgo

import (
	"testing"

	"github.com/xuboboo/web2api/core"
	"github.com/xuboboo/web2api/testutil/ithelper"
)

func TestSearchDuckDuckGo(t *testing.T) {
	ithelper.RunEngineTests(t, func(b *core.Browser) core.SearchEngine {
		return New(*b, ithelper.EngineOptions())
	})
}
