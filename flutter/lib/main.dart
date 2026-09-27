import 'dart:io';
import 'dart:async';
import 'dart:convert';
import 'dart:developer' as developer;
import 'package:path_provider/path_provider.dart';
import 'package:flutter/material.dart';
import 'package:sqlite3/sqlite3.dart' as sqlite;
import 'package:flutter_quill/flutter_quill.dart';
import 'package:flutter_quill_extensions/flutter_quill_extensions.dart';
import 'math_embeds.dart';

class Notebook {
  final String name;
  List<Page> pages;

  Notebook({
    required this.name,
    required this.pages,
  });

  Notebook loadNotebook(sqlite.Database db) {
    final result = db.select('''
      SELECT
        p.id,
        p.title,
        pt.parent_id,
        pt.position
      FROM pages p
      LEFT JOIN pagetree pt
        ON p.id = pt.child_id
      WHERE p.id != 0
      ORDER BY pt.parent_id, pt.position, p.id
    ''');

    final Map<int, Page> pages = {};
    final List<Page> top = [];
    final Map<int, List<Page>> children = {};

    for (final row in result) {
      final int id = row['id'] as int;
      final String title = row['title'] as String;
      final int? parentId = row['parent_id'] as int?;
      final int position = row['position'] as int? ?? 0;
      final page = Page(
        id: id,
        title: title,
        position: position,
        children: [],
      );
      pages[id] = page;
      if (parentId == null || parentId == 0) {
        top.add(page);
      } else {
        children.putIfAbsent(parentId, () => []);
        children[parentId]!.add(page);
      }
    }

    int pageOrder(Page a, Page b) {
      if (a.position != b.position) return a.position.compareTo(b.position);
      return a.id.compareTo(b.id);
    }

    top.sort(pageOrder);

    for (final entry in children.entries) {
      entry.value.sort(pageOrder);
      final parent = pages[entry.key];
      parent?.children.addAll(entry.value);
    }

    return Notebook(name: name, pages: top);
  }

  Page? findPage(int id) {
    for (final page in pages) {
      final found = page.findPage(id);
      if (found != null) return found;
    }
    return null;
  }

  bool savePage(sqlite.Database db, Page page, String delta) {
    final success = page.savePageContent(db, delta);
    if (!success) return false;
    pages = loadNotebook(db).pages;
    return true;
  }

  bool renamePage(sqlite.Database db, int id, String newTitle) {
    final stmt = db.prepare('''
      UPDATE pages
      SET title = ?, updated = CURRENT_TIMESTAMP
      WHERE id = ?
    ''');
    try {
      stmt.execute([newTitle, id]);
    } catch (e) {
      return false;
    } finally {
      stmt.close();
    }
    pages = loadNotebook(db).pages;
    return true;
  }

  int? createPage(sqlite.Database db, String title, {int parentId = 0}) {
    const emptyDelta = '{"ops":[{"insert":"\\n"}]}';

    try {
      db.execute('BEGIN');

      int position = 0;
      final posResult = db.select(
        'SELECT MAX(position) as maxpos FROM pagetree WHERE parent_id = ?',
        [parentId],
      );
      if (posResult.isNotEmpty) {
        final maxPos = posResult.first['maxpos'];
        if (maxPos != null) position = (maxPos as int) + 1;
      }

      final insertPage = db.prepare('''
        INSERT INTO pages(title, delta, created, updated)
        VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
      ''');
      insertPage.execute([title, emptyDelta]);
      insertPage.close();
      final newId = db.lastInsertRowId;

      final insertTree = db.prepare('''
        INSERT INTO pagetree(parent_id, child_id, position)
        VALUES (?, ?, ?)
      ''');
      insertTree.execute([parentId, newId, position]);
      insertTree.close();

      db.execute('COMMIT');

      pages = loadNotebook(db).pages;
      return newId;
    } catch (e) {
      db.execute('ROLLBACK');
      return null;
    }
  }

  bool deletePage(sqlite.Database db, int id) {
    try {
      db.execute('BEGIN');

      int? parentId;
      int? position;
      final parentResult = db.select(
        'SELECT parent_id, position FROM pagetree WHERE child_id = ?',
        [id],
      );
      if (parentResult.isNotEmpty) {
        parentId = parentResult.first['parent_id'] as int;
        position = parentResult.first['position'] as int;
      }

      db.execute('''
        WITH RECURSIVE descendants(id) AS (
          SELECT ?
          UNION ALL
          SELECT child_id
          FROM pagetree
          JOIN descendants
            ON pagetree.parent_id = descendants.id
        )
        DELETE FROM pages
        WHERE id IN (SELECT id FROM descendants)
      ''', [id]);

      if (parentId != null && position != null) {
        db.execute('''
          UPDATE pagetree
          SET position = position - 1
          WHERE parent_id = ? AND position > ?
        ''', [parentId, position]);
      }

      db.execute('COMMIT');

      pages = loadNotebook(db).pages;
      return true;
    } catch (e) {
      db.execute('ROLLBACK');
      return false;
    }
  }

