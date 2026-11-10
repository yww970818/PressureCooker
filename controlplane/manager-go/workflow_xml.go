package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strings"
)

// RawToken keeps lexical namespace prefixes. The ordered content preserves
// unknown properties, nested elements, comments, attributes and mixed text.
type xmlPart struct {
	Node               *xmlElement
	Kind, Text, Target string
}
type xmlElement struct {
	Name  xml.Name
	Attrs []xml.Attr
	Parts []xmlPart
}

func xmlName(name xml.Name) string {
	if name.Space != "" {
		return name.Space + ":" + name.Local
	}
	return name.Local
}
func parseXMLFragment(raw string) ([]*xmlElement, error) {
	if len(raw) > workflowByteLimit() || unsafeXML.MatchString(raw) {
		return nil, api(400, "unsafe_xml", "XML禁止DOCTYPE、ENTITY或超过工作流容量配置的内容")
	}
	decoder := xml.NewDecoder(strings.NewReader(raw))
	roots := []*xmlElement{}
	stack := []*xmlElement{}
	tokens := 0
	for {
		token, e := decoder.RawToken()
		if e == io.EOF {
			break
		}
		if e != nil {
			return nil, api(400, "invalid_xml", "XML无法解析")
		}
		tokens++
		if tokens > 250000 || len(stack) > 256 {
			return nil, api(400, "invalid_xml", "XML结构过于复杂")
		}
		switch v := token.(type) {
		case xml.StartElement:
			node := &xmlElement{Name: v.Name, Attrs: append([]xml.Attr(nil), v.Attr...)}
			if len(stack) == 0 {
				roots = append(roots, node)
			} else {
				parent := stack[len(stack)-1]
				parent.Parts = append(parent.Parts, xmlPart{Node: node})
			}
			stack = append(stack, node)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].Name != v.Name {
				return nil, api(400, "invalid_xml", "XML开始和结束标签不匹配")
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Parts = append(parent.Parts, xmlPart{Kind: "text", Text: string(v)})
			} else if strings.TrimSpace(string(v)) != "" {
				return nil, api(400, "invalid_xml", "XML根元素之外不能包含文本")
			}
		case xml.Comment:
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Parts = append(parent.Parts, xmlPart{Kind: "comment", Text: string(v)})
			}
		case xml.ProcInst:
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Parts = append(parent.Parts, xmlPart{Kind: "pi", Text: string(v.Inst), Target: v.Target})
			}
		case xml.Directive:
			return nil, api(400, "unsafe_xml", "XML不允许文档类型指令")
		}
	}
	if len(stack) != 0 || len(roots) == 0 {
		return nil, api(400, "invalid_xml", "XML结构不完整")
	}
	return roots, nil
}
func (e *xmlElement) attr(name string) string {
	for _, a := range e.Attrs {
		if xmlName(a.Name) == name {
			return a.Value
		}
	}
	return ""
}
func (e *xmlElement) setAttr(name, value string) {
	for i, a := range e.Attrs {
		if xmlName(a.Name) == name {
			e.Attrs[i].Value = value
			return
		}
	}
	e.Attrs = append(e.Attrs, xml.Attr{Name: xml.Name{Local: name}, Value: value})
}
func (e *xmlElement) children() []*xmlElement {
	items := []*xmlElement{}
	for _, part := range e.Parts {
		if part.Node != nil {
			items = append(items, part.Node)
		}
	}
	return items
}
func (e *xmlElement) text() string {
	var b strings.Builder
	for _, part := range e.Parts {
		if part.Kind == "text" {
			b.WriteString(part.Text)
		}
	}
	return b.String()
}
func (e *xmlElement) setText(value string) { e.Parts = []xmlPart{{Kind: "text", Text: value}} }

