# Example: two synced lamps

A complete example of what you can build with WLED and [WLED Audio Sync](../../): two small lamps that
act as one.

- **Mirrored:** change one lamp (touch button, web page or phone app) and the other copies it.
- **Touch button:**
  - tap = next light mode
  - double tap = next music mode
  - hold = on/off
- **Music modes** react to whatever the PC is playing, via WLED Audio Sync.
- **Private sync group,** so other WLED lights at home aren't affected, but can join in when you want.

[`presets.json`](presets.json) contains 9 light modes, 7 music modes and the button actions. It works on
any WLED device, whatever its number of LEDs, so it's a good starting point for your own setup.

```
                 audio data (WLED Audio Sync)
  PC ───────────────────────────────────────►  Lamp 1    Lamp 2
                                                 ▲          ▲
                                                 └──────────┘
                          WLED sync, group 2 (UDP 21324): mode, colour, brightness, on/off
```

## Hardware used

Adapt the pins and LED count to your build.

| Part | Connection |
|---|---|
| ESP32 dev board | USB for flashing/power |
| 12× WS2812 (NeoPixel) ring, GRB | data on **GPIO 27** |
| Touch sensor module (TTP223-style, output HIGH when touched) | **GPIO 17** |

## Setup (on each lamp)

### 1. Flash WLED
1. Connect the ESP32 with a USB **data** cable.
2. Open **https://install.wled.me** in **Chrome or Edge**.
3. Install the latest **stable** release and allow it to erase the device.
   - **No port shows up?** Install your board's USB driver (*CP210x* or *CH340*).
   - **On Linux,** use Chrome or Chromium, and add yourself to the serial group, then log in again:
     `sudo usermod -aG dialout $USER` (Arch: `uucp`).

### 2. Connect to WiFi
1. Join the WiFi **`WLED-AP`** (password **`wled1234`**). If no setup page opens, go to **http://4.3.2.1**.
2. **WiFi Settings:**
   - Enter your home WiFi.
   - Set **mDNS address** to `lamp1` / `lamp2`: lowercase letters, digits and hyphens only.
   - Tick **Disable WiFi sleep**.
3. Click **Save & Connect**. The lamp restarts and is then at `http://lamp1.local`.

### 3. Config
- **LED Preferences:**
  - LED output: WS281x, color order **GRB**, length **12**, GPIO **27**.
  - Set the brightness limiter to match your power supply.
  - **Button 0:** GPIO **17**, type **Push inverted** (an active-HIGH touch sensor).
- **Sync Interfaces → WLED Broadcast:**
  - UDP port **21324**. Tick **Send** and **Receive** for sync group **2** only (untick group 1).
    - Every WLED device listens on group 1 by default. A private group keeps the lamps' changes from
      spilling onto your other WLED lights, and theirs onto the lamps.
  - **Receive:** tick brightness, color, effects and palette.
  - Tick **Send notifications on direct change** and **Send notifications on button press**.
  - Make sure the **Sync** button at the top of the main page is lit; it's the master "send" switch.
- **Usermods → AudioReactive:**
  - Tick **Enabled**.
  - Set **Sync → Mode: Receive**, port **11988**.
- **Security & Updates → Restore presets:** upload [`presets.json`](presets.json).
- **Time & Macros → Button actions,** button 0: **Short `100`**, **Long `102`**, **Double `101`**.

**Shortcut for the second lamp:**
1. Back up the first lamp's config (**Security & Updates → Backup configuration**).
2. Restore it on the second lamp, along with the presets.
3. Change the second lamp's mDNS name.

Keep the backup file private: it contains your WiFi's name, though not the password.

## The presets

**Light modes**: tap cycles through them.

| ID | Mode | Look |
|---|---|---|
| 1 | Ambient | Slow sunset color waves |
| 2 | Candle | Warm flickering candle |
| 3 | Ocean | Rolling blue-green waves (*Pacifica*) |
| 4 | Aurora | Drifting northern-lights curtains |
| 5 | Rainbow | Slowly rotating rainbow |
| 6 | Fireplace | Orange-red flames |
| 7 | Twinkle | Soft pastel twinkles |
| 8 | Lava lamp | Slow blobs of red and orange |
| 9 | Night | Very dim warm glow |

**Music modes**: double tap cycles through them. They need WLED Audio Sync running on a PC.

| ID | Mode | Look |
|---|---|---|
| 21 | Freqwave | Color follows pitch, brightness follows volume |
| 22 | Party ripples | Colored ripples burst out on each beat |
| 23 | Bass meter | Bars that grow from the center with the volume |
| 24 | DJ light | Pulsing waves with color by frequency |
| 25 | Puddles | Splashes of color on peaks |
| 26 | Fire beat | Flames that flare with the music |
| 27 | Gravcentric | Bars that grow out from the center with the volume, with a falling peak dot (tuned for a 12-LED ring: sensitivity 180, rate of fall 128) |

*Party ripples* and *Puddles* place their splashes randomly, so each lamp shows its own. The others look the
same on both lamps.

**Button presets**: used by the button actions.

| ID | Preset | What it does |
|---|---|---|
| 100 | Next light mode | Cycles 1 → 9 (`P1=1&P2=9&PL=~`) |
| 101 | Next music mode | Cycles 21 → 27 (`P1=21&P2=27&PL=~`); from a light mode it starts at 21 |
| 102 | On/off | Toggles power |
| 103 | Brightness step | +64 brightness, wraps to dim (spare action) |

- **To change a mode,** set it up how you like on the main page, then re-save over its ID with
  **"Use current state"** ticked.
- **To add a mode:**
  1. Save it as 10–20 (light) or 28–40 (music).
  2. Widen the range in preset **100** or **101**, e.g. `P1=1&P2=10&PL=~`.
- **Keep the button presets in the `P1=…&P2=…&PL=~` form.** The JSON form `{"ps":"1~9~"}` also cycles,
  but WLED then doesn't send the new mode to the other lamp, so the lamps drift apart.

## Button cheat sheet

| Touch | Action |
|---|---|
| **Tap** | Next light mode |
| **Double tap** | Next music mode |
| **Hold ~1 s** | On / off |
| Hold **5 s**, then let go | WiFi setup hotspot `WLED-AP` |
| Hold **10 s**, then let go | ⚠ **Factory reset** (WLED's built-in behaviour for button 0) |

Each lamp remembers its own place in the cycle. If you tap lamp 1 and then lamp 2, lamp 2 continues from
where *it* last was, so a tap occasionally seems to repeat a mode. Just tap again.

## Letting other WLED lights join

- **Music:** enable AudioReactive with **Sync mode: Receive** on that device; see the
  [main README](../../#set-up-your-wled-lights).
- **Follow the lamps' modes:**
  1. On that device, under Sync Interfaces, tick **Receive** for group **2**.
  2. Untick receiving segment options/bounds.
  3. Its **Sync** button then toggles following on and off.
  4. For automation, use presets `{"udpn":{"recv":true}}` / `{"udpn":{"recv":false}}`.

## Troubleshooting

| Problem | Fix |
|---|---|
| Lamps don't mirror each other | Same sync group (2) and port (21324) on both, Send and Receive ticked, the **Sync** button lit, same WLED version |
| Other WLED lights copy the lamps (or the other way round) | They share a sync group. Keep the lamps on group 2 and other devices on their own group |
| Button does nothing, or triggers by itself | Wrong button type: try **Push** instead of **Push inverted** |
| Music modes don't react | See the end of [Using it](../../#using-it) in the main README |
