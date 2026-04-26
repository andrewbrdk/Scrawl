import Quill from 'quill';
import katex from 'katex';
import 'katex/dist/katex.min.css';

type Context = {
    cursor: number;
    lineStart: number;
    lineText: string;
    symbol?: string;
};

type Rule = {
    name: string;
    shouldRun: (ctx: Context) => boolean;
    run: (quill: any, ctx: Context) => void;
};

export function markdownBehaviour(quill: any) {
    const rules: Rule[] = [
        escapeStarRule,
        escapeBacktickRule,
        heading3Rule,
        heading2Rule,
        heading1Rule,
        listRule,
        boldRule,
        inlineCodeRule,
        codeBlockRule,
        mathBlockRule
    ];

    quill.on('text-change', (delta: any, _old: any, source: string) => {
        if (source !== 'user') return;

        let symbol: string | undefined;

        for (const op of delta.ops || []) {
            if (typeof op.insert === 'string' && op.insert.length > 0) {
                symbol = op.insert[op.insert.length - 1];
            }
        }
        if (!symbol || !['*', ' ', '`', '$'].includes(symbol)) return;

        const sel = quill.getSelection();
        if (!sel) return;

        const cursor = sel.index;
        const textBefore = quill.getText(0, cursor);
        const lineStart = textBefore.lastIndexOf('\n') + 1;
        const lineText = textBefore.slice(lineStart);
        const ctx: Context = {
            cursor,
            lineStart,
            lineText,
            symbol,
        };

        for (const rule of rules) {
            if (rule.shouldRun(ctx)) {
                rule.run(quill, ctx);
                break;
            }
        }
    });
}

const escapeStarRule: Rule = {
    name: 'escape-star',

    shouldRun: (ctx) => {
        const t = ctx.lineText;
        return t.length >= 2 &&
        t[t.length - 2] === '\\' &&
        t[t.length - 1] === '*';
    },

    run: (quill, ctx) => {
        quill.deleteText(ctx.cursor - 2, 1);
        quill.setSelection(ctx.cursor - 1, 0);
    }
};

const escapeBacktickRule: Rule = {
    name: 'escape-backtick',

    shouldRun: (ctx) => {
        const t = ctx.lineText;
        return t.length >= 2 &&
        t[t.length - 2] === '\\' &&
        t[t.length - 1] === '`';
    },

    run: (quill, ctx) => {
        quill.deleteText(ctx.cursor - 2, 1);
        quill.setSelection(ctx.cursor - 1, 0);
    }
};

const heading3Rule: Rule = {
    name: 'heading3',

    shouldRun: (ctx) => {
        return ctx.lineText === '### ';
    },

    run: (quill, ctx) => {
        quill.deleteText(ctx.lineStart, 4);
        quill.formatLine(ctx.lineStart, 1, 'header', 3);
    }
};

const heading2Rule: Rule = {
    name: 'heading2',

    shouldRun: (ctx) => {
        return ctx.lineText === '## ';
    },

    run: (quill, ctx) => {
        quill.deleteText(ctx.lineStart, 3);
        quill.formatLine(ctx.lineStart, 1, 'header', 2);
    }
};

const heading1Rule: Rule = {
    name: 'heading1',

    shouldRun: (ctx) => {
        return ctx.lineText === '# ';
    },

    run: (quill, ctx) => {
        quill.deleteText(ctx.lineStart, 2);
        quill.formatLine(ctx.lineStart, 1, 'header', 1);
    }
};

const listRule: Rule = {
    name: 'list',

    shouldRun: (ctx) => {
        return ctx.lineText === '* ';
    },

    run: (quill, ctx) => {
        quill.deleteText(ctx.lineStart, 2);
        quill.formatLine(ctx.lineStart, 1, 'list', 'bullet');
    }
};

const boldRule: Rule = {
    name: 'bold',

    shouldRun: (ctx) => {
        const text = ctx.lineText;

        if (text.length < 3) return false;
        if (text[text.length - 1] !== '*') return false;

        let i = text.length - 2;
        while (i >= 0) {
            if (text[i] === '*') break;
            i--;
        }

        if (i < 0) return false;

        const contentLength = text.length - i - 2;
        return contentLength > 0;
    },

    run: (quill, ctx) => {
        const text = ctx.lineText;

        let i = text.length - 2;
        while (i >= 0) {
            if (text[i] === '*') break;
            i--;
        }

        const startOffset = i;
        const contentLength = text.length - i - 2;

        const absStart = ctx.lineStart + startOffset;
        const end = ctx.cursor - 1;
        quill.deleteText(end, 1);
        quill.deleteText(absStart, 1);
        quill.formatText(absStart, contentLength, 'bold', true);
        quill.setSelection(absStart + contentLength, 0);
        quill.format('bold', false);
    }
};

