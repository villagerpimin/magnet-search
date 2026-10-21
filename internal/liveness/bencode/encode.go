package bencode

import (
	"bytes"
	"fmt"
	"sort"
	"strconv"
)

// Encode 把值编码为 bencode 字节序列。
// 字典的键按字节序升序输出，这是 bencode 协议对字典顺序的要求。
func Encode(v Value) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeValue(&buf, v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// encodeValue 把单个值写入缓冲区。
func encodeValue(buf *bytes.Buffer, v Value) error {
	switch v.kind {
	case KindInt:
		buf.WriteByte('i')
		buf.WriteString(strconv.FormatInt(v.num, 10))
		buf.WriteByte('e')
	case KindBytes:
		buf.WriteString(strconv.Itoa(len(v.raw)))
		buf.WriteByte(':')
		buf.Write(v.raw)
	case KindList:
		buf.WriteByte('l')
		for _, item := range v.list {
			if err := encodeValue(buf, item); err != nil {
				return err
			}
		}
		buf.WriteByte('e')
	case KindDict:
		keys := make([]string, 0, len(v.dict))
		for k := range v.dict {
			keys = append(keys, k)
		}
		// Go 的字符串比较是按字节比较，正好符合 bencode 的字典序要求。
		sort.Strings(keys)
		buf.WriteByte('d')
		for _, k := range keys {
			buf.WriteString(strconv.Itoa(len(k)))
			buf.WriteByte(':')
			buf.WriteString(k)
			if err := encodeValue(buf, v.dict[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('e')
	default:
		return fmt.Errorf("bencode: 无法编码未知类型 %d", v.kind)
	}
	return nil
}