  bool movePage(sqlite.Database db, int draggedId, int targetId, String placement) {
    final descendants = db.select('''
      WITH RECURSIVE descendants(id) AS (
        SELECT ? AS id
        UNION ALL
        SELECT child_id AS id
        FROM pagetree
        JOIN descendants ON pagetree.parent_id = descendants.id
      )
      SELECT id FROM descendants WHERE id = ?
    ''', [draggedId, targetId]);
    if (descendants.isNotEmpty) return false;

    try {
      db.execute('BEGIN');

      int oldParentId = 0;
      int oldPosition = 0;
      final oldResult = db.select(
        'SELECT parent_id, position FROM pagetree WHERE child_id = ?',
        [draggedId],
      );
      if (oldResult.isNotEmpty) {
        oldParentId = oldResult.first['parent_id'] as int;
        oldPosition = oldResult.first['position'] as int;
      }

      int newParentId = 0;
      int newPosition = 0;
      if (placement == 'child') {
        newParentId = targetId;
        final maxResult = db.select(
          'SELECT MAX(position) as maxpos FROM pagetree WHERE parent_id = ?',
          [targetId],
        );
        if (maxResult.isNotEmpty && maxResult.first['maxpos'] != null) {
          newPosition = (maxResult.first['maxpos'] as int) + 1;
        }
      } else {
        final targetResult = db.select(
          'SELECT parent_id, position FROM pagetree WHERE child_id = ?',
          [targetId],
        );
        if (targetResult.isNotEmpty) {
          newParentId = targetResult.first['parent_id'] as int;
          final targetPosition = targetResult.first['position'] as int;
          newPosition = placement == 'above' ? targetPosition : targetPosition + 1;
        }
      }

      db.execute(
        'UPDATE pagetree SET parent_id = ?, position = ? WHERE child_id = ?',
        [newParentId, newPosition, draggedId],
      );
      db.execute(
        'UPDATE pagetree SET position = position - 1 WHERE parent_id = ? AND position >= ? AND child_id != ?',
        [oldParentId, oldPosition, draggedId],
      );
      db.execute(
        'UPDATE pagetree SET position = position + 1 WHERE parent_id = ? AND position >= ? AND child_id != ?',
        [newParentId, newPosition, draggedId],
      );

      db.execute('COMMIT');
      pages = loadNotebook(db).pages;
      return true;
    } catch (e) {
      db.execute('ROLLBACK');
      return false;
    }
  }
}

class Page {
  final int id;
  String title;
  final int position;
  List<Page> children;

  Page({
    required this.id,
    required this.title,
    required this.position,
    required this.children,
  });

  Page? findPage(int id) {
    if (id == this.id) return this;
    for (final child in children) {
      final found = child.findPage(id);
      if (found != null) return found;
    }
    return null;
  }

  String? readPage(sqlite.Database db) {
    try {
      final stmt = db.prepare('SELECT delta FROM pages WHERE id = ?');
      final result = stmt.select([id]);
      stmt.close();
      if (result.isEmpty) return null;
      final row = result.first;
      return row['delta'] as String;
    } catch (e) {
      return null;
    }
  }

  bool savePageContent(sqlite.Database db, String newDelta) {
    try {
      final stmt = db.prepare('''
        UPDATE pages
        SET delta = ?, updated = CURRENT_TIMESTAMP
        WHERE id = ?
      ''');
      stmt.execute([newDelta, id]);
      stmt.close();
      return true;
    } catch (e) {
      return false;
    }
  }
}

class PageRow {
  final Page page;
  final int depth;
  final bool hasChildren;
  final bool isExpanded;
  final bool isSelected;

  PageRow({
    required this.page,
    required this.depth,
    required this.hasChildren,
    required this.isExpanded,
    required this.isSelected,
  });
}

class DatabaseService {
  final sqlite.Database db;

  DatabaseService._(this.db);

