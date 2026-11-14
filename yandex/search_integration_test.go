//go:build integration
// +build integration

package yandex

import (
	"testing"

	"github.com/xuboboo/web2api/core"
	"github.com/xuboboo/web2api/testutil/ithelper"
)

func TestSearchYandex(t *testing.T) {
	ithelper.RunEngineTests(t, func(b *core.Browser) core.SearchEngine {
		return New(*b, ithelper.EngineOptions())
	})
}
