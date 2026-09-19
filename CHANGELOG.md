# Changelog

## Unreleased

### Security

- Escape link targets exactly once when they enter an HTML attribute context.

### Changed

- Parser boundary: `RegularLink.URL` continues to store the unmodified Org link target, and descriptions remain parsed and escaped as text/inline markup.
- Routing boundary: file stripping, relative `.org` rewriting, `PrettyRelativeLinks`, mailto and other custom schemes, exact `#+LINK` targets, `%s`, and `%h` mapping continue to operate on unencoded routing values.
- Serialization boundary: every target-derived `href`, `src`, `alt`, and `title` value emitted for a `RegularLink` is encoded through one shared attribute serializer; the AST and route decision are not mutated.
- Affected branches are regular anchors, links without descriptions, image and video elements (including media descriptions), file links, fragments, relative links, pretty relative links, custom protocol mappings, and mailto links.
- The old fixtures covered safe conventional URLs and descriptive text but did not combine quote, ampersand, angle-bracket, Unicode, pre-escaped, empty, or nested markup targets with tokenizer-level attribute assertions.
- The regression suite also includes deterministic property checks and automated source mutations for missing attribute escaping, pre-routing escaping, whole-tag escaping, and double-escaped ampersands.
