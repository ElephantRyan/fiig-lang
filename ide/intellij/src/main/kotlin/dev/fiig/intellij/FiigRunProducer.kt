package dev.fiig.intellij

import com.intellij.execution.actions.ConfigurationContext
import com.intellij.execution.actions.LazyRunConfigurationProducer
import com.intellij.execution.configurations.ConfigurationFactory
import com.intellij.execution.configurations.ConfigurationTypeUtil
import com.intellij.execution.lineMarker.ExecutorAction
import com.intellij.execution.lineMarker.RunLineMarkerContributor
import com.intellij.icons.AllIcons
import com.intellij.openapi.util.Ref
import com.intellij.psi.PsiElement
import com.intellij.psi.util.elementType
import java.nio.file.Files
import java.nio.file.Path

/**
 * ADR 0090 §4: what the gutter offers for a declaration: Run on a workflow,
 * Serve on an `adapter http`, Check on any other file's declaration.
 */
object FiigRunTargets {
    data class Target(val command: FiigCommand, val decl: FiigDeclaration)

    fun at(element: PsiElement): Target? {
        val decl = FiigDeclarations.of(element) ?: return null
        val file = element.containingFile ?: return null
        if (FiigDeclarations.first(file)?.keyword != element) return null
        return when {
            decl.kind == "workflow" -> Target(FiigCommand.RUN, decl)
            FiigDeclarations.isHttpAdapter(decl) -> Target(FiigCommand.SERVE, decl)
            else -> Target(FiigCommand.CHECK, decl)
        }
    }
}

class FiigRunLineMarkerContributor : RunLineMarkerContributor() {
    override fun getInfo(element: PsiElement): Info? {
        if (element.elementType != FiigTokens.KEYWORD) return null
        val target = FiigRunTargets.at(element) ?: return null
        val icon = if (target.command == FiigCommand.CHECK) AllIcons.Actions.Checked else AllIcons.RunConfigurations.TestState.Run
        return Info(icon, ExecutorAction.getActions(0)) { "${target.command.title} ${target.decl.name}" }
    }
}

class FiigRunConfigurationProducer : LazyRunConfigurationProducer<FiigRunConfiguration>() {
    override fun getConfigurationFactory(): ConfigurationFactory =
        ConfigurationTypeUtil.findConfigurationType(FiigRunConfigurationType::class.java).configurationFactories[0]

    private fun target(context: ConfigurationContext): Pair<FiigRunTargets.Target, Path>? {
        val element = context.psiLocation ?: return null
        val target = FiigRunTargets.at(element) ?: return null
        val path = element.containingFile?.virtualFile?.toNioPath() ?: return null
        return target to path
    }

    override fun setupConfigurationFromContext(configuration: FiigRunConfiguration, context: ConfigurationContext, sourceElement: Ref<PsiElement>): Boolean {
        val (target, file) = target(context) ?: return false
        val pkg = FiigDeclarations.packageDir(file)
        val o = configuration.options
        o.command = target.command.name
        o.packageDir = pkg.toString()
        o.target = if (target.command == FiigCommand.CHECK) "" else target.decl.name
        o.workspace = context.project.basePath ?: ""
        o.capsFile = pkg.resolve("caps.json").takeIf { Files.isRegularFile(it) && target.command != FiigCommand.CHECK }?.toString() ?: ""
        configuration.name = when (target.command) {
            FiigCommand.CHECK -> "Check ${pkg.fileName}"
            else -> "${target.command.title} ${target.decl.name}"
        }
        return true
    }

    override fun isConfigurationFromContext(configuration: FiigRunConfiguration, context: ConfigurationContext): Boolean {
        val (target, file) = target(context) ?: return false
        val o = configuration.options
        val wantTarget = if (target.command == FiigCommand.CHECK) "" else target.decl.name
        return configuration.command == target.command &&
            o.packageDir == FiigDeclarations.packageDir(file).toString() &&
            (o.target ?: "") == wantTarget
    }
}
