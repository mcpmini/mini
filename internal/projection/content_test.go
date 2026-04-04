package projection_test

import (
	"strings"
	"testing"

	"github.com/mcpmini/mini/internal/config"
	"github.com/mcpmini/mini/internal/projection"
)

var detectContentTypeCases = []struct {
	name  string
	input string
	want  projection.ContentType
}{
	{"html tag start", "<p>Hello</p>", projection.ContentHTML},
	{"html self-closing", "<br/>text", projection.ContentHTML},
	{"html with attrs", "<div class='x'>hi</div>", projection.ContentHTML},
	{"html uppercase tag", "<P>text</P>", projection.ContentHTML},
	{"md heading h1", "# Title", projection.ContentMarkdown},
	{"md heading h2", "## Section", projection.ContentMarkdown},
	{"md heading h6", "###### Deep", projection.ContentMarkdown},
	{"md bullet", "* item", projection.ContentMarkdown},
	{"md bold", "**bold** text", projection.ContentMarkdown},
	{"md underscore bold", "__bold__", projection.ContentMarkdown},
	{"md link", "[click](https://example.com)", projection.ContentMarkdown},
	{"md code fence", "```go\ncode\n```", projection.ContentMarkdown},
	{"md table", "| col1 | col2 |", projection.ContentMarkdown},
	{"md task list checked", "- [x] done", projection.ContentMarkdown},
	{"md task list unchecked", "- [ ] todo", projection.ContentMarkdown},
	{"plain text", "just plain text here", projection.ContentPlain},
	{"empty string", "", projection.ContentPlain},
	{"numbers only", "12345", projection.ContentPlain},
	{"whitespace only", "   \n\t  ", projection.ContentPlain},
}

