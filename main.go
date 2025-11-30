package main

import (
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed index.html style.css
var embedded embed.FS

var jwtSecretKey []byte

var infoLog *log.Logger
var errorLog *log.Logger

var CONF Config
var SCRAWL Scrawl

// todo: limit usage of global vars
type Scrawl struct {
	db       *sql.DB
	notebook *Notebook
	//add config
}

type Config struct {
	port     string
	dbFile   string
	password string
}

type Notebook struct {
	Name  string     `json:"name"`
	Pages []PageNode `json:"pages"`
	//todo: []PageNode -> []*PageNode
}

type PageNode struct {
	//todo: rename PageNode -> Page
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Children []PageNode `json:"children"`
	//todo: []PageNode -> []*PageNode
	//todo: Delta   string     `json:"delta"`
}

func main() {
	initConfig()
	SCRAWL.initDB()
	SCRAWL.loadNotebook()
	jwtSecretKey = generateRandomKey(32)
	infoLog = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	errorLog = log.New(os.Stdout, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	//todo: load notebooks, store in memory
	httpServer()
}

func initConfig() {
	CONF.port = ":8080"
	CONF.dbFile = "./scrawls.db"
	CONF.password = ""
	if port := os.Getenv("SCRAWL_PORT"); port != "" {
		CONF.port = ":" + port
	}
	if dbFile := os.Getenv("SCRAWL_DBFILE"); dbFile != "" {
		CONF.dbFile = dbFile
	}
	CONF.password = os.Getenv("SCRAWL_PASSWORD")
}

func (S *Scrawl) initDB() {
	var err error
	S.db, err = sql.Open("sqlite3", CONF.dbFile)
	if err != nil {
		log.Fatalf("cannot open sqlite db: %v", err)
	}

	_, err = S.db.Exec(`
        PRAGMA foreign_keys = ON;

        CREATE TABLE IF NOT EXISTS pages (
            id TEXT PRIMARY KEY,
            title TEXT NOT NULL,
            delta TEXT NOT NULL,
			created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    		updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
        );

        CREATE TABLE IF NOT EXISTS tree (
            parent_id TEXT NOT NULL,
			child_id TEXT NOT NULL UNIQUE,
			PRIMARY KEY(parent_id, child_id),
			FOREIGN KEY(parent_id) REFERENCES pages(id) ON DELETE CASCADE,
			FOREIGN KEY(child_id) REFERENCES pages(id) ON DELETE CASCADE
        );
    `)

	if err != nil {
		log.Fatalf("sqlite migration failed: %v", err)
	}
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

func (S *Scrawl) loadNotebook() error {
	//todo: Notebook method
	rows, err := S.db.Query(`
        SELECT 
			p.id, 
			p.title, 
			t.parent_id
        FROM pages p
        LEFT JOIN tree t ON p.id = t.child_id
	`)
	if err != nil {
		errorLog.Printf("loadNotebook error: %v", err)
		return nil
	}
	defer rows.Close()

	pages := map[string]*PageNode{}
	top := []*PageNode{}
	children := map[string][]*PageNode{}

	for rows.Next() {
		var id, title string
		var parentID sql.NullString
		err := rows.Scan(&id, &title, &parentID)
		if err != nil {
			errorLog.Printf("loadNotebook scan error: %v", err)
			return nil
		}
		page := &PageNode{
			ID:       id,
			Title:    title,
			Children: []PageNode{},
		}
		pages[id] = page
		if parentID.Valid {
			children[parentID.String] = append(children[parentID.String], page)
		} else {
			top = append(top, page)
		}
	}
	for pid, kids := range children {
		for _, kid := range kids {
			pages[pid].Children = append(pages[pid].Children, *kid)
		}
	}
	//todo: child pages sometimes missing, pointers?
	nb := Notebook{
		Name:  "Notebook",
		Pages: []PageNode{},
	}
	for _, p := range top {
		nb.Pages = append(nb.Pages, *p)
	}
	S.notebook = &nb

	return nil
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
	var delta string
	infoLog.Printf("Reading page '%s' (id='%s')", p.Title, p.ID)
	err := SCRAWL.db.QueryRow("SELECT delta FROM pages WHERE id=?", p.ID).Scan(&delta)
	if err != nil {
		errorLog.Printf("Can't read page '%s' (id='%s'): %s", p.Title, p.ID, err)
		return nil
	}
	return json.RawMessage(delta)
}

func (nb *Notebook) CreatePage(title string, parentID string) (string, error) {
	id := generateID()
	delta := `{"ops":[{"insert":"\n"}]}`
	infoLog.Printf("Creating page '%s' (id='%s')", title, id)
	tx, err := SCRAWL.db.Begin()
	if err != nil {
		errorLog.Printf("Failed to begin transaction: %v", err)
		return "", err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	//todo: create in-memory page?
	_, err = tx.Exec(`
        INSERT INTO pages(id, title, delta, created_at, updated_at)
        VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
    `, id, title, delta)
	if err != nil {
		errorLog.Printf("Failed to insert page: %v", err)
		return "", err
	}
	if parentID != "" {
		_, err = tx.Exec(`
            INSERT INTO tree(parent_id, child_id)
            VALUES (?, ?)
        `, parentID, id)
		if err != nil {
			errorLog.Printf("Failed to insert into tree: %v", err)
			return "", err
		}
	}
	if err = tx.Commit(); err != nil {
		errorLog.Printf("Commit failed: %v", err)
		return "", err
	}
	SCRAWL.loadNotebook()
	return id, nil
}

func (nb *Notebook) SavePage(p *PageNode, delta string) error {
	err := p.Save(delta)
	if err != nil {
		return err
	}
	SCRAWL.loadNotebook()
	return nil
}

func (p *PageNode) Save(delta string) error {
	//todo: add delta to PageNode struct
	_, err := SCRAWL.db.Exec(`
		INSERT INTO pages (id, title, delta, created_at, updated_at)
		VALUES (?, ?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		ON CONFLICT(id) DO UPDATE SET
			title      = excluded.title,
			delta      = excluded.delta,
			updated_at = CURRENT_TIMESTAMP
	`, p.ID, p.Title, delta)
	if err != nil {
		errorLog.Printf("Failed to save page '%s': %v", p.ID, err)
	}
	return err
}

func (nb *Notebook) DeletePage(id string) error {
	tx, err := SCRAWL.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	_, err = tx.Exec(`
        WITH RECURSIVE descendants(id) AS (
            SELECT ? as id
            UNION ALL
            SELECT child_id
            FROM tree
            JOIN descendants 
				ON tree.parent_id = descendants.id
        )
        DELETE FROM pages
        WHERE id IN (SELECT id FROM descendants)
    `, id)
	if err != nil {
		errorLog.Printf("Failed deleting pages recursively: %v", err)
		return err
	}
	err = tx.Commit()
	if err != nil {
		errorLog.Printf("Commit failed during delete: %v", err)
		return err
	}
	SCRAWL.loadNotebook()
	return nil
}

func (nb *Notebook) RenamePage(id string, newTitle string) error {
	res, err := SCRAWL.db.Exec(`
        UPDATE pages
        SET title = ?, updated_at = CURRENT_TIMESTAMP
        WHERE id = ?
    `, newTitle, id)
	if err != nil {
		errorLog.Printf("Failed to rename page '%s': %v", id, err)
		return err
	}
	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("page not found: %s", id)
	}
	SCRAWL.loadNotebook()
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
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SCRAWL.notebook)
}

func httpPage(w http.ResponseWriter, r *http.Request) {
	err, code, msg := httpCheckAuth(w, r)
	if err != nil {
		http.Error(w, msg, code)
		return
	}
	id := r.URL.Query().Get("id")
	page := SCRAWL.notebook.FindPage(id)
	if page == nil {
		errorLog.Printf("Page with id=%s not found", id)
		http.Error(w, "Page not found", 404)
		return
	}
	delta := page.ReadPage()
	if delta == nil {
		http.Error(w, "Can't read page", 500)
		return
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
	page := SCRAWL.notebook.FindPage(req.Id)
	if page == nil {
		http.Error(w, "Page not found", http.StatusNotFound)
		return
	}
	err = SCRAWL.notebook.SavePage(page, string(req.Delta))
	if err != nil {
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

	newId, err := SCRAWL.notebook.CreatePage(req.Title, req.Parent)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}

	resp := struct {
		Nb    Notebook `json:"notebook"`
		NewId string   `json:"id"`
	}{Nb: *SCRAWL.notebook, NewId: newId}
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

	err = SCRAWL.notebook.DeletePage(req.Id)
	if err != nil {
		http.Error(w, "Error deleting page", 400)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SCRAWL.notebook)
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

	err = SCRAWL.notebook.RenamePage(req.Id, req.Title)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SCRAWL.notebook)
}
