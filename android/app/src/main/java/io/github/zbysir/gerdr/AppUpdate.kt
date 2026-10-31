package io.github.zbysir.gerdr

import android.app.Activity
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.provider.Settings
import android.widget.Toast
import org.json.JSONObject
import java.io.File
import java.net.HttpURLConnection
import java.net.URL
import java.security.MessageDigest
import java.util.concurrent.Executors

/**
 * The App updates itself straight from GitHub, not through the gerdr it's connected to: the release's android job
 * writes `android.json` (version, file, size, sha256) next to the APK, and `releases/latest/download/android.json`
 * always points at the newest one.
 *
 * Checked when the App comes to the front, at most every [EVERY]; a newer APK is downloaded by itself into the
 * cache and checked against the sha256. Installing still needs a tap: Android only installs a sideloaded app after
 * the person allows "install unknown apps" once and confirms the system dialog. On Android 12+ we ask for no
 * confirmation (setRequireUserAction), which the system may grant once this App installed itself before.
 * Android itself refuses an APK signed with another key, so a tampered download can't get in either way.
 *
 * The page sees the state as `{t:"update"}` (Bridge), and asks `get` / `check` / `install`.
 */
object AppUpdate {
    private const val FEED = "https://github.com/gerdr-dev/gerdr/releases/latest/download/android.json"
    private const val EVERY = 6 * 3600 * 1000L

    // idle | checking | downloading | ready | installing
    @Volatile var phase = "idle"; private set
    @Volatile var latest = ""; private set // the newest version seen (empty: not checked / up to date)
    @Volatile var progress = 0; private set
    @Volatile var error = ""; private set
    @Volatile var checkedAt = 0L; private set

    /** Told about every change, on the main thread (MainActivity forwards it to the page). */
    var onChange: (() -> Unit)? = null

    private val io = Executors.newSingleThreadExecutor()
    private val main = Handler(Looper.getMainLooper())
    private fun changed() = main.post { onChange?.invoke() }

    fun state(ctx: Context): JSONObject = JSONObject().put("t", "update")
        .put("version", BuildInfo.version(ctx)).put("phase", phase).put("latest", latest)
        .put("progress", progress).put("error", error).put("checkedAt", checkedAt)

    /** On coming to the front: check if the last one was long enough ago. Debug builds never update themselves. */
    fun maybeCheck(ctx: Context) {
        if (BuildInfo.debuggable(ctx)) return
        val last = ctx.getSharedPreferences("update", Context.MODE_PRIVATE).getLong("checked", 0)
        if (System.currentTimeMillis() - last >= EVERY) check(ctx)
    }

    fun check(ctx: Context) {
        if (phase != "idle" && phase != "ready") return
        val app = ctx.applicationContext
        phase = "checking"; error = ""
        changed()
        io.execute {
            try {
                val feed = JSONObject(get(FEED).toString(Charsets.UTF_8))
                checkedAt = System.currentTimeMillis()
                app.getSharedPreferences("update", Context.MODE_PRIVATE).edit().putLong("checked", checkedAt).apply()
                val ver = feed.getString("version")
                if (!newer(ver, BuildInfo.version(app))) {
                    latest = ""
                    phase = "idle"
                    return@execute
                }
                latest = ver
                val apk = apkFile(app)
                if (apk.exists() && sha256(apk) == feed.getString("sha256")) {
                    phase = "ready"
                    return@execute
                }
                phase = "downloading"; progress = 0
                changed()
                val url = "https://github.com/gerdr-dev/gerdr/releases/download/v$ver/${Uri.encode(feed.getString("file"))}"
                download(url, apk, feed.optLong("size"))
                if (sha256(apk) != feed.getString("sha256")) {
                    apk.delete()
                    throw Exception("下载的安装包校验不对")
                }
                phase = "ready"
            } catch (e: Exception) {
                error = e.message ?: e.toString()
                phase = "idle"
            } finally {
                changed()
            }
        }
    }

