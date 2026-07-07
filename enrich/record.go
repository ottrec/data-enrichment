package enrich

// This file holds the internal intermediate representation the item parser
// produces. The public output is the protobuf schema in
// github.com/ottrec/data-enrichment/schema (see enrichment.proto); enrich.go
// converts these types into it during placement. Date and clock encodings
// follow the scraper's conventions: schema.Date (YYYYMMDDW) and minutes from
// midnight.

import (
	"time"

	"github.com/ottrec/scraper/schema"
)

// DateSpan is a resolved date association, converted from a parsed dateSpec
// at emit time (years resolved, Ottawa time).
type DateSpan struct {
	Dates     []schema.Date  // enumerated dates
	From, To  schema.Date    // range (zero when absent)
	OpenEnded bool           // "until further notice"-style open end
	Weekdays  []time.Weekday // weekday-only patterns ("Monday to Friday")
}

// TimeAssoc is a clock range extracted from a notice and its relation to the
// schedule's actual time slots. Text/StartMin/EndMin are absent for
// whole-activity notices where only the affected slots are known.
type TimeAssoc struct {
	Text     string
	StartMin int // minutes from midnight
	EndMin   int // may exceed 1440 for overnight
	// OpenStart/OpenEnd mark single-ended mentions: "closed until noon" is
	// open at the start (affected from start of day), "closed at 7:30 pm" /
	// "will end at 6 pm" open at the end (affected through end of day).
	OpenStart bool
	OpenEnd   bool
	// Relation to the matched activity's slots on the resolved dates:
	// exact | within | covers | overlaps | novel | none | unchecked.
	Relation string
	Slots    []string // affected slot labels, "Weekday HH:MM - HH:MM"
}

// Effects are the best-effort flags. Booleans are only ever set when the
// corresponding trigger word appears in the item text (the no-false-positive
// contract); they convert to the schema's repeated-oneof Effect list.
type Effects struct {
	Cancelled     bool
	Added         bool
	TimeChange    bool
	Closure       bool
	SeasonalHours bool   // "Regular season, June 29 to August 30"
	ModifiedHours bool   // facility hours on specific dates
	MovedTo       string // "moved to the 25m pool"
	ChangedTo     string // "changed to Lane swim - shared pool"
	Restriction   string // "25m pool only" etc.
	SeeSchedule   string // referenced schedule name
	SeeURL        string
}

func (e Effects) any() bool {
	return e != Effects{}
}

// notice is the intermediate representation of one parsed fragment before
// placement; scope describes where in the hierarchy the record belongs.
type notice struct {
	Group    string // group label the block was posted under ("" for facility blocks)
	Source   string // special_hours | notifications | schedule_changes
	Section  string // nearest heading text
	DateText string // raw date-context text
	RawHTML  string
	RawText  string

	Dates   *DateSpan
	Scope   scope
	Time    *TimeAssoc
	Effects Effects

	Ambiguities []string
}

// scope is what a parsed notice applies to, before placement decides the
// tree position.
type scope struct {
	// Level: activity, class, group, facility, amenity, or none. Activity
	// and class descend to activity/session nodes; multiple-candidate
	// matches stay at group level.
	Level  string
	Groups []string // affected group labels
	// Activities holds raw activity labels (the canonical join key into the
	// dataset), or the candidate labels when MatchQuality is "multiple".
	Activities   []string
	Amenity      string
	Phrase       string
	MatchQuality string
}
