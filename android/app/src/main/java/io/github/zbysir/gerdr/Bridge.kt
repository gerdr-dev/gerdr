package io.github.zbysir.gerdr

import android.os.Handler
import android.os.Looper
import android.webkit.WebView
import androidx.webkit.JavaScriptReplyProxy
import androidx.webkit.WebViewCompat
import androidx.webkit.WebViewFeature
import org.json.JSONObject

/**
 * 网页 ↔ 壳 的那条线。网页那一半在 gerdr 的 `ui/src/lib/android.ts`，消息格式两边要对得上
 * （表在 docs/dev/ANDROID.md）。对象名是 `gerdrAndroid`，不叫 `gerdrNative`：后者是桌面 App
 * （Electron 的 preload）挂的，消息形状不一样（那边是对象 + `type`，这边是 JSON 字符串 + `t`），
 * 用同一个名字的话界面那边分不清是哪个壳。
 *
 * **只对服务器那个 origin 开**（`addWebMessageListener` 的白名单），不用
 * `addJavascriptInterface` —— 后者对 WebView 里所有 frame、所有 origin 都可见，而这个页面里
 * 渲染着 agent 的输出（不可信文本）。也只认主 frame 发来的。桥上只放无害的口：报平台、
 * 全屏、配色、开外部链接、返回键、音量键 —— 不放读文件、发任意 Intent 这类。
 */
class Bridge(private val act: MainActivity, web: WebView, origin: String) {
    /** 页面发过 hello 才有；换页 / 重载时作废（`reset`），等新页面再来一次 hello */
    private var page: JavaScriptReplyProxy? = null
    private var backCb: ((Boolean) -> Unit)? = null
    /**
     * 点通知进来要打开的会话。页面还没 hello（冷启动、或者正在重载）时先记着，hello 之后补发 ——
     * 直接丢掉的话表现是「点了通知，App 开了，停在上次那个会话」。
     */
    private var pendingFocus: String? = null
    private val main = Handler(Looper.getMainLooper())

    val ready get() = page != null

    init {
        if (WebViewFeature.isFeatureSupported(WebViewFeature.WEB_MESSAGE_LISTENER)) {
            WebViewCompat.addWebMessageListener(web, "gerdrAndroid", setOf(origin)) { _, msg, _, isMain, reply ->
                val data = msg.data
                if (isMain && data != null) onMessage(data, reply)
            }
        }
    }

    /** Something in the page is playing sound: the volume keys are left alone (MainActivity.dispatchKeyEvent) */
    var mediaPlaying = false
        private set

    /**
     * Whether the page has something for ↑ / ↓ right now (keyboard up, a dropdown open, a question waiting: android.ts
     * syncArrows); otherwise the volume keys stay volume keys. null = the page never said (a page from before this
     * message): keep the old always-on behaviour rather than never mapping them.
     */
    var arrows: Boolean? = null
        private set

    fun reset() {
        page = null
        mediaPlaying = false
        arrows = null
        backCb?.invoke(false)
        backCb = null
    }

