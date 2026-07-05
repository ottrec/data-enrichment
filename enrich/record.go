package enrich

import "time"

// Output is the enrichment result for one dataset version.
type Output struct {
	Version   string         `json:"version"`
	Generated time.Time      `json:"generated"`
	Notices   []Notice       `json:"notices"`
	Unparsed  []Unparsed     `json:"unparsed,omitempty"`
	Stats     map[string]int `json:"stats"`
}

// Notice is one extracted item: raw text plus whatever associations and
// effects could be established without guessing. Flags are only set when
// backed by a trigger word in the raw text; everything short of certain is
// listed in Ambiguities instead.
type Notice struct {
	Facility string `json:"facility"`
	Group    string `json:"group,omitempty"` // schedule group label
	Source   string `json:"source"`          // special_hours | notifications | schedule_changes
	// BlockHash identifies the containing HTML block (cache/dedup key).
	BlockHash string `json:"blockHash"`
	Section   string `json:"section,omitempty"`  // nearest heading text
	DateText  string `json:"dateText,omitempty"` // the raw date-context text
	RawHTML   string `json:"rawHTML"`
	RawText   string `json:"rawText"`

	Dates   *DateSpan  `json:"dates,omitempty"`
	Scope   Scope      `json:"scope"`
	Time    *TimeAssoc `json:"time,omitempty"`
	Effects Effects    `json:"effects"`

	Ambiguities []string `json:"ambiguities,omitempty"`
	// DuplicateOfGroups is set on special_hours notices that repeat one of
	// the facility's schedule_changes notices (labels of those groups).
	DuplicateOfGroups []string `json:"duplicateOfGroups,omitempty"`
	ProducedBy        string   `json:"producedBy"`
}

// DateSpan is a resolved date association (ISO dates, Ottawa time).
type DateSpan struct {
	Dates     []string `json:"dates,omitempty"` // enumerated dates
	From      string   `json:"from,omitempty"`
	To        string   `json:"to,omitempty"`
	OpenEnded bool     `json:"openEnded,omitempty"`
	Weekdays  []string `json:"weekdays,omitempty"` // weekday-only patterns
}

// Scope is what the notice applies to.
type Scope struct {
	// Level is the most specific target established: activity, class, group,
	// facility, amenity, or none.
	Level string `json:"level"`
	// Groups are affected schedule group labels (facility-level notices).
	Groups []string `json:"groups,omitempty"`
	// Activities are normalized activity names: the resolved targets, or the
	// candidates when MatchQuality is "multiple".
	Activities []string `json:"activities,omitempty"`
	Amenity    string   `json:"amenity,omitempty"`
	Phrase     string   `json:"phrase,omitempty"` // the extracted subject phrase
	// MatchQuality: exact | normalized | fuzzy | multiple | scope-phrase | none.
	MatchQuality string `json:"matchQuality,omitempty"`
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

// Unparsed is an item nothing could be extracted from, kept raw.
type Unparsed struct {
	Facility  string `json:"facility"`
	Group     string `json:"group,omitempty"`
	Source    string `json:"source"`
	BlockHash string `json:"blockHash"`
	Section   string `json:"section,omitempty"`
	DateText  string `json:"dateText,omitempty"`
	RawHTML   string `json:"rawHTML"`
	RawText   string `json:"rawText"`
	Reason    string `json:"reason"`
}
