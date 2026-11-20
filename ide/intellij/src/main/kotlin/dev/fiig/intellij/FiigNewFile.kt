package dev.fiig.intellij

import com.intellij.ide.actions.CreateFileFromTemplateAction
import com.intellij.ide.actions.CreateFileFromTemplateDialog
import com.intellij.ide.fileTemplates.FileTemplate
import com.intellij.ide.fileTemplates.FileTemplateManager
import com.intellij.ide.fileTemplates.FileTemplateUtil
import com.intellij.openapi.ui.InputValidatorEx
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
        // ADR 0108: an application declares nothing in src/ itself, so a file
        // made there must name its folder (`orders/getOrder`).
        val manifest = directory.parentDirectory?.findFile("fiig.pkg.json")?.text
        builder.setValidator(object : InputValidatorEx {
            override fun getErrorText(inputString: String): String? =
                FiigNaming.topLevelProblem(directory.name, manifest, inputString)

            override fun checkInput(inputString: String): Boolean = getErrorText(inputString) == null
            override fun canClose(inputString: String): Boolean = checkInput(inputString)
        })
        for (kind in FiigNaming.KINDS) {
            builder.addKind(kind.replaceFirstChar { it.uppercase() }, FiigIcons.forKind(kind),FiigNaming.template(kind))
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

    private val APPLICATION = Regex("\"kind\"\\s*:\\s*\"application\"")

    /**
     * Why a file typed as [typed] can't be made in a folder named [dirName],
     * or null. An application's `src/` holds only folders (ADR 0108), so a
     * name made directly there must start with one: `orders/getOrder`.
     * [manifest] is the `fiig.pkg.json` text beside the folder, if any.
     */
    fun topLevelProblem(dirName: String, manifest: String?, typed: String): String? {
        if (dirName != "src" || manifest == null || !APPLICATION.containsMatchIn(manifest)) return null
        if (typed.isBlank() || '/' in typed || '\\' in typed) return null
        return "An application's declarations live in a folder, one per domain (ADR 0108): type the folder first, as orders/$typed"
    }

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

    /** ADR 0104: a file is named after its declaration, `getCustomer` -> GetCustomer.fiig. */
    fun fileName(declaration: String): String =
        declaration.replaceFirstChar(Char::uppercase) + ".fiig"
}
