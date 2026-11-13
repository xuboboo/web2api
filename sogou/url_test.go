package sogou

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
			want:  []string{"query=golang", "ie=utf8", "sogou.com/web"},
		},
		{
			name:  "chinese text is percent encoded",
			query: core.Query{Text: "人工智能"},
			want:  []string{"query=%E4%BA%BA%E5%B7%A5%E6%99%BA%E8%83%BD"},
		},
		{
			name:  "site and filetype fold into the query",
			query: core.Query{Text: "golang", Site: "go.dev", Filetype: "pdf"},
			want:  []string{"site%3Ago.dev", "filetype%3Apdf"},
		},
		{
			name:  "start offset maps to a 1-based page",
			query: core.Query{Text: "golang", Start: 20},
			want:  []string{"page=3"},
		},
		{
			name:    "empty query is rejected",
			query:   core.Query{},
			wantErr: true,
		},
		{
			name:    "negative start is rejected",
			query:   core.Query{Text: "golang", Start: -1},
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

func TestBuildURLPagingWithoutStartHasNoPageParam(t *testing.T) {
	got, err := BuildURL(core.Query{Text: "golang"})
	if err != nil {
		t.Fatalf("BuildURL() error = %v", err)
	}
	if strings.Contains(got, "page=") {
		t.Fatalf("first page must not set page=, got %q", got)
	}
}
