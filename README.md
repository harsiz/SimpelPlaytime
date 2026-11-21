<p align="center"><img src="assets/icon.png" width="96" alt="Simpel Playtime logo"></p>

# Simpel Playtime

(yes "Simpel" not "Simple")

A fully functioning playtime tracker for games and apps, made in Go with Fyne. It tracks playtime (e.g: hours played) across all your .exes from the get-go, runs on startup (minimised obviously) and is very lightweight. Ships as a singular .exe, no installer.

## What it does

- Tracks every game and app you open, zero setup. Games count while they run (like Steam), apps only count while you actually use them
- Detects actual games with an offline database of 400+ games (Minecraft, R6S, GTA, Roblox, osu! (lazer and stable combined into a singular playtime), Cyberpunk and loads more) plus anything in your Steam/Epic libraries or a game folder
- Press **Customise** to track any .exe, a shortcut (.lnk or .url, it grabs the exe from it under the hood, Steam and Epic shortcuts work too), a whole folder, or just pick something that's running rn
- Posters, banners and game info (year, genres, dev) from Steam, with Wikipedia and SteamGridDB as backup
- Gets the icon of every tracked game straight out of the .exe
- Stats: playtime graphs per day/week/month from 7 days up to all time (since inception), a year heatmap, a "when do you play" heatmap, streaks, top games, longest session, best day, night owl % and more
- Per game page with session history, a journal, manual time adding, merging, custom posters etc
- Local JSON API for quick python scripts or other stuff
- Settings for basically everything (idle pause, ignore list, track mode, notifications, break reminders...)
- Dark purple theme, lives in the tray, frees its memory while minimised

## Download

Grab `SimpelPlaytime.exe` from the latest [release](https://github.com/harsiz/SimpelPlaytime/releases) or from the newest run in the Actions tab (artifact `SimpelPlaytime-windows`). Put it anywhere and run it. That's it.

Want it portable? Make a folder called `data` next to the exe and everything gets stored in there instead of `%APPDATA%\SimpelPlaytime`.

## Building it yourself

You need Go 1.24+ and a C compiler (Fyne uses OpenGL), on Windows that's [MSYS2](https://www.msys2.org/) mingw-w64 gcc or TDM-GCC.

```
go build -ldflags "-H windowsgui -s -w" -o SimpelPlaytime.exe .
```

Run the tests with `go test ./...`. The `.syso` files in the root give the exe its icon and version info, remake them with `go-winres make --in winres/winres.json` if you change the icon (`go run ./tools/genicon` redraws it).

## Local API

On by default at `http://127.0.0.1:47123/api/v1` (only your own PC can reach it, you can add a token in settings). Add `?pretty=1` to anything for readable JSON.

```python
import requests

API = "http://127.0.0.1:47123/api/v1"

for game in requests.get(f"{API}/now-playing").json():
    print(f"playing {game['name']} for {game['session']} (total {game['total']})")

for game in requests.get(f"{API}/games?kind=game&limit=5").json():
    print(f"{game['name']:30} {game['totalHours']:8.1f}h")
```

Endpoints: `/status`, `/now-playing`, `/games`, `/games/{key or name}`, `/games/{key}/sessions`, `/games/{key}/daily`, `/sessions`, `/stats/summary`, `/stats/chart?range=7d|30d|90d|year|12m|all`, `/stats/top`, `/stats/heatmap`, `/export`, `/export.csv`, plus `POST /tracking/pause`, `/tracking/resume` and `/app/show`. Hit `/api/v1` and it lists them all.

## Flags

- `--minimized` start in the tray (the startup entry uses this)
- `--data <folder>` use another data folder
- `--headless` no window, just tracking + the API (handy for testing)
- `--no-api` skip the API for this run
- `--version`

## Where's my data

`%APPDATA%\SimpelPlaytime`, all plain files: `library.json`, `sessions.jsonl` (one session per line), `settings.json`, cached icons and posters. Running sessions get checkpointed every 30 seconds so a crash or power cut loses basically nothing. You can export/import backups or a CSV from settings.

## Why no hash comparisons?

I thought about it but exe hashes change every time a game updates, so a hash list would be outdated in like a week. Instead it fingerprints by exe name + folder + window title (that's how Minecraft gets detected even though it's just `javaw.exe`), checks Steam/Epic manifests, and treats anything in a game library folder as a game.

## License

GPL-3.0, see [LICENSE](LICENSE).
