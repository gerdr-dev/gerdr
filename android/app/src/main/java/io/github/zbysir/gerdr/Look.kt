package io.github.zbysir.gerdr

import android.content.Context
import android.content.res.ColorStateList
import android.graphics.Typeface
import android.graphics.drawable.Drawable
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.RippleDrawable
import android.graphics.drawable.StateListDrawable
import android.util.TypedValue
import android.view.Gravity
import android.widget.Button
import android.widget.EditText
import android.widget.TextView

/**
 * 原生那几屏（填地址、连不上、passkey 浮层）的控件样式：照着配对页（internal/server/pairpage.html）
 * 那张卡片的样子画 —— 那一页是人在 App 里最先看到的网页，两边长得一样才不像「壳」和「网页」两个东西。
 * 颜色全从 res/values(-night)/colors.xml 拿（和 index.css 的 token 对齐），不在这里写死。
 *
 * 不用系统的 Button / EditText 默认外观：Theme.Material 的按钮是全大写、带阴影的灰块，
 * 各家 ROM 还会再改一遍，和网页那套扁平圆角对不上。
 */
class Look(private val c: Context) {
    val bg get() = c.getColor(R.color.bg)
    val card get() = c.getColor(R.color.card)
    val fg get() = c.getColor(R.color.fg)
    val muted get() = c.getColor(R.color.muted)
    val line get() = c.getColor(R.color.line)
    val ctl get() = c.getColor(R.color.ctl)
    val brand get() = c.getColor(R.color.brand)
    val bad get() = c.getColor(R.color.bad)

    fun dp(v: Int) = TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, v.toFloat(), c.resources.displayMetrics).toInt()

    fun shape(fill: Int, stroke: Int? = null, radius: Int = 8) = GradientDrawable().apply {
        setColor(fill)
        cornerRadius = dp(radius).toFloat()
        if (stroke != null) setStroke(dp(1).coerceAtLeast(1), stroke)
    }

    /** 卡片：card 底 + border 描边 + 14 圆角（配对页的 .card） */
    fun card() = shape(card, line, 14)

    fun text(s: String, sp: Float, color: Int = fg, bold: Boolean = false) = TextView(c).apply {
        text = s
        textSize = sp
        setTextColor(color)
        setLineSpacing(0f, 1.25f)
        if (bold) typeface = Typeface.create(Typeface.DEFAULT, Typeface.BOLD)
    }

    fun title(s: String) = text(s, 17f, fg, bold = true)
    fun lead(s: String) = text(s, 13f, muted)

    /** [primary] = 品牌色实心（配对页的 button.primary）；否则是 secondary 底 + 描边 */
    fun button(label: String, primary: Boolean) = Button(c).apply {
        text = label
        isAllCaps = false
        textSize = 14f
        stateListAnimator = null // 去掉 Material 按钮按下时浮起来的阴影
        minHeight = 0
        minimumHeight = 0
        minWidth = 0
        minimumWidth = 0
        setPadding(dp(16), dp(10), dp(16), dp(10))
        if (primary) typeface = Typeface.create(Typeface.DEFAULT, Typeface.BOLD)
        setTextColor(if (primary) c.getColor(R.color.brand_fg) else fg)
        val face = if (primary) shape(brand, radius = 8) else shape(ctl, line, 8)
        background = ripple(face, primary)
    }

    private fun ripple(face: Drawable, onBrand: Boolean): Drawable {
        // 按下的那层水波：品牌色上用浅色、普通底上用前景色，都压到很淡
        val tint = if (onBrand) 0x33FFFFFF else (fg and 0x00FFFFFF) or 0x1F000000
        return RippleDrawable(ColorStateList.valueOf(tint), face, shape(0xFF000000.toInt(), radius = 8))
    }

    /** 输入框：bg 底 + border 描边，聚焦时描边换成品牌色（配对页的 input） */
    fun input(hint: String) = EditText(c).apply {
        this.hint = hint
        textSize = 15f
        setTextColor(fg)
        setHintTextColor(muted)
        setPadding(dp(12), dp(11), dp(12), dp(11))
        gravity = Gravity.CENTER_VERTICAL
        background = StateListDrawable().apply {
            addState(intArrayOf(android.R.attr.state_focused), shape(bg, brand, 8))
            addState(intArrayOf(), shape(bg, line, 8))
        }
        // 光标和选中手柄跟着主题的 colorAccent（brand），这里只管光标颜色在老系统上也对
        if (android.os.Build.VERSION.SDK_INT >= 29) textCursorDrawable?.setTint(brand)
    }

    /** 分隔线以下那段小字（配对页的 .foot）用的顶边：只画上面一条线 */
    fun topRule(): Drawable {
        val l = android.graphics.drawable.LayerDrawable(arrayOf(shape(line, radius = 0), shape(card, radius = 0)))
        l.setLayerInset(1, 0, dp(1).coerceAtLeast(1), 0, 0)
        return l
    }
}