    private fun onMessage(raw: String, reply: JavaScriptReplyProxy) {
        val m = try { JSONObject(raw) } catch (_: Exception) { return }
        when (m.optString("t")) {
            "hello" -> {
                page = reply
                act.hideLoading()
                // webauthn：WebView 自己能不能做 passkey（MainActivity.webAuthn）。能的话页面直接用
                // navigator.credentials；不能才发 login 走 device flow（Login.kt）
                send(
                    JSONObject().put("t", "hello").put("platform", "android")
                        .put("version", BuildInfo.version(act)).put("webauthn", act.webAuthn).put("scan", true)
                        .put("copyImage", true).put("update", true),
                )
                send(LanRoute.snapshot()) // 顶栏那个「直连 / 中继」标签的初值
                pendingFocus?.let { focus(it) }
            }
            "back" -> {
                val cb = backCb ?: return
                backCb = null
                cb(m.optBoolean("handled"))
            }
            "fullscreen" -> {
                act.setFullscreen(m.optBoolean("on"))
                send(JSONObject().put("t", "fullscreen").put("on", m.optBoolean("on")))
            }
            // Long-press menu's "copy image" (ui AppContextMenu): base64 `data` + `mime`, answered with `ok` / `error`
            "copyImage" -> act.copyImage(m.optString("data"), m.optString("mime", "image/png")) { err ->
                val r = JSONObject().put("t", "copyImage").put("ok", err == null)
                if (err != null) r.put("error", err)
                send(r)
            }
            // Self-update (AppUpdate.kt): answered with the state as `update`, and again on every change
            "update" -> when (m.optString("op")) {
                "check" -> AppUpdate.check(act)
                "install" -> AppUpdate.install(act)
                else -> send(AppUpdate.state(act))
            }
            "theme" -> act.applyTheme(m.optString("bg"), m.optBoolean("light"))
            "open" -> act.openExternal(m.optString("url"))
            "setup" -> act.showSetup(null)
            // Pair page's scan button; the answer comes back as `scan` (scanResult)
            "scan" -> act.scanPair()
            "login" -> act.startLogin(m.optBoolean("reauth"))
            "loginCancel" -> act.cancelLogin()
            // 局域网直连口的候选（`https://192.168.x.x:7878`），换路由壳做，不换 origin（LanRoute）
            "lan" -> {
                val a = m.optJSONArray("origins") ?: return
                LanRoute.setCandidates((0 until a.length()).map { a.optString(it) })
            }
            // A video / audio in the page started or stopped playing (android.ts syncMedia)
            "media" -> mediaPlaying = m.optBoolean("playing")
            "arrows" -> arrows = m.optBoolean("on")
            "volkeys" -> {
                if (m.optString("op") == "set") act.setVolumeKeys(m.optBoolean("on"))
                send(JSONObject().put("t", "volkeys").put("on", act.volumeKeys()))
            }
            "notify" -> {
                val op = m.optString("op")
                if (op == "get") send(act.notifyState()) else act.notifyOp(op, m.optBoolean("on"))
            }
        }
    }

    /** 打开某个会话（点了后台通知进来的）。页面还没接上就等 hello 再发 */
    fun focus(session: String) {
        if (page == null) {
            pendingFocus = session
            return
        }
        pendingFocus = null
        send(JSONObject().put("t", "focus").put("session", session))
    }

    /** Self-update state (AppUpdate.state), on every change */
    fun update(m: JSONObject) = send(m)

    /** 音量键（MainActivity.dispatchKeyEvent）：`dir` 是 up / down，`repeat` 是按住不放的连发 */
    fun key(dir: String, repeat: Boolean) = send(JSONObject().put("t", "key").put("k", dir).put("repeat", repeat))

    /** 「直连 / 中继」变了（LanRoute.onChange，已经节流过） */
    fun route() = send(LanRoute.snapshot())

    /** 后台通知那一块的状态（开没开 / 有没有通知权限 / 是否免电池优化），设置页照它画 */
    fun notifyState(m: JSONObject) = send(m)

    /** 登录流程的进展：`done` / `error`（带 `msg`）/ `cancel` */
    fun loginState(state: String, msg: String? = null) {
        val m = JSONObject().put("t", "login").put("state", state)
        if (msg != null) m.put("msg", msg)
        send(m)
    }

    /** Result of a pair-page scan: the pairing code, or an error to show (`code` / `error`) */
    fun scanResult(code: String?, error: String?) {
        val m = JSONObject().put("t", "scan")
        if (code != null) m.put("code", code)
        if (error != null) m.put("error", error)
        send(m)
    }

    /**
     * 返回键先问页面：开着面板 / 查看器就由页面收掉（和 Esc 同一套「谁在上面先关谁」）。
     * 页面没回音（卡住了、或者是个没接桥的旧版服务端）时 400ms 后按「没处理」走，
     * 不能让返回键变成「按了没反应」。
     */
    fun askBack(cb: (Boolean) -> Unit) {
        if (page == null) return cb(false)
        backCb?.invoke(false)
        backCb = cb
        send(JSONObject().put("t", "back"))
        main.postDelayed({
            if (backCb === cb) {
                backCb = null
                cb(false)
            }
        }, 400)
    }

    private fun send(m: JSONObject) {
        try {
            page?.postMessage(m.toString())
        } catch (_: Exception) {
            page = null // 页面已经没了
        }
    }
}
