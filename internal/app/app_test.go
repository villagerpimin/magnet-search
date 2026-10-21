package app

import (
	"testing"

	"github.com/pigzhu/magnet-search/internal/engine"
)

// TestProxyChainScope 校验代理链的顺序随作用范围变化。
func TestProxyChainScope(t *testing.T) {
	proxy := []string{"http://127.0.0.1:7890"}
	overseas := engine.Spec{Name: "bt4g", Region: engine.RegionOverseas}
	any := engine.Spec{Name: "apibay", Region: engine.RegionAny}

	cases := []struct {
		name string
		spec engine.Spec
		opts Options
		want []string
	}{
		{
			name: "境外引擎默认优先走代理",
			spec: overseas,
			opts: Options{Proxy: proxy, ProxyScope: ProxyScopeRegion},
			want: []string{"http://127.0.0.1:7890", ""},
		},
		{
			name: "通用引擎默认优先直连",
			spec: any,
			opts: Options{Proxy: proxy, ProxyScope: ProxyScopeRegion},
			want: []string{"", "http://127.0.0.1:7890"},
		},
		{
			name: "全部引擎走代理时通用引擎也优先代理",
			spec: any,
			opts: Options{Proxy: proxy, ProxyScope: ProxyScopeAll},
			want: []string{"http://127.0.0.1:7890", ""},
		},
		{
			name: "按引擎指定的代理优先级最高",
			spec: any,
			opts: Options{
				Proxy:      proxy,
				ProxyScope: ProxyScopeRegion,
				ProxyEngine: map[string]string{
					"apibay": "http://127.0.0.1:1080",
				},
			},
			want: []string{"http://127.0.0.1:1080", "http://127.0.0.1:7890"},
		},
		{
			name: "没有配置代理时全程直连",
			spec: overseas,
			opts: Options{ProxyScope: ProxyScopeRegion},
			want: []string{""},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ProxyChain(c.spec, c.opts)
			if len(got) != len(c.want) {
				t.Fatalf("代理链 = %v，期望 %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("代理链[%d] = %q，期望 %q（整条 %v）", i, got[i], c.want[i], got)
				}
			}
		})
	}
}
