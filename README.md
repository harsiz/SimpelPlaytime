# Simpel Playtime

Just a *simpel* playtime counter written in Go.

It tracks how long you play your games (and use your apps) across all your .exes from the get-go, starts with Windows minimised in the tray and stays lightweight. It knows 490+ games offline (Minecraft, R6S, GTA, Roblox, osu! with lazer and stable combined, Cyberpunk and loads more), finds your Steam and Epic games, grabs posters and icons, and has a stats page with graphs, heatmaps and streaks. Something not getting picked up? Press *Customise* and pick the .exe or a shortcut.

Honestly, you can just get the binary from Releases, otherwise you can just `go build .` once git cloned (Fyne needs a C compiler, and add `-ldflags "-H windowsgui"` so no console pops up).

There's also a local API at `http://127.0.0.1:47123/api/v1` if you wanna use your playtime in python scripts or other stuff:

```python
import requests

for game in requests.get("http://127.0.0.1:47123/api/v1/games?kind=game&limit=5").json():
    print(game["name"], game["total"])
```

Your data lives in `%APPDATA%\SimpelPlaytime` as plain files. Make a `data` folder next to the exe if you want it portable.
