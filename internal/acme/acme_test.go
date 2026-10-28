package acme

import (
	"strings"
	"testing"
)

// 设置页按 Fields 画表单、exportEnv 按 dnsNamespaces 导出：两张表对不上的表现是「表单里填了却当没配」。
func TestProvidersConsistent(t *testing.T) {
	for _, p := range AllProviders() {
		ns := dnsNamespaces[p.Name]
		if len(ns) == 0 {
			t.Errorf("%s 不在 dnsNamespaces 里", p.Name)
		}
		if len(p.Fields) == 0 || p.Help == "" {
			t.Errorf("%s 没写要哪些凭据 / 去哪儿拿", p.Name)
		}
		for _, f := range p.Fields {
			ok := false
			for _, n := range ns {
				ok = ok || strings.HasPrefix(f.Env, n)
			}
			if !ok {
				t.Errorf("%s 的 %s 不在它的命名空间 %v 里，exportEnv 不会导出它", p.Name, f.Env, ns)
			}
		}
		if _, err := newDNS(p.Name); err != nil && strings.Contains(err.Error(), "不认识") {
			t.Errorf("%s 在表里但 newDNS 没有它", p.Name)
		}
	}
}
