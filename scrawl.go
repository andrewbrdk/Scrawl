package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"os"
	"sort"

	_ "github.com/mattn/go-sqlite3"
)

var SCRAWL Scrawl

// todo: limit usage of global vars
type Scrawl struct {
	db       *sql.DB
	notebook *Notebook
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

func (S *Scrawl) initDB() {
	var err error

	firstRun := false
	_, err = os.Stat(CONF.dbFile)
	if errors.Is(err, os.ErrNotExist) {
		firstRun = true
	}

	// DSN sets options for every pooled connection
	// _foreign_keys=on: enforce the foreign keys of the schema.
	// _txlock=immediate: BEGIN takes the write lock, so a sync reads and writes consistently.
	// _busy_timeout: longer than the sync http timeout, local writes wait for a running sync.
	// _journal_mode=WAL: reads continue during a write.
	sqliteOptions := "_foreign_keys=on&_txlock=immediate&_busy_timeout=20000&_journal_mode=WAL"
	S.db, err = sql.Open("sqlite3", CONF.dbFile+"?"+sqliteOptions)
	if err != nil {
		log.Fatalf("cannot open sqlite db: %v", err)
	}

	_, err = S.db.Exec(`
        CREATE TABLE IF NOT EXISTS pages (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            title TEXT NOT NULL,
            delta TEXT NOT NULL,
			created DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    		updated DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
            page_uid TEXT NOT NULL UNIQUE,
            is_deleted INTEGER NOT NULL DEFAULT 0,
            server_version INTEGER NOT NULL DEFAULT 0,
            has_local_changes INTEGER NOT NULL DEFAULT 0
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

		//fixed uids for initial pages
		tx, _ := S.db.Begin()
		tx.Exec(`INSERT INTO pages (id, page_uid, title, delta) VALUES (0, 'root', '__root__', '{}')`)
		result, _ := tx.Exec(`
			INSERT INTO pages (page_uid, title, delta)
			VALUES ('welcome', ?, ?)`, title, delta)
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
			COALESCE(pt.parent_id, 0),
			COALESCE(pt.position, 0)
        FROM pages p
        LEFT JOIN pagetree pt
			ON p.id = pt.child_id
		where
			p.id != 0 AND p.is_deleted = 0
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
		if parent == nil {
			// the tree is expected to have no live page under a deleted one
			errorLog.Printf("loadNotebook: pages %v have a deleted or missing parent %d", len(kids), pid)
			continue
		}
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

func markChanged(tx *sql.Tx, id int) error {
	var err error
	if isServer {
		_, err = tx.Exec(`
			UPDATE pages
			SET server_version = (SELECT COALESCE(MAX(server_version), 0) + 1 FROM pages)
			WHERE id = ?`, id)
	} else {
		_, err = tx.Exec(`UPDATE pages SET has_local_changes = 1 WHERE id = ?`, id)
	}
	return err
}

func markChildrenChanged(tx *sql.Tx, parentID int, fromPosition int) error {
	rows, err := tx.Query(`
		SELECT child_id
		FROM pagetree
		WHERE parent_id = ? AND position >= ?`, parentID, fromPosition)
	if err != nil {
		return err
	}
	var ids []int
	for rows.Next() {
		var id int
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		if err := markChanged(tx, id); err != nil {
			return err
		}
	}
	return nil
}

func newUID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		log.Fatalf("cannot generate uid: %v", err)
	}
	return hex.EncodeToString(b)
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

	res, err := tx.Exec(`
        INSERT INTO pages(page_uid, title, delta)
        VALUES (?, ?, ?)
    `, newUID(), title, delta)
	if err != nil {
		errorLog.Printf("Failed to insert page: %v", err)
		return 0, err
	}
	newId, _ := res.LastInsertId()

	_, err = tx.Exec(`
		INSERT INTO pagetree(parent_id, child_id, position)
		VALUES (?, ?, ?)
	`, parentID, newId, position)
	if err != nil {
		errorLog.Printf("Failed to insert into the page tree: %v", err)
		return 0, err
	}

	// new page is a change to sync
	err = markChanged(tx, int(newId))
	if err != nil {
		errorLog.Printf("Failed marking new page: %v", err)
		return 0, err
	}

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
	tx, err := S.db.Begin()
	if err != nil {
		errorLog.Printf("Failed to save page '%d': %v", id, err)
		return nil
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	_, err = tx.Exec(`
		UPDATE pages
		SET delta = ?, updated = CURRENT_TIMESTAMP
		WHERE id = ?
	`, delta, id)
	if err == nil {
		err = markChanged(tx, id)
	}
	if err == nil {
		err = tx.Commit()
	}
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

	// the page and all its descendants
	rows, err := tx.Query(`
        WITH RECURSIVE descendants(id) AS (
            SELECT ? as id
            UNION ALL
            SELECT child_id
            FROM pagetree
            JOIN descendants
				ON pagetree.parent_id = descendants.id
        )
        SELECT id FROM descendants`, id)
	if err != nil {
		errorLog.Printf("Failed listing descendants: %v", err)
		return err
	}
	var ids []int
	for rows.Next() {
		var did int
		if err = rows.Scan(&did); err != nil {
			rows.Close()
			errorLog.Printf("Failed reading descendants: %v", err)
			return err
		}
		ids = append(ids, did)
	}
	rows.Close()

	var parentID int
	var deletedPosition int
	tx.QueryRow(`
		SELECT parent_id, position
		FROM pagetree WHERE child_id = ?`, id).Scan(&parentID, &deletedPosition)

	// siblings below the deleted page change position, so they sync too
	err = markChildrenChanged(tx, parentID, deletedPosition+1)
	if err != nil {
		errorLog.Printf("Failed marking siblings: %v", err)
		return err
	}

	// soft delete: the page stays as a tombstone so the delete can sync
	for _, did := range ids {
		_, err = tx.Exec(`UPDATE pages SET is_deleted = 1 WHERE id = ?`, did)
		if err == nil {
			err = markChanged(tx, did)
		}
		if err != nil {
			errorLog.Printf("Failed deleting pages recursively: %v", err)
			return err
		}
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
        UPDATE pages
        SET title = ?, updated = CURRENT_TIMESTAMP
        WHERE id = ?
    `, newTitle, id)
	if err == nil {
		err = markChanged(tx, id)
	}
	if err == nil {
		err = tx.Commit()
	}
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

	// the dragged page and the siblings in both parents changed position
	err = markChildrenChanged(tx, oldParentID, 0)
	if err == nil {
		err = markChildrenChanged(tx, newParentID, 0)
	}
	if err != nil {
		errorLog.Printf("Failed marking moved pages: %v", err)
		return err
	}

	err = tx.Commit()
	if err != nil {
		errorLog.Printf("Commit failed during reorder: %v", err)
		return err
	}

	S.loadNotebook()
	return nil
}