  static Future<DatabaseService> open({String? path}) async {
    final String dbPath;
    const envDbFile = String.fromEnvironment('SCRAWL_DBFILE');
    if (path != null) {
      dbPath = path;
    } else if (envDbFile != '') {
      dbPath = envDbFile;
    } else {
      final dir = await getApplicationSupportDirectory();
      dbPath = '${dir.path}/pages.db';
    }
    developer.log('Using database: $dbPath');
    final file = File(dbPath);
    await file.parent.create(recursive: true);
    final database = sqlite.sqlite3.open(file.path);
    final service = DatabaseService._(database);
    service._initDB();
    return service;
  }

  void _initDB() {
    try {
      db.execute('PRAGMA foreign_keys = ON;');

      db.execute('''
        CREATE TABLE IF NOT EXISTS pages (
          id INTEGER PRIMARY KEY AUTOINCREMENT,
          title TEXT NOT NULL,
          delta TEXT NOT NULL,
          created DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
          updated DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
        );
      ''');

      db.execute('''
        CREATE TABLE IF NOT EXISTS pagetree (
          parent_id INTEGER NOT NULL,
          child_id INTEGER NOT NULL UNIQUE,
          position INTEGER NOT NULL DEFAULT 0,
          PRIMARY KEY(parent_id, child_id),
          FOREIGN KEY(parent_id) REFERENCES pages(id) ON DELETE CASCADE,
          FOREIGN KEY(child_id) REFERENCES pages(id) ON DELETE CASCADE
        );
      ''');

      final isNew = db.select(
        "SELECT COUNT(*) as c FROM pages"
      ).first['c'] as int == 0;

      if (isNew) {
        const welcomeDelta = '{"ops":[{"insert":"Welcome to Scrawl!\\n"}]}';
        db.execute("INSERT INTO pages (id, title, delta) VALUES (0, '__root__', '{}')");
        db.execute(
          "INSERT INTO pages (title, delta) VALUES ('Welcome', ?)",
          [welcomeDelta],
        );
        final welcomeId = db.lastInsertRowId;
        db.execute(
          "INSERT INTO pagetree (parent_id, child_id, position) VALUES (0, ?, 0)",
          [welcomeId],
        );
      }
    } catch (e) {
      throw Exception('Database initialization failed: $e');
    }
  }

  void close() => db.close();
}


void main() async {
  WidgetsFlutterBinding.ensureInitialized();
  final db = await DatabaseService.open();
  runApp(MyApp(db: db));
}

class MyApp extends StatefulWidget {
  const MyApp({super.key, required this.db});
  final DatabaseService db;

  @override
  State<MyApp> createState() => _MyAppState();
}

class _MyAppState extends State<MyApp> {
  late final DatabaseService db;
  late final Notebook notebook;

  @override
  void initState() {
    super.initState();
    db = widget.db;
    notebook = Notebook(name: 'My Notebook', pages: []).loadNotebook(widget.db.db);
  }

  @override
  void dispose() {
    db.close();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      debugShowCheckedModeBanner: false,
      home: EditorWithSidebar(db: db, notebook: notebook),
    );
  }
}

class EditorWithSidebar extends StatefulWidget {
  final DatabaseService db;
  final Notebook notebook;

  const EditorWithSidebar({super.key, required this.db, required this.notebook});

  @override
  State<EditorWithSidebar> createState() => _EditorWithSidebarState();
}

class _EditorWithSidebarState extends State<EditorWithSidebar> {
  late Notebook notebook;
  Page? _currentPage;
  List<PageRow> _pageRows = [];
  final Map<int, bool> _expanded = {};
  final Map<int, GlobalKey> _rowKeys = {};
  QuillController? _editorController;
  final FocusNode _editorFocusNode = FocusNode();
  final ScrollController _editorScrollController = ScrollController();
  late final TextEditingController _titleController;
  Timer? _saveTimer;
  final Duration _saveDelay = const Duration(milliseconds: 500);
  Timer? _titleSaveTimer;
  bool _isSidebarHovered = false;
  final Set<int> _hoveredPageIds = {};
  StreamSubscription? _editorSubscription;

  bool _sidebarVisible = true;
  bool _sidebarInitialized = false;

  // Drag state
  int? _draggedPageId;
  int? _dragOverPageId;
  String? _dragPlacement; // 'above', 'below', 'child'

  @override
  void initState() {
    super.initState();
    _titleController = TextEditingController();
    notebook = widget.notebook;
    if (notebook.pages.isNotEmpty) {
      _selectPage(notebook.pages.first.id);
    }
    _titleController.addListener(_onTitleChanged);
  }

  @override
  void didChangeDependencies() {
    super.didChangeDependencies();
    if (!_sidebarInitialized) {
      _sidebarInitialized = true;
      final platform = Theme.of(context).platform;
      _sidebarVisible = platform != TargetPlatform.android &&
          platform != TargetPlatform.iOS;
    }
  }

