package main

import (
	"log"
	"os"
	"runtime"
	"strings"
)

var infoLog *log.Logger
var errorLog *log.Logger

var CONF Config

type Config struct {
	port     string
	dbFile   string
	password string
}

// main is shared; run() is defined by web.go (web) or wails.go (android).
func main() {
	initApp()
	run()
}

func initApp() {
	initConfig()
	infoLog = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	errorLog = log.New(os.Stdout, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	SCRAWL.initDB()
	SCRAWL.loadNotebook()
}

// defaultDBPath is used unless SCRAWL_DBFILE is set.
func defaultDBPath() string {
	if runtime.GOOS == "android" {
		// The working directory is read-only on Android; use the app's private
		// files dir. The main process name is the package name.
		pkg := "com.wails.app"
		if b, err := os.ReadFile("/proc/self/cmdline"); err == nil {
			if name, _, _ := strings.Cut(string(b), "\x00"); name != "" {
				pkg = name
			}
		}
		dir := "/data/data/" + pkg + "/files"
		os.MkdirAll(dir, 0o700)
		return dir + "/scrawls.db"
	}
	return "./scrawls.db"
}

func initConfig() {
	CONF.port = ":8080"
	CONF.dbFile = defaultDBPath()
	CONF.password = ""
	if port := os.Getenv("SCRAWL_PORT"); port != "" {
		CONF.port = ":" + port
	}
	if dbFile := os.Getenv("SCRAWL_DBFILE"); dbFile != "" {
		CONF.dbFile = dbFile
	}
	CONF.password = os.Getenv("SCRAWL_PASSWORD")
}
