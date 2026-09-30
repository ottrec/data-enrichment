package enrich

import "strings"

// subjectKind is what the subject of a "<subject> is closed" sentence
// resolved to. The caller maps it to a scope; the reason beside it names the
// rule that decided and is counted as subject/closure/<reason>.
type subjectKind string

const (
	subjFacility      subjectKind = "facility"       // the facility itself
	subjPostedGroup   subjectKind = "posted-group"   // a cancelling closure posted under a group
	subjClass         subjectKind = "class"          // a cancelling closure naming the class it cancels
	subjPart          subjectKind = "part"           // a part of the facility with drop-in groups of its own
	subjPartUnmatched subjectKind = "part-unmatched" // a cancelling closure of a part no group title names
	subjUnit          subjectKind = "unit"           // some of the numbered units a row runs on
	subjActivity      subjectKind = "activity"       // an activity of the schedule
	subjAmenity       subjectKind = "amenity"        // a place with no drop-ins of its own
	subjOtherFacility subjectKind = "other-facility" // another facility of the dataset, named
	subjNone          subjectKind = "none"           // nothing the parser knows
)

// closureSubject is the resolved subject of a closure sentence.
type closureSubject struct {
	Kind   subjectKind
	Reason string
	// Class is the class phrase to resolve (subjClass); Groups the groups
	// the part names (subjPart); Acts, Groups and Quality the activity match
	// (subjActivity); Amenity the amenity name (subjUnit, subjAmenity) or
	// the other facility's name (subjOtherFacility).
	Class   string
	Groups  []string
	Acts    []*actEntry
	Quality string
	Amenity string
	Typo    bool // the activity match used the typo tolerance
}

// resolveClosureSubject resolves the subject of "<subject> is closed".
// cancelled says whether the sentence also cancels programs, fremainder is
// the folded sentence the subject came from.
//
// The order is the closure form's own and differs from the clause form's
// (the fold in processSentence): a closure names a place first, so the
// facility and its parts are tried before the activities and a match with
// several candidates is an amenity, not a multiple-candidate warning
// ("The steam room is closed" beside a "Hot tub and steam room" row); a
// clause names an activity first ("Public swim, 1 to 3 pm, cancelled"), so
// the clause form never considers the facility (Splash Wave Pool's "wave
// pool" would close it) and keeps a multiple match as candidates.
func (b *blockCtx) resolveClosureSubject(subject string, cancelled bool, fremainder string) closureSubject {
	q, acts, groups, typo := b.matchActivity(subject)
	s := closureSubject{Typo: typo}
	isFac, facReason := subjectIsFacility(subject, b.fac.GetName())
	if !isFac {
		isFac, facReason = b.facilityWithDesk(subject)
	}
	// a generic word that names only some of the facility's groups ("the
	// pool" at a complex) is a part of it, not the facility: closed, it
	// closes the swim group; cancelling, it cancels the part's programs
	// (invariant 5). At a pool the same word is the facility.
	part := isFac && namesPartOfFacility(b.matchers, subject)
	switch {
	case isFac && !part:
		s.Kind, s.Reason = subjFacility, facReason
	case part && !cancelled:
		s.Kind, s.Reason, s.Groups = subjPart, "part-groups", groupsForPart(b.matchers, subject)
	case cancelled && b.grp != nil:
		// "the pool is closed and all programs cancelled" posted under a
		// group: the group is the scope
		s.Kind, s.Reason = subjPostedGroup, "cancelled-under-group"
	case cancelled:
		// posted for the whole facility, where "all programs" means the
		// closed part's programs and not the facility's. A class named in
		// the cancellation is what the city says is cancelled ("the weight
		// and cardio room is closed, and all group fitness drop-ins are
		// cancelled"); otherwise the part names its groups ("squash and
		// racquetball courts", "the pool"); otherwise nothing is claimed
		if c := allProgramsRe.FindStringSubmatch(fremainder); c != nil && len(classSegments(c[1])) > 0 {
			s.Kind, s.Reason, s.Class = subjClass, "class-named", c[1]
		} else if gls := groupsForPart(b.matchers, subject); len(gls) > 0 {
			s.Kind, s.Reason, s.Groups = subjPart, "part-groups", gls
		} else {
			s.Kind, s.Reason = subjPartUnmatched, "part-unmatched"
		}
	case subjectNamesUnitOfActivity(subject, acts):
		// one court of six: the row keeps running on the rest, so this
		// closes a place and not a programme
		s.Kind, s.Reason, s.Amenity = subjUnit, "unit-of-row", amenityName(subject)
	case q == matchExact || q == matchNormalized || q == matchFuzzy:
		s.Kind, s.Reason = subjActivity, "activity-"+q
		s.Acts, s.Groups, s.Quality = acts, groups, q
	case b.namesOtherFacility(subject) != "":
		// "Meridian Theatres @ Centrepointe will remain closed" at Ben
		// Franklin Place: a facility of its own in the dataset, so a place
		// here, with no drop-ins of this one's
		s.Kind, s.Reason, s.Amenity = subjOtherFacility, "other-facility", b.namesOtherFacility(subject)
	case isAmenity(subject):
		s.Kind, s.Reason, s.Amenity = subjAmenity, "amenity-core", amenityName(subject)
	default:
		s.Kind, s.Reason = subjNone, "unmatched"
	}
	return s
}

