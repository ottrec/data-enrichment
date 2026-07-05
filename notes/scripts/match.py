#!/usr/bin/env python3
"""Pair CHANGES items with the group's activity labels; measure match quality."""
import re, collections, sys
from analyze import parse, elems, norm, DATEISH

def load_groups(path):
    """yield (facility, group_label, changes_html, [(act_label, act_name)...])"""
    fac = None
    grp = None
    changes = None
    acts = []
    for line in open(path):
        if line.startswith("=== "):
            if grp and changes: yield fac, grp, changes, acts
            fac = line[4:].split(" [")[0]; grp = changes = None; acts = []
        elif line.startswith("  GROUP "):
            if grp and changes: yield fac, grp, changes, acts
            grp = line.strip(); changes = None; acts = []
        elif line.startswith("    CHANGES: "):
            changes = line[len("    CHANGES: "):].rstrip("\n").replace("\\n", "\n")
        elif line.startswith("      ACT "):
            m = re.match(r'\s*ACT label="(.*)" name="(.*)"', line)
            if m: acts.append((m.group(1), m.group(2)))
    if grp and changes: yield fac, grp, changes, acts

def items(changes):
    """yield (date_head, item_text) from a CHANGES block"""
    root = parse(changes)
    for ul in elems(root, "ul"):
        for li in elems(ul, "li"):
            head = norm("".join(c.alltext() for c in li.children if c.tag != "ul"))
            for sub in elems(li, "ul"):
                for it in elems(sub, "li"):
                    yield head, norm(it.alltext())

def normname(s):
    s = s.lower().replace("’", "'")
    s = re.sub(r"[^a-z0-9+' ]+", " ", s)
    s = re.sub(r"\s+", " ", s).strip()
    return s

TAIL = re.compile(r",?\s*(cancelled|canceled|added|closed|moved.*|changed.*|25m pool only|short course only.*|schedule change)\s*\.?\s*$", re.I)
TIME = re.compile(r",?\s*\(?((from )?\d{1,2}(:\d{2})?\s*(am|pm)?|noon|midnight)((\s*(to|-|–|until|and)\s*)(\d{1,2}(:\d{2})?\s*(am|pm)?|noon|midnight))+\)?", re.I)

def activity_phrase(item):
    """strip trailing keyword and time range, return leading phrase"""
    t = TAIL.sub("", item)
    t = TIME.sub("", t)
    return t.strip(" ,.")

seen = set()
stats = collections.Counter()
unmatched = collections.Counter()
matched_loose = collections.Counter()
for fac, grp, changes, acts in load_groups(sys.argv[1] if len(sys.argv) > 1 else "context-all.txt"):
    key = (fac, grp, changes, tuple(acts))
    if key in seen: continue
    seen.add(key)
    labels = {normname(l) for l, n in acts} | {normname(n) for l, n in acts}
    for head, item in items(changes):
        phrase = activity_phrase(item)
        p = normname(phrase)
        if not p:
            stats["empty-phrase"] += 1
            continue
        if re.match(r"^(all |the |see |facility|both )", phrase, re.I) or "closed" in p or "cancelled" in p:
            stats["freeform/all"] += 1
            continue
        if p in labels:
            stats["exact"] += 1
        elif any(p in l or l in p for l in labels if len(min(p,l,key=len)) >= 4):
            stats["substring"] += 1
            matched_loose[(p, tuple(sorted(l for l in labels if p in l or l in p)))] += 1
        else:
            stats["none"] += 1
            unmatched[(p, tuple(sorted(labels)))] += 1

print(stats)
print("\n--- unmatched phrases (top 50) ---")
for (p, labels), v in sorted(unmatched.items(), key=lambda x: -x[1])[:50]:
    print(v, repr(p), "||", list(labels)[:8])
print("\n--- substring matches (top 30) ---")
for (p, ls), v in sorted(matched_loose.items(), key=lambda x: -x[1])[:30]:
    print(v, repr(p), "->", ls)
