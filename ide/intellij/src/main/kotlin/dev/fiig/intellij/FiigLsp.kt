package dev.fiig.intellij

import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.notification.NotificationAction
import com.intellij.notification.NotificationGroupManager
import com.intellij.notification.NotificationType
import com.intellij.openapi.editor.colors.TextAttributesKey
import com.intellij.openapi.options.ShowSettingsUtil
import com.intellij.openapi.project.Project
import com.intellij.openapi.util.Key
import com.intellij.openapi.vfs.VirtualFile
import com.intellij.platform.lsp.api.LspIntegrationProvider
import com.intellij.platform.lsp.api.ProjectWideLspClientDescriptor
import com.intellij.platform.lsp.api.customization.LspCustomization
import com.intellij.platform.lsp.api.customization.LspFormattingCustomizer
import com.intellij.platform.lsp.api.customization.LspFormattingSupport
import com.intellij.platform.lsp.api.customization.LspSemanticTokensCustomizer
import com.intellij.platform.lsp.api.customization.LspSemanticTokensSupport

/** ADR 0090 §3: one `fiig lsp` per project with a `.fiig` file. */
class FiigLspIntegrationProvider : LspIntegrationProvider {
    override fun fileOpened(project: Project, file: VirtualFile, clientStarter: LspIntegrationProvider.LspClientStarter) {
        if (file.extension != "fiig") return
        val binary = FiigBinary.find()
        if (binary == null) {
            notifyMissing(project)
            return
        }
        clientStarter.ensureClientStarted(FiigLspServerDescriptor(project, binary))
    }

    private fun notifyMissing(project: Project) {
        if (project.getUserData(NOTIFIED) == true) return
        project.putUserData(NOTIFIED, true)
        NotificationGroupManager.getInstance().getNotificationGroup("fiig")
            .createNotification(
                "fiig binary not found",
                "Errors, navigation, and formatting need <code>fiig lsp</code>. Set the binary's path, or put <code>fiig</code> on PATH.",
                NotificationType.WARNING,
            )
            .addAction(NotificationAction.createSimpleExpiring("Open settings") {
                ShowSettingsUtil.getInstance().showSettingsDialog(project, FiigConfigurable::class.java)
            })
            .notify(project)
    }

    private companion object {
        val NOTIFIED = Key.create<Boolean>("fiig.binary.notified")
    }
}

class FiigLspServerDescriptor(project: Project, private val binary: String) : ProjectWideLspClientDescriptor(project, "fiig") {
    override fun isSupportedFile(file: VirtualFile): Boolean = file.extension == "fiig"

    override fun createCommandLine(): GeneralCommandLine =
        GeneralCommandLine(binary, "lsp").withWorkDirectory(project.basePath)

    // Reformat Code, and Actions on Save, format with `fiig fmt` (ADR 0089 §7).
    override val lspCustomization: LspCustomization = object : LspCustomization() {
        override val formattingCustomizer: LspFormattingCustomizer = object : LspFormattingSupport() {
            override fun shouldFormatThisFileExclusivelyByServer(file: VirtualFile, ideCanFormatThisFileItself: Boolean, serverExplicitlyWantsToFormatThisFile: Boolean): Boolean =
                file.extension == "fiig"
        }

        // ADR 0106 §1: each call coloured by its callee's kind.
        override val semanticTokensCustomizer: LspSemanticTokensCustomizer = object : LspSemanticTokensSupport() {
            override fun getTextAttributesKey(tokenType: String, modifiers: List<String>): TextAttributesKey? =
                FiigColors.BY_SEMANTIC_TYPE[tokenType]
        }
    }
}