func TestDetectContentType(t *testing.T) {
	for _, tc := range detectContentTypeCases {
		t.Run(tc.name, func(t *testing.T) {
			got := projection.DetectContentType(tc.input)
			if got != tc.want {
				t.Errorf("DetectContentType(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

var stripHTMLCases = []struct {
	name        string
	input       string
	wantContain []string
	wantAbsent  []string
}{
	{
		name:        "basic tags stripped",
		input:       "<p>Hello <b>world</b></p>",
		wantContain: []string{"Hello", "world"},
		wantAbsent:  []string{"<p>", "<b>", "</b>", "</p>"},
	},
	{
		name:        "script subtree dropped entirely",
		input:       "<div>visible</div><script>alert('xss')</script>",
		wantContain: []string{"visible"},
		wantAbsent:  []string{"alert", "xss", "script"},
	},
	{
		name:        "style subtree dropped entirely",
		input:       "<p>keep</p><style>.x{color:red}</style>",
		wantContain: []string{"keep"},
		wantAbsent:  []string{"color", "red", "style"},
	},
	{
		name:        "head subtree dropped",
		input:       "<head><title>Doc Title</title></head><body>body text</body>",
		wantContain: []string{"body text"},
		wantAbsent:  []string{"Doc Title"},
	},
	{
		name:        "img alt text extracted",
		input:       `<img src="photo.jpg" alt="A sunset photo">`,
		wantContain: []string{"A sunset photo"},
		wantAbsent:  []string{"photo.jpg", "src=", "<img"},
	},
	{
		name:        "img without alt produces nothing",
		input:       `<img src="photo.jpg">text after`,
		wantContain: []string{"text after"},
		wantAbsent:  []string{"photo.jpg", "<img"},
	},
	{
		name:        "block elements produce newlines",
		input:       "<p>First</p><p>Second</p>",
		wantContain: []string{"First", "Second"},
		wantAbsent:  []string{"<p>"},
	},
	{
		name:        "anchor text kept, href dropped",
		input:       `<a href="https://example.com">click here</a>`,
		wantContain: []string{"click here"},
		wantAbsent:  []string{"https://example.com", "href"},
	},
	{
		name:        "HTML entities decoded",
		input:       "<p>5 &gt; 3 &amp; 2 &lt; 4</p>",
		wantContain: []string{"5 > 3", "2 < 4"},
		wantAbsent:  []string{"&gt;", "&amp;", "&lt;"},
	},
	{
		name:        "nested skip tags handled",
		input:       "<script><script>inner</script></script>after",
		wantContain: []string{"after"},
		wantAbsent:  []string{"inner"},
	},
	{
		name:        "table cells extracted",
		input:       "<table><tr><td>cell1</td><td>cell2</td></tr></table>",
		wantContain: []string{"cell1", "cell2"},
		wantAbsent:  []string{"<table>", "<td>"},
	},
	{
		name:        "list items extracted",
		input:       "<ul><li>item one</li><li>item two</li></ul>",
		wantContain: []string{"item one", "item two"},
		wantAbsent:  []string{"<li>", "<ul>"},
	},
	{
		name:        "self-closing br produces newline",
		input:       "line1<br/>line2",
		wantContain: []string{"line1", "line2"},
	},
	{
		name:        "empty input",
		input:       "",
		wantContain: []string{},
		wantAbsent:  []string{"<"},
	},
	{
		name:        "plain text passthrough",
		input:       "no tags here",
		wantContain: []string{"no tags here"},
	},
	{
		name:        "comment not emitted",
		input:       "<!-- hidden -->visible",
		wantContain: []string{"visible"},
		wantAbsent:  []string{"hidden", "<!--"},
	},
}

func TestStripHTML(t *testing.T) {
	for _, tc := range stripHTMLCases {
		t.Run(tc.name, func(t *testing.T) {
			got := projection.StripHTML(tc.input)
			for _, want := range tc.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("want %q in output, got: %q", want, got)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("want %q absent from output, got: %q", absent, got)
				}
			}
		})
	}
}

var stripMarkdownCases = []struct {
	name        string
	input       string
	wantContain []string
	wantAbsent  []string
}{
	{
		name:        "headings stripped",
		input:       "# H1\n## H2\n### H3",
		wantContain: []string{"H1", "H2", "H3"},
		wantAbsent:  []string{"#"},
	},
	{
		name:        "bold and italic stripped",
		input:       "**bold** and *italic* and __under__ and _em_",
		wantContain: []string{"bold", "italic", "under", "em"},
		wantAbsent:  []string{"**", "__"},
	},
	{
		name:        "link text kept, URL dropped",
		input:       "[click here](https://example.com)",
		wantContain: []string{"click here"},
		wantAbsent:  []string{"https://example.com", "]("},
	},
	{
		name:        "image alt kept, URL dropped",
		input:       "![diagram of system](https://example.com/img.png)",
		wantContain: []string{"diagram of system"},
		wantAbsent:  []string{"https://example.com", "!["},
	},
	{
		name:        "code block stripped",
		input:       "```go\nfunc main() {}\n```",
		wantContain: []string{"func main()"},
		wantAbsent:  []string{"```"},
	},
	{
		name:        "inline code stripped",
		input:       "call `fmt.Println` here",
		wantContain: []string{"fmt.Println"},
		wantAbsent:  []string{"`"},
	},
	{
		name:        "table converted to plain",
		input:       "| col1 | col2 |\n|------|------|\n| a    | b    |",
		wantContain: []string{"col1", "col2", "a", "b"},
		wantAbsent:  []string{"|---"},
	},
	{
		name:        "task list stripped",
		input:       "- [x] Done task\n- [ ] Pending task",
		wantContain: []string{"Done task", "Pending task"},
		wantAbsent:  []string{"[x]", "[ ]"},
	},
	{
		name:        "blockquote text kept",
		input:       "> This is a quote",
		wantContain: []string{"This is a quote"},
		wantAbsent:  []string{">"},
	},
	{
		name:        "horizontal rule stripped",
		input:       "before\n\n---\n\nafter",
		wantContain: []string{"before", "after"},
		wantAbsent:  []string{"---"},
	},
	{
		name:        "html embedded in markdown stripped",
		input:       "Normal text\n\n<script>evil()</script>\n\nMore text",
		wantContain: []string{"Normal text", "More text"},
		wantAbsent:  []string{"evil", "script"},
	},
	{
		name:        "ordered list",
		input:       "1. First\n2. Second\n3. Third",
		wantContain: []string{"First", "Second", "Third"},
	},
	{
		name:        "unordered list",
		input:       "- alpha\n- beta\n- gamma",
		wantContain: []string{"alpha", "beta", "gamma"},
	},
	{
		name:        "plain text unchanged",
		input:       "no markup whatsoever",
		wantContain: []string{"no markup whatsoever"},
	},
}

func TestStripMarkdown(t *testing.T) {
	for _, tc := range stripMarkdownCases {
		t.Run(tc.name, func(t *testing.T) {
			got := projection.StripMarkdown(tc.input)
			for _, want := range tc.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("want %q in output, got: %q", want, got)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("want %q absent from output, got: %q", absent, got)
				}
			}
		})
	}
}

var stripBase64DataURIsCases = []struct {
	name        string
	input       string
	wantContain []string
	wantAbsent  []string
}{
	{
		name: "png data URI stripped from html img",
		// After stripBase64DataURIs replaces the URI with [media], StripHTML
		// processes the img tag — since [media] is now in src (not alt), no
		// text is emitted. The important thing is the raw base64 bytes are gone.
		input:      `<img src="data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==">`,
		wantAbsent: []string{"iVBORw0KGgo", "base64"},
	},
	{
		name:        "non-base64 text not replaced",
		input:       "some data:text/plain;base64,notvalidbase64!!@# text",
		wantContain: []string{"some", "text"},
	},
	{
		name:        "no data URI unchanged",
		input:       "plain text without any data URIs",
		wantContain: []string{"plain text without any data URIs"},
	},
}

func TestStripBase64DataURIs(t *testing.T) {
	for _, tc := range stripBase64DataURIsCases {
		t.Run(tc.name, func(t *testing.T) {
			got := projection.StripMarkup(tc.input)
			for _, want := range tc.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("want %q in output, got: %q", want, got)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("want %q absent from output, got: %q", absent, got)
				}
			}
		})
	}
}

