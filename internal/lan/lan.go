// Package lan 回答两个问题：这台机器在局域网里的地址是哪几个，以及某个对端算不算「本地网络里的」。
//
// 照搬 herdr-web 的 internal/lan。用在局域网直连那条路上（docs/dev/ANDROID.md「局域网直连」）：
// 服务端把当前地址报给安卓 App，App 拿它探能不能直连；直连口只服务本地网络里的对端。
package lan

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

type Addr struct {
	Name    string // 网卡名（en0 / eth0…）
	Address string
	// Virtual：虚拟网卡上的地址（bridge / utun / vmnet / docker / OrbStack 那段）。手机永远碰不到，
	// 报给 App 只会白占一个探测名额（每个探不通的候选都要等一次超时）。
	Virtual bool
	score   int
}

// Addresses 机器上虚拟网卡一大堆（OrbStack / VPN / bridge），挑出手机真能连上的那个，排在最前面。
func Addresses() []Addr {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	out := []Addr{}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil {
				continue
			}
			ip := ipn.IP.To4().String()
			n := Addr{Name: ifc.Name, Address: ip}
			if strings.HasPrefix(ifc.Name, "en") {
				n.score += 10 // 无线 / 有线
			}
			for _, bad := range []string{"bridge", "utun", "vmnet", "llw", "awdl", "anpi", "ap", "docker", "veth", "tap", "tun"} {
				if strings.HasPrefix(ifc.Name, bad) {
					n.score -= 10
					n.Virtual = true
					break
				}
			}
			if strings.HasPrefix(ip, "198.18.") || strings.HasPrefix(ip, "198.19.") {
				n.score -= 20 // benchmark 段，OrbStack 在用
				n.Virtual = true
			}
			if IsPrivate(ip) {
				n.score += 2
			}
			out = append(out, n)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}

func IsPrivate(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && p.IsPrivate()
}

var (
	mu     sync.Mutex
	cached []Addr
	at     time.Time
)

// current：net.Interfaces() 是系统调用，页面每次加载都要问一遍候选，缓存两秒（换网之后最多两秒就跟上）。
func current() []Addr {
	mu.Lock()
	defer mu.Unlock()
	if cached != nil && time.Since(at) < 2*time.Second {
		return cached
	}
	cached, at = Addresses(), time.Now()
	return cached
}

// Origins 把当前局域网地址拼成 https origin：App 去探的候选。
//
// **只收私网地址上的真网卡**：公网 IP 报出去等于说「从任何地方都能直连我」，而这条路要解决的只是
// 「同一个 Wi-Fi 下别绕公网」。
func Origins(port int) []string {
	if port <= 0 {
		return nil
	}
	var out []string
	for _, a := range current() {
		if a.Virtual || !IsPrivate(a.Address) {
			continue
		}
		out = append(out, fmt.Sprintf("https://%s:%d", a.Address, port))
	}
	return out
}

// PeerIsLocal 这个对端地址算不算本地网络里的。传 RemoteAddr（带端口也行）。
//
// 直连口绑的是通配地址，而「通配地址只有局域网碰得到」是拓扑上的假设、不是代码保证的：路由器端口转发 / UPnP、
// 或者一台有全局 IPv6 的机器（Go 对 0.0.0.0 开的是双栈套接字，家宽 IPv6 通常没有 NAT），都能让公网直接连上来。
// 这道检查把「从直连口进来 = 对端在本地网络里」变成被强制的性质。
//
// CGNAT（100.64/10，Tailscale 在用）**不算**：那一段也被部分运营商用来分配真实客户地址，收进来等于放宽边界；
// 走 VPN 的人本来就经中继进得来。
func PeerIsLocal(remoteAddr string) bool {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil {
		return false // 解析不出来就当外面的 —— 这道门宁可误拒
	}
	// IsPrivate 覆盖 RFC1918 和 IPv6 的 fc00::/7；双栈套接字上的 IPv4 客户端是 ::ffff:192.168.x.x，To4 得出来，也命中
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}