  bool get _isMobile {
    final platform = Theme.of(context).platform;
    return platform == TargetPlatform.android || platform == TargetPlatform.iOS;
  }

  bool get _useOverlay {
    final isPortrait = MediaQuery.of(context).orientation == Orientation.portrait;
    return _isMobile && isPortrait;
  }

  GlobalKey _rowKeyFor(int id) =>
      _rowKeys.putIfAbsent(id, () => GlobalKey());

  QuillController controllerFromDelta(String deltaJson) {
    final decoded = jsonDecode(deltaJson) as Map<String, dynamic>;
    final ops = decoded['ops'] as List<dynamic>;
    final document = Document.fromJson(ops);
    return QuillController(
      document: document,
      selection: const TextSelection.collapsed(offset: 0),
    );
  }

  void _flushPendingSaves() {
    final page = _currentPage;
    if (page == null) return;

    if (_titleSaveTimer?.isActive ?? false) {
      _titleSaveTimer!.cancel();
      notebook.renamePage(widget.db.db, page.id, _titleController.text);
    }
    if (_saveTimer?.isActive ?? false) {
      _saveTimer!.cancel();
      final delta = jsonEncode({'ops': _editorController!.document.toDelta().toJson()});
      page.savePageContent(widget.db.db, delta);
    }
  }

  void _selectPage(int id) {
    _flushPendingSaves();
    _editorSubscription?.cancel();
    _editorController?.dispose();
    final page = notebook.findPage(id);
    if (page == null) return;

    setState(() {
      _currentPage = page;
      _editorController = controllerFromDelta(
        page.readPage(widget.db.db) ?? '{"ops":[{"insert":"\\n"}]}',
      );
      _titleController.text = page.title;
    });
    _updatePageRows();
    _editorSubscription = _editorController?.document.changes.listen((event) {
      _scheduleSave();
      _checkMathTrigger();
      _checkMarkdownTrigger();
    });
    WidgetsBinding.instance.addPostFrameCallback((_) {
      _editorFocusNode.requestFocus();
    });
  }

  void _updatePageRows() {
    setState(() {
      _pageRows = flattenPages();
    });
  }

  List<PageRow> flattenPages() {
    final List<PageRow> rows = [];
    void addPages(List<Page> pages, int depth) {
      for (final page in pages) {
        final hasChildren = page.children.isNotEmpty;
        final isExpanded = _expanded[page.id] ?? false;
        final isSelected = page.id == _currentPage?.id;
        rows.add(
          PageRow(
            page: page,
            depth: depth,
            hasChildren: hasChildren,
            isExpanded: isExpanded,
            isSelected: isSelected,
          ),
        );
        if (hasChildren && isExpanded) {
          addPages(page.children, depth + 1);
        }
      }
    }
    addPages(notebook.pages, 0);
    return rows;
  }

  void _scheduleSave() {
    if (_currentPage == null || _editorController == null) return;
    final page = _currentPage!;
    final controller = _editorController!;

    _saveTimer?.cancel();
    _saveTimer = Timer(_saveDelay, () {
      final deltaJson = jsonEncode({'ops': controller.document.toDelta().toJson()});
      page.savePageContent(widget.db.db, deltaJson);
    });
  }