// JMeter serializes object/double properties with a nested name element.
func (e *xmlElement) propertyName() string {
	if name := e.attr("name"); name != "" {
		return name
	}
	if contains([]string{"doubleProp", "FloatProperty", "objProp"}, e.Name.Local) {
		for _, child := range e.children() {
			if child.Name.Local == "name" {
				return child.text()
			}
		}
	}
	return ""
}
func (e *xmlElement) propertyText() string {
	if contains([]string{"doubleProp", "FloatProperty"}, e.Name.Local) {
		for _, child := range e.children() {
			if child.Name.Local == "value" {
				return child.text()
			}
		}
	}
	return e.text()
}
func (e *xmlElement) prop(name string) *xmlElement {
	for _, child := range e.children() {
		if child.propertyName() == name {
			return child
		}
	}
	return nil
}
func (e *xmlElement) propText(name string) string {
	if p := e.prop(name); p != nil {
		return p.propertyText()
	}
	return ""
}
func (e *xmlElement) setProp(tag, name, value string) {
	if child := e.prop(name); child != nil {
		if child.Name.Local == "doubleProp" && tag == "doubleProp" && len(child.children()) > 0 {
			for _, entry := range child.children() {
				if entry.Name.Local == "value" {
					entry.setText(value)
					return
				}
			}
		}
		child.Name = xml.Name{Local: tag}
		child.setText(value)
		return
	}
	e.Parts = append(e.Parts, xmlPart{Node: property(tag, name, value)})
}
func (e *xmlElement) removeProp(name string) {
	parts := e.Parts[:0]
	for _, p := range e.Parts {
		if p.Node == nil || p.Node.propertyName() != name {
			parts = append(parts, p)
		}
	}
	e.Parts = parts
}
func (e *xmlElement) append(child *xmlElement) { e.Parts = append(e.Parts, xmlPart{Node: child}) }
func (e *xmlElement) copy() *xmlElement {
	out := &xmlElement{Name: e.Name, Attrs: append([]xml.Attr(nil), e.Attrs...)}
	for _, part := range e.Parts {
		p := part
		if part.Node != nil {
			p.Node = part.Node.copy()
		}
		out.Parts = append(out.Parts, p)
	}
	return out
}

func workflowRootTemplate(wrapper, top *xmlElement) *xmlElement {
	out := &xmlElement{Name: wrapper.Name, Attrs: append([]xml.Attr(nil), wrapper.Attrs...)}
	for _, part := range wrapper.Parts {
		p := part
		if part.Node == top {
			p.Node = &xmlElement{Name: top.Name, Attrs: append([]xml.Attr(nil), top.Attrs...)}
			for _, entry := range top.Parts {
				if entry.Node != nil {
					// replaceTreePairs will replace these two placeholders. Keep
					// their positions among comments and whitespace verbatim.
					entry.Node = &xmlElement{}
				}
				p.Node.Parts = append(p.Node.Parts, entry)
			}
		} else if part.Node != nil {
			p.Node = part.Node.copy()
		}
		out.Parts = append(out.Parts, p)
	}
	return out
}

// Import retains its request-local XML tree across metadata restoration passes.
// No tree is shared across requests or persisted; raw strings stay authoritative.
func importedNodeXML(node Obj, templates map[string][]*xmlElement) ([]*xmlElement, error) {
	if roots, ok := templates[str(node["id"])]; ok {
		return roots, nil
	}
	return parseXMLFragment(str(obj(node["properties"])["raw_xml"]))
}

