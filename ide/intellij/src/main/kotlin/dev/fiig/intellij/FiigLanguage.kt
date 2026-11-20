package dev.fiig.intellij

import com.intellij.ide.IconProvider
import com.intellij.lang.Language
import com.intellij.psi.PsiElement
import com.intellij.psi.PsiFile
import com.intellij.openapi.fileTypes.LanguageFileType
import com.intellij.openapi.util.IconLoader
import javax.swing.Icon

object FiigLanguage : Language("fiig") {
    private fun readResolve(): Any = FiigLanguage
    override fun getDisplayName(): String = "fiig"
}

object FiigIcons {
    @JvmField
    val FILE: Icon = IconLoader.getIcon("/icons/fiig.svg", FiigIcons::class.java)

    /**
     * An icon per declaration kind, grouped by role: data (blue circle),
     * pure logic (green square), effects (orange diamond), the edge of the
     * program (purple hexagon), and tests (outlined check).
     */
    private val BY_KIND: Map<String, Icon> =
        (FiigNaming.KINDS + "test").associateWith { IconLoader.getIcon("/icons/kinds/$it.svg", FiigIcons::class.java) }

    fun forKind(kind: String): Icon = BY_KIND[kind] ?: FILE
}

/** The Project view and editor tabs show a file's declaration kind. */
class FiigIconProvider : IconProvider() {
    override fun getIcon(element: PsiElement, flags: Int): Icon? {
        if (element !is PsiFile || element.fileType != FiigFileType) return null
        return FiigDeclarations.iconKind(element)?.let(FiigIcons::forKind)
    }
}

/** ADR 0090 §2: the `.fiig` file type. */
object FiigFileType : LanguageFileType(FiigLanguage) {
    override fun getName(): String = "fiig"
    override fun getDescription(): String = "fiig source"
    override fun getDefaultExtension(): String = "fiig"
    override fun getIcon(): Icon = FiigIcons.FILE
}
