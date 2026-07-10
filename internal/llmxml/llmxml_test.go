package llmxml_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"dev.gaijin.team/go/go-gerrit-mcp/internal/llmxml"
)

func Test_AttrString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		give string
		want string
	}{
		{name: "simple", give: "hello", want: `name="hello"`},
		{name: "with spaces", give: "hello world", want: `name="hello world"`},
		{name: "with quotes", give: `say "hi"`, want: `name="say \"hi\""`},
		{name: "with newline", give: "line1\nline2", want: `name="line1\nline2"`},
		{name: "empty", give: "", want: `name=""`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := llmxml.Attr("name", tt.give).String()
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_AttrScalarTypes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, `count="42"`, llmxml.Attr("count", 42).String())
	assert.Equal(t, `draft="true"`, llmxml.Attr("draft", true).String())
	assert.Equal(t, `draft="false"`, llmxml.Attr("draft", false).String())
	assert.Equal(t, `score="-1"`, llmxml.Attr("score", -1).String())
	assert.Equal(t, `ratio="3.14"`, llmxml.Attr("ratio", 3.14).String())
}

func Test_Element(t *testing.T) {
	t.Parallel()

	t.Run("self-closing no attrs", func(t *testing.T) {
		t.Parallel()

		got := llmxml.NewElement("br").String()
		assert.Equal(t, "<br/>", got)
	})

	t.Run("self-closing with attrs", func(t *testing.T) {
		t.Parallel()

		got := llmxml.NewElement("label",
			llmxml.Attr("name", "Code-Review"),
			llmxml.Attr("value", "+1"),
		).String()
		assert.Equal(t, `<label name="Code-Review" value="+1"/>`, got)
	})

	t.Run("inline text", func(t *testing.T) {
		t.Parallel()

		got := llmxml.NewElement("concern").InlineText("null pointer dereference").String()
		assert.Equal(t, "<concern>null pointer dereference</concern>", got)
	})

	t.Run("wrapped single child", func(t *testing.T) {
		t.Parallel()

		inner := llmxml.NewElement("verdict", llmxml.Attr("status", "pass")).String()

		got := llmxml.NewElement("finding", llmxml.Attr("id", "F1")).WrapText(inner).String()
		assert.Equal(t, "<finding id=\"F1\">\n<verdict status=\"pass\"/>\n</finding>", got)
	})

	t.Run("wrapped multiple children", func(t *testing.T) {
		t.Parallel()

		concern := llmxml.NewElement("concern").InlineText("race condition").String()
		verdict := llmxml.NewElement("verdict", llmxml.Attr("status", "drop")).
			InlineText("\ninsufficient evidence").String()

		got := llmxml.NewElement("finding",
			llmxml.Attr("id", "F2"),
			llmxml.Attr("kind", "bug"),
		).WrapText(concern + "\n" + verdict).String()

		want := `<finding id="F2" kind="bug">` +
			"\n<concern>race condition</concern>" +
			"\n<verdict status=\"drop\">\ninsufficient evidence</verdict>" +
			"\n</finding>"
		assert.Equal(t, want, got)
	})

	t.Run("conditional attr after creation", func(t *testing.T) {
		t.Parallel()

		e := llmxml.NewElement("change", llmxml.Attr("author", "alice"))
		e.Attr(llmxml.Attr("draft", true))

		got := e.String()
		assert.Equal(t, `<change author="alice" draft="true"/>`, got)
	})

	t.Run("panics on double InlineText", func(t *testing.T) {
		t.Parallel()

		assert.Panics(t, func() {
			llmxml.NewElement("x").InlineText("a").InlineText("b")
		})
	})

	t.Run("panics on double WrapText", func(t *testing.T) {
		t.Parallel()

		assert.Panics(t, func() {
			llmxml.NewElement("x").WrapText("a").WrapText("b")
		})
	})

	t.Run("panics on WrapText after InlineText", func(t *testing.T) {
		t.Parallel()

		assert.Panics(t, func() {
			llmxml.NewElement("x").InlineText("a").WrapText("b")
		})
	})

	t.Run("panics on InlineText after WrapText", func(t *testing.T) {
		t.Parallel()

		assert.Panics(t, func() {
			llmxml.NewElement("x").WrapText("a").InlineText("b")
		})
	})

	t.Run("wrapped empty element", func(t *testing.T) {
		t.Parallel()

		got := llmxml.NewElement("parent").
			WrapText(llmxml.NewElement("empty").String()).
			String()
		assert.Equal(t, "<parent>\n<empty/>\n</parent>", got)
	})
}
