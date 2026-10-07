# -*- coding: utf-8 -*-
"""Picks design-token values that clear the WCAG AA text contrast threshold.

Used when fixing the light theme: converts OKLCH tokens to sRGB the same way a
browser does, then reports the contrast ratio for candidate values.
"""
import math


def oklch_to_srgb(lightness, chroma, hue):
    a = chroma * math.cos(math.radians(hue))
    b = chroma * math.sin(math.radians(hue))
    l_ = lightness + 0.3963377774 * a + 0.2158037573 * b
    m_ = lightness - 0.1055613458 * a - 0.0638541728 * b
    s_ = lightness - 0.0894841775 * a - 1.2914855480 * b
    l, m, s = l_**3, m_**3, s_**3
    r = 4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s
    g = -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s
    blue = -0.0041960863 * l - 0.7034186147 * m + 1.7076147010 * s

    def encode(value):
        value = max(0.0, min(1.0, value))
        return 12.92 * value if value <= 0.0031308 else 1.055 * value ** (1 / 2.4) - 0.055

    return tuple(int(round(encode(channel) * 255)) for channel in (r, g, blue))


def _linear(channel):
    channel = channel / 255
    return channel / 12.92 if channel <= 0.04045 else ((channel + 0.055) / 1.055) ** 2.4


def luminance(rgb):
    r, g, b = (_linear(channel) for channel in rgb)
    return 0.2126 * r + 0.7152 * g + 0.0722 * b


def contrast(first, second):
    a, b = luminance(first), luminance(second)
    high, low = max(a, b), min(a, b)
    return (high + 0.05) / (low + 0.05)


WHITE = (255, 255, 255)
LIGHT_BACKGROUND = oklch_to_srgb(0.982, 0, 0)
LIGHT_DEFAULT = oklch_to_srgb(0.922, 0, 0)

print("light --muted candidates (needs >= 4.5 on both --background and --default)")
current = oklch_to_srgb(0.556, 0, 0)
print(
    f"  current oklch(0.556 0 0) rgb{current}: on background "
    f"{contrast(current, LIGHT_BACKGROUND):.3f}, on default {contrast(current, LIGHT_DEFAULT):.3f}"
)
for lightness in (0.50, 0.505, 0.51, 0.515, 0.52):
    muted = oklch_to_srgb(lightness, 0, 0)
    print(
        f"  oklch({lightness} 0 0) rgb{muted}: on background "
        f"{contrast(muted, LIGHT_BACKGROUND):.3f}, on default {contrast(muted, LIGHT_DEFAULT):.3f}"
    )

print("\n--danger candidates (white text on it, needs >= 4.5)")
danger_current = oklch_to_srgb(0.637, 0.208, 25)
print(
    f"  current oklch(0.637 0.208 25) rgb{danger_current}: white on it "
    f"{contrast(WHITE, danger_current):.3f}"
)
for lightness in (0.60, 0.58, 0.56, 0.545, 0.53):
    danger = oklch_to_srgb(lightness, 0.208, 25)
    print(f"  oklch({lightness} 0.208 25) rgb{danger}: white on it {contrast(WHITE, danger):.3f}")

print("\nglass panels in light mode (dark text over them)")
light_foreground = oklch_to_srgb(0.145, 0, 0)
candidate = oklch_to_srgb(1.0, 0, 0)
print(f"  white panel rgb{candidate}: text contrast {contrast(light_foreground, candidate):.2f}")
