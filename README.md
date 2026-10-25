# Scrawl
Personal Notes App

The web version starts at http://localhost:8080/ after the following commands:
```bash
git clone https://github.com/andrewbrdk/Scrawl
cd ./Scrawl
npm ci
npm run build:web
./bin/scrawl-web
```

The desktop and Android versions ([Wails v3](https://v3alpha.wails.io/)) are built with `wails3` and the Taskfiles in `build/`. On Linux they need `libgtk-3-dev` and `libwebkit2gtk-4.1-dev`:
```bash
npm ci
wails3 build                    # Linux desktop -> bin/scrawl (bindings and frontend are built by the task)
wails3 package                  # AppImage / deb / rpm
wails3 task android:run         # build, install and launch on an emulator
wails3 task android:run:device  # same, on a USB device
wails3 task android:package     # release APK -> bin/scrawl.apk
```
Android needs `ANDROID_HOME`, `ANDROID_NDK_HOME` and a JDK (`wails3 task android:install:deps` checks them).
The Linux build uses GTK3 (`gtk3` build tag) because Wails' default GTK4 backend needs GTK 4.10+. Pass `EXTRA_TAGS=` to override it.
Web is the only target built with a Go tag (`-tags web`); everything else builds `main_wails.go`.
  
Env. variables
```bash
SCRAWL_PASSWORD              # Password for the web UI
SCRAWL_PORT                  # HTTP port (default: `8080`)
SCRAWL_DBFILE                # Sqlite database file  
```
