package dev.fiig.intellij

import com.intellij.openapi.options.BoundConfigurable
import com.intellij.openapi.project.ProjectManager
import com.intellij.openapi.ui.DialogPanel
import com.intellij.platform.lsp.api.LspClientManager
import com.intellij.ui.dsl.builder.AlignX
import com.intellij.ui.dsl.builder.bindText
import com.intellij.ui.dsl.builder.panel
import javax.swing.JLabel

/** ADR 0090 §3: Settings | Languages & Frameworks | fiig. */
class FiigConfigurable : BoundConfigurable("fiig") {
    private val versionLabel = JLabel()

    override fun createPanel(): DialogPanel {
        val settings = FiigSettings.get().state
        refreshVersion(settings.binaryPath)
        return panel {
            row("fiig binary:") {
                textField()
                    .bindText(settings::binaryPath)
                    .align(AlignX.FILL)
                    .comment("Leave empty to use <code>fiig</code> from PATH.")
                    .onChanged { refreshVersion(it.text) }
            }
            row("Found:") { cell(versionLabel) }
        }
    }

    private fun refreshVersion(path: String) {
        val binary = FiigBinary.find(path)
        versionLabel.text = if (binary == null) "no fiig binary found" else "$binary — ${FiigBinary.version(binary)}"
    }

    override fun apply() {
        super.apply()
        // The language server starts again with the new binary.
        for (project in ProjectManager.getInstance().openProjects) {
            LspClientManager.getInstance(project).stopAndRestartClientsIfNeeded(FiigLspIntegrationProvider::class.java)
        }
    }
}
