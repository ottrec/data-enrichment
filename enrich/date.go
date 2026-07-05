package enrich

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ottrec/website/pkg/ottrecidx"
)

// Ambiguity markers for date resolution.
const (
	ambDateUnparsed     = "date-unparsed"         // date-like text that didn't parse
	ambDateGarbled      = "date-garbled"          // parsed partially with date-like leftovers
	ambWeekdayMismatch  = "weekday-mismatch"      // written weekday agrees with no candidate year
	ambYearUnconfirmed  = "date-year-unconfirmed" // no weekday to confirm and far from the anchor
	ambYearAmbiguous    = "date-year-ambiguous"   // multiple candidate years fit
	ambDateRangeInvalid = "date-range-invalid"    // range didn't resolve to from<=to
)

// dateSpec is a resolved date expression. Zero values mean the corresponding
// form was absent.
type dateSpec struct {
	Dates     []time.Time    // enumerated single dates (also used for one date)
	From, To  time.Time      // range
	OpenEnded bool           // "until further notice"-style open end
	Weekdays  []time.Weekday // weekday-only patterns ("Monday to Friday")
	Raw       string
	Ambig     []string
}

func (d dateSpec) empty() bool {
	return len(d.Dates) == 0 && d.From.IsZero() && d.To.IsZero() && !d.OpenEnded && len(d.Weekdays) == 0
}

// allDates enumerates the concrete dates the spec covers, up to max days for
// ranges (nil beyond that, or for open/weekday-only specs).
func (d dateSpec) allDates(max int) []time.Time {
	if len(d.Dates) > 0 {
		return d.Dates
	}
	if !d.From.IsZero() && !d.To.IsZero() {
		n := int(d.To.Sub(d.From)/(24*time.Hour)) + 1
		if n <= 0 || n > max {
			return nil
		}
		out := make([]time.Time, n)
		for i := range out {
			out[i] = d.From.AddDate(0, 0, i)
		}
		return out
	}
	return nil
}

var weekdayNames = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday,
	"wednesday": time.Wednesday, "thursday": time.Thursday,
	"friday": time.Friday, "saturday": time.Saturday,
}

var monthNames = map[string]time.Month{
	"january": time.January, "february": time.February, "march": time.March,
	"april": time.April, "may": time.May, "june": time.June, "july": time.July,
	"august": time.August, "september": time.September, "october": time.October,
	"november": time.November, "december": time.December,
}

var wordRe = regexp.MustCompile(`\S+`)

// partialDate is a parsed but unresolved date mention.
type partialDate struct {
	wd    *time.Weekday
	month time.Month // 0 if inherited
	day   int
	year  int // 0 if unspecified
}

type dateParser struct {
	words  []string // folded words, trailing punctuation trimmed
	starts []int    // byte offset of each word in the original text
	src    string
}

func newDateParser(s string) *dateParser {
	p := &dateParser{src: s}
	for _, loc := range wordRe.FindAllStringIndex(s, -1) {
		w := strings.ToLower(s[loc[0]:loc[1]])
		w = strings.Trim(w, ",.;:!()")
		p.words = append(p.words, w)
		p.starts = append(p.starts, loc[0])
	}
	return p
}

// rest returns the source text from word i on.
func (p *dateParser) rest(i int) string {
	if i >= len(p.words) {
		return ""
	}
	return strings.TrimLeft(strings.Trim(p.src[p.starts[i]:], " ,.;:"), " ")
}

func isDateJoiner(w string) bool {
	switch w {
	case "to", "-", "–", "—", "through", "until":
		return true
	}
	return false
}

// parseSingle parses one date mention at word i. If dayOnly, a bare day
// number is accepted (month inherited later).
func (p *dateParser) parseSingle(i int, dayOnly bool) (partialDate, int, bool) {
	var d partialDate
	j := i
	if j < len(p.words) {
		if wd, ok := weekdayNames[strings.TrimSuffix(p.words[j], "s")]; ok {
			d.wd = &wd
			j++
		}
	}
	if j < len(p.words) {
		if m, ok := monthNames[p.words[j]]; ok {
			d.month = m
			j++
		}
	}
	if j < len(p.words) {
		if n, err := strconv.Atoi(p.words[j]); err == nil && n >= 1 && n <= 31 {
			d.day = n
			j++
		}
	}
	if d.day == 0 || (d.month == 0 && !dayOnly) {
		return d, i, false
	}
	if j < len(p.words) {
		if n, err := strconv.Atoi(p.words[j]); err == nil && n >= 2000 && n <= 2100 {
			d.year = n
			j++
		}
	}
	return d, j, true
}

