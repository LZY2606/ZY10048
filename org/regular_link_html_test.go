package org

import (
	"bytes"
	"fmt"
	"go/format"
	"html"
	"io"
	"io/fs"
	"math/rand"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	h "golang.org/x/net/html"
)

type regularLinkRoute struct {
	name        string
	rawTarget   string
	wantTarget  string
	pretty      bool
	linkMapping map[string]string
	kind        string
}

type regularLinkDescription struct {
	name string
	text string
	kind string
	want string
}

type regularLinkCase struct {
	route       regularLinkRoute
	description regularLinkDescription
	org         string
}

var regularLinkTargetPayloads = []string{
	"double-quote\" onmouseover=alert(1) x=",
	"single-quote' onclick=alert(1) y=",
	"ampersand?a=1&b=2&already=one&amp;two",
	"angle<b onClick=alert(1)>after",
	"Unicode 日本語 é🧭",
}

var regularLinkDescriptionCases = []regularLinkDescription{
	{name: "no-description", kind: "route"}, {name: "plain", text: "safe label", kind: "regular", want: `safe label`},
	{name: "empty", text: "", kind: "regular", want: ""},
	{name: "nested", text: `*bold "quoted" & 日本語*`, kind: "regular", want: `<strong>bold &#34;quoted&#34; &amp; 日本語</strong>`},
	{name: "escaped", text: `already &amp; "quoted" <日本語>`, kind: "regular", want: `already &amp;amp; &#34;quoted&#34; &lt;日本語&gt;`},
	{name: "image", text: `file:inner "quote" &amp;.png`, kind: "image", want: `inner &#34;quote&#34; &amp;amp;.png`},
	{name: "video", text: `file:inner 'quote' &amp;.mp4`, kind: "video", want: `inner &#39;quote&#39; &amp;amp;.mp4`},
}

func TestRegularLinkAttributeRegressionMatrix(t *testing.T) {
	cases := buildRegularLinkMatrixCases()
	for _, tc := range cases {
		t.Run(tc.route.name+"/"+tc.description.name, func(t *testing.T) {
			t.Helper()
			doc := parseRegularLinkDocument(t, tc.org, tc.route.linkMapping)
			link := findRegularLink(doc.Nodes)
			if link == nil {
				t.Fatalf("no RegularLink in AST")
			}
			if link.URL != tc.route.rawTarget {
				t.Fatalf("AST target was modified\n got: %q\nwant: %q", link.URL, tc.route.rawTarget)
			}
			if got := link.Kind(); got != tc.route.kind {
				t.Fatalf("routed kind mismatch\n got: %q\nwant: %q", got, tc.route.kind)
			}

			writer := NewHTMLWriter()
			writer.PrettyRelativeLinks = tc.route.pretty
			out, err := doc.Write(writer)
			if err != nil {
				t.Fatal(err)
			}
			out = strings.TrimSpace(out)
			want := expectedRegularLinkHTML(tc)
			if out != want {
				t.Fatalf("stable HTML mismatch\n%s\nAST target: %q\nbranch: %s\norg: %q", diff(out, want), link.URL, link.Kind(), tc.org)
			}

			attributes := tokenizedRegularLinkAttributes(t, out)
			verifyRegularLinkDOM(t, out, tc, attributes)
		})
	}
}

