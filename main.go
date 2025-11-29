package main

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
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

type Notebook struct {
	Name  string     `json:"name"`
	Pages []PageNode `json:"pages"`
	//todo: []*Page
}

type PageNode struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	File     string     `json:"file"` //todo: save, don't export
	Children []PageNode `json:"children"`
	//todo: []*Page
}

func main() {
	initConfig()
	jwtSecretKey = generateRandomKey(32)
	infoLog = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	errorLog = log.New(os.Stdout, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	//todo: load notebooks, store in memory
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

func loadNotebook() Notebook {
	if _, err := os.Stat(CONF.pagesDir); err != nil {
		infoLog.Printf("Pages directory '%s' missing. Creating.", CONF.pagesDir)
		if mkErr := os.MkdirAll(CONF.pagesDir, 0755); mkErr != nil {
			errorLog.Printf("Failed to create pages directory: %s", CONF.pagesDir)
			panic("Failed to create pages directory")
		}
		infoLog.Printf("Created pages directory: %s", CONF.pagesDir)
	}

	structurePath := filepath.Join(CONF.pagesDir, "structure.json")
	data, err := os.ReadFile(structurePath)
	if err != nil {
		infoLog.Printf("Creating new empty notebook")
		nb := Notebook{
			Name:  "Notebook",
			Pages: []PageNode{},
		}
		saveNotebook(nb)
		return nb
	}

	var nb Notebook
	json.Unmarshal(data, &nb)
	return nb
}

func saveNotebook(nb Notebook) {
	structurePath := filepath.Join(CONF.pagesDir, "structure.json")
	b, _ := json.MarshalIndent(nb, "", "  ")
	os.WriteFile(structurePath, b, 0644)
}

func generateID() string {
	//todo: use other method; check overlap with existing
	b := make([]byte, 8)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func (nb *Notebook) FindPage(id string) *PageNode {
	for i := range nb.Pages {
		p := nb.Pages[i].FindPage(id)
		if p != nil {
			return p
		}
	}
	return nil
}

func (p *PageNode) FindPage(id string) *PageNode {
	if p.ID == id {
		return p
	}
	for i := range p.Children {
		child := p.Children[i].FindPage(id)
		if child != nil {
			return child
		}
	}
	return nil
}

func (p *PageNode) ReadPage() json.RawMessage {
	path := filepath.Join(CONF.pagesDir, p.File)
	infoLog.Printf("Reading %s", path)
	delta, err := ioutil.ReadFile(path)
	if err != nil {
		errorLog.Printf("Can't read page '%s' (id='%s'): %s", p.Title, p.ID, err)
		return nil
	}
	return delta
}

func (nb *Notebook) InsertPage(parentID string, node PageNode) error {
	if parentID == "" {
		nb.Pages = append(nb.Pages, node)
		return nil
	}
	parent := nb.FindPage(parentID)
	if parent == nil {
		return fmt.Errorf("parent not found: %s", parentID)
	}
	parent.Children = append(parent.Children, node)
	return nil
}

func (nb *Notebook) CreatePage(title string) *PageNode {
	id := generateID()
	node := PageNode{
		ID:       id,
		Title:    title,
		File:     id + ".json",
		Children: []PageNode{},
	}
	filename := filepath.Join(CONF.pagesDir, node.File)
	if _, err := os.Stat(filename); err == nil {
		errorLog.Printf("Can't create page %s: file %s already exists", title, filename)
		return nil
	}
	empty := []byte(`{"ops":[{"insert":"\n"}]}`)
	err := os.WriteFile(filename, empty, 0644)
	if err != nil {
		errorLog.Printf("Failed to write page %s", title)
		return nil
	}
	return &node
}

func (nb *Notebook) DeletePage(id string) error {
	//todo: optimize?
	newRoot := nb.Pages[:0]
	var deleted *PageNode
	for i := range nb.Pages {
		if nb.Pages[i].ID == id {
			copy := nb.Pages[i]
			deleted = &copy
			continue
		}
		d := nb.Pages[i].DeleteChildPage(id)
		if d != nil {
			deleted = d
		}
		newRoot = append(newRoot, nb.Pages[i])
	}
	nb.Pages = newRoot
	if deleted == nil {
		err := fmt.Errorf("Error deleting page id='%s': page not found", id)
		errorLog.Printf(err.Error())
		return err
	}
	filePath := filepath.Join(CONF.pagesDir, deleted.File)
	err := os.Remove(filePath)
	if err != nil {
		errorLog.Printf("Can't remove file '%s' while deleting page id='%s': %s", filePath, id, err)
		return err
	}
	return nil
}

func (p *PageNode) DeleteChildPage(id string) *PageNode {
	newChildren := p.Children[:0]
	var deleted *PageNode
	for i := range p.Children {
		if p.Children[i].ID == id {
			copy := p.Children[i]
			deleted = &copy
			continue
		}
		if d := p.Children[i].DeleteChildPage(id); d != nil {
			deleted = d
		}
		newChildren = append(newChildren, p.Children[i])
	}
	p.Children = newChildren
	return deleted
}

func (nb *Notebook) RenamePage(id string, newTitle string) error {
	page := nb.FindPage(id)
	if page == nil {
		return fmt.Errorf("page not found: %s", id)
	}
	page.Title = newTitle
	return nil
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
	http.HandleFunc("/delete", httpDeletePage)
	http.HandleFunc("/rename", httpRenamePage)
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
	nb := loadNotebook()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nb)
}

func httpPage(w http.ResponseWriter, r *http.Request) {
	err, code, msg := httpCheckAuth(w, r)
	if err != nil {
		http.Error(w, msg, code)
		return
	}
	id := r.URL.Query().Get("id")
	nb := loadNotebook()
	page := nb.FindPage(id)
	if page == nil {
		errorLog.Printf("Page with id=%s not found", id)
		http.Error(w, "Page not found", 404)
	}
	delta := page.ReadPage()
	if delta == nil {
		http.Error(w, "Can't read page", 500)
	}
	resp := struct {
		Title string          `json:"title"`
		Delta json.RawMessage `json:"delta"`
	}{Title: page.Title, Delta: delta}
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
		Id    string          `json:"id"`
		Delta json.RawMessage `json:"delta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	nb := loadNotebook()
	page := nb.FindPage(req.Id)
	if page == nil {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}
	path := filepath.Join(CONF.pagesDir, page.File)
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
		Title  string `json:"title"`
		Parent string `json:"parent"`
	}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	nb := loadNotebook()
	p := nb.CreatePage(req.Title)
	err = nb.InsertPage(req.Parent, *p)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	saveNotebook(nb)

	resp := struct {
		Nb    Notebook `json:"notebook"`
		NewId string   `json:"id"`
	}{Nb: nb, NewId: p.ID}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func httpDeletePage(w http.ResponseWriter, r *http.Request) {
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
		Id string `json:"id"`
	}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	nb := loadNotebook()
	err = nb.DeletePage(req.Id)
	if err != nil {
		http.Error(w, "Error deleting page", 400)
		return
	}
	saveNotebook(nb)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nb)
}

func httpRenamePage(w http.ResponseWriter, r *http.Request) {
	err, code, msg := httpCheckAuth(w, r)
	if err != nil {
		http.Error(w, msg, code)
		return
	}
	var req struct {
		Id    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Id == "" || req.Title == "" {
		http.Error(w, "Missing ID or Title", http.StatusBadRequest)
		return
	}

	nb := loadNotebook()
	if err := nb.RenamePage(req.Id, req.Title); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	saveNotebook(nb)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(nb)
}
