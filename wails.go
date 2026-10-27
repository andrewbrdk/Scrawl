//go:build !web

package main

import (
	"embed"
	"errors"
	"io/fs"
	"log"
	"sync"

	"github.com/wailsapp/wails/v3/pkg/application"
)

//go:embed dist-wails
var embedded embed.FS

// ScrawlService is bound to the frontend; exported methods become TS functions
// in bindings/ (npm run bindings).
type ScrawlService struct {
	mu sync.Mutex // wails calls methods concurrently; loadNotebook mutates shared state
	s  *Scrawl
}

type CreateResult struct {
	Id       int       `json:"id"`
	Notebook *Notebook `json:"notebook"`
}

func (c *ScrawlService) GetNotebook() *Notebook {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.s.notebook
}

// ReadPage returns the Quill delta as a JSON string.
func (c *ScrawlService) ReadPage(id int) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.s.ReadPage(id)
	if d == nil {
		return "", errors.New("cannot read page")
	}
	return string(d), nil
}

// SavePage stores a Quill delta serialised as JSON.
func (c *ScrawlService) SavePage(id int, delta string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.s.SavePageContent(id, delta)
}

func (c *ScrawlService) CreatePage(title string, parent int) (*CreateResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id, err := c.s.CreatePage(title, parent)
	if err != nil {
		return nil, err
	}
	return &CreateResult{Id: id, Notebook: c.s.notebook}, nil
}

func (c *ScrawlService) DeletePage(id int) (*Notebook, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.s.DeletePage(id); err != nil {
		return nil, err
	}
	return c.s.notebook, nil
}

func (c *ScrawlService) RenamePage(id int, title string) (*Notebook, error) {
	if id == 0 || title == "" {
		return nil, errors.New("missing id or title")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.s.RenamePage(id, title); err != nil {
		return nil, err
	}
	return c.s.notebook, nil
}

// MovePage placement is "above", "below" or "child".
func (c *ScrawlService) MovePage(draggedId, targetId int, placement string) (*Notebook, error) {
	if placement != "above" && placement != "below" && placement != "child" {
		return nil, errors.New("invalid placement")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.s.MovePage(draggedId, targetId, placement); err != nil {
		return nil, err
	}
	return c.s.notebook, nil
}

// run is the wails (desktop and android) entry point, called from main() after initApp().
func run() {
	dist, err := fs.Sub(embedded, "dist-wails")
	if err != nil {
		log.Fatal(err)
	}
	app := application.New(application.Options{
		Name: "Scrawl",
		Services: []application.Service{
			application.NewService(&ScrawlService{s: &SCRAWL}),
		},
		Assets: application.AssetOptions{
			Handler: application.AssetFileServerFS(dist),
		},
	})
	app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:  "Scrawl",
		Width:  1100,
		Height: 750,
	})
	if err := app.Run(); err != nil {
		log.Fatal(err)
	}
}
