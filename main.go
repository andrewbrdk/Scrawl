//go:build web || android

package main

// main is shared; run() is defined by web.go (web) or wails.go (android).
func main() {
	initApp()
	run()
}
