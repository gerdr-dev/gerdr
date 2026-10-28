package io.github.zbysir.gerdr

import android.graphics.Color
import kotlin.math.cos
import kotlin.math.pow
import kotlin.math.roundToInt
import kotlin.math.sin

/**
 * 把页面报来的 CSS 颜色（桥的 `theme` 消息）解析成 Android 的颜色。
 *
 * `Color.parseColor` 只认 `#rrggbb`，而页面拿到的往往不是这个：getComputedStyle 给的是
 * `rgb(26, 26, 31)`，gerdr 的颜色 token（index.css）又是 `oklch(…)` 写的，读 CSS 变量拿到的就是
 * 原文。认不出的话 [MainActivity.applyTheme] 直接放弃 —— 表现是亮色主题顶上一条黑，没有任何报错。
 */
object Css {
    fun color(s: String): Int? {
        val v = s.trim().lowercase()
        if (v.startsWith("#")) return try { Color.parseColor(v) } catch (_: Exception) { null }
        val open = v.indexOf('(')
        if (open < 0 || !v.endsWith(")")) return null
        val fn = v.substring(0, open)
        // 逗号、空格、斜杠（alpha）混着写都认；alpha 不管（系统栏没有半透明这回事）
        val parts = v.substring(open + 1, v.length - 1).split(',', ' ', '/').filter { it.isNotBlank() }
        if (parts.size < 3) return null
        return when (fn) {
            "rgb", "rgba" -> {
                val c = parts.take(3).map { channel(it) ?: return null }
                Color.rgb(c[0], c[1], c[2])
            }
            "oklch" -> {
                val l = num(parts[0], 1.0) ?: return null
                val ch = num(parts[1], 0.4) ?: return null
                val h = parts[2].removeSuffix("deg").toDoubleOrNull() ?: 0.0 // `none` 当 0
                oklch(l, ch, h)
            }
            else -> null
        }
    }

    /** rgb() 的一个分量：0–255 或者百分比 */
    private fun channel(p: String): Int? {
        val x = num(p, 255.0) ?: return null
        return x.roundToInt().coerceIn(0, 255)
    }

    /** 数字或百分比；百分比按 [full] 折算（oklch 的 L 100% = 1、C 100% = 0.4，照 CSS Color 4） */
    private fun num(p: String, full: Double): Double? =
        if (p.endsWith("%")) p.dropLast(1).toDoubleOrNull()?.let { it / 100 * full }
        else p.toDoubleOrNull()

    /** OKLCH → sRGB（Björn Ottosson 的 OKLab 公式），超出色域的直接夹住 */
    private fun oklch(l: Double, c: Double, hDeg: Double): Int {
        val h = Math.toRadians(hDeg)
        val a = c * cos(h)
        val b = c * sin(h)
        val l1 = (l + 0.3963377774 * a + 0.2158037573 * b).pow(3)
        val m1 = (l - 0.1055613458 * a - 0.0638541728 * b).pow(3)
        val s1 = (l - 0.0894841775 * a - 1.2914855480 * b).pow(3)
        val r = 4.0767416621 * l1 - 3.3077115913 * m1 + 0.2309699292 * s1
        val g = -1.2684380046 * l1 + 2.6097574011 * m1 - 0.3413193965 * s1
        val bl = -0.0041960863 * l1 - 0.7034186147 * m1 + 1.7076147010 * s1
        return Color.rgb(gamma(r), gamma(g), gamma(bl))
    }

    private fun gamma(x: Double): Int {
        val v = if (x <= 0.0031308) 12.92 * x else 1.055 * x.pow(1 / 2.4) - 0.055
        return (v * 255).roundToInt().coerceIn(0, 255)
    }

}
