package dev.fiig.intellij

import com.intellij.lang.BracePair
import com.intellij.lang.Commenter
import com.intellij.lang.PairedBraceMatcher
import com.intellij.lang.folding.FoldingBuilderEx
import com.intellij.lang.folding.FoldingDescriptor
import com.intellij.lexer.Lexer
import com.intellij.openapi.editor.Document
import com.intellij.openapi.util.TextRange
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiFile
import com.intellij.psi.impl.search.IndexPatternBuilder
import com.intellij.psi.tree.IElementType
import com.intellij.psi.tree.TokenSet
import com.intellij.psi.util.elementType
import com.intellij.psi.util.siblings

/** ADR 0090 §2: `//` comments. */
class FiigCommenter : Commenter {
    override fun getLineCommentPrefix(): String = "//"
    override fun getBlockCommentPrefix(): String? = null
    override fun getBlockCommentSuffix(): String? = null
    override fun getCommentedBlockCommentPrefix(): String? = null
    override fun getCommentedBlockCommentSuffix(): String? = null
}

/** ADR 0090 §2: braces, parentheses, brackets, and `${ }`. */
class FiigBraceMatcher : PairedBraceMatcher {
    override fun getPairs(): Array<BracePair> = PAIRS
    override fun isPairedBracesAllowedBeforeType(lbraceType: IElementType, contextType: IElementType?): Boolean = true
    override fun getCodeConstructStart(file: PsiFile?, openingBraceOffset: Int): Int = openingBraceOffset

    private companion object {
        val PAIRS = arrayOf(
            BracePair(FiigTokens.LBRACE, FiigTokens.RBRACE, true),
            BracePair(FiigTokens.LPAREN, FiigTokens.RPAREN, false),
            BracePair(FiigTokens.LBRACKET, FiigTokens.RBRACKET, false),
            BracePair(FiigTokens.INTERP_START, FiigTokens.INTERP_END, false),
        )
    }
}

/** ADR 0090 §2: fold `{ }` blocks over several lines, and runs of comment lines. */
class FiigFoldingBuilder : FoldingBuilderEx() {
    override fun buildFoldRegions(root: PsiElement, document: Document, quick: Boolean): Array<FoldingDescriptor> {
        val out = ArrayList<FoldingDescriptor>()
        val open = ArrayDeque<PsiElement>()
        var commentRun: MutableList<PsiElement> = ArrayList()
        fun endCommentRun() {
            if (commentRun.size >= 2) {
                val range = TextRange(commentRun.first().textRange.startOffset, commentRun.last().textRange.endOffset)
                out.add(FoldingDescriptor(commentRun.first().node, range))
            }
            commentRun = ArrayList()
        }
        val first = root.firstChild ?: return emptyArray()
        for (leaf in first.siblings()) {
            when (leaf.elementType) {
                FiigTokens.LINE_COMMENT -> {
                    val last = commentRun.lastOrNull()
                    if (last != null && document.getLineNumber(leaf.textRange.startOffset) != document.getLineNumber(last.textRange.startOffset) + 1) {
                        endCommentRun()
                    }
                    commentRun.add(leaf)
                    continue
                }
                FiigTokens.WHITE_SPACE -> continue
                FiigTokens.LBRACE -> open.addLast(leaf)
                FiigTokens.RBRACE -> open.removeLastOrNull()?.let { lbrace ->
                    val range = TextRange(lbrace.textRange.startOffset, leaf.textRange.endOffset)
                    if (document.getLineNumber(range.startOffset) != document.getLineNumber(range.endOffset)) {
                        out.add(FoldingDescriptor(lbrace.node, range))
                    }
                }
            }
            endCommentRun()
        }
        endCommentRun()
        return out.toTypedArray()
    }

    override fun getPlaceholderText(node: com.intellij.lang.ASTNode): String =
        if (node.elementType == FiigTokens.LINE_COMMENT) "// ..." else "{...}"

    override fun isCollapsedByDefault(node: com.intellij.lang.ASTNode): Boolean = false
}

/** ADR 0090 §2: TODO and FIXME in comments. */
class FiigIndexPatternBuilder : IndexPatternBuilder {
    override fun getIndexingLexer(file: PsiFile): Lexer? = if (file is FiigFile) FiigLexer() else null
    override fun getCommentTokenSet(file: PsiFile): TokenSet? = if (file is FiigFile) FiigTokens.COMMENTS else null
    override fun getCommentStartDelta(tokenType: IElementType?): Int = if (tokenType == FiigTokens.LINE_COMMENT) 2 else 0
    override fun getCommentEndDelta(tokenType: IElementType?): Int = 0
}
