package bencode

import (
	"reflect"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		value Value
	}{
		{
			name:  "整数",
			value: NewInt(42),
		},
		{
			name:  "负整数",
			value: NewInt(-7),
		},
		{
			name:  "零",
			value: NewInt(0),
		},
		{
			name:  "字节串",
			value: NewBytes([]byte("spam")),
		},
		{
			name:  "空字节串",
			value: NewBytes([]byte{}),
		},
		{
			name:  "含零字节的字节串",
			value: NewBytes([]byte{0x00, 0x20, 0xff}),
		},
		{
			name:  "列表",
			value: NewList([]Value{NewInt(1), NewBytes([]byte("a")), NewList(nil)}),
		},
		{
			name:  "字典",
			value: NewDict(map[string]Value{"b": NewInt(2), "a": NewInt(1)}),
		},
		{
			name: "嵌套字典",
			value: NewDict(map[string]Value{
				"a": NewDict(map[string]Value{"id": NewBytes([]byte{1, 2, 3})}),
			}),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := Encode(tc.value)
			if err != nil {
				t.Fatalf("Encode 失败: %v", err)
			}
			decoded, err := Decode(encoded)
			if err != nil {
				t.Fatalf("Decode 失败: %v", err)
			}
			if !reflect.DeepEqual(decoded, tc.value) {
				t.Fatalf("往返结果不一致: 编码 %q 解码后 %#v, 期望 %#v", encoded, decoded, tc.value)
			}
		})
	}
}

func TestEncodeDictKeyOrder(t *testing.T) {
	value := NewDict(map[string]Value{
		"zebra": NewInt(1),
		"apple": NewInt(2),
		"mango": NewInt(3),
	})
	encoded, err := Encode(value)
	if err != nil {
		t.Fatalf("Encode 失败: %v", err)
	}
	want := "d5:applei2e5:mangoi3e5:zebrai1ee"
	if string(encoded) != want {
		t.Fatalf("字典键未按字节序排列: 得到 %q, 期望 %q", encoded, want)
	}
}

func TestDecodeMalformed(t *testing.T) {
	cases := []struct {
		name string
		data string
	}{
		{name: "空输入", data: ""},
		{name: "整数缺结束符", data: "i42"},
		{name: "整数为空", data: "ie"},
		{name: "整数只有负号", data: "i-e"},
		{name: "整数有前导零", data: "i03e"},
		{name: "整数溢出", data: "i99999999999999999999999e"},
		{name: "字节串长度超界", data: "5:abc"},
		{name: "字节串缺分隔符", data: "4spam"},
		{name: "非法类型标记", data: "abc"},
		{name: "列表未闭合", data: "l"},
		{name: "字典未闭合", data: "d"},
		{name: "字典键不是字节串", data: "d3:abce"},
		{name: "字典值缺失", data: "d3:abc"},
		{name: "尾部多余数据", data: "i1ei2e"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, err := Decode([]byte(tc.data))
			if err == nil {
				t.Fatalf("非法输入 %q 应当返回错误, 实际得到 %#v", tc.data, value)
			}
		})
	}
}

func TestDecodeNeverPanics(t *testing.T) {
	inputs := [][]byte{
		nil,
		{0x00},
		{0xff, 0xff, 0xff},
		[]byte("d"),
		[]byte("l"),
		[]byte("i"),
		[]byte("9999999999999999999999:"),
		[]byte("d1:ae"),
		[]byte("lld1:ae"),
	}
	for _, in := range inputs {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("Decode(%q) panic: %v", in, r)
				}
			}()
			if _, err := Decode(in); err == nil {
				t.Fatalf("Decode(%q) 应当返回错误", in)
			}
		}()
	}
}

func TestAccessors(t *testing.T) {
	dict := NewDict(map[string]Value{
		"complete":                 NewInt(42),
		"name":                     NewString("demo"),
		"peers":                    NewBytes([]byte{1, 2, 3, 4, 5, 6}),
		"list":                     NewList([]Value{NewInt(1), NewInt(2)}),
		"sub":                      NewDict(map[string]Value{"k": NewInt(9)}),
		string([]byte{0xaa, 0xbb}): NewInt(7),
	})

	if got, ok := Int(dict, "complete"); !ok || got != 42 {
		t.Fatalf("Int(complete) = %d, %v, 期望 42 true", got, ok)
	}
	if got, ok := Int(dict, "missing"); ok || got != 0 {
		t.Fatalf("Int(missing) = %d, %v, 期望 0 false", got, ok)
	}
	if got, ok := String(dict, "name"); !ok || got != "demo" {
		t.Fatalf("String(name) = %q, %v, 期望 demo true", got, ok)
	}
	if got, ok := Bytes(dict, "peers"); !ok || !reflect.DeepEqual(got, []byte{1, 2, 3, 4, 5, 6}) {
		t.Fatalf("Bytes(peers) = %v, %v, 期望 1..6 true", got, ok)
	}
	list, ok := List(dict, "list")
	if !ok || len(list) != 2 {
		t.Fatalf("List(list) = %v, %v, 期望长度 2", list, ok)
	}
	sub, ok := Dict(dict, "sub")
	if !ok {
		t.Fatal("Dict(sub) 应当存在")
	}
	if got, ok := Int(sub, "k"); !ok || got != 9 {
		t.Fatalf("Int(sub.k) = %d, %v, 期望 9 true", got, ok)
	}
	byBytes, ok := DictBytes(dict, []byte{0xaa, 0xbb})
	if !ok {
		t.Fatal("DictBytes 应当命中二进制键")
	}
	if got, ok := Int(byBytes, ""); ok {
		t.Fatalf("二进制键的值不应是整数, 得到 %d", got)
	}
}

func TestEncodeInvalidValue(t *testing.T) {
	if _, err := Encode(Value{kind: Kind(99)}); err == nil {
		t.Fatal("未知类型应当返回错误")
	}
}