// parseWeekdaySet parses weekday-only patterns like "Monday to Friday",
// "Saturday and Sunday", "Fridays, Saturdays, and Sundays" at word i.
func (p *dateParser) parseWeekdaySet(i int) ([]time.Weekday, int, bool) {
	var wds []time.Weekday
	j := i
	rangeTo := false
	for j < len(p.words) {
		w := strings.TrimSuffix(p.words[j], "s")
		if wd, ok := weekdayNames[w]; ok {
			if rangeTo && len(wds) > 0 {
				for x := wds[len(wds)-1] + 1; ; x = (x + 7) % 7 {
					wds = append(wds, x%7)
					if x%7 == wd {
						break
					}
				}
				rangeTo = false
			} else {
				wds = append(wds, wd)
			}
			j++
			continue
		}
		if p.words[j] == "and" || p.words[j] == "" {
			j++
			continue
		}
		if p.words[j] == "to" && len(wds) > 0 && !rangeTo {
			rangeTo = true
			j++
			continue
		}
		break
	}
	if len(wds) == 0 || rangeTo {
		return nil, i, false
	}
	return wds, j, true
}

// parseLeadingDate parses a date expression at the start of s, resolving
// years against anchor (in ottrecidx.TZ). It returns the resolved spec, the
// unconsumed remainder of s, and whether a date was found at all.
func parseLeadingDate(s string, anchor time.Time) (dateSpec, string, bool) {
	p := newDateParser(s)
	var spec dateSpec

	// weekday-only pattern (no month/day follows the weekday)
	if wds, j, ok := p.parseWeekdaySet(0); ok {
		if _, _, single := p.parseSingle(0, false); !single {
			spec.Weekdays = wds
			spec.Raw = strings.TrimSpace(s[:p.starts[j-1]+len(p.words[j-1])])
			return spec, p.rest(j), true
		}
	}

	first, j, ok := p.parseSingle(0, false)
	if !ok {
		return spec, s, false
	}
	parts := []partialDate{first}
	isRange := false
	for j < len(p.words) {
		w := p.words[j]
		if w == "until" && j+2 <= len(p.words) && strings.HasPrefix(p.rest(j), "until further notice") {
			spec.OpenEnded = true
			j += 3
			break
		}
		if isDateJoiner(w) && !isRange && len(parts) == 1 {
			second, k, ok := p.parseSingle(j+1, true)
			if !ok {
				break
			}
			parts = append(parts, second)
			isRange = true
			j = k
			continue
		}
		if w == "and" || w == "" {
			next, k, ok := p.parseSingle(j+1, len(parts) > 0)
			if !ok {
				break
			}
			parts = append(parts, next)
			j = k
			continue
		}
		break
	}

	end := p.starts[j-1] + len(p.words[j-1])
	if end > len(s) {
		end = len(s)
	}
	spec.Raw = strings.TrimRight(strings.TrimSpace(s[:end]), ",.")

	// inherit months for day-only mentions
	lastMonth := parts[0].month
	for i := range parts {
		if parts[i].month == 0 {
			parts[i].month = lastMonth
		} else {
			lastMonth = parts[i].month
		}
	}

	// resolve
	if isRange {
		from, ambF := resolveDate(parts[0], anchor)
		to, ambT := resolveDateAfter(parts[1], from, anchor)
		spec.Ambig = append(spec.Ambig, ambF...)
		spec.Ambig = append(spec.Ambig, ambT...)
		if from.IsZero() || to.IsZero() || to.Before(from) {
			spec.Ambig = append(spec.Ambig, ambDateRangeInvalid)
			spec.From, spec.To = time.Time{}, time.Time{}
		} else {
			spec.From, spec.To = from, to
		}
	} else {
		for _, pt := range parts {
			t, amb := resolveDate(pt, anchor)
			spec.Ambig = append(spec.Ambig, amb...)
			if !t.IsZero() {
				spec.Dates = append(spec.Dates, t)
			}
		}
		if spec.OpenEnded && len(spec.Dates) == 1 {
			spec.From = spec.Dates[0]
			spec.Dates = nil
		}
	}

	rest := p.rest(j)
	// a date-like leftover right after a parsed date means the text was
	// garbled ("Monday, July 6 to 10 Friday, July 10"); don't trust the parse
	if rest != "" {
		if w := strings.Trim(strings.ToLower(wordRe.FindString(rest)), ",.;:"); w != "" {
			if _, isWd := weekdayNames[strings.TrimSuffix(w, "s")]; isWd {
				return dateSpec{Raw: spec.Raw, Ambig: []string{ambDateGarbled}}, s, false
			}
			if _, isMon := monthNames[w]; isMon {
				return dateSpec{Raw: spec.Raw, Ambig: []string{ambDateGarbled}}, s, false
			}
		}
	}
	if spec.empty() {
		return spec, s, false
	}
	return spec, rest, true
}

