package dev.fiig.intellij

import com.intellij.ide.actions.CreateFileFromTemplateAction
import com.intellij.ide.actions.CreateFileFromTemplateDialog
import com.intellij.ide.fileTemplates.FileTemplate
import com.intellij.ide.fileTemplates.FileTemplateManager
import com.intellij.ide.fileTemplates.FileTemplateUtil
import com.intellij.openapi.project.Project
import com.intellij.psi.PsiDirectory
import com.intellij.psi.PsiFile

/**
 * ADR 0090 §5: New | fiig File. One declaration per file (ADR 0054), the
 * file named from the declaration as the corpus names them: `getCustomer`
 * in get_customer.fiig.
 */
class FiigNewFileAction : CreateFileFromTemplateAction("fiig File", "Create a fiig declaration in its own file", FiigIcons.FILE) {
    override fun buildDialog(project: Project, directory: PsiDirectory, builder: CreateFileFromTemplateDialog.Builder) {
        builder.setTitle("New fiig File")
        for (kind in FiigNaming.KINDS) {
            builder.addKind(kind.replaceFirstChar { it.uppercase() }, FiigIcons.FILE, FiigNaming.template(kind))
        }
    }

    override fun getActionName(directory: PsiDirectory, newName: String, templateName: String): String = "Create fiig file $newName"

    override fun createFileFromTemplate(name: String, template: FileTemplate, dir: PsiDirectory): PsiFile? {
        val kind = FiigNaming.kindOf(template.name) ?: return super.createFileFromTemplate(name, template, dir)
        val decl = FiigNaming.declarationName(kind, name)
        val props = FileTemplateManager.getInstance(dir.project).defaultProperties
        props["DECL_NAME"] = decl
        return FileTemplateUtil.createFromTemplate(template, FiigNaming.fileName(decl), props, dir).containingFile
    }
}

object FiigNaming {
    val KINDS = listOf(
        "model", "enum", "rule", "conversion", "activity", "gateway", "workflow",
        "adapter", "config", "capability", "interface", "formulas",
    )

    private val CAPITALIZED = setOf("model", "enum", "config", "capability", "interface")

    fun template(kind: String): String = "fiig " + kind.replaceFirstChar { it.uppercase() }

    fun kindOf(templateName: String): String? = KINDS.firstOrNull { template(it) == templateName }

    /** The declaration's name from what the user typed: `get customer`, `get_customer`, or `getCustomer`. */
    fun declarationName(kind: String, typed: String): String {
        val words = typed.trim()
            .replace(Regex("([a-z0-9])([A-Z])"), "$1 $2")
            .split(Regex("[^A-Za-z0-9]+"))
            .filter { it.isNotEmpty() }
            .map { it.lowercase() }
        if (words.isEmpty()) return typed.trim()
        val camel = words.first() + words.drop(1).joinToString("") { it.replaceFirstChar(Char::uppercase) }
        return if (kind in CAPITALIZED) camel.replaceFirstChar(Char::uppercase) else camel
    }

    /** `getCustomer` -> get_customer.fiig. */
    fun fileName(declaration: String): String =
        declaration.replace(Regex("([a-z0-9])([A-Z])"), "$1_$2").lowercase() + ".fiig"
}
