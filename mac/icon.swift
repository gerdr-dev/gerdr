// 画 App 图标（和 web/public/icon.svg 同一个图形）：swift mac/icon.swift out.png
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
rr(sc(0), sc(0), sl(64), sl(64), sl(14), NSColor(red: 0.11, green: 0.11, blue: 0.12, alpha: 1))
rr(sc(9), sc(12), sl(21), sl(40), sl(4), NSColor(red: 0.23, green: 0.51, blue: 0.97, alpha: 1))
rr(sc(34), sc(12), sl(21), sl(18), sl(4), NSColor(red: 0.9, green: 0.9, blue: 0.9, alpha: 1))
rr(sc(34), sc(34), sl(21), sl(18), sl(4), NSColor(red: 0.85, green: 0.47, blue: 0.29, alpha: 1))
img.unlockFocus()
let rep = NSBitmapImageRep(data: img.tiffRepresentation!)!
try! rep.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: CommandLine.arguments[1]))