// resolveDate resolves a partial date to a concrete date near anchor,
// validating against the written weekday when present.
func resolveDate(d partialDate, anchor time.Time) (time.Time, []string) {
	if d.month == 0 || d.day == 0 {
		return time.Time{}, []string{ambDateUnparsed}
	}
	if d.year != 0 {
		t := time.Date(d.year, d.month, d.day, 0, 0, 0, 0, ottrecidx.TZ)
		if t.Day() != d.day {
			return time.Time{}, []string{ambDateUnparsed}
		}
		if d.wd != nil && t.Weekday() != *d.wd {
			return t, []string{ambWeekdayMismatch}
		}
		return t, nil
	}
	if anchor.IsZero() {
		return time.Time{}, []string{ambYearUnconfirmed}
	}
	var cands []time.Time
	for _, y := range []int{anchor.Year() - 1, anchor.Year(), anchor.Year() + 1} {
		t := time.Date(y, d.month, d.day, 0, 0, 0, 0, ottrecidx.TZ)
		if t.Day() == d.day { // reject e.g. Feb 30 rollover
			cands = append(cands, t)
		}
	}
	if len(cands) == 0 {
		return time.Time{}, []string{ambDateUnparsed}
	}
	if d.wd != nil {
		var match []time.Time
		for _, t := range cands {
			if t.Weekday() == *d.wd {
				match = append(match, t)
			}
		}
		switch len(match) {
		case 1:
			return match[0], nil
		case 0:
			return nearest(cands, anchor), []string{ambWeekdayMismatch}
		default:
			return nearest(match, anchor), []string{ambYearAmbiguous}
		}
	}
	t := nearest(cands, anchor)
	if d := t.Sub(anchor); d > 210*24*time.Hour || d < -210*24*time.Hour {
		return t, []string{ambYearUnconfirmed}
	}
	return t, nil
}

// resolveDateAfter resolves the "to" side of a range: at or after from, in
// from's year or the next (for December-to-January ranges).
func resolveDateAfter(d partialDate, from time.Time, anchor time.Time) (time.Time, []string) {
	if from.IsZero() {
		return resolveDate(d, anchor)
	}
	if d.month == 0 || d.day == 0 {
		return time.Time{}, []string{ambDateUnparsed}
	}
	if d.year != 0 {
		return resolveDate(d, anchor)
	}
	for _, y := range []int{from.Year(), from.Year() + 1} {
		t := time.Date(y, d.month, d.day, 0, 0, 0, 0, ottrecidx.TZ)
		if t.Day() != d.day || t.Before(from) {
			continue
		}
		if t.Sub(from) > 400*24*time.Hour {
			break
		}
		if d.wd != nil && t.Weekday() != *d.wd {
			return t, []string{ambWeekdayMismatch}
		}
		return t, nil
	}
	return time.Time{}, []string{ambDateRangeInvalid}
}

func nearest(cands []time.Time, anchor time.Time) time.Time {
	best := cands[0]
	for _, t := range cands[1:] {
		if absDur(t.Sub(anchor)) < absDur(best.Sub(anchor)) {
			best = t
		}
	}
	return best
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
