package dev.fiig.intellij

import com.intellij.extapi.psi.ASTWrapperPsiElement
import com.intellij.extapi.psi.PsiFileBase
import com.intellij.lang.ASTNode
import com.intellij.lang.ParserDefinition
import com.intellij.lang.PsiBuilder
import com.intellij.lang.PsiParser
import com.intellij.lexer.Lexer
import com.intellij.openapi.fileTypes.FileType
import com.intellij.openapi.project.Project
import com.intellij.psi.FileViewProvider
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiFile
import com.intellij.psi.tree.IElementType
import com.intellij.psi.tree.IFileElementType
import com.intellij.psi.tree.TokenSet

class FiigFile(viewProvider: FileViewProvider) : PsiFileBase(viewProvider, FiigLanguage) {
    override fun getFileType(): FileType = FiigFileType
    override fun toString(): String = "fiig file"
}

/**
 * ADR 0090 §2: a flat tree, one leaf per token under the file. It exists so
 * run markers and templates can find a declaration's keyword and name;
 * names are resolved only by `fiig lsp`.
 */
class FiigParserDefinition : ParserDefinition {
    override fun createLexer(project: Project?): Lexer = FiigLexer()

    override fun createParser(project: Project?): PsiParser = PsiParser { root: IElementType, builder: PsiBuilder ->
        val marker = builder.mark()
        while (!builder.eof()) builder.advanceLexer()
        marker.done(root)
        builder.treeBuilt
    }

    override fun getFileNodeType(): IFileElementType = FILE
    override fun getWhitespaceTokens(): TokenSet = FiigTokens.WHITESPACES
    override fun getCommentTokens(): TokenSet = FiigTokens.COMMENTS
    override fun getStringLiteralElements(): TokenSet = FiigTokens.STRINGS
    override fun createElement(node: ASTNode): PsiElement = ASTWrapperPsiElement(node)
    override fun createFile(viewProvider: FileViewProvider): PsiFile = FiigFile(viewProvider)

    companion object {
        @JvmField
        val FILE = IFileElementType(FiigLanguage)
    }
}
