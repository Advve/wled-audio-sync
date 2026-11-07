# Example: pole lamp

A tall standing lamp with a single vertical strip of 60 visible LEDs. It's a companion to the
[synced lamps example](../synced-lamps/), but it runs on its own. Its music modes use the height of the
pole: VU meters that rise from the base, a spectrum waterfall, and particles that shoot up.

- **12 light modes and 19 music modes,** tuned for a 1D strip of about 60 LEDs.
- **Two playlists:** *Music mix* switches music modes every 30 s, and *Light mix* switches light modes
  every 10 min.
- **Hidden LEDs skipped:** the first 9 LEDs sit inside the base, so WLED leaves them dark and treats LED 10
  as the bottom of the pole.
- **Independent:** it doesn't follow the ring lamps' sync group, but reacts to the same PC music.

[`presets.json`](presets.json) contains the modes and playlists.

```
             audio data (WLED Audio Sync)
  PC ──────────────────────────────────────►  Pole lamp  (and any other AudioReactive lights)
```

## Hardware used

| Part | Connection |
|---|---|
| ESP32 dev board | USB for flashing/power |
| WS2812B strip, GRB: 69 LEDs, 9 hidden in the base + 60 up the pole, LED 1 at the bottom | data on **GPIO 2** |

No button: control it from the web page, the WLED app or a playlist.

## Setup

1. **Flash WLED and connect to WiFi:** as in the [synced lamps example](../synced-lamps/#setup-on-each-lamp).
   Set the **mDNS address** to `polelamp`, so the lamp is at `http://polelamp.local`.
2. **LED Preferences:**
   - LED output: WS281x, color order **GRB**, GPIO **2**.
   - Length **60** and **Skip first LEDs 9**.
     - In WLED, the length doesn't include the skipped LEDs: 60 + 9 = the 69 on the strip.
     - The hidden 9 stay dark, and the segment covers 0–60, the visible part.
   - Set the brightness limiter to match your power supply. 60 LEDs at full white draw up to ~3.6 A at 5 V.
   - If your LED 1 is at the **top**, tick **Reversed** on the LED output, so the meters still rise from
     the bottom.
3. **Usermods → AudioReactive:**
   - Tick **Enabled**.
   - Set **Sync → Mode: Receive**, port **11988**, or the port of the channel picked in the tray menu.
4. **WiFi Setup:** tick **Disable WiFi sleep**.
5. **Security & Updates → Restore presets:** upload [`presets.json`](presets.json).

Leave **Sync Interfaces → WLED Broadcast** at its defaults (Send off), so the pole lamp stays independent.
To let it follow the ring lamps, tick **Receive** for group **2**. Their modes are tuned for 12 LEDs,
though.

## The presets

**Light modes**

| ID | Mode | Look |
|---|---|---|
| 1 | Ambient | Slow sunset color waves (quick button ☀) |
| 2 | Candles | Several warm candles flickering independently along the pole |
| 3 | Ocean | Rolling blue-green waves (*Pacifica*) |
| 4 | Aurora | Drifting northern-lights curtains |
| 5 | Rainbow | Slowly rotating rainbow |
| 6 | Fireplace | Flames rising from the base (*Fire 2012*) |
| 7 | Twinkle | Soft pastel twinkles (*Twinklefox*) |
| 8 | Lava lamp | Slow blobs of red and orange |
| 9 | Shooting stars | Rainbow meteors running up the pole |
| 10 | Spotlights | Soft beams sweeping up and down (*Dancing Shadows*) |
| 11 | Pride | Shifting, layered rainbow waves |
| 12 | Night | Very dim warm glow (quick button ☾) |

**Music modes**: they need WLED Audio Sync running on a PC.

| ID | Mode | Look |
|---|---|---|
| 21 | VU meter | Rainbow bar rising from the base with the volume, falling peak dot (*Gravimeter*, quick button ♪) |
| 22 | Center pulse | Bar growing up and down from the middle of the pole (*Gravcenter*) |
| 23 | Gravcentric | Like Center pulse, but one color that shifts with the music |
| 24 | Pitch meter | VU meter whose color follows the pitch (*Gravfreq*) |
| 25 | Noise meter | Shimmering bar that rises with the volume |
| 26 | Midnoise | Shimmering glow that spreads from the middle |
| 27 | Waterfall | The melody's colors scroll up the pole, with a flash on peaks |
| 28 | Freqwave | Color follows pitch, brightness follows volume |
| 29 | Party ripples | Colored ripples burst out on each beat |
| 30 | Puddles | Splashes of color on peaks |
| 31 | Juggles | Dots bouncing along the pole, faster with the music |
| 32 | Matripix | Volume-colored pixels scrolling up |
| 33 | Plasmoid | Plasma that brightens with the volume |
| 34 | Rocktaves | Color by musical note, position by octave |
| 35 | DJ light | Pulsing waves with color by frequency |
| 36 | Fire beat | Flames that flare with the music (*Noisefire*) |
| 37 | Particle spectrum | Particles shoot up, one stream per frequency band (*PS GEQ 1D*) |
| 38 | Sonic boom | Particle bursts on the beat (*PS Sonic Boom*) |
| 39 | Blurz | Colored flashes per frequency, blurred together |

**Playlists**

| ID | Playlist | What it does |
|---|---|---|
| 50 | Music mix | Cycles all music modes, 30 s each, in a mixed order (quick button ♫) |
| 51 | Light mix | Cycles the light modes (not Night), 10 min each |

- **To change a mode,** set it up how you like on the main page, then re-save over its ID with
  **"Use current state"** ticked.
- **To change a playlist's timing,** edit preset 50 or 51 on the main page. Durations are in seconds there
  (`dur` is in tenths of a second in the JSON).
- The presets don't set segment bounds, so they keep the skipped LEDs dark and work on other LED counts
  too.
- The particle effects (37, 38) need WLED 0.15 or later.

## Troubleshooting

| Problem | Fix |
|---|---|
| The bottom 9 LEDs light up, or the top 9 stay dark | Length must be **60** (visible LEDs) with **Skip 9**, not 69 |
| Meters fall from the top instead of rising | LED 1 is at the top: tick **Reversed** on the LED output |
| Music modes don't react | AudioReactive port must match the tray menu's channel; see [Using it](../../#using-it) |
| The ring lamps change the pole lamp's mode | Untick **Receive** for group 2 under Sync Interfaces |
