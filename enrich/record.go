package enrich

import "time"

// Output is the enrichment result for one dataset version. Every fragment of
// text in the source HTML fields is accounted for by exactly one Object in
// the flat list; the facility hierarchy references objects by ID at the most
// specific level the association is guaranteed for.
type Output struct {
	Version    string         `json:"version"`
	Generated  time.Time      `json:"generated"`
	Objects    []Object       `json:"objects"`
	Facilities []Facility     `json:"facilities"`
	Stats      map[string]int `json:"stats"`
}

// Facility mirrors the dataset facility, holding facility-scoped object refs
// and the schedule groups that have content.
type Facility struct {
	Name    string   `json:"name"`
	Objects []string `json:"objects,omitempty"` // facility-scoped object ids
	Groups  []Group  `json:"groups,omitempty"`
}

// Group mirrors a schedule group (by label).
type Group struct {
	Label      string     `json:"label"`
	Objects    []string   `json:"objects,omitempty"` // group-scoped object ids
	Activities []Activity `json:"activities,omitempty"`
}

// Activity mirrors a schedule activity (by normalized name). Novel marks
// activities that only exist in a change notice ("Women's only swim, added"),
// not in the published schedule.
type Activity struct {
	Name     string    `json:"name"`
	Novel    bool      `json:"novel,omitempty"`
	Objects  []string  `json:"objects,omitempty"` // whole-activity object ids
	Sessions []Session `json:"sessions,omitempty"`
}

// Session is one concrete date + clock range. Objects reference notices about
// a published schedule time (cancellations, closures, changes); Added
// reference notices that add this time (it is not in the published schedule).
type Session struct {
	Date     string   `json:"date"` // ISO, Ottawa time
	StartMin int      `json:"startMin"`
	EndMin   int      `json:"endMin"`
	Objects  []string `json:"objects,omitempty"`
	Added    []string `json:"added,omitempty"`
}

// Object is one extracted fragment of source text. Kind "notice" carries
// parsed associations and effects; "unparsed" is freeform text nothing could
// be extracted from; "ignored" is structure and boilerplate (headings,
// date-context lines, contact blurbs, collapsed duplicates) kept so that the
// output accounts for every fragment of the source.
type Object struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`             // notice | unparsed | ignored
	Reason string `json:"reason,omitempty"` // unparsed: freeform | parse-error; ignored: heading | date-context | boilerplate | service-desk | duplicate

	Facility string `json:"facility"`
	Source   string `json:"source"` // special_hours | notifications | schedule_changes
	// SourceGroup is the schedule group whose block this came from (for
	// schedule_changes); the object may be placed elsewhere in the tree when
	// the city posted it under the wrong group (matched-other-group).
	SourceGroup string `json:"sourceGroup,omitempty"`
	// Sources lists all sources that carried this notice when duplicates
	// were collapsed into it (e.g. schedule_changes + special_hours).
	Sources []string `json:"sources,omitempty"`
	// DuplicateOf points from a collapsed ignored/duplicate object to the
	// surviving notice ids.
	DuplicateOf []string `json:"duplicateOf,omitempty"`

	BlockHash string `json:"blockHash"`
	Seq       int    `json:"seq"` // order within the block
	// HTMLOffset is the [start, end) byte range in the source block HTML
	// this fragment was extracted from, when it could be tracked.
	HTMLOffset []int `json:"htmlOffset,omitempty"`

	Section  string `json:"section,omitempty"`  // nearest heading text
	DateText string `json:"dateText,omitempty"` // raw date-context text
	RawHTML  string `json:"rawHTML,omitempty"`
	RawText  string `json:"rawText"`

	Dates   *DateSpan  `json:"dates,omitempty"`
	Time    *TimeAssoc `json:"time,omitempty"`
	Effects *Effects   `json:"effects,omitempty"`

	// MatchQuality: exact | normalized | fuzzy | novel | multiple |
	// scope-phrase | none. Candidates holds the activity names of an
	// unresolved multiple match (the object stays at group level).
	MatchQuality string   `json:"matchQuality,omitempty"`
	Phrase       string   `json:"phrase,omitempty"`
	Amenity      string   `json:"amenity,omitempty"`
	Candidates   []string `json:"candidates,omitempty"`

	Ambiguities []string `json:"ambiguities,omitempty"`
	ProducedBy  string   `json:"producedBy,omitempty"`
}

// DateSpan is a resolved date association (ISO dates, Ottawa time).
type DateSpan struct {
	Dates     []string `json:"dates,omitempty"` // enumerated dates
	From      string   `json:"from,omitempty"`
	To        string   `json:"to,omitempty"`
	OpenEnded bool     `json:"openEnded,omitempty"`
	Weekdays  []string `json:"weekdays,omitempty"` // weekday-only patterns
}

// TimeAssoc is a clock range extracted from the notice and its relation to
// the schedule's actual time slots.
type TimeAssoc struct {
	// Text/StartMin/EndMin are absent for whole-activity notices where only
	// the affected slots are known.
	Text     string `json:"text,omitempty"`
	StartMin int    `json:"startMin,omitempty"`
	EndMin   int    `json:"endMin,omitempty"` // may exceed 1440 for overnight
	// OpenStart/OpenEnd mark single-ended mentions: "closed until noon" is
	// open at the start (affected from start of day), "closed at 7:30 pm" /
	// "will end at 6 pm" open at the end (affected through end of day).
	OpenStart bool `json:"openStart,omitempty"`
	OpenEnd   bool `json:"openEnd,omitempty"`
	// Relation to the matched activity's slots on the resolved dates:
	// exact | within | covers | overlaps | novel | none | unchecked.
	Relation string   `json:"relation,omitempty"`
	Slots    []string `json:"slots,omitempty"` // affected slots, "Weekday HH:MM - HH:MM"
}

// Effects are the best-effort flags. Booleans are only true when the
// corresponding trigger word appears in the item.
type Effects struct {
	Cancelled     bool   `json:"cancelled,omitempty"`
	Added         bool   `json:"added,omitempty"`
	TimeChange    bool   `json:"timeChange,omitempty"`
	Closure       bool   `json:"closure,omitempty"`
	SeasonalHours bool   `json:"seasonalHours,omitempty"` // "Regular season, June 29 to August 30"
	ModifiedHours bool   `json:"modifiedHours,omitempty"` // facility hours on specific dates
	MovedTo       string `json:"movedTo,omitempty"`
	ChangedTo     string `json:"changedTo,omitempty"`
	Restriction   string `json:"restriction,omitempty"` // "25m pool only" etc.
	SeeSchedule   string `json:"seeSchedule,omitempty"` // referenced schedule name
	SeeURL        string `json:"seeURL,omitempty"`
}

func (e Effects) any() bool {
	return e != Effects{}
}

// notice is the intermediate representation the item parser produces before
// placement; scope describes where in the hierarchy the record belongs.
type notice struct {
	Group    string // group label the block was posted under
	Source   string
	Section  string
	DateText string
	RawHTML  string
	RawText  string

	Dates   *DateSpan
	Scope   scope
	Time    *TimeAssoc
	Effects Effects

	Ambiguities []string
}

// scope is what a parsed notice applies to, before placement.
type scope struct {
	// Level: activity, class, group, facility, amenity, or none.
	Level        string
	Groups       []string // affected group labels
	Activities   []string // normalized activity names (or candidates for multiple)
	Amenity      string
	Phrase       string
	MatchQuality string
}
