package enrich

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// anchor is a link found inside a block element.
type anchor struct {
	Text, Href string
}

// liNode is a parsed list item: its own content (excluding nested lists) plus
// any nested list items.
type liNode struct {
	Head     string // own text, normText'd, may contain newlines from <br>
	HeadHTML string // the li's full HTML (including nested lists)
	Links    []anchor
	Off      [2]int   // byte offsets into the source block HTML (zero if unknown)
	Items    []liNode // nested list items
}

// blockPart is one top-level element of an HTML block.
type blockPart struct {
	Kind  string // "heading", "para", "list"
	Text  string // normText'd, may contain newlines from <br> (empty for lists)
	HTML  string
	Links []anchor
	Off   [2]int   // byte offsets into the source block HTML (zero if unknown)
	Items []liNode // for lists
}

// splitBlock parses an HTML block into its top-level parts, with byte
// offsets into the source when the markup allows tracking them. Unknown
// elements and stray top-level text are treated as paragraphs. Returns
// ok=false when the HTML failed to parse at all.
func splitBlock(blockHTML string) (parts []blockPart, ok bool) {
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(blockHTML), ctx)
	if err != nil {
		return nil, false
	}

	// tokenizer offsets, aligned to the parse tree by shape (nil on mismatch)
	topOffs, liOffs := nodeOffsets(blockHTML)
	if len(topOffs) != len(nodes) {
		topOffs = nil
	}
	nthLi := 0
	liCount := countLis(nodes)
	if len(liOffs) != liCount {
		liOffs = nil
	}

	for i, n := range nodes {
		var off [2]int
		if topOffs != nil {
			off = topOffs[i]
		}
		switch {
		case n.Type == html.TextNode:
			if t := normText(n.Data); t != "" {
				parts = append(parts, blockPart{Kind: "para", Text: t, HTML: n.Data, Off: off})
			}
		case n.Type != html.ElementNode:
		case n.Data == "h1" || n.Data == "h2" || n.Data == "h3" || n.Data == "h4" || n.Data == "h5" || n.Data == "h6":
			parts = append(parts, blockPart{Kind: "heading", Text: normText(nodeText(n, false)), HTML: renderNode(n), Links: nodeLinks(n), Off: off})
		case n.Data == "ul" || n.Data == "ol":
			parts = append(parts, blockPart{Kind: "list", HTML: renderNode(n), Off: off, Items: listItems(n, liOffs, &nthLi)})
		default:
			if t := normText(nodeText(n, false)); t != "" {
				parts = append(parts, blockPart{Kind: "para", Text: t, HTML: renderNode(n), Links: nodeLinks(n), Off: off})
			}
		}
		if n.Type == html.ElementNode && n.Data != "ul" && n.Data != "ol" {
			nthLi += countLis([]*html.Node{n}) // lis inside non-list elements (rare)
		}
	}
	return parts, true
}

// countLis counts li elements under the nodes, in document order.
func countLis(nodes []*html.Node) int {
	n := 0
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && node.Data == "li" {
			n++
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, node := range nodes {
		walk(node)
	}
	return n
}

// listItems extracts the li children of a ul/ol node, consuming li offsets
// (pre-order) when available.
func listItems(ul *html.Node, liOffs [][2]int, nthLi *int) []liNode {
	var items []liNode
	for c := ul.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || c.Data != "li" {
			continue
		}
		li := liNode{
			Head:     normText(nodeText(c, true)),
			HeadHTML: renderNode(c),
			Links:    nodeLinks(c),
		}
		if liOffs != nil && *nthLi < len(liOffs) {
			li.Off = liOffs[*nthLi]
		}
		*nthLi++
		for cc := c.FirstChild; cc != nil; cc = cc.NextSibling {
			if cc.Type == html.ElementNode && (cc.Data == "ul" || cc.Data == "ol") {
				li.Items = append(li.Items, listItems(cc, liOffs, nthLi)...)
			}
		}
		items = append(items, li)
	}
	return items
}

// voidTags never get end tags; they don't affect nesting depth.
var voidTags = map[string]bool{
	"area": true, "base": true, "br": true, "col": true, "embed": true,
	"hr": true, "img": true, "input": true, "link": true, "meta": true,
	"source": true, "track": true, "wbr": true,
}

// nodeOffsets tokenizes blockHTML and returns byte offset ranges for each
// top-level node (elements and text runs, in order) and for each li element
// (pre-order). Alignment with the parse tree is by shape; callers must
// verify the counts match before using them. Returns nils when the markup
// can't be tracked (unbalanced tags etc).
func nodeOffsets(blockHTML string) (top [][2]int, lis [][2]int) {
	z := html.NewTokenizer(strings.NewReader(blockHTML))
	off := 0
	depth := 0
	topStart := -1
	var liStack []int // indexes into lis
	for {
		tt := z.Next()
		raw := z.Raw()
		start := off
		off += len(raw)
		switch tt {
		case html.ErrorToken:
			if err := z.Err(); err != nil && depth == 0 && off >= len(blockHTML) {
				return top, lis
			}
			return nil, nil
		case html.TextToken:
			if depth == 0 {
				top = append(top, [2]int{start, off})
			}
		case html.SelfClosingTagToken:
			if depth == 0 {
				top = append(top, [2]int{start, off})
			}
		case html.StartTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if voidTags[tag] {
				if depth == 0 {
					top = append(top, [2]int{start, off})
				}
				continue
			}
			if depth == 0 {
				topStart = start
			}
			if tag == "li" {
				liStack = append(liStack, len(lis))
				lis = append(lis, [2]int{start, -1})
			}
			depth++
		case html.EndTagToken:
			name, _ := z.TagName()
			tag := string(name)
			if voidTags[tag] {
				continue
			}
			depth--
			if depth < 0 {
				return nil, nil
			}
			if tag == "li" {
				if len(liStack) == 0 {
					return nil, nil
				}
				lis[liStack[len(liStack)-1]][1] = off
				liStack = liStack[:len(liStack)-1]
			}
			if depth == 0 {
				top = append(top, [2]int{topStart, off})
			}
		}
	}
}

// nodeText extracts the concatenated text of n, converting <br> to newlines.
// Text nodes are joined without inserting spaces (the source HTML splits words
// across elements, e.g. bold split mid-word). If skipLists is true, nested
// ul/ol subtrees are excluded (for li head text).
func nodeText(n *html.Node, skipLists bool) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			b.WriteString(n.Data)
		case html.ElementNode:
			if n.Data == "br" {
				b.WriteByte('\n')
				return
			}
			if skipLists && (n.Data == "ul" || n.Data == "ol") {
				return
			}
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c)
	}
	return b.String()
}

// nodeLinks collects the anchors under n.
func nodeLinks(n *html.Node) []anchor {
	var links []anchor
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			var href string
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = a.Val
				}
			}
			links = append(links, anchor{Text: normText(nodeText(n, false)), Href: href})
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return links
}

func renderNode(n *html.Node) string {
	var b strings.Builder
	html.Render(&b, n)
	return b.String()
}

// BlockText extracts the full text content of an HTML block (for coverage
// checking that every fragment of source text is accounted for by an output
// object).
func BlockText(blockHTML string) string {
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(blockHTML), ctx)
	if err != nil {
		return blockHTML
	}
	var b strings.Builder
	for _, n := range nodes {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		} else {
			b.WriteString(nodeText(n, false))
		}
		b.WriteByte('\n')
	}
	return normText(b.String())
}
