// Backend contract. The web build talks to the Go HTTP server with fetch, the
// wails build calls the bound ScrawlService (bindings/). esbuild --define:__DESKTOP__
// picks one at build time and drops the other from the bundle.

export interface Page {
    id: number;
    title: string;
    position: number;
    children: Page[];
}

export interface Notebook {
    pages: Page[];
}

export interface CreatePageResponse {
    id: number;
    notebook: Notebook;
}

// Thrown by implementations on failure. status 401 means login is required.
export class ApiError extends Error {
    constructor(public status: number, message = `HTTP ${status}`) {
        super(message);
    }
}

export interface Api {
    getNotebook(): Promise<Notebook>;
    readPage(id: number): Promise<unknown>; // Quill delta
    savePage(id: number, delta: unknown): Promise<void>;
    createPage(title: string, parent: number): Promise<CreatePageResponse>;
    deletePage(id: number): Promise<Notebook>;
    renamePage(id: number, title: string): Promise<Notebook>;
    movePage(draggedId: number, targetId: number, placement: string): Promise<Notebook>;
    login?(password: string): Promise<void>; // web only
}

const makeWebApi = (): Api => {
    async function http<T>(path: string, body?: unknown): Promise<T> {
        const res = await fetch(path, body === undefined ? undefined : {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body),
        });
        if (!res.ok) throw new ApiError(res.status);
        const ct = res.headers.get('Content-Type') ?? '';
        return (ct.includes('application/json') ? res.json() : undefined) as Promise<T>;
    }
    return {
        getNotebook: () => http<Notebook>('/pages'),
        readPage: async (id) => (await http<{ delta: unknown }>('/page?id=' + encodeURIComponent(id))).delta,
        savePage: (id, delta) => http<void>('/save', { id, delta }),
        createPage: (title, parent) => http<CreatePageResponse>('/create', { title, parent }),
        deletePage: (id) => http<Notebook>('/delete', { id }),
        renamePage: (id, title) => http<Notebook>('/rename', { id, title }),
        movePage: (draggedId, targetId, placement) =>
            http<Notebook>('/move', { draggedId, targetId, placement }),
        login: (password) => http<void>('/login', { password }),
    };
};

const makeWailsApi = (): Api => {
    // bindings/ is generated and only exists for the wails build, so web tsc must not check it
    // @ts-ignore
    const S = () => import('./bindings/scrawl/scrawlservice');
    // The generator types Go's []*Page as (Page | null)[] and pointer returns as T | null.
    // The Go side never returns nil elements, so cast to the contract types here.
    const nb = (v: unknown) => v as Notebook;
    return {
        getNotebook: async () => nb(await (await S()).GetNotebook()),
        readPage: async (id) => JSON.parse(await (await S()).ReadPage(id)),
        savePage: async (id, delta) => (await S()).SavePage(id, JSON.stringify(delta)),
        createPage: async (title, parent) =>
            (await (await S()).CreatePage(title, parent)) as unknown as CreatePageResponse,
        deletePage: async (id) => nb(await (await S()).DeletePage(id)),
        renamePage: async (id, title) => nb(await (await S()).RenamePage(id, title)),
        movePage: async (d, t, p) => nb(await (await S()).MovePage(d, t, p)),
    };
};

export const api: Api = __DESKTOP__ ? makeWailsApi() : makeWebApi();
