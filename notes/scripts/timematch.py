#!/usr/bin/env python3
"""For exact-activity-matched change items with a time range and a parseable
single-date head, check whether the time matches a schedule slot on that weekday."""
import re, collections, sys, datetime
from analyze import parse, elems, norm

def load_groups(path):
    fac = grp = None; changes = None; acts = []; act = None
    def emit():
        if grp and changes: yield_.append((fac, grp, changes, acts))
    yield_ = []
    for line in open(path):
        if line.startswith("=== "):
            emit(); fac = line[4:].split(" [")[0]; grp = changes = None; acts = []
        elif line.startswith("  GROUP "):
            emit(); grp = line.strip(); changes = None; acts = []
        elif line.startswith("    CHANGES: "):
            changes = line[13:].rstrip("\n").replace("\\n", "\n")
        elif line.startswith("      ACT "):
            m = re.match(r'\s*ACT label="(.*)" name="(.*)"', line)
            act = {"label": m.group(1), "name": m.group(2), "times": []}
            acts.append(act)
        elif line.startswith("        TIME ") and act is not None:
            m = re.match(r'\s*TIME day="(.*)" wd=(\w+)\((\w+)\) range=(.*)\((true|false)\) label=', line)
            if m and m.group(3) == "true" and m.group(5) == "true":
                act["times"].append((m.group(2), m.group(4)))
        for x in yield_: pass
    emit()
    return yield_

def parse_clock(s):
    # "9:30 - 11:30am" etc -> minutes; parse "H[:MM]" + am/pm
    s = s.strip().lower().replace("noon","12:00pm").replace("midnight","12:00am")
    m = re.match(r"(\d{1,2})(?::(\d{2}))?\s*(am|pm)?$", s)
    if not m: return None, False
    h, mi, ap = int(m.group(1)), int(m.group(2) or 0), m.group(3)
    if ap == "pm" and h != 12: h += 12
    if ap == "am" and h == 12: h = 0
    return h*60+mi, bool(ap)

def parse_item_time(t):
    # find "X to Y" / "X - Y" clock range in item text
    m = re.search(r"(\d{1,2}(?::\d{2})?\s*(?:am|pm)?|noon|midnight)\s*(?:to|-|–)\s*(\d{1,2}(?::\d{2})?\s*(?:am|pm)?|noon|midnight)", t, re.I)
    if not m: return None
    a, aok = parse_clock(m.group(1))
    b, bok = parse_clock(m.group(2))
    if a is None or b is None: return None
    return a, aok, b, bok

def parse_sched_range(s):
    # "9:30 - 11:30am" or "11:30am - 1:00pm"
    m = re.match(r"(.+?)\s*-\s*(.+)$", s)
    if not m: return None
    b, bok = parse_clock(m.group(2))
    a, aok = parse_clock(m.group(1))
    if a is None or b is None: return None
    if not aok and bok:  # inherit meridiem when start lacks it and start<=end after inheriting
        pass
    return a, aok, b, bok

WD = ["Monday","Tuesday","Wednesday","Thursday","Friday","Saturday","Sunday"]
MONTHS = {m: i+1 for i, m in enumerate(["January","February","March","April","May","June","July","August","September","October","November","December"])}
def parse_head(h):
    # single date like "Friday, July 3" -> (weekday, month, day)
    m = re.match(r"^(?:(%s),?\s+)?(%s)\s+(\d{1,2})$" % ("|".join(WD), "|".join(MONTHS)), norm(h).strip(" ,."))
    if not m: return None
    return m.group(1), MONTHS[m.group(2)], int(m.group(3))

def clock_variants(a, aok, b, bok):
    # candidate interpretations for missing meridiem: same as given or +12h
    avs = [a] if aok else [a, a+720 if a < 720 else a]
    bvs = [b] if bok else [b, b+720 if b < 720 else b]
    return {(x, y) for x in avs for y in bvs if x < y or True}

def normname(s):
    s = s.lower().replace("’","'")
    s = re.sub(r"[^a-z0-9+' ]+"," ",s)
    return re.sub(r"\s+"," ",s).strip()

def items(changes):
    root = parse(changes)
    for ul in elems(root, "ul"):
        for li in elems(ul, "li"):
            head = norm("".join(c.alltext() for c in li.children if c.tag != "ul"))
            for sub in elems(li, "ul"):
                for it in elems(sub, "li"):
                    yield head, norm(it.alltext())

seen=set(); stats=collections.Counter(); misses=[]
for fac, grp, changes, acts in load_groups(sys.argv[1] if len(sys.argv)>1 else "context-all.txt"):
    key=(grp,changes)
    if key in seen: continue
    seen.add(key)
    bylabel = {}
    for a in acts:
        bylabel.setdefault(normname(a["label"]), []).append(a)
        bylabel.setdefault(normname(a["name"]), []).append(a)
    for head, item in items(changes):
        hd = parse_head(head)
        if not hd: continue
        wd_name = hd[0]
        it = parse_item_time(item)
        if not it: continue
        # leading phrase before first comma
        phrase = normname(item.split(",")[0])
        if phrase not in bylabel: continue
        stats["candidates"] += 1
        a, aok, b, bok = it
        matched = False
        for act in bylabel[phrase]:
            for twd, trange in act["times"]:
                if wd_name and twd != wd_name: continue
                sr = parse_sched_range(trange)
                if not sr: continue
                sa, saok, sb, sbok = sr
                # try meridiem variants of the item time
                for xa in ({a} if aok else {a, (a+720)%1440}):
                    for xb in ({b} if bok else {b, (b+720)%1440}):
                        if xa == sa and xb == sb:
                            matched = True
        if matched: stats["time+weekday match"] += 1
        else:
            stats["no slot match"] += 1
            if len(misses) < 25: misses.append((fac, head, item, [t for act2 in bylabel[phrase] for t in act2["times"] if not wd_name or t[0] == wd_name]))
print(stats)
print()
for m in misses: print(m)
