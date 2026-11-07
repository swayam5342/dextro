package files

import (
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestParseSearch(t *testing.T) {
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	day := func(year int, month time.Month, date int) *time.Time {
		value := time.Date(year, month, date, 0, 0, 0, 0, time.UTC)
		return &value
	}
	size := func(n int64) *int64 { return &n }

	tests := []struct {
		raw  string
		want func(*searchFilter)
	}{
		{"holiday", func(f *searchFilter) { f.words = []string{"holiday"} }},
		{"two words", func(f *searchFilter) { f.words = []string{"two", "words"} }},
		{`"two words"`, func(f *searchFilter) { f.words = []string{"two words"} }},
		{"-draft", func(f *searchFilter) { f.without = []string{"draft"} }},
		{"report -draft", func(f *searchFilter) { f.words = []string{"report"}; f.without = []string{"draft"} }},
		{"-", func(f *searchFilter) { f.words = []string{"-"} }},

		// Anything it can't read is something to find in a name.
		{"note:draft", func(f *searchFilter) { f.words = []string{"note:draft"} }},
		{"type:nonsense", func(f *searchFilter) { f.words = []string{"type:nonsense"} }},
		{"size:huge", func(f *searchFilter) { f.words = []string{"size:huge"} }},
		{"after:someday", func(f *searchFilter) { f.words = []string{"after:someday"} }},
		{"type:", func(f *searchFilter) { f.words = []string{"type:"} }},
		{`"type:image"`, func(f *searchFilter) { f.words = []string{"type:image"} }},

		{"type:image", func(f *searchFilter) { f.types = []string{"image/%"} }},
		{"TYPE:Photos", func(f *searchFilter) { f.types = []string{"image/%"} }},
		{"type:pdf", func(f *searchFilter) { f.types = []string{"application/pdf"} }},

		{"size:>10mb", func(f *searchFilter) { f.minSize = size(10 << 20) }},
		{"size:<1gb", func(f *searchFilter) { f.maxSize = size(1 << 30) }},
		{"size:>=2k", func(f *searchFilter) { f.minSize = size(2 << 10) }},
		{"size:<=500", func(f *searchFilter) { f.maxSize = size(500) }},
		{"size:10mb", func(f *searchFilter) { f.minSize = size(10 << 20) }},
		{"larger:1.5MB", func(f *searchFilter) { f.minSize = size(3 << 19) }},
		{"smaller:1gb", func(f *searchFilter) { f.maxSize = size(1 << 30) }},

		{"after:2026-01-31", func(f *searchFilter) { f.after = day(2026, 1, 31) }},
		{"before:2026/02/01", func(f *searchFilter) { f.before = day(2026, 2, 1) }},
		{"before:2026/2/1", func(f *searchFilter) { f.before = day(2026, 2, 1) }},
		{"newer_than:7d", func(f *searchFilter) { value := now.AddDate(0, 0, -7); f.after = &value }},
		{"older_than:2w", func(f *searchFilter) { value := now.AddDate(0, 0, -14); f.before = &value }},
		{"newer_than:3m", func(f *searchFilter) { value := now.AddDate(0, -3, 0); f.after = &value }},
		{"older_than:1y", func(f *searchFilter) { value := now.AddDate(-1, 0, 0); f.before = &value }},

		{"account:work", func(f *searchFilter) { f.account = "work" }},
		{"owner:work", func(f *searchFilter) { f.account = "work" }},
		{`owner:"my work"`, func(f *searchFilter) { f.account = "my work" }},
		{"owner:me", func(*searchFilter) {}},
		{"in:Trip", func(f *searchFilter) { f.folder = "Trip" }},
		{`in:"Summer Trip"`, func(f *searchFilter) { f.folder = "Summer Trip" }},

		{"is:starred", func(f *searchFilter) { f.starred = true }},
		{"is:encrypted", func(f *searchFilter) { f.encrypted = true }},
		{"is:shared", func(f *searchFilter) { f.shared = true }},
		{"is:nothing", func(f *searchFilter) { f.words = []string{"is:nothing"} }},

		{"report type:pdf larger:1mb after:2026-01-01 owner:work -draft", func(f *searchFilter) {
			f.words = []string{"report"}
			f.without = []string{"draft"}
			f.types = []string{"application/pdf"}
			f.minSize = size(1 << 20)
			f.after = day(2026, 1, 1)
			f.account = "work"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.raw, func(t *testing.T) {
			want := searchFilter{words: []string{}, without: []string{}, types: []string{}}
			tt.want(&want)
			if got := parseSearch(tt.raw, now); !reflect.DeepEqual(got, want) {
				t.Errorf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestParseSearchTypes(t *testing.T) {
	now := time.Now()
	if got := parseSearch("type:document", now).types; !slices.Contains(got, "application/pdf") || !slices.Contains(got, "text/%") {
		t.Errorf("documents = %v", got)
	}
	both := parseSearch("type:image type:video", now).types
	if !slices.Equal(both, []string{"image/%", "video/%"}) {
		t.Errorf("two types = %v", both)
	}
}

func TestSearchFilterKinds(t *testing.T) {
	now := time.Now()
	if !parseSearch("   ", now).empty() || !parseSearch(`""`, now).empty() {
		t.Error("nothing should be empty")
	}
	if parseSearch("owner:me", now).filtersFiles() {
		t.Error("owner:me asks for nothing")
	}
	if parseSearch("holiday -draft", now).filtersFiles() {
		t.Error("words can match folders too")
	}
	if !parseSearch("holiday type:image", now).filtersFiles() {
		t.Error("a type only a file can have")
	}
}