func TestRegularLinkAttributeProperties(t *testing.T) {
	const seed int64 = 10520260920
	first := generateRegularLinkProperties(t, seed)
	second := generateRegularLinkProperties(t, seed)
	if len(first) == 0 || len(second) == 0 || len(first) != len(second) {
		t.Fatalf("non-deterministic property count: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].org != second[i].org || first[i].html != second[i].html || first[i].target != second[i].target {
			t.Fatalf("property case %d differs between runs", i)
		}
	}
	for _, generated := range first {
		attributes := tokenizedRegularLinkAttributes(t, generated.html)
		if len(attributes) != generated.attrCount {
			t.Fatalf(regularLinkFailure(generated, attributes, "dangerous separators changed attribute count"))
		}
		if hasEventAttribute(attributes) {
			t.Fatalf(regularLinkFailure(generated, attributes, "event attribute was introduced"))
		}
		verifyGeneratedRegularLinkDOM(t, generated)
		for key, value := range attributes {
			if value != generated.target {
				t.Fatalf(regularLinkFailure(generated, attributes, "decoded attribute target mismatch for "+strings.SplitN(key, "#", 2)[0]))
			}
		}
	}

	combinedCases := make([]generatedRegularLinkProperty, 0, len(first))
	for _, generated := range first {
		if generated.name == "pretty/nested" || generated.name == "pretty/no-description" {
			continue
		}
		combinedCases = append(combinedCases, generated)
	}
	combinedOrg := strings.Join(propertyOrgInputs(first), "\n\n")
	doc := parseRegularLinkDocument(t, combinedOrg, map[string]string{
		"goorg":  "https://example.test/search?q=%s",
		"goorgh": "https://example.test/search?q=%h",
	})
	writer := NewHTMLWriter()
	out, err := doc.Write(writer)
	if err != nil {
		t.Fatal(err)
	}
	root, err := h.Parse(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	paragraphs := htmlElementChildren(root, "p")
	if len(paragraphs) != len(combinedCases) {
		t.Fatalf("paragraph count changed: got %d want %d\n%s", len(paragraphs), len(combinedCases), out)
	}
	var builder strings.Builder
	for i, generated := range combinedCases {
		builder.Reset()
		if err := h.Render(&builder, paragraphs[i]); err != nil {
			t.Fatal(err)
		}
		if strings.Join(strings.Fields(builder.String()), "") != strings.Join(strings.Fields(generated.html), "") {
			t.Fatalf("state leaked between links at %d\nprevious: %q\n got: %q\nwant: %q", i, combinedCases[maxInt(i-1, 0)].target, builder.String(), generated.html)
		}
	}

	mappedInput := `[[goorg-a:quote " & <日本語>]]`
	unmappedInput := `[[goorg-b:quote " & <日本語>]]`
	mapped := parseRegularLinkDocument(t, "#+LINK: goorg-a https://example.test/%s\n"+mappedInput, map[string]string{"goorg-a": "https://example.test/%s"})
	unmapped := parseRegularLinkDocument(t, "#+LINK: goorg-b https://example.test/%s\n"+unmappedInput, map[string]string{"goorg-b": "https://example.test/%s"})
	mappedOut := strings.TrimSpace(mustWriteRegularLink(t, mapped, false))
	unmappedOut := strings.TrimSpace(mustWriteRegularLink(t, unmapped, false))
	if mappedOut != unmappedOut {
		t.Fatalf("equivalent route mappings changed escaping layers\n%s", diff(mappedOut, unmappedOut))
	}

	legal := parseRegularLinkDocument(t, `[[https://example.test/path?a=1&b=2#section][legal & path]]`, nil)
	link := findRegularLink(legal.Nodes)
	legalOut := strings.TrimSpace(mustWriteRegularLink(t, legal, false))
	attrs := flattenedRegularLinkAttributes(tokenizedRegularLinkAttributes(t, legalOut))
	if attrs["href"][0] != link.URL || strings.Contains(legalOut, "%22") || strings.Contains(legalOut, "%26") {
		t.Fatalf("legal URL semantics changed: %q -> %q", link.URL, legalOut)
	}
	if got := link.String(); got != `[[https://example.test/path?a=1&b=2#section][legal & path]]` {
		t.Fatalf("Org round-trip changed: %q", got)
	}
}

type regularLinkRouteSpec struct {
	name    string
	mapping map[string]string
	pretty  bool
	kind    string
	raw     func(string) string
	target  func(string) string
}

func buildRegularLinkMatrixCases() []regularLinkCase {
	routes := []regularLinkRouteSpec{
		{
			name:   "regular",
			kind:   "regular",
			raw:    func(payload string) string { return "https://example.test/path?q=" + payload },
			target: func(payload string) string { return "https://example.test/path?q=" + payload },
		},
		{
			name:   "mailto",
			kind:   "regular",
			raw:    func(payload string) string { return "mailto:user@example.test?subject=" + payload },
			target: func(payload string) string { return "mailto:user@example.test?subject=" + payload },
		},
		{
			name:   "image",
			kind:   "image",
			raw:    func(payload string) string { return "https://example.test/" + payload + ".png" },
			target: func(payload string) string { return "https://example.test/" + payload + ".png" },
		},
		{
			name:   "video",
			kind:   "video",
			raw:    func(payload string) string { return "https://example.test/" + payload + ".mp4" },
			target: func(payload string) string { return "https://example.test/" + payload + ".mp4" },
		},
		{
			name:   "file",
			kind:   "regular",
			raw:    func(payload string) string { return "file:report-" + payload + ".org" },
			target: func(payload string) string { return "report-" + payload + ".html" },
		},
		{
			name:   "fragment",
			kind:   "regular",
			raw:    func(payload string) string { return "#section-" + payload },
			target: func(payload string) string { return "#section-" + payload },
		},
		{
			name:   "relative",
			kind:   "regular",
			raw:    func(payload string) string { return "docs/" + payload + ".org" },
			target: func(payload string) string { return "docs/" + payload + ".html" },
		},
		{
			name:   "pretty-relative",
			kind:   "regular",
			pretty: true,
			raw:    func(payload string) string { return "docs/" + payload + ".org" },
			target: func(payload string) string { return "../docs/" + payload + "/" },
		},
		{
			name:   "pretty-relative-image",
			kind:   "image",
			pretty: true,
			raw:    func(payload string) string { return "images/" + payload + ".PNG" },
			target: func(payload string) string { return "../images/" + payload + ".PNG" },
		},
		{
			name:    "custom-s",
			kind:    "regular",
			mapping: map[string]string{"goorg": "https://example.test/search?q=%s"},
			raw:     func(payload string) string { return "goorg:" + payload },
			target:  func(payload string) string { return "https://example.test/search?q=" + payload },
		},
		{
			name:    "custom-h",
			kind:    "regular",
			mapping: map[string]string{"goorgh": "https://example.test/search?q=%h"},
			raw:     func(payload string) string { return "goorgh:" + payload },
			target:  func(payload string) string { return "https://example.test/search?q=" + url.QueryEscape(payload) },
		},
		{
			name:    "custom-exact",
			kind:    "regular",
			mapping: map[string]string{`custom "quote" &amp; <日本語>`: "https://example.test/exact"},
			raw:     func(payload string) string { return payload },
			target:  func(payload string) string { return "https://example.test/exact" },
		},
	}

	cases := []regularLinkCase{}
	for _, routeSpec := range routes {
		payload := strings.Join(regularLinkTargetPayloads, "/")
		if routeSpec.name == "custom-exact" {
			payload = `custom "quote" &amp; <日本語>`
		}
		for _, description := range regularLinkDescriptionCases {
			kind := routeSpec.kind
			switch description.name {
			case "no-description":
			case "empty":
				kind = routeSpec.kind
			default:
				if description.kind == "image" || description.kind == "video" {
					kind = description.kind
				} else {
					kind = "regular"
				}
			}
			raw := routeSpec.raw(payload)
			org := "[[" + raw + "]]"
			if description.name != "no-description" {
				org = "[[" + raw + "][" + description.text + "]]"
			}
			cases = append(cases, regularLinkCase{
				route: regularLinkRoute{
					name:        routeSpec.name,
					rawTarget:   raw,
					wantTarget:  routeSpec.target(payload),
					pretty:      routeSpec.pretty,
					linkMapping: routeSpec.mapping,
					kind:        kind,
				},
				description: description,
				org:         org,
			})
		}
	}
	return cases
}

func expectedRegularLinkHTML(tc regularLinkCase) string {
	target := html.EscapeString(tc.route.wantTarget)
	switch tc.description.kind {
	case "image":
		return `<p><a href="` + target + `"><img src="` + tc.description.want + `" alt="` + tc.description.want + `" /></a></p>`
	case "video":
		return `<p><a href="` + target + `"><video src="` + tc.description.want + `" title="` + tc.description.want + `"></video></a></p>`
	}
	switch tc.route.kind {
	case "image":
		return `<p><img src="` + target + `" alt="` + target + `" title="` + target + `" /></p>`
	case "video":
		return `<p><video src="` + target + `" title="` + target + `">` + target + `</video></p>`
	default:
		description := target
		if tc.route.kind == "image" || tc.route.kind == "video" {
			if tc.description.name != "no-description" && tc.description.name != "empty" {
				description = tc.description.want
			}
		} else if tc.description.name != "no-description" && tc.description.name != "empty" {
			description = tc.description.want
		}
		return `<p><a href="` + target + `">` + description + `</a></p>`
	}
}

func parseRegularLinkDocument(t *testing.T, input string, links map[string]string) *Document {
	t.Helper()
	doc := New().Silent().Parse(strings.NewReader(input), "./regular-link-tests.org")
	if doc.Error != nil {
		t.Fatal(doc.Error)
	}
	for key, value := range links {
		doc.Links[key] = value
	}
	return doc
}

func mustWriteRegularLink(t *testing.T, doc *Document, pretty bool) string {
	t.Helper()
	writer := NewHTMLWriter()
	writer.PrettyRelativeLinks = pretty
	out, err := doc.Write(writer)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func findRegularLink(nodes []Node) *RegularLink {
	for _, node := range nodes {
		switch n := node.(type) {
		case RegularLink:
			link := n
			return &link
		case Paragraph:
			if link := findRegularLink(n.Children); link != nil {
				return link
			}
		case Emphasis:
			if link := findRegularLink(n.Content); link != nil {
				return link
			}
		}
	}
	return nil
}

func collectRegularLinks(nodes []Node) []RegularLink {
	var links []RegularLink
	var walk func([]Node)
	walk = func(children []Node) {
		for _, node := range children {
			switch n := node.(type) {
			case RegularLink:
				links = append(links, n)
			case Paragraph:
				walk(n.Children)
			case Emphasis:
				walk(n.Content)
			}
		}
	}
	walk(nodes)
	return links
}

type regularLinkTokenAttributes map[string]string

func tokenizedRegularLinkAttributes(t *testing.T, output string) regularLinkTokenAttributes {
	t.Helper()
	tokenizer := h.NewTokenizer(strings.NewReader(output))
	attributes := regularLinkTokenAttributes{}
	for {
		eventType := tokenizer.Next()
		if eventType == h.ErrorToken {
			if tokenizer.Err() == io.EOF {
				return attributes
			}
			t.Fatal(tokenizer.Err())
		}
		if eventType != h.StartTagToken && eventType != h.SelfClosingTagToken {
			continue
		}
		_, hasMore := tokenizer.TagName()
		for hasMore {
			key, value, more := tokenizer.TagAttr()
			attributes[string(key)+"#"+fmt.Sprint(len(attributes))] = string(value)
			hasMore = more
		}
	}
}

func flattenedRegularLinkAttributes(attributes regularLinkTokenAttributes) map[string][]string {
	flattened := map[string][]string{}
	for key, value := range attributes {
		name := strings.SplitN(key, "#", 2)[0]
		flattened[name] = append(flattened[name], value)
	}
	return flattened
}

func hasEventAttribute(attributes regularLinkTokenAttributes) bool {
	for key := range attributes {
		name := strings.ToLower(strings.SplitN(key, "#", 2)[0])
		if strings.HasPrefix(name, "on") {
			return true
		}
	}
	return false
}

func verifyRegularLinkDOM(t *testing.T, output string, tc regularLinkCase, tokenAttributes regularLinkTokenAttributes) {
	t.Helper()
	root, err := h.Parse(strings.NewReader(output))
	if err != nil {
		t.Fatal(err)
	}
	paragraph := findHTMLNode(root, func(node *h.Node) bool { return node.Type == h.ElementNode && node.Data == "p" })
	if paragraph == nil || htmlChildCount(paragraph) != 1 {
		t.Fatalf("target must be the only child of its paragraph\n%s", output)
	}
	element := paragraph.FirstChild
	if element.Type != h.ElementNode {
		t.Fatalf("unexpected target node: %#v", element)
	}
	if got := decodedElementAttribute(element, "href"); got != "" && got != tc.route.wantTarget {
		t.Fatalf("decoded href mismatch\n got: %q\nwant: %q", got, tc.route.wantTarget)
	}
	expectedAttributes := map[string]int{}
	switch tc.description.kind {
	case "image":
		if element.Data != "a" || htmlChildCount(element) != 1 || element.FirstChild.Data != "img" {
			t.Fatalf("nested image description structure changed:\n%s", output)
		}
		expectedAttributes["href"] = 1
		img := element.FirstChild
		if got := decodedElementAttribute(img, "src"); got != strings.TrimPrefix(tc.description.text, "file:") {
			t.Fatalf("decoded image description mismatch: %q", got)
		}
		expectedAttributes["src"]++
		expectedAttributes["alt"]++
	case "video":
		if element.Data != "a" || htmlChildCount(element) != 1 || element.FirstChild.Data != "video" {
			t.Fatalf("nested video description structure changed:\n%s", output)
		}
		expectedAttributes["href"] = 1
		expectedAttributes["src"]++
		expectedAttributes["title"]++
	default:
		switch tc.route.kind {
		case "image":
			if element.Data != "img" || htmlChildCount(element) != 0 {
				t.Fatalf("image structure changed:\n%s", output)
			}
			expectedAttributes["src"] = 1
			expectedAttributes["alt"] = 1
			expectedAttributes["title"] = 1
		case "video":
			if element.Data != "video" || htmlChildCount(element) != 1 {
				t.Fatalf("video structure changed:\n%s", output)
			}
			expectedAttributes["src"] = 1
			expectedAttributes["title"] = 1
		default:
			if element.Data != "a" || htmlChildCount(element) != 1 {
				t.Fatalf("anchor structure changed:\n%s", output)
			}
			expectedAttributes["href"] = 1
		}
	}
	flattened := flattenedRegularLinkAttributes(tokenAttributes)
	attributeCount := 0
	for _, values := range flattened {
		attributeCount += len(values)
	}
	if attributeCount != len(expectedAttributes) {
		t.Fatalf("unexpected attributes: %#v", flattened)
	}
	for key, count := range expectedAttributes {
		if len(flattened[key]) != count {
			t.Fatalf("attribute %s count = %d, want %d: %#v", key, len(flattened[key]), count, flattened)
		}
	}
	if tc.route.kind == "regular" {
		verifyRegularLinkDescriptionStructure(t, element, tc)
	}
}

func verifyRegularLinkDescriptionStructure(t *testing.T, element *h.Node, tc regularLinkCase) {
	t.Helper()
	if tc.description.kind == "image" || tc.description.kind == "video" {
		return
	}
	textNode := element.FirstChild
	if tc.description.name == "nested" {
		if textNode == nil || textNode.Type != h.ElementNode || textNode.Data != "strong" || htmlChildCount(textNode) != 1 {
			t.Fatalf("nested description structure changed: %#v", element)
		}
		textNode = textNode.FirstChild
	}
	if tc.description.name == "no-description" || tc.description.name == "empty" {
		if textNode == nil || textNode.Type != h.TextNode || textNode.Data != tc.route.wantTarget {
			t.Fatalf("no-description text changed: %#v", element)
		}
		return
	}
	if textNode == nil || textNode.Type != h.TextNode {
		t.Fatalf("description text structure changed: %#v", element)
	}
}

func findHTMLNode(node *h.Node, match func(*h.Node) bool) *h.Node {
	if match(node) {
		return node
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if found := findHTMLNode(child, match); found != nil {
			return found
		}
	}
	return nil
}

func decodedElementAttribute(node *h.Node, key string) string {
	for _, attribute := range node.Attr {
		if attribute.Key == key {
			return attribute.Val
		}
	}
	return ""
}

type generatedRegularLinkProperty struct {
	seed      int64
	name      string
	org       string
	html      string
	target    string
	astTarget string
	kind      string
	attrCount int
}

func generateRegularLinkProperties(t *testing.T, seed int64) []generatedRegularLinkProperty {
	t.Helper()
	random := rand.New(rand.NewSource(seed))
	chars := []rune(`ABC09-._~?@!$'()*+,;=/%日本語`)
	payloads := make([]string, 96)
	for i := range payloads {
		builder := strings.Builder{}
		builder.WriteString(`" ' & < > 語`)
		length := random.Intn(7) + 1
		for j := 0; j < length; j++ {
			builder.WriteRune(chars[random.Intn(len(chars))])
		}
		payloads[i] = builder.String()
	}

	var generated []generatedRegularLinkProperty
	for i, rawPayload := range payloads {
		routeName := []string{"regular", "image", "video", "file", "fragment", "relative", "pretty", "custom-s", "custom-h", "mailto"}[i%10]
		payload := sanitizeGeneratedRegularLinkPayload(routeName, rawPayload)
		for _, mode := range []string{"no-description", "nested"} {
			raw, target, kind, pretty, mapping := generatedRegularLinkRoute(routeName, payload)
			description := ""
			if mode == "nested" {
				description = `*` + payload + `*`
			}
			org := "[[" + raw + "]]"
			if description != "" {
				org = "[[" + raw + "][" + description + "]]"
			}
			doc := parseRegularLinkDocument(t, org, mapping)
			link := findRegularLink(doc.Nodes)
			if link == nil || link.URL != raw {
				t.Fatalf("seed %d case %d AST target mismatch: %q -> %#v", seed, i, raw, link)
			}
			out := strings.TrimSpace(mustWriteRegularLink(t, doc, pretty))
			if mode == "nested" {
				kind = "regular"
			}
			attrCount := generatedRegularLinkAttributeCount(kind)
			generated = append(generated, generatedRegularLinkProperty{
				seed:      seed,
				name:      routeName + "/" + mode,
				org:       org,
				html:      out,
				target:    target,
				astTarget: raw,
				kind:      kind,
				attrCount: attrCount,
			})
		}
	}
	return generated
}

func generatedRegularLinkRoute(routeName, payload string) (raw, target, kind string, pretty bool, mapping map[string]string) {
	switch routeName {
	case "image":
		return "https://example.test/" + payload + ".png", "https://example.test/" + payload + ".png", "image", false, nil
	case "video":
		return "https://example.test/" + payload + ".mp4", "https://example.test/" + payload + ".mp4", "video", false, nil
	case "file":
		return "file:docs/" + payload + ".org", "docs/" + payload + ".html", "regular", false, nil
	case "fragment":
		return "#" + payload, "#" + payload, "regular", false, nil
	case "relative":
		return "docs/" + payload + ".org", "docs/" + payload + ".html", "regular", false, nil
	case "pretty":
		return "docs/" + payload + ".org", "../docs/" + payload + "/", "regular", true, nil
	case "custom-s":
		return "goorg:" + payload, "https://example.test/search?q=" + payload, "regular", false, map[string]string{"goorg": "https://example.test/search?q=%s"}
	case "custom-h":
		return "goorgh:" + payload, "https://example.test/search?q=" + url.QueryEscape(payload), "regular", false, map[string]string{"goorgh": "https://example.test/search?q=%h"}
	case "mailto":
		return "mailto:user@example.test?subject=" + payload, "mailto:user@example.test?subject=" + payload, "regular", false, nil
	default:
		return "https://example.test/?q=" + payload, "https://example.test/?q=" + payload, "regular", false, nil
	}
}

func sanitizeGeneratedRegularLinkPayload(routeName, payload string) string {
	replacer := strings.NewReplacer("]", "", "\n", "", ":", "", "#", "")
	payload = replacer.Replace(payload)
	if routeName == "file" || routeName == "relative" || routeName == "pretty" {
		payload = strings.NewReplacer("/", "", ".", "").Replace(payload)
	}
	if strings.Contains(payload, ".org") || strings.HasSuffix(payload, ".") || strings.HasSuffix(payload, "/") {
		payload = strings.TrimSuffix(payload, ".") + "x"
	}
	return payload
}

func generatedRegularLinkAttributeCount(kind string) int {
	if kind == "image" {
		return 3
	}
	if kind == "video" {
		return 2
	}
	return 1
}

func propertyOrgInputs(cases []generatedRegularLinkProperty) []string {
	var orgs []string
	for i := range cases {
		if cases[i].name == "pretty/nested" || cases[i].name == "pretty/no-description" {
			continue
		}
		orgs = append(orgs, cases[i].org)
	}
	return orgs
}

func regularLinkFailure(generated generatedRegularLinkProperty, attributes regularLinkTokenAttributes, reason string) string {
	keys := make([]string, 0, len(attributes))
	for key, value := range attributes {
		keys = append(keys, key+"="+value)
	}
	sort.Strings(keys)
	return fmt.Sprintf("%s\nseed input: %q\nAST target: %q\nbranch: %s\ntoken attributes: %s", reason, generated.org, generated.target, generated.kind, strings.Join(keys, ", "))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func htmlChildCount(node *h.Node) int {
	count := 0
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		count++
	}
	return count
}

func TestRegularLinkAttributeMutationTarget(t *testing.T) {
	t.Run("attribute escaping bypass", func(t *testing.T) {
		orgInput := `[[https://example.test/a" onmouseover=alert(1).png]]`
		doc := parseRegularLinkDocument(t, orgInput, nil)
		out := strings.TrimSpace(mustWriteRegularLink(t, doc, false))
		attributes := tokenizedRegularLinkAttributes(t, out)
		if len(attributes) != 3 || hasEventAttribute(attributes) {
			t.Fatalf("attribute injection was not isolated:\n%s\n%#v", out, attributes)
		}
	})

	t.Run("pre-routing escape changes route", func(t *testing.T) {
		rawTarget := `custom "quote" &amp; <日本語>`
		doc := parseRegularLinkDocument(t, "[["+rawTarget+"]]", map[string]string{rawTarget: "https://example.test/exact"})
		out := strings.TrimSpace(mustWriteRegularLink(t, doc, false))
		attributes := tokenizedRegularLinkAttributes(t, out)
		flattened := flattenedRegularLinkAttributes(attributes)
		if flattened["href"][0] != "https://example.test/exact" {
			t.Fatalf("pre-routing escape changed route mapping:\n%s\n%#v", out, flattened)
		}
	})

	t.Run("whole tag remains markup", func(t *testing.T) {
		doc := parseRegularLinkDocument(t, `[[https://example.test/a<b>&c][label]]`, nil)
		out := strings.TrimSpace(mustWriteRegularLink(t, doc, false))
		root, err := h.Parse(strings.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		if findHTMLNode(root, func(node *h.Node) bool { return node.Type == h.ElementNode && node.Data == "a" }) == nil {
			t.Fatalf("whole-tag escaping produced text instead of an anchor:\n%s", out)
		}
	})

	t.Run("ampersand escaped once", func(t *testing.T) {
		doc := parseRegularLinkDocument(t, `[[https://example.test?a=1&b=2&amp;c]]`, nil)
		out := strings.TrimSpace(mustWriteRegularLink(t, doc, false))
		want := `<p><a href="https://example.test?a=1&amp;b=2&amp;amp;c">https://example.test?a=1&amp;b=2&amp;amp;c</a></p>`
		if out != want || strings.Contains(out, "&amp;amp;amp;") {
			t.Fatalf("ampersand escaping layer mismatch:\n%s", diff(out, want))
		}
	})
}

func TestRegularLinkAttributeMutationGuards(t *testing.T) {
	repoRoot := filepath.Clean("..")
	sourcePath := filepath.Join(repoRoot, "org", "html_writer.go")
	original, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	tempRoot := t.TempDir()
	copyRepositoryForMutation(t, repoRoot, tempRoot)
	tempSourcePath := filepath.Join(tempRoot, "org", "html_writer.go")
	defer func() {
		if err := os.WriteFile(sourcePath, original, 0o644); err != nil {
			t.Fatal(err)
		}
	}()
	t.Cleanup(func() {
		restored, err := os.ReadFile(sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(restored, original) {
			t.Fatalf("mutation source was not restored")
		}
	})

	mutations := []regularLinkMutation{
		{
			name:   "bypasses attribute escaping",
			target: "attribute escaping bypass",
			apply: func(source string) string {
				return strings.ReplaceAll(source, "return html.EscapeString(value)", "return value")
			},
		},
		{
			name:   "escapes before routing",
			target: "pre-routing escape changes route",
			apply: func(source string) string {
				old := "kind := l.Kind()\n\ttarget := w.regularLinkTarget(l)"
				new := "escapedLink := RegularLink{Protocol: l.Protocol, Description: l.Description, URL: html.EscapeString(l.URL), AutoLink: l.AutoLink}\n\tkind := escapedLink.Kind()\n\ttarget := w.regularLinkTarget(escapedLink)"
				return strings.ReplaceAll(source, old, new)
			},
		},
		{
			name:   "escapes the complete tag",
			target: "whole tag remains markup",
			apply: func(source string) string {
				return strings.ReplaceAll(source, "w.WriteString(output)", "w.WriteString(html.EscapeString(output))")
			},
		},
		{
			name:   "double escapes ampersands",
			target: "ampersand escaped once",
			apply: func(source string) string {
				return strings.ReplaceAll(source, "return html.EscapeString(value)", "return strings.ReplaceAll(html.EscapeString(value), \"&\", \"&amp;\")")
			},
		},
	}

	for _, mutation := range mutations {
		mutation := mutation
		name := mutation.name
		t.Run(name, func(t *testing.T) {
			source := mutation.apply(string(original))
			if source == string(original) {
				t.Fatalf("mutation %s did not modify source", name)
			}
			formatted, err := format.Source([]byte(source))
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(tempSourcePath, formatted, 0o644); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("go", "test", "./org", "-run", "TestRegularLinkAttributeMutationTarget/"+strings.ReplaceAll(mutation.target, " ", "_"), "-count=1")
			cmd.Dir = tempRoot
			cmd.Env = append(os.Environ(), "GOWORK=off", "GOTOOLCHAIN=local")
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("mutation %s was not caught\n%s", name, output)
			}
			if !strings.Contains(strings.ReplaceAll(string(output), "_", " "), mutation.target) {
				t.Fatalf("mutation %s was caught by the wrong assertion\n%s", name, output)
			}
		})
	}
}

func copyRepositoryForMutation(t *testing.T, sourceRoot, targetRoot string) {
	t.Helper()
	err := filepath.WalkDir(sourceRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(sourceRoot, path)
		if err != nil {
			return err
		}
		if relative == ".git" || strings.HasPrefix(relative, ".git"+string(os.PathSeparator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		targetPath := filepath.Join(targetRoot, relative)
		if entry.IsDir() {
			return os.MkdirAll(targetPath, 0o755)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		defer output.Close()
		_, err = io.Copy(output, input)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func htmlElementChildren(root *h.Node, name string) []*h.Node {
	body := findHTMLNode(root, func(node *h.Node) bool { return node.Type == h.ElementNode && node.Data == "body" })
	if body == nil {
		return nil
	}
	var elements []*h.Node
	for child := body.FirstChild; child != nil; child = child.NextSibling {
		if child.Type == h.ElementNode && child.Data == name {
			elements = append(elements, child)
		}
	}
	return elements
}

type regularLinkMutation struct {
	name   string
	target string
	apply  func(string) string
}

func verifyGeneratedRegularLinkDOM(t *testing.T, generated generatedRegularLinkProperty) {
	t.Helper()
	root, err := h.Parse(strings.NewReader(generated.html))
	if err != nil {
		t.Fatal(err)
	}
	paragraphs := htmlElementChildren(root, "p")
	if len(paragraphs) != 1 || htmlChildCount(paragraphs[0]) != 1 {
		t.Fatalf("generated output changed node or sibling count: %s", generated.html)
	}
	element := paragraphs[0].FirstChild
	wantElement := "a"
	if generated.name == "image/no-description" {
		wantElement = "img"
	}
	if generated.name == "video/no-description" {
		wantElement = "video"
	}
	if element.Data != wantElement || element.Type != h.ElementNode {
		t.Fatalf("generated target element changed: got %q want %q", element.Data, wantElement)
	}
}
