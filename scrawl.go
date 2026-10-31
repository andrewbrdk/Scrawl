package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"os"
	"runtime"
	"sort"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

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
	Position int     `json:"position"`
	Children []*Page `json:"children"`
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

func (S *Scrawl) initDB() {
	var err error

	firstRun := false
	_, err = os.Stat(CONF.dbFile)
	if errors.Is(err, os.ErrNotExist) {
		firstRun = true
	}

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

        CREATE TABLE IF NOT EXISTS pagetree (
            parent_id INTEGER NOT NULL,
			child_id INTEGER NOT NULL UNIQUE,
			position INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY(parent_id, child_id),
			FOREIGN KEY(parent_id) REFERENCES pages(id) ON DELETE CASCADE,
			FOREIGN KEY(child_id) REFERENCES pages(id) ON DELETE CASCADE
        );
    `)

	if err != nil {
		log.Fatalf("Can't create tables: %v", err)
	}

	if firstRun {
		infoLog.Println("Database created")
		delta := `{"ops":[{"insert": "Welcome to Scrawl!\n"}]}`
		title := "Welcome"

		tx, _ := S.db.Begin()
		tx.Exec(`INSERT INTO pages (id, title, delta) VALUES (0, '__root__', '{}')`)
		result, _ := tx.Exec(`
			INSERT INTO pages (title, delta)
			VALUES (?, ?)`, title, delta)
		welcomeId, _ := result.LastInsertId()
		tx.Exec(`
			INSERT INTO pagetree (parent_id, child_id, position)
			VALUES (0, ?, 0)`, welcomeId)
		err := tx.Commit()
		if err != nil {
			errorLog.Printf("failed to create welcome page: %v", err)
		}
	}
}

func (S *Scrawl) loadNotebook() error {
	rows, err := S.db.Query(`
        SELECT 
			p.id, 
			p.title, 
			pt.parent_id,
			pt.position
        FROM pages p
        LEFT JOIN pagetree pt
			ON p.id = pt.child_id
		where 
			p.id != 0
		ORDER BY pt.parent_id, pt.position, p.id`)
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
		var parentID int
		var position int
		err := rows.Scan(&id, &title, &parentID, &position)
		if err != nil {
			errorLog.Printf("loadNotebook scan error: %v", err)
			return nil
		}
		page := &Page{
			Id:       id,
			Title:    title,
			Position: position,
			Children: []*Page{},
		}
		pages[id] = page
		if parentID == 0 {
			top = append(top, page)
		} else {
			children[parentID] = append(children[parentID], page)
		}
	}
	sort.SliceStable(top, func(i, j int) bool {
		if top[i].Position == top[j].Position {
			return top[i].Id < top[j].Id
		}
		return top[i].Position < top[j].Position
	})

	for pid, kids := range children {
		sort.SliceStable(kids, func(i, j int) bool {
			if kids[i].Position == kids[j].Position {
				return kids[i].Id < kids[j].Id
			}
			return kids[i].Position < kids[j].Position
		})
		parent := pages[pid]
		parent.Children = kids
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

	var maxPos sql.NullInt64
	tx.QueryRow(`
		SELECT MAX(position) 
		FROM pagetree
		WHERE parent_id = ?
	`, parentID).Scan(&maxPos)

	position := 0
	if maxPos.Valid {
		position = int(maxPos.Int64) + 1
	}

	res, _ := tx.Exec(`
        INSERT INTO pages(title, delta)
        VALUES (?, ?)
    `, title, delta)
	newId, _ := res.LastInsertId()

	tx.Exec(`
		INSERT INTO pagetree(parent_id, child_id, position)
		VALUES (?, ?, ?)
	`, parentID, newId, position)

	err = tx.Commit()
	if err != nil {
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

	var parentID int
	var deletedPosition int
	tx.QueryRow(`
		SELECT parent_id, position 
		FROM pagetree WHERE child_id = ?`, id).Scan(&parentID, &deletedPosition)

	_, err = tx.Exec(`
        WITH RECURSIVE descendants(id) AS (
            SELECT ? as id
            UNION ALL
            SELECT child_id
            FROM pagetree
            JOIN descendants 
				ON pagetree.parent_id = descendants.id
        )
        DELETE FROM pages
        WHERE id IN (SELECT id FROM descendants)
    `, id)
	if err != nil {
		errorLog.Printf("Failed deleting pages recursively: %v", err)
		return err
	}

	tx.Exec(`
		UPDATE pagetree 
		SET position = position - 1
		WHERE parent_id = ? AND position > ?
	`, parentID, deletedPosition)

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

func (S *Scrawl) MovePage(draggedId int, targetId int, placement string) error {
	var exists int
	err := S.db.QueryRow(`
        WITH RECURSIVE descendants(id) AS (
			SELECT ? AS id
			UNION ALL
			SELECT child_id as id
			FROM pagetree
			JOIN descendants 
				ON pagetree.parent_id = descendants.id
		)
        SELECT 1 FROM descendants WHERE id = ? LIMIT 1
    `, draggedId, targetId).Scan(&exists)
	if err != sql.ErrNoRows {
		errorLog.Printf("Can't move page into its own descendant: draggedId=%d, targetId=%d", draggedId, targetId)
		return err
	}

	tx, err := S.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	var oldParentID, oldPosition int
	tx.QueryRow(`
		SELECT parent_id, position FROM pagetree WHERE child_id = ?
	`, draggedId).Scan(&oldParentID, &oldPosition)

	var newParentID, newPosition int
	if placement == "child" {
		newParentID = targetId
		newPosition = 0
		var maxPos sql.NullInt64
		tx.QueryRow(`
			SELECT MAX(position) 
			FROM pagetree
			WHERE parent_id = ?`, targetId).Scan(&maxPos)
		if maxPos.Valid {
			newPosition = int(maxPos.Int64) + 1
		}
	} else {
		var targetParentId, targetPosition int
		tx.QueryRow(`
			SELECT parent_id, position 
			FROM pagetree
			WHERE child_id = ?`, targetId).Scan(&targetParentId, &targetPosition)
		newParentID = targetParentId
		if placement == "above" {
			newPosition = targetPosition
		} else {
			newPosition = targetPosition + 1
		}
	}

	tx.Exec(`
		UPDATE pagetree 
		SET parent_id = ?, position = ?
		WHERE child_id = ?`, newParentID, newPosition, draggedId)
	tx.Exec(`
		UPDATE pagetree 
		SET position = position - 1
		WHERE parent_id = ? AND position >= ? AND child_id != ?`, oldParentID, oldPosition, draggedId)
	tx.Exec(`
		UPDATE pagetree 
		SET position = position + 1
		WHERE parent_id = ? AND position >= ? AND child_id != ?`, newParentID, newPosition, draggedId)

	err = tx.Commit()
	if err != nil {
		errorLog.Printf("Commit failed during reorder: %v", err)
		return err
	}

	S.loadNotebook()
	return nil
}

