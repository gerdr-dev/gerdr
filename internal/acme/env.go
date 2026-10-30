package acme

import (
	"os"
	"strings"
)

// 云凭据的环境变量也带 GERDR_ 前缀，进 lego 之前再脱掉（照搬 herdr-web，那边是 HERDR_WEB_）。
//
// 为什么要这一层：lego 的每个 provider 都直接读固定名字（`CLOUDFLARE_DNS_API_TOKEN` 这种），
// 而本项目别的配置一律 `GERDR_*`。统一前缀之后 remote.json 的 env 那一节、启动环境里的写法是同一套
// （config.Remote 把 remote.json 里的项按 `GERDR_<名字>` 补进进程环境）。
//
// 对外统一成 `GERDR_<厂商变量>`。老写法（光秃秃的名字）仍然能用 —— lego 自己会读，
// 我们什么都不做；两个都给的话**带前缀的赢**，显式配的那个应该盖过环境里捡来的。
const envPrefix = "GERDR_"

// dnsNamespaces 是每家 provider 的环境变量前缀（对应 lego 里的 envNamespace）。
//
// 记前缀而不是列全名：除了凭据，每家还有一串可选项（`_TTL` / `_PROPAGATION_TIMEOUT` /
// `_FILE` 后缀那种从文件读的写法）。列全名一定会漏，而漏掉的表现是「我明明配了这个变量
// 却没生效」—— 比报错难查得多。
//
// Adding a provider takes four places (see the providers table in acme.go). 少了这张表的表现是
// 「带前缀的凭据被当成没配」。
var dnsNamespaces = map[string][]string{
	"cloudflare":   {"CLOUDFLARE_", "CF_"}, // CF_ 是 lego 认的别名
	"alidns":       {"ALICLOUD_"},
	"tencentcloud": {"TENCENTCLOUD_"},
	"route53":      {"AWS_"},
	"digitalocean": {"DO_"},
	"huaweicloud":  {"HUAWEICLOUD_"},
}

// exportEnv 把 GERDR_<厂商变量> 脱掉前缀写回进程环境，好让 lego 读到。
//
// 只翻译**当前这一家**的：`AWS_` 这种命名空间很宽（AWS SDK 自己也认一堆），跟着别家
// 一起导出去只会换来「我用的是 cloudflare，为什么 AWS 的东西也生效了」这种查不清的事。
//
// 导出来的名字不会漏进网页里的终端：PTY 的子进程环境按前缀清掉了这一批
// （`internal/server/pty.go` 的 dropEnv，`HERDR_` 和各家命名空间都在里面）。
func exportEnv(name string) (set []string) {
	ns := dnsNamespaces[name]
	if len(ns) == 0 {
		return nil
	}
	for _, kv := range os.Environ() {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || !strings.HasPrefix(k, envPrefix) {
			continue
		}
		bare := strings.TrimPrefix(k, envPrefix)
		for _, p := range ns {
			if strings.HasPrefix(bare, p) {
				if _, had := os.LookupEnv(bare); !had {
					set = append(set, bare)
				}
				_ = os.Setenv(bare, v)
				break
			}
		}
	}
	return set
}

// unexportEnv 把 exportEnv 新写进去的那几个名字撤掉（原来就有的不动）。
func unexportEnv(names []string) {
	for _, k := range names {
		_ = os.Unsetenv(k)
	}
}

// SecretEnv 判一个环境变量名是不是云凭据。给「别把它明文打到终端上」用
// （`gerdr remote status` 会把配置打出来，而那段输出往往就落在一个 agent 读得到的地方）。
//
// 判据是「在某家的命名空间里」+「名字里有 TOKEN / SECRET / KEY / PASSWORD」。
// 不整个命名空间一起盖是因为 `AWS_REGION` / `*_TTL` 这些恰恰是装的时候要看一眼的。
func SecretEnv(key string) bool {
	bare := strings.TrimPrefix(key, envPrefix)
	var known bool
	for _, ns := range dnsNamespaces {
		for _, p := range ns {
			if strings.HasPrefix(bare, p) {
				known = true
			}
		}
	}
	if !known {
		return false
	}
	for _, s := range []string{"TOKEN", "SECRET", "KEY", "PASSWORD"} {
		if strings.Contains(bare, s) {
			return true
		}
	}
	return false
}
