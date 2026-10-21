// Package bencode 实现 BitTorrent 系协议使用的最小 bencode 编解码器。
//
// 只覆盖协议实际用到的四种类型：整数（i...e）、字节串（<长度>:<字节>）、
// 列表（l...e）、字典（d...e）。解码器对任何非法输入都返回错误，绝不 panic，
// 因为它面对的是完全不可信的公网报文。
package bencode

// Kind 是 bencode 值的类型。
type Kind int

const (
	// KindInt 是整数值，例如 i42e。
	KindInt Kind = iota
	// KindBytes 是字节串，例如 4:spam。
	KindBytes
	// KindList 是列表，例如 l4:spami3ee。
	KindList
	// KindDict 是字典，例如 d4:spami42ee。
	KindDict
)

// maxDepth 限制嵌套深度，防止畸形报文把栈压爆。
const maxDepth = 32

// Value 是一个 bencode 值。
type Value struct {
	kind Kind
	num  int64
	raw  []byte
	list []Value
	dict map[string]Value
}

// NewInt 创建整数值。
func NewInt(n int64) Value {
	return Value{kind: KindInt, num: n}
}

// NewBytes 创建字节串值，底层切片不会被复制。
func NewBytes(b []byte) Value {
	return Value{kind: KindBytes, raw: b}
}

// NewString 创建字符串形式的字节串值。
func NewString(s string) Value {
	return Value{kind: KindBytes, raw: []byte(s)}
}

// NewList 创建列表值。
func NewList(items []Value) Value {
	return Value{kind: KindList, list: items}
}

// NewDict 创建字典值。
func NewDict(m map[string]Value) Value {
	return Value{kind: KindDict, dict: m}
}

// Kind 返回值的类型。
func (v Value) Kind() Kind {
	return v.kind
}

// Int 返回整数值；类型不符时 ok 为 false。
func (v Value) Int() (int64, bool) {
	if v.kind != KindInt {
		return 0, false
	}
	return v.num, true
}

// Bytes 返回字节串内容；类型不符时 ok 为 false。
func (v Value) Bytes() ([]byte, bool) {
	if v.kind != KindBytes {
		return nil, false
	}
	return v.raw, true
}

// List 返回列表元素；类型不符时 ok 为 false。
func (v Value) List() ([]Value, bool) {
	if v.kind != KindList {
		return nil, false
	}
	return v.list, true
}

// Dict 返回字典内容；类型不符时 ok 为 false。
func (v Value) Dict() (map[string]Value, bool) {
	if v.kind != KindDict {
		return nil, false
	}
	return v.dict, true
}

// Int 读取字典中的整数字段；字段缺失或类型不符时 ok 为 false。
func Int(dict Value, key string) (int64, bool) {
	v, ok := Dict(dict, key)
	if !ok {
		return 0, false
	}
	return v.Int()
}

// Bytes 读取字典中的字节串字段；字段缺失或类型不符时 ok 为 false。
func Bytes(dict Value, key string) ([]byte, bool) {
	v, ok := Dict(dict, key)
	if !ok {
		return nil, false
	}
	return v.Bytes()
}

// String 读取字典中的字符串字段；字段缺失或类型不符时 ok 为 false。
func String(dict Value, key string) (string, bool) {
	b, ok := Bytes(dict, key)
	if !ok {
		return "", false
	}
	return string(b), true
}

// List 读取字典中的列表字段；字段缺失或类型不符时 ok 为 false。
func List(dict Value, key string) ([]Value, bool) {
	v, ok := Dict(dict, key)
	if !ok {
		return nil, false
	}
	return v.List()
}

// Dict 读取字典中的子字典字段；字段缺失或类型不符时 ok 为 false。
func Dict(dict Value, key string) (Value, bool) {
	if dict.kind != KindDict {
		return Value{}, false
	}
	v, ok := dict.dict[key]
	return v, ok
}

// DictBytes 按原始字节键读取子字典，用于 info hash 这类二进制键。
// 字段缺失或类型不符时 ok 为 false。
func DictBytes(dict Value, key []byte) (Value, bool) {
	if dict.kind != KindDict {
		return Value{}, false
	}
	v, ok := dict.dict[string(key)]
	return v, ok
}
