# Changelog

## Unreleased

### Fixed: HTML attribute escaping for RegularLink targets

**Symptom.** A link target containing `"` (e.g. `[[https://example.com/?a=" onclick="alert(1)][x]]`)
closed the `href` attribute early, so the browser parsed the remainder as new
attributes (attribute injection / XSS). Targets containing only `&` produced
invalid HTML (`?a=1&b=2` emitted as a bare `&` in an attribute).

**Root cause.** The three layers involved in rendering a link conflated their
responsibilities:

1. **Parser** (`org/inline.go`): stores the raw, unescaped target in
   `RegularLink.URL`. This is correct and unchanged.
2. **Routing** (`WriteRegularLink` in `org/html_writer.go`): derives the final
   target from the raw URL - `file:` prefix stripping, `.org` → `.html` /
   `PrettyRelativeLinks` rewriting, and custom `#+LINK:` protocol mapping.
   This layer escaped *some* outputs (`html.EscapeString` inside the custom
   protocol branches, and even then only partially: the plain-prefix branch
   escaped the prefix but appended the raw tag) while leaving the plain
   http/file/relative branches completely unescaped.
3. **Attribute serialization**: `fmt.Sprintf` interpolated the routed target
   into `href`/`src`/`alt`/`title` with no escaping of its own.

**Fix.** The layers are now strictly separated:

- Routing operates only on the raw target and never escapes. `RegularLink.URL`
  in the AST is never mutated, and the values used for image/video/file and
  custom-protocol detection are unchanged, so all existing routing semantics
  (mailto, file, fragment, relative links, `PrettyRelativeLinks`, custom
  schemes, image/video recognition) are preserved.
- Attribute serialization escapes exactly once, via the single helper
  `escapeHTMLAttribute`, applied uniformly to every `RegularLink`-derived
  attribute (`href`, `src`, `alt`, `title`) in every branch (`<a>`, `<img>`,
  `<video>`, with and without description). The same once-escaped value is
  used for the default link text (a URL rendered as its own description),
  which is text context and equally requires escaping.

**Semantics deliberately preserved** (verified by regression and mutation
tests):

- Suspicious characters are never *deleted* - they round-trip through the
  tokenizer back to the exact raw target.
- URLs are *not* wholesale percent-encoded - legal targets stay byte-identical
  after tokenizer decoding; only the five HTML-significant characters are
  entity-escaped, exactly once.
- The whole tag is *not* text-escaped after concatenation - markup structure
  (nested emphasis in descriptions, `<img>` inside `<a>`) is untouched.
- No new sanitizer layer was added - escaping lives at the single existing
  serialization boundary, not in a parallel responsibility-overlapping
  component.

**Why the old tests missed it.** The existing testdata only exercised link
targets composed of URL-safe characters; the single case containing `&`
(`example_interpolate_s`) went through the one routing branch that happened to
escape, so the unescaped plain-link, image, video, file, fragment, relative
and `PrettyRelativeLinks` branches were never checked for attribute-context
safety, and no test ever parsed the emitted HTML to verify attribute
boundaries.

**Regression coverage** (`org/html_writer_link_test.go`):

- `TestWriteRegularLinkAttributeEscaping`: cross-branch matrix (plain link,
  no/empty description, nested-markup description, image, video, file,
  fragment, relative, `PrettyRelativeLinks`, mailto, custom protocol mapping
  with `%s`/`%h`/plain prefix, already-escaped text) combined with `"`, `'`,
  `&`, `<>`, unicode. Each case asserts the stable full HTML and re-parses it
  with the `golang.org/x/net/html` tokenizer: decoded attribute values must
  equal the routed target, each element must carry exactly its expected
  attributes (no injected event handlers, no extra siblings), and description
  text structure must be unchanged.
- `TestWriteRegularLinkAttributeEscapingProperties`: fixed-seed (1052),
  length- and charset-limited generator running 200 targets through Org
  parse → HTML write → tokenizer. Asserts: dangerous delimiters never
  increase attribute/node counts; a reused writer leaks no state between
  links; an equivalent routing configuration changes only the target mapping,
  not the number of escaping layers; legal targets survive tokenizer decoding
  semantically intact. Two consecutive runs must be identical. Failures print
  the seed, raw Org input, AST target, routed branch and token attributes.
