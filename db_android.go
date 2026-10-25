//go:build android

package main

import "os"

// The working directory on Android is read-only, so keep the database in the
// app's private files dir. The package name matches APP_ID in build/android.
const androidFilesDir = "/data/data/com.wails.app/files"

func init() {
	os.MkdirAll(androidFilesDir, 0o700)
	defaultDBFile = androidFilesDir + "/scrawls.db"
}
