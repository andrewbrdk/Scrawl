# Scrawl
Personal Notes App

The web version starts at http://localhost:8080/ after the following commands:
```bash
git clone https://github.com/andrewbrdk/Scrawl
cd ./Scrawl
npm ci --prefix frontend
npm run dist:web --prefix frontend
go build -tags web -o bin/scrawl-web
./bin/scrawl-web
```

The Android version ([Wails v3](https://v3alpha.wails.io/)) is built with `wails3` and the Taskfiles in `build/`:
```bash
wails3 init -n scrawl -t vanilla -d /tmp/scaffold
mkdir -p build/android
cp -rn /tmp/scaffold/scrawl/build/android/. build/android/
npm ci --prefix frontend
wails3 task android:run         # build, install and launch on an emulator
wails3 task android:run:device  # same, on a USB device
wails3 task android:package     # release APK -> bin/scrawl.apk
```
Android needs `ANDROID_HOME`, `ANDROID_NDK_HOME` and a JDK (`wails3 task android:install:deps` checks them).
Go build tags: `web` builds `web.go`, `android` builds `wails.go`.
  
Env. variables
```bash
SCRAWL_PASSWORD              # Password for the web UI
SCRAWL_PORT                  # HTTP port (default: `8080`)
SCRAWL_DBFILE                # Sqlite database file  
```
