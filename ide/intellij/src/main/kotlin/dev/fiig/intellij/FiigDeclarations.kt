package dev.fiig.intellij

import com.intellij.psi.PsiElement
import com.intellij.psi.PsiFile
import com.intellij.psi.util.elementType
import com.intellij.psi.util.siblings
import java.nio.file.Files
import java.nio.file.Path

/** A file's public declaration, read from its tokens (ADR 0090 §2's flat tree). */
data class FiigDeclaration(val kind: String, val name: String, val keyword: PsiElement)

object FiigDeclarations {
    val KINDS = setOf(
        "model", "enum", "rule", "conversion", "activity", "gateway", "workflow",
        "adapter", "config", "capability", "interface", "formulas",
    )

    /** The file's first declaration: its kind keyword at the start of a line, and the name after it. */
    fun first(file: PsiFile): FiigDeclaration? {
        val start = file.firstChild ?: return null
        var depth = 0
        for (leaf in start.siblings()) {
            when (leaf.elementType) {
                FiigTokens.LBRACE -> depth++
                FiigTokens.RBRACE -> depth--
                FiigTokens.KEYWORD -> if (depth == 0 && leaf.text in KINDS) return of(leaf)
            }
        }
        return null
    }

    /** The declaration whose kind keyword is [keyword]. */
    fun of(keyword: PsiElement): FiigDeclaration? {
        if (keyword.elementType != FiigTokens.KEYWORD || keyword.text !in KINDS) return null
        for (leaf in keyword.nextSibling?.siblings() ?: return null) {
            when (leaf.elementType) {
                FiigTokens.WHITE_SPACE, FiigTokens.LINE_COMMENT -> continue
                // `adapter http name`, `adapter cli name`
                FiigTokens.KEYWORD -> if (keyword.text == "adapter" && leaf.text in setOf("http", "cli")) continue else return null
                FiigTokens.IDENTIFIER, FiigTokens.TYPE_NAME -> return FiigDeclaration(keyword.text, leaf.text, keyword)
                else -> return null
            }
        }
        return null
    }

    /** Whether an adapter declaration is `adapter http`. */
    fun isHttpAdapter(decl: FiigDeclaration): Boolean {
        if (decl.kind != "adapter") return false
        val next = decl.keyword.nextSibling?.siblings()?.firstOrNull { it.elementType != FiigTokens.WHITE_SPACE }
        return next?.text == "http"
    }

    /** The package a file belongs to: the nearest directory with fiig.pkg.json, else the file's own. */
    fun packageDir(file: Path): Path {
        var dir: Path? = file.parent
        while (dir != null) {
            if (Files.isRegularFile(dir.resolve("fiig.pkg.json"))) return dir
            dir = dir.parent
        }
        return file.parent
    }
}
