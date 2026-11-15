package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// A client sends the pages with local changes and the highest server_version it has seen,
// the server replies with every page that has a newer server_version.

type SyncPage struct {
	PageUID       string          `json:"page_uid"`
	Title         string          `json:"title"`
	Delta         json.RawMessage `json:"delta"`
	IsDeleted     bool            `json:"is_deleted"`
	ParentPageUID string          `json:"parent_page_uid"`
	Position      int             `json:"position"`
	ServerVersion int64           `json:"server_version"`
}

type SyncMessage struct {
	SinceServerVersion int64      `json:"since_server_version,omitempty"`
	Pages              []SyncPage `json:"pages"`
}

// 10 s per request, local writes wait for it (busy_timeout in initDB is longer)
var syncHTTP = http.Client{Timeout: 10 * time.Second}

func (S *Scrawl) Sync(server, token string) (err error) {
	tx, err := S.db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	var req SyncMessage
	err = tx.QueryRow(`SELECT COALESCE(MAX(server_version), 0) FROM pages`).Scan(&req.SinceServerVersion)
	if err != nil {
		return err
	}
	if req.Pages, err = selectPagesToPush(tx); err != nil {
		return err
	}

	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequest(http.MethodPost, strings.TrimRight(server, "/")+"/sync", bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if token != "" {
		httpReq.Header.Set("Authorization", "Bearer "+token)
	}
	httpResp, err := syncHTTP.Do(httpReq)
	if err != nil {
		return err
	}
	defer httpResp.Body.Close()
	if httpResp.StatusCode != http.StatusOK {
		return fmt.Errorf("sync: server answered %s", httpResp.Status)
	}
	var resp SyncMessage
	if err = json.NewDecoder(httpResp.Body).Decode(&resp); err != nil {
		return err
	}

	for _, p := range resp.Pages {
		_, err = tx.Exec(`
			INSERT INTO pages(page_uid, title, delta, is_deleted, server_version, has_local_changes)
			VALUES (?, ?, ?, ?, ?, 0)
			ON CONFLICT(page_uid) DO UPDATE SET
				title = excluded.title,
				delta = excluded.delta,
				is_deleted = excluded.is_deleted,
				server_version = excluded.server_version,
				has_local_changes = 0,
				updated = CURRENT_TIMESTAMP`,
			p.PageUID, p.Title, string(p.Delta), p.IsDeleted, p.ServerVersion)
		if err != nil {
			return err
		}
	}
	for _, p := range resp.Pages {
		if err = upsertTree(tx, p); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	S.loadNotebook()
	return nil
}

func selectPagesToPush(tx *sql.Tx) ([]SyncPage, error) {
	rows, err := tx.Query(`
		SELECT
			p.page_uid,
			p.title,
			p.delta,
			p.is_deleted,
			COALESCE(par.page_uid, 'root'),
			COALESCE(pt.position, 0),
			p.server_version
		FROM pages p
		LEFT JOIN pagetree pt ON pt.child_id = p.id
		LEFT JOIN pages par ON par.id = pt.parent_id
		WHERE p.id != 0
			AND p.has_local_changes = 1
		ORDER BY p.id`)
	if err != nil {
		return nil, err
	}
	return scanSyncPages(rows)
}

func scanSyncPages(rows *sql.Rows) ([]SyncPage, error) {
	defer rows.Close()
	pages := []SyncPage{}
	for rows.Next() {
		var p SyncPage
		var delta string
		var deleted int
		if err := rows.Scan(&p.PageUID, &p.Title, &delta, &deleted, &p.ParentPageUID, &p.Position, &p.ServerVersion); err != nil {
			return nil, err
		}
		p.Delta = json.RawMessage(delta)
		p.IsDeleted = deleted != 0
		pages = append(pages, p)
	}
	return pages, rows.Err()
}

// ServerSync applies the pushed pages and returns everything newer than the client has.
// All or nothing: on an error the transaction is rolled back and the client retries.
func (S *Scrawl) ServerSync(req SyncMessage) (resp *SyncMessage, err error) {
	tx, err := S.db.Begin()
	if err != nil {
		return nil, err
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// pass 1: the pages themselves, pass 2: the tree, a parent may come later in the request
	placed := []SyncPage{}
	for _, p := range req.Pages {
		if p.PageUID == "" || p.PageUID == "root" {
			continue
		}
		var current int64
		err = tx.QueryRow(`SELECT server_version FROM pages WHERE page_uid = ?`, p.PageUID).Scan(&current)
		if err == sql.ErrNoRows {
			// a new page
			_, err = tx.Exec(`
				INSERT INTO pages(page_uid, title, delta, is_deleted, server_version)
				VALUES (?, ?, ?, ?, ?)`, p.PageUID, p.Title, string(p.Delta), p.IsDeleted, nextServerVersion(tx))
			if err != nil {
				return nil, err
			}
			placed = append(placed, p)
			continue
		}
		if err != nil {
			return nil, err
		}

		if current == p.ServerVersion {
			// the client saw the server's latest version of the page
			_, err = tx.Exec(`
				UPDATE pages
				SET title = ?, delta = ?, is_deleted = ?, server_version = ?, updated = CURRENT_TIMESTAMP
				WHERE page_uid = ?`, p.Title, string(p.Delta), p.IsDeleted, nextServerVersion(tx), p.PageUID)
			if err != nil {
				return nil, err
			}
			placed = append(placed, p)
			continue
		}

		// the page changed on the server since the client's pull
		var title, delta string
		var serverDeleted bool
		err = tx.QueryRow(`
			SELECT title, delta, is_deleted FROM pages WHERE page_uid = ?`, p.PageUID).Scan(&title, &delta, &serverDeleted)
		if err != nil {
			return nil, err
		}

		if title == p.Title && delta == string(p.Delta) && serverDeleted == p.IsDeleted {
			// the same content is already stored: a retry of a push whose reply was lost.
			// The tree is set again, the reply gives the client the current version.
			placed = append(placed, p)
			continue
		}

		if p.IsDeleted && serverDeleted {
			// deleted on both sides
			continue
		}

		if p.IsDeleted {
			// delete against an edit: the delete is applied, the edited page stays as a conflict page
			_, err = tx.Exec(`
				UPDATE pages
				SET is_deleted = 1, server_version = ?, updated = CURRENT_TIMESTAMP
				WHERE page_uid = ?`, nextServerVersion(tx), p.PageUID)
			if err != nil {
				return nil, err
			}
			if err = addConflictPage(tx, title, delta); err != nil {
				return nil, err
			}
			continue
		}

		// edit against an edit or a delete: the pushed edit becomes a conflict page
		if err = addConflictPage(tx, p.Title, string(p.Delta)); err != nil {
			return nil, err
		}
	}

	for _, p := range placed {
		if err = upsertTree(tx, p); err != nil {
			return nil, err
		}
	}

	// the tree never keeps a live page under a deleted one
	if err = resolveOrphans(tx); err != nil {
		return nil, err
	}

	pages, err := selectPagesNewerThan(tx, req.SinceServerVersion)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	S.loadNotebook()
	return &SyncMessage{Pages: pages}, nil
}

func nextServerVersion(tx *sql.Tx) int64 {
	var v int64
	tx.QueryRow(`SELECT COALESCE(MAX(server_version), 0) + 1 FROM pages`).Scan(&v)
	return v
}

func selectPagesNewerThan(tx *sql.Tx, serverVersion int64) ([]SyncPage, error) {
	rows, err := tx.Query(`
		SELECT
			p.page_uid,
			p.title,
			p.delta,
			p.is_deleted,
			COALESCE(par.page_uid, 'root'),
			COALESCE(pt.position, 0),
			p.server_version
		FROM pages p
		LEFT JOIN pagetree pt ON pt.child_id = p.id
		LEFT JOIN pages par ON par.id = pt.parent_id
		WHERE p.id != 0
			AND p.server_version > ?
		ORDER BY p.id`, serverVersion)
	if err != nil {
		return nil, err
	}
	return scanSyncPages(rows)
}

// addConflictPage keeps the content that lost a conflict. It is a new page at the top of
// the tree, so it is visible whatever happened to the page's parent.
func addConflictPage(tx *sql.Tx, title, delta string) error {
	res, err := tx.Exec(`
		INSERT INTO pages(page_uid, title, delta, server_version)
		VALUES (?, ?, ?, ?)`, newUID(), title+" (conflict)", delta, nextServerVersion(tx))
	if err != nil {
		return err
	}
	id, _ := res.LastInsertId()
	_, err = tx.Exec(`
		INSERT INTO pagetree(parent_id, child_id, position)
		VALUES (0, ?, (SELECT COALESCE(MAX(position), -1) + 1 FROM pagetree WHERE parent_id = 0))`, id)
	return err
}

// resolveOrphans: a device can add or edit a page under a parent another device deleted.
// The page is deleted too and its content moves to a conflict page.
// Its own children become orphans in the next round.
func resolveOrphans(tx *sql.Tx) error {
	for {
		rows, err := tx.Query(`
			SELECT p.id, p.title, p.delta
			FROM pages p
			JOIN pagetree pt ON pt.child_id = p.id
			JOIN pages parent ON parent.id = pt.parent_id
			WHERE p.is_deleted = 0 AND parent.is_deleted = 1`)
		if err != nil {
			return err
		}
		type orphan struct {
			id           int
			title, delta string
		}
		var orphans []orphan
		for rows.Next() {
			var o orphan
			if err := rows.Scan(&o.id, &o.title, &o.delta); err != nil {
				rows.Close()
				return err
			}
			orphans = append(orphans, o)
		}
		rows.Close()
		if len(orphans) == 0 {
			return nil
		}
		for _, o := range orphans {
			_, err := tx.Exec(`
				UPDATE pages
				SET is_deleted = 1, server_version = ?, updated = CURRENT_TIMESTAMP
				WHERE id = ?`, nextServerVersion(tx), o.id)
			if err == nil {
				err = addConflictPage(tx, o.title, o.delta)
			}
			if err != nil {
				return err
			}
		}
	}
}

// upsertTree puts the page under its parent. Both pages must exist already.
func upsertTree(tx *sql.Tx, p SyncPage) error {
	var id int
	if err := tx.QueryRow(`SELECT id FROM pages WHERE page_uid = ?`, p.PageUID).Scan(&id); err != nil {
		return err
	}
	// an unknown parent puts the page at the top
	parentID := 0
	tx.QueryRow(`SELECT id FROM pages WHERE page_uid = ?`, p.ParentPageUID).Scan(&parentID)
	if parentID == id {
		parentID = 0
	}
	_, err := tx.Exec(`
		INSERT INTO pagetree(parent_id, child_id, position)
		VALUES (?, ?, ?)
		ON CONFLICT(child_id) DO UPDATE SET parent_id = excluded.parent_id, position = excluded.position
	`, parentID, id, p.Position)
	if err != nil {
		return err
	}
	return breakCycle(tx, id)
}

// breakCycle: two devices can move pages into each other, the pages would vanish from the tree.
// The page just placed goes to the top instead.
func breakCycle(tx *sql.Tx, id int) error {
	seen := map[int]bool{}
	cur := id
	for {
		var parent int
		if err := tx.QueryRow(`SELECT parent_id FROM pagetree WHERE child_id = ?`, cur).Scan(&parent); err != nil || parent == 0 {
			return nil
		}
		if parent == id {
			if _, err := tx.Exec(`UPDATE pagetree SET parent_id = 0 WHERE child_id = ?`, id); err != nil {
				return err
			}
			return markChanged(tx, id)
		}
		if seen[parent] {
			return nil
		}
		seen[parent] = true
		cur = parent
	}
}
