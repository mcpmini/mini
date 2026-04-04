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
	htmlTagPattern = regexp.MustCompile(`(?i)^<[a-z][a-z0-9]*[\s>/]`)
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
		skipDepth = processHTMLToken(tokenizer, &sb, tt, skipDepth)
	}
}

func processHTMLToken(tokenizer *html.Tokenizer, sb *strings.Builder, tt html.TokenType, skipDepth int) int {
	switch tt {
	case html.TextToken:
		if skipDepth == 0 {
			sb.Write(tokenizer.Text())
		}
	case html.StartTagToken, html.SelfClosingTagToken:
		return processStartTag(tokenizer, sb, tt, skipDepth)
	case html.EndTagToken:
		if skipDepth > 0 {
			return skipDepth - 1
		}
	}
	return skipDepth
}

func processStartTag(tokenizer *html.Tokenizer, sb *strings.Builder, tt html.TokenType, skipDepth int) int {
	rawName, hasAttr := tokenizer.TagName()
	name := string(rawName)
	isSelfClose := tt == html.SelfClosingTagToken
	if skipDepth == 0 && skipTags[name] {
		if !isSelfClose {
			return 1
		}
		return 0
	}
	if skipDepth > 0 {
		if !isSelfClose {
			return skipDepth + 1
		}
		return skipDepth
	}
	if blockTags[name] {
		sb.WriteByte('\n')
	}
	if name == "img" && hasAttr {
		writeImgAlt(tokenizer, sb)
	}
	return 0
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
	default:
		return s
	}
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