// Generated XML no longer needs a second parser/tree allocation. Retain the
// parser's structure limits for the assembled tree, including opaque subtrees.
func validateGeneratedXMLStructure(root *xmlElement) error {
	tokens := 3 // XML declaration and surrounding newlines
	var visit func(*xmlElement, int) error
	visit = func(node *xmlElement, depth int) error {
		if depth > 256 {
			return api(400, "invalid_xml", "XML结构过于复杂")
		}
		tokens += 2 // start and end
		text := false
		for _, part := range node.Parts {
			if part.Node != nil {
				text = false
				if err := visit(part.Node, depth+1); err != nil {
					return err
				}
			} else if part.Kind == "comment" || part.Kind == "pi" {
				text = false
				tokens++
			} else if part.Text != "" && !text {
				text = true
				tokens++
			}
		}
		if tokens > 250000 {
			return api(400, "invalid_xml", "XML结构过于复杂")
		}
		return nil
	}
	return visit(root, 1)
}
func element(tag string, attrs ...string) *xmlElement {
	e := &xmlElement{Name: xml.Name{Local: tag}}
	for i := 0; i+1 < len(attrs); i += 2 {
		e.setAttr(attrs[i], attrs[i+1])
	}
	return e
}
func property(tag, name, value string) *xmlElement {
	e := element(tag, "name", name)
	e.setText(value)
	return e
}
func writeElement(b *bytes.Buffer, e *xmlElement) {
	b.WriteByte('<')
	b.WriteString(xmlName(e.Name))
	for _, a := range e.Attrs {
		b.WriteByte(' ')
		b.WriteString(xmlName(a.Name))
		b.WriteString(`="`)
		xml.EscapeText(b, []byte(a.Value))
		b.WriteByte('"')
	}
	b.WriteByte('>')
	for _, part := range e.Parts {
		switch {
		case part.Node != nil:
			writeElement(b, part.Node)
		case part.Kind == "comment":
			b.WriteString("<!--" + part.Text + "-->")
		case part.Kind == "pi":
			b.WriteString("<?" + part.Target + " " + part.Text + "?>")
		default:
			xml.EscapeText(b, []byte(part.Text))
		}
	}
	b.WriteString("</" + xmlName(e.Name) + ">")
}
func elementXML(e *xmlElement) string { var b bytes.Buffer; writeElement(&b, e); return b.String() }
func fragmentXML(elements ...*xmlElement) string {
	var b bytes.Buffer
	for _, e := range elements {
		writeElement(&b, e)
	}
	return b.String()
}
func pairedComponents(tree *xmlElement) ([][2]*xmlElement, error) {
	if tree.Name.Local != "hashTree" {
		return nil, api(400, "invalid_jmx", "组件必须与hashTree配对")
	}
	children := tree.children()
	if len(children)%2 != 0 {
		return nil, api(400, "invalid_jmx", "JMX组件缺少配对hashTree")
	}
	pairs := [][2]*xmlElement{}
	for i := 0; i < len(children); i += 2 {
		if children[i].Name.Local == "hashTree" || children[i+1].Name.Local != "hashTree" {
			return nil, api(400, "invalid_jmx", "JMX组件与hashTree顺序不正确")
		}
		pairs = append(pairs, [2]*xmlElement{children[i], children[i+1]})
	}
	return pairs, nil
}
func testPlanTemplate(raw string) (*xmlElement, *xmlElement, error) {
	roots, e := parseXMLFragment(raw)
	if e != nil {
		return nil, nil, e
	}
	if len(roots) != 1 {
		return nil, nil, api(400, "invalid_jmx", "测试计划只能有一个根元素")
	}
	root := roots[0]
	if root.Name.Local == "TestPlan" {
		return root, nil, nil
	}
	if root.Name.Local != "jmeterTestPlan" {
		return nil, nil, api(400, "invalid_jmx", "测试计划XML根元素无效")
	}
	var top *xmlElement
	for _, child := range root.children() {
		if child.Name.Local == "hashTree" {
			top = child
			break
		}
	}
	if top == nil {
		return nil, nil, api(400, "invalid_jmx", "JMX缺少hashTree")
	}
	pairs, e := pairedComponents(top)
	if e != nil || len(pairs) != 1 || pairs[0][0].Name.Local != "TestPlan" {
		return nil, nil, api(400, "invalid_jmx", "JMX必须包含唯一TestPlan")
	}
	return pairs[0][0], root, nil
}
func boolXML(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
func xmlFailure(component string) error {
	return api(400, "invalid_component", fmt.Sprintf("组件%s的原始XML与类型不一致", component))
}

func validXMLText(value string) bool {
	for _, r := range value {
		if !(r == 9 || r == 10 || r == 13 || (r >= 0x20 && r <= 0xd7ff) || (r >= 0xe000 && r <= 0xfffd) || (r >= 0x10000 && r <= 0x10ffff)) {
			return false
		}
	}
	return true
}
