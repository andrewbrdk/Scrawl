import Quill from 'quill';
import 'quill/dist/quill.bubble.css';
import katex from 'katex';
import 'katex/dist/katex.min.css';
import { setupQuill, markdownBehaviour } from './quillcustom';

window.katex = katex;
setupQuill();

interface Page {
    id: number;
    title: string;
    position: number;
    children: Page[];
}

interface Notebook {
    pages: Page[];
}

interface DragState {
    draggedItem: HTMLElement | null;
    dragOverItem: HTMLElement | null;
    dragPlacement: string | null;
}

interface CreatePageResponse {
    id: number;
    notebook: Notebook;
}

class XMain extends HTMLElement {
    #login: HTMLElement | null = null;
    #scrawl: XScrawl | null = null;

    constructor() {
        super();
        this.innerHTML = `
            <x-login></x-login>
            <x-scrawl></x-scrawl>
        `;
        this.#scrawl = this.querySelector('x-scrawl') as XScrawl;
        this.#login = this.querySelector('x-login') as HTMLElement;
        this.#login.hidden = true;
        this.#scrawl.hidden = true;
        this.addEventListener('loginsuccess', () => {
            this.renderPage();
        });
        this.renderPage();
    }

    async renderPage(): Promise<void> {
        const res = await fetch('/pages');
        const statusCode = res.status;
        if (statusCode === 200) {
            this.#scrawl!.hidden = false;
            this.#login!.hidden = true;
            const p: Notebook = await res.json();
            this.#scrawl!.init(p);
        } else if (statusCode === 401) {
            this.#login!.hidden = false;
            this.#scrawl!.hidden = true;
        } else {
            this.#scrawl!.hidden = true;
        }
    }
}

class XLogin extends HTMLElement {
    constructor() {
        super();
        this.innerHTML = `
            <h1>Scrawl</h1>
            <form>
                <label for="password">Password:</label>
                <input type="password" id="password" name="password" required>
                <button type="button">Login</button>
                <br/>
                <div id="invalidpassword" style="display: none;">Invalid Password</div>
            </form>
        `;
        this.querySelector('form')!.onsubmit = (e: Event) => {
            e.preventDefault();
            this.submitLogin();
        };
        this.querySelector('button')!.onclick = (e: MouseEvent) => {
            e.preventDefault();
            this.submitLogin();
        };
    }

    async submitLogin(): Promise<void> {
        const password = (this.querySelector('#password') as HTMLInputElement).value;
        const response = await fetch('/login', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'include',
            body: JSON.stringify({ password }),
        });
        if (response.ok) {
            (this.querySelector('#invalidpassword') as HTMLElement).style.display = "none";
            this.dispatchEvent(new CustomEvent('loginsuccess', { bubbles: true }));
        } else {
            (this.querySelector('#invalidpassword') as HTMLElement).style.display = "inline";
            setTimeout(() => {
                (this.querySelector('#invalidpassword') as HTMLElement).style.display = "none";
            }, 3000);
        }
    }
}

