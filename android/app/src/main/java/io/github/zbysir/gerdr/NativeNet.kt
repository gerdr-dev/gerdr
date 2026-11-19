package io.github.zbysir.gerdr

import android.util.Base64
import okhttp3.Call
import okhttp3.Callback
import okhttp3.EventListener
import okhttp3.MediaType.Companion.toMediaTypeOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString
import org.json.JSONObject
import java.io.IOException
import java.util.concurrent.ConcurrentHashMap
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Add-ons' g.socket / g.fetch made by the phone itself, straight to the service (the page half is
 * ui/src/lib/appnet.ts; the bridge messages `ws` and `http` in docs/dev/ANDROID.md). Otherwise they go through the
 * computer's backend, which remotely is phone → relay → computer → service and back: streaming speech-to-text
 * (the voice add-on) lagged behind the voice. The page can't connect itself: its CSP is `connect-src 'self'`, a
 * browser WebSocket can't set the headers these APIs authenticate with, and their HTTP APIs send no CORS headers.
 *
 * It gives the page nothing it couldn't already do through the backend (g.fetch / g.socket reach any URL from the
 * computer), and the bridge is only on the server's origin, main frame. Same limits as the backend: http(s) /
 * ws(s) only, 30s per request, 10 MB per answer, 5 redirects.
 *
 * When the phone can't reach the host at all (no DNS answer, refused, connect timeout) and nothing of the request
 * went out, the failure says `unreachable`: the page then makes the same request through the backend (appnet.ts),
 * for hosts only the computer reaches (behind its VPN). Anything the service answered is never retried.
 *
 * Callbacks come on OkHttp's threads; [post] hands each message to the main thread in order (the page's reply
 * proxy must be used there).
 */
class NativeNet(private val post: (JSONObject) -> Unit) {
    private val sockets = ConcurrentHashMap<String, WebSocket>()

