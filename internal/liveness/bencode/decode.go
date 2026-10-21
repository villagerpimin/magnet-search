package bencode

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Decode 解析一段 bencode 数据。
// 输入非法时返回错误，绝不 panic。
func Decode(data []byte) (Value, error) {
	d := &decoder{data: data}
	v, err := d.value(0)
	if err != nil {
		return Value{}, err
	}
	if d.pos != len(d.data) {
		return Value{}, fmt.Errorf("bencode: 偏移 %d 处存在多余数据", d.pos)
	}
	return v, nil
}

// decoder 是 bencode 解析器，pos 记录当前读取位置。
type decoder struct {
	data []byte
	pos  int
}

// nextByte 读取一个字节，越界时返回错误。
func (d *decoder) nextByte() (byte, error) {
	if d.pos >= len(d.data) {
		return 0, errors.New("bencode: 数据意外结束")
	}
	b := d.data[d.pos]
	d.pos++
	return b, nil
}

// value 解析一个值。
func (d *decoder) value(depth int) (Value, error) {
	if depth > maxDepth {
		return Value{}, errors.New("bencode: 嵌套层级过深")
	}
	c, err := d.nextByte()
	if err != nil {
		return Value{}, err
	}
	switch {
	case c == 'i':
		return d.intValue()
	case c == 'l':
		return d.listValue(depth)
	case c == 'd':
		return d.dictValue(depth)
	case c >= '0' && c <= '9':
		d.pos--
		return d.bytesValue()
	default:
		return Value{}, fmt.Errorf("bencode: 偏移 %d 处出现非法类型标记 %q", d.pos-1, c)
	}
}

// intValue 解析 i...e 形式的整数。
func (d *decoder) intValue() (Value, error) {
	start := d.pos
	for {
		c, err := d.nextByte()
		if err != nil {
			return Value{}, err
		}
		if c == 'e' {
			break
		}
		if c < '0' || c > '9' {
			if !(c == '-' && d.pos == start+1) {
				return Value{}, fmt.Errorf("bencode: 偏移 %d 处整数含有非法字符 %q", d.pos-1, c)
			}
		}
	}
	raw := string(d.data[start : d.pos-1])
	digits := strings.TrimPrefix(raw, "-")
	if len(digits) > 1 && digits[0] == '0' {
		return Value{}, fmt.Errorf("bencode: 整数 %q 含前导零", raw)
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return Value{}, fmt.Errorf("bencode: 整数 %q 非法: %w", raw, err)
	}
	return Value{kind: KindInt, num: n}, nil
}

// bytesValue 解析 <长度>:<字节> 形式的字节串。
func (d *decoder) bytesValue() (Value, error) {
	start := d.pos
	for {
		c, err := d.nextByte()
		if err != nil {
			return Value{}, err
		}
		if c == ':' {
			break
		}
		if c < '0' || c > '9' {
			return Value{}, fmt.Errorf("bencode: 偏移 %d 处长度含有非法字符 %q", d.pos-1, c)
		}
	}
	lengthRaw := string(d.data[start : d.pos-1])
	length, err := strconv.Atoi(lengthRaw)
	if err != nil {
		return Value{}, fmt.Errorf("bencode: 长度 %q 非法: %w", lengthRaw, err)
	}
	if length < 0 || length > len(d.data)-d.pos {
		return Value{}, fmt.Errorf("bencode: 字节串长度 %d 超出剩余数据", length)
	}
	raw := make([]byte, length)
	copy(raw, d.data[d.pos:d.pos+length])
	d.pos += length
	return Value{kind: KindBytes, raw: raw}, nil
}

// listValue 解析 l...e 形式的列表。
func (d *decoder) listValue(depth int) (Value, error) {
	var items []Value
	for {
		c, err := d.nextByte()
		if err != nil {
			return Value{}, err
		}
		if c == 'e' {
			return Value{kind: KindList, list: items}, nil
		}
		d.pos--
		item, err := d.value(depth + 1)
		if err != nil {
			return Value{}, err
		}
		items = append(items, item)
	}
}

// dictValue 解析 d...e 形式的字典，键必须是字节串。
func (d *decoder) dictValue(depth int) (Value, error) {
	m := make(map[string]Value)
	for {
		c, err := d.nextByte()
		if err != nil {
			return Value{}, err
		}
		if c == 'e' {
			return Value{kind: KindDict, dict: m}, nil
		}
		if c < '0' || c > '9' {
			return Value{}, fmt.Errorf("bencode: 偏移 %d 处字典键非法 %q", d.pos-1, c)
		}
		d.pos--
		key, err := d.bytesValue()
		if err != nil {
			return Value{}, err
		}
		val, err := d.value(depth + 1)
		if err != nil {
			return Value{}, err
		}
		m[string(key.raw)] = val
	}
}
