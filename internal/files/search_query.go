package files

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// searchFilter is what a search box's text asks for. Everything in it has to match.
type searchFilter struct {
	// words and without are parts of a name it must and mustn't contain.
	words   []string
	without []string
	// types are LIKE patterns for the content type, any of which will do.
	types     []string
	minSize   *int64
	maxSize   *int64
	after     *time.Time
	before    *time.Time
	account   string
	folder    string
	starred   bool
	encrypted bool
	shared    bool
}

// searchTypes are the kinds of file people ask for, as content types.
var searchTypes = func() map[string][]string {
	documents := []string{
		"application/pdf", "application/msword", "text/%",
		"application/vnd.openxmlformats-officedocument.wordprocessingml%",
		"application/vnd.oasis.opendocument.text%",
	}
	sheets := []string{
		"application/vnd.ms-excel%", "text/csv",
		"application/vnd.openxmlformats-officedocument.spreadsheetml%",
		"application/vnd.oasis.opendocument.spreadsheet%",
	}
	slides := []string{
		"application/vnd.ms-powerpoint%",
		"application/vnd.openxmlformats-officedocument.presentationml%",
		"application/vnd.oasis.opendocument.presentation%",
	}
	archives := []string{
		"application/zip", "application/x-zip%", "application/x-7z%", "application/x-rar%",
		"application/vnd.rar", "application/x-tar", "application/gzip", "application/x-gzip",
	}
	found := map[string][]string{"pdf": {"application/pdf"}, "text": {"text/%"}}
	add := func(patterns []string, names ...string) {
		for _, name := range names {
			found[name] = patterns
		}
	}
	add([]string{"image/%"}, "image", "images", "photo", "photos", "picture", "pictures")
	add([]string{"video/%"}, "video", "videos", "movie", "movies")
	add([]string{"audio/%"}, "audio", "music", "sound")
	add(documents, "document", "documents", "doc", "docs")
	add(sheets, "spreadsheet", "spreadsheets", "sheet", "sheets")
	add(slides, "presentation", "presentations", "slides")
	add(archives, "archive", "archives", "zip")
	return found
}()

var (
	sizePattern = regexp.MustCompile(`^(\d+(?:\.\d+)?)(b|k|kb|m|mb|g|gb|t|tb)?$`)
	agePattern  = regexp.MustCompile(`^(\d+)([dwmy])$`)
	sizeUnits   = map[string]float64{
		"": 1, "b": 1,
		"k": 1 << 10, "kb": 1 << 10,
		"m": 1 << 20, "mb": 1 << 20,
		"g": 1 << 30, "gb": 1 << 30,
		"t": 1 << 40, "tb": 1 << 40,
	}
)

func parseSize(raw string) (int64, bool) {
	match := sizePattern.FindStringSubmatch(strings.ToLower(raw))
	if match == nil {
		return 0, false
	}
	amount, err := strconv.ParseFloat(match[1], 64)
	if err != nil {
		return 0, false
	}
	bytes := amount * sizeUnits[match[2]]
	if bytes >= 1<<62 {
		return 0, false
	}
	return int64(bytes), true
}

func parseDate(raw string) (time.Time, bool) {
	for _, layout := range []string{"2006-01-02", "2006/01/02", "2006/1/2", "2006-1-2"} {
		if date, err := time.Parse(layout, raw); err == nil {
			return date, true
		}
	}
	return time.Time{}, false
}

// parseAge goes back from now by something like 7d, 2w, 3m or 1y.
func parseAge(raw string, now time.Time) (time.Time, bool) {
	match := agePattern.FindStringSubmatch(strings.ToLower(raw))
	if match == nil {
		return time.Time{}, false
	}
	amount, err := strconv.Atoi(match[1])
	if err != nil || amount > 100_000 {
		return time.Time{}, false
	}
	switch match[2] {
	case "d":
		return now.AddDate(0, 0, -amount), true
	case "w":
		return now.AddDate(0, 0, -7*amount), true
	case "m":
		return now.AddDate(0, -amount, 0), true
	}
	return now.AddDate(-amount, 0, 0), true
}

