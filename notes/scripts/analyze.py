#!/usr/bin/env python3
"""Classify unique special/notif/changes blocks by structural shape."""
import sys, re, html, collections
from html.parser import HTMLParser

# parse the escaped-oneline blocks back
def load(path):
    for line in open(path):
        kind, _, blob = line.rstrip("\n").partition("\t")
        yield kind, blob.replace("\\n", "\n")

# minimal DOM
class Node:
    def __init__(self, tag, parent=None):
        self.tag, self.parent, self.children, self.text = tag, parent, [], ""
    def alltext(self):
        out = [self.text]
        for c in self.children:
            out.append(c.alltext())
        return "".join(out)

class P(HTMLParser):
    def __init__(self):
        super().__init__()
        self.root = Node("root"); self.cur = self.root
    def handle_starttag(self, tag, attrs):
        if tag in ("br",): return
        n = Node(tag, self.cur); self.cur.children.append(n); self.cur = n
    def handle_endtag(self, tag):
        c = self.cur
        while c is not self.root and c.tag != tag: c = c.parent
        if c is not self.root: self.cur = c.parent
    def handle_data(self, data):
        n = Node("#text", self.cur); n.text = data; self.cur.children.append(n)

def parse(blob):
    p = P(); p.feed(blob); return p.root

def elems(n, tag=None):
    for c in n.children:
        if c.tag != "#text" and (tag is None or c.tag == tag):
            yield c

def norm(s):
    return re.sub(r"\s+", " ", s.replace("​","").replace("\xa0"," ")).strip()

MONTHS = r"(January|February|March|April|May|June|July|August|September|October|November|December)"
WD = r"(Monday|Tuesday|Wednesday|Thursday|Friday|Saturday|Sunday)"
# a "date-ish" heading: optional weekday, month day, possibly ranges w/ to/and/until
DATEISH = re.compile(rf"^({WD},?\s+)?{MONTHS}\s+\d{{1,2}}", re.I)

def classify_li_head(text):
    t = norm(text)
    if DATEISH.match(t): return "date"
    if re.match(rf"^{WD}s?\b", t, re.I): return "weekday-only"
    return "other"

def top_shape(root):
    """shape of top-level element sequence"""
    return ",".join(c.tag for c in elems(root))

def main():
    stats = collections.Counter()

    li_head_kinds = collections.Counter()
    item_tails = collections.Counter()
    odd = []

    for kind, blob in load(sys.argv[1] if len(sys.argv)>1 else "blocks-uniq.txt"):
        if kind != sys.argv[2 if len(sys.argv)>2 else 999] and len(sys.argv)>2: continue
        root = parse(blob)
        shape = top_shape(root)
        stats[(kind, shape)] += 1
        # dig into uls: are all top li's date-headed with nested ul?
        for ul in elems(root, "ul"):
            for li in elems(ul, "li"):
                subuls = list(elems(li, "ul"))
                # head text = li text excluding nested ul
                head = "".join(c.alltext() for c in li.children if c.tag != "ul")
                hk = classify_li_head(head)
                li_head_kinds[(hk, bool(subuls))] += 1
                if hk == "other" and subuls:
                    odd.append(norm(head)[:100])
                for sub in subuls:
                    for item in elems(sub, "li"):
                        t = norm(item.alltext())
                        # tail keyword
                        m = re.search(r"(cancelled|canceled|added|closed|moved[^,]*|changed[^,]*|resume[^,]*|only|available)\W*$", t, re.I)
                        item_tails[m.group(1).lower() if m else "NONE:"+t[-40:]] += 1

    for k, v in sorted(stats.items(), key=lambda x:-x[1])[:30]:
        print(v, k)
    print("\n--- li head kinds (kind, has nested ul) ---")
    for k, v in sorted(li_head_kinds.items(), key=lambda x:-x[1]):
        print(v, k)
    print("\n--- item tails ---")
    for k, v in sorted(item_tails.items(), key=lambda x:-x[1])[:40]:
        print(v, k)
    print("\n--- odd date-heads with nested ul ---")
    for t in sorted(set(odd))[:40]:
        print(repr(t))


if __name__ == "__main__":
    main()