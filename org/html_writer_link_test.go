package org

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"

	h "golang.org/x/net/html"
)

// tokenElement is one HTML element (start or self-closing tag) with its
// attributes as decoded by the tokenizer (i.e. entities already unescaped).
type tokenElement struct {
	tag   string
	attrs map[string]string
}

// tokenizeLinkHTML parses htmlOut with the golang.org/x/net/html tokenizer
// and returns the element sequence (with decoded attributes) and the text
// chunks in document order.
func tokenizeLinkHTML(t *testing.T, htmlOut string) ([]tokenElement, []string) {
	t.Helper()
	var elements []tokenElement
	var texts []string
	tokenizer := h.NewTokenizer(strings.NewReader(htmlOut))
	for {
		switch tokenizer.Next() {
		case h.ErrorToken:
			return elements, texts
		case h.StartTagToken, h.SelfClosingTagToken:
			name, hasAttr := tokenizer.TagName()
			el := tokenElement{tag: string(name), attrs: map[string]string{}}
			if hasAttr {
				for {
					key, val, more := tokenizer.TagAttr()
					el.attrs[string(key)] = string(val)
					if !more {
						break
					}
				}
			}
			elements = append(elements, el)
		case h.TextToken:
			texts = append(texts, string(tokenizer.Text()))
		}
	}
}

func formatTokenElements(elements []tokenElement) string {
	var sb strings.Builder
	for _, el := range elements {
		fmt.Fprintf(&sb, "<%s", el.tag)
		for k, v := range el.attrs {
			fmt.Fprintf(&sb, " %s=%q", k, v)
		}
		sb.WriteString("> ")
	}
	return sb.String()
}

// assertLinkHTML asserts that htmlOut contains exactly the expected element
// sequence with exactly the expected (decoded) attribute values, no event
// handler attributes, and exactly the expected text chunks.
func assertLinkHTML(t *testing.T, context, htmlOut string, wantElements []tokenElement, wantTexts []string) {
	t.Helper()
	elements, texts := tokenizeLinkHTML(t, strings.TrimSpace(htmlOut))
	if len(elements) != len(wantElements) {
		t.Errorf("%s: element count mismatch\n got: %s\nwant: %s", context, formatTokenElements(elements), formatTokenElements(wantElements))
		return
	}
	for i, want := range wantElements {
		got := elements[i]
		if got.tag != want.tag {
			t.Errorf("%s: element %d: got tag %q, want %q", context, i, got.tag, want.tag)
			continue
		}
		if len(got.attrs) != len(want.attrs) {
			t.Errorf("%s: element %d <%s>: got %d attributes (%s), want %d", context, i, got.tag, len(got.attrs), formatTokenElements([]tokenElement{got}), len(want.attrs))
			continue
		}
		for k, wantV := range want.attrs {
			if gotV, ok := got.attrs[k]; !ok {
				t.Errorf("%s: element %d <%s>: missing attribute %q", context, i, got.tag, k)
			} else if gotV != wantV {
				t.Errorf("%s: element %d <%s> attribute %q:\n got: %q\nwant: %q", context, i, got.tag, k, gotV, wantV)
			}
		}
	}
	for _, el := range elements {
		for k := range el.attrs {
			if len(k) >= 2 && strings.EqualFold(k[:2], "on") {
				t.Errorf("%s: element <%s> has unexpected event handler attribute %q", context, el.tag, k)
			}
		}
	}
	if strings.Join(texts, "\x00") != strings.Join(wantTexts, "\x00") {
		t.Errorf("%s: text chunks:\n got: %q\nwant: %q", context, texts, wantTexts)
	}
}

// regularLinkEscapingCase is one row of the cross-branch regression matrix.
// expected is the stable full HTML output; elements/texts describe the
// expected tokenized structure with decoded attribute values.
type regularLinkEscapingCase struct {
	name     string
	org      string
	pretty   bool
	expected string
	elements []tokenElement
	texts    []string
}

func el(tag string, attrs map[string]string) tokenElement { return tokenElement{tag, attrs} }

