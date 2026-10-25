//go:build !wails

package main

import (
	"embed"
	"io/fs"
	"log"
	"net/http"
)

//go:embed dist-web
var embedded embed.FS

func main() {
	jwtSecretKey = generateRandomKey(32)
	initApp()

	dist, err := fs.Sub(embedded, "dist-web")
	if err != nil {
		log.Fatal(err)
	}
	http.Handle("/", http.FileServer(http.FS(dist)))
	http.HandleFunc("/login", httpLogin)
	http.HandleFunc("/pages", httpPages)
	http.HandleFunc("/page", httpPage)
	http.HandleFunc("/save", httpSavePage)
	http.HandleFunc("/create", httpCreatePage)
	http.HandleFunc("/delete", httpDeletePage)
	http.HandleFunc("/rename", httpRenamePage)
	http.HandleFunc("/move", httpMovePage)
	log.Fatal(http.ListenAndServe(CONF.port, nil))
}
