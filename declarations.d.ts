declare module '*.css' {
    const content: string;
    export default content;
}

// Set by esbuild --define: false for the web bundle, true for the wails bundle.
declare const __DESKTOP__: boolean;

interface Window {                                                                                                         
    katex: any;                                                                                         
}