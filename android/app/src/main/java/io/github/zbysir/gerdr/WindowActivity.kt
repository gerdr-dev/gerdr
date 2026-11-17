package io.github.zbysir.gerdr

import android.net.Uri

/**
 * One more window, opened on a project from the page ("open in new window", MainActivity.newWindow). The manifest gives
 * it documentLaunchMode=always: every start is a task of its own, so the recents list shows each window apart and
 * switching projects is switching apps, like the desktop App's windows. Everything else is MainActivity's: its own
 * WebView and bridge, the same cookie jar, LAN proxy and notice service (process-wide).
 *
 * The page opens on `/<project id>` (ui sync.ts wantSpace), the address the desktop shells give a new window. When the
 * system brings the task back after killing the process it relaunches this with the same intent, so it reopens there.
 * Notifications and the launcher still go to the main window (singleTask).
 */
class WindowActivity : MainActivity() {
    override fun startUri(server: Uri): Uri {
        val space = intent?.getStringExtra(EXTRA_SPACE)?.ifEmpty { null } ?: return server
        return Uri.parse(Server.origin(server) + "/" + Uri.encode(space))
    }

    companion object {
        const val EXTRA_SPACE = "space"
    }
}