var regularLinkEscapingCases = []regularLinkEscapingCase{
	{
		name:     "plain link with description",
		org:      `[[https://example.com/p?q="x"&r='y'&z=<>&u=日本&e=&amp;][desc]]`,
		expected: `<p><a href="https://example.com/p?q=&#34;x&#34;&amp;r=&#39;y&#39;&amp;z=&lt;&gt;&amp;u=日本&amp;e=&amp;amp;">desc</a></p>`,
		elements: []tokenElement{el("p", nil), el("a", map[string]string{"href": `https://example.com/p?q="x"&r='y'&z=<>&u=日本&e=&amp;`})},
		texts:    []string{"desc"},
	},
	{
		name:     "link without description",
		org:      `[[https://example.com/p?q="x"&e=&amp;]]`,
		expected: `<p><a href="https://example.com/p?q=&#34;x&#34;&amp;e=&amp;amp;">https://example.com/p?q=&#34;x&#34;&amp;e=&amp;amp;</a></p>`,
		elements: []tokenElement{el("p", nil), el("a", map[string]string{"href": `https://example.com/p?q="x"&e=&amp;`})},
		texts:    []string{`https://example.com/p?q="x"&e=&amp;`},
	},
	{
		name:     "link with empty description",
		org:      `[[https://example.com/p?q="x"&e=&amp;][]]`,
		expected: `<p><a href="https://example.com/p?q=&#34;x&#34;&amp;e=&amp;amp;">https://example.com/p?q=&#34;x&#34;&amp;e=&amp;amp;</a></p>`,
		elements: []tokenElement{el("p", nil), el("a", map[string]string{"href": `https://example.com/p?q="x"&e=&amp;`})},
		texts:    []string{`https://example.com/p?q="x"&e=&amp;`},
	},
	{
		name:     "link with nested markup description",
		org:      `[[https://example.com/p?q="x"&e=&amp;][*bold* 日本 /it/]]`,
		expected: `<p><a href="https://example.com/p?q=&#34;x&#34;&amp;e=&amp;amp;"><strong>bold</strong> 日本 <em>it</em></a></p>`,
		elements: []tokenElement{el("p", nil), el("a", map[string]string{"href": `https://example.com/p?q="x"&e=&amp;`}), el("strong", nil), el("em", nil)},
		texts:    []string{"bold", " 日本 ", "it"},
	},
	{
		name:     "image without description",
		org:      `[[https://example.com/i"m&a<日>.png]]`,
		expected: `<p><img src="https://example.com/i&#34;m&amp;a&lt;日&gt;.png" alt="https://example.com/i&#34;m&amp;a&lt;日&gt;.png" title="https://example.com/i&#34;m&amp;a&lt;日&gt;.png" /></p>`,
		elements: []tokenElement{el("p", nil), el("img", map[string]string{
			"src": `https://example.com/i"m&a<日>.png`, "alt": `https://example.com/i"m&a<日>.png`, "title": `https://example.com/i"m&a<日>.png`,
		})},
		texts: nil,
	},
	{
		name:     "image with image description",
		org:      `[[https://example.com/l?q="1"&e=&amp;][https://example.com/i"&g.png]]`,
		expected: `<p><a href="https://example.com/l?q=&#34;1&#34;&amp;e=&amp;amp;"><img src="https://example.com/i&#34;&amp;g.png" alt="https://example.com/i&#34;&amp;g.png" /></a></p>`,
		elements: []tokenElement{
			el("p", nil),
			el("a", map[string]string{"href": `https://example.com/l?q="1"&e=&amp;`}),
			el("img", map[string]string{"src": `https://example.com/i"&g.png`, "alt": `https://example.com/i"&g.png`}),
		},
		texts: nil,
	},
	{
		name:     "video without description",
		org:      `[[https://example.com/v"m&a<日>.mp4]]`,
		expected: `<p><video src="https://example.com/v&#34;m&amp;a&lt;日&gt;.mp4" title="https://example.com/v&#34;m&amp;a&lt;日&gt;.mp4">https://example.com/v&#34;m&amp;a&lt;日&gt;.mp4</video></p>`,
		elements: []tokenElement{el("p", nil), el("video", map[string]string{
			"src": `https://example.com/v"m&a<日>.mp4`, "title": `https://example.com/v"m&a<日>.mp4`,
		})},
		texts: []string{`https://example.com/v"m&a<日>.mp4`},
	},
	{
		name:     "file link",
		org:      `[[file:/tmp/a"b&c'd.txt][f]]`,
		expected: `<p><a href="/tmp/a&#34;b&amp;c&#39;d.txt">f</a></p>`,
		elements: []tokenElement{el("p", nil), el("a", map[string]string{"href": `/tmp/a"b&c'd.txt`})},
		texts:    []string{"f"},
	},
	{
		name:     "fragment link",
		org:      `[[#sec"tion&frag<日>][jump]]`,
		expected: `<p><a href="#sec&#34;tion&amp;frag&lt;日&gt;">jump</a></p>`,
		elements: []tokenElement{el("p", nil), el("a", map[string]string{"href": `#sec"tion&frag<日>`})},
		texts:    []string{"jump"},
	},
	{
		name:     "relative org link",
		org:      `[[pa"ge&x.org][p]]`,
		expected: `<p><a href="pa&#34;ge&amp;x.html">p</a></p>`,
		elements: []tokenElement{el("p", nil), el("a", map[string]string{"href": `pa"ge&x.html`})},
		texts:    []string{"p"},
	},
	{
		name:     "pretty relative org link",
		org:      `[[sub/pa"ge&x.org][p]]`,
		pretty:   true,
		expected: `<p><a href="../sub/pa&#34;ge&amp;x/">p</a></p>`,
		elements: []tokenElement{el("p", nil), el("a", map[string]string{"href": `../sub/pa"ge&x/`})},
		texts:    []string{"p"},
	},
	{
		name:     "mailto link",
		org:      `[[mailto:a&b@example.com][m]]`,
		expected: `<p><a href="mailto:a&amp;b@example.com">m</a></p>`,
		elements: []tokenElement{el("p", nil), el("a", map[string]string{"href": `mailto:a&b@example.com`})},
		texts:    []string{"m"},
	},
	{
		name: "custom protocol mapping with %s and %h interpolation",
		org: `#+LINK: ex https://ex.com/?raw=%s&enc=%h
[[ex:t"&<日>][d]]`,
		expected: `<p><a href="https://ex.com/?raw=t&#34;&amp;&lt;日&gt;&amp;enc=t%22%26%3C%E6%97%A5%3E">d</a></p>`,
		elements: []tokenElement{el("p", nil), el("a", map[string]string{"href": `https://ex.com/?raw=t"&<日>&enc=t%22%26%3C%E6%97%A5%3E`})},
		texts:    []string{"d"},
	},
	{
		name: "custom protocol mapping with plain prefix",
		org: `#+LINK: ex2 https://ex2.com/
[[ex2:a"&b][d]]`,
		expected: `<p><a href="https://ex2.com/a&#34;&amp;b">d</a></p>`,
		elements: []tokenElement{el("p", nil), el("a", map[string]string{"href": `https://ex2.com/a"&b`})},
		texts:    []string{"d"},
	},
	{
		name:     "already escaped target text",
		org:      `[[https://example.com/?a=&amp;&b=&#34;][d]]`,
		expected: `<p><a href="https://example.com/?a=&amp;amp;&amp;b=&amp;#34;">d</a></p>`,
		elements: []tokenElement{el("p", nil), el("a", map[string]string{"href": `https://example.com/?a=&amp;&b=&#34;`})},
		texts:    []string{"d"},
	},
	{
		name:     "single quotes and angle brackets",
		org:      `[[https://example.com/q?a='b'&c=<d>][x]]`,
		expected: `<p><a href="https://example.com/q?a=&#39;b&#39;&amp;c=&lt;d&gt;">x</a></p>`,
		elements: []tokenElement{el("p", nil), el("a", map[string]string{"href": `https://example.com/q?a='b'&c=<d>`})},
		texts:    []string{"x"},
	},
}

