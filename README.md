# Scrawl
Personal Notes App

The web version starts at http://localhost:8080/ after the following commands:
```bash
git clone https://github.com/andrewbrdk/Scrawl
cd ./Scrawl
npm ci
npm run build
go build
./scrawl
```

The desktop version ([Wails v3](https://v3alpha.wails.io/)) needs `wails3`, and on Linux `libgtk-3-dev` and `libwebkit2gtk-4.1-dev`:
```bash
npm ci
npm run bindings        # regenerates ./bindings, run after changing ScrawlService in main_wails.go
npm run build:wails
go build -tags wails,gtk3 -o scrawl-desktop
./scrawl-desktop
```
The `gtk3` tag is Linux only. Wails' default GTK4 backend needs GTK 4.10+ (`GtkFileDialog`), which older distros such as Debian 12 don't have. On macOS and Windows use `-tags wails`.
  
Env. variables
```bash
SCRAWL_PASSWORD              # Password for the web UI
SCRAWL_PORT                  # HTTP port (default: `8080`)
SCRAWL_DBFILE                # Sqlite database file  
```