  void _checkMarkdownTrigger() {
    final controller = _editorController;
    if (controller == null) return;

    final selection = controller.selection;
    if (!selection.isCollapsed) return;
    final index = selection.baseOffset;
    if (index < 1) return;

    final text = controller.document.toPlainText();
    if (index > text.length) return;

    final lineStart = text.lastIndexOf('\n', index - 1) + 1;
    final lineText = text.substring(lineStart, index);
    if (lineText.isEmpty) return;

    final lastChar = lineText[lineText.length - 1];
    if (!['*', ' ', '`', r'$'].contains(lastChar)) return;

    if (lineText.length >= 2 &&
        lineText[lineText.length - 2] == r'\' &&
        lineText[lineText.length - 1] == '*') {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        controller.replaceText(
          index - 2, 1, '',
          TextSelection.collapsed(offset: index - 1),
        );
      });
      return;
    }

    if (lineText.length >= 2 &&
        lineText[lineText.length - 2] == r'\' &&
        lineText[lineText.length - 1] == '`') {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        controller.replaceText(
          index - 2, 1, '',
          TextSelection.collapsed(offset: index - 1),
        );
      });
      return;
    }

    if (lineText.length >= 2 &&
        lineText[lineText.length - 2] == r'\' &&
        lineText[lineText.length - 1] == r'$') {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        controller.replaceText(
          index - 2, 1, '',
          TextSelection.collapsed(offset: index - 1),
        );
      });
      return;
    }

    if (lineText == '### ') {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        final c = _editorController;
        if (c == null) return;
        if (lineStart + 4 > c.document.length) return;
        c.replaceText(
          lineStart, 4, '',
          TextSelection.collapsed(offset: lineStart),
        );
        c.formatText(lineStart, 0, Attribute.h3);
      });
      return;
    }

    if (lineText == '## ') {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        final c = _editorController;
        if (c == null) return;
        if (lineStart + 3 > c.document.length) return;
        c.replaceText(
          lineStart, 3, '',
          TextSelection.collapsed(offset: lineStart),
        );
        c.formatText(lineStart, 0, Attribute.h2);
      });
      return;
    }

    if (lineText == '# ') {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        final c = _editorController;
        if (c == null) return;
        if (lineStart + 2 > c.document.length) return;
        c.replaceText(
          lineStart, 2, '',
          TextSelection.collapsed(offset: lineStart),
        );
        c.formatText(lineStart, 0, Attribute.h1);
      });
      return;
    }

    if (lineText == '* ') {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        final c = _editorController;
        if (c == null) return;
        if (lineStart + 2 > c.document.length) return;
        c.replaceText(
          lineStart, 2, '',
          TextSelection.collapsed(offset: lineStart),
        );
        c.formatText(lineStart, 0, Attribute.ul);
      });
      return;
    }

    if (lineText.length >= 3 && lineText[lineText.length - 1] == '*') {
      int i = lineText.length - 2;
      while (i >= 0 && lineText[i] != '*') {
        i--;
      }
      if (i >= 0 && (lineText.length - i - 2) > 0) {
        final contentLength = lineText.length - i - 2;
        final absStart = lineStart + i;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          final c = _editorController;
          if (c == null) return;
          if (index > c.document.length || absStart >= c.document.length) return;
          c.replaceText(index - 1, 1, '', TextSelection.collapsed(offset: index - 1));
          c.replaceText(absStart, 1, '', TextSelection.collapsed(offset: absStart));
          c.formatText(absStart, contentLength, Attribute.bold);
          final caret = (absStart + contentLength).clamp(0, c.document.length - 1);
          c.updateSelection(
            TextSelection.collapsed(offset: caret),
            ChangeSource.local,
          );
          c.formatText(
            caret,
            0,
            Attribute('bold', AttributeScope.inline, null),
          );
        });
        return;
      }
    }

    if (lineText.length >= 3 && lineText[lineText.length - 1] == '`') {
      int i = lineText.length - 2;
      while (i >= 0 && lineText[i] != '`') {
        i--;
      }
      if (i >= 0 && (lineText.length - i - 2) > 0) {
        final contentLength = lineText.length - i - 2;
        final absStart = lineStart + i;
        WidgetsBinding.instance.addPostFrameCallback((_) {
          final c = _editorController;
          if (c == null) return;
          if (index > c.document.length || absStart >= c.document.length) return;
          c.replaceText(index - 1, 1, '', TextSelection.collapsed(offset: index - 1));
          c.replaceText(absStart, 1, '', TextSelection.collapsed(offset: absStart));
          c.formatText(absStart, contentLength, Attribute.inlineCode);
          final caret = (absStart + contentLength).clamp(0, c.document.length - 1);
          c.updateSelection(
            TextSelection.collapsed(offset: caret),
            ChangeSource.local,
          );
          c.formatText(
            caret,
            0,
            Attribute('code', AttributeScope.inline, null),
          );
        });
        return;
      }
    }

    if (lastChar == '`' && lineText == '`') {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        final c = _editorController;
        if (c == null) return;
        if (lineStart + 1 > c.document.length) return;
        c.replaceText(
          lineStart, 1, '',
          TextSelection.collapsed(offset: lineStart),
        );
        c.formatText(lineStart, 0, Attribute.codeBlock);
      });
      return;
    }
  }

  void _checkMathTrigger() {
    final controller = _editorController;
    if (controller == null) return;

    final selection = controller.selection;
    if (!selection.isCollapsed) return;
    final index = selection.baseOffset;
    if (index < 1) return;

    final text = controller.document.toPlainText();
    if (index > text.length) return;

    final lineStart = text.lastIndexOf('\n', index - 1) + 1;
    final lineText = text.substring(lineStart, index);

    if (lineText == r'$') {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (_editorController == null) return;
        _editorController!.replaceText(
          lineStart,
          1,
          const MathBlockEmbed(''),
          TextSelection.collapsed(offset: lineStart + 1),
        );
      });
      return;
    }

    if (lineText.endsWith(r'$$')) {
      final embedStart = lineStart + lineText.length - 2;
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (_editorController == null) return;
        _editorController!.replaceText(
          embedStart,
          2,
          const MathInlineEmbed(''),
          TextSelection.collapsed(offset: embedStart + 1),
        );
      });
      return;
    }
  }

  void _renameCurrentPage(String newTitle) {
    if (_currentPage == null) return;
    if (newTitle.isEmpty) return;

    final success = notebook.renamePage(
      widget.db.db,
      _currentPage!.id,
      newTitle,
    );

    if (success) {
      setState(() {});
    }
  }

  void _onTitleChanged() {
    if (_currentPage != null) {
      setState(() {
        _currentPage!.title = _titleController.text;
      });
    }
    _titleSaveTimer?.cancel();
    _titleSaveTimer = Timer(
      const Duration(milliseconds: 300),
      () => _renameCurrentPage(_titleController.text),
    );
  }

  void _createPage({required int parentId}) {
    final newId = notebook.createPage(
      widget.db.db,
      'New Page',
      parentId: parentId,
    );
    if (newId == null) return;
    final page = notebook.findPage(newId);
    if (page == null) return;
    _selectPage(page.id);
  }

  void _deletePage(Page page) {
    final success = notebook.deletePage(widget.db.db, page.id);
    if (!success) return;

    setState(() {
      if (_currentPage?.id == page.id) {
        if (notebook.pages.isNotEmpty) {
          _selectPage(notebook.pages.first.id);
        } else {
          _currentPage = null;
          _editorController = null;
          _titleController.clear();
        }
      }
      _updatePageRows();
    });
  }

  void _setSidebarHovered(bool hover) {
    setState(() {
      _isSidebarHovered = hover;
    });
  }

  bool get _alwaysShowNewPageButton {
    return Theme.of(context).platform == TargetPlatform.android ||
          Theme.of(context).platform == TargetPlatform.iOS;
  }

  bool _isHovered(PageRow row) => _hoveredPageIds.contains(row.page.id);

  void _setHovered(PageRow row, bool hover) {
    setState(() {
      if (hover) {
        _hoveredPageIds.add(row.page.id);
      } else {
        _hoveredPageIds.remove(row.page.id);
      }
    });
  }

  Widget _buildDragFeedback(PageRow row) {
    return Material(
      elevation: 4,
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 8),
        color: Colors.grey[300],
        child: Text(
          row.page.title,
          style: const TextStyle(fontSize: 16),
        ),
      ),
    );
  }

  void _onDragStarted(PageRow row) {
    setState(() => _draggedPageId = row.page.id);
  }

  void _onDragEnded() {
    setState(() {
      _draggedPageId = null;
      _dragOverPageId = null;
      _dragPlacement = null;
    });
  }

  @override
  void dispose() {
    _editorFocusNode.dispose();
    _editorScrollController.dispose();
    _titleController.dispose();
    _titleSaveTimer?.cancel();
    _saveTimer?.cancel();
    _editorSubscription?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final useOverlay = _useOverlay;

    return Scaffold(
      body: SafeArea(
        child: useOverlay
            ? Column(
                children: [
                  if (_sidebarVisible)
                    Expanded(child: _buildSidebar(fullWidth: true)),
                  Expanded(child: _buildEditorPanel()),
                ],
              )
            : Row(
                children: [
                  if (_sidebarVisible) _buildSidebar(),
                  Expanded(child: _buildEditorPanel()),
                ],
              ),
      ),
    );
  }

  Widget _buildSidebar({bool fullWidth = false}) {
    return MouseRegion(
      onEnter: (_) => _setSidebarHovered(true),
      onExit: (_) => _setSidebarHovered(false),
      child: Container(
        width: fullWidth ? double.infinity : 300,
        color: Colors.grey[200],
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            Padding(
              padding: const EdgeInsets.all(16),
              child: Row(
                children: [
                  const Expanded(
                    child: Text(
                      'Pages',
                      style: TextStyle(
                        fontSize: 30,
                        fontWeight: FontWeight.bold,
                        color: Colors.black,
                      ),
                    ),
                  ),
                  IconButton(
                    icon: const Icon(Icons.close),
                    tooltip: 'Hide sidebar',
                    onPressed: () => setState(() => _sidebarVisible = false),
                  ),
                ],
              ),
            ),
            const Divider(height: 1),
            Expanded(
              child: ListView.builder(
                padding: const EdgeInsets.only(right: 8),
                itemCount: _pageRows.length + 1,
                itemBuilder: (context, index) {
                  if (index == _pageRows.length) return _buildNewPageButton();
                  return _buildPageRow(_pageRows[index]);
                },
              ),
            ),
          ],
        ),
      ),
    );
  }

  Widget _buildNewPageButton() {
    final showButton = _alwaysShowNewPageButton || _isSidebarHovered;

    return Padding(
      padding: const EdgeInsets.only(left: 12, top: 8, bottom: 8),
      child: Row(
        children: [
          if (showButton) ...[
            IconButton(
              icon: const Icon(Icons.add_circle_outline),
              iconSize: 40,
              color: Colors.black,
              tooltip: 'New page',
              onPressed: () => _createPage(parentId: 0),
            ),
          ],
        ],
      ),
    );
  }

  Widget _buildPageRow(PageRow row) {
    const double indentPerLevel = 26.0;
    final rowKey = _rowKeyFor(row.page.id);

    final isDragOver = _dragOverPageId == row.page.id;
    final borderTop = isDragOver && _dragPlacement == 'above';
    final borderBottom = isDragOver && _dragPlacement == 'below';
    final highlight = isDragOver && _dragPlacement == 'child';

    return DragTarget<int>(
      onWillAcceptWithDetails: (details) {
        return details.data != row.page.id;
      },
      onMove: (details) {
        final renderBox =
            rowKey.currentContext?.findRenderObject() as RenderBox?;
        if (renderBox == null) return;
        final rowHeight = renderBox.size.height;
        final localY = renderBox.globalToLocal(details.offset).dy;
        final edge = rowHeight * 0.25;
        final String placement;
        if (localY < edge) {
          placement = 'above';
        } else if (localY > rowHeight - edge) {
          placement = 'below';
        } else {
          placement = 'child';
        }
        setState(() {
          _dragOverPageId = row.page.id;
          _dragPlacement = placement;
        });
      },
      onLeave: (_) {
        setState(() {
          if (_dragOverPageId == row.page.id) {
            _dragOverPageId = null;
            _dragPlacement = null;
          }
        });
      },
      onAcceptWithDetails: (details) {
        final draggedId = details.data;
        final targetId = row.page.id;
        final placement = _dragPlacement ?? 'below';
        setState(() {
          _dragOverPageId = null;
          _dragPlacement = null;
          _draggedPageId = null;
        });
        final success = notebook.movePage(widget.db.db, draggedId, targetId, placement);
        if (success) {
          setState(() { _updatePageRows(); });
        }
      },
      builder: (context, candidateData, rejectedData) {
        return Container(
          key: rowKey,
          decoration: BoxDecoration(
            color: highlight ? Colors.blue.withOpacity(0.1) : null,
            border: Border(
              top: borderTop
                  ? const BorderSide(color: Colors.blue, width: 2)
                  : BorderSide.none,
              bottom: borderBottom
                  ? const BorderSide(color: Colors.blue, width: 2)
                  : BorderSide.none,
            ),
          ),
          child: LongPressDraggable<int>(
            data: row.page.id,
            delay: const Duration(milliseconds: 200),
            onDragStarted: () => _onDragStarted(row),
            onDragEnd: (_) => _onDragEnded(),
            feedback: _buildDragFeedback(row),
            childWhenDragging: Opacity(
              opacity: 0.4,
              child: _buildPageRowContent(row, indentPerLevel),
            ),
            child: _buildPageRowContent(row, indentPerLevel),
          ),
        );
      },
    );
  }

  Widget _buildPageRowContent(PageRow row, double indentPerLevel) {
    return MouseRegion(
      onEnter: (_) => _setHovered(row, true),
      onExit: (_) => _setHovered(row, false),
      child: Padding(
        padding: EdgeInsets.only(left: row.depth * indentPerLevel),
        child: Row(
          children: [
            row.hasChildren
                ? IconButton(
                    iconSize: 26,
                    padding: EdgeInsets.zero,
                    constraints: const BoxConstraints(),
                    icon: Icon(
                      row.isExpanded
                          ? Icons.expand_more
                          : Icons.chevron_right,
                    ),
                    onPressed: () {
                      setState(() {
                        _expanded[row.page.id] = !row.isExpanded;
                        _updatePageRows();
                      });
                    },
                  )
                : SizedBox(width: indentPerLevel),
            const SizedBox(width: 4),

            Expanded(
              child: InkWell(
                onTap: () {
                  if (row.isSelected && row.hasChildren) {
                    setState(() {
                      _expanded[row.page.id] = !row.isExpanded;
                      _updatePageRows();
                    });
                  } else {
                    _selectPage(row.page.id);
                  }
                },
                child: Padding(
                  padding: const EdgeInsets.symmetric(vertical: 8),
                  child: Text(
                    row.page.title,
                    style: TextStyle(
                      fontSize: 24,
                      fontWeight:
                          row.isSelected ? FontWeight.bold : FontWeight.normal,
                    ),
                    overflow: TextOverflow.ellipsis,
                    softWrap: false,
                  ),
                ),
              ),
            ),

            if (_isHovered(row) || (_isMobile && row.isSelected)) ...[
              if (!_isMobile) ...[  
                Draggable<int>(
                  data: row.page.id,
                  onDragStarted: () => _onDragStarted(row),
                  onDragEnd: (_) => _onDragEnded(),
                  feedback: _buildDragFeedback(row),
                  child: const MouseRegion(
                    cursor: SystemMouseCursors.grab,
                    child: Icon(Icons.drag_indicator, size: 26),
                  ),
                ),
                const SizedBox(width: 8),
              ],
              IconButton(
                iconSize: 26,
                padding: EdgeInsets.zero,
                constraints: const BoxConstraints(),
                icon: const Icon(Icons.add),
                tooltip: 'Add child page',
                onPressed: () {
                  _createPage(parentId: row.page.id);
                  setState(() {
                    _expanded[row.page.id] = true;
                    _updatePageRows();
                  });
                },
              ),
              const SizedBox(width: 8),
              IconButton(
                iconSize: 26,
                padding: EdgeInsets.zero,
                constraints: const BoxConstraints(),
                icon: const Icon(Icons.delete_outline),
                tooltip: 'Delete page',
                onPressed: () => _deletePage(row.page),
              ),
              const SizedBox(width: 8),
            ],
          ],
        ),
      ),
    );
  }

  Widget _buildEditorPanel() {
    return Column(
      children: [
        Padding(
          padding: const EdgeInsets.all(16),
          child: Row(
            children: [
              if (!_sidebarVisible)
                IconButton(
                  icon: const Icon(Icons.menu),
                  tooltip: 'Show sidebar',
                  onPressed: () => setState(() => _sidebarVisible = !_sidebarVisible),
                ),
              Expanded(
                child: TextField(
                  controller: _titleController,
                  textAlign: TextAlign.center,
                  decoration: const InputDecoration(
                    border: InputBorder.none,
                    hintText: 'Enter page title...',
                  ),
                  style: const TextStyle(
                    fontSize: 30,
                    fontWeight: FontWeight.bold,
                  ),
                ),
              ),
              if (!_sidebarVisible) const SizedBox(width: 48),
            ],
          ),
        ),
        Expanded(
          child: _editorController == null
            ? const Center(
                child: Text(
                  'Select a page',
                  style: TextStyle(color: Colors.grey),
                ),
              )
            : QuillEditor(
                controller: _editorController!,
                focusNode: _editorFocusNode,
                scrollController: _editorScrollController,
                config: QuillEditorConfig(
                  placeholder: 'Start writing...',
                  padding: const EdgeInsets.all(16),
                  expands: true,
                  autoFocus: false,
                  embedBuilders: [
                    ...FlutterQuillEmbeds.editorBuilders(),
                    const MathInlineEmbedBuilder(),
                    const MathBlockEmbedBuilder(),
                  ],
                  customStyles: DefaultStyles(
                    paragraph: DefaultTextBlockStyle(
                      const TextStyle(fontSize: 24, color: Colors.black),
                      const HorizontalSpacing(0, 0),
                      const VerticalSpacing(0, 0),
                      const VerticalSpacing(1, 1),
                      null,
                    ),
                    inlineCode: InlineCodeStyle(
                      backgroundColor: const Color(0xFFF0F0F0),
                      radius: const Radius.circular(4),
                      style: const TextStyle(
                        fontFamily: 'monospace',
                        fontSize: 24,
                        color: Colors.black,
                      ),
                    ),
                    code: DefaultTextBlockStyle(
                      const TextStyle(
                        fontFamily: 'monospace',
                        fontSize: 24,
                        color: Colors.black,
                      ),
                      const HorizontalSpacing(0, 0),
                      const VerticalSpacing(8, 8),
                      const VerticalSpacing(0, 0),
                      BoxDecoration(
                        color: const Color(0xFFF0F0F0),
                        borderRadius: BorderRadius.circular(4),
                      ),
                    ),
                  ),
                ),
              ),
        ),
      ],
    );
  }
}
