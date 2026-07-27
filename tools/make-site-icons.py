"""Derive the site's favicon, touch icon and Open Graph card from the app icon.

Run from the repo root:  python tools/make-site-icons.py

gui/build/appicon.png is the single source of truth for the mark - the same art
as docs/logo.svg. Pillow is the only dependency.
"""

from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parent.parent
DOCS = ROOT / "docs"
FONTS = Path("C:/Windows/Fonts")

# Straight from docs/index.html's light theme.
PAPER = "#F4F6F9"
LINE = "#E3E7ED"
INK = "#171A21"
INK_2 = "#3E4552"
MUTED = "#6B7482"
ACCENT = "#E07C15"
TILE = "#141413"  # the logo's own backing colour


def mark(size, opaque=False):
    """The app icon at `size` px. `opaque` fills the squircle's transparent
    corners with the tile colour, which is what iOS wants - it masks its own."""
    src = Image.open(ROOT / "gui" / "build" / "appicon.png").convert("RGBA")
    src = src.resize((size, size), Image.LANCZOS)
    if not opaque:
        return src
    bg = Image.new("RGB", (size, size), TILE)
    bg.paste(src, (0, 0), src)
    return bg


def font(name, size):
    return ImageFont.truetype(str(FONTS / name), size)


def make_og():
    img = Image.new("RGB", (1200, 630), PAPER)
    d = ImageDraw.Draw(img)
    d.rectangle([36, 36, 1163, 593], outline=LINE, width=2)

    logo = mark(112)
    img.paste(logo, (96, 84), logo)

    d.text((96, 320), "shape", font=font("CascadiaCode-Bold.ttf", 88), fill=INK, anchor="ls")
    d.rectangle([96, 352, 192, 357], fill=ACCENT)
    d.text(
        (96, 428),
        "See the shape of any data file.",
        font=font("seguisb.ttf", 46),
        fill=INK,
        anchor="ls",
    )
    d.text(
        (96, 478),
        "JSON, CSV, Parquet, SQLite - and it writes the jq and SQL for you.",
        font=font("segoeui.ttf", 30),
        fill=INK_2,
        anchor="ls",
    )
    d.text((96, 556), "Free for noncommercial use.", font=font("segoeui.ttf", 26), fill=MUTED, anchor="ls")
    d.text(
        (1104, 556),
        "hoijun-kim.github.io/shape",
        font=font("CascadiaCode-Regular.ttf", 24),
        fill=MUTED,
        anchor="rs",
    )
    return img


def main():
    mark(64).save(DOCS / "favicon.ico", sizes=[(16, 16), (32, 32), (48, 48)])
    mark(180, opaque=True).save(DOCS / "apple-touch-icon.png")
    make_og().save(DOCS / "og.png", optimize=True)
    print("wrote docs/favicon.ico, docs/apple-touch-icon.png, docs/og.png")


if __name__ == "__main__":
    main()