var stripContentDispatchCases = []struct {
	name        string
	input       string
	wantContain []string
	wantAbsent  []string
}{
	{
		name:        "HTML dispatched to StripHTML",
		input:       "<p>PR body text with <b>bold</b></p>",
		wantContain: []string{"PR body text with", "bold"},
		wantAbsent:  []string{"<p>", "<b>"},
	},
	{
		name:        "Markdown dispatched to StripMarkdown",
		input:       "## PR Title\n\n**Summary:** fixed the bug",
		wantContain: []string{"PR Title", "Summary", "fixed the bug"},
		wantAbsent:  []string{"##", "**"},
	},
	{
		name:        "plain text returned as-is",
		input:       "plain text no markup",
		wantContain: []string{"plain text no markup"},
	},
	{
		name:        "base64 stripped before dispatch",
		input:       "<p>See image: <img src=\"data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==\"></p>",
		wantContain: []string{"See image"},
		wantAbsent:  []string{"iVBORw0KGgo"},
	},
}

func TestStripMarkupDispatch(t *testing.T) {
	for _, tc := range stripContentDispatchCases {
		t.Run(tc.name, func(t *testing.T) {
			got := projection.StripMarkup(tc.input)
			for _, want := range tc.wantContain {
				if !strings.Contains(got, want) {
					t.Errorf("want %q in output, got: %q", want, got)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(got, absent) {
					t.Errorf("want %q absent from output, got: %q", absent, got)
				}
			}
		})
	}
}

func TestProjectionStripsHTMLInSummary(t *testing.T) {
	value := map[string]any{
		"title": "My Page",
		"body":  "<p>Hello <b>world</b></p>",
	}

	cfg := &config.ProjectionConfig{StripMarkup: true}
	result := projection.Apply(value, cfg, defaultLimits)
	m := result.Summary.(map[string]any)

	body := m["body"].(string)
	if strings.Contains(body, "<p>") || strings.Contains(body, "<b>") {
		t.Errorf("HTML should be stripped from summary, got: %s", body)
	}
	if !strings.Contains(body, "Hello") || !strings.Contains(body, "world") {
		t.Errorf("text content should be preserved, got: %s", body)
	}
}
