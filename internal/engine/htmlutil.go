package engine

import (
	"bytes"
	"strings"

	"golang.org/x/net/html"
)

// ParseDoc 解析 HTML 文档并返回根节点。
func ParseDoc(body []byte) (*html.Node, error) {
	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return doc, nil
}

// Text 递归提取节点的纯文本，并归一化空白。
func Text(n *html.Node) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.TextNode {
			b.WriteString(node.Data)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return strings.Join(strings.Fields(b.String()), " ")
}

// Attr 返回节点指定属性值，不存在时返回空串。
func Attr(n *html.Node, key string) string {
	if n == nil {
		return ""
	}
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// HasClass 判断节点是否带有指定 class。
func HasClass(n *html.Node, class string) bool {
	if n == nil {
		return false
	}
	for _, c := range strings.Fields(Attr(n, "class")) {
		if c == class {
			return true
		}
	}
	return false
}

// FindAll 深度遍历收集所有满足条件的元素节点。
func FindAll(n *html.Node, pred func(*html.Node) bool) []*html.Node {
	var out []*html.Node
	if n == nil {
		return out
	}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode && pred(node) {
			out = append(out, node)
		}
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return out
}

// FindByClass 收集所有带指定 class 的元素。
func FindByClass(n *html.Node, class string) []*html.Node {
	return FindAll(n, func(x *html.Node) bool { return HasClass(x, class) })
}

// FindByTag 收集所有指定标签名的元素。
func FindByTag(n *html.Node, tag string) []*html.Node {
	return FindAll(n, func(x *html.Node) bool { return x.Data == tag })
}

// FindFirst 返回第一个满足条件的元素。
func FindFirst(n *html.Node, pred func(*html.Node) bool) *html.Node {
	all := FindAll(n, pred)
	if len(all) == 0 {
		return nil
	}
	return all[0]
}

// FindByAttrPrefix 收集属性值以指定前缀开头的元素，常用于匹配 magnet 链接。
func FindByAttrPrefix(n *html.Node, attr, prefix string) []*html.Node {
	return FindAll(n, func(x *html.Node) bool {
		return strings.HasPrefix(strings.ToLower(strings.TrimSpace(Attr(x, attr))), strings.ToLower(prefix))
	})
}

// FindByAttr 收集指定属性值完全匹配的元素。
func FindByAttr(n *html.Node, attr, value string) []*html.Node {
	return FindAll(n, func(x *html.Node) bool { return Attr(x, attr) == value })
}

// ElementText 返回指定标签下第一个匹配元素的文本。
func ElementText(n *html.Node, tag string) string {
	el := FindFirst(n, func(x *html.Node) bool { return x.Data == tag })
	return Text(el)
}

// ClassText 返回第一个带指定 class 的元素的文本。
func ClassText(n *html.Node, class string) string {
	return Text(FindFirst(n, func(x *html.Node) bool { return HasClass(x, class) }))
}
