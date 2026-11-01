package main

import (
	"log"
	"os"
	"path/filepath"
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

func main() {
	initConfig()
	infoLog = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	errorLog = log.New(os.Stdout, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	SCRAWL.initDB()
	SCRAWL.loadNotebook()
	// run() is defined by web.go (web) or wails.go (android).
	run()
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

func defaultDBPath() string {
	if runtime.GOOS != "android" {
		return "./scrawls.db"
	}
	pkg := "com.scrawl.app"
	//todo: hardcode package name and remove?
	if b, err := os.ReadFile("/proc/self/cmdline"); err == nil {
		if p, _, _ := strings.Cut(string(b), "\x00"); p != "" {
			pkg = p
		}
	}
	dir := filepath.Join("/data/data", pkg, "files")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		log.Fatalf("creating db dir %s: %v", dir, err)
	}
	return filepath.Join(dir, "scrawls.db")
}
