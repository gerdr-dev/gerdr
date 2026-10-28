// 画 App 图标（和 ui/public/icon.svg 同一个图形）：swift desktop/icon.swift out.png（make icon）
// 图形：一条在跑的蓝陨石色边境牧羊犬（像素画）。herdr 是牧人、logo 是羊，gerdr 是帮他看住一群 agent 的狗。
// 像素图和 ui/public/icon.svg 是同一份，改一处要两处一起改。
import AppKit

let size: CGFloat = 1024
let img = NSImage(size: NSSize(width: size, height: size))
img.lockFocus()
func rr(_ x: CGFloat, _ y: CGFloat, _ w: CGFloat, _ h: CGFloat, _ r: CGFloat, _ c: NSColor) {
    c.setFill()
    // SVG 的 y 向下，AppKit 的 y 向上
    NSBezierPath(roundedRect: NSRect(x: x, y: size - y - h, width: w, height: h), xRadius: r, yRadius: r).fill()
}
let s = size / 64
// macOS 图标的网格：内容区约占 824/1024，四周留白。
let inset: CGFloat = 100
let k = (size - inset * 2) / size
func sc(_ v: CGFloat) -> CGFloat { inset + v * s * k }
func sl(_ v: CGFloat) -> CGFloat { v * s * k }

let colors: [Character: NSColor] = [
    "B": NSColor(red: 0.227, green: 0.510, blue: 0.969, alpha: 1), // #3a82f7
    "W": NSColor(red: 0.902, green: 0.902, blue: 0.902, alpha: 1), // #e6e6e6
    "O": NSColor(red: 0.851, green: 0.467, blue: 0.290, alpha: 1), // #d9774a
    "K": NSColor(red: 0.11, green: 0.11, blue: 0.12, alpha: 1),    // 眼睛 / 鼻子，和底色一样
]
// 16×11 格，每格 3（64 的坐标系里），左上角在 (8, 15)
let dog = [
    "..........B.B...",
    "..........BBB...",
    ".........BBKBW..",
    "B........BBBBWWK",
    "BB......OBBBWWW.",
    ".BB...BBOBBWW...",
    "..BBBBBBBBBWW...",
    "..BBBBBBBBWWW...",
    "..BWWBBBBBWW....",
    "..B..B....B..B..",
    ".B....B..B....B.",
]

rr(sc(0), sc(0), sl(64), sl(64), sl(14), colors["K"]!)
for (r, row) in dog.enumerated() {
    for (c, ch) in row.enumerated() {
        guard let col = colors[ch] else { continue }
        // 每格往外多画一点点，免得缩放后格子之间露出底色的细缝
        rr(sc(8 + CGFloat(c) * 3) - 0.5, sc(15 + CGFloat(r) * 3) - 0.5, sl(3) + 1, sl(3) + 1, 0, col)
    }
}
img.unlockFocus()
let rep = NSBitmapImageRep(data: img.tiffRepresentation!)!
try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
