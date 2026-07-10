// Package llmxml builds line-structured, XML-like markup addressed to a model
// reading it. It is a presentation format, not a serialization: single-pass
// string construction with no parser, no schema, no namespaces, and no
// indentation. Attribute values are quoted via strconv.Quote; body text is
// emitted verbatim.
package llmxml

import (
	"fmt"
	"strconv"
	"strings"
)

// Scalar constrains attribute values to types with unambiguous string representation.
type Scalar interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64 |
		~string | ~bool
}

// Attribute is a rendered key="value" pair of an element's opening tag.
// Construct via Attr.
type Attribute struct {
	key   string
	value string
}

func (a Attribute) writeTo(b *strings.Builder) {
	b.WriteString(a.key)
	b.WriteByte('=')
	b.WriteString(strconv.Quote(a.value))
}

// String renders the attribute as key="value".
func (a Attribute) String() string {
	var b strings.Builder

	a.writeTo(&b)

	return b.String()
}

// Attr constructs an attribute from any scalar value.
// The value is formatted via fmt.Sprint and quoted with strconv.Quote on render.
func Attr[T Scalar](key string, value T) Attribute {
	return Attribute{key: key, value: fmt.Sprint(value)}
}

// NewElement creates a mutable element builder.
// Attributes and body content can be added after creation.
// String renders <tag attrs/> when empty, <tag attrs>body</tag> otherwise.
func NewElement(tag string, attrs ...Attribute) *Element {
	return &Element{tag: tag, attrs: attrs}
}

// Element accumulates attributes and body content, rendering either
// a self-closing <tag .../> or an open/close <tag ...>body</tag> pair.
type Element struct {
	tag    string
	attrs  []Attribute
	text   string `exhaustruct:"optional"`
	sealed bool   `exhaustruct:"optional"`
}

// Attr appends an attribute to the opening tag.
func (e *Element) Attr(a Attribute) *Element {
	e.attrs = append(e.attrs, a)

	return e
}

// InlineText sets the element's text content inline (no wrapping newlines).
// Panics if content was already set.
func (e *Element) InlineText(s string) *Element {
	if e.sealed {
		panic("llmxml: content already set")
	}

	e.sealed = true
	e.text = s

	return e
}

// WrapText sets the element's text content wrapped with newlines: \n before and after.
// Panics if content was already set.
func (e *Element) WrapText(s string) *Element {
	if e.sealed {
		panic("llmxml: content already set")
	}

	e.sealed = true
	e.text = "\n" + s + "\n"

	return e
}

func (e *Element) writeTo(b *strings.Builder) {
	b.WriteByte('<')
	b.WriteString(e.tag)

	for _, a := range e.attrs {
		b.WriteByte(' ')
		a.writeTo(b)
	}

	if !e.sealed {
		b.WriteString("/>")

		return
	}

	b.WriteByte('>')
	b.WriteString(e.text)
	b.WriteString("</")
	b.WriteString(e.tag)
	b.WriteByte('>')
}

// String renders the element. No body → <tag attrs/>.
// With body → <tag attrs>text</tag> or <tag attrs>\nchildren\n</tag>.
func (e *Element) String() string {
	var b strings.Builder

	e.writeTo(&b)

	return b.String()
}