class XScrawl extends HTMLElement {
    #aside: HTMLElement | null = null;
    #ulpages: HTMLElement | null = null;
    #pagetitle: HTMLInputElement | null = null;
    #editor: HTMLElement | null = null;
    #quill: Quill | null = null;
    #notebook: Notebook | null = null;
    #selected: number | null = null;
    #expanded: Record<number, boolean> = {};
    #resizer: HTMLElement | null = null;
    #isResizing: boolean = false;
    #resizingStartX: number = 0;
    #resizingStartWidth: number = 0;
    #sidebarDefaultWidth: string = "15%";
    #sidebarMinWidth: number = 120;
    #collapseEdgeThreshold: number = 30;
    #dragState: DragState = {
        draggedItem: null,
        dragOverItem: null,
        dragPlacement: null
    };

    constructor() {
        super();
        this.innerHTML = `
            <aside>
                <div id="sidebar-header">
                    <span id="sidebar-label">Pages</span>
                </div>
                <ul id="pages"></ul>
                <button id="newpage">＋</button>
            </aside>
            <div id="sidebar-control">
                <div class="resizer"></div>
            </div>
            <div id="content">
                <div class="content-header">
                    <input id="pagetitle" type="text">
                </div>
                <div id="editor"></div>
            </div>`;
        this.#aside = this.querySelector('aside') as HTMLElement;
        this.#ulpages = this.querySelector('#pages') as HTMLElement;
        this.#pagetitle = this.querySelector('#pagetitle') as HTMLInputElement;
        this.#editor = this.querySelector('#editor') as HTMLElement;
        this.#resizer = this.querySelector(".resizer") as HTMLElement;

        if (!this.#quill) {
            this.#quill = new Quill(this.#editor, {
                modules: {
                    toolbar: [
                        ['bold', 'italic', 'underline'],
                        ['image'],
                        ['code-block'],
                        ['formula'],
                        ['clean']
                    ],
                },
                theme: "bubble"
            });
            markdownBehaviour(this.#quill);
            let saveTimer: ReturnType<typeof setTimeout> | null = null;
            this.#quill.on("text-change", () => {
                if (saveTimer) clearTimeout(saveTimer);
                saveTimer = setTimeout(() => this.savePage(), 500);
            });
            //todo: slows down
            this.shiftQuillTooltip();
        }
    }

    async init(notebook: Notebook): Promise<void> {
        this.#notebook = notebook;
        //todo: select last opened page
        this.#selected = this.#notebook?.pages.length > 0 ? this.#notebook.pages[0].id : null;
        await this.render();
    }

    async render(): Promise<void> {
        this.updatePages();
        this.bindEvents();
        //todo: handle empty notebook
        this.updateTitle();
        this.updateEditor();
    }

    updatePages(): void {
        if (!this.#notebook) return;
        this.#ulpages!.innerHTML = "";
        const addPageList = (pages: Page[], depth: number = 0): void => {
            pages.forEach(page => {
                const li = document.createElement("li");
                li.dataset.id = String(page.id);
                li.dataset.depth = String(depth);
                li.dataset.haschildren = String(page.children && page.children.length > 0);

                const isSelected = page.id === this.#selected ? "selected" : "";
                const hasChildren = page.children && page.children.length > 0;
                const collapseSymbol = "›";
                const collapseHidden = hasChildren ? "" : "hide";
                const expanded = this.#expanded?.[page.id];
                const collapseClass = expanded ? "expanded" : "";

                li.innerHTML = `
                    <div class="page-row ${isSelected}" data-depth="${depth}">
                        <div class="left">
                            <button class="collapse ${collapseHidden} ${collapseClass}">${collapseSymbol}</button>
                            <span class="title">${escapeHTML(page.title)}</span>
                        </div>
                        <div class="right">
                            <button class="drag-handle" draggable="true">⠿</button>
                            <button class="addchild">＋</button>
                            <button class="delete">✕</button>
                        </div>
                    </div>
                `;
                const wrapper = li.firstElementChild as HTMLElement;
                wrapper.style.setProperty('--depth', String(depth));

                this.#ulpages!.appendChild(li);
                if (hasChildren && expanded) {
                    addPageList(page.children, depth + 1);
                }
            });
        };
        addPageList(this.#notebook.pages, 0);
    }

    collapseSidebar(): void {
        this.#aside!.classList.add("hidden");
        this.#aside!.style.width = "0px";
        this.#resizer!.classList.add("sidebar-collapsed");
    }

    expandSidebar(): void {
        this.#aside!.classList.remove("hidden");
        this.#aside!.style.width = this.#sidebarDefaultWidth;
        this.#resizer!.classList.remove("sidebar-collapsed");
    }

    bindEvents(): void {
        this.unbindEvents();

        this.#resizer!.onmousedown = (e: MouseEvent) => {
            if (this.#aside!.classList.contains("hidden")) {
                this.expandSidebar();
                return;
            }
            this.#isResizing = true;
            this.#resizingStartX = e.clientX;
            this.#resizingStartWidth = parseInt(window.getComputedStyle(this.#aside!).width, 10);
            document.body.style.userSelect = "none";
        };

        this.onmousemove = (e: MouseEvent) => {
            if (!this.#isResizing) return;
            const delta = e.clientX - this.#resizingStartX;
            const newWidth = this.#resizingStartWidth + delta;
            const nearLeftEdge = e.clientX <= this.#collapseEdgeThreshold;
            if (newWidth <= this.#sidebarMinWidth && nearLeftEdge) {
                this.collapseSidebar();
                this.#isResizing = false;
                document.body.style.userSelect = "";
                return;
            }
            if (newWidth > this.#sidebarMinWidth) {
                this.#aside!.style.width = newWidth + "px";
            } else {
                this.#aside!.style.width = this.#sidebarMinWidth + "px";
            }
        };

        this.onmouseup = () => {
            if (this.#isResizing) {
                this.#isResizing = false;
                this.#resizingStartX = 0;
                this.#resizingStartWidth = 0;
                document.body.style.userSelect = "";
            }
        };

        (this.querySelector("#newpage") as HTMLButtonElement).onclick = () => {
            this.createPage("New Page", null);
        };

        this.querySelectorAll<HTMLLIElement>("#pages li").forEach(li => {
            const id = parseInt(li.dataset.id!, 10);
            li.onclick = () => {
                this.savePage();
                if (this.#selected === id && li.dataset.haschildren === "true") {
                    if (id in this.#expanded) {
                        delete this.#expanded[id];
                    } else {
                        this.#expanded[id] = true;
                    }
                }
                this.#selected = id;
                this.render();
            };
            (li.querySelector(".delete") as HTMLButtonElement).onclick = async (e: MouseEvent) => {
                e.stopPropagation();
                await this.deletePage(id);
            };
            (li.querySelector(".addchild") as HTMLButtonElement).onclick = (e: MouseEvent) => {
                e.stopPropagation();
                this.createPage("New Page", id);
            };
            (li.querySelector(".collapse") as HTMLButtonElement).onclick = (e: MouseEvent) => {
                e.stopPropagation();
                if (id in this.#expanded) {
                    delete this.#expanded[id];
                } else {
                    this.#expanded[id] = true;
                }
                this.#selected = id;
                this.render();
            };
        });

        this.bindDragEvents();

        let renameTimer: ReturnType<typeof setTimeout> | null = null;
        this.#pagetitle!.oninput = () => {
            if (renameTimer) clearTimeout(renameTimer);
            renameTimer = setTimeout(() => { this.saveTitleChange(); }, 400);
        };
    }

    bindDragEvents(): void {
        const pages = this.querySelectorAll<HTMLLIElement>('#pages li');
        pages.forEach(li => {
            const handle = li.querySelector('.drag-handle') as HTMLElement;

            handle.ondragstart = (e: DragEvent) => {
                this.#dragState.draggedItem = li;
                li.classList.add('dragging');
                const liRect = li.getBoundingClientRect();
                const offsetX = e.clientX - liRect.left;
                const offsetY = e.clientY - liRect.top;
                e.dataTransfer!.setDragImage(li, offsetX, offsetY);
                e.dataTransfer!.effectAllowed = "move";
            };

            handle.ondragend = () => {
                this.querySelectorAll<HTMLLIElement>('#pages li').forEach(item => {
                    item.classList.remove('dragging', 'drag-over', 'drag-over-above', 'drag-over-below', 'drag-over-child');
                });
                this.handleDrop();
                this.#dragState = {
                    draggedItem: null,
                    dragOverItem: null,
                    dragPlacement: null
                };
            };

            li.ondragover = (e: DragEvent) => {
                e.preventDefault();
                if (li === this.#dragState.draggedItem) return;
                const rect = li.getBoundingClientRect();
                const y = e.clientY - rect.top;
                const h = rect.height;
                const edge = Math.min(20, h * 0.25);
                this.#dragState.dragOverItem = li;
                if (y < edge) {
                    this.#dragState.dragPlacement = 'above';
                } else if (y > h - edge) {
                    this.#dragState.dragPlacement = 'below';
                } else {
                    this.#dragState.dragPlacement = 'child';
                }
                this.updateDragFeedback();
            };

            li.ondragleave = () => {
                if (li === this.#dragState.dragOverItem) {
                    li.classList.remove('drag-over', 'drag-over-above', 'drag-over-below', 'drag-over-child');
                    this.#dragState.dragOverItem = null;
                    this.#dragState.dragPlacement = null;
                }
            };
        });
    }

    updateDragFeedback(): void {
        this.querySelectorAll<HTMLLIElement>('#pages li').forEach(item => {
            item.classList.remove('drag-over', 'drag-over-above', 'drag-over-below', 'drag-over-child');
        });
        if (!this.#dragState.dragOverItem) return;
        const targetLi = this.#dragState.dragOverItem;
        if (this.#dragState.dragPlacement === 'above') {
            targetLi.classList.add('drag-over-above');
        } else if (this.#dragState.dragPlacement === 'below') {
            targetLi.classList.add('drag-over-below');
        } else if (this.#dragState.dragPlacement === 'child') {
            targetLi.classList.add('drag-over-child');
        } else {
            targetLi.classList.add('drag-over');
        }
    }

    handleDrop(): void {
        if (!this.#dragState.dragOverItem || !this.#dragState.draggedItem) return;
        const draggedId = parseInt(this.#dragState.draggedItem.dataset.id!, 10);
        const targetId = parseInt(this.#dragState.dragOverItem.dataset.id!, 10);
        const placement = this.#dragState.dragPlacement!;
        this.movePage(draggedId, targetId, placement);
    }

    unbindEvents(): void {
        this.onclick = null;
        this.querySelectorAll('button').forEach(btn => btn.onclick = null);
        this.querySelectorAll<HTMLLIElement>("aside li").forEach(li => li.onclick = null);
    }

    shiftQuillTooltip(): void {
        const tooltip = this.#editor!.querySelector('.ql-tooltip') as HTMLElement;
        if (!tooltip) return;
        const observer = new MutationObserver(() => {
            const tooltipRect = tooltip.getBoundingClientRect();
            const editorRect = this.#editor!.getBoundingClientRect();
            let left = parseFloat(tooltip.style.left || "0");
            if (tooltipRect.left < editorRect.left) {
                left += editorRect.left - tooltipRect.left;
            } else if (tooltipRect.right > editorRect.right) {
                left -= tooltipRect.right - editorRect.right;
            }
            tooltip.style.left = left + "px";
        });
        observer.observe(tooltip, { attributes: true, attributeFilter: ['style'] });
    }

    updateTitle(): void {
        this.#pagetitle!.value = this.getPageTitleById(this.#selected) ?? "";
    }

    getPageTitleById(id: number | null, pages: Page[] | null = null): string | null {
        if (!id) return null;
        if (pages === null) pages = this.#notebook!.pages;
        for (const page of pages) {
            if (page.id === id) return page.title;
            if (page.children && page.children.length > 0) {
                const title = this.getPageTitleById(id, page.children);
                if (title) return title;
            }
        }
        return null;
    }

    async updateEditor(): Promise<void> {
        if (!this.#selected) return;
        const res = await fetch('/page?id=' + encodeURIComponent(this.#selected));
        if (res.ok) {
            const data = await res.json();
            this.#quill!.setContents(data.delta);
        } else {
            this.#quill!.setText('');
        }
    }

    async savePage(): Promise<void> {
        if (!this.#selected) return;
        const delta = this.#quill!.getContents();
        const res = await fetch('/save', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ id: this.#selected, delta: delta })
        });
        if (!res.ok) {
            console.error("Failed to auto-save page:", this.#selected);
        }
    }

    async createPage(title: string, parentId: number | null): Promise<void> {
        const res = await fetch('/create', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ title: title, parent: parentId || 0 })
        });
        if (!res.ok) {
            console.error("Failed to create page");
            return;
        }
        const data: CreatePageResponse = await res.json();
        if (parentId) this.#expanded[parentId] = true;
        this.#notebook = data.notebook;
        this.#selected = data.id;
        await this.render();
    }

    async deletePage(id: number): Promise<void> {
        if (!confirm(`Delete page "${this.getPageTitleById(id)}"?`)) return;
        const res = await fetch('/delete', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ id }),
        });
        if (!res.ok) {
            console.error("Failed to delete page");
            return;
        }
        const data: Notebook = await res.json();
        this.#notebook = data;
        this.#selected = null;
        await this.render();
    }

    async saveTitleChange(): Promise<void> {
        const newTitle = this.#pagetitle!.value.trim();
        const oldTitle = this.getPageTitleById(this.#selected);
        if (!newTitle || newTitle === oldTitle) return;

        const res = await fetch('/rename', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ id: this.#selected, title: newTitle })
        });
        if (!res.ok) {
            console.error("Rename failed");
            this.#pagetitle!.value = oldTitle ?? "";
            return;
        }
        const data: Notebook = await res.json();
        this.#notebook = data;
        this.render();
    }

    async movePage(draggedId: number, targetId: number, placement: string): Promise<void> {
        const res = await fetch('/move', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                draggedId: draggedId,
                targetId: targetId,
                placement: placement
            })
        });
        if (!res.ok) {
            console.error("Failed to move page");
            return;
        }
        const data: Notebook = await res.json();
        this.#notebook = data;
        this.render();
    }
}








function escapeHTML(str: string): string {
    const div = document.createElement('div');
    div.textContent = str;
    return div.innerHTML;
}

customElements.define('x-main', XMain);
customElements.define('x-login', XLogin);
customElements.define('x-scrawl', XScrawl);