func TestWriteRegularLinkAttributeEscaping(t *testing.T) {
	for _, tc := range regularLinkEscapingCases {
		t.Run(tc.name, func(t *testing.T) {
			writer := NewHTMLWriter()
			writer.PrettyRelativeLinks = tc.pretty
			out, err := New().Silent().Parse(strings.NewReader(tc.org), "").Write(writer)
			if err != nil {
				t.Fatalf("%s\n got error: %s", tc.org, err)
			}
			if got := strings.TrimSpace(out); got != tc.expected {
				t.Errorf("stable HTML mismatch:\n%s", diff(got, tc.expected))
			}
			assertLinkHTML(t, tc.name, out, tc.elements, tc.texts)
		})
	}
}

// findRegularLink returns the first RegularLink in the node tree, or nil.
func findRegularLink(nodes []Node) *RegularLink {
	for _, n := range nodes {
		switch n := n.(type) {
		case RegularLink:
			l := n
			return &l
		case Paragraph:
			if l := findRegularLink(n.Children); l != nil {
				return l
			}
		case Headline:
			if l := findRegularLink(n.Title); l != nil {
				return l
			}
		}
	}
	return nil
}

const (
	propertySeed  = 1052
	propertyCases = 200
)

// propertyTargetCharset contains dangerous HTML attribute delimiters on
// purpose ('"', ”', '&', '<', '>'), plus unicode, space and percent. It
// deliberately excludes ']' so the generated target survives Org parsing
// unchanged.
var propertyTargetCharset = []rune(`abcXYZ019-._~:/?#@!$&'()*+,;="<>%日 `)

