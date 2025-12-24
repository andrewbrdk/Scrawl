package main

import (
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
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
}

type Config struct {
	port     string
	dbFile   string
	password string
}

type Notebook struct {
	//todo: remove?
	Pages []*Page `json:"pages"`
}

type Page struct {
	Id       int     `json:"id"`
	Title    string  `json:"title"`
	Children []*Page `json:"children"`
}

func main() {
	initConfig()
	jwtSecretKey = generateRandomKey(32)
	infoLog = log.New(os.Stdout, "INFO: ", log.Ldate|log.Ltime|log.Lshortfile)
	errorLog = log.New(os.Stdout, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	SCRAWL.initDB()
	SCRAWL.loadNotebook()
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
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            title TEXT NOT NULL,
            delta TEXT NOT NULL,
			created DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    		updated DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
        );

        CREATE TABLE IF NOT EXISTS children (
            parent_id INTEGER NOT NULL,
			child_id INTEGER NOT NULL UNIQUE,
			PRIMARY KEY(parent_id, child_id),
			FOREIGN KEY(parent_id) REFERENCES pages(id) ON DELETE CASCADE,
			FOREIGN KEY(child_id) REFERENCES pages(id) ON DELETE CASCADE
        );
    `)

	if err != nil {
		log.Fatalf("Can't create tables: %v", err)
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
	rows, err := S.db.Query(`
        SELECT 
			p.id, 
			p.title, 
			c.parent_id
        FROM pages p
        LEFT JOIN children c
			ON p.id = c.child_id`)
	if err != nil {
		errorLog.Printf("loadNotebook error: %v", err)
		return nil
	}
	defer rows.Close()

	//todo: move to client
	pages := map[int]*Page{}
	top := []*Page{}
	children := map[int][]*Page{}

	for rows.Next() {
		var id int
		var title string
		var parentID sql.NullInt64
		err := rows.Scan(&id, &title, &parentID)
		if err != nil {
			errorLog.Printf("loadNotebook scan error: %v", err)
			return nil
		}
		page := &Page{
			Id:       id,
			Title:    title,
			Children: []*Page{},
		}
		pages[id] = page
		if parentID.Valid {
			pid := int(parentID.Int64)
			children[pid] = append(children[pid], page)
		} else {
			top = append(top, page)
		}
	}
	for pid, kids := range children {
		parent := pages[pid]
		parent.Children = append(parent.Children, kids...)
	}
	if S.notebook == nil {
		S.notebook = &Notebook{}
	}
	S.notebook.Pages = top
	return nil
}

func (S *Scrawl) ReadPage(id int) json.RawMessage {
	var delta string
	infoLog.Printf("Reading page id='%d'", id)
	err := SCRAWL.db.QueryRow("SELECT delta FROM pages WHERE id=?", id).Scan(&delta)
	if err != nil {
		errorLog.Printf("Can't read page id='%d': %s", id, err)
		return nil
	}
	return json.RawMessage(delta)
}

func (S *Scrawl) CreatePage(title string, parentID int) (int, error) {
	//todo: merge with SavePage
	delta := `{"ops":[{"insert":"\n"}]}`
	infoLog.Printf("Creating page '%s'", title)
	tx, err := S.db.Begin()
	if err != nil {
		errorLog.Printf("Failed to begin transaction: %v", err)
		return 0, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()
	res, err := tx.Exec(`
        INSERT INTO pages(title, delta)
        VALUES (?, ?)
    `, title, delta)
	if err != nil {
		errorLog.Printf("Failed to insert page: %v", err)
		return 0, err
	}
	newId, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if parentID != 0 {
		_, err = tx.Exec(`
            INSERT INTO children(parent_id, child_id)
            VALUES (?, ?)
        `, parentID, newId)
		if err != nil {
			errorLog.Printf("Failed to insert into children: %v", err)
			return 0, err
		}
	}
	if err = tx.Commit(); err != nil {
		errorLog.Printf("Commit failed: %v", err)
		return 0, err
	}
	S.loadNotebook()
	return int(newId), nil
}

func (S *Scrawl) SavePageContent(id int, delta string) error {
	//todo: merge with CreatePage, upsert
	_, err := S.db.Exec(`
		UPDATE pages
        SET delta = ?, updated = CURRENT_TIMESTAMP
        WHERE id = ?
	`, delta, id)
	if err != nil {
		errorLog.Printf("Failed to save page '%d': %v", id, err)
	}
	return nil
}

func (S *Scrawl) DeletePage(id int) error {
	tx, err := S.db.Begin()
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
            FROM children
            JOIN descendants 
				ON children.parent_id = descendants.id
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
	S.loadNotebook()
	return nil
}

func (S *Scrawl) RenamePage(id int, newTitle string) error {
	_, err := S.db.Exec(`
        UPDATE pages
        SET title = ?, updated = CURRENT_TIMESTAMP
        WHERE id = ?
    `, newTitle, id)
	if err != nil {
		errorLog.Printf("Failed to rename page id='%d': %v", id, err)
		return err
	}
	S.loadNotebook()
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
	idStr := r.URL.Query().Get("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	delta := SCRAWL.ReadPage(id)
	if delta == nil {
		http.Error(w, "Can't read page", 500)
		return
	}
	resp := struct {
		Delta json.RawMessage `json:"delta"`
	}{Delta: delta}
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
		Id    int             `json:"id"`
		Delta json.RawMessage `json:"delta"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	err = SCRAWL.SavePageContent(req.Id, string(req.Delta))
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
		Parent int    `json:"parent"`
	}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}

	newPageId, err := SCRAWL.CreatePage(req.Title, req.Parent)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}

	resp := struct {
		Notebooks *Notebook `json:"notebook"`
		NewPageId int       `json:"id"`
	}{Notebooks: SCRAWL.notebook, NewPageId: newPageId}
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
		Id int `json:"id"`
	}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid JSON", 400)
		return
	}
	err = SCRAWL.DeletePage(req.Id)
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
		Id    int    `json:"id"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Id == 0 || req.Title == "" {
		http.Error(w, "Missing ID or Title", http.StatusBadRequest)
		return
	}
	err = SCRAWL.RenamePage(req.Id, req.Title)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SCRAWL.notebook)
}
