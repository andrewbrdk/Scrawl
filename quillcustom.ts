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
        escapeDollarRule,
        heading3Rule,
        heading2Rule,
        heading1Rule,
        listRule,
        boldRule,
        inlineCodeRule,
        codeBlockRule,
        mathInlineRule,
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

        const range = quill.getSelection();
        if (!range) return;

        const [line, offset] = quill.getLine(range.index);
        const lineStart = range.index - offset;
        const lineText = quill.getText(lineStart, offset);

        const ctx: Context = {
            cursor: range.index,
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

    quill.on('selection-change', (range: any, oldRange: any, source: any) => {
        if (!range || range.length || source !== 'user') return;
        const [line] = quill.getLine(range.index);
        if (!(line instanceof MathBlock)) return;
        const textarea = line.domNode.querySelector('.math-editor') as HTMLTextAreaElement;
        if (!textarea || document.activeElement === textarea) return;
        line.expand();
        requestAnimationFrame(() => {
            textarea.focus();
            textarea.setSelectionRange(0, 0);
        });
    });

    quill.keyboard.addBinding({ key: 'ArrowDown' }, (range: any) => {
        if (!range) return true;
        const [currentLine, offset] = quill.getLine(range.index);
        if (!currentLine) return true;
        const nextIndex = range.index + (currentLine.length() - offset);
        const [nextLine] = quill.getLine(nextIndex);
        if (nextLine instanceof MathBlock) {
            const textarea = nextLine.domNode.querySelector('.math-editor') as HTMLTextAreaElement;
            if (textarea) {
                nextLine.expand();
                requestAnimationFrame(() => {
                    textarea.focus();
                    textarea.setSelectionRange(0, 0);
                });
                return false;
            }
        }
        return true;
    });

    quill.keyboard.addBinding({ key: 'ArrowLeft' }, (range: any) => {
        if (!range) return true;
        const index = range.index;
        const [prevLine] = quill.getLine(index - 1);
        if (prevLine instanceof MathBlock) {
            const textarea = prevLine.domNode.querySelector('.math-editor') as HTMLTextAreaElement;
            if (textarea) {
                prevLine.expand();
                quill.setSelection(index - 1, 0, 'silent');
                requestAnimationFrame(() => {
                    textarea.focus();
                    textarea.setSelectionRange(textarea.value.length, textarea.value.length);
                });
                return false;
            }
        }
        return true;
    });

    quill.keyboard.addBinding({ key: 'ArrowUp' }, (range: any) => {
        if (!range) return true;
        const [currentLine, offset] = quill.getLine(range.index);
        if (!currentLine) return true;
        const prevIndex = range.index - offset - 1;
        if (prevIndex < 0) return true;
        const [prevLine] = quill.getLine(prevIndex);
        if (prevLine instanceof MathBlock) {
            const textarea = prevLine.domNode.querySelector('.math-editor') as HTMLTextAreaElement;
            if (textarea) {
                prevLine.expand();
                requestAnimationFrame(() => {
                    textarea.focus();
                    textarea.setSelectionRange(textarea.value.length, textarea.value.length);
                });
                return false;
            }
        }
        return true;
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

const escapeDollarRule: Rule = {
    name: 'escape-dollar',

    shouldRun: (ctx) => {
        const t = ctx.lineText;
        return t.length >= 2 &&
        t[t.length - 2] === '\\' &&
        t[t.length - 1] === '$';
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

const mathInlineRule: Rule = {                                                                                             
    name: 'math-inline',                                                                                                   
                                                                                                                           
    shouldRun: (ctx) => {                                                                                                  
        if (ctx.symbol !== '$') return false;                                                                              
        const text = ctx.lineText;                                                                                         
        return text.length >= 2 && text[text.length - 1] === '$' && text[text.length - 2] === '$';                         
    },                                                                                                                     
                                                                                                                           
    run: (quill, ctx) => {                                                                                                 
        const absStart = ctx.lineStart + ctx.lineText.length - 2;                                                          
        quill.deleteText(absStart, 2);                                                                                     
        quill.insertEmbed(absStart, 'math-inline', { latex: '' }, 'user');                                                 
        quill.setSelection(absStart + 1, 0, 'user');                                                                       
        const node = quill.getLeaf(absStart + 1)[0]?.domNode?.closest('.ql-math-inline') as HTMLElement;                   
        if (node) {                                                                                                        
            const input = node.querySelector('.math-inline-editor') as HTMLInputElement;                                   
            if (input) {                                                                                                   
                input.hidden = false;                                                                                      
                input.style.width = '1ch';                                                                                 
                input.focus();                                                                                             
            }                                                                                                              
        }                                                                                                                  
    }                                                                                                                      
};

const mathBlockRule: Rule = {
    name: 'math-block',

    shouldRun: (ctx) => {
        return ctx.symbol === '$' && ctx.lineText === '';
    },

    run: (quill, ctx) => {
        quill.deleteText(ctx.cursor, 1);
        quill.insertEmbed(
            ctx.lineStart,
            'math-block',
            { latex: '' },
            'user'
        );
        quill.setSelection(ctx.lineStart + 1, 0, 'silent');
    }
};


const BlockEmbed = Quill.import('blots/block/embed') as any;
const InlineEmbed = Quill.import('blots/embed') as any;

class MathInline extends InlineEmbed {                                                                                     
    static blotName = 'math-inline';                                                                                       
    static tagName = 'span';                                                                                               
    static className = 'ql-math-inline';                                                                                   
                                                                                                                           
    static create(value: { latex: string }) {                                                                              
        const node = super.create() as HTMLElement;                                                                        
        const latex = value?.latex || '';                                                                                  
        node.dataset.latex = latex;                                                                                        
        node.setAttribute('contenteditable', 'false');                                                                     
                                                                                                                           
        const preview = document.createElement('span');                                                                    
        preview.className = 'math-inline-preview';                                                                         
        MathInline.render(preview, latex);                                                                                 
                                                                                                                           
        const input = document.createElement('input');                                                                     
        input.type = 'text';                                                                                               
        input.className = 'math-inline-editor';                                                                            
        input.value = latex;                                                                                               
        input.hidden = true;                                                                                               
                                                                                                                           
        node.appendChild(input); 
        node.appendChild(preview);                                                                                                                                                          
                                                                                                                           
        node.addEventListener('click', () => {                                                                             
            input.hidden = false;                                                                                          
            input.style.width = Math.max(input.value.length, 1) + 'ch';                                                    
            input.focus();                                                                                                 
            input.select();                                                                                                
        });                                                                                                                
                                                                                                                           
        input.addEventListener('blur', () => {                                                                             
            input.hidden = true;                                                                                           
        });                                                                                                                
                                                                                                                           
        input.addEventListener('mousedown', (e) => e.stopPropagation());                                                   
        input.addEventListener('click', (e) => e.stopPropagation());                                                       
        input.addEventListener('keydown', (e) => {                                                                         
            e.stopPropagation();                                                                                           
            if (e.key === 'Escape' || e.key === 'Enter') {                                                                 
                input.blur();                                                                                              
            }                                                                                                              
        });                                                                                                                
                                                                                                                           
        input.addEventListener('input', () => {                                                                            
            input.style.width = Math.max(input.value.length, 1) + 'ch';                                                    
            node.dataset.latex = input.value;                                                                              
            MathInline.render(preview, input.value);                                                                       
        });                                                                                                                
                                                                                                                           
        return node;                                                                                                       
    }                                                                                                                      
                                                                                                                           
    static value(node: HTMLElement) {                                                                                      
        return { latex: node.dataset.latex || '' };                                                                        
    }                                                                                                                      
                                                                                                                           
    static render(container: HTMLElement, latex: string) {                                                                 
        try {                                                                                                              
            katex.render(latex, container, { throwOnError: false, displayMode: false });                                   
        } catch {                                                                                                          
            container.innerText = latex;                                                                                   
        }                                                                                                                  
    }                                                                                                                      
} 

class MathBlock extends BlockEmbed {
    static blotName = 'math-block';
    static tagName = 'div';
    static className = 'ql-math-block';

    static create(value: { latex: string }) {
        const node = super.create() as HTMLElement;
        const latex = value?.latex || '';
        node.dataset.latex = latex;
        node.setAttribute('contenteditable', 'false');

        const preview = document.createElement('div');
        preview.className = 'math-preview';
        MathBlock.render(preview, latex);

        const textarea = document.createElement('textarea');
        textarea.className = 'math-editor';
        textarea.value = latex;

        node.appendChild(preview);
        node.appendChild(textarea);
        node.classList.add('math-collapsed');

        return node;
    }

    static value(node: HTMLElement) {
        return { latex: node.dataset.latex || '' };
    }

    static render(container: HTMLElement, latex: string) {
        try {
            katex.render(latex, container, { throwOnError: false });
        } catch {
            container.innerText = latex;
        }
    }

    expand(): void {
        const node = this.domNode as HTMLElement;
        const textarea = node.querySelector('.math-editor') as HTMLTextAreaElement;
        if (!textarea) return;
        node.classList.remove('math-collapsed');
        textarea.style.height = 'auto';
        textarea.style.height = textarea.scrollHeight + 'px';
    }

    attach() {
        super.attach();

        const node = this.domNode as HTMLElement;
        const textarea = node.querySelector('.math-editor') as HTMLTextAreaElement;
        const preview = node.querySelector('.math-preview') as HTMLElement;
        if (!textarea || !preview) return;

        node.addEventListener('click', () => {
            this.expand();
            requestAnimationFrame(() => {
                textarea.focus();
                textarea.setSelectionRange(textarea.value.length, textarea.value.length);
            });
        });

        textarea.addEventListener('blur', () => {
            node.classList.add('math-collapsed');
        });

        textarea.addEventListener('mousedown', (e) => e.stopPropagation());
        textarea.addEventListener('click', (e) => e.stopPropagation());
        textarea.addEventListener('keyup', (e) => e.stopPropagation());
        textarea.addEventListener('keydown', (e) => {
            e.stopPropagation();
            if (e.key === 'Escape') {
                node.classList.add('math-collapsed');
                const quillInstance = Quill.find(node.closest('.ql-editor')!.parentElement!);
                if (quillInstance) {
                    const blot = Quill.find(node);
                    const index = (quillInstance as any).getIndex(blot);
                    (quillInstance as any).setSelection(index + 1, 0, 'user');
                }
            }
            if (e.key === 'ArrowDown' || e.key === 'ArrowRight') {
                if (textarea.selectionStart === textarea.value.length) {
                    const quillInstance = Quill.find(node.closest('.ql-editor')!.parentElement!);
                    if (quillInstance) {
                        const blot = Quill.find(node);
                        const index = (quillInstance as any).getIndex(blot);
                        (quillInstance as any).setSelection(index + 1, 0, 'user');
                    }
                }
            }
            if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') {
                if (textarea.selectionStart === 0) {
                    const quillInstance = Quill.find(node.closest('.ql-editor')!.parentElement!);
                    if (quillInstance) {
                        const blot = Quill.find(node);
                        const index = (quillInstance as any).getIndex(blot);
                        (quillInstance as any).setSelection(index, 0, 'user');
                    }
                }
            }
        });

        textarea.addEventListener('input', () => {
            textarea.style.height = 'auto';
            textarea.style.height = textarea.scrollHeight + 'px';
            node.dataset.latex = textarea.value;
            MathBlock.render(preview, textarea.value);
            const quillInstance = Quill.find(node.closest('.ql-editor')!.parentElement!);
            if (quillInstance) {
                (quillInstance as any).update('user');
            }
        });

        if (!textarea.value) {
            this.expand();
            requestAnimationFrame(() => {
                textarea.focus();
                textarea.setSelectionRange(textarea.value.length, textarea.value.length);
            });
        }
    }
}


let registered = false;

export function setupQuill() {
    if (registered) return;

    Quill.register(MathBlock);
    Quill.register(MathInline);

    registered = true;
}
