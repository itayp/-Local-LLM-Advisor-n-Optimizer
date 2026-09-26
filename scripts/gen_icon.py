#!/usr/bin/env python3
"""Generates the app's icon assets: a simple, original mark (three
ascending bars on a rounded square — "measured performance"), never a
borrowed logo or character.

Produces:
  packaging/icon/master.png      1024x1024, full color — the source every
                                  per-OS installer icon (.icns/.ico/hicolor)
                                  is converted from during packaging.
  data/icon/tray.png              32x32, full color — embedded in the Go
                                  binary for the Windows/Linux tray icon.
  data/icon/tray_template.png     32x32, monochrome silhouette with alpha —
                                  embedded for macOS's "template image" tray
                                  icon (SetTemplateIcon), which the OS
                                  recolors for light/dark menu bars.
  packaging/windows/icon.ico      multi-size (16-256px) — setup.iss's
                                  installer/uninstaller icon and the
                                  installed advisor.exe's own icon
                                  (embedded into the binary by a resource
                                  step in the Windows CI job, not here).
  packaging/linux/icons/hicolor/  16, 32, 48, 64, 128, 256, 512px PNGs
    <N>x<N>/apps/                 under the freedesktop hicolor theme
    local-llm-advisor.png         layout — what the .deb (nfpm) and the
                                  AppImage (build_appimage.sh) both install
                                  so desktop environments can find an icon
                                  at whatever size they ask for.

.icns (macOS) is *not* produced here — build_app.sh converts master.png at
package time with iconutil, a macOS-only tool this script cannot depend on.

Run with: python3 scripts/gen_icon.py
"""
from PIL import Image, ImageDraw
import math
import os

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

ACCENT = (37, 99, 224, 255)  # a plain, brand-neutral blue
WHITE = (255, 255, 255, 255)


def rounded_square(size, radius_ratio, fill):
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    r = int(size * radius_ratio)
    d.rounded_rectangle([(0, 0), (size - 1, size - 1)], radius=r, fill=fill)
    return img


def draw_bars(img, size, color, pad_ratio=0.30):
    """Three ascending bars, centered, like a small bar chart / speedometer
    reading — simple enough to stay legible at 16-32px."""
    d = ImageDraw.Draw(img)
    pad = size * pad_ratio
    usable = size - 2 * pad
    n = 3
    gap = usable * 0.14
    bar_w = (usable - gap * (n - 1)) / n
    heights = [0.42, 0.68, 1.0]  # ascending, as a fraction of usable height
    base_y = size - pad
    for i, h in enumerate(heights):
        x0 = pad + i * (bar_w + gap)
        x1 = x0 + bar_w
        y0 = base_y - usable * h
        y1 = base_y
        radius = bar_w * 0.28
        d.rounded_rectangle([(x0, y0), (x1, y1)], radius=radius, fill=color)


def make_master(size=1024):
    img = rounded_square(size, 0.22, ACCENT)
    draw_bars(img, size, WHITE, pad_ratio=0.30)
    return img


def make_tray_color(size=32):
    # A simplified, bolder version for very small sizes: same shape, less
    # corner rounding so it doesn't dissolve into a blob at 32px.
    img = rounded_square(size, 0.18, ACCENT)
    draw_bars(img, size, WHITE, pad_ratio=0.22)
    return img


def make_tray_template(size=32):
    # macOS template images: a monochrome silhouette (black shapes, alpha
    # transparency) with NO background fill — the OS recolors it to match
    # the light/dark menu bar. Bars only, no rounded-square background.
    img = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    draw_bars(img, size, (0, 0, 0, 255), pad_ratio=0.14)
    return img


def save(img, rel_path):
    path = os.path.join(ROOT, rel_path)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    img.save(path)
    print("wrote", rel_path, img.size)


HICOLOR_SIZES = [16, 32, 48, 64, 128, 256, 512]
ICO_SIZES = [16, 32, 48, 64, 128, 256]


def save_hicolor(master):
    for size in HICOLOR_SIZES:
        resized = master.resize((size, size), Image.LANCZOS)
        save(resized, f"packaging/linux/icons/hicolor/{size}x{size}/apps/local-llm-advisor.png")


def save_ico(master):
    path = os.path.join(ROOT, "packaging/windows/icon.ico")
    os.makedirs(os.path.dirname(path), exist_ok=True)
    master.save(path, format="ICO", sizes=[(s, s) for s in ICO_SIZES])
    print("wrote", "packaging/windows/icon.ico", ICO_SIZES)


if __name__ == "__main__":
    master = make_master(1024)
    save(master, "packaging/icon/master.png")
    save(make_tray_color(32), "data/icon/tray.png")
    save(make_tray_template(32), "data/icon/tray_template.png")
    save_hicolor(master)
    save_ico(master)
