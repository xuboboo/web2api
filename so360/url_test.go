package so360

import (
	"strings"
	"testing"

	"github.com/karust/openserp/core"
)

func TestBuildURL(t *testing.T) {
	tests := []struct {
		name    string
		query   core.Query
		want    []string
		wantErr bool
	}{
		{
			name:  "plain text",
			query: core.Query{Text: "golang"},
			want:  []string{"q=golang", "so.com/s"},
		},
		{
			name:  "chinese text is percent encoded",
			query: core.Query{Text: "人工智能"},
			want:  []string{"q=%E4%BA%BA%E5%B7%A5%E6%99%BA%E8%83%BD"},
		},
		{
			name:  "site filter folds into the query",
			query: core.Query{Text: "golang", Site: "go.dev"},
			want:  []string{"site%3Ago.dev"},
		},
		{
			name:  "start offset maps to a 1-based page",
			query: core.Query{Text: "golang", Start: 10},
			want:  []string{"pn=2"},
		},
		{
			name:    "empty query is rejected",
			query:   core.Query{},
			wantErr: true,
		},
		{
			name:    "negative start is rejected",
			query:   core.Query{Text: "golang", Start: -5},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := BuildURL(tt.query)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got URL %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("BuildURL() error = %v", err)
			}
			for _, fragment := range tt.want {
				if !strings.Contains(got, fragment) {
					t.Fatalf("BuildURL() = %q, missing %q", got, fragment)
				}
			}
		})
	}
}
