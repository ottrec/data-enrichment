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
	Items    []liNode // nested list items
}

// blockPart is one top-level element of an HTML block.
type blockPart struct {
	Kind  string // "heading", "para", "list"
	Text  string // normText'd, may contain newlines from <br> (empty for lists)
	HTML  string
	Links []anchor
	Items []liNode // for lists
}

// splitBlock parses an HTML block into its top-level parts. Unknown elements
// and stray top-level text are treated as paragraphs.
func splitBlock(blockHTML string) []blockPart {
	ctx := &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := html.ParseFragment(strings.NewReader(blockHTML), ctx)
	if err != nil {
		return nil
	}
	var parts []blockPart
	for _, n := range nodes {
		switch {
		case n.Type == html.TextNode:
			if t := normText(n.Data); t != "" {
				parts = append(parts, blockPart{Kind: "para", Text: t, HTML: n.Data})
			}
		case n.Type != html.ElementNode:
		case n.Data == "h1" || n.Data == "h2" || n.Data == "h3" || n.Data == "h4" || n.Data == "h5" || n.Data == "h6":
			parts = append(parts, blockPart{Kind: "heading", Text: normText(nodeText(n, false)), HTML: renderNode(n), Links: nodeLinks(n)})
		case n.Data == "ul" || n.Data == "ol":
			parts = append(parts, blockPart{Kind: "list", HTML: renderNode(n), Items: listItems(n)})
		default:
			if t := normText(nodeText(n, false)); t != "" {
				parts = append(parts, blockPart{Kind: "para", Text: t, HTML: renderNode(n), Links: nodeLinks(n)})
			}
		}
	}
	return parts
}

// listItems extracts the li children of a ul/ol node.
func listItems(ul *html.Node) []liNode {
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
		for cc := c.FirstChild; cc != nil; cc = cc.NextSibling {
			if cc.Type == html.ElementNode && (cc.Data == "ul" || cc.Data == "ol") {
				li.Items = append(li.Items, listItems(cc)...)
			}
		}
		items = append(items, li)
	}
	return items
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
