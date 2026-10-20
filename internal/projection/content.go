package projection

import (
	"bytes"
	"encoding/base64"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"golang.org/x/net/html"
)

type ContentType int

const (
	ContentPlain ContentType = iota
	ContentHTML
	ContentMarkdown
)

func DetectContentType(s string) ContentType {
	trimmed := strings.TrimSpace(s)
	if htmlTagPattern.MatchString(trimmed) {
		return ContentHTML
	}
	if markdownPattern.MatchString(trimmed) {
		return ContentMarkdown
	}
	return ContentPlain
}

var (
	htmlTagPattern  = regexp.MustCompile(`(?i)^<[a-z][a-z0-9]*[\s>/]`)
	markdownPattern = regexp.MustCompile(`(?m)^#{1,6} |^\* |\*\*|__|\[.+\]\(.+\)|` + "```" + `|^- \[[ x]\]|^\|`)
)

// StripMarkdown converts Markdown to plain text via goldmark (Markdown→HTML)
// then strips the HTML. This correctly handles all CommonMark constructs:
// tables, images, code blocks, footnotes, HTML embedded in Markdown, etc.
var mdParser = goldmark.New(
	goldmark.WithExtensions(extension.Table, extension.TaskList),
)

func StripMarkdown(s string) string {
	var buf bytes.Buffer
	if err := mdParser.Convert([]byte(s), &buf); err != nil {
		return s
	}
	return StripHTML(buf.String())
}

// skipTags are HTML elements whose entire subtree (including text) should be dropped.
var skipTags = map[string]bool{
	"script": true,
	"style":  true,
	"head":   true,
}

// blockTags are HTML elements that should produce a newline before their content.
var blockTags = map[string]bool{
	"p": true, "div": true, "br": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"li": true, "tr": true, "td": true, "th": true,
	"blockquote": true, "pre": true, "hr": true,
}

// StripHTML removes HTML markup and returns plain text.
// - <script>, <style>, <head> subtrees are dropped entirely
// - <img> emits its alt attribute text
// - block elements produce newlines
// - HTML entities are decoded by the tokenizer
// - base64-encoded data URIs are stripped
func StripHTML(s string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(s))
	var sb strings.Builder
	skipDepth := 0
	for {
		tt := tokenizer.Next()
		if tt == html.ErrorToken {
			return normalizeWhitespace(sb.String())
		}
		skipDepth = processHTMLToken(htmlTokenParams{
			tokenizer: tokenizer, builder: &sb, tokenType: tt, skipDepth: skipDepth,
		})
	}
}

type htmlTokenParams struct {
	tokenizer *html.Tokenizer
	builder   *strings.Builder
	tokenType html.TokenType
	skipDepth int
}

func processHTMLToken(p htmlTokenParams) int {
	switch p.tokenType {
	case html.TextToken:
		if p.skipDepth == 0 {
			p.builder.Write(p.tokenizer.Text())
		}
	case html.StartTagToken, html.SelfClosingTagToken:
		return processStartTag(p)
	case html.EndTagToken:
		if p.skipDepth > 0 {
			return p.skipDepth - 1
		}
	case html.ErrorToken:
		// EOF and tokenizer errors terminate the outer loop before dispatch.
		return p.skipDepth
	case html.CommentToken, html.DoctypeToken:
		// Neither carries visible text nor changes element nesting.
		return p.skipDepth
	}
	return p.skipDepth
}

func processStartTag(p htmlTokenParams) int {
	rawName, hasAttr := p.tokenizer.TagName()
	name := string(rawName)
	isSelfClose := p.tokenType == html.SelfClosingTagToken
	if nextDepth, handled := nextSkipDepth(name, isSelfClose, p.skipDepth); handled {
		return nextDepth
	}
	if blockTags[name] {
		p.builder.WriteByte('\n')
	}
	if name == "img" && hasAttr {
		writeImgAlt(p.tokenizer, p.builder)
	}
	return 0
}

func nextSkipDepth(name string, isSelfClose bool, skipDepth int) (int, bool) {
	if skipDepth > 0 {
		if isSelfClose {
			return skipDepth, true
		}
		return skipDepth + 1, true
	}
	if !skipTags[name] {
		return 0, false
	}
	if isSelfClose {
		return 0, true
	}
	return 1, true
}

func writeImgAlt(tokenizer *html.Tokenizer, sb *strings.Builder) {
	for {
		k, v, more := tokenizer.TagAttr()
		if string(k) == "alt" && len(v) > 0 {
			sb.Write(v)
		}
		if !more {
			break
		}
	}
}

func StripMarkup(s string) string {
	s = stripBase64DataURIs(s)
	switch DetectContentType(s) {
	case ContentHTML:
		return StripHTML(s)
	case ContentMarkdown:
		return StripMarkdown(s)
	case ContentPlain:
		return s
	}
	return s
}

// stripBase64DataURIs removes embedded base64 data URIs (common in HTML emails
// and rich PR bodies — a single embedded image can be hundreds of KB).
var base64URIPattern = regexp.MustCompile(`data:[a-zA-Z0-9/+]+;base64,[A-Za-z0-9+/]+=*`)

func stripBase64DataURIs(s string) string {
	return base64URIPattern.ReplaceAllStringFunc(s, func(match string) string {
		// Decode enough to confirm it's binary data, not accidentally matching text.
		parts := strings.SplitN(match, ",", 2)
		if len(parts) < 2 {
			return match
		}
		if _, err := base64.StdEncoding.DecodeString(parts[1][:min(64, len(parts[1]))]); err != nil {
			return match
		}
		return "[media]"
	})
}

func normalizeWhitespace(s string) string {
	s = multiSpacePattern.ReplaceAllString(s, " ")
	s = multiNewlinePattern.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}

var (
	multiSpacePattern   = regexp.MustCompile(`[^\S\n]+`)
	multiNewlinePattern = regexp.MustCompile(`\n{3,}`)
)
