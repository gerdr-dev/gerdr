package io.github.zbysir.gerdr

import android.content.Context
import android.media.AudioAttributes
import android.media.AudioFocusRequest
import android.media.AudioManager
import android.media.AudioRecordingConfiguration
import android.os.Handler
import android.os.Looper

/**
 * Holds audio focus while the page records (voice input: getUserMedia), so another app's music stays paused until
 * the recording ends, then resumes.
 *
 * Without it, with a Bluetooth headset playing music: pressing the mic paused the music, ~2s later it started again
 * on its own and the rest of the recording was silent (the recognizer got the first words, then nothing; seen in
 * the voice add-on's traces). The WebView starts the capture in communication mode but holds no focus of its own,
 * so the music app takes the headset back. The page can't hold focus, and a bridge message would need every page
 * that records to send it: Android tells an app about its own recordings, so this watches those instead
 * (AudioRecordingCallback; WebView's capture runs in our process).
 */
object MicFocus {
    private var started = false
    private var held: AudioFocusRequest? = null

    fun start(context: Context) {
        if (started) return
        started = true
        val am = context.applicationContext.getSystemService(AudioManager::class.java) ?: return
        val main = Handler(Looper.getMainLooper())
        val cb = object : AudioManager.AudioRecordingCallback() {
            override fun onRecordingConfigChanged(configs: MutableList<AudioRecordingConfiguration>) {
                update(am, configs.isNotEmpty())
            }
        }
        am.registerAudioRecordingCallback(cb, main)
        update(am, am.activeRecordingConfigurations.isNotEmpty())
    }

    private fun update(am: AudioManager, recording: Boolean) {
        if (recording && held == null) {
            val req = AudioFocusRequest.Builder(AudioManager.AUDIOFOCUS_GAIN_TRANSIENT_EXCLUSIVE)
                .setAudioAttributes(
                    AudioAttributes.Builder()
                        .setUsage(AudioAttributes.USAGE_VOICE_COMMUNICATION)
                        .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
                        .build()
                )
                // Losing it (a call comes in) changes nothing here: the recording ends on its own, and that abandons it
                .setOnAudioFocusChangeListener {}
                .build()
            if (am.requestAudioFocus(req) == AudioManager.AUDIOFOCUS_REQUEST_GRANTED) held = req
        } else if (!recording) {
            held?.let { am.abandonAudioFocusRequest(it) }
            held = null
        }
    }
}