    /**
     * Hand the downloaded APK to the system installer. The first time Android sends the person to "install unknown
     * apps" for gerdr instead; after allowing it they tap install again.
     */
    fun install(act: Activity) {
        if (phase != "ready") return
        if (Build.VERSION.SDK_INT >= 26 && !act.packageManager.canRequestPackageInstalls()) {
            Toast.makeText(act, "先允许 gerdr 安装应用，回来再点一次安装", Toast.LENGTH_LONG).show()
            act.startActivity(Intent(Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES, Uri.parse("package:${act.packageName}")))
            return
        }
        val apk = apkFile(act)
        phase = "installing"
        changed()
        io.execute {
            try {
                val pi = act.packageManager.packageInstaller
                val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL)
                if (Build.VERSION.SDK_INT >= 31) params.setRequireUserAction(PackageInstaller.SessionParams.USER_ACTION_NOT_REQUIRED)
                val id = pi.createSession(params)
                pi.openSession(id).use { s ->
                    s.openWrite("gerdr.apk", 0, apk.length()).use { out -> apk.inputStream().use { it.copyTo(out) } }
                    val flags = PendingIntent.FLAG_UPDATE_CURRENT or (if (Build.VERSION.SDK_INT >= 31) PendingIntent.FLAG_MUTABLE else 0)
                    val cb = PendingIntent.getBroadcast(act, id, Intent(act, InstallResult::class.java), flags)
                    s.commit(cb.intentSender)
                }
            } catch (e: Exception) {
                error = e.message ?: e.toString()
                phase = "ready"
                changed()
            }
        }
    }

    /** The installer's answer: show its confirmation dialog, or report a failure. Success kills this process. */
    class InstallResult : BroadcastReceiver() {
        override fun onReceive(ctx: Context, intent: Intent) {
            when (intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)) {
                PackageInstaller.STATUS_PENDING_USER_ACTION -> {
                    @Suppress("DEPRECATION")
                    val confirm = intent.getParcelableExtra<Intent>(Intent.EXTRA_INTENT) ?: return
                    ctx.startActivity(confirm.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
                }
                PackageInstaller.STATUS_SUCCESS -> {}
                else -> {
                    // Cancelled in the dialog, or the system refused (e.g. signed with another key)
                    val msg = intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE) ?: "没装上"
                    error = if (intent.getIntExtra(PackageInstaller.EXTRA_STATUS, 0) == PackageInstaller.STATUS_FAILURE_ABORTED) "" else msg
                    phase = "ready"
                    changed()
                }
            }
        }
    }

    private fun apkFile(ctx: Context) = File(ctx.cacheDir, "update.apk")

    private fun get(url: String): ByteArray {
        val c = open(url)
        try {
            return c.inputStream.use { it.readBytes() }
        } finally {
            c.disconnect()
        }
    }

    private fun download(url: String, to: File, size: Long) {
        val c = open(url)
        try {
            val total = c.contentLengthLong.takeIf { it > 0 } ?: size
            val tmp = File(to.path + ".part")
            var got = 0L
            c.inputStream.use { inp ->
                tmp.outputStream().use { out ->
                    val buf = ByteArray(64 * 1024)
                    while (true) {
                        val n = inp.read(buf)
                        if (n < 0) break
                        out.write(buf, 0, n)
                        got += n
                        val p = if (total > 0) (got * 100 / total).toInt() else 0
                        if (p != progress) { progress = p; changed() }
                    }
                }
            }
            if (!tmp.renameTo(to)) throw Exception("存不下安装包")
        } finally {
            c.disconnect()
        }
    }

    /** GitHub's release links redirect (github.com → objects.githubusercontent.com); HttpURLConnection follows https → https. */
    private fun open(url: String): HttpURLConnection {
        val c = URL(url).openConnection() as HttpURLConnection
        c.connectTimeout = 15000
        c.readTimeout = 30000
        c.instanceFollowRedirects = true
        if (c.responseCode !in 200..299) {
            val code = c.responseCode
            c.disconnect()
            throw Exception("GitHub 回了 $code")
        }
        return c
    }

    private fun sha256(f: File): String {
        val md = MessageDigest.getInstance("SHA-256")
        f.inputStream().use { inp ->
            val buf = ByteArray(64 * 1024)
            while (true) {
                val n = inp.read(buf)
                if (n < 0) break
                md.update(buf, 0, n)
            }
        }
        return md.digest().joinToString("") { "%02x".format(it) }
    }

    /** "0.2.10" > "0.2.9": by number, part by part. A local build's "0.1.0" is behind every release. */
    fun newer(a: String, b: String): Boolean {
        val x = a.substringBefore('-').split('.').map { it.toIntOrNull() ?: 0 }
        val y = b.substringBefore('-').split('.').map { it.toIntOrNull() ?: 0 }
        for (i in 0 until maxOf(x.size, y.size)) {
            val d = x.getOrElse(i) { 0 } - y.getOrElse(i) { 0 }
            if (d != 0) return d > 0
        }
        return false
    }
}