// propertyDescCharset avoids Org emphasis markers so descriptions stay plain
// text nodes.
var propertyDescCharset = []rune(`abcXYZ019 &<>"'日`)

type generatedLink struct {
	org     string
	hasDesc bool
	desc    string
}

func generatePropertyCase(r *rand.Rand) generatedLink {
	var target strings.Builder
	target.WriteString("https://example.com/")
	for i, n := 0, 1+r.Intn(24); i < n; i++ {
		target.WriteRune(propertyTargetCharset[r.Intn(len(propertyTargetCharset))])
	}
	switch r.Intn(4) {
	case 0:
		target.WriteString(".png")
	case 1:
		target.WriteString(".mp4")
	}
	gl := generatedLink{hasDesc: r.Intn(2) == 0}
	if gl.hasDesc {
		var desc strings.Builder
		for i, n := 0, 1+r.Intn(12); i < n; i++ {
			desc.WriteRune(propertyDescCharset[r.Intn(len(propertyDescCharset))])
		}
		gl.desc = desc.String()
		gl.org = "[[" + target.String() + "][" + gl.desc + "]]"
	} else {
		gl.org = "[[" + target.String() + "]]"
	}
	return gl
}

// renderPropertyCase parses gl.org and renders it with a fresh HTMLWriter,
// returning the output, the AST link and any error.
func renderPropertyCase(t *testing.T, gl generatedLink, writer *HTMLWriter) (string, *RegularLink) {
	t.Helper()
	doc := New().Silent().Parse(strings.NewReader(gl.org), "")
	link := findRegularLink(doc.Nodes)
	if link == nil {
		t.Fatalf("no RegularLink parsed from %q", gl.org)
	}
	if writer == nil {
		writer = NewHTMLWriter()
	}
	out, err := doc.Write(writer)
	if err != nil {
		t.Fatalf("%q: write error: %s", gl.org, err)
	}
	return out, link
}

func (gl generatedLink) context(link *RegularLink, out string) string {
	elements, _ := tokenizeLinkHTML(&testing.T{}, out)
	return fmt.Sprintf("seed=%d org=%q astTarget=%q protocol=%q kind=%q tokens=[%s]",
		propertySeed, gl.org, link.URL, link.Protocol, link.Kind(), formatTokenElements(elements))
}