func (f *searchFilter) applySize(raw string) bool {
	switch {
	case strings.HasPrefix(raw, ">="):
		return f.setSize(&f.minSize, raw[2:])
	case strings.HasPrefix(raw, "<="):
		return f.setSize(&f.maxSize, raw[2:])
	case strings.HasPrefix(raw, ">"):
		return f.setSize(&f.minSize, raw[1:])
	case strings.HasPrefix(raw, "<"):
		return f.setSize(&f.maxSize, raw[1:])
	}
	// Gmail reads a bare size as "at least this big".
	return f.setSize(&f.minSize, raw)
}

func (f *searchFilter) setSize(target **int64, raw string) bool {
	size, ok := parseSize(raw)
	if ok {
		*target = &size
	}
	return ok
}

func setTime(target **time.Time, value time.Time, ok bool) bool {
	if ok {
		*target = &value
	}
	return ok
}

// apply takes one operator. False means it isn't one, so it's something to look for in a
// name, the way Gmail treats anything it can't read. That also keeps a name with a colon
// in it searchable.
func (f *searchFilter) apply(key, value string, now time.Time) bool {
	switch key {
	case "type":
		patterns, ok := searchTypes[strings.ToLower(value)]
		if ok {
			f.types = append(f.types, patterns...)
		}
		return ok
	case "size":
		return f.applySize(value)
	case "larger":
		return f.setSize(&f.minSize, value)
	case "smaller":
		return f.setSize(&f.maxSize, value)
	case "after":
		date, ok := parseDate(value)
		return setTime(&f.after, date, ok)
	case "before":
		date, ok := parseDate(value)
		return setTime(&f.before, date, ok)
	case "newer_than":
		date, ok := parseAge(value, now)
		return setTime(&f.after, date, ok)
	case "older_than":
		date, ok := parseAge(value, now)
		return setTime(&f.before, date, ok)
	case "account", "owner":
		// Every file here is the user's own, so "me" asks for nothing.
		if !strings.EqualFold(value, "me") {
			f.account = value
		}
		return true
	case "in":
		f.folder = value
		return true
	case "is":
		return f.applyIs(strings.ToLower(value))
	}
	return false
}

func (f *searchFilter) applyIs(value string) bool {
	switch value {
	case "starred":
		f.starred = true
	case "encrypted":
		f.encrypted = true
	case "shared":
		f.shared = true
	default:
		return false
	}
	return true
}

// searchToken is one piece of a search. Quoted means it started with a quote, so it's
// meant literally, whatever it looks like.
type searchToken struct {
	text   string
	quoted bool
}

// tokenize splits on spaces, keeping anything in double quotes together, as in
// "two words" and owner:"two words".
func tokenize(raw string) []searchToken {
	var tokens []searchToken
	var current strings.Builder
	quoted, inQuotes, started := false, false, false
	flush := func() {
		if started {
			tokens = append(tokens, searchToken{text: current.String(), quoted: quoted})
		}
		current.Reset()
		quoted, started = false, false
	}
	for _, r := range raw {
		switch {
		case r == '"':
			if !started {
				quoted = true
			}
			inQuotes = !inQuotes
			started = true
		case !inQuotes && (r == ' ' || r == '\t'):
			flush()
		default:
			current.WriteRune(r)
			started = true
		}
	}
	flush()
	return tokens
}

// parseSearch reads the search box. The slices are never nil, because the query asks how
// long they are.
func parseSearch(raw string, now time.Time) searchFilter {
	filter := searchFilter{words: []string{}, without: []string{}, types: []string{}}
	for _, token := range tokenize(raw) {
		if token.text == "" {
			continue
		}
		if !token.quoted {
			key, value, found := strings.Cut(token.text, ":")
			if found && value != "" && filter.apply(strings.ToLower(key), value, now) {
				continue
			}
			if len(token.text) > 1 && token.text[0] == '-' {
				filter.without = append(filter.without, token.text[1:])
				continue
			}
		}
		filter.words = append(filter.words, token.text)
	}
	return filter
}

// empty says the search asks for nothing at all.
func (f searchFilter) empty() bool {
	return len(f.words) == 0 && len(f.without) == 0 && !f.filtersFiles()
}

// filtersFiles says the search asks something only a file can answer, so folders are left
// out of the results.
func (f searchFilter) filtersFiles() bool {
	return len(f.types) > 0 || f.minSize != nil || f.maxSize != nil || f.after != nil || f.before != nil ||
		f.account != "" || f.folder != "" || f.starred || f.encrypted || f.shared
}
