package main

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

//go:embed index.html style.css
var embedded embed.FS

var jwtSecretKey []byte

var infoLog *log.Logger
var errorLog *log.Logger

var CONF Config

type Config struct {
	port     string
	pagesDir string
	password string
}

func main() {
	initConfig()
	jwtSecretKey = generateRandomKey(32)
	infoLog = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	errorLog = log.New(os.Stdout, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	httpServer()
}

func initConfig() {
	CONF.port = ":8080"
	CONF.pagesDir = "./pages"
	CONF.password = ""
	if port := os.Getenv("SCRAWL_PORT"); port != "" {
		CONF.port = ":" + port
	}
	if pagesDir := os.Getenv("SCRAWL_PAGES"); pagesDir != "" {
		CONF.pagesDir = pagesDir
	}
	CONF.password = os.Getenv("SCRAWL_PASSWORD")
}

func generateRandomKey(size int) []byte {
	key := make([]byte, size)
	_, err := rand.Read(key)
	if err != nil {
		errorLog.Printf("Failed to generate a JWT secret key. Aborting.")
		os.Exit(1)
	}
	return key
}

func loadPages() ([]string, string) {
	files, err := ioutil.ReadDir(CONF.pagesDir)
	if err != nil {
		errorLog.Printf("Pages directory missing: %s", CONF.pagesDir)
		return nil, ""
	}
	var pages []string
	selected := ""
	for _, f := range files {
		if filepath.Ext(f.Name()) == ".json" {
			name := f.Name()[:len(f.Name())-len(".json")]
			pages = append(pages, name)
		}
	}
	if len(pages) > 0 {
		selected = pages[0]
	}
	return pages, selected
}

type Response struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

func httpServer() {
	http.HandleFunc("/", httpIndex)
	http.Handle("/style.css", http.FileServer(http.FS(embedded)))
	http.HandleFunc("/login", httpLogin)
	http.HandleFunc("/pages", httpPages)
	http.HandleFunc("/page", httpPage)
	http.HandleFunc("/save", httpSavePage)
	http.HandleFunc("/create", httpCreatePage)
	log.Fatal(http.ListenAndServe(CONF.port, nil))
}

func httpIndex(w http.ResponseWriter, r *http.Request) {
	data, err := embedded.ReadFile("index.html")
	if err != nil {
		http.Error(w, "Error loading the page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html")
	w.Write(data)
}

func httpLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Invalid request method", http.StatusMethodNotAllowed)
		return
	}
	var creds struct {
		Password string `json:"password"`
	}
	err := json.NewDecoder(r.Body).Decode(&creds)
	if err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}
	if creds.Password != CONF.password {
		http.Error(w, "Invalid credentials", http.StatusUnauthorized)
		return
	}
	expirationTime := time.Now().Add(15 * time.Minute)
	claims := jwt.RegisteredClaims{
		ExpiresAt: jwt.NewNumericDate(expirationTime),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(jwtSecretKey)
	if err != nil {
		http.Error(w, "Failed to create token", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "token",
		Value:    tokenString,
		Expires:  expirationTime,
		HttpOnly: true,
	})
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Login successful!"))
}

func httpCheckAuth(w http.ResponseWriter, r *http.Request) (error, int, string) {
	if CONF.password == "" {
		return nil, http.StatusOK, "Ok"
	}
	cookie, err := r.Cookie("token")
	if err != nil {
		if err == http.ErrNoCookie {
			return err, http.StatusUnauthorized, "Unauthorized"
		}
		return err, http.StatusBadRequest, "Bad request"
	}
	tokenStr := cookie.Value
	token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
		return jwtSecretKey, nil
	})
	if err != nil || !token.Valid {
		return err, http.StatusUnauthorized, "Unauthorized"
	}
	//todo: prolong token
	return nil, http.StatusOK, "Ok"
}

func httpPages(w http.ResponseWriter, r *http.Request) {
	err, code, msg := httpCheckAuth(w, r)
	if err != nil {
		http.Error(w, msg, code)
		return
	}
	pages, selected := loadPages()
	resp := struct {
		Pages    []string `json:"pages"`
		Selected string   `json:"selected"`
	}{
		Pages:    pages,
		Selected: selected,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func httpPage(w http.ResponseWriter, r *http.Request) {
	err, code, msg := httpCheckAuth(w, r)
	if err != nil {
		http.Error(w, msg, code)
		return
	}
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "Missing name", 400)
		return
	}
	path := filepath.Join(CONF.pagesDir, name+".json")
	delta, err := ioutil.ReadFile(path)
	if err != nil {
		http.Error(w, "Page not found", 404)
		return
	}
	resp := struct {
		Name  string          `json:"name"`
		Delta json.RawMessage `json:"delta"`
	}{Name: name, Delta: json.RawMessage(delta)}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func httpSavePage(w http.ResponseWriter, r *http.Request) {
	err, code, msg := httpCheckAuth(w, r)
	if err != nil {
		http.Error(w, msg, code)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name  string          `json:"name"`
		Delta json.RawMessage `json:"delta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	path := filepath.Join(CONF.pagesDir, req.Name+".json")
	if err := ioutil.WriteFile(path, []byte(req.Delta), 0644); err != nil {
		http.Error(w, "Failed to save page", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func httpCreatePage(w http.ResponseWriter, r *http.Request) {
	err, code, msg := httpCheckAuth(w, r)
	if err != nil {
		http.Error(w, msg, code)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", 405)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}
	if req.Name == "" {
		http.Error(w, "Missing page name", 400)
		return
	}

	filename := filepath.Join(CONF.pagesDir, req.Name+".json")
	if _, err := os.Stat(filename); err == nil {
		http.Error(w, "Page already exists", 409)
		return
	}

	empty := []byte(`{"ops":[{"insert":"\n"}]}`)
	if err := os.WriteFile(filename, empty, 0644); err != nil {
		http.Error(w, "Failed to write page", 500)
		return
	}

	pages, _ := loadPages()
	resp := struct {
		Pages []string `json:"pages"`
	}{
		Pages: pages,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