// subjectIsFacility reports whether a closure subject names the facility
// itself, and the reason: generic facility words only ("the facility", "the
// pool" at a pool), or a distinctive (non-generic) token shared with the
// facility's name ("Fairfields Heritage House").
func subjectIsFacility(subject, facName string) (bool, string) {
	st := tokens(subject)
	if len(st) == 0 {
		return false, ""
	}
	generic := true
	for _, t := range st {
		if !genericFacility[t] {
			generic = false
			break
		}
	}
	if generic {
		return true, "facility-generic"
	}
	ft := tokenSet(facName)
	for _, t := range st {
		if ft[t] && !genericFacility[t] {
			return true, "facility-name-token"
		}
	}
	return false, ""
}

// facilityWithDesk reports whether a list subject names the facility and
// its service desk ("the complex and client services"), which is the
// facility: subjectIsFacility reads the list as one phrase, and the desk's
// words are not the facility's.
func (b *blockCtx) facilityWithDesk(subject string) (bool, string) {
	parts := subjectParts(subject)
	if len(parts) < 2 {
		return false, ""
	}
	fac := false
	for _, p := range parts {
		p = strings.TrimPrefix(p, "the ")
		if serviceDeskPhrase(p) {
			continue
		}
		if ok, _ := subjectIsFacility(p, b.fac.GetName()); !ok {
			return false, ""
		}
		fac = true
	}
	if !fac {
		return false, ""
	}
	return true, "facility-list-with-desk"
}

// otherFacility is a facility of the dataset a closure subject can name:
// its name and the name's distinctive tokens (the non-generic ones, at
// least two: "Entrance Pool" has one and names nothing).
type otherFacility struct {
	name string
	toks []string
}

// otherFacilities lists the facilities of the version a subject can name.
func otherFacilities(names []string) []otherFacility {
	var out []otherFacility
	for _, name := range names {
		var toks []string
		for _, t := range tokens(name) {
			if !genericFacility[t] {
				toks = append(toks, t)
			}
		}
		if len(toks) >= 2 {
			out = append(out, otherFacility{name, toks})
		}
	}
	return out
}

// namesOtherFacility returns the name of another facility of the dataset
// whose every distinctive token the subject has ("Meridian Theatres @
// Centrepointe" names Meridian Theatres at Centrepointe), or "".
func (b *blockCtx) namesOtherFacility(subject string) string {
	st := tokenSet(subject)
	for _, f := range b.others {
		if f.name == b.fac.GetName() {
			continue
		}
		all := true
		for _, t := range f.toks {
			if !st[t] {
				all = false
				break
			}
		}
		if all {
			return f.name
		}
	}
	return ""
}
