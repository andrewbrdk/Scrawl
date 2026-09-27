import 'package:flutter/material.dart';
import 'package:flutter_math_fork/flutter_math.dart';
import 'package:flutter_quill/flutter_quill.dart';

class MathInlineEmbed extends Embeddable {
  static const String mathInlineType = 'math-inline';
  const MathInlineEmbed(String latex) : super(mathInlineType, latex);
}

class MathBlockEmbed extends Embeddable {
  static const String mathBlockType = 'math-block';
  const MathBlockEmbed(String latex) : super(mathBlockType, latex);
}

class MathInlineEmbedBuilder extends EmbedBuilder {
  const MathInlineEmbedBuilder();

  @override
  String get key => MathInlineEmbed.mathInlineType;

  @override
  bool get expanded => false;

  @override
  Widget build(BuildContext context, EmbedContext embedContext) {
    final latex = embedContext.node.value.data as String;
    return _MathEmbedView(
      latex: latex,
      isBlock: false,
      embedContext: embedContext,
    );
  }
}

class MathBlockEmbedBuilder extends EmbedBuilder {
  const MathBlockEmbedBuilder();

  @override
  String get key => MathBlockEmbed.mathBlockType;

  @override
  bool get expanded => true;

  @override
  Widget build(BuildContext context, EmbedContext embedContext) {
    final latex = embedContext.node.value.data as String;
    return _MathEmbedView(
      latex: latex,
      isBlock: true,
      embedContext: embedContext,
    );
  }
}

/// Static (non-focusable) preview of a math embed. Tapping it opens a modal
/// dialog to edit the LaTeX source, so the embed never has to compete with
/// the surrounding QuillEditor for keyboard focus/caret.
class _MathEmbedView extends StatefulWidget {
  final String latex;
  final bool isBlock;
  final EmbedContext embedContext;

  const _MathEmbedView({
    required this.latex,
    required this.isBlock,
    required this.embedContext,
  });

  @override
  State<_MathEmbedView> createState() => _MathEmbedViewState();
}

class _MathEmbedViewState extends State<_MathEmbedView> {
  @override
  void initState() {
    super.initState();
    if (widget.latex.isEmpty) {
      WidgetsBinding.instance.addPostFrameCallback((_) {
        if (mounted) _openEditor();
      });
    }
  }

  Future<void> _openEditor() async {
    final result = await showDialog<String>(
      context: context,
      builder: (context) => _MathEditDialog(initialLatex: widget.latex),
    );
    if (result == null) return; // cancelled

    final offset = _findNodeOffset(widget.embedContext.node);
    if (offset == null) return;

    final embed =
        widget.isBlock ? MathBlockEmbed(result) : MathInlineEmbed(result);
    widget.embedContext.controller.replaceText(
      offset,
      1,
      embed,
      TextSelection.collapsed(offset: offset + 1),
    );
  }

  @override
  Widget build(BuildContext context) {
    if (widget.latex.isEmpty) {
      return GestureDetector(
        onTap: _openEditor,
        child: Container(
          padding: const EdgeInsets.symmetric(horizontal: 4, vertical: 2),
          decoration: BoxDecoration(
            border: Border.all(color: Colors.blue),
            borderRadius: BorderRadius.circular(4),
          ),
          child: const Text(
            'math',
            style: TextStyle(color: Colors.grey, fontSize: 14),
          ),
        ),
      );
    }

    final math = Math.tex(
      widget.latex,
      mathStyle: widget.isBlock ? MathStyle.display : MathStyle.text,
      textStyle: TextStyle(fontSize: widget.isBlock ? 20 : 16),
      onErrorFallback: (_) => Text(
        widget.latex,
        style: const TextStyle(color: Colors.red, fontSize: 16),
      ),
    );

    if (widget.isBlock) {
      return GestureDetector(
        onTap: _openEditor,
        child: Container(
          width: double.infinity,
          margin: const EdgeInsets.symmetric(vertical: 6),
          padding: const EdgeInsets.symmetric(vertical: 12, horizontal: 16),
          decoration: BoxDecoration(
            color: Colors.grey[100],
            borderRadius: BorderRadius.circular(8),
            border: Border.all(color: Colors.grey.shade300),
          ),
          child: SingleChildScrollView(
            scrollDirection: Axis.horizontal,
            child: math,
          ),
        ),
      );
    }

    return GestureDetector(onTap: _openEditor, child: math);
  }
}

class _MathEditDialog extends StatefulWidget {
  final String initialLatex;

  const _MathEditDialog({required this.initialLatex});

  @override
  State<_MathEditDialog> createState() => _MathEditDialogState();
}

class _MathEditDialogState extends State<_MathEditDialog> {
  late final TextEditingController _controller =
      TextEditingController(text: widget.initialLatex);

  void _submit() => Navigator.of(context).pop(_controller.text);

  @override
  void dispose() {
    _controller.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Edit Math'),
      content: SizedBox(
        width: 400,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            AnimatedBuilder(
              animation: _controller,
              builder: (context, _) {
                final latex = _controller.text;
                if (latex.isEmpty) return const SizedBox.shrink();
                return Padding(
                  padding: const EdgeInsets.only(bottom: 12),
                  child: SingleChildScrollView(
                    scrollDirection: Axis.horizontal,
                    child: Math.tex(
                      latex,
                      mathStyle: MathStyle.display,
                      textStyle: const TextStyle(fontSize: 20),
                      onErrorFallback: (_) => Text(
                        latex,
                        style: const TextStyle(color: Colors.red, fontSize: 16),
                      ),
                    ),
                  ),
                );
              },
            ),
            TextField(
              controller: _controller,
              autofocus: true,
              maxLines: null,
              onSubmitted: (_) => _submit(),
              decoration: const InputDecoration(
                border: OutlineInputBorder(),
                hintText: r'Enter LaTeX...',
              ),
              style: const TextStyle(fontFamily: 'monospace', fontSize: 14),
            ),
          ],
        ),
      ),
      actions: [
        TextButton(
          onPressed: () => Navigator.of(context).pop(),
          child: const Text('Cancel'),
        ),
        FilledButton(
          onPressed: _submit,
          child: const Text('Done'),
        ),
      ],
    );
  }
}

// ── Shared helper ────────────────────────────────────────────────────────

/// Returns the document offset of [node] using the node's own
/// documentOffset property, which flutter_quill exposes on all nodes.
/// Returns null if the property is unavailable.
int? _findNodeOffset(Node node) {
  try {
    return node.documentOffset;
  } catch (_) {
    return null;
  }
}
