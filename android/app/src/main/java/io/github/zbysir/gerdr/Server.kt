package io.github.zbysir.gerdr

import android.content.Context
import android.net.Uri

/**
 * 「连的是哪台 gerdr」—— 填的是它远程访问的那个地址（设置 → 远程访问里的 https://…）。
 * App 不带任何人的域名（每个用户连自己机器上的那台），所以第一次打开要人自己填。
 */
object Server {
    private const val PREFS = "server"
    private const val KEY = "origin"

    /**
     * 把人填的东西解析成一个 URL。认两种：光秃秃的地址（`gerdr.example.com`、带不带 https 都行），
     * 和配对链接（`https://host/?pair=CODE`，设置页二维码 / `gerdr remote pair` 打出来的）——
     * 后者原样拿去加载一次，服务端（remoteGuard 的 enterPair）会把 `?pair=` 换成 cookie 再把
     * 地址洗干净，App 这边什么都不用做。
     */
    fun parse(input: String): Uri? {
        var s = input.trim()
        if (s.isEmpty()) return null
        if (!s.contains("://")) s = "https://$s"
        val u = Uri.parse(s)
        if (u.scheme != "https" && u.scheme != "http") return null
        if (u.host.isNullOrEmpty()) return null
        return u
    }

    /** `scheme://host[:port]`，默认端口不写 —— 桥的 origin 白名单就是按这个格式比的 */
    fun origin(u: Uri): String {
        val port = if (u.port != -1) ":${u.port}" else ""
        return "${u.scheme}://${u.host}$port"
    }

    fun load(ctx: Context): Uri? =
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).getString(KEY, null)?.let(Uri::parse)

    fun save(ctx: Context, origin: String) {
        ctx.getSharedPreferences(PREFS, Context.MODE_PRIVATE).edit().putString(KEY, origin).apply()
    }

    /**
     * 本机 / 局域网地址。只有调试时会填这种（debug 包连 `make dev` 起的实例，`adb reverse` 之后
     * 填 `http://localhost:…`，或者直接填局域网 IP）—— 正式用法永远是远程访问那个域名，局域网直连
     * 由 LanRoute 在底下换路、**不换 origin**，页面不会跑到 IP 上去。
     *
     * 导航锁里放行这些地址，别的一律交给系统浏览器 —— 不然点一下 agent 输出里的链接，整个 App
     * 就被换成了别人的网页，返回键还回不来。LanRoute 也拿它判断「这个候选是不是局域网地址」。
     */
    fun isLocalHost(host: String?): Boolean {
        if (host.isNullOrEmpty()) return false
        if (host == "localhost" || host.endsWith(".local")) return true
        val p = host.split('.').mapNotNull { it.toIntOrNull() }
        if (p.size != 4) return false
        return p[0] == 10 ||
            (p[0] == 172 && p[1] in 16..31) ||
            (p[0] == 192 && p[1] == 168) ||
            (p[0] == 100 && p[1] in 64..127) // CGNAT（Tailscale 这类）
    }
}
