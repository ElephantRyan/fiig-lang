package dev.fiig.intellij

import com.intellij.lang.Language
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
}

/** ADR 0090 §2: the `.fiig` file type. */
object FiigFileType : LanguageFileType(FiigLanguage) {
    override fun getName(): String = "fiig"
    override fun getDescription(): String = "fiig source"
    override fun getDefaultExtension(): String = "fiig"
    override fun getIcon(): Icon = FiigIcons.FILE
}
