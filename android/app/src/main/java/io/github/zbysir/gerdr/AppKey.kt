package io.github.zbysir.gerdr

import android.app.Activity
import android.content.Context
import android.hardware.biometrics.BiometricManager
import android.hardware.biometrics.BiometricPrompt
import android.os.Build
import android.os.CancellationSignal
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyPermanentlyInvalidatedException
import android.security.keystore.KeyProperties
import android.security.keystore.StrongBoxUnavailableException
import java.math.BigInteger
import java.security.KeyPairGenerator
import java.security.KeyStore
import java.security.PrivateKey
import java.security.Signature
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec

/**
 * This device's App key: re-verify with a fingerprint / face instead of the browser, on phones whose WebView can't do
 * passkeys (MainActivity.webAuthn false: they sign in through the browser device flow, Login.kt). Server:
 * internal/auth/appkey.go (the rules and why); the iOS twin is ios/Gerdr/AppKey.swift; docs/dev/ANDROID.md "App key".
 *
 * A P-256 key in the Android Keystore (StrongBox when the phone has one): the private half never leaves it, every
 * signature needs a strong biometric through BiometricPrompt, and enrolling a new fingerprint voids it
 * (setInvalidatedByBiometricEnrollment; then the browser approval makes a new one). One key per server origin.
 * Uses the platform BiometricPrompt (API 28+), not androidx's: MainActivity is a ComponentActivity.
 */
/** The phone's language: zh or en (the page has its own i18n; these are the system prompt's words). */
private fun L(zh: String, en: String) = if (java.util.Locale.getDefault().language == "zh") zh else en

object AppKey {
    private fun alias(origin: String) = "gerdr.appkey:$origin"

    private fun keyStore() = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }

    /** A strong biometric is set up. Android 9 has no way to ask, so the feature starts at 10. */
    fun available(ctx: Context): Boolean {
        val bm = ctx.getSystemService(BiometricManager::class.java) ?: return false
        return when {
            Build.VERSION.SDK_INT >= 30 ->
                bm.canAuthenticate(BiometricManager.Authenticators.BIOMETRIC_STRONG) == BiometricManager.BIOMETRIC_SUCCESS
            Build.VERSION.SDK_INT == 29 -> @Suppress("DEPRECATION") (bm.canAuthenticate() == BiometricManager.BIOMETRIC_SUCCESS)
            else -> false
        }
    }

    fun exists(origin: String): Boolean = try { keyStore().containsAlias(alias(origin)) } catch (_: Exception) { false }

    fun delete(origin: String) {
        try { keyStore().deleteEntry(alias(origin)) } catch (_: Exception) {}
    }

    /** A fresh key for this origin (replacing any old one); returns its public half as the uncompressed point (65 bytes). */
    fun create(origin: String): ByteArray {
        delete(origin)
        fun spec(strongBox: Boolean) = KeyGenParameterSpec.Builder(alias(origin), KeyProperties.PURPOSE_SIGN)
            .setAlgorithmParameterSpec(ECGenParameterSpec("secp256r1"))
            .setDigests(KeyProperties.DIGEST_SHA256)
            .setUserAuthenticationRequired(true)
            .setInvalidatedByBiometricEnrollment(true)
            .apply {
                if (Build.VERSION.SDK_INT >= 30) setUserAuthenticationParameters(0, KeyProperties.AUTH_BIOMETRIC_STRONG)
                else @Suppress("DEPRECATION") setUserAuthenticationValidityDurationSeconds(-1) // every use, via CryptoObject
                if (strongBox) setIsStrongBoxBacked(true)
            }
            .build()
        val gen = KeyPairGenerator.getInstance(KeyProperties.KEY_ALGORITHM_EC, "AndroidKeyStore")
        val kp = try {
            gen.initialize(spec(true))
            gen.generateKeyPair()
        } catch (_: StrongBoxUnavailableException) {
            gen.initialize(spec(false))
            gen.generateKeyPair()
        }
        val w = (kp.public as ECPublicKey).w
        return byteArrayOf(4) + fixed(w.affineX) + fixed(w.affineY)
    }

    /** A coordinate as exactly 32 bytes (BigInteger adds a sign byte or drops leading zeros). */
    private fun fixed(v: BigInteger): ByteArray {
        val b = v.toByteArray()
        return when {
            b.size == 32 -> b
            b.size > 32 -> b.copyOfRange(b.size - 32, b.size)
            else -> ByteArray(32 - b.size) + b
        }
    }

    sealed class Result {
        class Signed(val der: ByteArray) : Result()
        object Cancelled : Result()
        class Failed(val msg: String, val voided: Boolean) : Result()
    }

    /**
     * Sign the server's payload (auth.AppKeyPayload) after the biometric prompt; DER, as the server's VerifyASN1 wants.
     * Call on the main thread; `done` comes back on it.
     */
    fun sign(act: Activity, origin: String, host: String, deviceId: String, nonce: String, done: (Result) -> Unit) {
        val sig = try {
            val key = keyStore().getKey(alias(origin), null) as? PrivateKey
                ?: return done(Result.Failed("no key", voided = true))
            Signature.getInstance("SHA256withECDSA").apply { initSign(key) }
        } catch (_: KeyPermanentlyInvalidatedException) {
            return done(Result.Failed("the key was voided (a new fingerprint?)", voided = true))
        } catch (e: Exception) {
            return done(Result.Failed(e.toString(), voided = false))
        }
        val exec = act.mainExecutor
        val prompt = BiometricPrompt.Builder(act)
            .setTitle(L("验证这台设备", "Verify this device"))
            .setNegativeButton(L("取消", "Cancel"), exec) { _, _ -> done(Result.Cancelled) }
            .apply { if (Build.VERSION.SDK_INT >= 30) setAllowedAuthenticators(BiometricManager.Authenticators.BIOMETRIC_STRONG) }
            .build()
        val payload = "gerdr-appkey-v1\n$host\n$deviceId\n$nonce".toByteArray()
        prompt.authenticate(BiometricPrompt.CryptoObject(sig), CancellationSignal(), exec, object : BiometricPrompt.AuthenticationCallback() {
            override fun onAuthenticationSucceeded(r: BiometricPrompt.AuthenticationResult) {
                try {
                    val s = r.cryptoObject.signature ?: return done(Result.Failed("no signature", voided = false))
                    s.update(payload)
                    done(Result.Signed(s.sign()))
                } catch (e: Exception) {
                    done(Result.Failed(e.toString(), voided = false))
                }
            }

            override fun onAuthenticationError(code: Int, msg: CharSequence) {
                if (code == BiometricPrompt.BIOMETRIC_ERROR_USER_CANCELED || code == BiometricPrompt.BIOMETRIC_ERROR_CANCELED) done(Result.Cancelled)
                else done(Result.Failed(msg.toString(), voided = false))
            }
            // onAuthenticationFailed (a finger not recognised) keeps the prompt open: nothing to do
        })
    }
}