// checkPropertyCase verifies the serialization invariants for one generated
// link: exactly the expected elements and attributes for the routed branch,
// no event handler attributes, and attribute values that decode back to the
// raw AST target (https routing is the identity mapping).
func checkPropertyCase(t *testing.T, gl generatedLink, out string, link *RegularLink) {
	t.Helper()
	ctx := gl.context(link, out)
	elements, _ := tokenizeLinkHTML(t, out)
	for _, el := range elements {
		for k := range el.attrs {
			if len(k) >= 2 && strings.EqualFold(k[:2], "on") {
				t.Errorf("%s: event handler attribute %q on <%s>", ctx, k, el.tag)
			}
		}
	}
	var wantElements []tokenElement
	var wantTexts []string
	switch {
	case link.Description == nil && link.Kind() == "image":
		wantElements = []tokenElement{el("p", nil), el("img", map[string]string{"src": link.URL, "alt": link.URL, "title": link.URL})}
	case link.Description == nil && link.Kind() == "video":
		wantElements = []tokenElement{el("p", nil), el("video", map[string]string{"src": link.URL, "title": link.URL})}
		wantTexts = []string{link.URL}
	case link.Description == nil:
		wantElements = []tokenElement{el("p", nil), el("a", map[string]string{"href": link.URL})}
		wantTexts = []string{link.URL}
	default:
		wantElements = []tokenElement{el("p", nil), el("a", map[string]string{"href": link.URL})}
		wantTexts = []string{gl.desc}
	}
	assertLinkHTML(t, ctx, out, wantElements, wantTexts)
}

func TestWriteRegularLinkAttributeEscapingProperties(t *testing.T) {
	// The whole batch is generated and rendered twice from the same seed;
	// both runs must produce identical inputs and outputs, in order.
	runBatch := func() ([]generatedLink, []string, []*RegularLink) {
		r := rand.New(rand.NewSource(propertySeed))
		links := make([]generatedLink, propertyCases)
		outs := make([]string, propertyCases)
		astLinks := make([]*RegularLink, propertyCases)
		for i := range links {
			links[i] = generatePropertyCase(r)
			outs[i], astLinks[i] = renderPropertyCase(t, links[i], nil)
		}
		return links, outs, astLinks
	}
	links, outs, astLinks := runBatch()
	links2, outs2, _ := runBatch()
	for i := range links {
		if links[i] != links2[i] || outs[i] != outs2[i] {
			t.Fatalf("non-deterministic run at case %d: %+v vs %+v", i, links[i], links2[i])
		}
	}

	for i := range links {
		gl, out, link := links[i], outs[i], astLinks[i]
		ctx := gl.context(link, out)

		// Property 1: dangerous delimiters never increase the attribute or
		// node count beyond the routed branch's fixed shape, and decoded
		// attributes keep the raw target's semantics.
		checkPropertyCase(t, gl, out, link)

		// Property 2: a reused writer must not leak state from the previous
		// link into the next one.
		if i+1 < len(links) {
			shared := NewHTMLWriter()
			outA, _ := renderPropertyCase(t, gl, shared)
			outAB, _ := renderPropertyCase(t, links[i+1], shared)
			if outA != outs[i] || outAB != outs[i]+outs[i+1] {
				t.Errorf("%s: writer state leaked into next link", ctx)
			}
		}

		// Property 3: an equivalent routing configuration (custom protocol
		// mapping to the same final URL) changes only the target mapping,
		// not the number of escaping layers.
		rawTarget := link.URL
		mappedOrg := "#+LINK: pfx https://%s\n[[pfx:" + strings.TrimPrefix(rawTarget, "https://") + "][x]]"
		directOrg := "[[" + rawTarget + "][x]]"
		mappedOut, _ := renderPropertyCase(t, generatedLink{org: mappedOrg}, nil)
		directOut, _ := renderPropertyCase(t, generatedLink{org: directOrg}, nil)
		if mappedOut != directOut {
			t.Errorf("%s: equivalent routing changed escaping layers:\n mapped: %q\n direct: %q", ctx, mappedOut, directOut)
		}
	}
}