const inlineCodeRule: Rule = {
    name: 'inline-code',

    shouldRun: (ctx) => {
        const text = ctx.lineText;
        if (text.length < 3) return false;
        if (text[text.length - 1] !== '`') return false;
        let i = text.length - 2;
        while (i >= 0) {
            if (text[i] === '`') break;
            i--;
        }
        if (i < 0) return false;
        const contentLength = text.length - i - 2;
        return contentLength > 0;
    },

    run: (quill, ctx) => {
        const text = ctx.lineText;
        let i = text.length - 2;
        while (i >= 0) {
            if (text[i] === '`') break;
            i--;
        }
        const startOffset = i;
        const contentLength = text.length - i - 2;
        const absStart = ctx.lineStart + startOffset;
        const end = ctx.cursor - 1;
        quill.deleteText(end, 1);
        quill.deleteText(absStart, 1);
        quill.formatText(absStart, contentLength, 'code', true);
        const newCursor = absStart + contentLength;
        quill.setSelection(newCursor, 0);
        quill.format('code', false);
    }
};

const codeBlockRule: Rule = {
    name: 'code-block',

    shouldRun: (ctx) => {
        return ctx.symbol === '`' && ctx.lineText === '';
    },

    run: (quill, ctx) => {
        quill.deleteText(ctx.cursor, 1);
        quill.formatLine(ctx.lineStart, 1, 'code-block', true);
    }
};

const mathBlockRule: Rule = {
    name: 'math-block',

    shouldRun: (ctx) => {
        return ctx.symbol === '$' && ctx.lineText === '';
    },

    run: (quill, ctx) => {
        quill.deleteText(ctx.cursor, 1);
        quill.insertEmbed(ctx.lineStart, 'math-block', { latex: '', mode: 'edit' }, 'user');
        quill.setSelection(ctx.lineStart + 1, 0);
    }
};


const BlockEmbed = Quill.import('blots/block/embed') as any;

class MathBlock extends BlockEmbed {
    static blotName = 'math-block';
    static tagName = 'div';
    static className = 'ql-math-block';

    static create(value: { latex: string; mode?: string }) {
        const node = super.create();
        node.setAttribute('data-value', value.latex || '');
        node.setAttribute('contenteditable', 'false');
        if (value.mode === 'edit') {
            node.setAttribute('data-pending-edit', 'true');
        }
        MathBlock.renderKatex(node, value.latex || '');
        return node;
    }

    static renderKatex(node: HTMLElement, latex: string) {
        node.innerHTML = '';
        if (latex.trim()) {
            katex.render(latex, node, { throwOnError: false });
        } else {
            node.innerHTML = '<span class="math-empty">Empty equation</span>';
        }
    }

    static value(node: HTMLElement) {
        const textarea = node.querySelector('textarea');
        return { latex: textarea ? textarea.value : node.getAttribute('data-value') || '' };
    }

    attach() {
        super.attach();
        const node = this.domNode;
        const quill = Quill.find(node.closest('.ql-container')) as any;

        node.onclick = () => {
            if (node.getAttribute('data-mode') !== 'edit') {
                this.enterEditMode(quill);
            }
        };

        if (node.getAttribute('data-pending-edit') === 'true') {
            node.removeAttribute('data-pending-edit');
            setTimeout(() => this.enterEditMode(quill), 0);
        }
    }

    enterEditMode(quill: any) {
        const node = this.domNode;
        const latex = node.getAttribute('data-value') || '';
        node.setAttribute('data-mode', 'edit');
        node.innerHTML = `<textarea class="math-editor" placeholder="LaTeX...">${latex}</textarea>`;

        const textarea = node.querySelector('textarea')!;

        const resize = () => {
            textarea.style.height = 'auto';
            textarea.style.height = textarea.scrollHeight + 'px';
        };

        textarea.oninput = resize;

        setTimeout(() => {
            textarea.focus();
            textarea.setSelectionRange(latex.length, latex.length);
            resize();
        }, 0);

        const commit = () => {
            if (node.getAttribute('data-mode') !== 'edit') return;
            const value = textarea.value;
            node.setAttribute('data-value', value);
            node.setAttribute('data-mode', 'render');
            MathBlock.renderKatex(node, value);
            const blot = Quill.find(node);
            if (blot && quill) {
                quill.setSelection(quill.getIndex(blot) + 1, 0, 'silent');
            }
        };

        textarea.onkeydown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                e.preventDefault();
                commit();
            }
        };

        textarea.onblur = commit;
    }
}


let registered = false;

export function setupQuill() {
    if (registered) return;

    Quill.register(MathBlock);

    registered = true;
}
