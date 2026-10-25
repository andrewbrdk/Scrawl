//go:build web

package main

import (
	"crypto/rand"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

//go:embed dist-web
var embedded embed.FS

var jwtSecretKey []byte

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

func generateRandomKey(size int) []byte {
	key := make([]byte, size)
	_, err := rand.Read(key)
	if err != nil {
		errorLog.Printf("Failed to generate a JWT secret key. Aborting.")
		os.Exit(1)
	}
	return key
}

type Response struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
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

func httpMovePage(w http.ResponseWriter, r *http.Request) {
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
		DraggedId int    `json:"draggedId"`
		TargetId  int    `json:"targetId"`
		Placement string `json:"placement"`
	}
	err = json.NewDecoder(r.Body).Decode(&req)
	if err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}
	if req.Placement != "above" && req.Placement != "below" && req.Placement != "child" {
		http.Error(w, "Invalid placement", http.StatusBadRequest)
		return
	}

	err = SCRAWL.MovePage(req.DraggedId, req.TargetId, req.Placement)
	if err != nil {
		http.Error(w, "Failed to reorder page", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SCRAWL.notebook)
}