    /** Open a socket: answers `ws` events (open / message / error / close) for its `id`. */
    fun open(m: JSONObject) {
        val id = m.optString("id")
        val url = m.optString("url")
        val scheme = url.substringBefore("://", "").lowercase()
        if (id.isEmpty() || (scheme != "ws" && scheme != "wss")) {
            post(ev(id, "error").put("msg", "url must be ws(s): $url"))
            post(ev(id, "close").put("code", 1006).put("reason", ""))
            return
        }
        val req = try {
            val b = Request.Builder().url(url)
            m.optJSONObject("headers")?.let { h -> h.keys().forEach { k -> b.header(k, h.optString(k)) } }
            m.optJSONArray("protocols")?.let { p ->
                if (p.length() > 0) b.header("Sec-WebSocket-Protocol", (0 until p.length()).joinToString(", ") { p.optString(it) })
            }
            b.build()
        } catch (e: Exception) {
            post(ev(id, "error").put("msg", e.message ?: "bad request"))
            post(ev(id, "close").put("code", 1006).put("reason", ""))
            return
        }
        sockets[id] = client.newWebSocket(req, object : WebSocketListener() {
            var opened = false

            override fun onOpen(webSocket: WebSocket, response: Response) {
                opened = true
                post(ev(id, "open").put("protocol", response.header("Sec-WebSocket-Protocol") ?: ""))
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                post(ev(id, "message").put("text", text))
            }

            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                post(ev(id, "message").put("b64", Base64.encodeToString(bytes.toByteArray(), Base64.NO_WRAP)))
            }

            // The server's close: answer it (OkHttp waits for ours), the page hears close once onClosed lands
            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                webSocket.close(code, null)
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                sockets.remove(id)
                post(ev(id, "close").put("code", code).put("reason", reason))
            }

            // A failed handshake (a 401 for a wrong key) or a dropped connection: error then close 1006, as a browser does
            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                sockets.remove(id)
                val why = if (response != null) "HTTP ${response.code} ${response.message}".trim() else (t.message ?: t.javaClass.simpleName)
                // No answer to the handshake and never open: nothing reached the service, safe to try another way
                post(ev(id, "error").put("msg", why).put("unreachable", response == null && !opened))
                post(ev(id, "close").put("code", 1006).put("reason", ""))
            }
        })
    }

    fun send(m: JSONObject) {
        val s = sockets[m.optString("id")] ?: return
        if (m.has("b64")) s.send(Base64.decode(m.optString("b64"), Base64.DEFAULT).toByteString())
        else s.send(m.optString("text"))
    }

    fun close(m: JSONObject) {
        val s = sockets[m.optString("id")] ?: return
        // 1005 / 1006 can't be sent; a page's close() without a code means 1000
        val code = m.optInt("code", 1000).let { if (it == 1000 || it in 3000..4999) it else 1000 }
        if (!s.close(code, m.optString("reason").ifEmpty { null })) s.cancel()
    }

    /** The page went away (reload, another page): its sockets have no one to talk to. */
    fun closeAll() {
        sockets.values.forEach { it.cancel() }
        sockets.clear()
    }

    /** One request, answered as `http` with the backend's /api/addons/{id}/fetch shape, or `error`. */
    fun fetch(m: JSONObject) {
        val id = m.optString("id")
        val url = m.optString("url")
        val scheme = url.substringBefore("://", "").lowercase()
        if (scheme != "http" && scheme != "https") {
            post(JSONObject().put("t", "http").put("id", id).put("error", "url must be http(s): $url"))
            return
        }
        val req = try {
            val method = m.optString("method").ifEmpty { "GET" }.uppercase()
            val headers = m.optJSONObject("headers")
            var type: String? = null
            val b = Request.Builder().url(url).tag(Sent::class.java, Sent())
            headers?.keys()?.forEach { k ->
                val v = headers.optString(k)
                if (k.equals("content-type", true)) type = v
                b.header(k, v)
            }
            var body: RequestBody? = null
            if (m.has("body")) {
                val raw = m.optString("body")
                val bytes = if (m.optBoolean("bodyBase64")) Base64.decode(raw, Base64.DEFAULT) else raw.toByteArray()
                body = bytes.toRequestBody(type?.toMediaTypeOrNull())
            } else if (method == "POST" || method == "PUT" || method == "PATCH") {
                body = ByteArray(0).toRequestBody(null)
            }
            b.method(method, body).build()
        } catch (e: Exception) {
            post(JSONObject().put("t", "http").put("id", id).put("error", e.message ?: "bad request"))
            return
        }
        val binary = m.optBoolean("binary")
        client.newCall(req).enqueue(object : Callback {
            override fun onFailure(call: Call, e: IOException) {
                val sent = call.request().tag(Sent::class.java)?.get() ?: true
                post(
                    JSONObject().put("t", "http").put("id", id).put("error", e.message ?: e.javaClass.simpleName)
                        .put("unreachable", !sent),
                )
            }

            override fun onResponse(call: Call, response: Response) {
                response.use { r ->
                    val out = JSONObject().put("t", "http").put("id", id)
                    try {
                        val src = r.body!!.source()
                        // Read one past the limit to tell "exactly 10 MB" from "more"
                        src.request(MAX_BODY + 1)
                        if (src.buffer.size > MAX_BODY) {
                            post(out.put("error", "response larger than ${MAX_BODY shr 20} MB"))
                            return
                        }
                        val data = src.readByteArray()
                        val h = JSONObject()
                        for (name in r.headers.names()) h.put(name.lowercase(), r.header(name))
                        out.put("status", r.code).put("statusText", r.message).put("headers", h)
                        if (binary) out.put("body", Base64.encodeToString(data, Base64.NO_WRAP)).put("base64", true)
                        else out.put("body", String(data, Charsets.UTF_8))
                        post(out)
                    } catch (e: Exception) {
                        post(out.put("error", e.message ?: e.javaClass.simpleName))
                    }
                }
            }
        })
    }

    private fun ev(id: String, ev: String) = JSONObject().put("t", "ws").put("id", id).put("ev", ev)

    /** Set once the request's headers start going out (an [EventListener]): from then on a failure isn't "unreachable" */
    private class Sent : AtomicBoolean()

    companion object {
        private const val MAX_BODY = 10L shl 20

        // One client for the App: its own connection pool and threads. callTimeout covers a whole request (as the
        // backend's 30s); WebSockets are exempt from it. A ping every 20s keeps a quiet socket through NATs.
        private val client by lazy {
            OkHttpClient.Builder()
                // Short, so a host only the computer reaches falls back soon (appnet.ts)
                .connectTimeout(8, TimeUnit.SECONDS)
                .callTimeout(30, TimeUnit.SECONDS)
                .eventListener(object : EventListener() {
                    override fun requestHeadersStart(call: Call) {
                        call.request().tag(Sent::class.java)?.set(true)
                    }
                })
                .pingInterval(20, TimeUnit.SECONDS)
                .build()
        }
    }
}
